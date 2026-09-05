package loop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalInstructionsUsePawHome(t *testing.T) {
	for _, mode := range []string{"default", "override", "override_without_home", "injected_home"} {
		t.Run(mode, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PAW_CONFIG_HOME", "")
			t.Chdir(workspace)
			root := filepath.Join(home, ".paw")
			if mode != "default" {
				root = t.TempDir()
				t.Setenv("PAW_CONFIG_HOME", root)
			}
			if mode == "override_without_home" {
				t.Setenv("HOME", "")
				t.Setenv("USERPROFILE", "")
			}
			manager := NewInstructionManager(workspace)
			if mode == "injected_home" {
				manager.homeDir = t.TempDir()
				root = filepath.Join(manager.homeDir, ".paw")
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "AgEnT.Md"), []byte("global instruction fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "agent.md"), []byte("project instruction fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := manager.GlobalInstructions(); got != "global instruction fixture" {
				t.Fatalf("global instructions = %q", got)
			}
			if got := manager.ProjectInstructions(); got != "project instruction fixture" {
				t.Fatalf("project instructions = %q", got)
			}
			if _, err := os.Stat(filepath.Join(workspace, ".paw")); !os.IsNotExist(err) {
				t.Fatalf("workspace .paw should not exist: %v", err)
			}
		})
	}
}

func TestGlobalInstructionsDoNotFallBackToWorkspace(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PAW_CONFIG_HOME", "")
	workspace := t.TempDir()
	t.Chdir(workspace)
	if err := os.MkdirAll(filepath.Join(workspace, ".paw"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".paw", "agent.md"), []byte("not global instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := NewInstructionManager(workspace).GlobalInstructions(); got != "" {
		t.Fatalf("unresolved home must not load workspace instructions as global: %q", got)
	}
}
