package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"paw/internal/message"
)

func TestRequestTelemetryResponseIdentityAcrossAttempts(t *testing.T) {
	for _, tc := range []struct {
		name, firstID, secondID string
	}{
		{"same response", "resp-shared", "resp-shared"},
		{"different responses", "resp-first", "resp-second"},
		{"no response identity", "", ""},
		{"identity not inherited", "resp-first", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-ID", "header-is-not-response-id")
				id := tc.firstID
				if calls > 1 {
					id = tc.secondID
				}
				fmt.Fprintf(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":%q,\"status\":\"in_progress\"}}\n\n", id)
				if calls == 1 {
					fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":2}}}\n\n")
					return
				}
				fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":5}}}\n\n")
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture", Stream: true, streamSet: true, RetryCount: 1, RetryCountSet: true, Timeout: time.Second})
			var collected telemetryCollector
			client.SetRequestObserver(collected.observe)
			ctx := WithUsageRequestID(context.Background(), "logical-request")
			if err := drainTelemetryStream(client, ctx); err != nil {
				t.Fatal(err)
			}
			attempts := telemetryAttemptSnapshots(collected.snapshot())
			if calls != 2 || len(attempts) != 2 {
				t.Fatalf("calls=%d attempts=%+v", calls, attempts)
			}
			for i, id := range []string{tc.firstID, tc.secondID} {
				attempt := attempts[i+1]
				finality, status, output := "partial", "failed", 2
				if i == 1 {
					finality, status, output = "final", "completed", 5
				}
				if attempt.RequestID != "logical-request" || attempt.ProviderResponseID != id || attempt.UsageFinality != finality || attempt.Status != status || attempt.Usage == nil || attempt.Usage.Breakdown() != (UsageBreakdown{Input: 100, Output: output}) {
					t.Errorf("attempt %d = %+v; want response=%q finality=%s output=%d", i+1, attempt, id, finality, output)
				}
			}
			for _, event := range collected.snapshot() {
				if event.Kind == "attempt_usage" && (event.ProviderResponseID != []string{tc.firstID, tc.secondID}[event.Attempt-1] || event.UsageFinality == "") {
					t.Errorf("usage lacks its response metadata: %+v", event)
				}
			}
		})
	}
}

func TestRequestTelemetryResponseIdentityRemainsLogicalRequestScoped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp-shared","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2}}`)
	}))
	defer server.Close()
	client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture", Stream: false, streamSet: true, Timeout: time.Second})
	var collected telemetryCollector
	client.SetRequestObserver(collected.observe)
	for _, requestID := range []string{"logical-1", "logical-2"} {
		if err := drainTelemetryStream(client, WithUsageRequestID(context.Background(), requestID)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, event := range collected.snapshot() {
		if event.Kind != "attempt_usage" {
			continue
		}
		if event.ProviderResponseID != "resp-shared" || event.UsageFinality != "final" || event.Usage == nil || event.Usage.RequestID != event.RequestID {
			t.Fatalf("response identity replaced logical usage identity: %+v", event)
		}
		seen[event.RequestID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("logical requests merged: %v", seen)
	}
}

func TestRequestTelemetryUsageFinalityFollowsTerminalEvidence(t *testing.T) {
	const chatUsage = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2}}\n\n"
	const anthropicUsage = "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":2}}}\n\n"
	for _, tc := range []struct {
		name, transport, body, finality string
		wantError                       bool
	}{
		{"chat complete", "openai-compatible", chatUsage + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", "final", false},
		{"chat EOF", "openai-compatible", chatUsage, "partial", true},
		{"anthropic complete", "anthropic-compatible", anthropicUsage + "data: {\"type\":\"message_stop\"}\n\n", "final", false},
		{"anthropic EOF preserves legacy Done", "anthropic-compatible", anthropicUsage, "partial", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := NewClient(Config{Transport: tc.transport, APIBaseURL: server.URL, Model: "fixture", Stream: true, streamSet: true, RetryCount: -1, Timeout: time.Second})
			var collected telemetryCollector
			client.SetRequestObserver(collected.observe)
			err := drainTelemetryStream(client, context.Background())
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v wantError=%v", err, tc.wantError)
			}
			attempt := telemetryAttemptSnapshots(collected.snapshot())[1]
			if attempt.Usage == nil || attempt.Usage.Breakdown().Total() != 102 || attempt.UsageFinality != tc.finality {
				t.Fatalf("attempt=%+v; want finality=%s and reported tokens=102", attempt, tc.finality)
			}
		})
	}
}

func TestRequestTelemetryAnthropicStopReasonPreservesFinalUsage(t *testing.T) {
	for _, tc := range []struct {
		stopReason   string
		finishReason FinishReason
	}{
		{"end_turn", FinishReasonStop},
		{"max_tokens", FinishReasonLength},
		{"tool_use", FinishReasonToolCalls},
	} {
		t.Run(tc.stopReason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n")
				fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":5}}\n\n", tc.stopReason)
				fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "anthropic-compatible", APIBaseURL: server.URL, Model: "fixture", Stream: true, streamSet: true, RetryCount: -1, Timeout: time.Second})
			var collected telemetryCollector
			client.SetRequestObserver(collected.observe)
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "fixture"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var usage Usage
			usageEvents, doneEvents := 0, 0
			for event := range events {
				if event.Err != nil {
					t.Fatal(event.Err)
				}
				if event.Usage != nil {
					usage = MergeUsageSnapshot(usage, *event.Usage)
					usageEvents++
				}
				if event.Done {
					doneEvents++
					if event.FinishReason != tc.finishReason {
						t.Errorf("finish reason = %q; want %q", event.FinishReason, tc.finishReason)
					}
				}
			}
			want := UsageBreakdown{Input: 100, Output: 5}
			if usage.Breakdown() != want || usageEvents != 2 || doneEvents != 1 {
				t.Errorf("stream usage=%+v usageEvents=%d doneEvents=%d; want %+v, 2, 1", usage.Breakdown(), usageEvents, doneEvents, want)
			}
			observed := collected.snapshot()
			usageEvents = 0
			for _, event := range observed {
				if event.Kind == "attempt_usage" {
					usageEvents++
				}
			}
			attempt := telemetryAttemptSnapshots(observed)[1]
			if attempt.Usage == nil || attempt.Usage.Breakdown() != want || attempt.UsageFinality != "final" || usageEvents != 2 {
				t.Errorf("observer attempt=%+v usageEvents=%d; want %+v, final, 2", attempt, usageEvents, want)
			}
		})
	}
}

func TestRequestTelemetryFailedResponseUsageRemainsPartial(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, status := range []string{"failed", "incomplete"} {
			t.Run(fmt.Sprintf("%v/%s", stream, status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					response := fmt.Sprintf(`{"id":"resp-failed","status":%q,"output":[],"usage":{"input_tokens":30,"output_tokens":5}}`, status)
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"type\":\"response.%s\",\"response\":%s}\n\n", status, response)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, response)
					}
				}))
				defer server.Close()
				client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture", Stream: stream, streamSet: true, RetryCount: -1, Timeout: time.Second})
				var collected telemetryCollector
				client.SetRequestObserver(collected.observe)
				if err := drainTelemetryStream(client, context.Background()); err == nil {
					t.Fatal("failed response was accepted")
				}
				attempt := telemetryAttemptSnapshots(collected.snapshot())[1]
				if attempt.ProviderResponseID != "resp-failed" || attempt.UsageFinality != "partial" || attempt.Usage == nil || attempt.Usage.Breakdown().Total() != 35 {
					t.Fatalf("failure metadata/usage lost: %+v", attempt)
				}
			})
		}
	}
}

func TestRequestTelemetryResponseTerminalMetadataUsesFailureEvidence(t *testing.T) {
	for _, tc := range []struct{ name, body, status string }{
		{"completed with failed status", `{"type":"response.completed","response":{"status":"failed","usage":{"input_tokens":10,"output_tokens":2}}}`, "failed"},
		{"completed with error", `{"type":"response.completed","response":{"status":"completed","error":{"message":"fixture"},"usage":{"input_tokens":10,"output_tokens":2}}}`, "failed"},
		{"failed without status", `{"type":"response.failed","response":{"usage":{"input_tokens":10,"output_tokens":2}}}`, "failed"},
		{"incomplete without status", `{"type":"response.incomplete","response":{"usage":{"input_tokens":10,"output_tokens":2}}}`, "incomplete"},
		{"error without response", `{"type":"error","error":{"message":"fixture"}}`, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-created\"}}\n\n")
				fmt.Fprintf(w, "data: %s\n\n", tc.body)
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture", Stream: true, streamSet: true, RetryCount: -1, Timeout: time.Second})
			var collected telemetryCollector
			client.SetRequestObserver(collected.observe)
			if err := drainTelemetryStream(client, context.Background()); err == nil {
				t.Fatal("failed response was accepted")
			}
			attempt := telemetryAttemptSnapshots(collected.snapshot())[1]
			if attempt.ProviderResponseID != "resp-created" || attempt.UsageFinality != "partial" || attempt.Status != tc.status {
				t.Fatalf("failure metadata = %+v; want status=%s", attempt, tc.status)
			}
		})
	}
}

func drainTelemetryStream(client *Client, ctx context.Context) error {
	events, err := client.StreamMessage(ctx, []message.Message{{Role: message.RoleUser, Content: "fixture"}}, nil)
	if err != nil {
		return err
	}
	for event := range events {
		if event.Err != nil {
			err = event.Err
		}
	}
	return err
}

func telemetryAttemptSnapshots(events []RequestEvent) map[int]RequestEvent {
	attempts := map[int]RequestEvent{}
	for _, event := range events {
		if event.Attempt == 0 || event.Kind == "request_end" {
			continue
		}
		current := attempts[event.Attempt]
		if event.Kind == "attempt_start" {
			current = event
		}
		if event.ProviderResponseID != "" {
			current.ProviderResponseID = event.ProviderResponseID
		}
		if event.UsageFinality != "" {
			current.UsageFinality = event.UsageFinality
		}
		if event.Kind == "attempt_response" && event.Status != "" {
			current.Status = event.Status
		}
		if event.Usage != nil {
			current.Usage = event.Usage
		}
		attempts[event.Attempt] = current
	}
	return attempts
}
