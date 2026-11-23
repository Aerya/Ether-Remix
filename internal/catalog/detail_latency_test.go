package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type heldSeasonProvider struct {
	TMDB
	release chan struct{}
}

func (p *heldSeasonProvider) SeasonDetails(ctx context.Context, _ int64, _ int) (*tmdb.SeasonDetails, error) {
	select {
	case <-p.release:
		return nil, errors.New("offline provider")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Local detail reads must keep a bounded statement ceiling as the number of cold
// seasons grows, even in a realistic catalogue and while providers are stuck.
// They may schedule bounded discovery but must never wait for or write it.
func TestLocalDetailSeasonDiscoveryBudget(t *testing.T) {
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	p := &heldSeasonProvider{release: make(chan struct{})}
	c, err := New(db, p, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(p.release); c.WaitRefreshes() }()
	// Unrelated episodes dominate the mirror, as in the reported Windows log.
	_, err = db.Exec(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<67000)
 INSERT INTO catalog_titles(key,type,show_key,season,episode,title,released_ts)
 SELECT ?+n,'episode',?,1,n,'Unrelated',? FROM seq`, int64(core.EpisodeKey(999, 1, 1)), int64(core.ShowKey(999)), now.Add(-24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	var counts []int64
	for _, seasons := range []int{1, 64} {
		id := int64(seasons)
		show := core.Title{Key: core.ShowKey(id), Type: core.TypeShow, Title: fmt.Sprint("Show ", id), Released: now.AddDate(-1, 0, 0), ChildCount: seasons}
		if err := c.UpsertDetailedTitle(context.Background(), show); err != nil {
			t.Fatal(err)
		}
		var summaries []core.Title
		for n := 1; n <= seasons; n++ {
			summaries = append(summaries, core.Title{Key: core.SeasonKey(id, n), ShowKey: show.Key, Type: core.TypeSeason, Season: n, Title: fmt.Sprint("Season ", n), Released: show.Released, ChildCount: 1})
		}
		if err := c.upsertSeasonRows(context.Background(), summaries, upsertOpts{airDates: true}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(WithReadWaitBudget(context.Background(), 0), 5*time.Second)
		ctx, stats := store.WithQueryStats(ctx)
		items, err := c.Grandchildren(ctx, show.Key)
		cancel()
		if err != nil || len(items) != 0 {
			t.Fatalf("seasons=%d: items=%d err=%v", seasons, len(items), err)
		}
		if stats.Writes() != 0 {
			t.Fatalf("local read wrote %d statements", stats.Writes())
		}
		counts = append(counts, stats.Queries())
	}
	// The guard is that a show's season count does not add statements, not
	// any particular number of them.
	if counts[1] > counts[0]+2 {
		t.Fatalf("statements grow with seasons: 1 season %d, 64 seasons %d", counts[0], counts[1])
	}
}
