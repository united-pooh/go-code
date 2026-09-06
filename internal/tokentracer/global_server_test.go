package tokentracer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGlobalServerIsIndependentAndRejectsCrossOriginReads(t *testing.T) {
	server := NewGlobalServer(t.TempDir(), ServerConfig{Host: "127.0.0.1"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	client := &http.Client{Timeout: time.Second}
	for _, tc := range []struct {
		path, host, origin string
		status             int
	}{
		{path: "/api/global", status: 200},
		{path: "/api/state", status: 404},
		{path: "/api/global", host: "evil.invalid", status: 403},
		{path: "/api/global", origin: "https://evil.invalid", status: 403},
	} {
		request, err := http.NewRequest(http.MethodGet, server.URL()+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			request.Host = tc.host
		}
		if tc.origin != "" {
			request.Header.Set("Origin", tc.origin)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.status {
			t.Errorf("%+v status=%d", tc, response.StatusCode)
		}
		if response.StatusCode == 200 {
			var snapshot LedgerPage
			if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil || snapshot.Version != globalQueryVersion {
				t.Errorf("snapshot=%+v error=%v", snapshot, err)
			}
		}
		response.Body.Close()
	}
}

func TestGlobalServerRejectsPublicBind(t *testing.T) {
	server := NewGlobalServer(t.TempDir(), ServerConfig{Host: "0.0.0.0"})
	if err := server.Start(context.Background()); err == nil {
		defer server.Shutdown(context.Background())
		t.Fatal("public bind accepted")
	}
}

func TestGlobalServerRequiresExplicitHome(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	server := NewGlobalServer("", ServerConfig{})
	if server.ledger != nil {
		t.Fatal("empty explicit home must not fall back to the environment")
	}
	if err := server.Start(context.Background()); err == nil {
		defer server.Shutdown(context.Background())
		t.Fatal("server started without telemetry storage")
	}
}

func TestGlobalServerWaitReturnsAfterServerStops(t *testing.T) {
	server := NewGlobalServer(t.TempDir(), ServerConfig{})
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Wait(ctx); err != nil {
		t.Fatalf("wait after shutdown: %v", err)
	}
}

func TestGlobalServerQueryDetailExportAndValidation(t *testing.T) {
	server := NewGlobalServer(t.TempDir(), ServerConfig{})
	server.ledger = queryFixture(t, 601)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	client := &http.Client{Timeout: time.Second}
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/api/global?view=requests&period=all&limit=2&offset=300", 200, `"total":601`},
		{"/api/global/request?instance_id=instance-0&request_id=request-000000", 200, `"atoms"`},
		{"/api/global/request?instance_id=instance-1&request_id=request-000000", 404, ""},
		{"/api/global/export?period=all&project=project-0&limit=1", 200, `"requests":201`},
		{"/api/global?view=bogus", 400, ""},
		{"/api/global?period=bogus", 400, ""},
		{"/api/global?limit=251", 400, ""},
		{"/api/global?limit=0", 400, ""},
		{"/api/global?offset=-1", 400, ""},
		{"/api/global?offset=nope", 400, ""},
		{"/api/global?project=one&project=two", 400, ""},
		{"/api/global?typo=foo", 400, ""},
		{"/api/global/request", 400, ""},
	} {
		response, err := client.Get(server.URL() + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != tc.status || !strings.Contains(string(body), tc.contains) {
			t.Errorf("%s: status %d err %v body %.200s", tc.path, response.StatusCode, err, body)
		}
		if tc.status == 200 && response.Header.Get("Cache-Control") != "no-store" {
			t.Error("history response is cacheable")
		}
		if strings.Contains(tc.path, "/export") && response.Header.Get("Content-Disposition") == "" {
			t.Error("export is not an attachment")
		}
	}
}
