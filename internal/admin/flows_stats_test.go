package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestRunStatsWindowAlignsToLocalBuckets(t *testing.T) {
	now := time.Date(2026, 11, 22, 17, 40, 0, 0, time.UTC) // 12:40 in Toronto
	toronto, _ := time.LoadLocation("America/Toronto")
	cases := []struct {
		rng, want string
		bucket    time.Duration
		n         int
	}{
		{"24h", "2026-11-21T13:00:00-05:00", time.Hour, 24},
		{"7d", "2026-11-15T18:00:00-05:00", 6 * time.Hour, 28},
		{"30d", "2026-10-24T00:00:00-04:00", 24 * time.Hour, 30},
		{"bogus", "2026-11-21T13:00:00-05:00", time.Hour, 24},
	}
	for _, c := range cases {
		_, q := runStatsWindow(c.rng, "America/Toronto", now)
		if got := q.Since.In(toronto).Format(time.RFC3339); got != c.want || q.Bucket != c.bucket || q.Buckets != c.n {
			t.Errorf("%s: since %s bucket %s n %d; want %s %s %d", c.rng, got, q.Bucket, q.Buckets, c.want, c.bucket, c.n)
		}
		if end := q.Since.Add(q.Bucket * time.Duration(q.Buckets)); !end.After(now) {
			t.Errorf("%s: window ends %s, before now", c.rng, end)
		}
	}
}

func TestFlowsStatsEndpoint(t *testing.T) {
	// The in-memory API fake cannot aggregate: 503, not a wrong answer.
	env := newFlowsTestEnv(t, nil)
	if rec := env.do(t, http.MethodGet, "/stats", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fake store stats = %d %s", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 30, 0, 0, time.UTC)
	st := flows.NewSQLStore(store.OpenTest(t))
	f, err := st.SaveFlow(ctx, flows.Flow{Name: "Alpha", Enabled: true, Graph: flows.Graph{SchemaVersion: flows.SchemaVersion}})
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range []flows.RunStatus{flows.RunCompleted, flows.RunFailed} {
		at := now.Add(-time.Duration(i+1) * time.Hour)
		if err := st.CreateRun(ctx, flows.Run{ID: "run_" + string(status), FlowID: f.ID, TriggerKind: flows.NodeTriggerManual, Status: status, StartedAt: at, UpdatedAt: at, FinishedAt: at.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	api := NewFlowsAPI(FlowsDeps{Store: st, Now: func() time.Time { return now }})
	env = &flowsTestEnv{api: api}
	rec := env.do(t, http.MethodGet, "/stats?range=24h&tz=UTC", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Range string         `json:"range"`
		Stats flows.RunStats `json:"stats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Range != "24h" || body.Stats.Totals.Runs != 2 || body.Stats.Totals.Failed != 1 || len(body.Stats.Buckets) != 24 || len(body.Stats.RecentFailures) != 1 {
		t.Fatalf("stats body = %s", rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, "/stats", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /stats = %d", rec.Code)
	}
}
