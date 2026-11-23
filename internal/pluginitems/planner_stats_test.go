package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeStatsClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeStatsClock() *fakeStatsClock {
	return &fakeStatsClock{now: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeStatsClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeStatsClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type scheduledStatsTimer struct {
	delay   time.Duration
	fn      func()
	stopped atomic.Bool
}

func (t *scheduledStatsTimer) Stop() bool {
	return !t.stopped.Swap(true)
}

type fakeTimerHarness struct {
	mu     sync.Mutex
	timers []*scheduledStatsTimer
}

func (h *fakeTimerHarness) afterFunc(d time.Duration, fn func()) statsTimer {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := &scheduledStatsTimer{delay: d, fn: fn}
	h.timers = append(h.timers, st)
	return st
}

func (h *fakeTimerHarness) active() *scheduledStatsTimer {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.timers) - 1; i >= 0; i-- {
		if !h.timers[i].stopped.Load() {
			return h.timers[i]
		}
	}
	return nil
}

func (h *fakeTimerHarness) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.timers)
}

func (h *fakeTimerHarness) fireActive(t *testing.T) {
	t.Helper()
	st := h.active()
	if st == nil {
		t.Fatal("expected an active planner-stats timer, found none")
	}
	st.stopped.Store(true)
	st.fn()
}

func makeStatsTestItems(prefix string, start, count int) []core.KindItem {
	items := make([]core.KindItem, 0, count)
	for i := start; i < start+count; i++ {
		title := fmt.Sprintf("%s Title %04d", prefix, i)
		if i == start {
			title = prefix + " Water Needle"
		}
		items = append(items, core.KindItem{
			ID:       fmt.Sprintf("%s-%04d", prefix, i),
			Level:    "series",
			Title:    title,
			Year:     2000 + (i % 25),
			Released: int64(1700000000 + i),
			ETag:     "v1",
			Fields: map[string]json.RawMessage{
				"tags": json.RawMessage(fmt.Sprintf(`["genre-%d"]`, i%5)),
			},
		})
	}
	return items
}

func readStat1(t *testing.T, db *sql.DB, tbl, idx string) (string, bool) {
	t.Helper()
	var hasStat1 int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name = 'sqlite_stat1'`).Scan(&hasStat1); err != nil {
		t.Fatal(err)
	}
	if hasStat1 == 0 {
		return "", false
	}
	var stat string
	err := db.QueryRow(`SELECT stat FROM sqlite_stat1 WHERE tbl = ? AND idx = ?`, tbl, idx).Scan(&stat)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return stat, true
}

func parseStat1Leading(t *testing.T, stat string) (total, leading int) {
	t.Helper()
	fields := strings.Fields(stat)
	if len(fields) < 2 {
		t.Fatalf("unexpected sqlite_stat1 value %q", stat)
	}
	var err error
	if total, err = strconv.Atoi(fields[0]); err != nil {
		t.Fatalf("parse total in %q: %v", stat, err)
	}
	if leading, err = strconv.Atoi(fields[1]); err != nil {
		t.Fatalf("parse leading selectivity in %q: %v", stat, err)
	}
	return total, leading
}

func TestPlannerStatsRefreshOnFirstSyncAndSecondLibrary(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	clock := newFakeStatsClock()
	timers := &fakeTimerHarness{}

	const itemsPerLib = 300
	fcA := &fakeSyncCaller{
		rootPages: map[string]core.KindPage{
			"": {Items: makeStatsTestItems("libA", 0, itemsPerLib)},
		},
	}
	fcB := &fakeSyncCaller{
		rootPages: map[string]core.KindPage{
			"": {Items: makeStatsTestItems("libB", 0, itemsPerLib)},
		},
	}

	s, err := New(ctx, db, Options{Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.stats.afterFunc = timers.afterFunc

	declA := sampleBookDecl()
	declB := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "plugin.alpha", 10, declA, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "plugin.beta", 20, declB, false); err != nil {
		t.Fatal(err)
	}

	// 1. Library A completes its first pull sync. SyncKind must return without
	// blocking on ANALYZE; instead it arms a debounced background timer.
	if _, err := s.SyncKind(ctx, "plugin.alpha", "book", SyncOptions{Caller: fcA}); err != nil {
		t.Fatalf("SyncKind lib A: %v", err)
	}
	if active := timers.active(); active == nil || active.delay != plannerStatsDebounce {
		t.Fatalf("after lib A first sync: active timer = %+v, want delay %v", active, plannerStatsDebounce)
	}
	if stat, ok := readStat1(t, db, "plugin_items", "plugin_items_browse"); ok && stat != "" {
		t.Fatalf("ANALYZE ran inline during SyncKind: plugin_items_browse stat = %q", stat)
	}

	// Fire the debounced refresh for library A.
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)

	statA, ok := readStat1(t, db, "plugin_items", "plugin_items_browse")
	if !ok {
		t.Fatal("expected sqlite_stat1 row for plugin_items_browse after lib A refresh")
	}
	totalA, perLibA := parseStat1Leading(t, statA)
	if totalA < itemsPerLib || perLibA < itemsPerLib {
		t.Fatalf("plugin_items_browse stat after lib A = %q, want >= %d rows per library", statA, itemsPerLib)
	}

	// Verify related facet and library_id tables were analyzed too.
	for _, tc := range []struct{ tbl, idx string }{
		{"plugin_items", "plugin_items_search_title"},
		{"plugin_item_fields", "plugin_item_fields_lookup"},
		{"plugin_item_field_tally", "plugin_item_field_tally_count"},
		{"plugin_kinds", "plugin_kinds_library"},
	} {
		if st, found := readStat1(t, db, tc.tbl, tc.idx); !found || st == "" {
			t.Errorf("expected sqlite_stat1 entry for %s.%s after refresh", tc.tbl, tc.idx)
		}
	}

	// Hold a pooled connection open BEFORE library B is published so we verify
	// reloadStatistics propagates the new statistics to existing connections.
	held, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var planSeq int
	queryPlan := func(libID int64) string {
		t.Helper()
		planSeq++
		var one int
		if err := held.QueryRowContext(ctx, `SELECT 1 FROM plugin_items LIMIT 1`).Scan(&one); err != nil {
			t.Fatal(err)
		}
		rows, err := held.QueryContext(ctx,
			`EXPLAIN QUERY PLAN SELECT COUNT(*) FROM plugin_item_fts f JOIN plugin_items i ON i.seq = f.rowid WHERE f.plugin_item_fts MATCH '"water"*' AND i.library_id = ? -- `+strconv.Itoa(planSeq),
			libID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		return strings.Join(details, " | ")
	}

	// 2. Library B completes its first sync (2x table growth, under SQLite's
	// 10x PRAGMA optimize threshold).
	if _, err := s.SyncKind(ctx, "plugin.beta", "book", SyncOptions{Caller: fcB}); err != nil {
		t.Fatalf("SyncKind lib B: %v", err)
	}
	if active := timers.active(); active == nil || active.delay != plannerStatsDebounce {
		t.Fatalf("after lib B first sync: active timer = %+v, want delay %v", active, plannerStatsDebounce)
	}

	// Before the refresh fires, library B is absent from sqlite_stat4, so the
	// planner leads with plugin_items instead of FTS on a bare library_id filter.
	beforePlan := queryPlan(20)
	if !strings.Contains(beforePlan, "SEARCH i USING") {
		t.Logf("note: pre-refresh plan on bare library_id=%d was %q", 20, beforePlan)
	}

	// Fire the debounced refresh for library B.
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)

	statAB, ok := readStat1(t, db, "plugin_items", "plugin_items_browse")
	if !ok {
		t.Fatal("expected sqlite_stat1 row for plugin_items_browse after lib B refresh")
	}
	totalAB, perLibAB := parseStat1Leading(t, statAB)
	if totalAB <= totalA || perLibAB >= totalAB {
		t.Fatalf("plugin_items_browse stat after lib B = %q (total=%d perLib=%d), want multi-library growth past %d", statAB, totalAB, perLibAB, totalA)
	}

	// After refresh, the held pooled connection must plan from FTS first even
	// without the unary + guard, proving library B is now in sqlite_stat1/stat4.
	afterPlan := queryPlan(20)
	if !strings.HasPrefix(afterPlan, "SCAN f VIRTUAL TABLE") {
		t.Fatalf("held connection plan for lib B after refresh = %q, want FTS-first (SCAN f VIRTUAL TABLE...)", afterPlan)
	}
}

func TestPlannerStatsRefreshPushDebounceAndGrowthThreshold(t *testing.T) {
	ctx := context.Background()
	clock := newFakeStatsClock()
	timers := &fakeTimerHarness{}
	var analyzeCalls atomic.Int32

	s, db := openTestService(t, Options{Now: clock.Now})
	if store.DialectOf(db) != store.SQLite {
		t.Skip("SQLite planner stats test")
	}
	s.stats.afterFunc = timers.afterFunc
	s.stats.analyzeFn = func(ctx context.Context, db *sql.DB, tables ...string) error {
		analyzeCalls.Add(1)
		return store.AnalyzeFull(ctx, db, tables...)
	}

	decl := sampleBookDecl()
	decl.Sync = plugins.KindSyncDecl{Mode: plugins.KindSyncPush}
	if _, err := s.UpsertKind(ctx, "community.push", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	// Three back-to-back push batches coalesce into a single debounced timer.
	for b := 0; b < 3; b++ {
		if _, err := s.KindUpsert(ctx, "community.push", "book", makeStatsTestItems("push", b*10, 10)); err != nil {
			t.Fatal(err)
		}
		clock.Advance(2 * time.Second)
	}
	if analyzeCalls.Load() != 0 {
		t.Fatalf("analyzeCalls before debounce settled = %d, want 0", analyzeCalls.Load())
	}
	if timers.active() == nil {
		t.Fatal("expected debounced timer to be active after push burst")
	}

	// Debounce expires -> one ANALYZE covers all 30 items.
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)
	if analyzeCalls.Load() != 1 {
		t.Fatalf("analyzeCalls after first publish = %d, want 1", analyzeCalls.Load())
	}

	// Move past cooldown, then grow 2x (30 -> 60) and 3x (60 -> 90): below 4x
	// threshold, so no refresh should be scheduled.
	clock.Advance(plannerStatsMinInterval + time.Minute)
	if _, err := s.KindUpsert(ctx, "community.push", "book", makeStatsTestItems("push", 30, 60)); err != nil {
		t.Fatal(err)
	}
	if timers.active() != nil {
		t.Fatalf("3x growth (30 -> 90) unexpectedly scheduled refresh: %+v", timers.active())
	}

	// Grow to 4x (90 -> 120): must schedule refresh.
	if _, err := s.KindUpsert(ctx, "community.push", "book", makeStatsTestItems("push", 90, 30)); err != nil {
		t.Fatal(err)
	}
	if active := timers.active(); active == nil || active.delay != plannerStatsDebounce {
		t.Fatalf("4x growth (30 -> 120): active timer = %+v, want delay %v", active, plannerStatsDebounce)
	}
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)
	if analyzeCalls.Load() != 2 {
		t.Fatalf("analyzeCalls after 4x growth = %d, want 2", analyzeCalls.Load())
	}
}

func TestPlannerStatsRefreshPerLibraryRateLimit(t *testing.T) {
	ctx := context.Background()
	clock := newFakeStatsClock()
	timers := &fakeTimerHarness{}
	var analyzeCalls atomic.Int32

	s, db := openTestService(t, Options{Now: clock.Now})
	if store.DialectOf(db) != store.SQLite {
		t.Skip("SQLite planner stats test")
	}
	s.stats.afterFunc = timers.afterFunc
	s.stats.analyzeFn = func(ctx context.Context, db *sql.DB, tables ...string) error {
		analyzeCalls.Add(1)
		return store.AnalyzeFull(ctx, db, tables...)
	}

	decl := sampleBookDecl()
	decl.Sync = plugins.KindSyncDecl{Mode: plugins.KindSyncPush}
	if _, err := s.UpsertKind(ctx, "community.a", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "community.b", 20, decl, false); err != nil {
		t.Fatal(err)
	}

	// Initial publish of library A (10 items) and analyze at t = 10s.
	if _, err := s.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 0, 10)); err != nil {
		t.Fatal(err)
	}
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)
	if analyzeCalls.Load() != 1 {
		t.Fatalf("analyzeCalls = %d, want 1", analyzeCalls.Load())
	}

	// 1 minute later (inside the 5m cooldown), library A grows 4x (10 -> 40).
	// The refresh must be deferred to the end of cooldown, NOT run immediately
	// and NOT dropped.
	clock.Advance(time.Minute)
	if _, err := s.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 10, 30)); err != nil {
		t.Fatal(err)
	}
	active := timers.active()
	if active == nil {
		t.Fatal("4x growth inside cooldown was dropped; want deferred timer")
	}
	wantRemaining := plannerStatsMinInterval - time.Minute
	if active.delay != wantRemaining {
		t.Fatalf("cooldown delay = %v, want %v", active.delay, wantRemaining)
	}

	// Meanwhile, a different library B's first publish is NOT blocked by
	// library A's cooldown: timer shortens to library B's debounce window.
	if _, err := s.KindUpsert(ctx, "community.b", "book", makeStatsTestItems("b", 0, 10)); err != nil {
		t.Fatal(err)
	}
	active = timers.active()
	if active == nil || active.delay != plannerStatsDebounce {
		t.Fatalf("lib B timer delay = %+v, want %v", active, plannerStatsDebounce)
	}

	// Firing library B's refresh analyzes the whole table, covering both A (40)
	// and B (10), so A's deferred pending flag clears with no redundant run.
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)
	if analyzeCalls.Load() != 2 {
		t.Fatalf("analyzeCalls = %d, want 2", analyzeCalls.Load())
	}
	if timers.active() != nil {
		t.Fatalf("expected no lingering timer after shared refresh, got %+v", timers.active())
	}
}

func TestPlannerStatsRefreshNeverConcurrent(t *testing.T) {
	ctx := context.Background()
	clock := newFakeStatsClock()
	timers := &fakeTimerHarness{}

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var inFlight atomic.Int32
	var maxConcurrent atomic.Int32
	var calls atomic.Int32

	s, db := openTestService(t, Options{Now: clock.Now})
	if store.DialectOf(db) != store.SQLite {
		t.Skip("SQLite planner stats test")
	}
	s.stats.afterFunc = timers.afterFunc
	s.stats.analyzeFn = func(ctx context.Context, db *sql.DB, tables ...string) error {
		cur := inFlight.Add(1)
		for {
			prev := maxConcurrent.Load()
			if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer inFlight.Add(-1)
		n := calls.Add(1)
		entered <- struct{}{}
		if n == 1 {
			<-release
		}
		return nil
	}

	decl := sampleBookDecl()
	decl.Sync = plugins.KindSyncDecl{Mode: plugins.KindSyncPush}
	if _, err := s.UpsertKind(ctx, "community.a", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "community.b", 20, decl, false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 0, 10)); err != nil {
		t.Fatal(err)
	}
	clock.Advance(plannerStatsDebounce)
	firstTimer := timers.active()
	if firstTimer == nil {
		t.Fatal("expected timer for lib A")
	}
	firstTimer.stopped.Store(true)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		firstTimer.fn()
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first analyze to start")
	}

	// Publish library B while the first analyze is in flight. Because an
	// analysis is already running, no timer or second concurrent run may start.
	if _, err := s.KindUpsert(ctx, "community.b", "book", makeStatsTestItems("b", 0, 10)); err != nil {
		t.Fatal(err)
	}
	if timers.active() != nil {
		t.Fatal("timer armed while an analysis pass was already running")
	}

	// Release the first pass; it must schedule a follow-up for library B.
	close(release)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first analyze to finish")
	}

	if timers.active() == nil {
		t.Fatal("expected follow-up timer for lib B after first analyze finished")
	}
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)

	if maxConcurrent.Load() != 1 {
		t.Fatalf("max concurrent ANALYZE runs = %d, want 1", maxConcurrent.Load())
	}
	if calls.Load() != 2 {
		t.Fatalf("total analyze calls = %d, want 2", calls.Load())
	}
}

func TestPlannerStatsRefreshSeedOnRestart(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Populate library A and gather complete statistics, simulating a healthy
	// server before restart.
	s1, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	decl := sampleBookDecl()
	decl.Sync = plugins.KindSyncDecl{Mode: plugins.KindSyncPush}
	if _, err := s1.UpsertKind(ctx, "community.a", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 0, 100)); err != nil {
		t.Fatal(err)
	}
	s1.Close()

	if err := store.AnalyzeFull(ctx, db, plannerStatsTables...); err != nil {
		t.Fatal(err)
	}

	// Restart: Service seeds library A from sqlite_stat1 + plugin_item_tally.
	// A small delta update on library A must NOT schedule a redundant ANALYZE.
	timers := &fakeTimerHarness{}
	s2, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.stats.afterFunc = timers.afterFunc

	if _, err := s2.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 100, 5)); err != nil {
		t.Fatal(err)
	}
	if timers.active() != nil {
		t.Fatal("healthy restart unexpectedly scheduled ANALYZE for small delta on already-analyzed library")
	}

	// Adding a brand new library B after restart MUST schedule ANALYZE.
	if _, err := s2.UpsertKind(ctx, "community.b", 20, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.KindUpsert(ctx, "community.b", "book", makeStatsTestItems("b", 0, 50)); err != nil {
		t.Fatal(err)
	}
	if timers.active() == nil {
		t.Fatal("new library B after restart did not schedule ANALYZE")
	}
	s2.Close()

	// Stale restart (#621): library B now has items in DB, but sqlite_stat1
	// still only reflects library A (1 library). Restarting must NOT treat
	// the stale statistics as fresh.
	timers3 := &fakeTimerHarness{}
	s3, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	s3.stats.afterFunc = timers3.afterFunc

	if _, err := s3.KindUpsert(ctx, "community.b", "book", makeStatsTestItems("b", 50, 1)); err != nil {
		t.Fatal(err)
	}
	if timers3.active() == nil {
		t.Fatal("restart with stale statistics (new library absent from sqlite_stat1) did not schedule ANALYZE")
	}
}

func TestPlannerStatsRefreshPurgeAndClose(t *testing.T) {
	ctx := context.Background()
	clock := newFakeStatsClock()
	timers := &fakeTimerHarness{}

	s, db := openTestService(t, Options{Now: clock.Now})
	if store.DialectOf(db) != store.SQLite {
		t.Skip("SQLite planner stats test")
	}
	s.stats.afterFunc = timers.afterFunc

	decl := sampleBookDecl()
	decl.Sync = plugins.KindSyncDecl{Mode: plugins.KindSyncPush}
	if _, err := s.UpsertKind(ctx, "community.a", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 0, 20)); err != nil {
		t.Fatal(err)
	}
	clock.Advance(plannerStatsDebounce)
	timers.fireActive(t)

	// Purging the kind forgets its tracked baseline so re-installing and
	// re-populating schedules a first-publish analysis again.
	if err := s.PurgeKind(ctx, "community.a", "book"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "community.a", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 0, 5)); err != nil {
		t.Fatal(err)
	}
	if timers.active() == nil {
		t.Fatal("expected first-publish refresh after PurgeKind + re-populate")
	}

	// Purging and re-populating a library while ANALYZE is in flight must not
	// credit the stale pre-purge snapshot or drop the follow-up refresh.
	inFlightStarted := make(chan struct{})
	inFlightRelease := make(chan struct{})
	s.stats.analyzeFn = func(runCtx context.Context, db *sql.DB, tables ...string) error {
		close(inFlightStarted)
		<-inFlightRelease
		return store.AnalyzeFull(runCtx, db, tables...)
	}
	clock.Advance(plannerStatsDebounce)
	inFlightTimer := timers.active()
	if inFlightTimer == nil {
		t.Fatal("expected timer before in-flight purge test")
	}
	inFlightTimer.stopped.Store(true)
	inFlightDone := make(chan struct{})
	go func() {
		defer close(inFlightDone)
		inFlightTimer.fn()
	}()
	select {
	case <-inFlightStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for in-flight analyzeFn to start")
	}
	if err := s.PurgeKind(ctx, "community.a", "book"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "community.a", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a-reborn", 0, 5)); err != nil {
		t.Fatal(err)
	}
	close(inFlightRelease)
	select {
	case <-inFlightDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for in-flight analyzeFn to finish")
	}
	if timers.active() == nil {
		t.Fatal("expected follow-up refresh after PurgeKind + re-populate during in-flight ANALYZE")
	}

	// Close cancels an in-flight analysis and waits for it to finish.
	started := make(chan struct{})
	canceled := make(chan struct{})
	s.stats.analyzeFn = func(runCtx context.Context, _ *sql.DB, _ ...string) error {
		close(started)
		<-runCtx.Done()
		close(canceled)
		return runCtx.Err()
	}

	clock.Advance(plannerStatsDebounce)
	st := timers.active()
	st.stopped.Store(true)
	go st.fn()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for analyzeFn to start")
	}

	s.Close()

	select {
	case <-canceled:
	default:
		t.Fatal("Close returned before in-flight analyzeFn observed cancellation and exited")
	}
}

func TestPlannerStatsRefreshNonLeaderNoOp(t *testing.T) {
	ctx := context.Background()
	timers := &fakeTimerHarness{}
	s, db := openTestService(t, Options{
		IsLeader: func() bool { return false },
	})
	if store.DialectOf(db) != store.SQLite {
		t.Skip("SQLite planner stats test")
	}
	s.stats.afterFunc = timers.afterFunc

	decl := sampleBookDecl()
	decl.Sync = plugins.KindSyncDecl{Mode: plugins.KindSyncPush}
	if _, err := s.UpsertKind(ctx, "community.a", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KindUpsert(ctx, "community.a", "book", makeStatsTestItems("a", 0, 10)); err != nil {
		t.Fatal(err)
	}
	if timers.count() != 0 {
		t.Fatalf("non-leader scheduled %d timers, want 0", timers.count())
	}
}
