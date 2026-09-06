package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"paw/internal/message"
	"sort"
	"strings"
	"time"
)

const (
	responsesProviderTransport = "openai-responses"
	responsesProviderVersion   = 1
)

// responsesProviderData 是固定在消息 ProviderData 上的不透明信封。
// transport/version 校验由 decodeResponsesProviderData 完成，其余字段透传。
type responsesProviderData struct {
	Transport   string            `json:"transport"`
	Version     int               `json:"version"`
	OutputItems []json.RawMessage `json:"output_items"`
}

// encodeResponsesProviderData 将原始 output items 打包进 provider 信封。
func encodeResponsesProviderData(items []json.RawMessage) (json.RawMessage, error) {
	if err := validateResponsesOutputItems(items); err != nil {
		return nil, err
	}
	envelope := responsesProviderData{
		Transport:   responsesProviderTransport,
		Version:     responsesProviderVersion,
		OutputItems: items,
	}
	return json.Marshal(envelope)
}

// decodeResponsesProviderData 校验信封并返回深拷贝的 output items。
// 任何校验失败都返回 false，调用方应回退到通用投影。
func decodeResponsesProviderData(raw json.RawMessage) ([]json.RawMessage, bool) {
	var envelope responsesProviderData
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	if envelope.Transport != responsesProviderTransport || envelope.Version != responsesProviderVersion {
		return nil, false
	}
	if err := validateResponsesOutputItems(envelope.OutputItems); err != nil {
		return nil, false
	}
	items := make([]json.RawMessage, len(envelope.OutputItems))
	for i, item := range envelope.OutputItems {
		items[i] = append(json.RawMessage(nil), item...)
	}
	return items, true
}

// validateResponsesOutputItems 校验每个 output item 是带非空 type 的 JSON object。
// 未知 item type 不拒绝，以便前向兼容。
func validateResponsesOutputItems(items []json.RawMessage) error {
	for i, item := range items {
		if len(bytes.TrimSpace(item)) == 0 || !json.Valid(item) || bytes.TrimSpace(item)[0] != '{' {
			return fmt.Errorf("Responses output item %d 不是有效 JSON object", i)
		}
		var view struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &view); err != nil || strings.TrimSpace(view.Type) == "" {
			return fmt.Errorf("Responses output item %d 缺少非空 type", i)
		}
	}
	return nil
}

// responsesRequest models the OpenAI Responses API without coupling the rest
// of the agent loop to provider-specific input/output item shapes.
type responsesRequest struct {
	Model     string            `json:"model"`
	Input     []json.RawMessage `json:"input"`
	Stream    bool              `json:"stream"`
	Reasoning RequestBody       `json:"reasoning,omitempty"`
	Tools     []responsesTool   `json:"tools,omitempty"`
}

// responsesItem 用于把通用消息投影为 Responses input item。
type responsesItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type responsesFunctionCallOutputItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// responsesTool always serializes strict explicitly. Adapter preparation owns
// the value and the wire schema, independently from the Responses transport.
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

// responsesAPIResponse 的 Output 保留为 raw items，作为权威完成快照，
// 由 completedResponsesEvent 统一验证并生成最终 StreamEvent。
type responsesAPIResponse struct {
	ID     string            `json:"id,omitempty"`
	Status string            `json:"status,omitempty"`
	Output []json.RawMessage `json:"output"`
	Usage  *Usage            `json:"usage,omitempty"`
	Error  *responsesError   `json:"error,omitempty"`
}

// responsesOutputItemView 是已知字段的轻量视图，用于从 raw item 中提取
// 文本、reasoning summary 和函数调用；未知字段原样保留在 raw bytes 中。
type responsesOutputItemView struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Summary   json.RawMessage `json:"summary,omitempty"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content,omitempty"`
}

type responsesError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

type responsesStreamEvent struct {
	Type        string                `json:"type"`
	Delta       string                `json:"delta,omitempty"`
	Text        string                `json:"text,omitempty"`
	ItemID      string                `json:"item_id,omitempty"`
	OutputIndex int                   `json:"output_index,omitempty"`
	CallID      string                `json:"call_id,omitempty"`
	Name        string                `json:"name,omitempty"`
	Item        json.RawMessage       `json:"item,omitempty"`
	Response    *responsesAPIResponse `json:"response,omitempty"`
	Error       *responsesError       `json:"error,omitempty"`
}

func observeResponsesResponse(ctx context.Context, response *responsesAPIResponse, eventType string) {
	responseID, status := "", ""
	if response != nil {
		responseID, status = response.ID, response.Status
	}
	switch eventType {
	case "response.failed", "error":
		status = "failed"
	case "response.incomplete":
		status = "incomplete"
	case "response.completed":
		switch status {
		case "":
			status = "completed"
		case "completed", "failed", "incomplete", "canceled":
		default:
			status = "incomplete"
		}
	}
	if response != nil && response.Error != nil && status != "incomplete" && status != "canceled" {
		status = "failed"
	}
	observeProviderResponse(ctx, responseID, status)
}

func shouldUseResponsesAPI(cfg Config) bool {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if strings.Contains(transport, "response") {
		return true
	}
	if transport != "" {
		return false
	}
	path := strings.TrimRight(strings.ToLower(strings.TrimSpace(cfg.APIPath)), "/")
	return strings.HasSuffix(path, "/responses")
}

func responsesReasoningSummary(view responsesOutputItemView, raw bool) string {
	var structured []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if json.Unmarshal(view.Summary, &structured) == nil {
		parts := make([]string, 0, len(structured))
		for _, summary := range structured {
			if summary.Type != "summary_text" {
				continue
			}
			parts = append(parts, summary.Text)
		}
		if len(parts) > 0 {
			return joinResponsesReasoning(parts, raw)
		}
	}
	var summaries []string
	if json.Unmarshal(view.Summary, &summaries) != nil {
		return ""
	}
	return joinResponsesReasoning(summaries, raw)
}

func responsesReasoningText(view responsesOutputItemView, raw bool) string {
	if summary := responsesReasoningSummary(view, raw); summary != "" {
		return summary
	}
	parts := make([]string, 0, len(view.Content))
	for _, content := range view.Content {
		if content.Type != "reasoning_text" {
			continue
		}
		parts = append(parts, content.Text)
	}
	return joinResponsesReasoning(parts, raw)
}

func joinResponsesReasoning(parts []string, raw bool) string {
	if raw {
		return strings.Join(parts, "")
	}
	formatted := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := strings.TrimSpace(part); text != "" {
			formatted = append(formatted, text)
		}
	}
	return strings.Join(formatted, "\n\n")
}

func isGPTResponsesConfig(cfg Config) bool {
	adapter := strings.ToLower(strings.TrimSpace(cfg.Adapter))
	if adapter == "gpt" {
		return true
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	model := strings.ToLower(strings.TrimSpace(cfg.Model))
	return provider == "gpt" || provider == "openai" || strings.HasPrefix(model, "gpt-")
}

func defaultResponsesReasoning(cfg Config) RequestBody {
	if !isGPTResponsesConfig(cfg) {
		return nil
	}
	return RequestBody{"summary": "auto"}
}

func effectiveResponsesExtraRequestBody(cfg Config) RequestBody {
	body := CloneRequestBody(EffectiveExtraRequestBody(cfg))
	if body == nil {
		body = make(RequestBody)
	}

	if effort, ok := body["reasoning_effort"]; ok {
		reasoning, reasoningOK := jsonObject(body["reasoning"])
		if !reasoningOK {
			reasoning = make(RequestBody)
		}
		if _, exists := reasoning["effort"]; !exists {
			reasoning["effort"] = cloneJSONValue(effort)
		}
		body["reasoning"] = reasoning
		delete(body, "reasoning_effort")
	}
	// thinking is an Anthropic Messages field and is not valid Responses input.
	delete(body, "thinking")
	return body
}

func buildResponsesRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (responsesRequest, error) {
	input, err := buildResponsesInput(cfg, messages)
	if err != nil {
		return responsesRequest{}, err
	}
	// wire 层兜底：隔离孤儿 function_call_output、给悬空 function_call 补合成
	// output（覆盖 ProviderData 重放与结构化字段不一致的崩溃场景）。
	input, _ = repairResponsesInputItems(input)
	if err := validateResponsesInputItems(input); err != nil {
		return responsesRequest{}, err
	}
	req := responsesRequest{
		Model:     cfg.Model,
		Input:     input,
		Stream:    stream,
		Reasoning: defaultResponsesReasoning(cfg),
	}
	for _, tool := range tools {
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
		}
		req.Tools = append(req.Tools, responsesTool{
			Type: "function", Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: tool.Strict,
		})
	}
	return req, nil
}

func validateResponsesInputItems(items []json.RawMessage) error {
	for i, raw := range items {
		var view struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			return fmt.Errorf("Responses input item %d 不是有效 JSON: %w", i, err)
		}
		if view.Type != "function_call_output" {
			continue
		}

		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			return fmt.Errorf("Responses function_call_output item %d 不是有效 JSON object: %w", i, err)
		}
		var callID string
		callIDRaw, ok := item["call_id"]
		if !ok || json.Unmarshal(callIDRaw, &callID) != nil || strings.TrimSpace(callID) == "" {
			return fmt.Errorf("Responses function_call_output item %d 缺少非空 call_id", i)
		}
		outputRaw, ok := item["output"]
		if !ok {
			return fmt.Errorf("Responses function_call_output item %d 缺少 output", i)
		}
		var output string
		if err := json.Unmarshal(outputRaw, &output); err != nil {
			return fmt.Errorf("Responses function_call_output item %d 的 output 必须是字符串", i)
		}
	}
	return nil
}

// responsesInputReplayStrippedFields 是同一 provider/model 回放 output item 时，
// Responses input 端不接受的 output-only 字段。
var responsesInputReplayStrippedFields = []string{"status"}

// stripResponsesOutputOnlyFields 清洗回放 item 中仅 output 侧合法的字段。
// 解析或重编码失败时原样返回：宁可让 provider 报错也不静默改写内容。
func stripResponsesOutputOnlyFields(item json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(item, &fields); err != nil {
		return item
	}
	changed := false
	for _, key := range responsesInputReplayStrippedFields {
		if _, ok := fields[key]; ok {
			delete(fields, key)
			changed = true
		}
	}
	if !changed {
		return item
	}
	cleaned, err := json.Marshal(fields)
	if err != nil {
		return item
	}
	return cleaned
}

func responsesProviderDataReplayCompatible(cfg Config, origin *message.MessageOrigin) bool {
	if origin == nil {
		return false
	}
	current := messageOriginForConfig(cfg, responsesProviderTransport)
	if !strings.EqualFold(strings.TrimSpace(origin.Transport), current.Transport) ||
		strings.TrimSpace(origin.Model) != current.Model {
		return false
	}

	originProfile := strings.TrimSpace(origin.ProfileID)
	currentProfile := strings.TrimSpace(current.ProfileID)
	if originProfile != "" || currentProfile != "" {
		if originProfile == "" || currentProfile == "" || originProfile != currentProfile {
			return false
		}
	} else {
		originProvider := strings.TrimSpace(origin.Provider)
		currentProvider := strings.TrimSpace(current.Provider)
		if originProvider == "" || currentProvider == "" || !strings.EqualFold(originProvider, currentProvider) {
			return false
		}
	}

	originProvider := strings.TrimSpace(origin.Provider)
	currentProvider := strings.TrimSpace(current.Provider)
	if originProvider != "" || currentProvider != "" {
		if originProvider == "" || currentProvider == "" || !strings.EqualFold(originProvider, currentProvider) {
			return false
		}
	}
	originAdapter := strings.TrimSpace(origin.Adapter)
	currentAdapter := strings.TrimSpace(current.Adapter)
	if originAdapter != "" || currentAdapter != "" {
		if originAdapter == "" || currentAdapter == "" || !strings.EqualFold(originAdapter, currentAdapter) {
			return false
		}
	}
	return true
}

func buildResponsesInput(cfg Config, messages []message.Message) ([]json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		calls := messageToolCalls(msg)
		results := messageToolResults(msg)

		// ProviderData 只在同一 provider/profile、transport、adapter 和 model
		// 上权威重放。切换供应商或模型时回退到通用 message/tool 投影，避免
		// 把 action、encrypted_content 等 provider 私有 output 字段送给新端点。
		if len(results) == 0 && len(msg.ProviderData) != 0 && responsesProviderDataReplayCompatible(cfg, msg.GeneratedBy) {
			if replayed, ok := decodeResponsesProviderData(msg.ProviderData); ok {
				for _, item := range replayed {
					items = append(items, stripResponsesOutputOnlyFields(item))
				}
				continue
			}
		}

		// Keep ordinary text (including assistant reasoning preceding a call) as a
		// message item, then append native function_call/function_call_output items.
		if len(results) == 0 && (strings.TrimSpace(msg.Content) != "" || len(msg.Parts) != 0) {
			content, err := responsesMessageContent(msg)
			if err != nil {
				return nil, err
			}
			item, err := json.Marshal(responsesItem{Role: string(msg.Role), Content: content})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		for _, call := range calls {
			arguments := strings.TrimSpace(string(call.Input))
			if arguments == "" || !json.Valid([]byte(arguments)) {
				arguments = "{}"
			}
			item, err := json.Marshal(responsesItem{
				Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: arguments,
			})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		for _, result := range results {
			item, err := json.Marshal(responsesFunctionCallOutputItem{
				Type: "function_call_output", CallID: result.ToolUseID, Output: result.Content,
			})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func responsesMessageContent(msg message.Message) (any, error) {
	if !hasImagePart(msg.Parts) {
		return msg.Content, nil
	}
	parts := make([]responsesContentPart, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case message.ContentPartText:
			parts = append(parts, responsesContentPart{Type: "input_text", Text: part.Text})
		case message.ContentPartImage:
			if part.Image == nil || len(part.Image.Data) == 0 {
				return nil, fmt.Errorf("OpenAI Responses 图片消息缺少已加载的附件")
			}
			mimeType := strings.TrimSpace(part.Image.MIMEType)
			if mimeType == "" {
				return nil, fmt.Errorf("OpenAI Responses 图片消息缺少 MIME 类型")
			}
			parts = append(parts, responsesContentPart{
				Type: "input_image", ImageURL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(part.Image.Data),
			})
		default:
			return nil, fmt.Errorf("OpenAI Responses 消息包含未知内容块类型: %q", part.Type)
		}
	}
	return parts, nil
}

func messageToolCalls(msg message.Message) []message.ToolCall {
	if len(msg.AssistantParts) > 0 {
		calls := make([]message.ToolCall, 0)
		for _, part := range msg.AssistantParts {
			if part.ToolCall != nil {
				calls = append(calls, *part.ToolCall)
			}
		}
		return calls
	}
	if len(msg.ToolUses) != 0 {
		return msg.ToolUses
	}
	if msg.ToolUse != nil {
		return []message.ToolCall{*msg.ToolUse}
	}
	return nil
}

func messageToolResults(msg message.Message) []message.ToolResult {
	if len(msg.ToolResults) != 0 {
		return msg.ToolResults
	}
	if msg.ToolResult != nil {
		return []message.ToolResult{*msg.ToolResult}
	}
	return nil
}

func (c *Client) runResponsesMessage(ctx context.Context, cfg Config, messages []message.Message) (string, error) {
	reqBody, err := buildResponsesRequestForAdapter(cfg, SelectModelAdapter(cfg), messages, nil, false)
	if err != nil {
		return "", fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	if err := ValidateExtraRequestBodies(cfg); err != nil {
		return "", fmt.Errorf("校验请求体配置失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, effectiveResponsesExtraRequestBody(cfg))
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		c.setRequestHeadersForConfig(req, cfg)
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", newProviderHTTPErrorWithReadError(resp.StatusCode, resp.Header, data, err, "模型接口")
	}
	if err != nil {
		return "", fmt.Errorf("读取响应体失败: %w", err)
	}
	var parsed responsesAPIResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("解析 Responses JSON 失败: %w", err)
	}
	observeResponsesResponse(ctx, &parsed, "")
	observeProviderUsage(ctx, parsed.Usage)
	if parsed.Error != nil {
		return "", fmt.Errorf("模型接口返回错误: %s", parsed.Error.Message)
	}
	var text strings.Builder
	for _, raw := range parsed.Output {
		var view responsesOutputItemView
		if err := json.Unmarshal(raw, &view); err != nil {
			return "", fmt.Errorf("解析 Responses output item 失败: %w", err)
		}
		if view.Type != "message" {
			continue
		}
		for _, part := range view.Content {
			if part.Type == "output_text" {
				text.WriteString(part.Text)
			}
		}
	}
	content := strings.TrimSpace(text.String())
	if content == "" {
		return "", fmt.Errorf("模型接口返回了空内容")
	}
	return content, nil
}

func (c *Client) nonStreamingResponsesMessage(ctx context.Context, cfg Config, messages []message.Message, tools PreparedToolSet) (<-chan StreamEvent, error) {
	reqBody, err := buildResponsesRequestForAdapter(cfg, SelectModelAdapter(cfg), messages, tools, false)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI Responses 请求失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, effectiveResponsesExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		c.setRequestHeadersForConfig(req, cfg)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newProviderHTTPErrorWithReadError(resp.StatusCode, resp.Header, data, err, "模型接口")
	}
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}
	var parsed responsesAPIResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析 Responses JSON 失败: %w", err)
	}
	observeResponsesResponse(ctx, &parsed, "")
	observeProviderUsage(ctx, parsed.Usage)
	if parsed.Error != nil {
		return nil, fmt.Errorf("模型接口返回错误: %s", parsed.Error.Message)
	}
	if parsed.Status != "" && parsed.Status != "completed" {
		return nil, fmt.Errorf("Responses API 响应未完成 (status=%s)", parsed.Status)
	}
	event, err := completedResponsesEvent(parsed.Output, parsed.Usage, false)
	if err != nil {
		return nil, err
	}
	events := make(chan StreamEvent, 8)
	if event.Usage != nil {
		events <- StreamEvent{Usage: event.Usage}
	}
	if event.Thinking != "" {
		events <- StreamEvent{Thinking: event.Thinking}
	}
	if event.Delta != "" {
		events <- StreamEvent{Delta: event.Delta}
	}
	if len(event.ToolCalls) != 0 {
		events <- StreamEvent{ToolCalls: event.ToolCalls}
	}
	if len(event.ProviderData) != 0 {
		events <- StreamEvent{ProviderData: event.ProviderData}
	}
	events <- StreamEvent{Done: true}
	close(events)
	return events, nil
}

// completedResponsesEvent 验证快照并将工具、ProviderData 与 Done 组成同一事件。
// rawReasoning 保留原始拼接，避免展示格式影响已播放 delta 的字节级前缀校验。
func completedResponsesEvent(output []json.RawMessage, usage *Usage, rawReasoning bool) (StreamEvent, error) {
	if err := validateResponsesOutputItems(output); err != nil {
		return StreamEvent{}, err
	}
	var calls []message.ToolCall
	var thinking strings.Builder
	var text strings.Builder
	for _, raw := range output {
		var view responsesOutputItemView
		if err := json.Unmarshal(raw, &view); err != nil {
			return StreamEvent{}, fmt.Errorf("解析 Responses output item 失败: %w", err)
		}
		switch view.Type {
		case "reasoning":
			if reasoning := responsesReasoningText(view, rawReasoning); reasoning != "" {
				if !rawReasoning && thinking.Len() > 0 {
					thinking.WriteString("\n\n")
				}
				thinking.WriteString(reasoning)
			}
		case "message":
			for _, part := range view.Content {
				if part.Type == "output_text" && part.Text != "" {
					text.WriteString(part.Text)
				}
			}
		case "function_call":
			call, err := responseToolCall(view)
			if err != nil {
				call.InputError = fmt.Sprintf("Responses API 工具参数无法解析，可修正参数后重试: %v", err)
			}
			calls = append(calls, call)
		}
	}
	providerData, err := encodeResponsesProviderData(output)
	if err != nil {
		return StreamEvent{}, err
	}
	return StreamEvent{
		Thinking:     thinking.String(),
		Delta:        text.String(),
		ToolCalls:    calls,
		ProviderData: providerData,
		Usage:        usage,
		Done:         true,
	}, nil
}

func responseToolCall(view responsesOutputItemView) (message.ToolCall, error) {
	id := view.CallID
	if id == "" {
		id = view.ID
	}
	input, err := decodeToolArguments("Responses API", id, view.Name, []byte(view.Arguments))
	if err != nil {
		return message.ToolCall{ID: id, Name: view.Name, Input: json.RawMessage(`{}`)}, err
	}
	return message.ToolCall{ID: id, Name: view.Name, Input: input}, nil
}

type activeResponseToolCall struct {
	item   json.RawMessage
	args   strings.Builder
	id     string
	callID string
	name   string
}

type responsesStreamResult struct {
	err        error
	retryAfter time.Duration
}

func responsesRawOutputWithDeltas(active map[int]*activeResponseToolCall) []json.RawMessage {
	indexes := make([]int, 0, len(active))
	for index := range active {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	output := make([]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		call := active[index]
		if call == nil || len(call.item) == 0 {
			continue
		}
		if len(call.args.String()) != 0 {
			call.item = responseItemWithArguments(call.item, call.args.String())
		}
		output = append(output, call.item)
	}
	return output
}

func mergeResponsesOutputWithDeltas(output []json.RawMessage, active map[int]*activeResponseToolCall) []json.RawMessage {
	merged := append([]json.RawMessage(nil), output...)
	for index, call := range active {
		if call == nil || call.args.Len() == 0 {
			continue
		}
		if index >= 0 && index < len(merged) {
			merged[index] = responseItemWithArguments(merged[index], call.args.String())
		} else if len(call.item) != 0 {
			merged = append(merged, responseItemWithArguments(call.item, call.args.String()))
		}
	}
	return merged
}

func responseItemWithArguments(raw json.RawMessage, arguments string) json.RawMessage {
	var item map[string]any
	if json.Unmarshal(raw, &item) != nil {
		return raw
	}
	item["arguments"] = arguments
	encoded, err := json.Marshal(item)
	if err != nil {
		return raw
	}
	return encoded
}

// responsesRawOutput 按 output_index 排序收集 raw output items。
func responsesRawOutput(active map[int]json.RawMessage) []json.RawMessage {
	indexes := make([]int, 0, len(active))
	for index := range active {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	output := make([]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		if raw := active[index]; len(raw) != 0 {
			output = append(output, raw)
		}
	}
	return output
}
