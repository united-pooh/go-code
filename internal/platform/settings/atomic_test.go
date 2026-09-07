package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSaveReplacesSettingsWithoutTruncatingPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cfg := DefaultConfig()
	cfg.UI.ContextLimitTokens = 270000
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	previous := path + ".previous"
	if err := os.Link(path, previous); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	cfg.UI.ContextLimitTokens = 96000
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	old, err := Load(previous)
	if err != nil || old.UI.ContextLimitTokens != 270000 {
		t.Fatalf("previous file was truncated: limit=%d, err=%v", old.UI.ContextLimitTokens, err)
	}
	current, err := Load(path)
	if err != nil || current.UI.ContextLimitTokens != 96000 {
		t.Fatalf("new file invalid: limit=%d, err=%v", current.UI.ContextLimitTokens, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("permissions=%v", info.Mode())
	}
}

func TestSaveSettingsHoldsControllerLockDuringDiskCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	controller, err := NewController(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.UI.ContextLimitTokens = 270000
	if err := controller.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	controller.mu.Lock()
	cfg.UI.ContextLimitTokens = 96000
	done := make(chan error, 1)
	go func() { done <- controller.SaveSettings(cfg) }()
	// Holding the publication lock must prevent committing the disk half too.
	deadline := time.Now().Add(200 * time.Millisecond)
	changed := false
	for time.Now().Before(deadline) {
		loaded, loadErr := Load(path)
		if loadErr != nil || loaded.UI.ContextLimitTokens != 270000 {
			changed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	controller.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("disk commit bypassed controller transaction lock")
	}
}

func TestConcurrentSettingsSavesKeepDiskAndMemoryConsistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	controller, err := NewController(path)
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	for worker := range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := range 10 {
				cfg := DefaultConfig()
				cfg.UI.ContextLimitTokens = 270000 + worker*10 + i
				if err := controller.SaveSettings(cfg); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	writers.Wait()
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != controller.CurrentSettings() {
		t.Fatal("successful saves left disk and runtime out of sync")
	}
}

func TestSaveRenameFailurePreservesRuntimeAndCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	controller, err := NewController(path)
	if err != nil {
		t.Fatal(err)
	}
	before := controller.CurrentSettings()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "sentinel"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := before
	cfg.UI.ContextLimitTokens = 270000
	if err := controller.SaveSettings(cfg); err == nil {
		t.Fatal("save to directory succeeded")
	}
	if got := controller.CurrentSettings(); got != before {
		t.Fatal("failed save changed runtime settings")
	}
	data, err := os.ReadFile(filepath.Join(path, "sentinel"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("destination modified: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file leaked: %v, %v", entries, err)
	}
}
