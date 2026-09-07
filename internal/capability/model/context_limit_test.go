package model

import (
	"reflect"
	"testing"
)

func TestResolveContextLimitTokens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		general int
		want    int
	}{
		{
			name: "model map beats scalar general and metadata",
			cfg: Config{Model: "gpt-5", ContextLimitTokens: 200000,
				ModelContextLimitTokens: map[string]int{"gpt-5": 300000}},
			general: 270000, want: 300000,
		},
		{
			name: "model map matches trimmed model",
			cfg: Config{Model: "  gpt-5 \t", ContextLimitTokens: 200000,
				ModelContextLimitTokens: map[string]int{"gpt-5": 300000}},
			general: 270000, want: 300000,
		},
		{
			name: "unmatched map falls through to explicit scalar",
			cfg: Config{Model: "gpt-5", ContextLimitTokens: 200000,
				ModelContextLimitTokens: map[string]int{"other-model": 300000}},
			general: 270000, want: 200000,
		},
		{
			name: "zero map falls through to explicit scalar",
			cfg: Config{Model: "gpt-5", ContextLimitTokens: 200000,
				ModelContextLimitTokens: map[string]int{"gpt-5": 0}},
			general: 270000, want: 200000,
		},
		{
			name: "negative map falls through to explicit scalar",
			cfg: Config{Model: "gpt-5", ContextLimitTokens: 200000,
				ModelContextLimitTokens: map[string]int{"gpt-5": -1}},
			general: 270000, want: 200000,
		},
		{
			name:    "scalar beats larger general and metadata",
			cfg:     Config{Model: "gpt-5", ContextLimitTokens: 200000},
			general: 1048576, want: 200000,
		},
		{
			name:    "scalar beats smaller general and metadata",
			cfg:     Config{Model: "gpt-5", ContextLimitTokens: 500000},
			general: 1, want: 500000,
		},
		{
			name: "general beats larger metadata", cfg: Config{Model: "gpt-5"},
			general: 270000, want: 270000,
		},
		{
			name: "general beats smaller metadata", cfg: Config{Model: "gpt-5"},
			general: 1048576, want: 1048576,
		},
		{
			name: "nonpositive model limits fall through to general",
			cfg: Config{Model: "gpt-5", ContextLimitTokens: -2,
				ModelContextLimitTokens: map[string]int{"gpt-5": -1}},
			general: 270000, want: 270000,
		},
		{
			name:    "unmatched map falls through to general",
			cfg:     Config{Model: "gpt-5", ModelContextLimitTokens: map[string]int{"other-model": 300000}},
			general: 270000, want: 270000,
		},
		{
			name: "auto uses metadata", cfg: Config{Provider: "openai", Model: "gpt-5"},
			want: 400000,
		},
		{
			name: "metadata alias normalization", cfg: Config{Provider: "gateway", Model: "  openai/GPT-5  "},
			want: 400000,
		},
		{
			name: "negative general uses metadata", cfg: Config{Model: "gpt-5"},
			general: -1, want: 400000,
		},
		{
			name: "nonpositive model limits use metadata",
			cfg: Config{Model: "gpt-5", ContextLimitTokens: -2,
				ModelContextLimitTokens: map[string]int{"gpt-5": 0}},
			general: -1, want: 400000,
		},
		{
			name: "unknown model uses general", cfg: Config{Model: "unknown-context-model"},
			general: 270000, want: 270000,
		},
		{
			name: "unknown model uses fallback", cfg: Config{Model: "unknown-context-model"},
			want: 131072,
		},
		{
			name: "nonpositive unknown limits use fallback",
			cfg: Config{Model: "unknown-context-model", ContextLimitTokens: -2,
				ModelContextLimitTokens: map[string]int{"unknown-context-model": -1}},
			general: -1, want: 131072,
		},
		{name: "empty config uses fallback", want: 131072},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveContextLimitTokens(tc.cfg, tc.general); got != tc.want {
				t.Fatalf("ResolveContextLimitTokens = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEffectiveContextLimitTokensAlias(t *testing.T) {
	for _, cfg := range []Config{
		{Model: " gpt-5 ", ContextLimitTokens: 200000, ModelContextLimitTokens: map[string]int{"gpt-5": 300000}},
		{Model: "gpt-5", ContextLimitTokens: 200000},
		{Model: "gpt-5"},
		{Model: "unknown-context-model"},
		{},
	} {
		if got, want := EffectiveContextLimitTokens(cfg), ResolveContextLimitTokens(cfg, 0); got != want {
			t.Errorf("EffectiveContextLimitTokens(%+v) = %d, want resolver with auto: %d", cfg, got, want)
		}
	}
}

func TestResolveContextLimitTokensDoesNotMutateConfig(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		modelName               string
		mapped, scalar, general int
	}{
		{"map", "gpt-5", 300000, 200000, 270000},
		{"scalar", "gpt-5", 0, 200000, 270000},
		{"general", "gpt-5", -1, 0, 270000},
		{"metadata", "gpt-5", -1, -2, 0},
		{"fallback", "unknown-context-model", -1, -2, -3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			makeConfig := func() Config {
				return Config{
					Provider: " gateway ", Model: "  " + tc.modelName + "  ",
					Models:                  []string{tc.modelName, " other ", tc.modelName},
					ContextLimitTokens:      tc.scalar,
					ModelContextLimitTokens: map[string]int{tc.modelName: tc.mapped, " untouched ": -10},
					Headers:                 map[string]string{"X-Test": "original"},
					Proxy:                   &ProxyConfig{Mode: ProxyModeDirect},
					ExtraBody:               RequestBody{"metadata": map[string]any{"team": "platform"}},
					ModelExtraBody:          map[string]RequestBody{tc.modelName: {"service_tier": "fast"}},
					Profiles:                []Profile{{Model: " profile-model ", ModelContextLimitTokens: map[string]int{"profile-model": 123}}},
				}
			}
			cfg, before := makeConfig(), makeConfig()
			ResolveContextLimitTokens(cfg, tc.general)
			if !reflect.DeepEqual(cfg, before) {
				t.Fatalf("resolver mutated config: got %+v, want %+v", cfg, before)
			}
			EffectiveContextLimitTokens(cfg)
			if !reflect.DeepEqual(cfg, before) {
				t.Fatalf("compatibility wrapper mutated config: got %+v, want %+v", cfg, before)
			}
		})
	}
}

func TestConfiguredProfilesPreservesContextLimits(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "synthetic"
		if configured {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) {
			cfg := Config{
				Model:                   "model-a",
				ContextLimitTokens:      200000,
				ModelContextLimitTokens: map[string]int{"model-a": 300000, "model-b": 0, "model-c": -1},
			}
			if configured {
				cfg.Profiles = []Profile{{
					Model:                   cfg.Model,
					ContextLimitTokens:      cfg.ContextLimitTokens,
					ModelContextLimitTokens: cfg.ModelContextLimitTokens,
				}}
			}
			profiles := ConfiguredProfiles(cfg)
			if len(profiles) != 1 {
				t.Fatalf("ConfiguredProfiles returned %d profiles, want 1", len(profiles))
			}
			profile := profiles[0]
			if profile.ContextLimitTokens != cfg.ContextLimitTokens || !reflect.DeepEqual(profile.ModelContextLimitTokens, cfg.ModelContextLimitTokens) {
				t.Fatalf("profile context limits = %d/%v, want %d/%v", profile.ContextLimitTokens, profile.ModelContextLimitTokens, cfg.ContextLimitTokens, cfg.ModelContextLimitTokens)
			}
			resolved := profile.Config()
			if resolved.ContextLimitTokens != cfg.ContextLimitTokens || !reflect.DeepEqual(resolved.ModelContextLimitTokens, cfg.ModelContextLimitTokens) {
				t.Fatalf("round-trip context limits = %d/%v, want %d/%v", resolved.ContextLimitTokens, resolved.ModelContextLimitTokens, cfg.ContextLimitTokens, cfg.ModelContextLimitTokens)
			}
			if got := EffectiveContextLimitTokens(resolved); got != 300000 {
				t.Fatalf("round-trip model limit = %d, want 300000", got)
			}
			resolved.Model = "model-b"
			if got := EffectiveContextLimitTokens(resolved); got != 200000 {
				t.Fatalf("round-trip scalar limit = %d, want 200000", got)
			}
			resolved.ModelContextLimitTokens["model-a"] = 1
			if profile.ModelContextLimitTokens["model-a"] != 300000 {
				t.Fatal("Profile.Config shared context limits with its source")
			}
			profile.ModelContextLimitTokens["model-a"] = 2
			if cfg.ModelContextLimitTokens["model-a"] != 300000 {
				t.Fatal("ConfiguredProfiles shared context limits with its source")
			}
		})
	}

	profile := ConfiguredProfiles(Config{ContextLimitTokens: 200000})[0]
	if profile.ContextLimitTokens != 200000 || profile.ModelContextLimitTokens != nil {
		t.Fatalf("scalar-only fallback = %d/%v, want 200000/nil", profile.ContextLimitTokens, profile.ModelContextLimitTokens)
	}
}
