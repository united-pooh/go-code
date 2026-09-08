package loop

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
)

func TestContextLimitSourcePrecedesLiteralAndCanBeUnbound(t *testing.T) {
	client := model.NewClient(model.Config{Model: "unknown-context-model", ContextLimitTokens: 64000})
	runner := NewEngineWithInstructionRoot(client, &fakeUI{}, tool.NewRegistry(), nil, "limit", t.TempDir())
	assertLimit := func(want int) {
		t.Helper()
		for _, input := range []int{0, -1} {
			if got := runner.ContextStats(input, "").LimitTokens; got != want {
				t.Fatalf("ContextStats(%d).LimitTokens = %d, want %d", input, got, want)
			}
		}
		if got := runner.contextLimit(); got != want {
			t.Fatalf("contextLimit = %d, want %d", got, want)
		}
	}
	assertLimit(64000)
	runner.SetContextLimitTokens(32000)
	assertLimit(32000)
	var live atomic.Int64
	live.Store(270000)
	runner.BindContextLimitSource(func() int { return int(live.Load()) })
	assertLimit(270000)
	for _, stale := range []int{1000000, 0, -1} {
		runner.SetContextLimitTokens(stale)
		assertLimit(270000)
	}
	live.Store(96000)
	assertLimit(96000)
	if got := runner.ContextStats(123, "").LimitTokens; got != 123 {
		t.Fatalf("positive ContextStats override = %d", got)
	}
	for _, invalid := range []int64{0, -1} {
		live.Store(invalid)
		assertLimit(model.DefaultContextLimitTokens)
	}
	runner.SetContextLimitTokens(48000)
	runner.BindContextLimitSource(nil)
	assertLimit(48000)
	runner.SetContextLimitTokens(0)
	assertLimit(64000)
	if got := runner.compact.limit(); got != 0 {
		t.Fatalf("unbound zero setter no longer disables automatic compaction: %d", got)
	}
	var absent *Engine
	if got := absent.ContextStats(0, "").LimitTokens; got != model.DefaultContextLimitTokens {
		t.Fatalf("nil engine limit = %d", got)
	}
}

func TestContextLimitSourceRunsOutsideCompactionLock(t *testing.T) {
	runner := NewEngineWithInstructionRoot(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "limit", t.TempDir())
	runner.BindContextLimitSource(func() int {
		runner.SetContextLimitTokens(7)
		return 64000
	})
	if got := runner.ContextStats(0, "").LimitTokens; got != 64000 {
		t.Fatalf("limit = %d", got)
	}
}

func TestContextLimitSourceConcurrentReadsAndRebinding(t *testing.T) {
	runner := NewEngineWithInstructionRoot(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "limit", t.TempDir())
	var live atomic.Int64
	live.Store(64000)
	source := func() int { return int(live.Load()) }
	runner.BindContextLimitSource(source)
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range 300 {
				if worker == 0 {
					live.Store(int64(64000 + i))
					runner.SetContextLimitTokens(i)
					runner.BindContextLimitSource(source)
				}
				if got := runner.ContextStats(0, "").LimitTokens; got < 64000 {
					t.Errorf("literal leaked through bound source: %d", got)
					return
				}
			}
		}()
	}
	workers.Wait()
}

func TestContextLimitSourceControlsSummaryPressure(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "maintenance", true: "legacy"}[legacy], func(t *testing.T) {
			client := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "summary", Done: true}}}}}
			runner := newPressureTestRunner(t, client, 1000000)
			history := pressureFixture("small tool result")
			limit := 1000000
			runner.BindContextLimitSource(func() int { return limit })
			maintain := func() bool {
				t.Helper()
				if legacy {
					_, result, err := runner.maybeCompactHistory(context.Background(), history)
					if err != nil {
						t.Fatal(err)
					}
					return result != nil
				}
				result, err := runner.maintainContextProjection(context.Background(), history, true)
				if err != nil {
					t.Fatal(err)
				}
				return result.summaryPerformed
			}
			if maintain() || len(client.calls) != 0 {
				t.Fatal("summary triggered below bound limit")
			}
			limit = 1000
			runner.SetContextLimitTokens(0)
			if !maintain() || len(client.calls) != 1 {
				t.Fatalf("lowered bound limit did not trigger summary: calls=%d", len(client.calls))
			}
		})
	}
}

func TestContextLimitSourceControlsStatePressure(t *testing.T) {
	runner, client, store, sessionID := newStateCompactionRunner(t)
	snapshot, err := store.LoadSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	limit := 1000000
	runner.BindContextLimitSource(func() int { return limit })
	result, err := runner.maintainContextProjection(context.Background(), snapshot.ActiveHistory, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.history) != len(snapshot.ActiveHistory) {
		t.Fatal("state compacted below bound limit")
	}
	limit = 4000
	runner.SetContextLimitTokens(0)
	result, err = runner.maintainContextProjection(context.Background(), snapshot.ActiveHistory, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.history) == 0 || result.history[len(result.history)-1].Content != stateRefreshInstruction {
		t.Fatal("lowered bound limit did not trigger state compaction")
	}
	if len(client.calls) != 0 {
		t.Fatalf("state compaction called summarizer %d times", len(client.calls))
	}
}
