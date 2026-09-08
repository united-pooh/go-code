package task

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/message"
	"paw/internal/platform/settings"
	"paw/internal/storage/session"
)

type contextLimitModel struct {
	recordingModel
	configMu           sync.RWMutex
	cfg                model.Config
	afterInitialConfig func()
	initialConfig      sync.Once
}

func (m *contextLimitModel) CurrentModelConfig() model.Config {
	m.configMu.RLock()
	cfg := model.CloneConfig(m.cfg)
	m.configMu.RUnlock()
	m.initialConfig.Do(func() {
		if m.afterInitialConfig != nil {
			m.afterInitialConfig()
		}
	})
	return cfg
}

func runContextLimitTask(t *testing.T, manager *Manager, store *session.JSONLStore, streaming bool, disableTools bool) {
	t.Helper()
	ctx := context.Background()
	const id = "context-limit"
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, id,
		message.Message{Role: message.RoleUser, Content: "initial task"},
		message.Message{Role: message.RoleAssistant, Content: strings.Repeat("old investigation details ", 160)},
		message.Message{Role: message.RoleUser, Content: "keep API"},
		message.Message{Role: message.RoleAssistant, Content: strings.Repeat("old implementation details ", 160)},
		message.Message{Role: message.RoleUser, Content: "run tests"},
		message.Message{Role: message.RoleAssistant, Content: "recent answer"},
	); err != nil {
		t.Fatal(err)
	}
	if streaming {
		events := make(chan model.StreamEvent, 128)
		if _, _, _, _, err := manager.runStreamingSession(ctx, id, "continue", "", disableTools, events); err != nil {
			t.Fatal(err)
		}
	} else if _, _, _, err := manager.runSession(ctx, id, "continue", disableTools); err != nil {
		t.Fatal(err)
	}
}

func TestTaskContextLimitConstructors(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			cfg       model.Config
			general   int
			wantCalls int
		}{
			{"general", model.Config{Model: "unknown-context-model"}, 1000, 2},
			{"general before metadata", model.Config{Model: "deepseek-v4-flash"}, 1000, 2},
			{"explicit before general", model.Config{ContextLimitTokens: 1000000}, 1000, 1},
			{"map before general", model.Config{Model: "test", ContextLimitTokens: 1000, ModelContextLimitTokens: map[string]int{"test": 1000000}}, 1000, 1},
			{"auto", model.Config{Model: "unknown-context-model"}, 0, 1},
		} {
			name := "session/"
			if streaming {
				name = "streaming/"
			}
			t.Run(name+tc.name, func(t *testing.T) {
				t.Setenv("PAW_CONFIG_HOME", t.TempDir())
				cfg := settings.DefaultConfig()
				cfg.UI.ContextLimitTokens = tc.general
				client := &contextLimitModel{cfg: tc.cfg, recordingModel: recordingModel{rounds: []fakeRound{
					{events: []model.StreamEvent{{Delta: "summary or done", Done: true}}},
					{events: []model.StreamEvent{{Delta: "done", Done: true}}},
				}}}
				manager, store, _ := newTestManager(t, client, cfg, nil)
				runContextLimitTask(t, manager, store, streaming, true)
				calls := client.callsSnapshot()
				if len(calls) != tc.wantCalls {
					t.Fatalf("model calls = %d, want %d", len(calls), tc.wantCalls)
				}
				if tc.wantCalls == 2 && !strings.Contains(calls[0][0].Content, "Standing facts & constraints") {
					t.Fatal("missing pressure-triggered summary")
				}
			})
		}
	}
}

func TestTaskContextLimitLiveChangesWithinBothConstructors(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, changeModel := range []bool{false, true} {
			name := "session/settings"
			if streaming {
				name = "streaming/settings"
			}
			if changeModel {
				name += "/model"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("PAW_CONFIG_HOME", t.TempDir())
				controller, err := settings.NewController(filepath.Join(t.TempDir(), "settings.json"))
				if err != nil {
					t.Fatal(err)
				}
				cfg := settings.DefaultConfig()
				cfg.UI.ContextLimitTokens = 1000000
				controller.UpdateRuntime(cfg)
				client := &contextLimitModel{cfg: model.Config{Model: "unknown-context-model"}, recordingModel: recordingModel{rounds: []fakeRound{
					{events: []model.StreamEvent{{Delta: "summary", Done: true}}},
					{events: []model.StreamEvent{{Delta: "done", Done: true}}},
				}}}
				// Change after the constructor's initial model read, before pressure is checked.
				client.afterInitialConfig = func() {
					if changeModel {
						client.configMu.Lock()
						client.cfg.ContextLimitTokens = 1000
						client.configMu.Unlock()
					} else {
						next := controller.CurrentSettings()
						next.UI.ContextLimitTokens = 1000
						controller.UpdateRuntime(next)
					}
				}
				manager, store, _ := newTestManager(t, client, cfg, nil)
				manager.settings = controller
				runContextLimitTask(t, manager, store, streaming, false)
				calls := client.callsSnapshot()
				if len(calls) != 2 || !strings.Contains(calls[0][0].Content, "Standing facts & constraints") {
					t.Fatalf("live change after construction did not trigger summary: %d model calls", len(calls))
				}
			})
		}
	}
}
