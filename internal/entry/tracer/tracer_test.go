package tracer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTracerOptionsAndIndependentStartup(t *testing.T) {
	if _, err := ParseOptions([]string{"--listen", "0.0.0.0:9000"}); err == nil {
		t.Fatal("public listen accepted")
	}
	if _, err := ParseOptions([]string{"project"}); err == nil {
		t.Fatal("positional argument accepted")
	}
	if _, err := ParseOptions([]string{"--listen", "127.0.0.1:abc"}); err == nil {
		t.Fatal("invalid port accepted")
	}
	opts, err := ParseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	finished := make(chan error, 1)
	go func() { finished <- runTracer(ctx, opts, writer); writer.Close() }()
	line := make([]byte, 512)
	n, err := reader.Read(line)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line[:n]), "http://127.0.0.1:") {
		t.Fatalf("URL missing: %q", line[:n])
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}
	for _, path := range []string{"config.jsonc", "projects", "tracer/instances"} {
		if _, err := os.Stat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Errorf("read-only tracer initialized runtime path %s: %v", path, err)
		}
	}
}
