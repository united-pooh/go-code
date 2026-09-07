package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextLimitAutoAndLegacySettings(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      int
	}{
		{"missing", `{}`, 0},
		{"auto", `{"ui":{"context_limit_tokens":0}}`, 0},
		{"custom", `{"ui":{"context_limit_tokens":270000}}`, 270000},
		{"legacy positive", `{"ui":{"context_limit_tokens":1048576}}`, 1048576},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil || cfg.UI.ContextLimitTokens != tc.want {
				t.Fatalf("Load = %d, %v, want %d", cfg.UI.ContextLimitTokens, err, tc.want)
			}
			if err := Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			cfg, err = Load(path)
			if err != nil || cfg.UI.ContextLimitTokens != tc.want {
				t.Fatalf("round trip = %d, %v, want %d", cfg.UI.ContextLimitTokens, err, tc.want)
			}
		})
	}
	if got := DefaultConfig().UI.ContextLimitTokens; got != 0 {
		t.Fatalf("default = %d, want auto", got)
	}
}

func TestNegativeContextLimitRejectedWithoutChangingSettings(t *testing.T) {
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
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.ContextLimitTokens = -1
	if err := controller.SaveSettings(cfg); err == nil || !strings.Contains(err.Error(), "ui.context_limit_tokens") {
		t.Fatalf("negative setting error = %v, want ui.context_limit_tokens", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed save changed file: %v", err)
	}
	if got := controller.CurrentSettings().UI.ContextLimitTokens; got != 270000 {
		t.Fatalf("failed save changed memory: %d", got)
	}
	if err := os.WriteFile(path, []byte(`{"ui":{"context_limit_tokens":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "ui.context_limit_tokens") {
		t.Fatalf("negative file setting error = %v, want ui.context_limit_tokens", err)
	}
}

func TestNormalizePreservesContextLimitTokens(t *testing.T) {
	for _, limit := range []int{-1048576, -1, 0, 1, 270000, 1048576} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.UI.ContextLimitTokens = limit
			if got := Normalize(cfg).UI.ContextLimitTokens; got != limit {
				t.Fatalf("Normalize context limit = %d, want unchanged %d", got, limit)
			}
		})
	}
}

func TestValidateContextLimitTokens(t *testing.T) {
	for _, limit := range []int{-1048576, -1, 0, 1, 270000, 1048576} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.UI.ContextLimitTokens = limit
			err := Validate(cfg)
			if limit < 0 {
				if err == nil || !strings.Contains(err.Error(), "ui.context_limit_tokens") {
					t.Fatalf("Validate error = %v, want ui.context_limit_tokens", err)
				}
			} else if err != nil {
				t.Fatalf("nonnegative context limit %d rejected: %v", limit, err)
			}
		})
	}
}

func TestLoadRejectsNegativeContextLimit(t *testing.T) {
	for _, limit := range []int{-1, -1048576} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			raw := fmt.Sprintf(`{"ui":{"context_limit_tokens":%d}}`, limit)
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "ui.context_limit_tokens") {
				t.Fatalf("Load error = %v, want path and ui.context_limit_tokens", err)
			}
		})
	}
}
