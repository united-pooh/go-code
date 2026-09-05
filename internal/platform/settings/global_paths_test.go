package settings

import (
	"path/filepath"
	"testing"
)

func TestDefaultSettingsPathUsesPawHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PAW_CONFIG_HOME", root)
	got, err := DefaultPath(nil)
	if err != nil || got != filepath.Join(root, "settings.json") {
		t.Fatalf("path=%q err=%v", got, err)
	}
	explicit := t.TempDir()
	got, err = DefaultPath(func() (string, error) { return explicit, nil })
	if err != nil || got != filepath.Join(explicit, ".paw", "settings.json") {
		t.Fatalf("explicit path=%q err=%v", got, err)
	}
}
