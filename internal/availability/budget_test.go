package availability

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// busyIndexers serves feeds that post `rate` releases a minute (several pages
// between polls) and answers title searches, counting every request that
// passed admission, as a real indexer client does.
type busyIndexers struct {
	sources  []core.ReleaseFeed
	start    time.Time
	now      *time.Time
	rate     map[string]int
	hits     map[string]int            // requests per source
	heads    map[string]int            // first-page feed requests per source
	pairs    map[string]int            // searches per title and source
	reached  []int                     // sources each search call reached
	releases map[core.RatingKey]string // title -> the only source holding it
}

func (b *busyIndexers) FeedSources() []core.ReleaseFeed { return b.sources }

func (b *busyIndexers) Recent(ctx context.Context, id string, offset, limit int) ([]core.Release, error) {
	if err := core.SpendBackground(ctx, "hit", id, 1); err != nil {
		return nil, err
	}
	b.hits[id]++
	if offset == 0 {
		b.heads[id]++
	}
	posted := int(b.now.Sub(b.start)/time.Minute) * b.rate[id]
	var out []core.Release
	for i := posted - 1 - offset; i >= 0 && len(out) < limit; i-- {
		out = append(out, core.Release{ID: fmt.Sprintf("%s-%d", id, i), Name: "Unrelated.Upload", Indexer: id,
			PubDate: b.start.Add(time.Duration(i) * time.Minute / time.Duration(b.rate[id]))})
	}
	return out, nil
}

func (b *busyIndexers) search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	key := core.MovieKey(req.IDs.TMDB)
	reached := 0
	var out []core.Release
	for _, f := range b.sources {
		if core.IndexerSkipped(ctx, f.ID) {
			continue
		}
		reached++
		if err := core.SpendBackground(ctx, "hit", f.ID, 1); err != nil {
			return nil, err
		}
		b.hits[f.ID]++
		b.pairs[fmt.Sprintf("%d/%s", key, f.ID)]++
		if b.releases[key] == f.ID {
			out = append(out, core.Release{ID: fmt.Sprintf("%d", key), Name: "Film.2026.1080p.WEB-DL", Indexer: f.Name})
		}
	}
	b.reached = append(b.reached, reached)
	return out, nil
}

// A day of background traffic for a large window of waiting titles stays
// inside every indexer's own allowance, keeps every feed's 30-minute head
// polls, and searches titles one source at a time: never a fan-out to all
// indexers, never past the first hit, never the same title twice on a
// source (no timed rechecks).
func TestBackgroundTrafficStaysWithinEachIndexerAllowance(t *testing.T) {
	x := setup(t)
	x.c.Availability.Verify = false
	x.c.Availability.CatchUp = false
	x.now = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	b := &busyIndexers{start: x.now.Add(-time.Hour), now: &x.now,
		sources: []core.ReleaseFeed{
			{ID: "geek", Name: "Geek", Generation: "g", Supported: true, PageMax: 100},
			{ID: "slug", Name: "Slug", Generation: "g", Supported: true, PageMax: 100},
			{ID: "site", Name: "Site", Generation: "g", Supported: true, SinglePage: true},
			{ID: "addon", Name: "Addon", Generation: "g"},
		},
		rate: map[string]int{"geek": 8, "slug": 3, "site": 1},
		hits: map[string]int{}, heads: map[string]int{}, pairs: map[string]int{}, releases: map[core.RatingKey]string{}}
	titles := map[core.RatingKey]core.Title{}
	for i := 1; i <= 200; i++ {
		k := core.MovieKey(int64(i))
		titles[k] = core.Title{Key: k, Type: core.TypeMovie, Title: fmt.Sprintf("Film %d", i), Year: 2026, IDs: core.IDs{TMDB: int64(i)},
			Released: x.now.Add(-time.Duration(i%50+1) * 24 * time.Hour), Popularity: float64(i)}
		switch i % 10 {
		case 0:
			b.releases[k] = "geek"
		case 5:
			b.releases[k] = "addon"
		}
	}
	x.cat.titles = titles
	x.s.d.Catalog = probeCatalog{fakeCatalog: x.cat}
	x.s.d.Feeds = b
	x.s.d.Search = b.search
	x.cycleFor(t, 24*time.Hour)

	allowance := x.s.budgets().IndexerAllowance(0)
	for _, f := range b.sources {
		if b.hits[f.ID] > allowance {
			t.Errorf("%s: %d background requests in a day, allowance %d", f.ID, b.hits[f.ID], allowance)
		}
		if f.Supported && b.heads[f.ID] < 47 {
			t.Errorf("%s: %d head polls in a day, want one every 30 minutes", f.ID, b.heads[f.ID])
		}
	}
	if b.hits["geek"] < allowance*9/10 {
		t.Errorf("geek used %d of %d: a busy feed and 200 first looks should use the allowance", b.hits["geek"], allowance)
	}
	for i, n := range b.reached {
		if n != 1 {
			t.Fatalf("search %d reached %d sources, want one at a time", i, n)
		}
	}
	for pair, n := range b.pairs {
		if n > 1 {
			t.Errorf("%s searched %d times in a day", pair, n)
		}
	}
	for k, src := range b.releases {
		// geek may have been out of allowance; once it answered, nothing
		// after it was asked.
		if src == "geek" && b.pairs[fmt.Sprintf("%d/geek", k)] > 0 && b.pairs[fmt.Sprintf("%d/slug", k)] > 0 {
			t.Errorf("title %d found on geek was still searched on slug", k)
		}
	}
}

func TestAdmitPluginUsenetCheckDailyAllowanceAndReset(t *testing.T) {
	ctx := context.Background()
	x := setup(t)
	x.c.Availability.Verify = false // must not require Ether's own title verification
	x.c.Availability.PluginUsenetChecks = 2
	x.c.Availability.ReservePercent = 80
	x.now = time.Date(2026, 11, 22, 18, 30, 0, 0, time.UTC)
	wantReset := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	// Plugin A can spend up to 2 background checks; returned context carries
	// background priority and foreground reserve.
	bgCtx1, refund1, reset1, err := x.s.AdmitPluginUsenetCheck(ctx, "plug.a")
	if err != nil {
		t.Fatalf("first admit: %v", err)
	}
	if !reset1.Equal(wantReset) {
		t.Fatalf("resetAt = %v, want %v", reset1, wantReset)
	}
	if !core.IsBackgroundCheck(bgCtx1) || core.ForegroundReserve(bgCtx1) != 80 {
		t.Fatalf("bgCtx missing background/reserve markers: bg=%v reserve=%d", core.IsBackgroundCheck(bgCtx1), core.ForegroundReserve(bgCtx1))
	}

	_, _, _, err = x.s.AdmitPluginUsenetCheck(ctx, "plug.a")
	if err != nil {
		t.Fatalf("second admit: %v", err)
	}

	// Third check for plugin A is refused with ErrBudgetExhausted and next UTC midnight.
	_, _, reset3, err := x.s.AdmitPluginUsenetCheck(ctx, "plug.a")
	if !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("third admit err = %v, want core.ErrBudgetExhausted", err)
	}
	if !reset3.Equal(wantReset) {
		t.Fatalf("exhausted resetAt = %v, want %v", reset3, wantReset)
	}

	// Plugin B has an independent per-plugin allowance in availability_budget.
	if _, _, _, err := x.s.AdmitPluginUsenetCheck(ctx, "plug.b"); err != nil {
		t.Fatalf("plugin B admit failed while plugin A exhausted: %v", err)
	}
	if got := x.s.used(ctx, PluginUsenetCheckBucket("plug.a")); got != 2 {
		t.Fatalf("plug.a used = %d, want 2", got)
	}
	if got := x.s.used(ctx, PluginUsenetCheckBucket("plug.b")); got != 1 {
		t.Fatalf("plug.b used = %d, want 1", got)
	}

	// Refund is idempotent and returns exactly 1 slot.
	refund1()
	refund1()
	if got := x.s.used(ctx, PluginUsenetCheckBucket("plug.a")); got != 1 {
		t.Fatalf("plug.a used after idempotent refund = %d, want 1", got)
	}
	if _, _, _, err := x.s.AdmitPluginUsenetCheck(ctx, "plug.a"); err != nil {
		t.Fatalf("re-admit after refund: %v", err)
	}

	// Next UTC day resets the allowance.
	x.now = wantReset.Add(time.Second)
	if _, _, resetNext, err := x.s.AdmitPluginUsenetCheck(ctx, "plug.a"); err != nil {
		t.Fatalf("admit after UTC midnight rollover: %v", err)
	} else if !resetNext.Equal(wantReset.Add(24 * time.Hour)) {
		t.Fatalf("next-day resetAt = %v, want %v", resetNext, wantReset.Add(24*time.Hour))
	}

	// Setting allowance to 0 disables background checks immediately.
	x.c.Availability.PluginUsenetChecks = 0
	if _, _, _, err := x.s.AdmitPluginUsenetCheck(ctx, "plug.c"); !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("zero allowance err = %v, want core.ErrBudgetExhausted", err)
	}
}
