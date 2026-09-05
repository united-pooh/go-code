package config

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalConfigurationFromHomeAndWorkspace(t *testing.T) {
	for _, fromHome := range []bool{false, true} {
		t.Run(map[bool]string{false: "project", true: "home"}[fromHome], func(t *testing.T) {
			clearDetectionEnv(t)
			t.Setenv("PAW_CONFIG_HOME", "")
			t.Setenv("PAW_GLOBAL_STORAGE_TEST_KEY", "synthetic")
			home := t.TempDir()
			workspace := home
			if !fromHome {
				workspace = t.TempDir()
			}
			paths, err := ResolvePaths(PathOptions{UserHomeDir: home, UserConfigDir: t.TempDir(), WorkspaceRoot: workspace})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(paths.Home, 0700); err != nil {
				t.Fatal(err)
			}
			raw := []byte(`{"schemaVersion":2,"activeModel":"demo/chat","providers":{"demo":{"transport":"openai-compatible","endpoint":"https://example.invalid/v1","auth":{"env":["PAW_GLOBAL_STORAGE_TEST_KEY"]}}},"models":{"demo/chat":{"provider":"demo","name":"chat"}}}`)
			if err := os.WriteFile(paths.GlobalConfig, raw, 0600); err != nil {
				t.Fatal(err)
			}
			manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableModelDiscovery: true})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			snapshot := manager.Snapshot()
			if !snapshot.Ready || len(snapshot.Document.Providers) != 1 || len(snapshot.Document.Models) != 1 {
				t.Fatalf("ready=%v providers=%d models=%d diagnostics=%v", snapshot.Ready, len(snapshot.Document.Providers), len(snapshot.Document.Models), snapshot.Diagnostics)
			}
			if paths.GlobalConfig == paths.WorkspaceConfig {
				t.Fatal("global and workspace configuration collide")
			}
			if !fromHome {
				if _, err := os.Stat(filepath.Join(workspace, ".paw")); !os.IsNotExist(err) {
					t.Fatalf("watcher created workspace .paw: %v", err)
				}
			}
			if err := manager.Reload(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(paths.GlobalConfig)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("global config changed: %v", err)
			}
		})
	}
}

func TestWorkspaceLocalConfigurationIsNotLoaded(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, true)
	local := filepath.Join(paths.WorkspaceRoot, ".paw", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"providers":{"invalid":{}}}`)
	if err := os.WriteFile(local, raw, 0600); err != nil {
		t.Fatal(err)
	}
	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, true)
	for _, diagnostic := range manager.Snapshot().Diagnostics {
		if diagnostic.File == local {
			t.Fatalf("loaded obsolete workspace config: %v", diagnostic)
		}
	}
	got, err := os.ReadFile(local)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("local config changed: %v", err)
	}
}
