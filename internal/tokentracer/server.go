package tokentracer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"paw/internal/platform/pawpath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type ServerConfig struct {
	Host        string
	Port        int
	OpenBrowser bool
}

type Server struct {
	tracer   *Tracer
	cfg      ServerConfig
	server   *http.Server
	url      string
	ledger   *LedgerReader
	done     chan struct{}
	serveErr error
}

func NewServer(tracer *Tracer, cfg ServerConfig) *Server {
	if strings.TrimSpace(cfg.Host) == "" {
		cfg.Host = "127.0.0.1"
	}
	server := &Server{tracer: tracer, cfg: cfg}
	if home, err := pawpath.Home(); err == nil {
		server.ledger = NewLedgerReader(home)
	}
	return server
}

func (s *Server) Start(ctx context.Context) error {
	if s == nil || (s.tracer == nil && s.ledger == nil) {
		return fmt.Errorf("token tracer server requires a tracer or telemetry store")
	}
	host := strings.TrimSpace(s.cfg.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("token tracer listen address must be loopback")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(s.cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("start token tracer listener: %w", err)
	}
	actual := listener.Addr().(*net.TCPAddr)
	s.url = "http://" + net.JoinHostPort(host, strconv.Itoa(actual.Port))
	if s.tracer != nil {
		s.tracer.SetServerURL(s.url)
	}

	s.server = &http.Server{Handler: s.localOnly(s.handler()), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
	s.done = make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-s.done:
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
	}()
	go func() {
		defer close(s.done)
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.serveErr = err
			if s.tracer != nil {
				s.tracer.RecordEvent("server_error", map[string]any{"error": err.Error()})
			}
		}
	}()
	if s.cfg.OpenBrowser {
		openBrowser(s.url)
	}
	return nil
}

func (s *Server) Wait(ctx context.Context) error {
	if s == nil || s.done == nil {
		return fmt.Errorf("token tracer server not started")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.serveErr
	}
}

func (s *Server) URL() string {
	if s == nil {
		return ""
	}
	return s.url
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if s.tracer == nil {
		http.Error(w, "live debug instance unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(s.tracer.Snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.tracer == nil {
		http.Error(w, "live debug instance unavailable", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	events, unsubscribe := s.tracer.Subscribe(true)
	defer unsubscribe()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: token_tracer\n")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
}

func openBrowser(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
