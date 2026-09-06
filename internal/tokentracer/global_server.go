package tokentracer

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func NewGlobalServer(home string, cfg ServerConfig) *Server {
	server := NewServer(nil, cfg)
	if home == "" {
		server.ledger = nil
	} else {
		server.ledger = NewLedgerReader(home)
	}
	return server
}

func (s *Server) handleGlobal(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		http.Error(w, "telemetry storage unavailable", http.StatusServiceUnavailable)
		return
	}
	query, err := parseGlobalQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := s.ledger.Query(query)
	if err != nil {
		http.Error(w, "telemetry storage read failed", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		LedgerPage
		DebugAvailable bool `json:"debug_available"`
	}{page, s.tracer != nil})
}

func parseGlobalQuery(r *http.Request) (LedgerQuery, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return LedgerQuery{}, err
	}
	q := LedgerQuery{View: values.Get("view"), Period: values.Get("period"), Project: values.Get("project"), Session: values.Get("session"), Model: values.Get("model"), Tool: values.Get("tool"), Search: values.Get("search")}
	for key, items := range values {
		if len(items) != 1 {
			return q, fmt.Errorf("duplicate query parameter")
		}
		switch key {
		case "view", "period", "project", "session", "model", "tool", "search":
		case "offset", "limit":
			n, err := strconv.Atoi(items[0])
			if err != nil || n < 0 || key == "limit" && n == 0 {
				return q, fmt.Errorf("invalid pagination")
			}
			if key == "offset" {
				q.Offset = n
			} else {
				q.Limit = n
			}
		default:
			return q, fmt.Errorf("unknown query parameter")
		}
	}
	return q.normalized()
}

func (s *Server) handleGlobalRequest(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		http.Error(w, "telemetry storage unavailable", http.StatusServiceUnavailable)
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 2 || len(values["instance_id"]) != 1 || len(values["request_id"]) != 1 || values.Get("instance_id") == "" || values.Get("request_id") == "" || len(values.Get("instance_id")) > 256 || len(values.Get("request_id")) > 256 {
		http.Error(w, "instance_id and request_id required", http.StatusBadRequest)
		return
	}
	request, err := s.ledger.Request(values.Get("instance_id"), values.Get("request_id"))
	if err != nil {
		http.Error(w, "telemetry storage read failed", http.StatusServiceUnavailable)
		return
	}
	if request == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(request)
}

func (s *Server) handleGlobalExport(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		http.Error(w, "telemetry storage unavailable", http.StatusServiceUnavailable)
		return
	}
	query, err := parseGlobalQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	snapshot, err := s.ledger.Export(query)
	if err != nil {
		http.Error(w, "telemetry storage read failed", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="paw-token-tracer.json"`)
	_ = json.NewEncoder(w).Encode(struct {
		LedgerSnapshot
		Filters LedgerQuery `json:"filters"`
	}{snapshot, query})
}

func (s *Server) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'")
		host, _, err := net.SplitHostPort(r.Host)
		ip := net.ParseIP(host)
		if err != nil || (!strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback())) || "http://"+r.Host != s.url {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); (origin != "" && origin != s.url) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-origin read denied", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read-only service", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}
