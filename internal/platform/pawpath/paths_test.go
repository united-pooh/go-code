package pawpath

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, override := range []string{"", filepath.Join(t.TempDir(), "portable")} {
		t.Setenv("PAW_CONFIG_HOME", override)
		got, err := Home()
		if err != nil {
			t.Fatal(err)
		}
		want := override
		if want == "" {
			want = filepath.Join(home, ".paw")
		}
		if got != want {
			t.Fatalf("Home() = %q, want %q", got, want)
		}
		if _, err := os.Stat(want); !os.IsNotExist(err) {
			t.Fatalf("path resolution created a directory: %v", err)
		}
	}
}

func TestProjectDirPreservesExistingLayoutAndIsolatesWorkspaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", home)
	parent := t.TempDir()
	seen := map[string]bool{}
	for _, workspace := range []string{filepath.Join(parent, "one", "paw"), filepath.Join(parent, "two", "paw"), filepath.Join(parent, "paw ")} {
		got, err := ProjectDir(workspace)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(workspace))
		want := filepath.Join(home, "projects", fmt.Sprintf("paw-%x", sum[:4]))
		if got != want {
			t.Fatalf("ProjectDir(%q) = %q, want %q", workspace, got, want)
		}
		if seen[got] {
			t.Fatalf("workspace collision: %s", got)
		}
		seen[got] = true
		again, err := ProjectDir(filepath.Join(workspace, "."))
		if err != nil || again != got {
			t.Fatalf("unstable directory: %q %v", again, err)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolution wrote data: %v %v", entries, err)
	}
}

func TestProjectDirInHomeDoesNotUseEnvironment(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	home := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ProjectDirInHome(home, "")
	if err != nil {
		t.Fatal(err)
	}
	want, err := ProjectDirInHome(home, cwd)
	if err != nil || got != want || filepath.Dir(filepath.Dir(got)) != home {
		t.Fatalf("project directory = %q, want %q: %v", got, want, err)
	}
}
