package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"paw/internal/capability/model"
	configv2 "paw/internal/platform/config"
	"paw/internal/platform/settings"
)

func contextLimitRuntime(t *testing.T, incomplete bool, general int) *WorkspaceRuntime {
	t.Helper()
	home, configHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PAW_CONFIG_HOME", configHome)
	t.Setenv("PAW_CONTEXT_TEST_KEY", "synthetic")
	if !incomplete {
		raw := `{"schemaVersion":2,"activeModel":"demo/unknown","providers":{"demo":{"transport":"openai-compatible","endpoint":"https://example.invalid/v1","auth":{"env":["PAW_CONTEXT_TEST_KEY"]}}},"models":{"demo/unknown":{"provider":"demo","name":"unknown-context-model"},"demo/explicit":{"provider":"demo","name":"explicit-context-model","contextWindow":64000},"demo/metadata":{"provider":"demo","name":"deepseek-v4-flash"}}}`
		if err := os.WriteFile(filepath.Join(configHome, "config.jsonc"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := settings.DefaultConfig()
	cfg.UI.ContextLimitTokens = general
	if err := settings.Save(filepath.Join(configHome, "settings.json"), cfg); err != nil {
		t.Fatal(err)
	}
	runtime, err := BuildWorkspaceRuntime(context.Background(), WorkspaceRuntimeOptions{
		Root: t.TempDir(), AllowIncomplete: incomplete,
		WorkerContext: WorkerContext{WorkerMode: true, DisableMainTodo: true, MCPBroker: emptyMCPBroker{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func assertRuntimeContextLimit(t *testing.T, runtime *WorkspaceRuntime, want int) {
	t.Helper()
	if got := runtime.SessionHost.ContextStats(0, "").LimitTokens; got != want {
		t.Fatalf("runtime context limit = %d, want %d", got, want)
	}
}

func TestRuntimeContextLimitStartup(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		for _, general := range []int{0, 270000} {
			name := "ready"
			if incomplete {
				name = "incomplete"
			}
			t.Run(name, func(t *testing.T) {
				runtime := contextLimitRuntime(t, incomplete, general)
				want := general
				if want == 0 {
					want = model.DefaultContextLimitTokens
				}
				assertRuntimeContextLimit(t, runtime, want)
			})
		}
	}
}

func TestRuntimeContextLimitLiveSettingsAndModelSwitches(t *testing.T) {
	runtime := contextLimitRuntime(t, false, 270000)
	assertRuntimeContextLimit(t, runtime, 270000)
	for _, tc := range []struct {
		id            string
		general, want int
	}{
		{"demo/explicit", 270000, 64000},
		{"demo/explicit", 96000, 64000},
		{"demo/unknown", 96000, 96000},
		{"demo/metadata", 96000, 96000},
		{"demo/metadata", 0, 1000000},
		{"demo/unknown", 0, model.DefaultContextLimitTokens},
	} {
		cfg := runtime.SettingsController.CurrentSettings()
		cfg.UI.ContextLimitTokens = tc.general
		if err := runtime.SettingsController.SaveSettings(cfg); err != nil {
			t.Fatal(err)
		}
		if err := runtime.ConfigController.SetActiveModelID(tc.id); err != nil {
			t.Fatal(err)
		}
		runtime.SessionHost.SetContextLimitTokens(1000000)
		assertRuntimeContextLimit(t, runtime, tc.want)
	}
	cfg := runtime.Model.CurrentModelConfig()
	cfg.ContextLimitTokens = 48000
	cfg.ModelContextLimitTokens = map[string]int{cfg.Model: 32000}
	if err := runtime.Model.ApplyModelConfig(cfg); err != nil {
		t.Fatal(err)
	}
	assertRuntimeContextLimit(t, runtime, 32000)
}

func TestRuntimeContextLimitIncompleteLiveSettings(t *testing.T) {
	runtime := contextLimitRuntime(t, true, 270000)
	cfg := runtime.SettingsController.CurrentSettings()
	cfg.UI.ContextLimitTokens = 96000
	runtime.SettingsController.UpdateRuntime(cfg)
	assertRuntimeContextLimit(t, runtime, 96000)
}

func TestRuntimeContextLimitConcurrentSettingsAndModelChanges(t *testing.T) {
	runtime := contextLimitRuntime(t, false, 270000)
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range 30 {
				switch worker {
				case 0, 1:
					id := "demo/unknown"
					if (i+worker)%2 == 0 {
						id = "demo/explicit"
					}
					snapshot := runtime.ConfigController.Snapshot()
					_, err := runtime.ConfigController.UpdateConfig(context.Background(), snapshot.Revision, []configv2.Operation{
						configv2.SetActiveModel(id), configv2.SetYolo((i+worker)%2 == 0),
					})
					if err != nil && !errors.Is(err, configv2.ErrRevisionConflict) {
						t.Error(err)
						return
					}
				case 2:
					cfg := runtime.SettingsController.CurrentSettings()
					cfg.UI.ContextLimitTokens = 270000 + i
					runtime.SettingsController.UpdateRuntime(cfg)
				default:
					runtime.SessionHost.SetContextLimitTokens(1)
					if got := runtime.SessionHost.ContextStats(0, "").LimitTokens; got < 64000 {
						t.Errorf("stale literal leaked: %d", got)
					}
				}
			}
		}()
	}
	workers.Wait()
	if err := runtime.ConfigController.SetActiveModelID("demo/unknown"); err != nil {
		t.Fatal(err)
	}
	assertRuntimeContextLimit(t, runtime, runtime.SettingsController.CurrentSettings().UI.ContextLimitTokens)
	if got, want := runtime.SessionHost.YoloMode(), runtime.ConfigController.Snapshot().Document.Yolo; got != want {
		t.Fatalf("callback left stale yolo mode: got %t, want %t", got, want)
	}
}
