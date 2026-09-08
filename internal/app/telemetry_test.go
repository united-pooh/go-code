package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/tokentracer"
	"paw/internal/ui/headless"
)

func TestRuntimesPublishIndependentGlobalTelemetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	var runtimes []*WorkspaceRuntime
	for i := 0; i < 3; i++ {
		root := filepath.Join(t.TempDir(), "project")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		runtime, err := BuildWorkspaceRuntime(context.Background(), WorkspaceRuntimeOptions{Root: root, AllowIncomplete: true, Output: headless.New(io.Discard), WorkerContext: WorkerContext{MCPBroker: emptyMCPBroker{}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		if err := runtime.Model.ApplyModelConfig(model.Config{Provider: "fixture", Model: "fixture", Transport: "openai-compatible", APIBaseURL: server.URL, APIPath: "/v1/chat/completions", APIKey: "synthetic", Timeout: time.Second}); err != nil {
			t.Fatal(err)
		}
		runtimes = append(runtimes, runtime)
	}
	var group sync.WaitGroup
	for _, runtime := range runtimes {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := runtime.SessionHost.RunTurn(context.Background(), "private prompt"); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	for _, runtime := range runtimes {
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(runtime.Root, ".paw")); !os.IsNotExist(err) {
			t.Fatalf("workspace .paw created: %v", err)
		}
	}
	snapshot, err := tokentracer.NewLedgerReader(home).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Total.Total() != 36 || len(snapshot.Requests) != 3 {
		t.Fatalf("global telemetry missing: %+v", snapshot)
	}
	for _, request := range snapshot.Requests {
		if request.Scope.SessionID == "" || request.Status != "completed" || len(request.Attempts) != 1 || len(request.Attempts[0].Atoms) == 0 {
			t.Errorf("request incomplete: %+v", request)
		}
	}
}
