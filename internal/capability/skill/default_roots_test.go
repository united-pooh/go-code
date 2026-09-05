package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRootsUsesUnifiedPawHome(t *testing.T) {
	for _, mode := range []string{"default", "override", "override_without_home"} {
		t.Run(mode, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
			want := filepath.Join(root, "skills")
			writeTestSkill(t, want, "global-fixture", "# Global fixture\nUse the global skill.")
			roots := DefaultRoots(workspace)
			if len(roots) != 1 || roots[0] != want {
				t.Fatalf("roots = %#v, want only %q", roots, want)
			}
			skills := NewRegistry(roots).Skills()
			if len(skills) != 1 || skills[0].Name != "global-fixture" {
				t.Fatalf("global skills = %#v", skills)
			}
			if _, err := os.Stat(filepath.Join(workspace, ".paw")); !os.IsNotExist(err) {
				t.Fatalf("workspace .paw should not exist: %v", err)
			}
		})
	}
}

func TestDefaultRootsDoesNotFallBackWithoutPawHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PAW_CONFIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	t.Chdir(workspace)
	if roots := DefaultRoots(workspace); len(roots) != 0 {
		t.Fatalf("unresolved home roots = %#v, want none", roots)
	}
}
