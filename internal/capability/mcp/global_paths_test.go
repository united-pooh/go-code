package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultMCPConfigUsesPawHome(t *testing.T) {
	home, root, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PAW_CONFIG_HOME", root)
	cfg, err := LoadConfig("", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("unexpected servers: %d", len(cfg.Servers))
	}
	if _, err := os.Stat(filepath.Join(root, "mcp.toml")); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{home, workspace} {
		if _, err := os.Stat(filepath.Join(base, ".paw")); !os.IsNotExist(err) {
			t.Fatalf("unexpected .paw in %s: %v", base, err)
		}
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := LoadConfig("", workspace); err != nil {
		t.Fatal(err)
	}
}
