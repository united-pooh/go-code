package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"paw/internal/message"
)

func TestResponsesCompletionRejectsContradictorySnapshot(t *testing.T) {
	for _, ending := range []string{
		`{"type":"response.completed","response":{"status":"incomplete","output":[]}}`,
		`{"type":"response.completed","response":{"error":{"message":"failed"},"output":[]}}`,
		`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"different"}]}]}}`,
	} {
		t.Run(ending, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"prefix"}`+"\n\n"+"data: {broken\n\n"+"data: "+ending+"\n\n")
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 0, RetryCountSet: true})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			text, done, failed := "", false, false
			for event := range events {
				text += event.Delta
				done = done || event.Done
				failed = failed || event.Err != nil
				if len(event.ToolCalls) > 0 {
					t.Fatal("published tools before validation")
				}
			}
			if text != "prefix" || done || !failed {
				t.Fatalf("text=%q done=%v failed=%v", text, done, failed)
			}
		})
	}
}

func TestResponsesCorruptToolDeltaUsesOnlyAuthoritativeSnapshot(t *testing.T) {
	for _, snapshot := range []bool{true, false} {
		t.Run(fmt.Sprint(snapshot), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item","call_id":"call","name":"Read","arguments":""}}`+"\n\n")
				fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"file_path\":\""}`+"\n\n")
				fmt.Fprint(w, "data: {corrupt\n\n")
				fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"file.txt\"}"}`+"\n\n")
				if snapshot {
					fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"call","name":"Read","arguments":"{\"file_path\":\"important/file.txt\"}"}]}}`+"\n\n")
				} else {
					fmt.Fprint(w, `data: {"type":"response.completed"}`+"\n\n")
				}
			}))
			defer server.Close()
			client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test", RetryCount: 0, RetryCountSet: true})
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var calls []message.ToolCall
			done, failed := false, false
			for event := range events {
				calls = append(calls, event.ToolCalls...)
				done = done || event.Done
				failed = failed || event.Err != nil
			}
			if snapshot {
				if !done || failed || len(calls) != 1 || string(calls[0].Input) != `{"file_path":"important/file.txt"}` {
					t.Fatalf("done=%v failed=%v calls=%+v", done, failed, calls)
				}
			} else if done || !failed || len(calls) != 0 {
				t.Fatalf("partial tool committed: done=%v failed=%v calls=%+v", done, failed, calls)
			}
		})
	}
}

func TestResponsesCompletionRecoversUnsentSuffix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"prefix"}`+"\n\n"+`data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"prefix suffix"}]}]}}`+"\n\n")
	}))
	defer server.Close()
	client := NewClient(Config{Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "test"})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "test"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		text += event.Delta
	}
	if text != "prefix suffix" {
		t.Fatalf("text=%q", text)
	}
}
