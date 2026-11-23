package flows

import (
	"context"
	"testing"
	"time"
)

func TestSQLStoreRunStatsAggregatesWindow(t *testing.T) {
	ctx := context.Background()
	clk := newFakeClock(time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC))
	st := openTestSQLStore(t, clk)
	a, err := st.SaveFlow(ctx, Flow{Name: "Alpha", Enabled: true, Graph: Graph{SchemaVersion: SchemaVersion}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.SaveFlow(ctx, Flow{Name: "Beta", Enabled: true, Graph: Graph{SchemaVersion: SchemaVersion}})
	if err != nil {
		t.Fatal(err)
	}
	since := clk.Now().Add(-3 * time.Hour)
	n := 0
	add := func(f *Flow, status RunStatus, at time.Time, took time.Duration, kind NodeKind, ev string, dry bool) {
		n++
		r := Run{ID: "run_" + string(rune('a'+n)), FlowID: f.ID, TriggerKind: kind, EventType: ev, Status: status, DryRun: dry, StartedAt: at, UpdatedAt: at, Error: string(status)}
		if status.Terminal() {
			r.FinishedAt = at.Add(took)
		}
		if err := st.CreateRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	h := time.Hour
	add(a, RunCompleted, since.Add(10*time.Minute), 2*time.Second, NodeTriggerEvent, "playback.session.started", false)
	add(a, RunCompleted, since.Add(20*time.Minute), 4*time.Second, NodeTriggerEvent, "playback.session.started", false)
	add(a, RunFailed, since.Add(h+5*time.Minute), 6*time.Second, NodeTriggerEvent, "playback.session.started", false)
	add(b, RunThrottled, since.Add(h+10*time.Minute), 0, NodeTriggerSchedule, "", false)
	add(b, RunFailed, since.Add(2*h+1*time.Minute), 12*time.Second, NodeTriggerSchedule, "", false)
	add(b, RunWaiting, since.Add(2*h+30*time.Minute), 0, NodeTriggerManual, "", false)
	// Outside the window and test runs never count.
	add(a, RunFailed, since.Add(-time.Minute), time.Second, NodeTriggerEvent, "playback.session.started", false)
	add(a, RunFailed, since.Add(30*time.Minute), time.Second, NodeTriggerEvent, "playback.session.started", true)

	got, err := st.RunStats(ctx, RunStatsQuery{Since: since, Bucket: h, Buckets: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals != (RunStatsTotals{Runs: 6, Succeeded: 2, Failed: 2, Skipped: 1, Active: 1, AvgDurationMS: 6000}) {
		t.Fatalf("totals = %+v", got.Totals)
	}
	want := []RunStatsBucket{
		{Succeeded: 2, AvgDurationMS: 3000},
		{Failed: 1, Skipped: 1, AvgDurationMS: 6000},
		{Failed: 1, Active: 1, AvgDurationMS: 12000},
	}
	for i, w := range want {
		g := got.Buckets[i]
		g.Start = time.Time{}
		if g != w {
			t.Errorf("bucket %d = %+v, want %+v", i, g, w)
		}
	}
	if len(got.Flows) != 2 || got.Flows[0].Name != "Alpha" || got.Flows[0].Runs != 3 || got.Flows[0].Failed != 1 || got.Flows[1].Failed != 1 {
		t.Fatalf("flows = %+v", got.Flows)
	}
	if len(got.Triggers) != 3 || got.Triggers[0].Kind != NodeTriggerEvent || got.Triggers[0].Runs != 3 {
		t.Fatalf("triggers = %+v", got.Triggers)
	}
	if len(got.RecentFailures) != 2 || got.RecentFailures[0].FlowName != "Beta" || got.RecentFailures[1].FlowName != "Alpha" {
		t.Fatalf("failures = %+v", got.RecentFailures)
	}

	empty, err := st.RunStats(ctx, RunStatsQuery{Since: since.Add(24 * h), Bucket: h, Buckets: 24})
	if err != nil || empty.Totals.Runs != 0 || len(empty.Buckets) != 24 || empty.Flows == nil || empty.RecentFailures == nil {
		t.Fatalf("empty window = %+v, %v", empty, err)
	}
	if _, err := st.RunStats(ctx, RunStatsQuery{Since: since, Bucket: h, Buckets: 1000}); err == nil {
		t.Fatal("unbounded bucket count accepted")
	}
}
