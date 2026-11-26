package catalog

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type layoutSource struct {
	core.MetadataSource
}

func (layoutSource) ID() core.MetadataSourceID { return core.MetadataTVDB }
func (layoutSource) Configured() bool          { return true }
func (layoutSource) Capabilities() core.MetadataCaps {
	return core.MetadataCaps{Shows: true, EpisodeOrders: []string{core.OrderAired, core.OrderDVD}}
}
func (layoutSource) Seasons(_ context.Context, _ core.ProviderRef, order core.EpisodeOrder) ([]core.SeasonSummary, error) {
	n := 2025
	if order.Kind == core.OrderDVD {
		n = 1
	}
	return []core.SeasonSummary{{Season: n, Title: "Season", EpisodeCount: -1}}, nil
}
func (layoutSource) Episodes(_ context.Context, _ core.ProviderRef, order core.EpisodeOrder, season int) ([]core.EpisodeDetail, error) {
	one, two := 2, 1
	if order.Kind == core.OrderDVD {
		one, two = two, one
	}
	return []core.EpisodeDetail{
		{Ref: core.ProviderRef{Source: core.MetadataTVDB, ID: "301", Type: core.TypeEpisode}, IDs: core.IDs{TMDB: 7001, TVDB: 301},
			Season: season, Episode: one, Title: "Matched", AirDate: "2025-01-02"},
		{Ref: core.ProviderRef{Source: core.MetadataTVDB, ID: "302", Type: core.TypeEpisode}, IDs: core.IDs{TVDB: 302},
			Season: season, Episode: two, Title: "Provider only", AirDate: "2025-01-01"},
	}, nil
}

func TestSourceOrderPreservesEpisodeIdentityAcrossOrders(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := metadataDate("2026-01-01")
	c, err := New(db, nil, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	c.MetadataSources().Register(layoutSource{})
	show := core.ShowKey(70)
	legacy := core.EpisodeKey(70, 1, 1)
	for _, title := range []core.Title{
		{Key: show, Type: core.TypeShow, IDs: core.IDs{TMDB: 70, TVDB: 300}, Title: "Show", Released: metadataDate("2024-01-01")},
		{Key: legacy, Type: core.TypeEpisode, IDs: core.IDs{TMDB: 7001}, ShowKey: show, ShowIDs: core.IDs{TMDB: 70, TVDB: 300}, Season: 1, Episode: 1, Title: "Matched", Released: metadataDate("2025-01-02")},
	} {
		if err := c.UpsertDetailedTitle(ctx, title); err != nil {
			t.Fatal(err)
		}
	}
	const user = 4
	if _, err := db.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,view_count) VALUES (?,?,1)`, user, legacy); err != nil {
		t.Fatal(err)
	}
	official := core.EpisodeOrder{Source: core.MetadataTVDB, Kind: core.OrderAired}
	dvd := core.EpisodeOrder{Source: core.MetadataTVDB, Kind: core.OrderDVD}
	c.SetEpisodeOrderDefault(func(context.Context, *core.Title) (EffectiveEpisodeOrder, error) {
		return EffectiveEpisodeOrder{LibraryDefault: official, LibraryID: 2}, nil
	})
	seasons, err := c.Children(ctx, show)
	if err != nil || len(seasons) != 1 || !seasons[0].Key.Allocated() {
		t.Fatalf("year season: %+v %v", seasons, err)
	}
	items, err := c.Children(ctx, seasons[0].Key)
	if err != nil || len(items) != 2 {
		t.Fatalf("episodes: %+v %v", items, err)
	}
	extra := items[0].Key
	if !extra.Allocated() || items[1].Key != legacy || items[1].ParentKey != seasons[0].Key || items[1].Season != 2025 {
		t.Fatalf("unexpected official identities: %+v", items)
	}
	if err := c.SetEpisodeOrder(ctx, show, dvd); err != nil {
		t.Fatal(err)
	}
	layout, err := c.HydrateEpisodeLayout(ctx, show)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := layout.EpisodeAt(1, 1)
	if !ok || a.Key != legacy {
		t.Fatalf("matched identity changed: %+v", a)
	}
	b, ok := layout.EpisodeOn(metadataDate("2025-01-01"))
	if !ok || b.Key != extra || b.Episode != 2 {
		t.Fatalf("allocated identity changed: %+v", b)
	}
	next, err := c.NextStoredEpisode(ctx, legacy)
	if err != nil || next.Key != extra || next.Season != 1 || next.Episode != 2 {
		t.Fatalf("next: %+v %v", next, err)
	}
	tallies, err := c.AiredEpisodeTallies(ctx, user, []core.RatingKey{show}, time.Time{}, nil)
	if err != nil || tallies[show].Seasons[1].Played != 1 {
		t.Fatalf("watch state: %+v %v", tallies, err)
	}
	if err := c.SetEpisodeOrder(ctx, show, core.EpisodeOrder{}); err != nil {
		t.Fatal(err)
	}
	restored, ok, err := c.CachedEpisodeLayout(ctx, show)
	if err != nil || !ok || restored.Order != official {
		t.Fatalf("restored layout: %+v %v", restored, err)
	}
	if ep, found := restored.EpisodeAt(2025, 2); !found || ep.Key != legacy {
		t.Fatalf("original coordinate changed: %+v", ep)
	}
}

type linkedLayoutSource struct{ layoutSource }

func (linkedLayoutSource) Lookup(context.Context, core.ProviderRef) (*core.Title, error) {
	return &core.Title{Type: core.TypeShow, IDs: core.IDs{IMDB: "tt0123000", TVDB: 970}, Title: "Linked", Released: metadataDate("2020-01-01")}, nil
}

func TestAllocatedSourceAliasKeepsCanonicalWatchIdentity(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.MetadataSources().Register(linkedLayoutSource{})
	canonical := core.ShowKey(700)
	if err := c.UpsertDetailedTitle(ctx, core.Title{Key: canonical, Type: core.TypeShow, IDs: core.IDs{TMDB: 700, IMDB: "tt0123000"},
		Title: "Original", Released: metadataDate("2020-01-01")}); err != nil {
		t.Fatal(err)
	}
	hit := core.MetadataHit{Ref: core.ProviderRef{Source: core.MetadataTVDB, ID: "970", Type: core.TypeShow}}
	stub, err := c.SourceSummary(ctx, hit)
	if err != nil || !stub.Key.Allocated() {
		t.Fatalf("initial identity: %v %v", stub.Key, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,view_count) VALUES (1,?,1)`, stub.Key); err != nil {
		t.Fatal(err)
	}
	c.SetSourceIdentityRekey(func(ctx context.Context, tx *sql.Tx, old, next *core.Title, ranges []core.TitleKeyRange) (func(), error) {
		if len(ranges) != 1 || old.Key != stub.Key || next.Key != canonical {
			t.Fatalf("wrong alias migration: %+v", ranges)
		}
		_, err := tx.ExecContext(ctx, `UPDATE state_watch SET rating_key=? WHERE rating_key=?`, next.Key, old.Key)
		return nil, err
	})
	opened, err := c.Get(ctx, stub.Key)
	if err != nil || opened.Key != canonical || opened.IDs.TMDB != 700 || opened.IDs.TVDB != 970 {
		t.Fatalf("opened: %+v %v", opened, err)
	}
	again, err := c.SourceSummary(ctx, hit)
	if err != nil || again.Key != canonical {
		t.Fatalf("summary after linking: %+v %v", again, err)
	}
	var watchKey core.RatingKey
	if err := db.QueryRowContext(ctx, `SELECT rating_key FROM state_watch WHERE user_id=1`).Scan(&watchKey); err != nil || watchKey != canonical {
		t.Fatalf("watch identity: %v %v", watchKey, err)
	}
	alias, err := c.CachedTitle(ctx, stub.Key)
	if err != nil || alias.Key != canonical {
		t.Fatalf("alias stopped resolving: %+v %v", alias, err)
	}
}
