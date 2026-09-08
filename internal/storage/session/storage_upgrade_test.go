package session

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/platform/pawpath"
)

type upgradeFileState struct {
	Mode    fs.FileMode
	ModTime time.Time
	Data    string
}

func upgradeTree(t *testing.T, root string) map[string]upgradeFileState {
	t.Helper()
	out := make(map[string]upgradeFileState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		state := upgradeFileState{Mode: info.Mode(), ModTime: info.ModTime()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state.Data = string(data)
		}
		out[rel] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func upgradeWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func upgradeStore(t *testing.T, root string) *JSONLStore {
	t.Helper()
	s, err := NewJSONLStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func upgradeWorkspace(t *testing.T) (*JSONLStore, *JSONLStore, *JSONLStore, string) {
	t.Helper()
	home, configHome := t.TempDir(), t.TempDir()
	workspace := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PAW_CONFIG_HOME", configHome)
	s, err := NewJSONLStoreForWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot, err := pawpath.ProjectDirInHome(filepath.Join(home, ".paw"), workspace)
	if err != nil {
		t.Fatal(err)
	}
	return s, upgradeStore(t, oldRoot), upgradeStore(t, filepath.Join(workspace, ".paw")), workspace
}

func upgradeSeed(t *testing.T, s *JSONLStore, id, content string) {
	t.Helper()
	ctx := context.Background()
	createTestSession(t, s, id, []message.Message{testMessage(content)})
	if err := s.AppendTurnMetadata(ctx, id, TurnMetadata{TurnID: content, Status: TurnStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteActorSnapshot(ctx, id, 1, json.RawMessage(`{"source":"`+content+`"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestStorageUpgradeMergesAuxiliariesAndLeavesLegacyUntouched(t *testing.T) {
	for _, operation := range []string{"append", "touch"} {
		t.Run(operation, func(t *testing.T) {
			s, _, local, _ := upgradeWorkspace(t)
			const id = "resume"
			upgradeSeed(t, local, id, "legacy")
			upgradeWrite(t, filepath.Join(local.sessionDir(id), "compactions", "old.json"), "old compaction")
			upgradeWrite(t, filepath.Join(local.sessionDir(id), "ariadne", "shared.json"), "old shared")
			upgradeWrite(t, filepath.Join(s.sessionDir(id), "compactions", "new.json"), "new compaction")
			upgradeWrite(t, filepath.Join(s.sessionDir(id), "ariadne", "shared.json"), "global shared")
			oldTime := time.Unix(1000, 0)
			if err := filepath.WalkDir(local.Root(), func(path string, _ fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				return os.Chtimes(path, oldTime, oldTime)
			}); err != nil {
				t.Fatal(err)
			}
			before := upgradeTree(t, local.Root())
			s.nowFn = func() time.Time { return time.Unix(2000, 0) }
			ctx := context.Background()
			var err error
			if operation == "touch" {
				err = s.TouchSession(ctx, id)
			} else {
				err = s.Append(ctx, id, testMessage("continued"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := upgradeTree(t, local.Root()); !reflect.DeepEqual(got, before) {
				t.Fatal("legacy files or directory mtimes changed")
			}
			info, err := os.Stat(s.metaPath(id))
			if err != nil {
				t.Fatal(err)
			}
			if !info.ModTime().Equal(s.nowFn()) {
				t.Fatalf("new meta mtime = %v", info.ModTime())
			}
			for name, want := range map[string]string{"compactions/old.json": "old compaction", "compactions/new.json": "new compaction", "ariadne/shared.json": "global shared"} {
				data, err := os.ReadFile(filepath.Join(s.sessionDir(id), name))
				if err != nil || string(data) != want {
					t.Fatalf("%s = %q, %v", name, data, err)
				}
			}
			history, err := s.LoadResolvedHistory(ctx, id)
			wantLen := 1
			if operation == "append" {
				wantLen++
			}
			if err != nil || len(history) != wantLen || history[0].Content != "legacy" {
				t.Fatalf("history = %+v, %v", history, err)
			}
			turns, err := s.LoadTurnMetadata(ctx, id)
			if err != nil || len(turns) != 1 || turns[0].TurnID != "legacy" {
				t.Fatalf("turns = %+v, %v", turns, err)
			}
			snap, ok, err := s.ReadActorSnapshot(ctx, id)
			if err != nil || !ok || string(snap.State) != `{"source":"legacy"}` {
				t.Fatalf("snapshot = %+v, %v, %v", snap, ok, err)
			}
		})
	}
}

func TestStorageUpgradePublishesMetaLastAndRecovers(t *testing.T) {
	s, _, local, workspace := upgradeWorkspace(t)
	const id = "retry"
	upgradeSeed(t, local, id, "legacy")
	before := upgradeTree(t, local.Root())
	// A directory at a file destination must not be mistaken for a published file.
	if err := os.MkdirAll(s.turnMetadataPath(id), 0o755); err != nil {
		t.Fatal(err)
	}
	ok, err := s.ensureWritableSession(id)
	if err == nil || ok {
		t.Fatalf("migration = %v, %v; want failure", ok, err)
	}
	if _, err := os.Stat(s.metaPath(id)); !os.IsNotExist(err) {
		t.Fatalf("meta published before sidecars: %v", err)
	}
	history, err := s.LoadResolvedHistory(context.Background(), id)
	if err != nil || len(history) != 1 || history[0].Content != "legacy" {
		t.Fatalf("fallback after failure = %+v, %v", history, err)
	}
	if err := os.Remove(s.turnMetadataPath(id)); err != nil {
		t.Fatal(err)
	}
	// Restart, without any in-memory migration state.
	s, err = NewJSONLStoreForWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(context.Background(), id, testMessage("continued")); err != nil {
		t.Fatal(err)
	}
	history, err = s.LoadResolvedHistory(context.Background(), id)
	if err != nil || len(history) != 2 {
		t.Fatalf("retry history = %+v, %v", history, err)
	}
	if got := upgradeTree(t, local.Root()); !reflect.DeepEqual(got, before) {
		t.Fatal("legacy changed during retry")
	}
}

func TestStorageUpgradeSyncFailureNeverCommitsPartialSession(t *testing.T) {
	for _, failAt := range []string{"transcript", "meta", "meta-directory"} {
		t.Run(failAt, func(t *testing.T) {
			s, _, local, workspace := upgradeWorkspace(t)
			const id = "sync-failure"
			upgradeSeed(t, local, id, "legacy")
			before := upgradeTree(t, local.Root())
			injected := errors.New("injected migration sync failure")
			s.syncFile = func(f *os.File) error {
				name := filepath.Base(f.Name())
				fail := failAt == "transcript" && strings.HasPrefix(name, ".migrate-transcript.jsonl-") ||
					failAt == "meta" && strings.HasPrefix(name, ".migrate-meta.json-")
				if failAt == "meta-directory" && f.Name() == s.sessionDir(id) {
					_, err := os.Stat(s.metaPath(id))
					fail = err == nil
				}
				if fail {
					return injected
				}
				return f.Sync()
			}
			ok, err := s.ensureWritableSession(id)
			if ok || !errors.Is(err, injected) {
				t.Fatalf("migration = %v, %v", ok, err)
			}
			if _, err := os.Stat(s.metaPath(id)); !os.IsNotExist(err) {
				t.Fatalf("meta visible after failure: %v", err)
			}
			s, err = NewJSONLStoreForWorkspace(workspace)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Append(context.Background(), id, testMessage("retry")); err != nil {
				t.Fatal(err)
			}
			history, err := s.LoadResolvedHistory(context.Background(), id)
			if err != nil || len(history) != 2 || history[0].Content != "legacy" {
				t.Fatalf("retry history = %+v, %v", history, err)
			}
			if !reflect.DeepEqual(upgradeTree(t, local.Root()), before) {
				t.Fatal("sync failure changed legacy")
			}
		})
	}
}

func TestStorageUpgradePreservesPublishedGlobalFiles(t *testing.T) {
	for _, hasMeta := range []bool{false, true} {
		t.Run(map[bool]string{false: "transcript-only", true: "complete"}[hasMeta], func(t *testing.T) {
			s, _, local, _ := upgradeWorkspace(t)
			const id = "same"
			upgradeSeed(t, local, id, "legacy")
			global := upgradeStore(t, s.Root())
			upgradeSeed(t, global, id, "global")
			if !hasMeta {
				if err := os.Remove(global.metaPath(id)); err != nil {
					t.Fatal(err)
				}
			}
			before := upgradeTree(t, global.sessionDir(id))
			ok, err := s.ensureWritableSession(id)
			if err != nil || !ok {
				t.Fatalf("migration = %v, %v", ok, err)
			}
			after := upgradeTree(t, global.sessionDir(id))
			for name, state := range before {
				if name != "." && after[name] != state {
					t.Fatalf("overwrote global %s", name)
				}
			}
		})
	}
}

func TestStorageUpgradePublicationDoesNotReplaceConcurrentWinner(t *testing.T) {
	for _, name := range []string{"meta.json", "transcript.jsonl"} {
		t.Run(name, func(t *testing.T) {
			s, _, local, _ := upgradeWorkspace(t)
			const id = "winner"
			upgradeSeed(t, local, id, "legacy")
			winner := upgradeStore(t, t.TempDir())
			upgradeSeed(t, winner, id, "winner")
			data, err := os.ReadFile(filepath.Join(winner.sessionDir(id), name))
			if err != nil {
				t.Fatal(err)
			}
			var published bool
			s.syncFile = func(f *os.File) error {
				if strings.HasPrefix(filepath.Base(f.Name()), ".migrate-"+name+"-") {
					if _, err := os.Stat(s.metaPath(id)); !os.IsNotExist(err) {
						t.Errorf("meta visible before commit: %v", err)
					}
					if name == "meta.json" {
						for _, payload := range []string{"transcript.jsonl", "turns.jsonl", sessionActorSnapshotFile} {
							if _, err := os.Stat(filepath.Join(s.sessionDir(id), payload)); err != nil {
								t.Error(err)
							}
						}
					}
					upgradeWrite(t, filepath.Join(s.sessionDir(id), name), string(data))
					published = true
				}
				return f.Sync()
			}
			ok, err := s.ensureWritableSession(id)
			if !ok || err != nil || !published {
				t.Fatalf("migration = %v, %v; concurrent publication = %v", ok, err, published)
			}
			got, err := os.ReadFile(filepath.Join(s.sessionDir(id), name))
			if err != nil || string(got) != string(data) {
				t.Fatalf("overwrote concurrent %s: %v", name, err)
			}
		})
	}
}

func TestStorageUpgradeRollbackPreservesReplacedMeta(t *testing.T) {
	s, _, local, _ := upgradeWorkspace(t)
	const id = "replacement"
	upgradeSeed(t, local, id, "legacy")
	winner := upgradeStore(t, t.TempDir())
	upgradeSeed(t, winner, id, "winner")
	data, err := os.ReadFile(winner.metaPath(id))
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("directory sync failed after concurrent replacement")
	replaced := false
	s.syncFile = func(f *os.File) error {
		if f.Name() == s.sessionDir(id) && !replaced {
			if _, err := os.Stat(s.metaPath(id)); err == nil {
				if err := os.Rename(winner.metaPath(id), s.metaPath(id)); err != nil {
					t.Fatal(err)
				}
				replaced = true
				return injected
			}
		}
		return f.Sync()
	}
	if ok, err := s.ensureWritableSession(id); ok || !errors.Is(err, injected) {
		t.Fatalf("migration = %v, %v", ok, err)
	}
	got, err := os.ReadFile(s.metaPath(id))
	if err != nil || string(got) != string(data) {
		t.Fatalf("rollback removed another writer's metadata: %v", err)
	}
}

func TestStorageUpgradeRejectsCorruptMetaWithoutPublishing(t *testing.T) {
	s, old, local, _ := upgradeWorkspace(t)
	const id = "corrupt"
	upgradeSeed(t, old, id, "old")
	upgradeSeed(t, local, id, "local")
	upgradeWrite(t, old.metaPath(id), `{broken`)
	before := upgradeTree(t, old.Root())
	if err := s.TouchSession(context.Background(), id); err == nil {
		t.Fatal("touch accepted corrupt high-priority metadata")
	}
	if ok, err := s.ensureWritableSession(id); ok || err == nil {
		t.Fatalf("migration = %v, %v", ok, err)
	}
	if _, err := os.Stat(s.sessionDir(id)); !os.IsNotExist(err) {
		t.Fatalf("published corrupt session: %v", err)
	}
	if !reflect.DeepEqual(upgradeTree(t, old.Root()), before) {
		t.Fatal("corrupt source mutated")
	}
}

func TestStorageUpgradeConfigHomeReadPriorityAndIsolation(t *testing.T) {
	s, old, local, workspace := upgradeWorkspace(t)
	ctx := context.Background()
	global := upgradeStore(t, s.Root())
	for _, source := range []struct {
		s     *JSONLStore
		label string
	}{{global, "new"}, {old, "old"}, {local, "local"}} {
		upgradeSeed(t, source.s, source.label, source.label)
		upgradeSeed(t, source.s, "shared", source.label)
		upgradeWrite(t, source.s.keyIndexPath("shared-key"), source.label)
		upgradeWrite(t, source.s.keyIndexPath(source.label+"-key"), source.label)
		upgradeWrite(t, filepath.Join(source.s.Root(), "attachments", "shared.png"), source.label)
		upgradeWrite(t, filepath.Join(source.s.Root(), "attachments", source.label+".png"), source.label)
	}
	upgradeSeed(t, old, "old-over-local", "old")
	upgradeSeed(t, local, "old-over-local", "local")
	upgradeWrite(t, old.keyIndexPath("old-over-local-key"), "old")
	upgradeWrite(t, local.keyIndexPath("old-over-local-key"), "local")
	upgradeWrite(t, filepath.Join(old.Root(), "attachments", "old-over-local.png"), "old")
	upgradeWrite(t, filepath.Join(local.Root(), "attachments", "old-over-local.png"), "local")
	upgradeSeed(t, old, "legacy-task", "task")
	upgradeWrite(t, filepath.Join(old.Root(), "tasks", "legacy-task", "meta.json"), `{}`)

	otherWorkspace := filepath.Join(t.TempDir(), filepath.Base(workspace))
	otherRoot, err := pawpath.ProjectDirInHome(filepath.Join(os.Getenv("HOME"), ".paw"), otherWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	other := upgradeStore(t, otherRoot)
	upgradeSeed(t, other, "foreign", "foreign")
	upgradeWrite(t, other.keyIndexPath("foreign-key"), "foreign")
	upgradeWrite(t, filepath.Join(other.Root(), "attachments", "foreign.png"), "foreign")
	beforeOld, beforeLocal, beforeOther := upgradeTree(t, old.Root()), upgradeTree(t, local.Root()), upgradeTree(t, other.Root())

	for _, tc := range []struct {
		id, content string
		source      *JSONLStore
	}{{"shared", "new", global}, {"new", "new", global}, {"old", "old", old}, {"local", "local", local}, {"old-over-local", "old", old}} {
		t.Run(tc.id, func(t *testing.T) {
			meta, err := s.GetMeta(ctx, tc.id)
			if err != nil || meta.SessionID != tc.id {
				t.Fatalf("meta = %+v, %v", meta, err)
			}
			if ok, err := s.Exists(ctx, tc.id); err != nil || !ok {
				t.Fatalf("exists = %v, %v", ok, err)
			}
			history, err := s.LoadResolvedHistory(ctx, tc.id)
			if err != nil || len(history) != 1 || history[0].Content != tc.content {
				t.Fatalf("history = %+v, %v", history, err)
			}
			envs, torn, err := s.LoadEnvelopes(ctx, tc.id)
			if err != nil || torn || len(envs) != 1 {
				t.Fatalf("envelopes = %d, %v, %v", len(envs), torn, err)
			}
			if got := s.TranscriptPath(tc.id); got != tc.source.transcriptPath(tc.id) {
				t.Fatalf("transcript path = %s", got)
			}
			if got := s.TurnMetadataPath(tc.id); got != tc.source.turnMetadataPath(tc.id) {
				t.Fatalf("turn metadata path = %s", got)
			}
			turns, err := s.LoadTurnMetadata(ctx, tc.id)
			if err != nil || len(turns) != 1 || turns[0].TurnID != tc.content {
				t.Fatalf("turns = %+v, %v", turns, err)
			}
			snap, ok, err := s.ReadActorSnapshot(ctx, tc.id)
			if err != nil || !ok || string(snap.State) != `{"source":"`+tc.content+`"}` {
				t.Fatalf("snapshot = %+v, %v, %v", snap, ok, err)
			}
			key := tc.id + "-key"
			got, err := s.OpenOrCreate(ctx, key)
			if err != nil || got != tc.content {
				t.Fatalf("index = %q, %v", got, err)
			}
			mime, data, err := s.ReadAttachment(ctx, "attachments/"+tc.id+".png")
			if err != nil || mime != "image/png" || string(data) != tc.content {
				t.Fatalf("attachment = %q, %q, %v", mime, data, err)
			}
		})
	}
	list, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]string)
	for _, summary := range list {
		ids[summary.SessionID] = summary.FirstMessage
	}
	want := map[string]string{"shared": "new", "new": "new", "old": "old", "local": "local", "old-over-local": "old"}
	if len(list) != len(want) || !reflect.DeepEqual(ids, want) {
		t.Fatalf("list = %+v", list)
	}
	if ok, err := s.Exists(ctx, "foreign"); err != nil || ok {
		t.Fatalf("foreign exists = %v, %v", ok, err)
	}
	if _, err := s.GetMeta(ctx, "foreign"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("foreign meta: %v", err)
	}
	if _, _, err := s.ReadAttachment(ctx, "attachments/foreign.png"); err == nil {
		t.Fatal("read another workspace's attachment")
	}
	if got, err := s.readKeyIndex("foreign-key"); err != nil || got != "" {
		t.Fatalf("foreign index = %q, %v", got, err)
	}
	if !reflect.DeepEqual(upgradeTree(t, old.Root()), beforeOld) || !reflect.DeepEqual(upgradeTree(t, local.Root()), beforeLocal) || !reflect.DeepEqual(upgradeTree(t, other.Root()), beforeOther) {
		t.Fatal("read-only fallback changed a legacy tree")
	}
}

func TestStorageUpgradeDefaultGlobalMigratesOnEveryWriteEntry(t *testing.T) {
	for _, operation := range []string{"append", "touch", "turn", "snapshot", "envelope"} {
		t.Run(operation, func(t *testing.T) {
			s, old, local, _ := upgradeWorkspace(t)
			const id = "upgrade"
			upgradeSeed(t, old, id, "old")
			upgradeSeed(t, local, id, "local")
			beforeOld, beforeLocal := upgradeTree(t, old.Root()), upgradeTree(t, local.Root())
			ctx := context.Background()
			var err error
			switch operation {
			case "append":
				err = s.Append(ctx, id, testMessage("new"))
			case "touch":
				err = s.TouchSession(ctx, id)
			case "turn":
				err = s.AppendTurnMetadata(ctx, id, TurnMetadata{TurnID: "new"})
			case "snapshot":
				err = s.WriteActorSnapshot(ctx, id, 1, json.RawMessage(`{}`))
			case "envelope":
				envs, _, loadErr := old.LoadEnvelopes(ctx, id)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				_, _, err = s.AppendEnvelopes(ctx, id, envs)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(s.metaPath(id)); err != nil {
				t.Fatal(err)
			}
			history, err := s.LoadResolvedHistory(ctx, id)
			if err != nil || len(history) == 0 || history[0].Content != "old" {
				t.Fatalf("migrated history = %+v, %v", history, err)
			}
			if !reflect.DeepEqual(upgradeTree(t, old.Root()), beforeOld) || !reflect.DeepEqual(upgradeTree(t, local.Root()), beforeLocal) {
				t.Fatal("write mutated legacy roots")
			}
		})
	}
}
