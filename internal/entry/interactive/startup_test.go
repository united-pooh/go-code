package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"paw/internal/tokentracer"
	"testing"
	"time"
)

func TestClearTerminalWindowWritesClearAndScrollbackSequence(t *testing.T) {
	var output bytes.Buffer

	clearTerminalWindow(&output)

	if got, want := output.String(), "\x1b[H\x1b[2J\x1b[3J"; got != want {
		t.Fatalf("clearTerminalWindow() wrote %q, want %q", got, want)
	}
}

func TestStartTokenTracerStartsDashboardService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, server, err := startTokenTracer(ctx, "session-test", Options{TokenTracerPort: 0})
	if err != nil {
		t.Fatalf("startTokenTracer() error = %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	resp, err := http.Get(server.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snapshot tokentracer.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if snapshot.SessionID != "session-test" || snapshot.ServerURL == "" {
		t.Fatalf("snapshot = %#v, want session and server url", snapshot)
	}
}

// 端口被占时回退到随机空闲端口继续，而不是让会话启动失败。
func TestStartTokenTracerFallsBackWhenPortBusy(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	busyPort := blocker.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, server, err := startTokenTracer(ctx, "session-busy-port", Options{TokenTracerPort: busyPort})
	if err != nil {
		t.Fatalf("startTokenTracer() error = %v, want fallback to free port", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	resp, err := http.Get(server.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state on fallback server error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
