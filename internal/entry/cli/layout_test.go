package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceLayoutHasOneBuildEntryAndNoRuntimeArtifacts(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("repository go.mod not found")
		}
		root = parent
	}
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "paw" {
		t.Fatalf("build entries = %v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "paw", "main.go")); err != nil {
		t.Fatal(err)
	}
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && (entry.Name() == "node_modules" || entry.Name() == "dist") {
				return filepath.SkipDir
			}
			switch entry.Name() {
			case ".paw", ".pipeline-workspace", ".pipeline-last-run-summary.json":
				t.Errorf("runtime artifact in source tree: %s", path)
				if entry.IsDir() {
					return filepath.SkipDir
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
