package model

import (
	"context"
	"encoding/json"
	"paw/internal/message"
	"testing"
)

func TestUsageBreakdownProviderConventions(t *testing.T) {
	tests := []struct {
		name  string
		usage Usage
	}{
		{"anthropic", Usage{Protocol: UsageProtocolAnthropic, InputTokens: 100, OutputTokens: 5, CacheReadInputTokens: 10, CacheCreationInputTokens: 30, OutputTokensDetails: TokenDetails{ThinkingTokens: 3}}},
		{"responses", Usage{Protocol: UsageProtocolOpenAI, InputTokens: 140, OutputTokens: 5, InputTokensDetails: TokenDetails{CachedTokens: 10, CacheCreationTokens: 30}, OutputTokensDetails: TokenDetails{ReasoningTokens: 3}}},
		{"chat", Usage{Protocol: UsageProtocolOpenAI, PromptTokens: 140, CompletionTokens: 5, PromptTokensDetails: TokenDetails{CachedTokens: 10, CacheCreationTokens: 30}, CompletionTokensDetails: TokenDetails{ReasoningTokens: 3}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.usage.Breakdown()
			want := UsageBreakdown{Input: 100, Output: 5, CacheRead: 10, CacheCreation: 30, Reasoning: 3}
			if got != want {
				t.Fatalf("Breakdown() = %+v, want %+v", got, want)
			}
			if got.Total() != 145 {
				t.Fatalf("Total() = %d, reasoning must not be added twice", got.Total())
			}
			if restored := got.ModelUsage().Breakdown(); restored != want {
				t.Fatalf("round trip = %+v, want %+v", restored, want)
			}
		})
	}
}

func TestUsageCacheMissIsNotCacheCreation(t *testing.T) {
	u := Usage{Protocol: UsageProtocolOpenAI, PromptTokens: 100, PromptCacheHitTokens: 30, PromptCacheMissTokens: 70}
	if got := u.Breakdown(); got.Input != 70 || got.CacheCreation != 0 || got.CacheRead != 30 {
		t.Fatalf("cache miss must remain ordinary input: %+v", got)
	}
}

func TestUsageAccumulatorReplacesSnapshotsAndAccumulatesRequests(t *testing.T) {
	var meter UsageAccumulator
	for _, u := range []Usage{
		{RequestID: "a", PromptTokens: 100},
		{RequestID: "a", PromptTokens: 100, CompletionTokens: 5},
		{RequestID: "a", PromptTokens: 100, CompletionTokens: 5},
		{RequestID: "b", PromptTokens: 20, CompletionTokens: 2},
	} {
		meter.Observe(u)
	}
	if got := meter.Total(); got != (UsageBreakdown{Input: 120, Output: 7}) {
		t.Fatalf("Total() = %+v, want 120 input / 7 output", got)
	}
}

func TestUsageMergeDistinguishesAbsentAndExplicitZero(t *testing.T) {
	var initial, partial, correction Usage
	for _, item := range []struct {
		raw    string
		target *Usage
	}{
		{`{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":10,"cache_creation_input_tokens":30}`, &initial},
		{`{"output_tokens":8}`, &partial},
		{`{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}`, &correction},
	} {
		if err := json.Unmarshal([]byte(item.raw), item.target); err != nil {
			t.Fatal(err)
		}
	}
	initial.Protocol = UsageProtocolAnthropic
	got := MergeUsageSnapshot(initial, partial)
	if split := got.Breakdown(); split != (UsageBreakdown{Input: 100, Output: 8, CacheRead: 10, CacheCreation: 30}) {
		t.Fatalf("partial update lost fields: %+v", split)
	}
	// Worker protocol round trips must retain which fields were actually reported.
	wire, err := json.Marshal(correction)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Usage
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	got = MergeUsageSnapshot(got, decoded)
	if split := got.Breakdown(); split != (UsageBreakdown{Output: 8}) {
		t.Fatalf("explicit zero or absent output was lost: %+v", split)
	}
}

func TestUsageAccumulatorAppliesCorrectionsWithoutDoubleCounting(t *testing.T) {
	var meter UsageAccumulator
	meter.Observe(Usage{RequestID: "a", PromptTokens: 100, CompletionTokens: 5})
	meter.Observe(Usage{RequestID: "b", PromptTokens: 20, CompletionTokens: 2})
	var correction Usage
	if err := json.Unmarshal([]byte(`{"paw_request_id":"a","prompt_tokens":0,"completion_tokens":3}`), &correction); err != nil {
		t.Fatal(err)
	}
	meter.Observe(correction)
	if got := meter.Total(); got != (UsageBreakdown{Input: 20, Output: 5}) {
		t.Fatalf("correction did not replace original request: %+v", got)
	}
}

func TestUsageWirePreservesAbsentNestedDetails(t *testing.T) {
	var initial, partial Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":30,"cache_creation_tokens":20},"completion_tokens_details":{"reasoning_tokens":3}}`), &initial); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"completion_tokens":10}`), &partial); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(partial)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wire, &partial); err != nil {
		t.Fatal(err)
	}
	got := MergeUsageSnapshot(initial, partial).Breakdown()
	if got != (UsageBreakdown{Input: 50, Output: 10, CacheRead: 30, CacheCreation: 20, Reasoning: 3}) {
		t.Fatalf("worker round trip erased absent detail fields: %+v", got)
	}
}

func TestUsagePresenceWireBits(t *testing.T) {
	for _, tc := range []struct {
		field  string
		bit    uint64
		detail bool
	}{
		{"input_tokens", 1, false},
		{"output_tokens", 2, false},
		{"cache_creation_input_tokens", 4, false},
		{"cache_read_input_tokens", 8, false},
		{"prompt_tokens", 16, false},
		{"completion_tokens", 32, false},
		{"total_tokens", 64, false},
		{"prompt_cache_hit_tokens", 128, false},
		{"prompt_cache_miss_tokens", 256, false},
		{"cached_tokens", 1, true},
		{"cache_read_tokens", 2, true},
		{"cache_read_input_tokens", 4, true},
		{"cache_creation_tokens", 8, true},
		{"cache_creation_input_tokens", 16, true},
		{"reasoning_tokens", 32, true},
		{"thinking_tokens", 64, true},
	} {
		t.Run(tc.field, func(t *testing.T) {
			for _, value := range []string{"0", "null"} {
				data := []byte(`{"` + tc.field + `":` + value + `}`)
				want := tc.bit
				if value == "null" {
					want = 0
				}
				for round := range 2 {
					var present uint64
					var err error
					if tc.detail {
						var usage TokenDetails
						if err := json.Unmarshal(data, &usage); err != nil {
							t.Fatal(err)
						}
						present = usage.PresentFields
						data, err = json.Marshal(usage)
					} else {
						var usage Usage
						if err := json.Unmarshal(data, &usage); err != nil {
							t.Fatal(err)
						}
						present = usage.PresentFields
						data, err = json.Marshal(usage)
					}
					if err != nil || present != want {
						t.Fatalf("value=%s round=%d: mask=%d want=%d err=%v", value, round, present, want, err)
					}
				}
			}
		})
	}
}

func TestUsageWrapperTagsActualProtocolAndRequest(t *testing.T) {
	for _, transport := range []string{"anthropic-compatible", "openai-compatible", "openai-responses"} {
		t.Run(transport, func(t *testing.T) {
			input := make(chan StreamEvent, 2)
			u := Usage{InputTokens: 100, OutputTokens: 5, CacheReadInputTokens: 20}
			input <- StreamEvent{Usage: &u}
			input <- StreamEvent{Done: true}
			close(input)
			var observed *Usage
			for event := range wrapWithOrigin(context.Background(), input, &message.MessageOrigin{Transport: transport}, "request-a") {
				if event.Usage != nil {
					observed = event.Usage
				}
			}
			if observed == nil || observed.RequestID != "request-a" {
				t.Fatalf("usage identity lost: %+v", observed)
			}
			want := 80
			if transport == "anthropic-compatible" {
				want = 100
			}
			if got := observed.Breakdown().Input; got != want {
				t.Fatalf("ordinary input = %d, want %d", got, want)
			}
			if u.RequestID != "" || u.Protocol != "" {
				t.Fatal("wrapper mutated provider-owned usage")
			}
		})
	}
	ctx := WithUsageRequestID(context.Background(), "parent-request")
	if got := usageRequestID(ctx); got != "parent-request" {
		t.Fatalf("context request = %q", got)
	}
}

func TestUsageAccumulatorPreservesReportedCacheMiss(t *testing.T) {
	var meter UsageAccumulator
	meter.Observe(Usage{RequestID: "a", PromptTokens: 100, PromptCacheHitTokens: 40, PromptCacheMissTokens: 60})
	meter.Observe(Usage{RequestID: "a", CompletionTokens: 10})
	if got := meter.CombinedUsage(); got.PromptCacheMissTokens != 60 || !got.CacheMissReported() {
		t.Fatalf("reported cache miss lost: %+v", got)
	}
	var zero Usage
	if err := json.Unmarshal([]byte(`{"paw_request_id":"a","prompt_cache_miss_tokens":0}`), &zero); err != nil {
		t.Fatal(err)
	}
	meter.Observe(zero)
	if got := meter.CombinedUsage(); got.PromptCacheMissTokens != 0 || !got.CacheMissReported() {
		t.Fatalf("reported zero must remain known: %+v", got)
	}
	meter.Observe(Usage{RequestID: "b", PromptTokens: 20})
	if got := meter.CombinedUsage(); got.CacheMissReported() {
		t.Fatalf("partial cache miss coverage must not be presented as complete: %+v", got)
	}
}

func TestUsageKnowledgeAndProtocolAwareContext(t *testing.T) {
	var usage Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":100,"output_tokens":0,"input_tokens_details":{"cached_tokens":20},"output_tokens_details":{"reasoning_tokens":0}}`), &usage); err != nil {
		t.Fatal(err)
	}
	usage.Protocol = UsageProtocolOpenAI
	if usage.ContextTokenCount() != 100 {
		t.Fatalf("Responses cache counted twice: %d", usage.ContextTokenCount())
	}
	known := usage.Knowledge()
	if !known.Input || !known.Output || !known.CacheRead || !known.Reasoning || known.CacheCreation {
		t.Fatalf("presence = %+v", known)
	}
	if empty := (Usage{}).Knowledge(); empty.Input || empty.Output || empty.CacheRead {
		t.Fatalf("empty usage is unknown: %+v", empty)
	}
}

func TestUsageInconsistentSubsetsDoNotInflateReportedTotals(t *testing.T) {
	var usage Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":130},"completion_tokens_details":{"reasoning_tokens":9}}`), &usage); err != nil {
		t.Fatal(err)
	}
	usage.Protocol = UsageProtocolOpenAI
	if got := usage.Breakdown(); got.Total() != 105 || got.CacheRead != 0 || got.Reasoning != 0 {
		t.Fatalf("invalid subsets inflate or fabricate the total: %+v", got)
	}
	if known := usage.Knowledge(); known.CacheRead || known.Reasoning {
		t.Fatalf("inconsistent detail marked known: %+v", known)
	}
	if usage.PromptTokensDetails.CachedTokens != 130 {
		t.Fatal("raw provider evidence must be retained")
	}
}
