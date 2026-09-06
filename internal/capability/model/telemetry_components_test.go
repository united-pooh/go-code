package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInputAtomsDescribeActualSerializedComponentsWithoutText(t *testing.T) {
	for _, body := range []string{
		`{"model":"fixture","messages":[{"role":"system","content":"private rules"},{"role":"user","content":"private question"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"Read","arguments":"{\"path\":\"secret.txt\"}"}}]},{"role":"tool","tool_call_id":"c1","content":"private result"}],"tools":[{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}]}`,
		`{"model":"fixture","system":"private rules","messages":[{"role":"user","content":[{"type":"text","text":"private question"}]},{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Read","input":{"path":"secret.txt"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"private result"}]}],"tools":[{"name":"Read","input_schema":{"type":"object"}}]}`,
		`{"model":"fixture","instructions":"private rules","input":[{"role":"user","content":[{"type":"input_text","text":"private question"}]},{"type":"function_call","call_id":"c1","name":"Read","arguments":"{\"path\":\"secret.txt\"}"},{"type":"function_call_output","call_id":"c1","output":"private result"}],"tools":[{"type":"function","name":"Read","parameters":{"type":"object"}}]}`,
	} {
		atoms, state := inputAtoms([]byte(body))
		if state != "complete" {
			t.Fatalf("state = %s", state)
		}
		categories := map[string]int{}
		wireBytes := 0
		for _, atom := range atoms {
			categories[atom.Category]++
			wireBytes += atom.WireBytes
			if strings.HasPrefix(atom.Category, "tool_") && atom.ToolName != "Read" {
				t.Errorf("tool attribution lost: %+v", atom)
			}
			if atom.Category != "request_envelope" && (atom.EstimatedTokens == nil || atom.Estimator != "mixed_text_v1") {
				t.Errorf("estimate provenance lost: %+v", atom)
			}
		}
		for _, category := range []string{"system_prompt", "user_prompt", "tool_definition", "tool_arguments", "tool_result", "request_envelope"} {
			if categories[category] != 1 {
				t.Errorf("%s count = %d in %+v", category, categories[category], atoms)
			}
		}
		if wireBytes != len(body) {
			t.Fatalf("wire accounting = %d, want %d", wireBytes, len(body))
		}
		encoded, err := json.Marshal(atoms)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private", "secret.txt"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("atom leaked body: %s", encoded)
			}
		}
	}
}

func TestInputAtomsDoNotPretendMultimodalOrUnknownBodiesAreCounted(t *testing.T) {
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,private"}]}]}`)
	atoms, state := inputAtoms(body)
	if state != "partial" || len(atoms) != 2 || atoms[0].Category != "multimodal" || atoms[0].EstimatedTokens != nil {
		t.Fatalf("unsupported image tokens are not known: %s %+v", state, atoms)
	}
	if _, state := inputAtoms([]byte(`{"custom_prompt":"private"}`)); state != "partial" {
		t.Fatal("unknown request treated as complete")
	}
	if _, state := inputAtoms([]byte(`{`)); state != "invalid_json" {
		t.Fatal("malformed body accepted")
	}
}
