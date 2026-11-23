package collections_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestMDBListMatchingDigitalGateAndImportMembership(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	recentRelease := now.AddDate(0, 0, -14).Format("2006-01-02")

	var sawDefaultLimit string
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lists/alice/next-up/json":
			sawDefaultLimit = r.URL.Query().Get("limit")
			// 1) Internal/non-TMDB id=9999991 with imdb_id -> movie 101
			// 2) Internal/TVDB id=8888881 with tvdbid + mediatype="series" -> show 501 (must not collide with movie 501)
			// 3) Episode IMDb id tt7654321 -> parent show 502 via tv_episode_results
			// 4) Recent <120d movie 130 already in local DB as summary without home_release_checked -> hydrated via release_dates
			fmt.Fprint(w, `[
				{"id":9999991,"imdb_id":"tt0000101","mediatype":"movie","title":"Space Saga","release_year":1977},
				{"id":8888881,"tvdbid":81189,"mediatype":"series","title":"Chemistry Teacher","year":2008},
				{"id":7777771,"imdb":"tt7654321","mediatype":"episode","title":"Next Up Episode","year":2005},
				{"tmdbid":130,"mediatype":"movie","title":"Digital Premiere","release_year":2026}
			]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ext.Close()

	tmdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/find/81189":
			// TVDB ID 81189 collides across a movie and a TV show; show-typed lookup must pick TVResults (501).
			fmt.Fprint(w, `{"movie_results":[{"id":999,"title":"Wrong Movie","release_date":"1999-01-01"}],"tv_results":[{"id":501,"name":"Chemistry Teacher","first_air_date":"2008-01-20"}]}`)
		case "/find/tt7654321":
			fmt.Fprint(w, `{"movie_results":[],"tv_results":[],"tv_episode_results":[{"id":62085,"name":"Pilot","show_id":502}]}`)
		case "/movie/130":
			fmt.Fprintf(w, `{"id":130,"title":"Digital Premiere","release_date":%q,"runtime":110,"vote_average":7.8,"vote_count":500,"popularity":80,"external_ids":{"imdb_id":"tt0000130"},"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"type":4,"release_date":%q,"certification":"PG-13"}]}]}}`,
				recentRelease+"", recentRelease+"T00:00:00.000Z")
		default:
			http.NotFound(w, r)
		}
	}))
	defer tmdbSrv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := tmdb.New("test-key", log, tmdb.WithBaseURL(tmdbSrv.URL), tmdb.WithRateLimit(100000, 100000), tmdb.WithMaxRetries(0))
	defer client.Close()

	cfg := config.Catalog{Language: "en-US", Region: "US", ReleaseMode: config.ModeDigital}
	cat, err := catalog.New(db, client, cfg, log, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// Seed local titles:
	// - Movie 101 has IMDB tt0000101
	// - Show 501 & Show 502 are in local catalog
	// - Movie 130 is only a summary row (<120d old, HomeReleaseChecked=false), which ModeDigital hides until hydrated!
	if err := cat.UpsertDetailedTitle(ctx, core.Title{
		Key: core.MovieKey(101), Type: core.TypeMovie, IDs: core.IDs{TMDB: 101, IMDB: "tt0000101"},
		Title: "Space Saga", Year: 1977, Released: time.Date(1977, 5, 25, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpsertDetailedTitle(ctx, core.Title{
		Key: core.ShowKey(501), Type: core.TypeShow, IDs: core.IDs{TMDB: 501},
		Title: "Chemistry Teacher", Year: 2008, Released: time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpsertDetailedTitle(ctx, core.Title{
		Key: core.ShowKey(502), Type: core.TypeShow, IDs: core.IDs{TMDB: 502},
		Title: "Office Life", Year: 2005, Released: time.Date(2005, 3, 24, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	relTime, _ := time.Parse("2006-01-02", recentRelease)
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: core.MovieKey(130), Type: core.TypeMovie, IDs: core.IDs{TMDB: 130},
		Title: "Digital Premiere", Year: 2026, Released: relTime,
	}, true); err != nil {
		t.Fatal(err)
	}

	svc, err := collections.New(
		db,
		cat,
		log,
		collections.WithIndexTTL(0),
		collections.WithLiveTTL(0),
		collections.WithTMDB(client),
		collections.WithCatalogConfig(func() config.Catalog { return cfg }),
		collections.WithExternalBases(collections.ExternalBases{MDBList: ext.URL}),
		collections.WithUserAgent("Ether/test"),
	)
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.CreateSpec(ctx, core.CollectionSpec{
		Name: "Next Up & Picks", MediaType: "mixed", Sort: "source", Refresh: "daily", Enabled: true,
		Sources: []core.CollectionSource{{Kind: "mdblist", URL: "https://mdblist.com/lists/alice/next-up"}},
		Targets: core.CollectionTargets{CollectionsTab: true, HomeRow: true, Users: core.AllUsers()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created, err = svc.Rebuild(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if sawDefaultLimit != "1000" {
		t.Fatalf("expected bounded default MDBList page size limit=1000, got %q", sawDefaultLimit)
	}
	if created.ItemCount != 4 {
		t.Fatalf("expected 4 matched items, got %d (warnings=%v)", created.ItemCount, created.Warnings)
	}

	// Verify Plex collection Items() actually serves all 4 items (including the <120d ModeDigital movie).
	numID, err := strconv.ParseInt(strings.TrimPrefix(created.ID, "col_"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	itemsPage, err := svc.Items(ctx, nil, core.SmartKey(numID), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var gotTitles []string
	for _, it := range itemsPage.Items {
		gotTitles = append(gotTitles, it.Title)
	}
	if strings.Join(gotTitles, ",") != "Space Saga,Chemistry Teacher,Office Life,Digital Premiere" {
		t.Fatalf("Plex collection items = %v, want all 4 items", gotTitles)
	}

	// Verify ImportTitlesTx writes collections_members so collection-backed libraries are non-empty in Plex.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	importedID, err := collections.ImportTitlesTx(ctx, tx, 0, 1, "Imported MDBList", []core.RatingKey{core.MovieKey(101), core.MovieKey(130)}, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var memberCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections_members WHERE spec_id=?`, importedID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if memberCount != 2 {
		t.Fatalf("expected 2 persisted members in collections_members for imported collection, got %d", memberCount)
	}
	importedPage, err := svc.Items(ctx, &core.User{ID: 1}, core.SmartKey(importedID), 0, 10)
	if err != nil || importedPage.Total != 2 {
		t.Fatalf("expected 2 items in imported collection via svc.Items, got %+v %v", importedPage, err)
	}
}
