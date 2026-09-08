package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"paw/internal/storage/session"
)

func TestGlobalToolsetMemoryAndRecentWorkspacePaths(t *testing.T) {
	for _, mode := range []string{"default", "override", "override_without_home"} {
		t.Run(mode, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PAW_CONFIG_HOME", "")
			t.Chdir(workspace)
			root := filepath.Join(home, ".paw")
			if mode != "default" {
				root = filepath.Join(t.TempDir(), "paw-config")
				t.Setenv("PAW_CONFIG_HOME", root)
			}
			if mode == "override_without_home" {
				t.Setenv("HOME", "")
				t.Setenv("USERPROFILE", "")
			}

			ctx := context.Background()
			store, err := session.NewJSONLStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tools := NewToolset(nil)
			if err := tools.BindSession(store, "memory-test", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.Memory().Run(ctx, json.RawMessage(`{"content":"global memory fixture"}`)); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, "memory.md"))
			if err != nil || string(data) != "global memory fixture" {
				t.Fatalf("memory = %q, %v", data, err)
			}

			recent, err := NewRecentWorkspaceStore("")
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(root, "recent-workspaces.json"); recent.Path() != want {
				t.Fatalf("recent path = %q, want %q", recent.Path(), want)
			}
			if err := recent.Remember(ctx, mustCanonicalWorkspace(t, workspace)); err != nil {
				t.Fatal(err)
			}
			reloaded, err := NewRecentWorkspaceStore("")
			if err != nil {
				t.Fatal(err)
			}
			items, err := reloaded.List(ctx)
			if err != nil || len(items) != 1 || items[0].Path != mustCanonicalWorkspace(t, workspace).Path {
				t.Fatalf("recent workspaces = %#v, %v", items, err)
			}
			if _, err := os.Stat(filepath.Join(workspace, ".paw")); !os.IsNotExist(err) {
				t.Fatalf("workspace .paw should not exist: %v", err)
			}
			if mode != "default" {
				if _, err := os.Stat(filepath.Join(home, ".paw")); !os.IsNotExist(err) {
					t.Fatalf("default home should not be used with override: %v", err)
				}
			}
		})
	}
}

func TestGlobalPathsDoNotFallBackToWorkspace(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PAW_CONFIG_HOME", "")
	workspace := t.TempDir()
	t.Chdir(workspace)
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := NewToolset(nil).BindSession(store, "s1", ""); err == nil {
		t.Fatal("BindSession should fail without a home")
	}
	if _, err := NewRecentWorkspaceStore(""); err == nil {
		t.Fatal("default recent store should fail without a home")
	}
	explicit := filepath.Join(t.TempDir(), "recent.json")
	recent, err := NewRecentWorkspaceStore(explicit)
	if err != nil || recent.Path() != explicit {
		t.Fatalf("explicit recent path should not require a home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".paw")); !os.IsNotExist(err) {
		t.Fatalf("workspace .paw should not exist: %v", err)
	}
}
