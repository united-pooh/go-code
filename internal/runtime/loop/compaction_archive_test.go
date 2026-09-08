package loop

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/platform/pawpath"
)

func isolateCompactionArchiveHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
}

func TestCompactionArchiveUsesGlobalProjectStorage(t *testing.T) {
	for _, override := range []bool{false, true} {
		for _, useCWD := range []bool{false, true} {
			t.Run(fmt.Sprintf("override=%t/cwd=%t", override, useCWD), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				t.Setenv("PAW_CONFIG_HOME", "")
				storageHome := filepath.Join(home, ".paw")
				if override {
					storageHome = filepath.Join(t.TempDir(), "storage")
					t.Setenv("PAW_CONFIG_HOME", storageHome)
				}
				root := t.TempDir()
				t.Chdir(root)
				root, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				workRoot := root
				if useCWD {
					workRoot = ""
				}
				archive, err := newCompactionArchive(workRoot, "session-1", true)
				if err != nil {
					t.Fatal(err)
				}
				projectDir, err := pawpath.ProjectDirInHome(storageHome, root)
				if err != nil {
					t.Fatal(err)
				}
				want := filepath.Join(projectDir, "sessions", "session-1", "compactions")
				if archive.dir != want {
					t.Errorf("archive dir = %q, want %q", archive.dir, want)
				}
				if _, err := os.Stat(storageHome); !os.IsNotExist(err) {
					t.Fatalf("archive constructor created storage: %v", err)
				}
				result, err := archive.archive([]archiveRequest{{
					Operation: "fold", OriginalContent: "original",
					Message: message.Message{Role: message.RoleUser, Content: "original"},
				}})
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Paths) != 1 || filepath.Dir(result.Paths[0]) != want {
					t.Errorf("archive paths = %v, want directory %q", result.Paths, want)
				}
				if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
					t.Fatalf("archive created workspace .paw: %v", err)
				}
			})
		}
	}
}

func TestCompactionArchiveWritesOriginalMessageAndReusesHash(t *testing.T) {
	isolateCompactionArchiveHome(t)
	root := t.TempDir()
	archive, err := newCompactionArchive(root, "session-1", true)
	if err != nil {
		t.Fatal(err)
	}
	msg := message.Message{Role: message.RoleUser, ToolResult: &message.ToolResult{
		ToolUseID: "call-1", Content: strings.Repeat("x", 2048),
	}}
	req := archiveRequest{
		Operation: "snip", MessageIndex: 3, ToolResultIndex: 0,
		ToolUseID: "call-1", ToolName: "Read", OriginalBytes: 2048,
		Message: msg, OriginalContent: msg.ToolResult.Content,
	}
	first, err := archive.archive([]archiveRequest{req})
	if err != nil {
		t.Fatal(err)
	}
	second, err := archive.archive([]archiveRequest{req})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Paths) != 1 || len(second.Paths) != 1 || first.Paths[0] != second.Paths[0] {
		t.Fatalf("archive path not reused: %#v != %#v", first.Paths, second.Paths)
	}
	data, err := os.ReadFile(first.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(strings.Repeat("x", 64))) {
		t.Fatal("archive does not contain original content")
	}
	indexData, err := os.ReadFile(filepath.Join(archive.dir, "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(bytes.TrimSpace(indexData), []byte{'\n'}) + 1; got != 1 {
		t.Fatalf("index records = %d, want 1", got)
	}
}

func TestCompactionArchiveSanitizesSessionID(t *testing.T) {
	isolateCompactionArchiveHome(t)
	root := t.TempDir()
	archive, err := newCompactionArchive(root, "../../escape", true)
	if err != nil {
		t.Fatal(err)
	}
	projectDir, err := pawpath.ProjectDir(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Join(projectDir, "sessions"), archive.dir)
	if err != nil {
		t.Fatal(err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("unsafe archive dir: %s", archive.dir)
	}
}

func TestCompactionArchiveFailureDoesNotPublishFinalFile(t *testing.T) {
	isolateCompactionArchiveHome(t)
	archive, err := newCompactionArchive(t.TempDir(), "session-1", true)
	if err != nil {
		t.Fatal(err)
	}
	archive.syncFile = func(*os.File) error { return errors.New("disk full") }
	_, err = archive.archive([]archiveRequest{{
		Operation: "snip", ToolUseID: "call-1", OriginalContent: strings.Repeat("x", 2048),
		Message: message.Message{Role: message.RoleUser},
	}})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("archive error = %v", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(archive.dir, "*-snip.jsonl"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("published archive files after sync failure: %v", matches)
	}
}

func TestCompactionArchiveIgnoresDamagedIndexLines(t *testing.T) {
	isolateCompactionArchiveHome(t)
	archive, err := newCompactionArchive(t.TempDir(), "session-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archive.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive.dir, "index.jsonl"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := archive.archive([]archiveRequest{{
		Operation: "prune", ToolUseID: "call-1", OriginalContent: strings.Repeat("y", 2048),
		Message: message.Message{Role: message.RoleUser},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("paths = %#v", result.Paths)
	}
}
