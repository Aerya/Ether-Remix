package debrid

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type capWaitKey struct{}
type capResponseKey struct{}
type capResponseRead struct {
	measurement *core.MediaCapWait
	began       time.Time
}
type capInterval struct{ from, to time.Time }

// One bounded upstream run can be joined by multiple responses. Keep admission
// intervals so each response counts only the portion after it joined, including
// a run started by read-ahead. Cache hits never consult this record.
type capWaitRecord struct {
	mu        sync.Mutex
	intervals []capInterval
	started   time.Time
}

func recordCapWait(ctx context.Context, from time.Time) {
	if record, _ := ctx.Value(capWaitKey{}).(*capWaitRecord); record != nil {
		to := time.Now()
		record.mu.Lock()
		record.intervals = append(record.intervals, capInterval{from, to})
		record.mu.Unlock()
	}
}
func (record *capWaitRecord) since(from time.Time) time.Duration {
	if record == nil {
		return 0
	}
	record.mu.Lock()
	intervals := append([]capInterval(nil), record.intervals...)
	started := record.started
	record.mu.Unlock()
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].from.Before(intervals[j].from) })
	// Union intervals: concurrent hedge admissions must not double-count time.
	var waited time.Duration
	for _, interval := range intervals {
		if !started.IsZero() && interval.to.After(started) {
			interval.to = started
		}
		start := interval.from
		if start.Before(from) {
			start = from
		}
		if interval.to.After(start) {
			waited += interval.to.Sub(start)
			from = interval.to
		}
	}
	return waited
}

func noteCapReadStarted(ctx context.Context) {
	if record, _ := ctx.Value(capWaitKey{}).(*capWaitRecord); record != nil {
		record.mu.Lock()
		if record.started.IsZero() {
			record.started = time.Now()
		}
		record.mu.Unlock()
	}
}
