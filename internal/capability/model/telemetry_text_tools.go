package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"paw/internal/message"
)

func telemetryTextSources(messages []message.Message, transport string) []message.Message {
	if transport == "openai-responses" {
		return nil
	}
	if transport != "anthropic-compatible" {
		return messages
	}
	sources := make([]message.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == message.RoleSystem {
			continue
		}
		if msg.Role == message.RoleAssistant && strings.TrimSpace(msg.Content) == "" {
			continue
		}
		msg.Content = strings.TrimRight(msg.Content, "\n")
		sources = append(sources, msg)
	}
	return sources
}

// Text-tool fields are trusted only after matching the structured source and
// the actual serialized string. User-authored lookalikes remain user prompts.
func (b *inputAtomBuilder) pawTextContent(raw json.RawMessage, path string, source *message.Message) bool {
	if source == nil || len(raw) == 0 || raw[0] != '"' || rawString(raw) != source.Content {
		return false
	}
	type field struct {
		raw                 json.RawMessage
		key, category, name string
	}
	var fields []field
	if calls := messageToolCalls(*source); len(calls) > 0 {
		lines := strings.Split(source.Content, "\n")
		if len(lines) != len(calls) {
			return false
		}
		for i, call := range calls {
			obj := rawObject([]byte(lines[i]))
			if rawString(obj["type"]) != "tool_use" || rawString(obj["id"]) != call.ID || rawString(obj["name"]) != call.Name || !jsonEquivalent(obj["input"], call.Input) {
				return false
			}
			fields = append(fields, field{obj["input"], "input", "tool_arguments", call.Name})
		}
	} else if results := messageToolResults(*source); len(results) > 0 {
		label := "TOOL_RESULT:\n"
		if len(results) > 1 {
			label = "TOOL_RESULTS:\n"
		}
		if !strings.HasPrefix(source.Content, label) {
			return false
		}
		lines := strings.Split(strings.TrimPrefix(source.Content, label), "\n")
		if len(lines) != len(results) {
			return false
		}
		for i, result := range results {
			obj := rawObject([]byte(lines[i]))
			if rawString(obj["tool_use_id"]) != result.ToolUseID || rawString(obj["content"]) != result.Content {
				return false
			}
			fields = append(fields, field{obj["content"], "content", "tool_result", b.names[result.ToolUseID]})
		}
	} else {
		return false
	}
	for i, f := range fields {
		before := len(b.atoms)
		b.add(f.raw, fmt.Sprintf("%s#paw/%d/%s", path, i, f.key), f.category, f.name, true)
		if len(b.atoms) == before {
			continue
		}
		// The field lives inside a JSON string, so its wire size includes the
		// outer string's escaping. Unattributed wrapper bytes stay in envelope.
		encoded, _ := json.Marshal(string(f.raw))
		wireBytes := len(encoded) - 2
		b.bytes += wireBytes - b.atoms[before].WireBytes
		b.atoms[before].WireBytes = wireBytes
	}
	return true
}

func jsonEquivalent(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	return string(x) == string(y)
}
