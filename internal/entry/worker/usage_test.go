package worker

import (
	"io"
	"paw/internal/capability/model"
	"paw/internal/runtime/task"
	"paw/internal/ui/headless"
	"testing"
)

func TestWorkerUsageUICapturesStructuredUsage(t *testing.T) {
	var events []task.WorkerStreamEvent
	workerUI := &workerUsageUI{UI: headless.New(io.Discard), emit: func(event task.WorkerStreamEvent) {
		events = append(events, event)
	}}

	if err := workerUI.OnAssistantDelta("partial answer"); err != nil {
		t.Fatal(err)
	}
	workerUI.OnModelUsage(model.Usage{
		PromptTokens:         120,
		CompletionTokens:     9,
		PromptCacheHitTokens: 50,
	})

	usage := workerUI.Usage()
	if usage == nil {
		t.Fatal("Usage() = nil, want structured usage")
	}
	if usage.Input != 70 || usage.CacheRead != 50 || usage.Output != 9 {
		t.Fatalf("Usage() = %#v, want input/cache/output split", usage)
	}
	if len(events) != 2 || events[0].Delta != "partial answer" || events[1].Usage == nil {
		t.Fatalf("worker events = %#v", events)
	}
}
