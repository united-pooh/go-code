package model

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"paw/internal/message"
)

const maxTelemetryBodyBytes = 8 << 20
const maxInputAtoms = 4096

type InputAtom struct {
	Path            string `json:"path"`
	Category        string `json:"category"`
	ToolName        string `json:"tool_name,omitempty"`
	WireBytes       int    `json:"wire_bytes"`
	EstimatedTokens *int   `json:"estimated_tokens,omitempty"`
	Estimator       string `json:"estimator,omitempty"`
}

type inputAtomBuilder struct {
	atoms []InputAtom
	names map[string]string
	state string
	bytes int
}

func requestInputAtoms(req *http.Request, messages []message.Message, transport string) ([]InputAtom, string) {
	if req.GetBody == nil {
		return nil, "unavailable"
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, "unavailable"
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxTelemetryBodyBytes+1))
	if err != nil {
		return nil, "read_error"
	}
	if len(data) > maxTelemetryBodyBytes {
		return nil, "body_limit"
	}
	return inputAtomsWithSources(data, telemetryTextSources(messages, transport))
}

func inputAtoms(data []byte) ([]InputAtom, string) {
	return inputAtomsWithSources(data, nil)
}

func inputAtomsWithSources(data []byte, sources []message.Message) ([]InputAtom, string) {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return nil, "invalid_json"
	}
	b := inputAtomBuilder{names: map[string]string{}, state: "complete"}
	for _, source := range sources {
		for _, call := range messageToolCalls(source) {
			b.names[call.ID] = call.Name
		}
	}
	for _, key := range []string{"messages", "input"} {
		for _, raw := range rawArray(root[key]) {
			b.findNames(raw)
		}
	}
	for _, key := range []string{"system", "instructions"} {
		if raw := root[key]; len(raw) > 0 {
			b.content(raw, "/"+key, "system_prompt", "")
		}
	}
	for _, key := range []string{"messages", "input"} {
		raw := root[key]
		if len(raw) == 0 {
			continue
		}
		if raw[0] == '"' {
			b.add(raw, "/"+key, "user_prompt", "", true)
			continue
		}
		for i, item := range rawArray(raw) {
			var source *message.Message
			if key == "messages" && i < len(sources) {
				source = &sources[i]
			}
			b.message(item, fmt.Sprintf("/%s/%d", key, i), source)
		}
	}
	for i, raw := range rawArray(root["tools"]) {
		obj := rawObject(raw)
		name := rawString(obj["name"])
		if function := rawObject(obj["function"]); function != nil {
			name = rawString(function["name"])
		}
		b.add(raw, fmt.Sprintf("/tools/%d", i), "tool_definition", name, true)
	}
	if len(b.atoms) == 0 {
		b.state = "partial"
	}
	if rest := len(data) - b.bytes; rest > 0 {
		b.atoms = append(b.atoms, InputAtom{Path: "/", Category: "request_envelope", WireBytes: rest})
	}
	return b.atoms, b.state
}

func (b *inputAtomBuilder) add(raw json.RawMessage, path, category, toolName string, estimable bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	if len(b.atoms) >= maxInputAtoms {
		b.state = "atom_limit"
		return
	}
	atom := InputAtom{Path: path, Category: category, ToolName: toolName, WireBytes: len(raw)}
	if estimable {
		text := string(raw)
		if raw[0] == '"' {
			text = rawString(raw)
		}
		ascii, other := 0, 0
		for _, r := range text {
			if r < 128 {
				ascii++
			} else {
				other++
			}
		}
		estimate := (ascii+3)/4 + other
		atom.EstimatedTokens, atom.Estimator = &estimate, "mixed_text_v1"
	} else {
		b.state = "partial"
	}
	b.bytes += len(raw)
	b.atoms = append(b.atoms, atom)
}

func (b *inputAtomBuilder) findNames(raw json.RawMessage) {
	obj := rawObject(raw)
	if name := rawString(obj["name"]); name != "" {
		for _, key := range []string{"id", "call_id"} {
			if id := rawString(obj[key]); id != "" {
				b.names[id] = name
			}
		}
	}
	for _, call := range rawArray(obj["tool_calls"]) {
		value := rawObject(call)
		b.names[rawString(value["id"])] = rawString(rawObject(value["function"])["name"])
	}
	for _, content := range rawArray(obj["content"]) {
		value := rawObject(content)
		if rawString(value["type"]) == "tool_use" {
			b.names[rawString(value["id"])] = rawString(value["name"])
		}
	}
}

func (b *inputAtomBuilder) message(raw json.RawMessage, path string, source *message.Message) {
	obj := rawObject(raw)
	switch rawString(obj["type"]) {
	case "function_call":
		b.add(obj["arguments"], path+"/arguments", "tool_arguments", rawString(obj["name"]), true)
		return
	case "function_call_output":
		b.content(obj["output"], path+"/output", "tool_result", b.names[rawString(obj["call_id"])])
		return
	case "reasoning":
		b.add(raw, path, "reasoning_history", "", false)
		return
	}
	category := "provider_other"
	name := ""
	switch rawString(obj["role"]) {
	case "system", "developer":
		category = "system_prompt"
	case "user":
		category = "user_prompt"
	case "assistant":
		category = "assistant_history"
	case "tool":
		category, name = "tool_result", b.names[rawString(obj["tool_call_id"])]
	}
	if !b.pawTextContent(obj["content"], path+"/content", source) {
		b.content(obj["content"], path+"/content", category, name)
	}
	for i, raw := range rawArray(obj["tool_calls"]) {
		function := rawObject(rawObject(raw)["function"])
		b.add(function["arguments"], fmt.Sprintf("%s/tool_calls/%d/function/arguments", path, i), "tool_arguments", rawString(function["name"]), true)
	}
	for _, key := range []string{"reasoning_content", "reasoning"} {
		if raw := obj[key]; len(raw) > 0 {
			b.add(raw, path+"/"+key, "reasoning_history", "", true)
		}
	}
}

func (b *inputAtomBuilder) content(raw json.RawMessage, path, category, name string) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	if raw[0] != '[' {
		b.add(raw, path, category, name, category != "provider_other")
		return
	}
	for i, part := range rawArray(raw) {
		obj := rawObject(part)
		partPath := fmt.Sprintf("%s/%d", path, i)
		switch rawString(obj["type"]) {
		case "text", "input_text", "output_text":
			b.add(obj["text"], partPath+"/text", category, name, true)
		case "tool_use":
			b.add(obj["input"], partPath+"/input", "tool_arguments", rawString(obj["name"]), true)
		case "tool_result":
			b.content(obj["content"], partPath+"/content", "tool_result", b.names[rawString(obj["tool_use_id"])])
		case "thinking":
			b.add(obj["thinking"], partPath+"/thinking", "reasoning_history", "", true)
		case "image", "image_url", "input_image", "input_audio", "audio", "document", "input_file":
			b.add(part, partPath, "multimodal", name, false)
		default:
			b.add(part, partPath, "provider_other", name, false)
		}
	}
}

func rawObject(raw json.RawMessage) map[string]json.RawMessage {
	var result map[string]json.RawMessage
	_ = json.Unmarshal(raw, &result)
	return result
}

func rawArray(raw json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	_ = json.Unmarshal(raw, &result)
	return result
}

func rawString(raw json.RawMessage) string {
	var result string
	_ = json.Unmarshal(raw, &result)
	return result
}
