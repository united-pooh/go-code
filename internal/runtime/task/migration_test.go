package task

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"paw/internal/storage/es"
	"paw/internal/storage/session"
)

func writeTaskMigrationJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedTaskMigrationActor(t *testing.T, base, kind, key, mode string, running, completed TaskSnapshot) {
	t.Helper()
	store, err := es.NewJSONLStore(base, kind)
	if err != nil {
		t.Fatal(err)
	}
	state := func(task TaskSnapshot) any {
		if kind == taskRegistryActorType {
			return taskRegistryState{Tasks: map[string]TaskSnapshot{task.ID: task}}
		}
		return taskActorState{Task: task, Found: true}
	}
	event := func(task TaskSnapshot) es.Envelope {
		var payload any = taskActorMutation{Event: taskEventForStatus(task.Status), Task: task}
		typ := taskEventForStatus(task.Status)
		if kind == taskRegistryActorType {
			payload, typ = taskRegistryUpdate{Task: task}, taskRegistryUpdated
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return es.Envelope{Type: typ, Kind: es.KindDomain, Payload: raw}
	}
	ctx := context.Background()
	if mode != "snapshot" {
		if _, _, err := store.Append(ctx, key, []es.Envelope{event(running), event(completed)}); err != nil {
			t.Fatal(err)
		}
	}
	if mode != "journal" {
		snapshot := completed
		if mode == "snapshot-tail" {
			snapshot = running
		}
		raw, err := json.Marshal(state(snapshot))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.WriteSnapshot(ctx, key, 1, raw); err != nil {
			t.Fatal(err)
		}
	}
}

type taskMigrationFile struct {
	Data    string
	Mode    fs.FileMode
	ModTime time.Time
}

func taskMigrationTree(t *testing.T, root string, freeze bool) map[string]taskMigrationFile {
	t.Helper()
	files := make(map[string]taskMigrationFile)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if freeze {
			stamp := time.Unix(1234567890, 0)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				return err
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		var data []byte
		if !entry.IsDir() {
			data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = taskMigrationFile{Data: string(data), Mode: info.Mode(), ModTime: info.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestTaskMigrationLegacyActorsPrecedeMetadataAndOrphans(t *testing.T) {
	for _, mode := range []string{"journal", "snapshot", "snapshot-tail"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PAW_CONFIG_HOME", t.TempDir())
			root := t.TempDir()
			registry := newTaskRegistry(root)
			legacy := filepath.Join(root, ".paw")
			base := filepath.Join(legacy, "actors")
			now := time.Now().UTC()
			exit := 0
			want := make(map[string]TaskSnapshot)
			for _, id := range []string{"actor-only", "stale-legacy-meta", "stale-global-meta", "registry-only"} {
				running := TaskSnapshot{ID: id, SessionID: id, ParentSessionID: "parent", Status: TaskRunning, StartedAt: now, OutputPath: filepath.Join(legacy, "tasks", id, "output.json")}
				completed := running
				completed.Status, completed.FinishedAt, completed.ExitCode = TaskCompleted, &now, &exit
				completed.Content, completed.UsedTokens = "authoritative result", 17
				want[id] = completed
				if id == "registry-only" {
					seedTaskMigrationActor(t, base, taskRegistryActorType, taskRegistryActorKey, mode, running, completed)
				} else {
					seedTaskMigrationActor(t, base, taskActorType, id, mode, running, completed)
				}
				if id == "stale-legacy-meta" {
					writeTaskMigrationJSON(t, filepath.Join(legacy, "tasks", id, "meta.json"), running)
				}
				if id == "stale-global-meta" {
					if err := registry.saveTask(context.Background(), running); err != nil {
						t.Fatal(err)
					}
				}
				writeTaskMigrationJSON(t, running.OutputPath, WorkerResult{TaskID: id, Content: completed.Content})
			}
			before := taskMigrationTree(t, legacy, true)
			for restart := 0; restart < 2; restart++ {
				manager := NewManager(Config{Root: root})
				for id, expected := range want {
					expected.OutputPath = registry.outputPath(id)
					got, ok := manager.Status(id)
					if !ok || !reflect.DeepEqual(got, expected) {
						t.Errorf("restart %d Status(%s) = %#v, %v; want %#v", restart, id, got, ok, expected)
					}
					data, err := os.ReadFile(registry.outputPath(id))
					if err != nil || !strings.Contains(string(data), expected.Content) {
						t.Errorf("migrated output %s = %s, %v", id, data, err)
					}
				}
				if tasks := manager.ListTasks(); len(tasks) != len(want) {
					t.Errorf("ListTasks = %#v, want %d tasks", tasks, len(want))
				}
				if tokens := manager.TotalTaskTokens("parent"); tokens != 68 {
					t.Errorf("TotalTaskTokens = %d, want 68", tokens)
				}
				if err := manager.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if after := taskMigrationTree(t, legacy, false); !reflect.DeepEqual(after, before) {
				t.Error("legacy tree contents, paths, permissions or mtimes changed")
			}
		})
	}
}

func TestTaskMigrationCorruptMetadataDoesNotDisableManager(t *testing.T) {
	for _, mode := range []string{"journal", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PAW_CONFIG_HOME", t.TempDir())
			root := t.TempDir()
			registry := newTaskRegistry(root)
			ctx := context.Background()
			completed := TaskSnapshot{ID: "recoverable", SessionID: "recoverable", Status: TaskCompleted, Content: "actor result"}
			seedTaskMigrationActor(t, filepath.Join(registry.projectDir, "actors"), taskActorType, completed.ID, mode, completed, completed)
			for _, id := range []string{"aaa-corrupt", completed.ID} {
				writeTaskMigrationJSON(t, registry.metaPath(id), completed)
				if err := os.WriteFile(registry.metaPath(id), []byte("{broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			legacyPath := filepath.Join(root, ".paw", "tasks", "bad-legacy", "meta.json")
			writeTaskMigrationJSON(t, legacyPath, "not a task")
			before := taskMigrationTree(t, filepath.Join(root, ".paw"), true)
			if err := registry.saveTask(ctx, TaskSnapshot{ID: "zzz-healthy", Status: TaskRunning}); err != nil {
				t.Fatal(err)
			}
			store, err := session.NewJSONLStore(registry.projectDir)
			if err != nil {
				t.Fatal(err)
			}
			notifier := newFakeNotifier()
			manager := NewManager(Config{Root: root, Store: store, Launcher: immediateLauncher{}, Notifier: notifier})
			defer manager.Close()
			if err := manager.storageError(); err != nil {
				t.Errorf("individual corrupt metadata disabled manager: %v", err)
			}
			if got, ok := manager.Status(completed.ID); !ok || got.Status != TaskCompleted || got.Content != completed.Content {
				t.Errorf("actor recovery = %#v, %v", got, ok)
			}
			if projection, ok, err := registry.loadTask(ctx, completed.ID); err != nil || !ok || projection.Status != TaskCompleted {
				t.Errorf("repaired projection = %#v, %v, %v", projection, ok, err)
			}
			if got, ok := manager.Status("zzz-healthy"); !ok || got.Status != TaskInterrupted {
				t.Errorf("unrelated orphan was not recovered: %#v, %v", got, ok)
			}
			if result, err := manager.WaitAny(ctx, []string{completed.ID}, 0); err != nil || len(result.Tasks) != 1 || result.Tasks[0].Status != TaskCompleted {
				t.Errorf("WaitAny = %#v, %v", result, err)
			}
			if _, err := manager.Run(ctx, Request{Prompt: "unrelated new task"}); err != nil {
				t.Errorf("new task blocked by corrupt metadata: %v", err)
			}
			notifier.mu.Lock()
			var diagnostics string
			for _, event := range notifier.events {
				diagnostics += event.Body
			}
			notifier.mu.Unlock()
			for _, id := range []string{"aaa-corrupt", "bad-legacy"} {
				if !strings.Contains(diagnostics, id) {
					t.Errorf("missing per-item diagnostic for %s: %s", id, diagnostics)
				}
			}
			manager.Close()
			if after := taskMigrationTree(t, filepath.Join(root, ".paw"), false); !reflect.DeepEqual(after, before) {
				t.Error("legacy tree changed during corrupt metadata recovery")
			}
		})
	}
}

func TestTaskRegistryListTasksRetainsHealthyItemsOnCorruptMetadata(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	registry := newTaskRegistry(t.TempDir())
	writeTaskMigrationJSON(t, registry.metaPath("aaa-corrupt"), "bad metadata")
	if err := registry.saveTask(context.Background(), TaskSnapshot{ID: "zzz-healthy", Status: TaskCompleted}); err != nil {
		t.Fatal(err)
	}
	tasks, err := registry.listTasks(context.Background())
	if err == nil || !strings.Contains(err.Error(), "aaa-corrupt") || len(tasks) != 1 || tasks[0].ID != "zzz-healthy" {
		t.Fatalf("listTasks = %#v, %v; want healthy task and scoped error", tasks, err)
	}
}

func TestTaskMigrationLegacyRuntimeMessagesAreNotExecuted(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	registry := newTaskRegistry(root)
	base := filepath.Join(root, ".paw", "actors")
	completed := TaskSnapshot{ID: "runtime-tail", SessionID: "runtime-tail", Status: TaskCompleted, Content: "durable fact"}
	running := completed
	running.Status, running.Content = TaskRunning, "pending command"
	seedTaskMigrationActor(t, base, taskActorType, completed.ID, "snapshot-tail", running, completed)
	seedTaskMigrationActor(t, base, taskRegistryActorType, taskRegistryActorKey, "snapshot-tail", running, running)
	store, err := es.NewJSONLStore(base, taskActorType)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"msg": map[string]any{
			"msg_id":  "pending-replace",
			"kind":    taskActorReplace,
			"payload": taskActorMutation{Event: taskEventStarted, Task: running},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Append(context.Background(), completed.ID, []es.Envelope{{Type: "sys.inbox.received", Kind: es.KindRuntime, Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	path, err := store.StreamPath(completed.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"torn":`); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	before := taskMigrationTree(t, filepath.Join(root, ".paw"), true)
	manager := NewManager(Config{Root: root})
	defer manager.Close()
	got, ok := manager.Status(completed.ID)
	if !ok || got.Status != TaskCompleted || got.Content != completed.Content {
		t.Fatalf("legacy inbox or stale registry replaced durable fact: %#v, %v", got, ok)
	}
	if tasks := manager.ListTasks(); len(tasks) != 1 || tasks[0].Status != TaskCompleted || tasks[0].OutputPath != registry.outputPath(completed.ID) {
		t.Errorf("stale legacy registry was not repaired: %#v", tasks)
	}
	manager.Close()
	if after := taskMigrationTree(t, filepath.Join(root, ".paw"), false); !reflect.DeepEqual(after, before) {
		t.Error("read-only recovery repaired a torn tail or modified legacy storage")
	}
}

func TestTaskMigrationGlobalActorWinsAndRepairsStaleIndex(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	registry := newTaskRegistry(root)
	completed := TaskSnapshot{ID: "global-wins", SessionID: "global-wins", Status: TaskCompleted, Content: "global fact"}
	running := completed
	running.Status, running.Content = TaskRunning, "stale"
	base := filepath.Join(registry.projectDir, "actors")
	seedTaskMigrationActor(t, base, taskActorType, completed.ID, "journal", running, completed)
	seedTaskMigrationActor(t, base, taskRegistryActorType, taskRegistryActorKey, "journal", running, running)
	seedTaskMigrationActor(t, filepath.Join(root, ".paw", "actors"), taskActorType, completed.ID, "journal", running, running)
	manager := NewManager(Config{Root: root, DisableStartupOrphanReconciliation: true})
	defer manager.Close()
	tasks := manager.ListTasks()
	if len(tasks) != 1 || tasks[0].Status != TaskCompleted || tasks[0].Content != completed.Content || tasks[0].OutputPath != registry.outputPath(completed.ID) {
		t.Fatalf("global actor projection not repaired before reconciliation: %#v", tasks)
	}
	if _, err := NewStatusTool(manager).Run(context.Background(), json.RawMessage(`{"id":"global-wins"}`)); err != nil {
		t.Fatal(err)
	}
}
