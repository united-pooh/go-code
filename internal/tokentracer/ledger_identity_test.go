package tokentracer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/message"
)

func TestLedgerResponseIdentityAndFinality(t *testing.T) {
	for _, tc := range []struct {
		name, secondID string
		want           int
	}{
		{"replay", "response-a", 105},
		{"independent", "response-b", 207},
		{"missing", "", 207},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			emit := func(raw string) {
				t.Helper()
				var event model.RequestEvent
				if err := json.Unmarshal([]byte(raw), &event); err != nil {
					t.Fatal(err)
				}
				if err := recorder.Record(event); err != nil {
					t.Fatal(err)
				}
			}
			emit(`{"kind":"request_start","request_id":"logical","provider":"fixture"}`)
			emit(`{"kind":"attempt_start","request_id":"logical","attempt":1,"transport":"openai-responses"}`)
			emit(`{"kind":"attempt_usage","request_id":"logical","attempt":1,"provider_response_id":"response-a","usage_finality":"partial","usage":{"input_tokens":100,"output_tokens":2}}`)
			emit(`{"kind":"attempt_start","request_id":"logical","attempt":2,"transport":"openai-responses"}`)
			emit(fmt.Sprintf(`{"kind":"attempt_usage","request_id":"logical","attempt":2,"provider_response_id":%q,"usage_finality":"final","usage":{"input_tokens":100,"output_tokens":5}}`, tc.secondID))
			emit(`{"kind":"request_end","request_id":"logical","attempt":2,"status":"completed"}`)
			if err := recorder.Close(); err != nil {
				t.Fatal(err)
			}
			reader := NewLedgerReader(home)
			for i := 0; i < 3; i++ {
				if i == 2 {
					reader = NewLedgerReader(home)
				}
				snapshot, err := reader.Snapshot()
				if err != nil || snapshot.Total.Total() != tc.want {
					t.Fatalf("snapshot total = %d, want %d: %v", snapshot.Total.Total(), tc.want, err)
				}
				page, err := reader.Query(LedgerQuery{Period: "all"})
				if err != nil || page.Summary.Total != tc.want || page.Summary.Unknown != 1 || page.Summary.Attempts != 2 {
					t.Fatalf("summary = %+v, err=%v", page.Summary, err)
				}
				detail, err := reader.Request(recorder.InstanceID(), "logical")
				if err != nil || len(detail.Attempts) != 2 || detail.Attempts[0].Tokens.Total() != 102 || detail.Attempts[1].Tokens.Total() != 105 {
					t.Fatalf("raw attempts changed: %+v %v", detail, err)
				}
				wire, _ := json.Marshal(detail)
				var view struct {
					Summary RequestSummary `json:"summary"`
				}
				if err := json.Unmarshal(wire, &view); err != nil || view.Summary.Total != tc.want {
					t.Fatalf("detail summary = %s", wire)
				}
				exported, err := reader.Export(LedgerQuery{Period: "all"})
				if err != nil || exported.Total.Total() != tc.want {
					t.Fatalf("export = %+v %v", exported.Total, err)
				}
			}
		})
	}
}

func TestLedgerPartialUsageIsNotFinalCoverage(t *testing.T) {
	r := queryFixture(t, 1)
	request := r.requests["instance-0/request-000000"]
	request.Status = "failed"
	request.Attempts[0].UsageFinality = "partial"
	if got := summarizeRequest(request, "failed"); got.Total != 105 || got.Unknown != 1 || got.Reported != 1 {
		t.Fatalf("partial usage hidden: %+v", got)
	}
}

func TestLedgerResponseIdentityIsScopedAndCorrectionsReplace(t *testing.T) {
	r := queryFixture(t, 3)
	for _, request := range r.requests {
		request.Attempts[0].ProviderResponseID = "same-provider-id"
	}
	page, err := r.Query(LedgerQuery{Period: "all"})
	if err != nil || page.Summary.Total != 315 || page.Summary.Requests != 3 {
		t.Fatalf("response ID crossed request/instance: %+v %v", page.Summary, err)
	}
	request := r.requests["instance-0/request-000000"]
	request.Attempts = append(request.Attempts, LedgerAttempt{Number: 2, ProviderResponseID: "same-provider-id", UsageFinality: "final", Usage: &model.Usage{PromptTokens: 80, CompletionTokens: 3}})
	if got := summarizeRequest(request, "completed"); got.Total != 83 {
		t.Fatalf("downward correction did not replace: %+v", got)
	}
	request.Attempts[1].Transport = "different-transport"
	if got := summarizeRequest(request, "completed"); got.Total != 188 {
		t.Fatalf("transport identity crossed: %+v", got)
	}
	request.Attempts[1].Usage = nil
	if got := summarizeRequest(request, "completed"); got.Total != 105 || got.Unknown != 1 {
		t.Fatalf("unknown attempt backfilled: %+v", got)
	}
}

func TestLedgerRecordsResponsesReplayFromRealHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1)%2 == 1 {
			fmt.Fprint(w, "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"response-replayed\",\"status\":\"incomplete\",\"output\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":2}}}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-replayed\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":5}}}\n\n")
	}))
	defer server.Close()
	home := t.TempDir()
	recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	client := model.NewClient(model.Config{Transport: "openai-responses", APIBaseURL: server.URL, Model: "fixture", RetryCount: 1, Timeout: time.Second})
	client.SetRequestObserver(func(event model.RequestEvent) {
		if err := recorder.Record(event); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []string{"first-logical", "second-logical"} {
		events, err := client.StreamMessage(model.WithUsageRequestID(context.Background(), id), []message.Message{{Role: message.RoleUser, Content: "synthetic"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for event := range events {
			if event.Err != nil {
				t.Fatal(event.Err)
			}
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("actual HTTP calls = %d", calls.Load())
	}
	reader := NewLedgerReader(home)
	page, err := reader.Query(LedgerQuery{Period: "all"})
	if err != nil || page.Summary.Requests != 2 || page.Summary.Attempts != 4 || page.Summary.Total != 210 || page.Summary.Unknown != 2 {
		t.Fatalf("production composition = %+v %v", page.Summary, err)
	}
	for _, id := range []string{"first-logical", "second-logical"} {
		detail, err := reader.Request(recorder.InstanceID(), id)
		if err != nil || detail == nil || detail.Summary.Total != 105 || detail.Attempts[0].UsageFinality != "partial" || detail.Attempts[1].UsageFinality != "final" {
			t.Fatalf("detail = %+v %v", detail, err)
		}
	}
}
