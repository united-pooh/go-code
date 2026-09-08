package tokentracer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paw/internal/capability/model"
)

func TestLedgerAggregatesIndependentInstancesAndRetainsClosedHistory(t *testing.T) {
	home := t.TempDir()
	reader := NewLedgerReader(home)
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		workspace := filepath.Join(t.TempDir(), "same-name")
		if err := os.Mkdir(workspace, 0700); err != nil {
			t.Fatal(err)
		}
		recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: workspace})
		if err != nil {
			t.Fatal(err)
		}
		if ids[recorder.InstanceID()] {
			t.Fatal("instance collision")
		}
		ids[recorder.InstanceID()] = true
		for _, event := range []model.RequestEvent{
			{Kind: "request_start", RequestID: "same-request-id", Timestamp: time.Now(), Scope: model.RequestScope{SessionID: "same-session"}},
			{Kind: "attempt_start", RequestID: "same-request-id", Attempt: 1, Timestamp: time.Now()},
			{Kind: "attempt_usage", RequestID: "same-request-id", Attempt: 1, Timestamp: time.Now(), Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 2}},
			{Kind: "attempt_usage", RequestID: "same-request-id", Attempt: 1, Timestamp: time.Now(), Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 5}},
			{Kind: "request_end", RequestID: "same-request-id", Timestamp: time.Now(), Status: "completed"},
		} {
			if err := recorder.Record(event); err != nil {
				t.Fatal(err)
			}
		}
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		snapshot, err := reader.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Total.Input != 300 || snapshot.Total.Output != 15 || len(snapshot.Requests) != 3 || len(snapshot.Instances) != 3 {
			t.Fatalf("duplicated/lost totals: %+v", snapshot)
		}
		for _, instance := range snapshot.Instances {
			if instance.Status != "stopped" {
				t.Errorf("instance status = %s", instance.Status)
			}
		}
	}
	if snapshot, err := NewLedgerReader(home).Snapshot(); err != nil || snapshot.Total.Total() != 315 {
		t.Fatalf("reader restart lost history: %+v %v", snapshot, err)
	}
	if snapshot, err := NewLedgerReader(t.TempDir()).Snapshot(); err != nil || len(snapshot.Requests) != 0 {
		t.Fatal("configuration homes crossed")
	}
}

func TestLedgerExposesUnknownFailureAndIgnoresPartialTail(t *testing.T) {
	home := t.TempDir()
	recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := recorder.InstanceID()
	for _, event := range []model.RequestEvent{
		{Kind: "request_start", RequestID: "failed", Timestamp: time.Now()},
		{Kind: "attempt_start", RequestID: "failed", Attempt: 1, Timestamp: time.Now()},
		{Kind: "request_end", RequestID: "failed", Timestamp: time.Now(), Status: "failed"},
	} {
		if err := recorder.Record(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "tracer", "instances", id, "events-000001.jsonl")
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"version":1`); err != nil {
		t.Fatal(err)
	}
	reader := NewLedgerReader(home)
	snapshot, err := reader.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Coverage.Attempts != 1 || snapshot.Coverage.ReportedAttempts != 0 || snapshot.Requests[0].Attempts[0].Usage != nil || len(snapshot.Issues) == 0 {
		t.Fatalf("unknown/partial data hidden: %+v", snapshot)
	}
	if _, err := f.WriteString("}\n"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = reader.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Requests) != 1 || len(snapshot.Issues) == 0 {
		t.Fatal("invalid tail silently accepted")
	}
	data, err := os.ReadFile(filepath.Join(home, "tracer", "instances", id, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta InstanceMetadata
	if err := json.Unmarshal(data, &meta); err != nil || meta.ID != id {
		t.Fatalf("metadata = %s, err=%v", data, err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("telemetry permissions = %o", info.Mode().Perm())
	}
}

func TestLedgerValidTailRecoveryDuplicatesAndInvalidVersion(t *testing.T) {
	home := t.TempDir()
	recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Record(model.RequestEvent{Kind: "request_start", RequestID: "request"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(recorder.dir, "events-000001.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encode := func(version int, seq uint64, event model.RequestEvent) []byte {
		data, err := json.Marshal(ledgerRecord{Version: version, Seq: seq, InstanceID: recorder.InstanceID(), Event: event})
		if err != nil {
			t.Fatal(err)
		}
		return append(data, '\n')
	}
	line := encode(1, 2, model.RequestEvent{Kind: "attempt_start", RequestID: "request", Attempt: 1})
	if _, err := file.Write(line[:len(line)/2]); err != nil {
		t.Fatal(err)
	}
	reader := NewLedgerReader(home)
	first, err := reader.Snapshot()
	if err != nil || first.Coverage.Attempts != 0 || !ledgerHasIssue(first, "partial_tail") {
		t.Fatalf("partial = %+v %v", first, err)
	}
	if _, err := file.Write(line[len(line)/2:]); err != nil {
		t.Fatal(err)
	}
	second, err := reader.Snapshot()
	if err != nil || second.Coverage.Attempts != 1 || ledgerHasIssue(second, "partial_tail") {
		t.Fatalf("completed tail = %+v %v", second, err)
	}
	usage := model.RequestEvent{Kind: "attempt_usage", RequestID: "request", Attempt: 1, Usage: &model.Usage{PromptTokens: 10, CompletionTokens: 2}}
	for _, data := range [][]byte{line, encode(9, 3, usage), encode(1, 4, usage), encode(1, 4, usage)} {
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		snapshot, err := reader.Snapshot()
		if err != nil || snapshot.Total.Total() != 12 || snapshot.Coverage.Attempts != 1 || !ledgerHasIssue(snapshot, "invalid_record") || !ledgerHasIssue(snapshot, "sequence_gap") {
			t.Fatalf("replay/invalid = %+v %v", snapshot, err)
		}
	}
}

func TestLedgerRotationTruncationAndSymlinkAreVisible(t *testing.T) {
	for _, mutation := range []string{"truncate", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			home := t.TempDir()
			recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if err := recorder.Record(model.RequestEvent{Kind: "request_start", RequestID: "request"}); err != nil {
				t.Fatal(err)
			}
			recorder.mu.Lock()
			err = recorder.rotateLocked()
			recorder.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range []model.RequestEvent{
				{Kind: "attempt_start", RequestID: "request", Attempt: 1},
				{Kind: "attempt_usage", RequestID: "request", Attempt: 1, Usage: &model.Usage{PromptTokens: 20}},
			} {
				if err := recorder.Record(event); err != nil {
					t.Fatal(err)
				}
			}
			if err := recorder.Close(); err != nil {
				t.Fatal(err)
			}
			reader := NewLedgerReader(home)
			snapshot, err := reader.Snapshot()
			if err != nil || snapshot.Total.Total() != 20 || len(snapshot.Issues) != 0 {
				t.Fatalf("rotation = %+v %v", snapshot, err)
			}
			path := filepath.Join(recorder.dir, "events-000002.jsonl")
			issue := "segment_replaced_or_truncated"
			if mutation == "truncate" {
				if err := os.Truncate(path, 0); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
				issue = "segment_unavailable"
			}
			snapshot, err = reader.Snapshot()
			if err != nil || snapshot.Total.Total() != 20 || !ledgerHasIssue(snapshot, issue) {
				t.Fatalf("mutation = %+v %v", snapshot, err)
			}
		})
	}
}

func ledgerHasIssue(snapshot LedgerSnapshot, kind string) bool {
	for _, issue := range snapshot.Issues {
		if issue.Kind == kind {
			return true
		}
	}
	return false
}
