package task

import (
	"context"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/message"
	"paw/internal/platform/settings"
	"paw/internal/storage/session"
	"paw/internal/tokentracer"
)

type scopeRecordingModel struct {
	recordingModel
	scopes []model.RequestScope
}

func (m *scopeRecordingModel) StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.scopes = append(m.scopes, model.RequestScopeFromContext(ctx))
	return m.recordingModel.StreamMessage(ctx, messages, tools)
}

func TestStreamingTaskPreservesTelemetryOwnership(t *testing.T) {
	streamer := &scopeRecordingModel{recordingModel: recordingModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done", Done: true}}}}}}
	manager, store, _ := newTestManager(t, streamer, settings.DefaultConfig(), nil)
	if _, err := store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
	stream, err := manager.Stream(context.Background(), Request{Prompt: "hello", ParentSessionID: "parent", ContextMode: settings.ContextModeEmpty})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events {
	}
	if len(streamer.scopes) != 1 {
		t.Fatalf("scopes: %+v", streamer.scopes)
	}
	scope := streamer.scopes[0]
	if scope.TaskID != stream.SessionID || scope.SessionID != stream.SessionID || scope.ParentSessionID != "parent" || scope.Purpose != "worker_stream" {
		t.Fatalf("lost ownership: %+v", scope)
	}
}

func TestStreamingUIForwardsStartWithoutMarkingUsageKnown(t *testing.T) {
	events := make(chan model.StreamEvent, 1)
	stream := &streamingUI{ctx: context.Background(), events: events}
	stream.OnModelRequestStart("request")
	if event := <-events; event.RequestStartID != "request" || event.Usage != nil {
		t.Fatalf("invalid lifecycle: %+v", event)
	}
	if stream.Usage() != nil {
		t.Fatal("lifecycle synthesized usage")
	}
}

func TestUsageConsumersAccumulateRequestsNotSnapshots(t *testing.T) {
	events := make(chan model.StreamEvent, 8)
	stream := &streamingUI{ctx: context.Background(), events: events}
	sink := &usageSinkUI{}
	job := &poolJob{}
	for _, usage := range []model.Usage{
		{RequestID: "a", Protocol: model.UsageProtocolAnthropic, InputTokens: 100, CacheReadInputTokens: 10, CacheCreationInputTokens: 30},
		{RequestID: "a", OutputTokens: 5},
		{RequestID: "a", OutputTokens: 5},
		{RequestID: "b", Protocol: model.UsageProtocolAnthropic, InputTokens: 20, OutputTokens: 2},
	} {
		stream.OnModelUsage(usage)
		sink.OnModelUsage(usage)
		job.recordEvent(WorkerStreamEvent{Usage: &usage})
	}
	for name, usage := range map[string]*tokentracer.Usage{"stream": stream.Usage(), "sink": sink.Usage(), "pool": job.partial.Usage} {
		if usage == nil || usage.Input != 120 || usage.CacheRead != 10 || usage.CacheCreation != 30 || usage.Output != 7 {
			t.Errorf("%s total = %+v, want 120 input / 10 read / 30 write / 7 output", name, usage)
		}
	}
	if len(events) != 4 {
		t.Errorf("forwarded %d usage events, want 4", len(events))
	}
}
