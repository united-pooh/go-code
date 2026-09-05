package model

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"paw/internal/message"
)

func TestResponsesRetriesTransportInterruptionWithoutDuplicateOutput(t *testing.T) {
	for _, mode := range []string{"text", "thinking", "buffered_tool", "mismatch", "short_replay"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "text/event-stream")
				if requests == 1 {
					switch mode {
					case "thinking":
						fmt.Fprint(w, "data: {\"type\":\"response.reasoning.delta\",\"delta\":\"plan\"}\n\n")
					case "buffered_tool":
						fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"partial\",\"call_id\":\"partial\",\"name\":\"Write\",\"arguments\":\"\"}}\n\n")
					default:
						fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
					}
					return
				}
				switch mode {
				case "thinking":
					fmt.Fprint(w, "data: {\"type\":\"response.reasoning.delta\",\"delta\":\"pl\"}\n\ndata: {\"type\":\"response.reasoning.delta\",\"delta\":\"an done\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
				case "buffered_tool":
					fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"final","name":"Read","arguments":"{}"}]}}`+"\n\n")
				case "mismatch":
					fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"wrong"}`+"\n\n"+`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
				case "short_replay":
					fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"hel"}`+"\n\n"+`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
				default:
					fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"he"}`+"\n\n"+`data: {"type":"response.output_text.delta","delta":"llo world"}`+"\n\n"+`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
				}
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 2, RetryCountSet: true})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var text, thinking string
			var gotErr error
			var calls []message.ToolCall
			done := 0
			for event := range events {
				text += event.Delta
				thinking += event.Thinking
				calls = append(calls, event.ToolCalls...)
				if event.Err != nil {
					gotErr = event.Err
				}
				if event.Done {
					done++
				}
			}
			if requests != 2 {
				t.Fatalf("requests=%d, want 2; err=%v", requests, gotErr)
			}
			switch mode {
			case "mismatch", "short_replay":
				if gotErr == nil || done != 0 || text != "hello" || len(calls) != 0 {
					t.Fatalf("unsafe replay text=%q done=%d calls=%v err=%v", text, done, calls, gotErr)
				}
			case "thinking":
				if gotErr != nil || thinking != "plan done" || done != 1 {
					t.Fatalf("thinking=%q done=%d err=%v", thinking, done, gotErr)
				}
			case "buffered_tool":
				if gotErr != nil || len(calls) != 1 || calls[0].ID != "final" || done != 1 {
					t.Fatalf("calls=%v done=%d err=%v", calls, done, gotErr)
				}
			default:
				if gotErr != nil || text != "hello world" || done != 1 {
					t.Fatalf("text=%q done=%d err=%v", text, done, gotErr)
				}
			}
		})
	}
}

type responsesChunkReader struct{ chunks []string }

func (r *responsesChunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

func TestResponsesSSECRLFAcrossReadBoundaries(t *testing.T) {
	scanner := bufio.NewScanner(&responsesChunkReader{chunks: []string{"event: response.completed\r", "\ndata: {\"type\":\r", "\ndata: \"response.completed\"}\r", "\n\r", "\n"}})
	scanner.Split(splitResponsesSSEEvents)
	var payloads []string
	for scanner.Scan() {
		if payload, ok := responsesSSEPayload(scanner.Bytes()); ok {
			payloads = append(payloads, string(payload))
		}
	}
	if scanner.Err() != nil || len(payloads) != 1 || payloads[0] != "{\"type\":\n\"response.completed\"}" {
		t.Fatalf("payloads=%q err=%v", payloads, scanner.Err())
	}
}

func TestResponsesFailureIncludesDiagnosticClassification(t *testing.T) {
	failure := &responsesStreamFailure{EventType: "stream_read", Message: "读取 Responses 流式响应失败", RequestID: "req-test", Retryable: true, cause: io.ErrUnexpectedEOF}
	text := failure.Error()
	for _, want := range []string{"stream_read", "unexpected EOF", "req-test"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if !errors.Is(failure, io.ErrUnexpectedEOF) {
		t.Fatal("lost cause")
	}
}
