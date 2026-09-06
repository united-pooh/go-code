package model

import "encoding/json"

type UsageProtocol string

const (
	UsageProtocolAnthropic UsageProtocol = "anthropic"
	UsageProtocolOpenAI    UsageProtocol = "openai"
)

// These bit values are persisted in paw_usage_fields; never reorder them.
const (
	usageInputTokens uint64 = 1 << iota
	usageOutputTokens
	usageCacheCreationInputTokens
	usageCacheReadInputTokens
	usagePromptTokens
	usageCompletionTokens
	usageTotalTokens
	usagePromptCacheHitTokens
	usagePromptCacheMissTokens
)

const (
	detailCachedTokens uint64 = 1 << iota
	detailCacheReadTokens
	detailCacheReadInputTokens
	detailCacheCreationTokens
	detailCacheCreationInputTokens
	detailReasoningTokens
	detailThinkingTokens
)

type UsageKnowledge struct {
	Total         bool `json:"total"`
	Input         bool `json:"input"`
	Output        bool `json:"output"`
	CacheRead     bool `json:"cache_read"`
	CacheCreation bool `json:"cache_creation"`
	Reasoning     bool `json:"reasoning"`
}

func (u Usage) Knowledge() UsageKnowledge {
	inputDetails := u.PromptTokensDetails.PresentFields | u.InputTokensDetails.PresentFields
	outputDetails := u.OutputTokensDetails.PresentFields | u.CompletionTokensDetails.PresentFields
	known := UsageKnowledge{
		Total:         u.TotalTokens != 0 || u.PresentFields&usageTotalTokens != 0,
		Input:         u.InputTokens != 0 || u.PromptTokens != 0 || u.PresentFields&(usageInputTokens|usagePromptTokens) != 0,
		Output:        u.OutputTokens != 0 || u.CompletionTokens != 0 || u.PresentFields&(usageOutputTokens|usageCompletionTokens) != 0,
		CacheRead:     u.CacheHitTokens() != 0 || u.PresentFields&(usageCacheReadInputTokens|usagePromptCacheHitTokens) != 0 || inputDetails&(detailCachedTokens|detailCacheReadTokens|detailCacheReadInputTokens) != 0,
		CacheCreation: u.CacheCreationTokens() != 0 || u.PresentFields&usageCacheCreationInputTokens != 0 || inputDetails&(detailCacheCreationTokens|detailCacheCreationInputTokens) != 0,
		Reasoning:     u.reportedReasoning() != 0 || outputDetails&(detailReasoningTokens|detailThinkingTokens) != 0,
	}
	if u.invalidCacheSubset() {
		known.CacheRead, known.CacheCreation = false, false
	}
	if u.invalidReasoningSubset() {
		known.Reasoning = false
	}
	return known
}

// UsageBreakdown uses disjoint input buckets. Reasoning is a subset of Output.
type UsageBreakdown struct {
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheRead     int `json:"cache_read"`
	CacheCreation int `json:"cache_creation"`
	Reasoning     int `json:"reasoning"`
}

func (u UsageBreakdown) Total() int {
	return u.Input + u.Output + u.CacheRead + u.CacheCreation
}

func (u UsageBreakdown) Add(v UsageBreakdown) UsageBreakdown {
	return UsageBreakdown{u.Input + v.Input, u.Output + v.Output, u.CacheRead + v.CacheRead, u.CacheCreation + v.CacheCreation, u.Reasoning + v.Reasoning}
}

func (u UsageBreakdown) Sub(v UsageBreakdown) UsageBreakdown {
	return UsageBreakdown{u.Input - v.Input, u.Output - v.Output, u.CacheRead - v.CacheRead, u.CacheCreation - v.CacheCreation, u.Reasoning - v.Reasoning}
}

func (u UsageBreakdown) ModelUsage() Usage {
	return Usage{
		Protocol:                UsageProtocolOpenAI,
		PromptTokens:            u.Input + u.CacheRead + u.CacheCreation,
		CompletionTokens:        u.Output,
		TotalTokens:             u.Total(),
		PromptTokensDetails:     TokenDetails{CachedTokens: u.CacheRead, CacheCreationTokens: u.CacheCreation},
		CompletionTokensDetails: TokenDetails{ReasoningTokens: u.Reasoning},
	}
}

func (u Usage) Breakdown() UsageBreakdown {
	read, write := max(0, u.CacheHitTokens()), max(0, u.CacheCreationTokens())
	output := max(0, u.CompletionTokenCount())
	input := max(0, u.PromptTokenCount())
	if input == 0 && u.TotalTokens > 0 && u.PresentFields&(usageInputTokens|usagePromptTokens) == 0 {
		input = max(0, u.TotalTokens-output)
	}
	protocol := u.effectiveUsageProtocol()
	if u.invalidCacheSubset() {
		read, write = 0, 0
	}
	if protocol == UsageProtocolOpenAI {
		input = max(0, input-read-write)
	}
	reasoning := u.reportedReasoning()
	if u.invalidReasoningSubset() {
		reasoning = 0
	}
	return UsageBreakdown{Input: input, Output: output, CacheRead: read, CacheCreation: write, Reasoning: max(0, reasoning)}
}

func (u Usage) effectiveUsageProtocol() UsageProtocol {
	protocol := u.Protocol
	if protocol == "" {
		// Compatibility for callers constructing Usage directly. Production
		// adapters set Protocol explicitly, including when all fields are zero.
		protocol = UsageProtocolAnthropic
		if u.PromptTokens != 0 || u.CompletionTokens != 0 || u.TotalTokens != 0 || u.InputTokensDetails != (TokenDetails{}) {
			protocol = UsageProtocolOpenAI
		}
	}
	return protocol
}

func (u Usage) reportedReasoning() int {
	reasoning := u.OutputTokensDetails.ReasoningTokens
	if reasoning == 0 {
		reasoning = u.OutputTokensDetails.ThinkingTokens
	}
	if reasoning == 0 {
		reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
	return reasoning
}

func (u Usage) invalidCacheSubset() bool {
	input := u.PromptTokenCount()
	reported := input != 0 || u.PresentFields&(usageInputTokens|usagePromptTokens) != 0
	if !reported && u.TotalTokens > 0 {
		input, reported = max(0, u.TotalTokens-u.CompletionTokenCount()), true
	}
	return reported && u.effectiveUsageProtocol() == UsageProtocolOpenAI && u.CacheHitTokens()+u.CacheCreationTokens() > input
}

func (u Usage) invalidReasoningSubset() bool {
	output := u.CompletionTokenCount()
	return (output != 0 || u.PresentFields&(usageOutputTokens|usageCompletionTokens) != 0) && u.reportedReasoning() > output
}

// Inconsistent reports invalid provider subsets. Keep raw fields as evidence;
// consumers must not turn rejected subsets into valid zero measurements.
func (u Usage) Inconsistent() bool {
	return u.invalidCacheSubset() || u.invalidReasoningSubset()
}

type usageJSONField struct {
	name string
	mask uint64
}

var usageFields = []usageJSONField{
	{"input_tokens", usageInputTokens},
	{"output_tokens", usageOutputTokens},
	{"cache_creation_input_tokens", usageCacheCreationInputTokens},
	{"cache_read_input_tokens", usageCacheReadInputTokens},
	{"prompt_tokens", usagePromptTokens},
	{"completion_tokens", usageCompletionTokens},
	{"total_tokens", usageTotalTokens},
	{"prompt_cache_hit_tokens", usagePromptCacheHitTokens},
	{"prompt_cache_miss_tokens", usagePromptCacheMissTokens},
}

var detailFields = []usageJSONField{
	{"cached_tokens", detailCachedTokens},
	{"cache_read_tokens", detailCacheReadTokens},
	{"cache_read_input_tokens", detailCacheReadInputTokens},
	{"cache_creation_tokens", detailCacheCreationTokens},
	{"cache_creation_input_tokens", detailCacheCreationInputTokens},
	{"reasoning_tokens", detailReasoningTokens},
	{"thinking_tokens", detailThinkingTokens},
}

func reportedFields(data []byte, fields []usageJSONField, existing uint64) (uint64, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, err
	}
	if _, ok := raw["paw_usage_fields"]; ok {
		return existing, nil
	}
	var present uint64
	for _, field := range fields {
		if value, ok := raw[field.name]; ok && string(value) != "null" {
			present |= field.mask
		}
	}
	return present, nil
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	type wire Usage
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	fields, err := reportedFields(data, usageFields, decoded.PresentFields)
	if err != nil {
		return err
	}
	*u = Usage(decoded)
	u.PresentFields = fields
	return nil
}

func (d *TokenDetails) UnmarshalJSON(data []byte) error {
	type wire TokenDetails
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	fields, err := reportedFields(data, detailFields, decoded.PresentFields)
	if err != nil {
		return err
	}
	*d = TokenDetails(decoded)
	d.PresentFields = fields
	return nil
}

func MergeUsageSnapshot(current, next Usage) Usage {
	if next.RequestID != "" {
		current.RequestID = next.RequestID
	}
	if next.Protocol != "" {
		current.Protocol = next.Protocol
	}
	fields := []struct {
		dst  *int
		src  int
		mask uint64
	}{
		{&current.InputTokens, next.InputTokens, usageInputTokens},
		{&current.OutputTokens, next.OutputTokens, usageOutputTokens},
		{&current.CacheCreationInputTokens, next.CacheCreationInputTokens, usageCacheCreationInputTokens},
		{&current.CacheReadInputTokens, next.CacheReadInputTokens, usageCacheReadInputTokens},
		{&current.PromptTokens, next.PromptTokens, usagePromptTokens},
		{&current.CompletionTokens, next.CompletionTokens, usageCompletionTokens},
		{&current.TotalTokens, next.TotalTokens, usageTotalTokens},
		{&current.PromptCacheHitTokens, next.PromptCacheHitTokens, usagePromptCacheHitTokens},
		{&current.PromptCacheMissTokens, next.PromptCacheMissTokens, usagePromptCacheMissTokens},
	}
	for _, field := range fields {
		if field.src != 0 || next.PresentFields&field.mask != 0 {
			*field.dst = field.src
		}
	}
	current.PresentFields |= next.PresentFields
	current.PromptTokensDetails = mergeUsageDetails(current.PromptTokensDetails, next.PromptTokensDetails)
	current.InputTokensDetails = mergeUsageDetails(current.InputTokensDetails, next.InputTokensDetails)
	current.OutputTokensDetails = mergeUsageDetails(current.OutputTokensDetails, next.OutputTokensDetails)
	current.CompletionTokensDetails = mergeUsageDetails(current.CompletionTokensDetails, next.CompletionTokensDetails)
	return current
}

func mergeUsageDetails(current, next TokenDetails) TokenDetails {
	fields := []struct {
		dst  *int
		src  int
		mask uint64
	}{
		{&current.CachedTokens, next.CachedTokens, detailCachedTokens},
		{&current.CacheReadTokens, next.CacheReadTokens, detailCacheReadTokens},
		{&current.CacheReadInputTokens, next.CacheReadInputTokens, detailCacheReadInputTokens},
		{&current.CacheCreationTokens, next.CacheCreationTokens, detailCacheCreationTokens},
		{&current.CacheCreationInputTokens, next.CacheCreationInputTokens, detailCacheCreationInputTokens},
		{&current.ReasoningTokens, next.ReasoningTokens, detailReasoningTokens},
		{&current.ThinkingTokens, next.ThinkingTokens, detailThinkingTokens},
	}
	for _, field := range fields {
		if field.src != 0 || next.PresentFields&field.mask != 0 {
			*field.dst = field.src
		}
	}
	current.PresentFields |= next.PresentFields
	return current
}

// UsageAccumulator receives cumulative request snapshots, not usage deltas.
// Its owner provides synchronization. Empty request IDs denote one legacy stream.
type UsageAccumulator struct {
	requests         map[string]Usage
	total            UsageBreakdown
	cacheMiss        int
	missingCacheMiss int
}

func (m *UsageAccumulator) Observe(next Usage) UsageBreakdown {
	if m.requests == nil {
		m.requests = make(map[string]Usage)
	}
	previous, exists := m.requests[next.RequestID]
	current := MergeUsageSnapshot(previous, next)
	m.requests[next.RequestID] = current
	if !exists {
		m.missingCacheMiss++
	}
	if !previous.CacheMissReported() && current.CacheMissReported() {
		m.missingCacheMiss--
	}
	m.cacheMiss += current.PromptCacheMissTokens - previous.PromptCacheMissTokens
	delta := current.Breakdown().Sub(previous.Breakdown())
	m.total = m.total.Add(delta)
	return delta
}

func (m *UsageAccumulator) Total() UsageBreakdown { return m.total }

func (u Usage) CacheMissReported() bool {
	return u.PromptCacheMissTokens != 0 || u.PresentFields&usagePromptCacheMissTokens != 0
}

func (m *UsageAccumulator) CombinedUsage() Usage {
	u := m.total.ModelUsage()
	if len(m.requests) > 0 && m.missingCacheMiss == 0 {
		u.PromptCacheMissTokens = m.cacheMiss
		u.PresentFields |= usagePromptCacheMissTokens
	}
	return u
}
