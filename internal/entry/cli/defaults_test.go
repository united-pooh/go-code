package cli

import (
	"testing"
)

func TestTokenTracerDefaultsFromEnv(t *testing.T) {
	t.Setenv("PAW_TOKEN_TRACER", "0")
	if defaultTokenTracerEnabled() {
		t.Fatalf("defaultTokenTracerEnabled() = true, want false when disabled by env")
	}
	t.Setenv("PAW_TOKEN_TRACER", "")
	if !defaultTokenTracerEnabled() {
		t.Fatalf("defaultTokenTracerEnabled() = false, want true by default")
	}
	t.Setenv("PAW_TOKEN_TRACER_OPEN", "true")
	if !defaultTokenTracerOpen() {
		t.Fatalf("defaultTokenTracerOpen() = false, want true")
	}
	t.Setenv("PAW_TOKEN_TRACER_PORT", "")
	if got := defaultTokenTracerPort(); got != 8999 {
		t.Fatalf("defaultTokenTracerPort() = %d, want 8999 by default", got)
	}
	t.Setenv("PAW_TOKEN_TRACER_PORT", "bad")
	if got := defaultTokenTracerPort(); got != 8999 {
		t.Fatalf("defaultTokenTracerPort() = %d, want 8999 for invalid env", got)
	}
	t.Setenv("PAW_TOKEN_TRACER_PORT", "43210")
	if got := defaultTokenTracerPort(); got != 43210 {
		t.Fatalf("defaultTokenTracerPort() = %d, want 43210", got)
	}
}

func TestStreamMADefaultFromEnv(t *testing.T) {
	t.Setenv("PAW_STREAMMA", "0")
	if defaultStreamMAEnabled() {
		t.Fatalf("defaultStreamMAEnabled() = true, want false when disabled by env")
	}
	t.Setenv("PAW_STREAMMA", "off")
	if defaultStreamMAEnabled() {
		t.Fatalf("defaultStreamMAEnabled() = true, want false for off")
	}
	t.Setenv("PAW_STREAMMA", "")
	if !defaultStreamMAEnabled() {
		t.Fatalf("defaultStreamMAEnabled() = false, want true by default")
	}
}
