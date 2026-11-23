package flows

import (
	"context"
	"errors"
	"sort"
	"time"
)

// RunStatsQuery selects the window summarised by RunStats. Runs are
// counted by start time in [Since, Since+Bucket*Buckets).
type RunStatsQuery struct {
	Since   time.Time
	Bucket  time.Duration
	Buckets int
	// Failures caps RecentFailures (default 6, max 20).
	Failures int
}

// RunStatsBucket is one slot of the runs-over-time series. Succeeded is
// completed runs; Skipped is cancelled or throttled runs; Active is runs
// still pending, running or waiting.
type RunStatsBucket struct {
	Start         time.Time `json:"start"`
	Succeeded     int       `json:"succeeded"`
	Failed        int       `json:"failed"`
	Skipped       int       `json:"skipped"`
	Active        int       `json:"active"`
	AvgDurationMS int64     `json:"avgDurationMs"`
}

// RunStatsFlow is one flow's share of the window.
type RunStatsFlow struct {
	FlowID        string `json:"flowId"`
	Name          string `json:"name"`
	Runs          int    `json:"runs"`
	Failed        int    `json:"failed"`
	AvgDurationMS int64  `json:"avgDurationMs"`
}

// RunStatsTrigger counts runs by what started them.
type RunStatsTrigger struct {
	Kind      NodeKind `json:"kind"`
	EventType string   `json:"eventType,omitempty"`
	Runs      int      `json:"runs"`
}

// RunStatsTotals sums the window.
type RunStatsTotals struct {
	Runs          int   `json:"runs"`
	Succeeded     int   `json:"succeeded"`
	Failed        int   `json:"failed"`
	Skipped       int   `json:"skipped"`
	Active        int   `json:"active"`
	AvgDurationMS int64 `json:"avgDurationMs"`
}

// RunStats aggregates real (non-test) runs over a window for the Flows
// overview. Every list is bounded: buckets by the query, flows by the
// flow count, triggers to 8, failures to the query cap.
type RunStats struct {
	Since          time.Time         `json:"since"`
	BucketMS       int64             `json:"bucketMs"`
	Totals         RunStatsTotals    `json:"totals"`
	Buckets        []RunStatsBucket  `json:"buckets"`
	Flows          []RunStatsFlow    `json:"flows"`
	Triggers       []RunStatsTrigger `json:"triggers"`
	RecentFailures []Run             `json:"recentFailures"`
}

// RunStatsStore is implemented by stores that can aggregate run history
// in the database. The SQL store does; the admin API answers 503 for a
// store that does not.
type RunStatsStore interface {
	RunStats(ctx context.Context, q RunStatsQuery) (*RunStats, error)
}

var _ RunStatsStore = (*SQLStore)(nil)

// maxStatsBuckets bounds the series (30 days of daily or 48 hourly slots).
const maxStatsBuckets = 96

// statusClass maps a run status to the overview's four series.
func statusClass(s RunStatus) string {
	switch s {
	case RunCompleted:
		return "succeeded"
	case RunFailed:
		return "failed"
	case RunCancelled, RunThrottled:
		return "skipped"
	default:
		return "active"
	}
}

// RunStats aggregates the window with three grouped, index-backed range
// scans over flows_runs(started_at_ms) plus one bounded failure lookup.
// Durations average finished runs that succeeded or failed (a skipped run
// never did its work). Test runs are excluded.
func (s *SQLStore) RunStats(ctx context.Context, q RunStatsQuery) (*RunStats, error) {
	if q.Bucket <= 0 || q.Buckets <= 0 || q.Buckets > maxStatsBuckets {
		return nil, errors.New("flows: invalid stats window")
	}
	failCap := q.Failures
	if failCap <= 0 {
		failCap = 6
	}
	if failCap > 20 {
		failCap = 20
	}
	sinceMS := timeToMS(q.Since)
	bucketMS := q.Bucket.Milliseconds()
	untilMS := sinceMS + bucketMS*int64(q.Buckets)

	out := &RunStats{
		Since:          msToTime(sinceMS),
		BucketMS:       bucketMS,
		Buckets:        make([]RunStatsBucket, q.Buckets),
		Flows:          []RunStatsFlow{},
		Triggers:       []RunStatsTrigger{},
		RecentFailures: []Run{},
	}
	for i := range out.Buckets {
		out.Buckets[i].Start = msToTime(sinceMS + bucketMS*int64(i))
	}

	const durExpr = `CASE WHEN status IN ('completed', 'failed') AND finished_at_ms >= started_at_ms THEN finished_at_ms - started_at_ms ELSE 0 END`
	const timedExpr = `CASE WHEN status IN ('completed', 'failed') AND finished_at_ms >= started_at_ms THEN 1 ELSE 0 END`

	// 1. Runs over time by status.
	rows, err := s.db.QueryContext(ctx,
		`SELECT (started_at_ms - ?) / ? AS slot, status, COUNT(*), CAST(SUM(`+durExpr+`) AS BIGINT), CAST(SUM(`+timedExpr+`) AS BIGINT)
		 FROM flows_runs
		 WHERE started_at_ms >= ? AND started_at_ms < ? AND dry_run = 0
		 GROUP BY slot, status`,
		sinceMS, bucketMS, sinceMS, untilMS,
	)
	if err != nil {
		return nil, err
	}
	type acc struct{ durSum, timed int64 }
	bucketDur := make([]acc, q.Buckets)
	var totalDur acc
	for rows.Next() {
		var slot int64
		var status string
		var n int
		var durSum, timed int64
		if err := rows.Scan(&slot, &status, &n, &durSum, &timed); err != nil {
			rows.Close()
			return nil, err
		}
		if slot < 0 || slot >= int64(q.Buckets) {
			continue
		}
		b := &out.Buckets[slot]
		switch statusClass(RunStatus(status)) {
		case "succeeded":
			b.Succeeded += n
			out.Totals.Succeeded += n
		case "failed":
			b.Failed += n
			out.Totals.Failed += n
		case "skipped":
			b.Skipped += n
			out.Totals.Skipped += n
		default:
			b.Active += n
			out.Totals.Active += n
		}
		out.Totals.Runs += n
		bucketDur[slot].durSum += durSum
		bucketDur[slot].timed += timed
		totalDur.durSum += durSum
		totalDur.timed += timed
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, a := range bucketDur {
		if a.timed > 0 {
			out.Buckets[i].AvgDurationMS = a.durSum / a.timed
		}
	}
	if totalDur.timed > 0 {
		out.Totals.AvgDurationMS = totalDur.durSum / totalDur.timed
	}
	if out.Totals.Runs == 0 {
		return out, nil
	}

	// 2. Per flow (one row per flow that ran in the window).
	rows, err = s.db.QueryContext(ctx,
		`SELECT r.flow_id, COALESCE(MAX(d.name), ''), COUNT(*),
		        CAST(SUM(CASE WHEN r.status = 'failed' THEN 1 ELSE 0 END) AS BIGINT),
		        CAST(SUM(CASE WHEN r.status IN ('completed', 'failed') AND r.finished_at_ms >= r.started_at_ms THEN r.finished_at_ms - r.started_at_ms ELSE 0 END) AS BIGINT),
		        CAST(SUM(CASE WHEN r.status IN ('completed', 'failed') AND r.finished_at_ms >= r.started_at_ms THEN 1 ELSE 0 END) AS BIGINT)
		 FROM flows_runs r LEFT JOIN flows_definitions d ON d.id = r.flow_id
		 WHERE r.started_at_ms >= ? AND r.started_at_ms < ? AND r.dry_run = 0
		 GROUP BY r.flow_id`,
		sinceMS, untilMS,
	)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f RunStatsFlow
		var durSum, timed int64
		if err := rows.Scan(&f.FlowID, &f.Name, &f.Runs, &f.Failed, &durSum, &timed); err != nil {
			rows.Close()
			return nil, err
		}
		if timed > 0 {
			f.AvgDurationMS = durSum / timed
		}
		out.Flows = append(out.Flows, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out.Flows, func(i, j int) bool {
		if out.Flows[i].Runs != out.Flows[j].Runs {
			return out.Flows[i].Runs > out.Flows[j].Runs
		}
		return out.Flows[i].Name < out.Flows[j].Name
	})

	// 3. What started the runs.
	rows, err = s.db.QueryContext(ctx,
		`SELECT trigger_kind, event_type, COUNT(*) AS n
		 FROM flows_runs
		 WHERE started_at_ms >= ? AND started_at_ms < ? AND dry_run = 0
		 GROUP BY trigger_kind, event_type
		 ORDER BY n DESC, trigger_kind, event_type
		 LIMIT 8`,
		sinceMS, untilMS,
	)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tr RunStatsTrigger
		var kind string
		if err := rows.Scan(&kind, &tr.EventType, &tr.Runs); err != nil {
			rows.Close()
			return nil, err
		}
		tr.Kind = NodeKind(kind)
		out.Triggers = append(out.Triggers, tr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 4. The latest failures, newest first.
	if out.Totals.Failed > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+runSelectCols+` FROM flows_runs r LEFT JOIN flows_definitions d ON d.id = r.flow_id
			 WHERE r.started_at_ms >= ? AND r.started_at_ms < ? AND r.status = 'failed' AND r.dry_run = 0
			 ORDER BY r.started_at_ms DESC, r.id DESC LIMIT ?`,
			sinceMS, untilMS, failCap,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			r, err := scanRun(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			// The trace drawer loads context on demand; keep the list light.
			r.Trigger, r.Context = nil, nil
			out.RecentFailures = append(out.RecentFailures, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
