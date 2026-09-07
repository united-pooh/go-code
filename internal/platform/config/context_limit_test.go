package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateDocumentContextWindow(t *testing.T) {
	for _, limit := range []int{-131072, -1, 0, 1, 1048576} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			document := Document{
				SchemaVersion: SchemaVersion,
				Providers: map[string]Provider{
					"local": {Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:1234/v1"},
				},
				Models: map[string]Model{
					"local/chat": {Provider: "local", Name: "chat", ContextWindow: limit},
				},
			}
			_, err := validateDocument(document, "config.jsonc")
			if limit < 0 {
				if err == nil || !strings.Contains(err.Error(), "config.jsonc") || !strings.Contains(err.Error(), "models.local/chat.contextWindow") {
					t.Fatalf("negative contextWindow error = %v, want path and model field", err)
				}
			} else if err != nil {
				t.Fatalf("contextWindow %d rejected: %v", limit, err)
			}
			if got := document.Models["local/chat"].ContextWindow; got != limit {
				t.Fatalf("validation changed contextWindow from %d to %d", limit, got)
			}
		})
	}
}

func TestParseAndValidateGlobalContextWindow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		field   string
		want    int
		wantErr bool
	}{
		{name: "omitted"},
		{name: "positive", field: `,"contextWindow":270000`, want: 270000},
		{name: "legacy positive", field: `,"contextWindow":1048576`, want: 1048576},
		{name: "negative", field: `,"contextWindow":-1`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"schemaVersion":2,"providers":{"local":{"transport":"openai-compatible","endpoint":"http://127.0.0.1:1234/v1"}},"models":{"local/chat":{"provider":"local","name":"chat"` + tc.field + `}}}`)
			document, _, err := parseAndValidateGlobal(raw, "config.jsonc")
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "config.jsonc") || !strings.Contains(err.Error(), "contextWindow") {
					t.Fatalf("invalid contextWindow error = %v, want path and field", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := document.Models["local/chat"].ContextWindow; got != tc.want {
				t.Fatalf("contextWindow = %d, want %d", got, tc.want)
			}
		})
	}
}
