package tokentracer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"paw/internal/capability/model"
)

func ledgerReaderFixture(t *testing.T, data []byte) (*LedgerReader, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "events-000001.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	reader := NewLedgerReader(home)
	reader.instances["instance"] = InstanceMetadata{ID: "instance", ProjectID: "project"}
	return reader, path
}

func ledgerReaderLine(t *testing.T, requestID string) []byte {
	t.Helper()
	data, err := json.Marshal(ledgerRecord{Version: ledgerVersion, InstanceID: "instance", Seq: 1, Event: model.RequestEvent{Kind: "request_start", RequestID: requestID}})
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestLedgerReaderChargesUnterminatedBytesToScanBudget(t *testing.T) {
	reader, path := ledgerReaderFixture(t, bytes.Repeat([]byte("x"), maxLedgerRecordBytes+1))
	budget := int64(4096)
	if reader.scanFile("instance", path, &budget) || budget != 0 {
		t.Fatalf("unterminated input bypassed budget: remaining=%d issues=%v", budget, reader.issues)
	}
	if _, ok := reader.issues[":scan_backlog"]; !ok {
		t.Fatalf("budget exhaustion not reported: %v", reader.issues)
	}
	if reader.files[path].offset != 0 {
		t.Fatal("incomplete record advanced committed cursor")
	}
}

func TestLedgerReaderSmallBudgetsMakeProgressWithoutDuplicateReads(t *testing.T) {
	line := ledgerReaderLine(t, "request")
	reader, path := ledgerReaderFixture(t, line)
	var consumed int64
	for i := 0; i < len(line)/23+2; i++ {
		budget := int64(23)
		done := reader.scanFile("instance", path, &budget)
		if budget < 0 {
			t.Fatalf("read exceeded budget: %d", budget)
		}
		consumed += 23 - budget
		if done {
			break
		}
	}
	if consumed != int64(len(line)) || len(reader.requests) != 1 || reader.sequences["instance"] != 1 {
		t.Fatalf("scan did not converge exactly once: bytes=%d want=%d requests=%d sequences=%v", consumed, len(line), len(reader.requests), reader.sequences)
	}
	if _, ok := reader.issues["instance:partial_tail"]; ok {
		t.Fatal("budget boundary was mislabeled as an incomplete file")
	}
}

func TestLedgerReaderReleasesPartialPayloadWithoutRereadingUnchangedTail(t *testing.T) {
	line := ledgerReaderLine(t, "request")
	reader, path := ledgerReaderFixture(t, line[:len(line)-1])
	budget := int64(len(line) + 10)
	if reader.scanFile("instance", path, &budget) || budget != 11 {
		t.Fatalf("partial read not charged: remaining=%d", budget)
	}
	if cap(reader.files[path].pending) != 0 {
		t.Fatal("EOF partial payload remained allocated")
	}
	budget = 100
	if reader.scanFile("instance", path, &budget) || budget != 100 {
		t.Fatalf("unchanged partial tail was reread: remaining=%d", budget)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{'\n'}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	budget = int64(len(line) + 10)
	if !reader.scanFile("instance", path, &budget) || budget != 10 || len(reader.requests) != 1 {
		t.Fatalf("completed tail failed to resume: remaining=%d requests=%d issues=%v", budget, len(reader.requests), reader.issues)
	}
}

func TestLedgerReaderRetainsOnlyOneBudgetCutoffPayload(t *testing.T) {
	data := bytes.Repeat([]byte("x"), maxLedgerRecordBytes-1)
	reader, firstPath := ledgerReaderFixture(t, data)
	for i := 0; i < 6; i++ {
		path := filepath.Join(t.TempDir(), "events-000001.jsonl")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		budget := int64(maxLedgerRecordBytes - 2)
		reader.scanFile("instance", path, &budget)
		pendingFiles, retained := 0, 0
		for _, cursor := range reader.files {
			if cap(cursor.pending) > 0 {
				pendingFiles++
				retained += cap(cursor.pending)
			}
		}
		if pendingFiles > 1 || retained > maxLedgerRecordBytes {
			t.Fatalf("pending payload is not globally bounded: files=%d retained=%d", pendingFiles, retained)
		}
	}
	budget := int64(len(data))
	reader.scanFile("instance", firstPath, &budget)
	for _, cursor := range reader.files {
		if cap(cursor.pending) != 0 {
			t.Fatal("EOF did not release pending payloads")
		}
	}
}

func TestLedgerReaderEvictedCutoffPayloadRestartsWithinBudget(t *testing.T) {
	line := ledgerReaderLine(t, "request")
	reader, path := ledgerReaderFixture(t, line)
	budget := int64(23)
	reader.scanFile("instance", path, &budget)
	otherPath := filepath.Join(t.TempDir(), "events-000001.jsonl")
	if err := os.WriteFile(otherPath, bytes.Repeat([]byte("x"), 100), 0600); err != nil {
		t.Fatal(err)
	}
	budget = 23
	reader.scanFile("instance", otherPath, &budget)
	if cap(reader.files[path].pending) != 0 {
		t.Fatal("inactive cutoff payload was not evicted")
	}
	var consumed int64
	for i := 0; i < len(line)/23+2; i++ {
		budget = 23
		done := reader.scanFile("instance", path, &budget)
		if budget < 0 {
			t.Fatalf("rescan exceeded budget: %d", budget)
		}
		consumed += 23 - budget
		if done {
			break
		}
	}
	if consumed != int64(len(line)) || len(reader.requests) != 1 {
		t.Fatalf("evicted payload did not recover: charged=%d want=%d requests=%d issues=%v", consumed, len(line), len(reader.requests), reader.issues)
	}
}

func TestLedgerReaderOversizedLineDoesNotLoseFollowingRecord(t *testing.T) {
	data := append(bytes.Repeat([]byte("x"), maxLedgerRecordBytes+1), '\n')
	data = append(data, ledgerReaderLine(t, "request")...)
	reader, path := ledgerReaderFixture(t, data)
	var consumed int64
	for i := 0; i < len(data)/(64<<10)+2; i++ {
		budget := int64(64 << 10)
		done := reader.scanFile("instance", path, &budget)
		if budget < 0 {
			t.Fatalf("oversized line bypassed budget: %d", budget)
		}
		consumed += 64<<10 - budget
		if done {
			break
		}
	}
	if consumed != int64(len(data)) || len(reader.requests) != 1 {
		t.Fatalf("oversized line blocked progress: bytes=%d want=%d requests=%d", consumed, len(data), len(reader.requests))
	}
	if _, ok := reader.issues["instance:invalid_record"]; !ok {
		t.Fatal("oversized record was not reported")
	}
}

func TestLedgerReaderDetectsSameInodeTruncateAndRegrow(t *testing.T) {
	reader, path := ledgerReaderFixture(t, ledgerReaderLine(t, "before"))
	budget := int64(4096)
	if !reader.scanFile("instance", path, &budget) {
		t.Fatalf("initial read failed: %v", reader.issues)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ledgerReaderLine(t, "after-with-more-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || after.Size() <= before.Size() {
		t.Fatal("fixture did not rewrite the same inode past the prior cursor")
	}
	budget = 4096
	if reader.scanFile("instance", path, &budget) {
		t.Fatal("mutated immutable segment was accepted")
	}
	if _, ok := reader.issues["instance:segment_replaced_or_truncated"]; !ok {
		t.Fatalf("same-inode rewrite not reported: %v", reader.issues)
	}
	if len(reader.requests) != 1 || reader.requests["instance/before"] == nil {
		t.Fatal("mutation changed already accepted history")
	}
}

func TestLedgerReaderScanBacklogClearsAfterLargePartialTails(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: workspace})
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(filepath.Join(recorder.dir, "events-000001.jsonl"), maxLedgerSegmentBytes); err != nil {
			t.Fatal(err)
		}
	}
	reader := NewLedgerReader(home)
	first, err := reader.Snapshot()
	if err != nil || !ledgerHasIssue(first, "scan_backlog") {
		t.Fatalf("80MiB of partial data did not exhaust the 64MiB scan budget: issues=%v err=%v", first.Issues, err)
	}
	for i := 0; i < 2; i++ {
		snapshot, err := reader.Snapshot()
		if err != nil || ledgerHasIssue(snapshot, "scan_backlog") {
			t.Fatalf("unchanged partial tails starved later instances: issues=%v err=%v", snapshot.Issues, err)
		}
		partials := 0
		for _, issue := range snapshot.Issues {
			if issue.Kind == "partial_tail" {
				partials++
			}
		}
		if partials != 5 {
			t.Fatalf("not all incomplete tails were inspected: %d", partials)
		}
	}
}

func TestLedgerReaderReleasesEOFPartialPayloadsAcrossInstances(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	for i := 0; i < 6; i++ {
		recorder, err := NewRecorder(RecorderConfig{Home: home, Workspace: workspace})
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(filepath.Join(recorder.dir, "events-000001.jsonl"), maxLedgerRecordBytes-1); err != nil {
			t.Fatal(err)
		}
	}
	reader := NewLedgerReader(home)
	for i := 0; i < 2; i++ {
		snapshot, err := reader.Snapshot()
		if err != nil || ledgerHasIssue(snapshot, "scan_backlog") {
			t.Fatalf("sub-limit tails failed to scan: issues=%v err=%v", snapshot.Issues, err)
		}
		partials := 0
		for _, issue := range snapshot.Issues {
			if issue.Kind == "partial_tail" {
				partials++
			}
		}
		if partials != 6 {
			t.Fatalf("not all incomplete tails were inspected: %d", partials)
		}
		for path, cursor := range reader.files {
			if cap(cursor.pending) != 0 {
				t.Fatalf("EOF payload remains allocated for %s: %d", path, cap(cursor.pending))
			}
		}
	}
}
