package pluginitems

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	// plannerStatsGrowthFactor is the multiplicative growth in a plugin
	// library's item count since its last analysis that triggers a targeted
	// SQLite ANALYZE. Daily PRAGMA optimize waits for tenfold table-wide
	// growth, which leaves newly published libraries misestimated (#621).
	plannerStatsGrowthFactor int64 = 4

	// plannerStatsDebounce coalesces bursts of push-mode upserts or back-to-back
	// sync completions into one ANALYZE pass after writes go quiet.
	plannerStatsDebounce = 10 * time.Second

	// plannerStatsMinInterval bounds how often any single library may trigger
	// an analysis pass. Qualifying growth that arrives during cooldown is
	// deferred until the window elapses rather than dropped.
	plannerStatsMinInterval = 5 * time.Minute

	// plannerStatsTimeout bounds a single background ANALYZE pass.
	plannerStatsTimeout = 5 * time.Minute

	// plannerStatsMaxRetries caps consecutive background retries after a
	// transient failure before waiting for the next publish/sync.
	plannerStatsMaxRetries = 3
)

// plannerStatsTables lists the SQLite tables whose planner statistics go stale
// when a plugin library is published after the last ANALYZE (#621). It covers
// plugin_items, its facet/field index table, and every related pluginitems
// table carrying library_id. plugin_item_fts is an FTS5 virtual table and does
// not store statistics in sqlite_stat1/sqlite_stat4.
var plannerStatsTables = []string{
	"plugin_items",
	"plugin_item_fields",
	"plugin_item_field_tally",
	"plugin_item_person_tally",
	"plugin_item_parent_tally",
	"plugin_item_tally",
	"plugin_facet_expansions",
	"plugin_kinds",
}

type statsTimer interface {
	Stop() bool
}

type libraryStatsState struct {
	analyzed          bool
	lastAnalyzedCount int64
	lastAnalyzedAt    time.Time
	pending           bool
	pendingFirstSync  bool
	pendingCount      int64
	dueAt             time.Time
	writeSeq          uint64
}

// statsRefresher schedules bounded, single-flight, debounced SQLite ANALYZE
// passes for plugin item tables off the request path. On PostgreSQL it is nil
// and every method is a no-op (PostgreSQL autovacuum maintains statistics).
type statsRefresher struct {
	db           *sql.DB
	log          *slog.Logger
	now          func() time.Time
	leader       func() bool
	growthFactor int64
	debounce     time.Duration
	minInterval  time.Duration
	timeout      time.Duration
	afterFunc    func(time.Duration, func()) statsTimer
	analyzeFn    func(context.Context, *sql.DB, ...string) error

	baseCtx context.Context
	cancel  context.CancelFunc

	mu              sync.Mutex
	closed          bool
	running         bool
	runDone         chan struct{}
	timer           statsTimer
	timerGen        uint64
	timerDue        time.Time
	writeSeq        uint64
	consecutiveErrs int
	purgedDuringRun map[core.LibraryID]bool
	libs            map[core.LibraryID]*libraryStatsState
}

func newStatsRefresher(ctx context.Context, db *sql.DB, log *slog.Logger, now func() time.Time, leader func() bool) *statsRefresher {
	if db == nil || store.DialectOf(db) == store.Postgres {
		return nil
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if leader == nil {
		leader = func() bool { return true }
	}
	baseCtx, cancel := context.WithCancel(context.Background())
	r := &statsRefresher{
		db:           db,
		log:          log,
		now:          now,
		leader:       leader,
		growthFactor: plannerStatsGrowthFactor,
		debounce:     plannerStatsDebounce,
		minInterval:  plannerStatsMinInterval,
		timeout:      plannerStatsTimeout,
		afterFunc: func(d time.Duration, fn func()) statsTimer {
			return time.AfterFunc(d, fn)
		},
		analyzeFn: store.AnalyzeFull,
		baseCtx:   baseCtx,
		cancel:    cancel,
		libs:      make(map[core.LibraryID]*libraryStatsState),
	}
	r.seedFromDB(ctx)
	return r
}

// seedFromDB initializes per-library baseline item counts from existing
// statistics on startup so healthy restarts do not re-analyze on small delta
// syncs, while still refusing to treat stale statistics (e.g. a library
// published after the last ANALYZE, #621) as already analyzed.
func (r *statsRefresher) seedFromDB(ctx context.Context) {
	if r == nil || r.db == nil {
		return
	}
	var hasStat1 int
	if err := r.db.QueryRowContext(ctx,
		store.SQLiteOnly+`SELECT COUNT(*) FROM sqlite_schema WHERE name = 'sqlite_stat1'`).Scan(&hasStat1); err != nil || hasStat1 == 0 {
		return
	}
	var stat string
	if err := r.db.QueryRowContext(ctx,
		store.SQLiteOnly+`SELECT stat FROM sqlite_stat1 WHERE tbl = 'plugin_items' AND idx = 'plugin_items_browse' LIMIT 1`).Scan(&stat); err != nil {
		return
	}
	fields := strings.Fields(stat)
	if len(fields) < 2 {
		return
	}
	statRows, err1 := strconv.ParseInt(fields[0], 10, 64)
	rowsPerLib, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil || statRows <= 0 || rowsPerLib <= 0 {
		return
	}
	statLibs := max(int64(1), (statRows+rowsPerLib/2)/rowsPerLib)

	counts, err := snapshotLibraryItemCounts(ctx, r.db)
	if err != nil || len(counts) == 0 {
		return
	}
	var sumTally, nonEmptyLibs int64
	for _, n := range counts {
		if n > 0 {
			sumTally += n
			nonEmptyLibs++
		}
	}
	if sumTally <= 0 || sumTally >= statRows*r.growthFactor || nonEmptyLibs > statLibs {
		return
	}
	for libID, n := range counts {
		if n > 0 {
			r.libs[libID] = &libraryStatsState{
				analyzed:          true,
				lastAnalyzedCount: n,
			}
		}
	}
}

func (r *statsRefresher) noteLibraryItems(libraryID core.LibraryID, itemCount int64, firstSync bool) {
	if r == nil || libraryID <= 0 || itemCount <= 0 || !r.leader() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	st := r.libs[libraryID]
	if st == nil {
		st = &libraryStatsState{}
		r.libs[libraryID] = st
	}
	r.writeSeq++
	st.writeSeq = r.writeSeq

	needsRefresh := !st.analyzed ||
		(firstSync && itemCount > st.lastAnalyzedCount) ||
		(st.lastAnalyzedCount > 0 && itemCount >= st.lastAnalyzedCount*r.growthFactor)
	if !needsRefresh {
		return
	}

	now := r.now()
	due := now.Add(r.debounce)
	if !st.lastAnalyzedAt.IsZero() {
		if earliest := st.lastAnalyzedAt.Add(r.minInterval); earliest.After(due) {
			due = earliest
		}
	}
	st.pending = true
	st.pendingFirstSync = st.pendingFirstSync || firstSync
	st.pendingCount = itemCount
	st.dueAt = due

	r.armTimerLocked(now)
}

func (r *statsRefresher) forgetLibrary(libraryID core.LibraryID) {
	if r == nil || libraryID <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	delete(r.libs, libraryID)
	if r.running {
		if r.purgedDuringRun == nil {
			r.purgedDuringRun = make(map[core.LibraryID]bool)
		}
		r.purgedDuringRun[libraryID] = true
		return
	}
	r.armTimerLocked(r.now())
}

func (r *statsRefresher) stopTimerLocked() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.timerDue = time.Time{}
	r.timerGen++
}

func (r *statsRefresher) armTimerLocked(now time.Time) {
	if r.closed || r.running {
		return
	}
	var (
		hasPending  bool
		earliestDue time.Time
	)
	for _, st := range r.libs {
		if !st.pending {
			continue
		}
		if !hasPending || st.dueAt.Before(earliestDue) {
			hasPending = true
			earliestDue = st.dueAt
		}
	}
	if !hasPending {
		r.stopTimerLocked()
		return
	}
	if r.timer != nil && r.timerDue.Equal(earliestDue) {
		return
	}
	r.stopTimerLocked()
	gen := r.timerGen
	r.timerDue = earliestDue
	delay := earliestDue.Sub(now)
	if delay < 0 {
		delay = 0
	}
	r.timer = r.afterFunc(delay, func() {
		r.onTimer(gen, earliestDue)
	})
}

func (r *statsRefresher) onTimer(gen uint64, firedDue time.Time) {
	r.mu.Lock()
	if r.closed || r.running || gen != r.timerGen {
		r.mu.Unlock()
		return
	}
	now := r.now()
	var triggerLibs []core.LibraryID
	for libID, st := range r.libs {
		if st.pending && (!st.dueAt.After(now) || !st.dueAt.After(firedDue)) {
			triggerLibs = append(triggerLibs, libID)
		}
	}
	if len(triggerLibs) == 0 {
		r.timer = nil
		r.timerDue = time.Time{}
		r.armTimerLocked(now)
		r.mu.Unlock()
		return
	}
	slices.Sort(triggerLibs)

	startWriteSeq := make(map[core.LibraryID]uint64, len(r.libs))
	for libID, st := range r.libs {
		startWriteSeq[libID] = st.writeSeq
	}
	r.timer = nil
	r.timerDue = time.Time{}
	r.running = true
	r.purgedDuringRun = nil
	done := make(chan struct{})
	r.runDone = done
	runCtx, cancel := context.WithTimeout(r.baseCtx, r.timeout)
	r.mu.Unlock()

	defer cancel()
	r.runAnalyze(runCtx, triggerLibs, startWriteSeq, done)
}

func (r *statsRefresher) runAnalyze(runCtx context.Context, triggerLibs []core.LibraryID, startWriteSeq map[core.LibraryID]uint64, done chan struct{}) {
	var (
		snapshot map[core.LibraryID]int64
		err      error
		started  = r.now()
		ran      bool
	)
	defer func() {
		finished := r.now()
		var (
			logSuccess bool
			logWarn    bool
		)
		r.mu.Lock()
		purged := r.purgedDuringRun
		r.purgedDuringRun = nil
		if ran {
			if err == nil {
				r.consecutiveErrs = 0
				logSuccess = true
				for libID, cnt := range snapshot {
					if cnt <= 0 || purged[libID] {
						continue
					}
					st := r.libs[libID]
					if st == nil {
						st = &libraryStatsState{}
						r.libs[libID] = st
					}
					st.analyzed = true
					st.lastAnalyzedCount = cnt
					st.lastAnalyzedAt = finished
				}
				for libID, st := range r.libs {
					if !st.pending {
						continue
					}
					if !purged[libID] && st.writeSeq == startWriteSeq[libID] {
						st.pending = false
						st.pendingFirstSync = false
						st.dueAt = time.Time{}
						continue
					}
					stillNeeds := !st.analyzed ||
						(st.pendingFirstSync && st.pendingCount > st.lastAnalyzedCount) ||
						(st.lastAnalyzedCount > 0 && st.pendingCount >= st.lastAnalyzedCount*r.growthFactor)
					if !stillNeeds {
						st.pending = false
						st.pendingFirstSync = false
						st.dueAt = time.Time{}
						continue
					}
					if !st.lastAnalyzedAt.IsZero() {
						if earliest := st.lastAnalyzedAt.Add(r.minInterval); earliest.After(st.dueAt) {
							st.dueAt = earliest
						}
					}
				}
			} else if !r.closed && !errors.Is(err, context.Canceled) {
				if errors.Is(err, sql.ErrConnDone) {
					for _, libID := range triggerLibs {
						if st := r.libs[libID]; st != nil && !purged[libID] && st.writeSeq == startWriteSeq[libID] {
							st.pending = false
							st.pendingFirstSync = false
							st.dueAt = time.Time{}
						}
					}
				} else {
					logWarn = true
					r.consecutiveErrs++
					for _, libID := range triggerLibs {
						st := r.libs[libID]
						if st == nil || !st.pending {
							continue
						}
						if r.consecutiveErrs < plannerStatsMaxRetries {
							if retryAt := finished.Add(r.minInterval); retryAt.After(st.dueAt) {
								st.dueAt = retryAt
							}
						} else {
							st.pending = false
							st.pendingFirstSync = false
							st.dueAt = time.Time{}
						}
					}
				}
			}
		} else {
			for _, libID := range triggerLibs {
				if st := r.libs[libID]; st != nil && !purged[libID] && st.writeSeq == startWriteSeq[libID] {
					st.pending = false
					st.pendingFirstSync = false
					st.dueAt = time.Time{}
				}
			}
		}
		r.running = false
		r.runDone = nil
		if !r.closed {
			r.armTimerLocked(finished)
		}
		r.mu.Unlock()

		if logSuccess && r.log != nil {
			r.log.Info("refreshed planner statistics",
				"libraries", formatLibraryIDs(triggerLibs),
				"tables", strings.Join(plannerStatsTables, ","),
				"took", finished.Sub(started).Round(time.Millisecond))
		} else if logWarn && r.log != nil {
			r.log.Warn("planner statistics refresh failed",
				"libraries", formatLibraryIDs(triggerLibs),
				"err", err,
				"took", finished.Sub(started).Round(time.Millisecond))
		}
		close(done)
	}()

	if !r.leader() {
		return
	}
	snapshot, err = snapshotLibraryItemCounts(runCtx, r.db)
	if err != nil {
		ran = true
		return
	}
	err = r.analyzeFn(runCtx, r.db, plannerStatsTables...)
	ran = true
}

func (r *statsRefresher) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.stopTimerLocked()
	done := r.runDone
	cancel := r.cancel
	r.mu.Unlock()

	cancel()
	if done != nil {
		<-done
	}
}

func snapshotLibraryItemCounts(ctx context.Context, db *sql.DB) (map[core.LibraryID]int64, error) {
	rows, err := db.QueryContext(ctx, `
SELECT library_id, COALESCE(SUM(n), 0)
FROM plugin_item_tally
GROUP BY library_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[core.LibraryID]int64)
	for rows.Next() {
		var libID, n int64
		if err := rows.Scan(&libID, &n); err != nil {
			return nil, err
		}
		if libID > 0 {
			out[core.LibraryID(libID)] = n
		}
	}
	return out, rows.Err()
}

func formatLibraryIDs(ids []core.LibraryID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(int64(id), 10)
	}
	return strings.Join(parts, ",")
}

func (s *Service) noteLibraryItems(libraryID core.LibraryID, itemCount int64, firstSync bool) {
	if s == nil || s.stats == nil {
		return
	}
	s.stats.noteLibraryItems(libraryID, itemCount, firstSync)
}

func (s *Service) forgetLibraryStats(libraryID core.LibraryID) {
	if s == nil || s.stats == nil {
		return
	}
	s.stats.forgetLibrary(libraryID)
}
