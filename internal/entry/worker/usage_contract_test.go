package worker

import (
	"encoding/json"
	"paw/internal/capability/model"
	"paw/internal/runtime/task"
	"testing"
)

func TestWorkerRequestLifecycleSurvivesWireWithoutUsage(t *testing.T) {
	var events []task.WorkerStreamEvent
	output := &workerUsageUI{emit: func(e task.WorkerStreamEvent) { events = append(events, e) }}
	output.OnModelRequestStart("request-a")
	if output.Usage() != nil || len(events) != 1 {
		t.Fatal("lifecycle changed usage")
	}
	data, err := json.Marshal(task.NewWorkerEventMessage("task", events[0]))
	if err != nil {
		t.Fatal(err)
	}
	var decoded task.WorkerMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Event == nil || decoded.Event.RequestStartID != "request-a" || decoded.Event.Usage != nil {
		t.Fatalf("worker lost lifecycle: %s", data)
	}
}

func TestWorkerUsageAccumulatesRequestsNotSnapshots(t *testing.T) {
	var events []task.WorkerStreamEvent
	output := &workerUsageUI{emit: func(e task.WorkerStreamEvent) { events = append(events, e) }}
	for _, usage := range []model.Usage{
		{RequestID: "a", Protocol: model.UsageProtocolAnthropic, InputTokens: 100, CacheReadInputTokens: 10, CacheCreationInputTokens: 30},
		{RequestID: "a", OutputTokens: 5},
		{RequestID: "a", OutputTokens: 5},
		{RequestID: "b", Protocol: model.UsageProtocolAnthropic, InputTokens: 20, OutputTokens: 2},
	} {
		output.OnModelUsage(usage)
	}
	u := output.Usage()
	if u == nil || u.Input != 120 || u.CacheRead != 10 || u.CacheCreation != 30 || u.Output != 7 {
		t.Fatalf("worker total = %+v, want 120 input / 10 read / 30 write / 7 output", u)
	}
	if len(events) != 4 || events[3].Usage.RequestID != "b" {
		t.Fatalf("usage protocol lost request identity: %+v", events)
	}
}
