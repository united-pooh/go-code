package tokentracer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"paw/internal/capability/model"
)

func queryFixture(t testing.TB, count int) *LedgerReader {
	t.Helper()
	r := NewLedgerReader(t.TempDir())
	now := time.Now().UTC()
	for i := 0; i < count; i++ {
		instance := fmt.Sprintf("instance-%d", i%3)
		project := fmt.Sprintf("project-%d", i%3)
		r.instances[instance] = InstanceMetadata{ID: instance, ProjectID: project, ProjectName: project, Workspace: "/work/" + project, StartedAt: now, UpdatedAt: now}
		estimate := 7
		request := &LedgerRequest{ID: fmt.Sprintf("request-%06d", i), InstanceID: instance, ProjectID: project, Provider: "fixture", Model: "model", Scope: model.RequestScope{SessionID: "session", Purpose: "conversation"}, StartedAt: now.Add(-time.Duration(i) * time.Second), UpdatedAt: now, Status: "completed", Attempts: []LedgerAttempt{{Number: 1, Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 5}, AtomsState: "complete", Atoms: []model.InputAtom{{Path: "/tools/0", Category: "tool_definition", ToolName: "Read", EstimatedTokens: &estimate}}}}}
		request.Attempts[0].UsageFinality = "final"
		r.requests[instance+"/"+request.ID] = request
	}
	return r
}

func TestLedgerQueryPaginatesWithoutTruncatingTotals(t *testing.T) {
	r := queryFixture(t, 601)
	seen := map[string]bool{}
	for offset := 0; offset < 601; offset += 100 {
		page, err := r.Query(LedgerQuery{View: "requests", Period: "all", Offset: offset, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if page.Version != 2 {
			t.Fatalf("paginated shape must not masquerade as v1 full snapshot: %d", page.Version)
		}
		if page.Summary.Requests != 601 || page.Summary.Total != 63105 || page.Pagination.Total != 601 || len(page.Requests) > 100 {
			t.Fatalf("incorrect page: %+v", page)
		}
		for _, request := range page.Requests {
			key := request.InstanceID + "/" + request.ID
			if seen[key] {
				t.Fatalf("duplicate %s", key)
			}
			seen[key] = true
		}
		payload, _ := json.Marshal(page)
		for _, forbidden := range []string{`"atoms"`, `"usage"`, `"request_body_bytes"`} {
			if strings.Contains(string(payload), forbidden) {
				t.Fatalf("ordinary query contains detail: %s", forbidden)
			}
		}
		if len(payload) > 100000 {
			t.Fatalf("unbounded page: %d bytes", len(payload))
		}
	}
	if len(seen) != 601 {
		t.Fatalf("lost requests: %d", len(seen))
	}
}

func TestLedgerQueryGroupsToolsFiltersDetailAndExportAgree(t *testing.T) {
	r := queryFixture(t, 601)
	query := LedgerQuery{View: "projects", Period: "all", Limit: 1}
	page, err := r.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Groups) != 1 || page.Pagination.Total != 3 || page.Summary.Total != 63105 || page.Groups[0].Summary.Requests != 201 {
		t.Fatalf("group total lost: %+v", page)
	}
	query.View, query.Project, query.Tool = "tools", "project-0", "Read"
	page, err = r.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tools) != 1 || page.Tools[0].Definitions != 1407 || page.Tools[0].Occurrences != 201 || page.ProjectCount != 1 {
		t.Fatalf("tool aggregation: %+v", page)
	}
	exported, err := r.Export(query)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Requests) != 201 || exported.Total.Total() != page.Summary.Total {
		t.Fatal("export was paginated or ignored filters")
	}
	if len(exported.Instances) != 1 || exported.Instances[0].ProjectID != query.Project {
		t.Fatal("export leaked metadata from an unselected project")
	}
	detail, err := r.Request("instance-0", "request-000000")
	if err != nil || detail == nil || !reflect.DeepEqual(*detail, exported.Requests[0]) {
		t.Fatalf("detail differs from export: %+v %v", detail, err)
	}
	*detail.Attempts[0].Atoms[0].EstimatedTokens = 999
	again, _ := r.Request("instance-0", "request-000000")
	if *again.Attempts[0].Atoms[0].EstimatedTokens != 7 {
		t.Fatal("detail aliases reader state")
	}
	missing, err := r.Request("instance-1", "request-000000")
	if err != nil || missing != nil {
		t.Fatal("detail crossed instance identity")
	}
	query.View, query.Session, query.Model, query.Search = "requests", "project-0/session", "model", "REQUEST-000000"
	page, err = r.Query(query)
	if err != nil || page.Summary.Requests != 1 {
		t.Fatalf("filters not combined: %+v %v", page, err)
	}
	query.Tool = "Write"
	page, _ = r.Query(query)
	if page.Summary.Requests != 0 {
		t.Fatal("tool filter ignored")
	}
}

func TestLedgerQueryPeriodCoverageAndStableTies(t *testing.T) {
	r := queryFixture(t, 3)
	now := time.Now().UTC()
	r.requests["instance-0/request-000000"].StartedAt = now.Add(-48 * time.Hour)
	r.requests["instance-1/request-000001"].Attempts[0].Usage = nil
	r.requests["instance-2/request-000002"].Status = "running"
	page, err := r.Query(LedgerQuery{View: "requests", Period: "24h", Limit: 1, Offset: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Pagination.Offset != 1 || page.Summary.Requests != 2 || page.Summary.Total != 105 || page.Summary.Unknown != 1 || page.Summary.Running != 0 {
		t.Fatalf("period or unknown coverage lost: %+v", page)
	}
	trendTotal, trendCount := 0, 0
	for _, bin := range page.Trend {
		trendTotal += bin.Total
		trendCount += bin.Count
	}
	if trendTotal != page.Summary.Total || trendCount != page.Summary.Requests {
		t.Fatal("trend is paginated")
	}
	for _, request := range r.requests {
		request.StartedAt = now
	}
	for i := 0; i < 3; i++ {
		page, err := r.Query(LedgerQuery{View: "requests", Period: "all", Offset: i, Limit: 1})
		if err != nil || page.Requests[0].InstanceID != fmt.Sprintf("instance-%d", i) {
			t.Fatalf("unstable tied page: %+v %v", page, err)
		}
	}
}

func BenchmarkLedgerQuery100k(b *testing.B) {
	r := queryFixture(b, 100000)
	for _, view := range []string{"overview", "requests", "tools"} {
		b.Run(view, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				page, err := r.Query(LedgerQuery{View: view, Period: "all", Limit: 50})
				if err != nil {
					b.Fatal(err)
				}
				payload, err := json.Marshal(page)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(payload)), "payload-B")
			}
		})
	}
	b.Run("full_snapshot_baseline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			snapshot, err := r.Snapshot()
			if err != nil {
				b.Fatal(err)
			}
			payload, err := json.Marshal(snapshot)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(payload)), "payload-B")
		}
	})
}
