package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/message"
)

type telemetryCollector struct {
	mu     sync.Mutex
	events []RequestEvent
}

func (c *telemetryCollector) observe(event RequestEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *telemetryCollector) snapshot() []RequestEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]RequestEvent(nil), c.events...)
}

func TestRequestTelemetryTracksActualUsageAndLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"prompt_tokens_details\":{\"cached_tokens\":20}}}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client := NewClient(Config{Transport: "openai-compatible", APIBaseURL: server.URL, Model: "fixture", Timeout: time.Second})
	var collected telemetryCollector
	client.SetRequestObserver(collected.observe)
	ctx := WithRequestScope(WithUsageRequestID(context.Background(), "request-test"), RequestScope{SessionID: "session-test", Purpose: "compaction"})
	events, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "private prompt"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
	seen := collected.snapshot()
	counts := map[string]int{}
	var last *Usage
	for _, event := range seen {
		counts[event.Kind]++
		if event.RequestID != "request-test" || event.Scope.SessionID != "session-test" || event.Scope.Purpose != "compaction" {
			t.Fatalf("scope lost: %+v", event)
		}
		if event.Kind == "attempt_usage" {
			last = event.Usage
			if event.Attempt != 1 {
				t.Fatalf("attempt = %d", event.Attempt)
			}
		}
	}
	if counts["request_start"] != 1 || counts["attempt_start"] != 1 || counts["request_end"] != 1 || counts["attempt_usage"] != 2 {
		t.Fatalf("events = %+v", seen)
	}
	if last == nil || last.Breakdown() != (UsageBreakdown{Input: 80, CacheRead: 20, Output: 5}) {
		t.Fatalf("last = %+v", last)
	}
	if seen[len(seen)-1].Status != "completed" {
		t.Fatalf("last event = %+v", seen[len(seen)-1])
	}
}

func TestRequestTelemetryKeepsUnknownOnHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private body", http.StatusUnauthorized) }))
	defer server.Close()
	client := NewClient(Config{Transport: "openai-compatible", APIBaseURL: server.URL, Model: "fixture", Timeout: time.Second})
	var collected telemetryCollector
	client.SetRequestObserver(collected.observe)
	_, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "private prompt"}}, nil)
	if err == nil {
		t.Fatal("expected HTTP failure")
	}
	seen := collected.snapshot()
	if len(seen) != 4 || seen[0].Kind != "request_start" || seen[3].Kind != "request_end" || seen[3].Status != "failed" {
		t.Fatalf("events = %+v", seen)
	}
	for _, event := range seen {
		if event.Usage != nil {
			t.Fatalf("unknown usage replaced with zero: %+v", event)
		}
	}
}

func TestRequestTelemetryClassifiesPawTextToolsUsingSourceIdentity(t *testing.T) {
	for _, transport := range []string{"openai-compatible", "anthropic-compatible"} {
		t.Run(transport, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "fixture stops after request capture", http.StatusUnauthorized)
			}))
			defer server.Close()
			client := NewClient(Config{Transport: transport, APIBaseURL: server.URL, Model: "fixture", Stream: true, streamSet: true, Timeout: time.Second})
			var collected telemetryCollector
			client.SetRequestObserver(collected.observe)
			call := message.ToolCall{ID: "c1", Name: "Read", Input: json.RawMessage(`{"file_path":"private.txt"}`)}
			result := message.ToolResult{ToolUseID: "c1", Content: "private result"}
			resultJSON, _ := json.Marshal(result)
			resultText := "TOOL_RESULT:\n" + string(resultJSON)
			messages := []message.Message{
				{Role: message.RoleSystem, Content: "private system"},
				{Role: message.RoleUser, Content: resultText}, // User-authored lookalike must stay a prompt.
				{Role: message.RoleAssistant, Content: `{"type":"tool_use","id":"c1","name":"Read","input":{"file_path":"private.txt"}}`, ToolUse: &call},
				{Role: message.RoleUser, Content: resultText, ToolResult: &result},
			}
			_, _ = client.StreamMessage(context.Background(), messages, nil)
			counts := map[string]int{}
			for _, event := range collected.snapshot() {
				if event.Kind != "attempt_start" {
					continue
				}
				for _, atom := range event.Atoms {
					counts[atom.Category]++
					if strings.HasPrefix(atom.Category, "tool_") && atom.ToolName != "Read" {
						t.Errorf("tool name lost: %+v", atom)
					}
				}
			}
			if counts["tool_arguments"] != 1 || counts["tool_result"] != 1 || counts["user_prompt"] != 1 {
				t.Fatalf("Paw text tool classification = %v", counts)
			}
		})
	}
}

func TestRequestTelemetryRetainsResponsesFailureUsageWithoutPublishingUnsafeOutput(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			response := `{"status":"incomplete","usage":{"input_tokens":30,"output_tokens":5},"output":[{"type":"function_call","call_id":"bad","name":"Write","arguments":"{}"}]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.incomplete\",\"response\":%s}\n\n", response)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, response)
				}
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture", Stream: stream, streamSet: true, RetryCount: -1, Timeout: time.Second})
			var collected telemetryCollector
			client.SetRequestObserver(collected.observe)
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "private prompt"}}, nil)
			failed := err != nil
			if err == nil {
				for event := range events {
					failed = failed || event.Err != nil
					if len(event.ToolCalls) > 0 {
						t.Fatal("unsafe output published")
					}
				}
			}
			if !failed {
				t.Fatal("incomplete response accepted")
			}
			var observed *Usage
			for _, event := range collected.snapshot() {
				if event.Kind == "attempt_usage" {
					observed = event.Usage
				}
			}
			if observed == nil || observed.Breakdown().Total() != 35 {
				t.Fatalf("failure consumption lost: %+v", observed)
			}
			wire, err := json.Marshal(collected.snapshot())
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private prompt", "arguments", "bad"} {
				if strings.Contains(string(wire), secret) {
					t.Fatalf("telemetry persisted content: %s", wire)
				}
			}
		})
	}
}
