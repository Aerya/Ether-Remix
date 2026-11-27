package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestFeedMatchesAreLocalUnambiguousAndRespectIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("feed matching fetched remote metadata")
		http.Error(w, "unexpected", 500)
	}))
	defer srv.Close()
	c := newTestCatalog(t, srv)
	ctx := t.Context()
	film := core.Title{Key: core.MovieKey(9), Type: core.TypeMovie, Title: "Premiere", Year: 2020, IDs: core.IDs{TMDB: 9, IMDB: "tt0000009"}, Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if e := c.UpsertTitle(ctx, film, true); e != nil {
		t.Fatal(e)
	}
	releases := []core.Release{{ID: "good", Name: "Premiere.2020.1080p.WEB-DL"}, {ID: "conflict", Name: "Premiere.2020.1080p.WEB-DL", TitleIDs: core.IDs{TMDB: 8}}, {ID: "noyear", Name: "Premiere.1080p.WEB-DL"}, {ID: "id", Name: "Localized.2020.1080p.WEB-DL", TitleIDs: core.IDs{TMDB: 9}}}
	for i := range releases {
		releases[i].Info = releaseparse.Parse(releases[i].Name)
	}
	got, e := c.ReleaseMatches(ctx, releases)
	if e != nil {
		t.Fatal(e)
	}
	if len(got["good"]) != 1 || len(got["id"]) != 1 || len(got["conflict"]) != 0 || len(got["noyear"]) != 0 {
		t.Fatalf("bad matches: %+v", got)
	}
	// Same title/year with no IDs must become ambiguous, never guess.
	film.Key = core.MovieKey(10)
	film.IDs = core.IDs{TMDB: 10}
	if e = c.UpsertTitle(ctx, film, true); e != nil {
		t.Fatal(e)
	}
	got, e = c.ReleaseMatches(ctx, releases)
	if e != nil || len(got["good"]) != 0 || len(got["id"]) != 1 {
		t.Fatal(got, e)
	}
}

// The same episode must keep its playback identity through an order change,
// including when feed discovery describes it by its effective broadcast date.
func TestFeedMatchesFollowEffectiveEpisodeIdentity(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, slog.New(slog.DiscardHandler), catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	show := core.Title{Key: core.ShowKey(5), Type: core.TypeShow, Title: "Evening Ledger", Year: 2020,
		IDs: core.IDs{TMDB: 5, TVDB: 55}, Released: now.AddDate(-6, 0, 0)}
	episode := core.Title{Key: core.EpisodeKey(5, 1, 7), Type: core.TypeEpisode, Title: "Episode", ShowKey: show.Key,
		Season: 1, Episode: 7, Released: now.AddDate(0, 0, -2)}
	season, ok := core.AllocatedKey(core.KindSeason, 1)
	if !ok {
		t.Fatal("could not allocate year season")
	}
	for _, row := range []core.Title{show, episode} {
		if err := cat.UpsertTitle(ctx, row, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_episode_order(show_key,provider,order_kind) VALUES(?,'tvdb','aired')`, []any{show.Key}},
		{`INSERT INTO catalog_order_manifest(show_key,provider,order_kind,fetched_at) VALUES(?,'tvdb','aired',?)`, []any{show.Key, now.Unix()}},
		{`INSERT INTO catalog_order_seasons(show_key,provider,order_kind,season,season_key,title,summary,poster,air_date,episode_count,fetched_at)
			VALUES(?,'tvdb','aired',2026,?,'2026','','','2026-03-09',1,?)`, []any{show.Key, season, now.Unix()}},
		{`INSERT INTO catalog_order_episodes(show_key,provider,order_kind,season,episode,episode_key,absolute,air_date)
			VALUES(?,'tvdb','aired',2026,123,?,123,'2026-03-09')`, []any{show.Key, episode.Key}},
	} {
		if _, err := db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := cat.ReleaseMatches(ctx, []core.Release{
		{ID: "number", Info: releaseparse.Parse("Evening.Ledger.S2026E123.1080p.WEB-DL")},
		{ID: "broadcast", Info: releaseparse.Parse("Evening.Ledger.2026.03.09.1080p.WEB-DL")},
		{ID: "old-number", Info: releaseparse.Parse("Evening.Ledger.S01E07.1080p.WEB-DL")},
		{ID: "old-date", Info: releaseparse.Parse("Evening.Ledger.2026.03.08.1080p.WEB-DL")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"number", "broadcast"} {
		found := matches[id]
		if len(found) != 1 || found[0].Key != episode.Key || found[0].ParentKey != season ||
			found[0].Season != 2026 || found[0].Episode != 123 || found[0].EpisodeOrder.Source != core.MetadataTVDB {
			t.Fatalf("effective episode for %s: %+v", id, found)
		}
	}
	if len(matches["old-number"]) != 0 || len(matches["old-date"]) != 0 {
		t.Fatalf("old coordinates matched: %+v", matches)
	}
}
