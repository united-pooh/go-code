package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"paw/internal/app"
	"paw/internal/capability/mcp"
	"paw/internal/platform/settings"
	"paw/internal/runtime/task"
	"paw/internal/storage/session"
	"paw/internal/tokentracer"
)

func main() {
	binary := flag.String("paw", "", "built Paw executable")
	root := flag.String("root", "", "isolated fixture directory")
	flag.Parse()
	if *binary == "" || *root == "" {
		log.Fatal("paw and root required")
	}
	resolvedRoot, err := filepath.EvalSymlinks(*root)
	check(err)
	*root = resolvedRoot
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	home := filepath.Join(*root, "state")
	check(os.MkdirAll(home, 0700))
	check(os.Setenv("PAW_CONFIG_HOME", home))
	check(os.Setenv("PAW_ENTRY_FIXTURE_KEY", "synthetic"))
	var mu sync.Mutex
	counts := map[string]int{}
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			http.Error(w, "read failed", 400)
			return
		}
		mode := ""
		for _, candidate := range []string{"WORKER", "SERVE", "INTERACTIVE"} {
			if bytes.Contains(body, []byte("PRIVATE_ENTRY_"+candidate)) {
				mode = candidate
			}
		}
		mu.Lock()
		counts[mode]++
		n := counts[mode]
		mu.Unlock()
		if mode == "" || n > 1 {
			http.Error(w, "unexpected fixture request", 401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"ENTRY_OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer modelServer.Close()
	config := fmt.Sprintf(`{"schemaVersion":2,"activeModel":"fixture/chat","providers":{"fixture":{"transport":"openai-compatible","endpoint":%q,"auth":{"env":["PAW_ENTRY_FIXTURE_KEY"]}}},"models":{"fixture/chat":{"provider":"fixture","name":"fixture-model"}}}`, modelServer.URL+"/v1")
	check(os.WriteFile(filepath.Join(home, "config.jsonc"), []byte(config), 0600))
	for _, name := range []string{"worker", "serve", "interactive"} {
		check(os.MkdirAll(filepath.Join(*root, name), 0700))
	}
	workerOK := runWorker(ctx, *binary, filepath.Join(*root, "worker"))
	serveOK := runServe(ctx, *binary, filepath.Join(*root, "serve"), home)
	log.Printf("ENTRY_READY worker=%v serve=%v interactive_workspace=%s config_home=%s", workerOK, serveOK, filepath.Join(*root, "interactive"), home)
	reader := tokentracer.NewLedgerReader(home)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Fatal("three completed entry requests were not recorded within fixture deadline")
		case <-ticker.C:
			snapshot, err := reader.Snapshot()
			check(err)
			if len(snapshot.Requests) != 3 {
				continue
			}
			complete := true
			for _, request := range snapshot.Requests {
				complete = complete && request.Status == "completed"
			}
			if !complete {
				continue
			}
			privacy, isolated := true, true
			check(filepath.WalkDir(filepath.Join(home, "tracer"), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if bytes.Contains(body, []byte("PRIVATE_ENTRY_")) || bytes.Contains(body, []byte("ENTRY_OK")) {
					privacy = false
				}
				return nil
			}))
			for _, name := range []string{"worker", "serve", "interactive"} {
				if _, err := os.Lstat(filepath.Join(*root, name, ".paw")); !os.IsNotExist(err) {
					isolated = false
				}
			}
			mu.Lock()
			calls := counts["WORKER"] + counts["SERVE"] + counts["INTERACTIVE"]
			mu.Unlock()
			ownership := false
			for _, request := range snapshot.Requests {
				if request.Scope.TaskID == "entry-worker-task" && request.Scope.ParentSessionID == "entry-parent" && request.Scope.Purpose == "worker" {
					ownership = true
				}
			}
			valid := workerOK && serveOK && ownership && privacy && isolated && calls == 3 && snapshot.Total.Total() == 36
			report := map[string]any{"valid": valid, "worker": workerOK, "serve": serveOK, "interactive": true, "requests": 3, "model_http_calls": calls, "total": snapshot.Total, "privacy": privacy, "workspace_isolation": isolated, "worker_ownership": ownership, "recorded_at": time.Now()}
			data, _ := json.MarshalIndent(report, "", "  ")
			check(os.WriteFile(filepath.Join(*root, "verification.json"), data, 0600))
			log.Printf("VERIFICATION %s", data)
			if !valid {
				os.Exit(1)
			}
			return
		}
	}
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func runWorker(ctx context.Context, binary, workspace string) bool {
	store, err := session.NewJSONLStoreForWorkspace(workspace)
	check(err)
	_, err = store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "entry-worker-session"})
	check(err)
	command := exec.CommandContext(ctx, binary, "--task-worker")
	command.Dir, command.Env, command.Stderr = workspace, os.Environ(), os.Stderr
	input, err := command.StdinPipe()
	check(err)
	output, err := command.StdoutPipe()
	check(err)
	check(command.Start())
	defer input.Close()
	request := task.WorkerRequest{TaskID: "entry-worker-task", SessionID: "entry-worker-session", ParentSessionID: "entry-parent", Prompt: "PRIVATE_ENTRY_WORKER", ContextMode: settings.ContextModeEmpty, RunMode: settings.RunModeSync, Depth: 1, MaxDepth: 2}
	check(json.NewEncoder(input).Encode(task.NewWorkerStartMessage(request, mcp.Snapshot{})))
	decoder := json.NewDecoder(io.LimitReader(output, 2<<20))
	starts, usages := 0, 0
	startID, usageID := "", ""
	for {
		var message task.WorkerMessage
		check(decoder.Decode(&message))
		if message.Event != nil {
			if message.Event.RequestStartID != "" {
				starts++
				startID = message.Event.RequestStartID
			}
			if message.Event.Usage != nil {
				usages++
				usageID = message.Event.Usage.RequestID
			}
		}
		if message.Type == task.WorkerMessageResult {
			check(command.Wait())
			log.Printf("WORKER_DIAGNOSTIC exit=%d error=%q content=%q starts=%d usages=%d start_id=%q usage_id=%q", message.ExitCode, message.Error, message.Content, starts, usages, startID, usageID)
			return message.ExitCode == 0 && message.Content == "ENTRY_OK" && starts == 1 && usages == 1 && startID == usageID
		}
	}
}

func runServe(ctx context.Context, binary, workspace, home string) bool {
	command := exec.CommandContext(ctx, binary, "serve", "--listen", "127.0.0.1:0")
	command.Dir, command.Env, command.Stderr = workspace, os.Environ(), os.Stderr
	output, err := command.StdoutPipe()
	check(err)
	check(command.Start())
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "Paw workbench: ") {
				lines <- strings.TrimPrefix(scanner.Text(), "Paw workbench: ")
				return
			}
		}
		close(lines)
	}()
	var location string
	select {
	case location = <-lines:
	case <-ctx.Done():
		check(ctx.Err())
	}
	if location == "" {
		log.Fatal("serve did not publish its local URL")
	}
	address, err := url.Parse(location)
	check(err)
	token := strings.TrimPrefix(address.Fragment, "bootstrap=")
	address.Fragment, address.Path = "", ""
	base := address.String()
	jar, err := cookiejar.New(nil)
	check(err)
	client := &http.Client{Timeout: 5 * time.Second, Jar: jar}
	call := func(method, route string, input, output any) {
		body, err := json.Marshal(input)
		check(err)
		request, err := http.NewRequestWithContext(ctx, method, base+route, bytes.NewReader(body))
		check(err)
		request.Header.Set("Origin", base)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		check(err)
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			data, _ := io.ReadAll(io.LimitReader(response.Body, 1000))
			log.Fatalf("serve %s: %d %s", route, response.StatusCode, data)
		}
		if output != nil {
			check(json.NewDecoder(response.Body).Decode(output))
		}
	}
	call("POST", "/api/auth/exchange", map[string]string{"token": token}, nil)
	var bootstrap struct {
		Loaded []app.RecentWorkspace `json:"loaded_workspaces"`
	}
	call("GET", "/api/bootstrap", nil, &bootstrap)
	if len(bootstrap.Loaded) != 1 {
		log.Fatal("serve workspace not loaded")
	}
	workspaceURL := "/api/workspaces/" + string(bootstrap.Loaded[0].ID)
	sessionURL := workspaceURL + "/sessions/entry-serve-session"
	call("POST", workspaceURL+"/sessions", map[string]string{"command_id": "entry-create", "session_id": "entry-serve-session"}, nil)
	var snapshot app.SessionSnapshot
	call("GET", sessionURL, nil, &snapshot)
	call("POST", sessionURL+"/messages", map[string]any{"command_id": "entry-submit", "session_version": snapshot.SessionVersion, "text": "PRIVATE_ENTRY_SERVE"}, nil)
	reader := tokentracer.NewLedgerReader(home)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			check(ctx.Err())
		case <-ticker.C:
			ledger, err := reader.Snapshot()
			check(err)
			for _, request := range ledger.Requests {
				if request.Scope.SessionID == "entry-serve-session" && request.Status == "completed" {
					call("POST", workspaceURL+"/close", map[string]any{}, nil)
					return request.Scope.TurnID != "" && len(request.Attempts) == 1
				}
			}
		}
	}
}
