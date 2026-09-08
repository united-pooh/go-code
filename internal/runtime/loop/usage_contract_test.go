package loop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/runtime/streamma"
	"paw/internal/tokentracer"
)

type usageCollectingUI struct {
	fakeUI
	usages []model.Usage
	starts []string
}

func (u *usageCollectingUI) OnModelUsage(usage model.Usage) { u.usages = append(u.usages, usage) }

func (u *usageCollectingUI) OnModelRequestStart(id string) { u.starts = append(u.starts, id) }

func TestRequestStartDisplayIsIndependentOfReportedUsage(t *testing.T) {
	for _, failed := range []bool{false, true} {
		events := []model.StreamEvent{{Delta: "done", Done: true}}
		if failed {
			events = []model.StreamEvent{{Err: errors.New("fixture failure")}}
		}
		output := &usageCollectingUI{}
		runner := NewEngine(&fakeModel{rounds: []fakeRound{{events: events}}}, output, tool.NewRegistry(), &fakeStore{}, "lifecycle")
		_, _ = runner.RunTurn(context.Background(), "hello")
		if len(output.starts) != 1 || output.starts[0] == "" {
			t.Fatalf("logical request was not announced (failed=%v): %+v", failed, output.starts)
		}
		if len(output.usages) != 0 {
			t.Fatal("request start synthesized provider usage")
		}
	}
}

func TestRequestUsageUpdatesCountOnceAndPreserveCacheCreation(t *testing.T) {
	streamer := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{Protocol: model.UsageProtocolAnthropic, InputTokens: 100, CacheReadInputTokens: 10, CacheCreationInputTokens: 30}},
		{Usage: &model.Usage{OutputTokens: 2}},
		{Usage: &model.Usage{OutputTokens: 5}},
		{Delta: "done", Done: true},
	}}}}
	output := &usageCollectingUI{}
	runner := NewEngine(streamer, output, tool.NewRegistry(), &fakeStore{}, "usage-session")
	tracer := tokentracer.New("usage")
	runner.SetTokenTracer(tracer)
	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	snapshot := tracer.Snapshot()
	if snapshot.Pipeline.Calls != 1 {
		t.Errorf("Calls = %d, want one request", snapshot.Pipeline.Calls)
	}
	for _, row := range snapshot.Timeline.Rows {
		if row.Kind != "agent" {
			continue
		}
		calls := 0
		for _, marker := range row.Markers {
			if marker.Type == "api_call" {
				calls++
			}
		}
		if row.Calls != 1 || calls != 1 {
			t.Errorf("timeline still counts usage updates: Calls=%d markers=%d", row.Calls, calls)
		}
	}
	total := snapshot.Pipeline.Total
	if total.Input != 100 || total.CacheRead != 10 || total.CacheCreation != 30 || total.Output != 5 {
		t.Errorf("classification lost: %+v", total)
	}
	if len(output.usages) != 3 {
		t.Fatalf("usage events = %d", len(output.usages))
	}
	id := output.usages[0].RequestID
	if id == "" {
		t.Fatal("request identity missing")
	}
	if len(output.starts) != 1 || output.starts[0] != id {
		t.Fatal("request lifecycle and usage identities differ")
	}
	for _, u := range output.usages {
		if u.RequestID != id {
			t.Errorf("one request changed identity: %q -> %q", id, u.RequestID)
		}
	}
	if got := output.usages[2].Breakdown(); got != (model.UsageBreakdown{Input: 100, Output: 5, CacheRead: 10, CacheCreation: 30}) {
		t.Errorf("consumer must receive cumulative request snapshot: %+v", got)
	}
}

func TestDistinctRequestsHaveDistinctUsageIdentities(t *testing.T) {
	streamer := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 5}}, {Delta: "first", Done: true}}},
		{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 20, CompletionTokens: 2}}, {Delta: "second", Done: true}}},
	}}
	output := &usageCollectingUI{}
	runner := NewEngine(streamer, output, tool.NewRegistry(), &fakeStore{}, "usage-session")
	for _, prompt := range []string{"one", "two"} {
		if _, err := runner.RunTurn(context.Background(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	if len(output.usages) != 2 || output.usages[0].RequestID == "" || output.usages[0].RequestID == output.usages[1].RequestID {
		t.Fatalf("requests need distinct IDs: %+v", output.usages)
	}
	var meter model.UsageAccumulator
	for _, u := range output.usages {
		meter.Observe(u)
	}
	if got := meter.Total(); got != (model.UsageBreakdown{Input: 120, Output: 7}) {
		t.Fatalf("worker total = %+v", got)
	}
}

func TestStreamMAUsageDoesNotSubtractDifferentRequests(t *testing.T) {
	tracer := tokentracer.New("streamma")
	stage, _ := tracer.StartTurn("parallel", "streamma")
	wrapper := &streamMATaskModel{tokenTracer: tracer, traceStageID: stage}
	events := make(chan model.StreamEvent, 4)
	for _, usage := range []model.Usage{
		{RequestID: "a", PromptTokens: 100},
		{RequestID: "a", PromptTokens: 100, CompletionTokens: 5},
		{RequestID: "b", PromptTokens: 20, CompletionTokens: 2},
	} {
		events <- model.StreamEvent{Usage: &usage}
	}
	close(events)
	for range wrapper.wrapStream(context.Background(), streamma.AgentInvocation{AgentID: "worker"}, StreamMATaskStream{Events: events}, -1) {
	}
	snapshot := tracer.Snapshot()
	if snapshot.Pipeline.Calls != 2 || snapshot.Pipeline.Total.Input != 120 || snapshot.Pipeline.Total.Output != 7 {
		t.Fatalf("StreamMA request totals: Calls=%d usage=%+v", snapshot.Pipeline.Calls, snapshot.Pipeline.Total)
	}
}

func TestStreamMARequestStartsCountWithoutInventingUsage(t *testing.T) {
	tracer := tokentracer.New("streamma")
	stage, _ := tracer.StartTurn("parallel", "streamma")
	wrapper := &streamMATaskModel{tokenTracer: tracer, traceStageID: stage}
	events := make(chan model.StreamEvent, 8)
	for _, event := range []model.StreamEvent{
		{RequestStartID: "a"}, {RequestStartID: "a"},
		{Usage: &model.Usage{RequestID: "a", PromptTokens: 100, CompletionTokens: 5}},
		{RequestStartID: "b"}, {Err: errors.New("missing usage")},
		{RequestStartID: "c"}, {Usage: &model.Usage{RequestID: "c", PresentFields: 1<<0 | 1<<1}},
	} {
		events <- event
	}
	close(events)
	for range wrapper.wrapStream(context.Background(), streamma.AgentInvocation{AgentID: "worker"}, StreamMATaskStream{Events: events}, -1) {
	}
	snapshot := tracer.Snapshot()
	if snapshot.Pipeline.Calls != 3 || snapshot.Pipeline.Total.Input != 100 || snapshot.Pipeline.Total.Output != 5 {
		t.Fatalf("request lifecycle lost: %+v", snapshot.Pipeline)
	}
	zeroReported := false
	for _, event := range snapshot.Events {
		if event.Type == "api_call" && event.Data["request_id"] == "b" && event.Data["usage_known"] != false {
			t.Fatal("missing usage was presented as reported")
		}
		if event.Type == "api_call" && event.Data["request_id"] == "c" && event.Data["usage_known"] == true {
			zeroReported = true
		}
	}
	if !zeroReported {
		t.Fatal("explicit zero usage was lost")
	}
}

func TestStreamMACancelDrainsRequestLifecycle(t *testing.T) {
	tracer := tokentracer.New("cancel")
	stage, _ := tracer.StartTurn("cancel", "streamma")
	wrapper := &streamMATaskModel{tokenTracer: tracer, traceStageID: stage}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan model.StreamEvent, 3)
	events <- model.StreamEvent{RequestStartID: "a"}
	events <- model.StreamEvent{RequestStartID: "b"}
	events <- model.StreamEvent{Usage: &model.Usage{RequestID: "b", PromptTokens: 20}}
	close(events)
	for range wrapper.wrapStream(ctx, streamma.AgentInvocation{AgentID: "worker"}, StreamMATaskStream{Events: events}, -1) {
	}
	if snapshot := tracer.Snapshot(); snapshot.Pipeline.Calls != 2 || snapshot.Pipeline.Total.Input != 20 {
		t.Fatalf("cancel lost drained lifecycle: %+v", snapshot.Pipeline)
	}
}

func TestCompactionRetryUsageIncludesBothRequests(t *testing.T) {
	streamer := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 10, CompletionTokens: 2}}, {Err: errors.New("transient")}}},
		{events: []model.StreamEvent{{Usage: &model.Usage{PromptTokens: 20}}, {Usage: &model.Usage{CompletionTokens: 3}}, {Delta: "summary", Done: true}}},
	}}
	runner := NewEngine(streamer, &fakeUI{}, tool.NewRegistry(), nil, "session")
	tracer := tokentracer.New("compaction")
	runner.SetTokenTracer(tracer)
	_, usage, err := runner.summarizeHistoryWithRetry(context.Background(), []message.Message{{Role: message.RoleUser, Content: "history"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.Breakdown().Total() != 35 {
		t.Errorf("retry total = %+v, want 35", usage)
	}
	snapshot := tracer.Snapshot()
	if snapshot.Pipeline.Calls != 2 || snapshot.Pipeline.Total.Input != 30 || snapshot.Pipeline.Total.Output != 5 {
		t.Errorf("compaction missing from tracer: Calls=%d usage=%+v", snapshot.Pipeline.Calls, snapshot.Pipeline.Total)
	}
	if got := runner.ContextStats(1024, "").SessionUsedTokens; got != 35 {
		t.Errorf("session total = %d, want 35", got)
	}
}

func TestUsageDownwardCorrectionReachesSessionTracerAndTimeline(t *testing.T) {
	var corrected model.Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":80,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":0}}`), &corrected); err != nil {
		t.Fatal(err)
	}
	streamer := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 10, PromptTokensDetails: model.TokenDetails{CachedTokens: 20}}},
		{Usage: &corrected}, {Delta: "done", Done: true},
	}}}}
	runner := NewEngine(streamer, &fakeUI{}, tool.NewRegistry(), &fakeStore{}, "corrected")
	tracer := tokentracer.New("corrected")
	runner.SetTokenTracer(tracer)
	if _, err := runner.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if got := runner.ContextStats(1024, "").SessionUsedTokens; got != 83 {
		t.Errorf("session = %d, want 83", got)
	}
	snapshot := tracer.Snapshot()
	if got := snapshot.Pipeline.Total; got.Input != 80 || got.Output != 3 || got.CacheRead != 0 {
		t.Errorf("tracer = %+v", got)
	}
	for _, row := range snapshot.Timeline.Rows {
		if row.Kind == "agent" && (row.Usage.Output != 3 || row.Usage.CacheRead != 0 || row.Calls != 1) {
			t.Errorf("timeline = %+v", row)
		}
	}
}

func TestTracerCountsRequestsWithoutUsage(t *testing.T) {
	for _, failed := range []bool{false, true} {
		events := []model.StreamEvent{{Delta: "done", Done: true}}
		if failed {
			events = []model.StreamEvent{{Err: errors.New("fixture failure")}}
		}
		runner := NewEngine(&fakeModel{rounds: []fakeRound{{events: events}}}, &fakeUI{}, tool.NewRegistry(), &fakeStore{}, "usage-session")
		tracer := tokentracer.New("usage")
		runner.SetTokenTracer(tracer)
		_, _ = runner.RunTurn(context.Background(), "hello")
		if snapshot := tracer.Snapshot(); snapshot.Pipeline.Calls != 1 || !snapshot.Pipeline.Total.Empty() {
			t.Fatalf("request without usage lost (failed=%v): %+v", failed, snapshot.Pipeline)
		}
	}
}
