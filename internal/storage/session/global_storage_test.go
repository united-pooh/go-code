package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"paw/internal/platform/pawpath"
)

func TestWorkspaceStoreUsesConfigHomeWithoutWorkspaceWrites(t *testing.T) {
	home, configHome, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PAW_CONFIG_HOME", configHome)
	store, err := NewJSONLStoreForWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pawpath.ProjectDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != want {
		t.Fatalf("store root = %q, want %q", store.Root(), want)
	}
	if _, err := store.CreateRoot(context.Background(), CreateRootRequest{SessionID: "global-only"}); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{home, workspace} {
		if _, err := os.Stat(filepath.Join(root, ".paw")); !os.IsNotExist(err) {
			t.Fatalf("unexpected .paw under %s: %v", root, err)
		}
	}
	if _, err := os.Stat(filepath.Join(want, "sessions", "global-only", "meta.json")); err != nil {
		t.Fatal(err)
	}
}
