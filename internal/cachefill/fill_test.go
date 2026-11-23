package cachefill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/segcache"
)

type catalogFake struct{ items []core.Title }

func (c catalogFake) Browse(_ context.Context, q core.BrowseQuery) (core.Page, error) {
	if q.Offset >= len(c.items) {
		return core.Page{Total: len(c.items)}, nil
	}
	return core.Page{Items: c.items[q.Offset:min(q.Offset+q.Limit, len(c.items))], Total: len(c.items)}, nil
}

func (c catalogFake) CachedTitle(_ context.Context, key core.RatingKey) (*core.Title, error) {
	for _, t := range c.items {
		if t.Key == key {
			return &t, nil
		}
	}
	return nil, core.ErrNotFound
}
func (c catalogFake) Children(context.Context, core.RatingKey) ([]core.Title, error) { return nil, nil }

type resolverFake struct {
	core.Resolver
	calls atomic.Int32
}

func (r *resolverFake) Resolve(_ context.Context, req core.ResolveRequest) (*core.Resolution, error) {
	r.calls.Add(1)
	return &core.Resolution{Release: core.Release{ID: fmt.Sprint(req.Title.Key), Size: 32}, NZB: []byte("payload")}, nil
}

type engineFake struct {
	cache  *segcache.Cache
	fail   bool
	calls  int
	opens  int
	size   int // source size; 0 means 32 bytes
	during func(context.Context)
}

func (e *engineFake) Open(_ context.Context, id string, _ []byte) (core.MediaSource, error) {
	e.opens++
	return &sourceFake{Reader: bytes.NewReader(make([]byte, max(e.size, 32))), id: id}, nil
}
func (e *engineFake) Prefetch(context.Context, string, []byte, int64, int64) error {
	panic("managed fill must use durable prefetch")
}
func (e *engineFake) DurablePrefetch(ctx context.Context, id string, _ []byte, off, n int64) error {
	e.calls++
	if e.during != nil {
		e.during(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.fail {
		return errors.New("disk unavailable")
	}
	return e.cache.Put(id, int(off), make([]byte, n))
}

type sourceFake struct {
	*bytes.Reader
	id string
}

func (s *sourceFake) Name() string      { return "movie.mkv" }
func (s *sourceFake) ReleaseID() string { return s.id }
func (s *sourceFake) Close() error      { return nil }

func harness(t *testing.T) (*Manager, *config.Cache, *engineFake, *resolverFake) {
	t.Helper()
	dir := t.TempDir()
	c, err := segcache.Open(dir, 1<<30, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cfg := &config.Cache{Mode: "adaptive", BudgetBytes: 1 << 30, PreloadEnabled: true, PreloadBudgetBytes: 256 << 20, PreloadMaxTitles: 2}
	cat := catalogFake{items: []core.Title{{Key: core.MovieKey(1), Title: "One", Type: core.TypeMovie, Released: time.Now().Add(-time.Hour)}, {Key: core.MovieKey(2), Title: "Two", Type: core.TypeMovie, Released: time.Now().Add(-time.Hour)}}}
	eng := &engineFake{cache: c}
	res := &resolverFake{}
	m, err := New(dir, Deps{Catalog: cat, Resolver: res, Engine: eng, Cache: c, State: &watchFake{history: map[int64][]core.HistoryEntry{1: {{Key: core.MovieKey(999), ViewedAt: time.Now()}}}}, Household: func(context.Context, string) ([]core.User, error) { return []core.User{{ID: 1}}, nil }, Config: func() config.Cache { return *cfg }, Owner: func(context.Context) (*core.User, error) { return &core.User{ID: 1}, nil }, FreeBytes: func() (int64, error) { return 100 << 30, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return m, cfg, eng, res
}
func TestFillsPersistsAndReusesCompletedShelf(t *testing.T) {
	m, _, eng, res := harness(t)
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.Ready != 2 || s.Allocated > 256<<20 {
		t.Fatalf("unexpected shelf %+v", s)
	}
	if eng.calls != 2 {
		t.Fatal(eng.calls)
	}
	for _, it := range m.Status().Items {
		if len(eng.cache.Pins()[it.ReleaseID]) != 1 {
			t.Fatal("completed source unprotected")
		}
		if it.State != "cached" {
			t.Fatalf("item state = %q", it.State)
		}
	}
	for _, sel := range m.Status().Selection {
		if sel.State != "cached" || len(sel.ReasonCodes) == 0 {
			t.Fatalf("selection = %+v", sel)
		}
	}
	for _, rel := range eng.cache.Releases() {
		if rel.Reason == nil || len(rel.Reason.Causes) == 0 {
			t.Fatalf("missing shelf reason: %+v", rel)
		}
		cause := rel.Reason.Causes[0]
		if cause.Code != core.CacheCauseShelf || cause.Shelf == nil || cause.Shelf.State != "fetched" || rel.Reason.File == "" {
			t.Fatalf("shelf reason = %+v", cause)
		}
	}
	reopened, err := New(eng.cache.Directory(), m.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res.calls.Load() != 2 || reopened.Status().Ready != 2 {
		t.Fatal("completed sources fetched again")
	}
}
func TestDisabledPreloadingNeverResolvesAndReleasesItsPins(t *testing.T) {
	m, cfg, eng, res := harness(t)
	cfg.PreloadEnabled = false
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res.calls.Load() != 0 {
		t.Fatal("disabled job resolved")
	}
	cfg.PreloadEnabled = true
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg.PreloadEnabled = false
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(eng.cache.Pins()) != 0 || m.Status().Ready != 0 {
		t.Fatal("disabled shelf kept reservations")
	}
	if used, _ := eng.cache.Usage(); used == 0 {
		t.Fatal("disabling must return bytes to retention, not delete them")
	}
}
func TestIdleAndDiskGuardsAvoidProviderWork(t *testing.T) {
	for _, mode := range []string{"live", "disk", "paused"} {
		t.Run(mode, func(t *testing.T) {
			m, _, eng, res := harness(t)
			switch mode {
			case "live":
				m.deps.Active = func() bool { return true }
			case "disk":
				m.deps.FreeBytes = func() (int64, error) { return 1 << 30, nil }
			case "paused":
				m.Pause(true)
			}
			if err := m.pass(context.Background()); err == nil {
				t.Fatal("guard ignored")
			}
			if eng.calls != 0 || res.calls.Load() != 0 {
				t.Fatal("guard allowed provider work")
			}
		})
	}
}
func TestFailedDurableWritesNeverBecomeReady(t *testing.T) {
	m, _, eng, _ := harness(t)
	eng.fail = true
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Status()
	if s.Ready != 0 || s.Failed != 2 {
		t.Fatalf("false completion: %+v", s)
	}
	if len(s.Items) != 2 {
		t.Fatalf("failed titles missing from manifest: %+v", s.Items)
	}
	for _, it := range s.Items {
		if it.State != "failed" {
			t.Fatalf("item state = %q", it.State)
		}
	}
	for _, sel := range s.Selection {
		if sel.State != "failed" {
			t.Fatalf("selection state = %q", sel.State)
		}
	}
}
func TestAllocationAndTitleLimits(t *testing.T) {
	m, cfg, eng, _ := harness(t)
	cfg.PreloadBudgetBytes = 16 << 20
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if eng.calls != 0 || m.Status().Allocated != 0 {
		t.Fatal("oversized source admitted")
	}
	cfg.PreloadBudgetBytes = 128 << 20
	cfg.PreloadMaxTitles = 1
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Status().Ready != 1 {
		t.Fatal(m.Status())
	}
	cfg.PreloadBudgetBytes = 16 << 20
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Status().Allocated != 0 {
		t.Fatal("lower budget left shelf over allocated")
	}
}

// Failed titles that never got a source reserve nothing, so they must not
// use up preload title slots, and they leave the manifest once no longer selected.
func TestFailedTitlesWithoutSourceDoNotFillTitleCap(t *testing.T) {
	m, cfg, _, res := harness(t)
	cfg.PreloadMaxTitles = 2
	released := time.Now().Add(-time.Hour)
	var titles []core.Title
	for i := int64(1); i <= 4; i++ {
		titles = append(titles, core.Title{Key: core.MovieKey(i), Title: fmt.Sprint("Movie ", i), Type: core.TypeMovie, Released: released})
	}
	m.deps.Catalog = catalogFake{items: titles}
	now := time.Now()
	raw, _ := json.Marshal(*cfg)
	m.mu.Lock()
	m.settings = string(raw) // unchanged settings keep the failure backoff
	m.failed = map[core.RatingKey]failure{}
	for _, k := range []core.RatingKey{core.MovieKey(1), core.MovieKey(2), core.MovieKey(3), core.MovieKey(50)} {
		m.items[k] = Item{Key: k, Title: fmt.Sprint(k), State: "failed", Updated: now}
		m.failed[k] = failure{count: 1, at: now} // still backing off
	}
	m.mu.Unlock()
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Status()
	if s.Ready != 1 || res.calls.Load() != 1 {
		t.Fatalf("fillable title blocked by failed titles: ready %d, resolves %d, items %+v", s.Ready, res.calls.Load(), s.Items)
	}
	states := map[core.RatingKey]string{}
	for _, it := range s.Items {
		states[it.Key] = it.State
	}
	if states[core.MovieKey(4)] != "cached" || states[core.MovieKey(1)] != "failed" {
		t.Fatalf("items = %+v", s.Items)
	}
	if _, ok := states[core.MovieKey(50)]; ok {
		t.Fatal("failed title that is no longer selected stayed in the manifest")
	}
}

// A title ranked below a full shelf cannot be admitted, so preload must not
// search indexers or open a source for it. Higher-ranked titles still displace.
func TestFullShelfSkipsLowerRankedTitlesBeforeResolving(t *testing.T) {
	m, cfg, eng, res := harness(t)
	cfg.PreloadBudgetBytes = 48 << 20 // one 32 MiB-overhead reservation fits
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.Ready != 1 || s.Items[0].Key != core.MovieKey(1) {
		t.Fatalf("shelf = %+v", s.Items)
	}
	if res.calls.Load() != 1 || eng.opens != 1 {
		t.Fatalf("lower-ranked title resolved %d times, opened %d times for a full shelf", res.calls.Load(), eng.opens)
	}
	m.deps.Catalog = catalogFake{items: []core.Title{{Key: core.MovieKey(3), Title: "Three", Type: core.TypeMovie, Released: time.Now().Add(-time.Hour)}, {Key: core.MovieKey(1), Title: "One", Type: core.TypeMovie, Released: time.Now().Add(-time.Hour)}}}
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(); s.Ready != 1 || s.Items[0].Key != core.MovieKey(3) {
		t.Fatalf("higher-ranked title did not displace: %+v", s.Items)
	}
}

// Once a title's source proved too large for the room below higher-ranked
// titles, later passes skip it without searching until that room grows.
func TestTooLargeTitleIsNotResolvedAgainWhileRoomIsShort(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sizeMiB   int
		budgetMiB int64
		ready     int
	}{
		{"higher-ranked reservation", 8, 75, 1},
		{"overhead exceeds entire allocation", 8, 39, 0},
		{"source exceeds entire allocation", 40, 36, 0},
		{"watchlist can fit a prefix", 40, 65, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, cfg, eng, res := harness(t)
			eng.size = tc.sizeMiB << 20
			cfg.PreloadBudgetBytes = tc.budgetMiB << 20
			clock := time.Now()
			m.deps.Now = func() time.Time { return clock }
			if err := m.pass(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s := m.Status(); s.Ready != tc.ready || res.calls.Load() != 2 {
				t.Fatalf("first pass: ready %d, resolves %d", s.Ready, res.calls.Load())
			}
			clock = clock.Add(3 * time.Hour) // past the failure backoff
			if err := m.pass(context.Background()); err != nil {
				t.Fatal(err)
			}
			if res.calls.Load() != 2 || eng.opens != 2 {
				t.Fatalf("title known not to fit resolved again: resolves %d, opens %d", res.calls.Load(), eng.opens)
			}
			if tc.name == "watchlist can fit a prefix" {
				clock = clock.Add(3 * time.Hour)
				m.deps.WatchlistIntents = func(context.Context) ([]WarmIntent, error) {
					return []WarmIntent{{UserID: 1, User: core.User{ID: 1}, Key: core.MovieKey(1), Added: clock}}, nil
				}
				if err := m.pass(context.Background()); err != nil {
					t.Fatal(err)
				}
				if s := m.Status(); s.Ready != 1 || res.calls.Load() != 3 {
					t.Fatalf("full-source reservation blocked watchlist prefix: ready %d, resolves %d", s.Ready, res.calls.Load())
				}
			}
			cfg.PreloadBudgetBytes = 256 << 20 // room grows: try again
			if err := m.pass(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s := m.Status(); s.Ready != 2 {
				t.Fatalf("title not retried after room grew: %+v", s.Items)
			}
		})
	}
}

func TestFutureTitlesNeverResolve(t *testing.T) {
	m, _, _, res := harness(t)
	m.deps.Catalog = catalogFake{items: []core.Title{{Key: core.MovieKey(3), Type: core.TypeMovie, Released: time.Now().Add(time.Hour)}}}
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res.calls.Load() != 0 {
		t.Fatal("future title was preloaded")
	}
}

func TestShelfRotatesToNewHigherRankedTitles(t *testing.T) {
	m, cfg, eng, _ := harness(t)
	cfg.PreloadMaxTitles = 1
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := m.Status().Items[0]
	m.deps.Catalog = catalogFake{items: []core.Title{{Key: core.MovieKey(3), Title: "New favourite", Type: core.TypeMovie, Released: time.Now().Add(-time.Hour)}}}
	if err := m.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.Status()
	if s.Ready != 1 || s.Items[0].Key != core.MovieKey(3) {
		t.Fatal(s)
	}
	if len(eng.cache.Pins()[old.ReleaseID]) > 0 {
		t.Fatal("old preload pin remained")
	}
	if eng.cache.ReleaseBytes(old.ReleaseID) == 0 {
		t.Fatal("rotation should not delete cached bytes")
	}
}
func TestPauseSurvivesRestart(t *testing.T) {
	m, _, eng, _ := harness(t)
	if err := m.Pause(true); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(eng.cache.Directory(), m.deps)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Status().Paused {
		t.Fatal("pause lost at restart")
	}
}

func TestCancelledRunStopsProviderWork(t *testing.T) {
	m, _, eng, _ := harness(t)
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	eng.during = func(providerCtx context.Context) {
		close(entered)
		<-providerCtx.Done()
	}
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("preload worker still running during cleanup")
		}
	})
	// Reservation and manifest writes happen before provider work starts.
	// Measure cancellation only after the provider has entered, not from
	// worker launch (which also measures filesystem startup under race CI).
	select {
	case <-entered:
	case <-done:
		t.Fatal("preload worker stopped before provider work")
	case <-time.After(10 * time.Second):
		t.Fatal("preload provider work did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("preload worker did not stop after provider cancellation")
	}
	if eng.calls != 1 {
		t.Fatal("cancelled fill kept fetching", eng.calls)
	}
}
