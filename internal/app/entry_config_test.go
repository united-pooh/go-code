package app

import (
	configv2 "paw/internal/platform/config"
	"testing"
)

func TestEffectiveYoloModeUsesConfigOrCommandLine(t *testing.T) {
	if effectiveYoloMode(false, configv2.Document{}) {
		t.Fatal("YOLO enabled without config or command-line flag")
	}
	if !effectiveYoloMode(false, configv2.Document{Yolo: true}) {
		t.Fatal("config.jsonc YOLO setting was ignored")
	}
	if !effectiveYoloMode(true, configv2.Document{}) {
		t.Fatal("command-line YOLO flag was ignored")
	}
}

func TestConfigOpenOptionsUsesExplicitWorkerMode(t *testing.T) {
	paths := configv2.Paths{
		Home:                "/tmp/paw",
		GlobalConfig:        "/tmp/paw/config.jsonc",
		Settings:            "/tmp/paw/settings.json",
		MCP:                 "/tmp/paw/mcp.toml",
		Skills:              "/tmp/paw/skills",
		Schemas:             "/tmp/paw/schemas",
		Schema:              "/tmp/paw/schemas/config-v2.schema.json",
		ModelDiscoveryCache: "/tmp/paw/model-discovery-cache.json",
		WorkspaceRoot:       "/tmp/workspace",
		WorkspaceConfig:     "/tmp/workspace/.paw/config.jsonc",
	}
	tests := []struct {
		name     string
		context  WorkerContext
		disabled bool
	}{
		{name: "root", context: WorkerContext{}},
		{name: "root ignores depth", context: WorkerContext{Depth: 2}},
		{name: "first-level worker", context: WorkerContext{WorkerMode: true, Depth: 1, MaxDepth: 4}, disabled: true},
		{name: "delegated worker", context: WorkerContext{WorkerMode: true, Depth: 2, MaxDepth: 4}, disabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configOpenOptions(paths, tt.context)
			if got.Paths != paths {
				t.Fatalf("paths changed: got %#v want %#v", got.Paths, paths)
			}
			if got.DisableModelDiscovery != tt.disabled {
				t.Fatalf("DisableModelDiscovery = %v, want %v", got.DisableModelDiscovery, tt.disabled)
			}
			if got.DisableWatch != tt.disabled {
				t.Fatalf("DisableWatch = %v, want %v", got.DisableWatch, tt.disabled)
			}
		})
	}
}
