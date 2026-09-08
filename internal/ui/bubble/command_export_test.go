package bubble

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/platform/pawpath"
)

type exportWorkspaceRunner struct {
	fakeRunner
	root string
}

func (r *exportWorkspaceRunner) WorkspaceRoot() string { return r.root }

func TestExportCommandUsesGlobalProjectStorage(t *testing.T) {
	for _, override := range []bool{false, true} {
		for _, runnerRoot := range []bool{false, true} {
			t.Run(fmt.Sprintf("override=%t/runner=%t", override, runnerRoot), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				t.Setenv("PAW_CONFIG_HOME", "")
				storageHome := filepath.Join(home, ".paw")
				if override {
					storageHome = filepath.Join(t.TempDir(), "storage")
					t.Setenv("PAW_CONFIG_HOME", storageHome)
				}
				t.Chdir(t.TempDir())
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				workspace := cwd
				var runner Runner = &fakeRunner{}
				if runnerRoot {
					workspace = t.TempDir()
					runner = &exportWorkspaceRunner{root: workspace}
				}
				m := newTestModel(runner)
				m.transcript = []transcriptEntry{{kind: entryUser, title: "you", body: "hello"}}
				projectDir, err := pawpath.ProjectDirInHome(storageHome, workspace)
				if err != nil {
					t.Fatal(err)
				}
				path, err := m.exportPath("")
				if err != nil {
					t.Fatal(err)
				}
				if filepath.Dir(path) != filepath.Join(projectDir, "exports") {
					t.Errorf("default export = %q, want project %q", path, projectDir)
				}
				if _, err := os.Stat(storageHome); !os.IsNotExist(err) {
					t.Fatalf("export path resolution created storage: %v", err)
				}
				m.handleExportCommand("/export")
				exports, err := filepath.Glob(filepath.Join(projectDir, "exports", "conversation-*.txt"))
				if err != nil || len(exports) != 1 {
					t.Errorf("exports = %v, error=%v", exports, err)
				} else {
					data, err := os.ReadFile(exports[0])
					if err != nil || !strings.Contains(string(data), "you:\nhello") {
						t.Errorf("export content = %q, error=%v", data, err)
					}
					info, err := os.Stat(exports[0])
					if err != nil || info.Mode().Perm() != 0o600 {
						t.Errorf("export mode must be 0600: info=%v, error=%v", info, err)
					}
				}
				for _, root := range []string{workspace, cwd} {
					if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
						t.Errorf("export created .paw in %q: %v", root, err)
					}
				}
			})
		}
	}
}

func TestExportPathExplicitWorkspacePaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	root := t.TempDir()
	m := newTestModel(&exportWorkspaceRunner{root: root})
	for _, arg := range []string{"notes", "notes.md", "notes.txt", filepath.Join(root, "notes.txt")} {
		got, err := m.exportPath(arg)
		want := filepath.Join(root, "notes.txt")
		if err != nil || got != want {
			t.Errorf("exportPath(%q) = %q, %v; want %q", arg, got, err, want)
		}
	}
	for _, arg := range []string{"../escape.txt", filepath.Join(t.TempDir(), "escape.txt")} {
		if _, err := m.exportPath(arg); err == nil {
			t.Errorf("exportPath(%q) allowed workspace escape", arg)
		}
	}
	m.transcript = []transcriptEntry{{kind: entryUser, title: "you", body: "hello"}}
	m.handleExportCommand("/export notes")
	if data, err := os.ReadFile(filepath.Join(root, "notes.txt")); err != nil || !strings.Contains(string(data), "hello") {
		t.Fatalf("explicit export content = %q, error=%v", data, err)
	}
}
