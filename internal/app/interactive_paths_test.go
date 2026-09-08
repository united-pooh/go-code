package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"paw/internal/storage/session"
	"strings"
	"testing"
)

func TestStateBlockProviderMemoryUsesPawHome(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "override"}[override], func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PAW_CONFIG_HOME", "")
			t.Chdir(workspace)
			root := filepath.Join(home, ".paw")
			if override {
				root = t.TempDir()
				t.Setenv("PAW_CONFIG_HOME", root)
			}
			store, err := session.NewJSONLStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tools := NewToolset(nil)
			if err := tools.BindSession(store, "s1", ""); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := tools.Memory().Run(ctx, json.RawMessage(`{"content":"memory read/write fixture"}`)); err != nil {
				t.Fatal(err)
			}
			provider := stateBlockProviderFor("s1", store, nil, t.TempDir())
			if want := filepath.Join(root, "memory.md"); provider.memoryPath != want {
				t.Fatalf("memory path = %q, want %q", provider.memoryPath, want)
			}
			state, err := provider.BuildStateContext(ctx)
			if err != nil || !strings.Contains(state, "memory read/write fixture") {
				t.Fatalf("state should read memory written by toolset: %q, %v", state, err)
			}
			if _, err := os.Stat(filepath.Join(workspace, ".paw")); !os.IsNotExist(err) {
				t.Fatalf("workspace .paw should not exist: %v", err)
			}
		})
	}
}

func TestStateBlockProviderMemoryDoesNotFallBackToWorkspace(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PAW_CONFIG_HOME", "")
	workspace := t.TempDir()
	t.Chdir(workspace)
	if err := os.MkdirAll(filepath.Join(workspace, ".paw"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".paw", "memory.md"), []byte("not global memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := stateBlockProviderFor("", nil, nil, "")
	if provider.memoryPath != "" {
		t.Errorf("unresolved memory path = %q, want empty", provider.memoryPath)
	}
	state, err := provider.BuildStateContext(context.Background())
	if err != nil || state != "" {
		t.Fatalf("unresolved home must skip memory: %q, %v", state, err)
	}

	root := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", root)
	provider = stateBlockProviderFor("", nil, nil, "")
	if want := filepath.Join(root, "memory.md"); provider.memoryPath != want {
		t.Fatalf("override without HOME = %q, want %q", provider.memoryPath, want)
	}
}
