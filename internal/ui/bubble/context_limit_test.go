package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	modelcfg "paw/internal/capability/model"
	configv2 "paw/internal/platform/config"
	"paw/internal/platform/settings"
)

func editGeneralContextLimit(m appModel, value string) appModel {
	for i, field := range m.configGeneralDisplayedFields() {
		if field.key == "ui.context_limit_tokens" {
			m.configCenter.selected = i
			break
		}
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m.configCenter.editValue = value
	return press(m, tea.KeyMsg{Type: tea.KeyEnter})
}

func assertContextLimit(t *testing.T, m appModel, runner *fakeRunner, want int) {
	t.Helper()
	if len(runner.contextLimits) == 0 || runner.contextLimits[len(runner.contextLimits)-1] != want {
		t.Fatalf("runner limits = %v, want last %d", runner.contextLimits, want)
	}
	if got := m.contextStats().LimitTokens; got != want {
		t.Fatalf("meter limit = %d, want %d", got, want)
	}
}

func TestGeneralContextLimitSavesAppliesAndRestarts(t *testing.T) {
	m, _, runner := openGeneralCenter(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	controller, err := settings.NewController(path)
	if err != nil {
		t.Fatal(err)
	}
	m.settingsConfig = controller
	m = editGeneralContextLimit(m, "270000")
	if m.configCenter.err != "" {
		t.Fatal(m.configCenter.err)
	}
	assertContextLimit(t, m, runner, 270000)
	if got := m.statusText("session"); !strings.Contains(got, "context_limit=270000") || !strings.Contains(got, "limit=270000") {
		t.Fatalf("inconsistent status: %s", got)
	}
	m.handleModelCommand("/model status")
	if got := m.transcript[len(m.transcript)-1].body; !strings.Contains(got, "context=270000") {
		t.Fatalf("model status: %s", got)
	}
	reloaded, err := settings.NewController(path)
	if err != nil {
		t.Fatal(err)
	}
	nextRunner := &fakeRunner{}
	restarted := newModel(context.Background(), nextRunner, "session", m.modelConfig, reloaded, nil, nil, newTerminalCursorAnchor())
	assertContextLimit(t, restarted, nextRunner, 270000)
}

func TestGeneralContextLimitValidationAndSaveFailure(t *testing.T) {
	for _, value := range []string{"-1", "270k", "1.5", "99999999999999999999999999", ""} {
		t.Run(value, func(t *testing.T) {
			m, controller, runner := openGeneralCenter(t)
			before := runner.contextLimits[len(runner.contextLimits)-1]
			m = editGeneralContextLimit(m, value)
			if m.configCenter.err == "" || len(controller.saved) != 0 {
				t.Fatalf("invalid value accepted: err=%q saved=%v", m.configCenter.err, controller.saved)
			}
			assertContextLimit(t, m, runner, before)
		})
	}
	t.Run("disk failure", func(t *testing.T) {
		m, controller, runner := openGeneralCenter(t)
		before := runner.contextLimits[len(runner.contextLimits)-1]
		controller.err = errors.New("disk full")
		m = editGeneralContextLimit(m, "270000")
		if !strings.Contains(m.configCenter.err, "disk full") {
			t.Fatal(m.configCenter.err)
		}
		assertContextLimit(t, m, runner, before)
	})
}

func TestGeneralContextLimitModelSwitchAndReset(t *testing.T) {
	m, _, runner := openGeneralCenter(t)
	controller := m.configCenterController
	m = editGeneralContextLimit(m, "270000")
	configured := controller.Snapshot().Document.Models["local/two"]
	configured.ContextWindow = 64000
	m.applyConfigOperations(configv2.UpsertModel("local/two", configured))
	m.handleModelCommand("/model local/two")
	assertContextLimit(t, m, runner, 64000)
	card, ok := parseModelCardBlock(m.transcript[len(m.transcript)-1].body)
	if !ok || card.Context != 64000 {
		t.Fatalf("model card = %+v", card)
	}
	m.handleModelCommand("/model local/one")
	assertContextLimit(t, m, runner, 270000)
	card, ok = parseModelCardBlock(m.transcript[len(m.transcript)-1].body)
	if !ok || card.Context != 270000 {
		t.Fatalf("fallback model card = %+v", card)
	}
	m.configCenter.page = configCenterGeneral
	m = editGeneralContextLimit(m, "0")
	assertContextLimit(t, m, runner, modelcfg.DefaultContextLimitTokens)
}

func TestGeneralContextLimitModelEditClearAndReload(t *testing.T) {
	m, _, runner := openGeneralCenter(t)
	m = editGeneralContextLimit(m, "270000")
	controller := m.configCenterController
	for _, value := range []struct {
		raw  string
		want int
	}{{"64000", 64000}, {"", 270000}} {
		m.configCenter.targetID = "local/one"
		m.openConfigEdit(configEditModelContextWindow, value.raw, "")
		m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
		if m.configCenter.err != "" {
			t.Fatal(m.configCenter.err)
		}
		assertContextLimit(t, m, runner, value.want)
	}
	path := controller.ConfigPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := configv2.DecodeJSONObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc["models"].(map[string]any)["local/one"].(map[string]any)["contextWindow"] = 96000
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m.handleConfigCommand("/config reload")
	assertContextLimit(t, m, runner, 96000)
}

func TestGeneralContextLimitWizardAndCatalogPaths(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		t.Run(fmt.Sprintf("catalog=%v", catalog), func(t *testing.T) {
			m, _, runner := openGeneralCenter(t)
			m = editGeneralContextLimit(m, "270000")
			configured := m.configCenterController.Snapshot().Document.Models["local/two"]
			configured.ContextWindow = 64000
			m.applyConfigOperations(configv2.UpsertModel("local/two", configured))
			for _, selection := range []struct {
				name string
				want int
			}{{"two", 64000}, {"one", 270000}} {
				m.configCenter = nil
				if catalog {
					m.openConfigCenter()
					m.configCenter.page = configCenterModels
					m.refreshConfigCenterCatalog(configCenterModels)
					m.configCenter.search = "local/" + selection.name
					m.configCenter.selected = 0
					m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
				} else {
					m.handleModelCommand("/model")
					m = advanceModelWizard(t, m, tea.KeyMsg{Type: tea.KeyEnter})
					m.modelWizard.selectedModel = sortedIndex(m.modelWizard.modelOptions, selection.name)
					m = advanceModelWizard(t, m, tea.KeyMsg{Type: tea.KeyEnter})
					if m.modelWizard != nil {
						t.Fatalf("wizard did not apply: %+v", m.modelWizard)
					}
					card, ok := parseModelCardBlock(m.transcript[len(m.transcript)-1].body)
					if !ok || card.Context != selection.want {
						t.Fatalf("card=%+v", card)
					}
				}
				assertContextLimit(t, m, runner, selection.want)
			}
		})
	}
}

func TestGeneralContextLimitFallbackControllerSwitching(t *testing.T) {
	for _, wizard := range []bool{false, true} {
		t.Run(fmt.Sprintf("wizard=%v", wizard), func(t *testing.T) {
			controller := &fakeModelConfigController{current: modelcfg.Config{Provider: "local", Model: "one", Models: []string{"one", "two"}, ModelContextLimitTokens: map[string]int{"two": 64000}}}
			cfg := settings.DefaultConfig()
			cfg.UI.ContextLimitTokens = 270000
			runner := &fakeRunner{}
			m := newModel(context.Background(), runner, "session", controller, &fakeSettingsController{current: cfg}, nil, nil, newTerminalCursorAnchor())
			for _, selection := range []struct {
				name string
				want int
			}{{"two", 64000}, {"one", 270000}} {
				if wizard {
					m.handleModelCommand("/model")
					m = advanceModelWizard(t, m, tea.KeyMsg{Type: tea.KeyEnter})
					m.modelWizard.selectedModel = sortedIndex(m.modelWizard.modelOptions, selection.name)
					m = advanceModelWizard(t, m, tea.KeyMsg{Type: tea.KeyEnter})
				} else {
					m.handleModelCommand("/model " + selection.name)
				}
				assertContextLimit(t, m, runner, selection.want)
				card, ok := parseModelCardBlock(m.transcript[len(m.transcript)-1].body)
				if !ok || card.Context != selection.want {
					t.Fatalf("card=%+v", card)
				}
			}
		})
	}
}

func TestGeneralContextLimitMissingControllerIsNotSuccess(t *testing.T) {
	m, _, runner := openGeneralCenter(t)
	m.settingsConfig = nil
	before := runner.contextLimits[len(runner.contextLimits)-1]
	m = editGeneralContextLimit(m, "270000")
	if m.configCenter.err == "" {
		t.Fatal("save reported success without settings controller")
	}
	assertContextLimit(t, m, runner, before)
}

func TestGeneralContextLimitStartupPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     modelcfg.Config
		general int
		want    int
	}{
		{"unknown", modelcfg.Config{Model: "gpt-6-astra"}, 270000, 270000},
		{"metadata", modelcfg.Config{Model: "deepseek-v4-flash"}, 0, 1000000},
		{"general before metadata", modelcfg.Config{Model: "deepseek-v4-flash"}, 270000, 270000},
		{"model before general", modelcfg.Config{Model: "one", ContextLimitTokens: 64000}, 270000, 64000},
		{"map before general", modelcfg.Config{Model: "one", ModelContextLimitTokens: map[string]int{"one": 64000}}, 270000, 64000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := settings.DefaultConfig()
			cfg.UI.ContextLimitTokens = tc.general
			runner := &fakeRunner{}
			m := newModel(context.Background(), runner, "session", &fakeModelConfigController{current: tc.cfg}, &fakeSettingsController{current: cfg}, nil, nil, newTerminalCursorAnchor())
			assertContextLimit(t, m, runner, tc.want)
		})
	}
}

func TestGeneralContextLimitRealSaveFailureLeavesDiskAndRuntime(t *testing.T) {
	m, _, runner := openGeneralCenter(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	controller, err := settings.NewController(path)
	if err != nil {
		t.Fatal(err)
	}
	m.settingsConfig = controller
	m = editGeneralContextLimit(m, "270000")
	if err := os.Mkdir(path+".block", 0700); err != nil {
		t.Fatal(err)
	}
	// Point a fresh controller at a missing child of a regular file to force ENOTDIR on save.
	blocked, err := settings.NewController(filepath.Join(path+".block", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".block", path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".block", []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked.UpdateRuntime(controller.CurrentSettings())
	m.settingsConfig = blocked
	m.configCenter.page = configCenterGeneral
	m = editGeneralContextLimit(m, "300000")
	if m.configCenter.err == "" {
		t.Fatal("expected real save failure")
	}
	assertContextLimit(t, m, runner, 270000)
	loaded, err := settings.Load(path)
	if err != nil || loaded.UI.ContextLimitTokens != 270000 {
		t.Fatalf("disk changed: %+v, %v", loaded.UI, err)
	}
}
