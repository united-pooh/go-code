package worker

import (
	"bytes"
	"encoding/json"
	"paw/internal/runtime/task"
	"strings"
	"testing"
)

func TestReadTaskWorkerStartBuildsWorkerRuntimeContext(t *testing.T) {
	tests := []struct {
		name     string
		Depth    int
		MaxDepth int
	}{
		{name: "first-level worker", Depth: 1, MaxDepth: 4},
		{name: "delegated worker", Depth: 3, MaxDepth: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantReq := task.WorkerRequest{
				TaskID:    "task-1",
				SessionID: "session-1",
				Depth:     tt.Depth,
				MaxDepth:  tt.MaxDepth,
			}
			decoder := json.NewDecoder(bytes.NewReader(mustWorkerStartJSON(t, wantReq)))
			start, gotReq, subCtx, err := readTaskWorkerStart(decoder)
			if err != nil {
				t.Fatalf("readTaskWorkerStart: %v", err)
			}
			if start.Type != task.WorkerMessageStart {
				t.Fatalf("message type = %q, want %q", start.Type, task.WorkerMessageStart)
			}
			if gotReq.Depth != wantReq.Depth || gotReq.MaxDepth != wantReq.MaxDepth {
				t.Fatalf("decoded depth = %d/%d, want %d/%d", gotReq.Depth, gotReq.MaxDepth, wantReq.Depth, wantReq.MaxDepth)
			}
			if !subCtx.WorkerMode {
				t.Fatal("valid worker start did not set workerMode")
			}
			if subCtx.Depth != wantReq.Depth || subCtx.MaxDepth != wantReq.MaxDepth {
				t.Fatalf("runtime depth = %d/%d, want %d/%d", subCtx.Depth, subCtx.MaxDepth, wantReq.Depth, wantReq.MaxDepth)
			}
			if subCtx.ParentTaskID != wantReq.TaskID {
				t.Fatalf("parentTaskID = %q, want %q", subCtx.ParentTaskID, wantReq.TaskID)
			}
			if !subCtx.DisableMainTodo {
				t.Fatal("worker runtime context did not disable the main todo tool")
			}
		})
	}
}

func TestReadTaskWorkerStartRejectsInvalidDepth(t *testing.T) {
	tests := []struct {
		name        string
		Depth       int
		MaxDepth    int
		wantMessage string
	}{
		{name: "zero max depth", Depth: 1, MaxDepth: 0, wantMessage: "max_depth must be at least 1"},
		{name: "negative max depth", Depth: 1, MaxDepth: -1, wantMessage: "max_depth must be at least 1"},
		{name: "zero depth", Depth: 0, MaxDepth: 4, wantMessage: "depth must satisfy"},
		{name: "negative depth", Depth: -1, MaxDepth: 4, wantMessage: "depth must satisfy"},
		{name: "too deep", Depth: 5, MaxDepth: 4, wantMessage: "depth must satisfy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := task.WorkerRequest{
				TaskID:    "task-1",
				SessionID: "session-1",
				Depth:     tt.Depth,
				MaxDepth:  tt.MaxDepth,
			}
			decoder := json.NewDecoder(bytes.NewReader(mustWorkerStartJSON(t, req)))
			_, _, _, err := readTaskWorkerStart(decoder)
			if err == nil {
				t.Fatal("readTaskWorkerStart succeeded")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantMessage)
			}
		})
	}
}

func TestReadTaskWorkerStartRejectsMalformedDepth(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"type":"worker.start","task_id":"task-1","session_id":"session-1","depth":"one","max_depth":4}`))
	_, _, _, err := readTaskWorkerStart(decoder)
	if err == nil {
		t.Fatal("readTaskWorkerStart succeeded")
	}
	if !strings.Contains(err.Error(), "decode task worker.start") || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerStartBuildsExplicitAppWorkerContext(t *testing.T) {
	wantReq := task.WorkerRequest{
		TaskID:    "delegated-task",
		SessionID: "delegated-session",
		Depth:     2,
		MaxDepth:  4,
	}
	decoder := json.NewDecoder(bytes.NewReader(mustWorkerStartJSON(t, wantReq)))
	_, _, subCtx, err := readTaskWorkerStart(decoder)
	if err != nil {
		t.Fatalf("readTaskWorkerStart: %v", err)
	}
	options := subCtx
	if !options.WorkerMode || !options.DisableMainTodo || options.Depth != wantReq.Depth || options.MaxDepth != wantReq.MaxDepth || options.ParentTaskID != wantReq.TaskID {
		t.Fatalf("worker context was not preserved: %#v", options)
	}
}

func mustWorkerStartJSON(t *testing.T, req task.WorkerRequest) []byte {
	t.Helper()
	payload, err := json.Marshal(task.NewWorkerStartMessage(req, req.MCPSnapshot))
	if err != nil {
		t.Fatalf("marshal worker start: %v", err)
	}
	return append(payload, '\n')
}
