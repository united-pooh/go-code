package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"paw/internal/tokentracer"
)

func main() {
	binary := flag.String("paw", "", "built Paw executable")
	root := flag.String("root", "", "isolated fixture directory")
	port := flag.Int("port", 0, "dashboard port")
	flag.Parse()
	if *binary == "" || *root == "" {
		log.Fatal("paw and root are required")
	}
	home := filepath.Join(*root, "state")
	if err := os.MkdirAll(home, 0700); err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	failed := 0
	ready := make(chan struct{})
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
			http.Error(w, "bad JSON", 400)
			return
		}
		mu.Lock()
		calls++
		number := calls
		if calls == 3 {
			close(ready)
		}
		mu.Unlock()
		if number > 12 {
			http.Error(w, "fixture request limit exceeded", 401)
			return
		}
		select {
		case <-ready:
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
			http.Error(w, "concurrent projects did not arrive", 401)
			return
		}
		toolResult := false
		for _, msg := range body.Messages {
			if msg.Role == "user" && strings.HasPrefix(msg.Content, "TOOL_RESULT:\n") {
				var result struct {
					ToolUseID string `json:"tool_use_id"`
					Content   string `json:"content"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(msg.Content, "TOOL_RESULT:\n")), &result) == nil {
					toolResult = toolResult || result.ToolUseID == "read-fixture" && strings.Contains(result.Content, "SECRET_TOOL_RESULT_DO_NOT_STORE")
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if toolResult {
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":700,\"completion_tokens\":50,\"prompt_tokens_details\":{\"cached_tokens\":200},\"completion_tokens_details\":{\"reasoning_tokens\":10}}}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture complete.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":500,\"completion_tokens\":30,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"read-fixture\",\"type\":\"function\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"file_path\\\":\\\"sample.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer modelServer.Close()
	config := fmt.Sprintf(`{"schemaVersion":2,"activeModel":"fixture/chat","providers":{"fixture":{"transport":"openai-compatible","endpoint":%q,"auth":{"env":["PAW_TRACER_FIXTURE_KEY"]}}},"models":{"fixture/chat":{"provider":"fixture","name":"fixture-model"}}}`, modelServer.URL+"/v1")
	if err := os.WriteFile(filepath.Join(home, "config.jsonc"), []byte(config), 0600); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := tokentracer.NewGlobalServer(home, tokentracer.ServerConfig{Host: "127.0.0.1", Port: *port})
	if err := server.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("DASHBOARD %s", server.URL())
	var group sync.WaitGroup
	for _, name := range []string{"paw-core", "agent-browser", "research-notes"} {
		workspace := filepath.Join(*root, name)
		if err := os.MkdirAll(workspace, 0700); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "sample.txt"), []byte("SECRET_TOOL_RESULT_DO_NOT_STORE"), 0600); err != nil {
			log.Fatal(err)
		}
		group.Add(1)
		go func() {
			defer group.Done()
			processCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			defer stop()
			command := exec.CommandContext(processCtx, *binary, "-p", "PRIVATE_PROMPT_DO_NOT_STORE: inspect sample.txt")
			command.Dir = workspace
			command.Env = append(os.Environ(), "PAW_CONFIG_HOME="+home, "PAW_TRACER_FIXTURE_KEY=synthetic")
			output, err := command.CombinedOutput()
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				if len(output) > 2000 {
					output = output[len(output)-2000:]
				}
				log.Printf("PROCESS %s FAILED: %v %s", name, err, output)
				return
			}
			log.Printf("PROCESS %s exited successfully", name)
		}()
	}
	group.Wait()
	snapshot, err := tokentracer.NewLedgerReader(home).Snapshot()
	if err != nil {
		log.Fatal(err)
	}
	mu.Lock()
	requests := calls
	mu.Unlock()
	toolAtoms := 0
	for _, request := range snapshot.Requests {
		for _, attempt := range request.Attempts {
			for _, atom := range attempt.Atoms {
				if atom.Category == "tool_result" && atom.ToolName == "Read" {
					toolAtoms++
				}
			}
		}
	}
	stopped := 0
	for _, instance := range snapshot.Instances {
		if instance.Status == "stopped" {
			stopped++
		}
	}
	isolated := true
	for _, name := range []string{"paw-core", "agent-browser", "research-notes"} {
		if _, err := os.Lstat(filepath.Join(*root, name, ".paw")); !os.IsNotExist(err) {
			isolated = false
		}
	}
	replayed, replayErr := tokentracer.NewLedgerReader(home).Snapshot()
	replayValid := replayErr == nil && replayed.Total == snapshot.Total && len(replayed.Requests) == len(snapshot.Requests)
	valid := len(snapshot.Requests) == 6 && snapshot.Total.Total() == 3840 && requests == 6 && failed == 0 && toolAtoms == 3 && stopped == 3 && isolated && replayValid
	privacy := true
	_ = filepath.WalkDir(filepath.Join(home, "tracer"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			privacy = false
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || strings.Contains(string(data), "PRIVATE_PROMPT_DO_NOT_STORE") || strings.Contains(string(data), "SECRET_TOOL_RESULT_DO_NOT_STORE") {
			privacy = false
		}
		return nil
	})
	result := map[string]any{"valid": valid && privacy, "privacy": privacy, "requests": len(snapshot.Requests), "model_http_calls": requests, "total": snapshot.Total, "instances": len(snapshot.Instances), "tool_result_atoms": toolAtoms, "stopped_instances": stopped, "workspace_isolation": isolated, "replay_valid": replayValid, "dashboard": server.URL(), "recorded_at": time.Now()}
	data, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(filepath.Join(*root, "verification.json"), data, 0600); err != nil {
		log.Fatal(err)
	}
	log.Printf("VERIFICATION %s", data)
	if !valid || !privacy {
		os.Exit(1)
	}
	<-ctx.Done()
}
