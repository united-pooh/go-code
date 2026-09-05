package model

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"paw/internal/message"
)

func TestResponsesRetryBudgetCoversHTTPAndStreamFailures(t *testing.T) {
	for _, budget := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if count == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, `data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"partial","name":"Write"}}`+"\n\n")
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: budget, RetryCountSet: true})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
			calls, done := 0, 0
			if err == nil {
				for event := range events {
					calls += len(event.ToolCalls)
					if event.Done {
						done++
					}
					if event.Err != nil {
						err = event.Err
					}
				}
			}
			if count != budget+1 || calls != 0 || done != 0 || err == nil {
				t.Fatalf("requests=%d calls=%d done=%d err=%v", count, calls, done, err)
			}
		})
	}
}

func TestResponsesRequestAttemptsHonorRetryAfterAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	attempts := responsesRequestAttempts{client: server.Client(), limit: 3, build: func() (*http.Request, error) { return http.NewRequestWithContext(ctx, "POST", server.URL, nil) }}
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	_, err := attempts.open(ctx)
	if err != context.Canceled || calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("calls=%d err=%v elapsed=%v", calls, err, time.Since(start))
	}
}

func TestRetryableConnectionEOF(t *testing.T) {
	if !isRetryableRequestError(context.Background(), io.EOF) || !isRetryableRequestError(context.Background(), io.ErrUnexpectedEOF) {
		t.Fatal("connection EOF must be retryable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if isRetryableRequestError(ctx, io.EOF) {
		t.Fatal("canceled EOF must not retry")
	}
}

func TestResponsesFrameLimitIsNotRetried(t *testing.T) {
	err := responsesReadFailure(bufio.ErrTooLong, http.Header{})
	if err.EventType != "frame_limit" || canRetryResponsesStream(context.Background(), err, false) {
		t.Fatalf("failure=%+v", err)
	}
}
