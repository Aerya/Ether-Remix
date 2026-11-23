package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestLocalizedNamesAcrossMetadataPaths(t *testing.T) {
	ctx := context.Background()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		want := "no-NO"
		if calls > 6 {
			want = "en-US"
		}
		if got := r.URL.Query().Get("language"); got != want {
			t.Errorf("language = %q, want %q", got, want)
		}
		appendices := strings.Split(r.URL.Query().Get("append_to_response"), ",")
		if !slices.Contains(appendices, "translations") || !slices.Contains(appendices, "alternative_titles") {
			t.Errorf("missing appended titles: %s", r.URL)
		}
		// Region-specific aliases come after enough unrelated aliases to fill
		// the storage cap. Primary translations are not alternative_titles.
		var aliases []string
		for i := range 30 {
			aliases = append(aliases, fmt.Sprintf(`{"iso_3166_1":"US","title":"Working Name %d"}`, i))
		}
		aliases = append(aliases, `{"iso_3166_1":"NO","title":"Superfamilien"}`)
		fmt.Fprintf(w, `{"id":508,"title":"The Incredibles","original_title":"The Incredibles","name":"The Incredibles","original_name":"The Incredibles","release_date":"2004-10-27","alternative_titles":{"titles":[%s]},"translations":{"translations":[{"iso_639_1":"fr","iso_3166_1":"FR","data":{"title":"Les Indestructibles","name":"Les Indestructibles"}},{"iso_639_1":"no","iso_3166_1":"NO","data":{"title":"De utrolige","name":"De utrolige"}},{"iso_639_1":"nb","iso_3166_1":"NO","data":{"title":"","name":""}}]}}`, strings.Join(aliases, ","))
	}))
	defer srv.Close()
	cfg := config.Catalog{TMDBKey: "test", Language: "nb-NO", Region: "NO"}
	client := tmdb.NewFromConfig(cfg, slog.Default(), tmdb.WithBaseURL(srv.URL), tmdb.WithCacheTTL(0))
	defer client.Close()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(db, client, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	check := func(title core.Title) {
		t.Helper()
		if title.Title != "De utrolige" || title.SortTitle != "de utrolige" || title.OriginalTitle != "The Incredibles" {
			t.Fatalf("localized identity: %+v", title)
		}
		if !slices.Contains(title.AlternativeTitles, "Superfamilien") || len(title.AlternativeTitles) > maxAltTitles || slices.Contains(title.AlternativeTitles, "Les Indestructibles") {
			t.Fatalf("aliases: %v", title.AlternativeTitles)
		}
		if err := c.upsert(ctx, title, upsertOpts{detail: true}); err != nil {
			t.Fatal(err)
		}
		stored, err := c.loadByKey(ctx, title.Key)
		if err != nil {
			t.Fatal(err)
		}
		if stored.t.Title != title.Title || !slices.Contains(stored.t.AlternativeTitles, "Superfamilien") {
			t.Fatalf("stored names: %+v", stored.t)
		}
		// The resolver passes these hydrated fields to TitleMatches.
		if !releaseparse.TitleMatches(releaseparse.Parse("De.Utrolige.2004.NORWEGIAN.DVDRip"), stored.t.Title) ||
			!slices.ContainsFunc(stored.t.AlternativeTitles, func(name string) bool {
				return releaseparse.TitleMatches(releaseparse.Parse("Superfamilien.2004.NORWEGIAN.DVDRip"), name)
			}) {
			t.Fatal("localized release title did not match")
		}
	}
	for _, fetch := range []func(context.Context, int64) (*tmdb.MovieDetails, error){client.MovieSummary, client.MovieDetails, client.MovieDetailsFresh} {
		d, err := fetch(ctx, 508)
		if err != nil {
			t.Fatal(err)
		}
		check(c.movieSummaryToTitle(ctx, d))
		check(c.movieDetailsToTitle(ctx, d))
	}
	for _, fetch := range []func(context.Context, int64) (*tmdb.TVDetails, error){client.TVSummary, client.TVDetails, client.TVDetailsFresh} {
		d, err := fetch(ctx, 508)
		if err != nil {
			t.Fatal(err)
		}
		check(c.tvSummaryToTitle(ctx, d))
		check(c.tvDetailsToTitle(ctx, d))
	}
	if calls != 6 {
		t.Fatalf("requests = %d, want one per existing metadata fetch", calls)
	}
	cfg.Language, cfg.Region = "en-US", "US"
	client.UpdateConfig(cfg)
	c.UpdateConfig(cfg)
	d, err := client.MovieDetails(ctx, 508)
	if err != nil {
		t.Fatal(err)
	}
	if title := c.movieDetailsToTitle(ctx, d); title.Title != "The Incredibles" || slices.Contains(title.AlternativeTitles, "De utrolige") {
		t.Fatalf("English metadata changed: %+v", title)
	}
}

func TestEnglishTitleForNonEnglishCatalog(t *testing.T) {
	ctx := context.Background()
	var foreignAliases []string
	for i := range 30 {
		foreignAliases = append(foreignAliases, fmt.Sprintf(`{"iso_3166_1":"DE","title":"Chihiros Reise %d"}`, i))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/genre/movie/list", "/genre/tv/list":
			fmt.Fprint(w, `{"genres":[{"id":16,"name":"Animation"}]}`)
		case "/movie/129":
			fmt.Fprintf(w, `{"id":129,"title":"Chihiro og heksene","original_title":"千と千尋の神隠し","original_language":"ja","release_date":"2001-07-20","runtime":125,"status":"Released","alternative_titles":{"titles":[%s]},"translations":{"translations":[{"iso_639_1":"no","iso_3166_1":"NO","data":{"title":"Chihiro og heksene"}},{"iso_639_1":"en","iso_3166_1":"GB","data":{"title":"Spirited Away UK"}},{"iso_639_1":"en","iso_3166_1":"US","data":{"title":"Spirited Away"}}]}}`, strings.Join(foreignAliases, ","))
		case "/movie/8392":
			fmt.Fprint(w, `{"id":8392,"title":"Min nabo Totoro","original_title":"となりのトトロ","original_language":"ja","release_date":"1988-04-16","runtime":86,"status":"Released","alternative_titles":{"titles":[{"iso_3166_1":"JP","title":"Tonari no Totoro"},{"iso_3166_1":"US","title":"My Neighbor Totoro"}]},"translations":{"translations":[{"iso_639_1":"no","iso_3166_1":"NO","data":{"title":"Min nabo Totoro"}},{"iso_639_1":"en","iso_3166_1":"US","data":{"title":""}}]}}`)
		case "/tv/9001":
			fmt.Fprint(w, `{"id":9001,"name":"Drømmenes rike","original_name":"夢の国","original_language":"ja","first_air_date":"2021-04-01","status":"Returning Series","episode_run_time":[24],"number_of_seasons":1,"genres":[{"id":16,"name":"Animation"}],"seasons":[{"id":1,"name":"Sesong 1","season_number":1,"episode_count":1,"air_date":"2021-04-01"}],"alternative_titles":{"results":[{"iso_3166_1":"JP","title":"Yume no Kuni"}]},"translations":{"translations":[{"iso_639_1":"no","iso_3166_1":"NO","data":{"name":"Drømmenes rike"}},{"iso_639_1":"en","iso_3166_1":"US","data":{"name":"Dream Kingdom"}}]}}`)
		case "/tv/9001/season/1":
			fmt.Fprint(w, `{"id":1,"name":"Sesong 1","season_number":1,"air_date":"2021-04-01","episodes":[{"id":11,"name":"Episode 1","episode_number":1,"season_number":1,"air_date":"2021-04-01","runtime":24}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"status_message":"not found"}`)
		}
	}))
	defer srv.Close()

	cfg := config.Catalog{TMDBKey: "test", Language: "nb-NO", Region: "NO"}
	client := tmdb.NewFromConfig(cfg, slog.Default(), tmdb.WithBaseURL(srv.URL), tmdb.WithCacheTTL(0))
	defer client.Close()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(db, client, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.WaitRefreshes()

	// Pre-seed a pre-migration row with empty english_title and a hand-inserted
	// catalog_alt_titles row that refresh will overwrite.
	spiritedKey := core.MovieKey(129)
	if err := c.upsert(ctx, core.Title{
		Key: spiritedKey, Type: core.TypeMovie, IDs: core.IDs{TMDB: 129},
		Title: "Chihiro og heksene", OriginalTitle: "千と千尋の神隠し",
		Year: 2001, Released: time.Date(2001, 7, 20, 0, 0, 0, 0, time.UTC),
		AlternativeTitles: []string{"Manual Spirited Away"},
	}, upsertOpts{detail: false}); err != nil {
		t.Fatal(err)
	}

	// Lazy backfill on detail refresh populates english_title even as writeFacets
	// replaces catalog_alt_titles, and subsequent discover upserts preserve it.
	d129, err := client.MovieDetailsFresh(ctx, 129)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.upsert(ctx, c.movieDetailsToTitle(ctx, d129), upsertOpts{detail: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.upsert(ctx, core.Title{
		Key: spiritedKey, Type: core.TypeMovie, IDs: core.IDs{TMDB: 129},
		Title: "Chihiro og heksene", OriginalTitle: "千と千尋の神隠し",
		Year: 2001, Released: time.Date(2001, 7, 20, 0, 0, 0, 0, time.UTC),
	}, upsertOpts{detail: false}); err != nil {
		t.Fatal(err)
	}
	spirited, err := c.Get(ctx, spiritedKey)
	if err != nil {
		t.Fatal(err)
	}
	if spirited.Title != "Chihiro og heksene" || spirited.OriginalTitle != "千と千尋の神隠し" || spirited.EnglishTitle != "Spirited Away" {
		t.Fatalf("Spirited Away identity = %+v", spirited)
	}
	if slices.Contains(spirited.AlternativeTitles, "Manual Spirited Away") || slices.Contains(spirited.AlternativeTitles, "Spirited Away") {
		t.Fatalf("unexpected alternative titles after refresh: %v", spirited.AlternativeTitles)
	}

	// Fallback to US alternative_titles when translations[] en-US title is empty.
	totoro, err := c.Get(ctx, core.MovieKey(8392))
	if err != nil {
		t.Fatal(err)
	}
	if totoro.Title != "Min nabo Totoro" || totoro.OriginalTitle != "となりのトトロ" || totoro.EnglishTitle != "My Neighbor Totoro" {
		t.Fatalf("Totoro identity = %+v", totoro)
	}

	// Cold show fill (writeLocalPage) stores EnglishTitle, and episode hydration inherits it.
	coldCtx := core.WithColdDetail(core.WithLocalResponse(WithReadWaitBudget(ctx, 0)), core.ShowKey(9001))
	show, err := c.Get(coldCtx, core.ShowKey(9001))
	if err != nil {
		t.Fatal(err)
	}
	if show.Title != "Drømmenes rike" || show.OriginalTitle != "夢の国" || show.EnglishTitle != "Dream Kingdom" {
		t.Fatalf("cold show identity = %+v", show)
	}
	ep, err := c.Get(ctx, core.EpisodeKey(9001, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if ep.ShowTitle != "Drømmenes rike" || ep.ShowOriginalTitle != "夢の国" || ep.ShowEnglishTitle != "Dream Kingdom" {
		t.Fatalf("episode show titles = %q / %q / %q", ep.ShowTitle, ep.ShowOriginalTitle, ep.ShowEnglishTitle)
	}

	// Namesake and episode-identity lookups still detect collisions via EnglishTitle.
	if err := c.UpsertTitle(ctx, core.Title{
		Key: core.MovieKey(999129), Type: core.TypeMovie, IDs: core.IDs{TMDB: 999129},
		Title: "Spirited Away", OriginalTitle: "Spirited Away", Year: 2001,
		Released: time.Date(2001, 5, 1, 0, 0, 0, 0, time.UTC),
	}, true); err != nil {
		t.Fatal(err)
	}
	if hasNamesake, err := c.Namesakes(ctx, spiritedKey); err != nil || !hasNamesake {
		t.Fatalf("Namesakes(Spirited Away) = %v, %v; want true", hasNamesake, err)
	}

	if err := c.UpsertTitle(ctx, core.Title{
		Key: core.ShowKey(9002), Type: core.TypeShow, IDs: core.IDs{TMDB: 9002},
		Title: "Dream Kingdom", OriginalTitle: "Dream Kingdom", Year: 2015, Language: "en",
		Released: time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC),
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertTitle(ctx, core.Title{
		Key: core.EpisodeKey(9002, 1, 1), Type: core.TypeEpisode, ShowKey: core.ShowKey(9002),
		Season: 1, Episode: 1, Title: "Pilot", Year: 2015,
		Released: time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC),
	}, true); err != nil {
		t.Fatal(err)
	}
	if found, err := c.SeriesNamesake(ctx, core.ShowKey(9001), "Dream Kingdom", 2021); err != nil || !found {
		t.Fatalf("SeriesNamesake(Dream Kingdom) = %v, %v; want true", found, err)
	}
	id, err := c.EpisodeIdentity(ctx, core.ShowKey(9001), 1, 1, []string{"Drømmenes rike", "夢の国", "Dream Kingdom"})
	if err != nil {
		t.Fatal(err)
	}
	if len(id.OtherShows["Dream Kingdom"]) != 1 || id.OtherShows["Dream Kingdom"][0].Year != 2015 {
		t.Fatalf("EpisodeIdentity OtherShows = %+v, want 2015 Dream Kingdom namesake", id.OtherShows)
	}

	// English catalogue leaves EnglishTitle empty because Title is already en-US.
	cfg.Language, cfg.Region = "en-US", "US"
	client.UpdateConfig(cfg)
	c.UpdateConfig(cfg)
	dEn, err := client.MovieDetailsFresh(ctx, 129)
	if err != nil {
		t.Fatal(err)
	}
	if enTitle := c.movieDetailsToTitle(ctx, dEn); enTitle.EnglishTitle != "" {
		t.Fatalf("English catalogue set EnglishTitle = %q, want empty", enTitle.EnglishTitle)
	}
}
