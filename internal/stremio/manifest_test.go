package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://addon.example/manifest.json":                           "https://addon.example",
		"  stremio://addon.example/manifest.json ":                      "https://addon.example",
		"https://addon.example/providers=a,b%7Csort=size/manifest.json": "https://addon.example/providers=a,b%7Csort=size",
		"https://addon.example/abc/configure":                           "https://addon.example/abc",
		"https://ADDON.example/x/manifest.json?key=secret":              "https://addon.example/x?key=secret",
		// User info and the fragment are dropped; the query is configuration
		// some add-ons keep there, so it stays.
		"https://user:pw@addon.example/x/?q=1#frag": "https://addon.example/x?q=1",
	} {
		u, err := NormalizeURL(raw)
		if err != nil || u.String() != want {
			t.Errorf("NormalizeURL(%q) = %v, %v; want %s", raw, u, err, want)
		}
	}
	for _, raw := range []string{"", "ftp://addon.example", "addon.example/manifest.json", "https://"} {
		if _, err := NormalizeURL(raw); err == nil {
			t.Errorf("NormalizeURL(%q) accepted", raw)
		}
	}
}

func TestResourceURLKeepsEscapingAndQuery(t *testing.T) {
	base, err := NormalizeURL("stremio://addon.example/providers=a,b%7Csort=size/manifest.json?key=k")
	if err != nil {
		t.Fatal(err)
	}
	if got := ManifestURL(base); got != "https://addon.example/providers=a,b%7Csort=size/manifest.json?key=k" {
		t.Fatalf("manifest URL %q", got)
	}
	if got := ResourceURL(base, "/stream/series/tt1:1:2.json"); got != "https://addon.example/providers=a,b%7Csort=size/stream/series/tt1:1:2.json?key=k" {
		t.Fatalf("resource URL %q", got)
	}
	if Origin(base) != "https://addon.example" {
		t.Fatalf("origin %q", Origin(base))
	}
	other, _ := NormalizeURL("https://addon.example/providers=a,b%7Csort=size?key=k2")
	same, _ := NormalizeURL("https://addon.example/providers=a,b%7Csort=size/manifest.json?key=k")
	if Fingerprint(base) != Fingerprint(same) || Fingerprint(base) == Fingerprint(other) {
		t.Fatal("fingerprint must follow the whole normalised link, query included")
	}
}

func TestManifestCheckStreams(t *testing.T) {
	for body, want := range map[string]string{
		manifestJSON: "",
		`{"id":"x","resources":["stream"],"types":["movie"],"idPrefixes":["tt"]}`:                            "",
		`{"id":"x","resources":["catalog","meta"],"types":["movie"]}`:                                        "does not provide streams",
		`{"id":"x","resources":[{"name":"stream","types":["tv"]}]}`:                                          "movie or series",
		`{"id":"x","resources":[{"name":"stream","types":["movie"],"idPrefixes":["kitsu"]}]}`:                "",
		`{"id":"x","resources":[{"name":"stream","types":["movie"],"idPrefixes":["tmdb:"]}]}`:                "",
		`{"id":"x","resources":[{"name":"stream","types":["movie"],"idPrefixes":["yt_id:"]}]}`:               "IMDb",
		`{"id":"x","resources":["stream"],"types":["movie"],"behaviorHints":{"configurationRequired":true}}`: "configured first",
	} {
		m, err := DecodeManifest([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		err = m.CheckStreams()
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("manifest %s: err %v, want %q", body, err, want)
		}
	}
	m, _ := DecodeManifest([]byte(`{"id":"x","resources":["stream"],"types":["movie"],"behaviorHints":{"configurationRequired":true}}`))
	if !errors.Is(m.CheckStreams(), ErrConfigurationRequired) {
		t.Fatal("configurationRequired is not ErrConfigurationRequired")
	}
}

func TestDecodeManifestRejectsNonManifests(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{}`, `{"id":"x"}`, `[]`} {
		if _, err := DecodeManifest([]byte(body)); err == nil {
			t.Errorf("DecodeManifest(%q) accepted", body)
		}
	}
}

// An add-on that refuses servers gets a clear, actionable message; no HTTP
// error names a URL, which carries the owner's configuration.
func TestHTTPErrorsNameNoURLAndRateLimitsAreRecognised(t *testing.T) {
	err := error(&HTTPError{Status: http.StatusForbidden})
	if !strings.Contains(err.Error(), "refused Ether") {
		t.Fatalf("forbidden message %q", err)
	}
	for status, limited := range map[int]bool{
		http.StatusTooManyRequests: true, http.StatusInternalServerError: false, http.StatusNotFound: false, http.StatusServiceUnavailable: false,
	} {
		he := &HTTPError{Status: status}
		if he.RateLimited() != limited || strings.Contains(he.Error(), "://") {
			t.Errorf("status %d: RateLimited=%v, want %v (%q)", status, he.RateLimited(), limited, he.Error())
		}
	}
	if !(&HTTPError{Status: http.StatusServiceUnavailable, RetryAfter: "30"}).RateLimited() {
		t.Fatal("503 with Retry-After is a rate limit")
	}
}

func TestRouteEscapesTypeIDAndExtras(t *testing.T) {
	for _, tc := range []struct {
		resource, typ, id string
		extra             map[string]string
		want              string
	}{
		{"stream", "series", "tt0903747:1:2", nil, "/stream/series/tt0903747:1:2.json"},
		{"stream", "movie", "yt id/x?#", nil, "/stream/movie/yt%20id%2Fx%3F%23.json"},
		{"catalog", "tv", "live", map[string]string{"skip": "25", "genre": "News & Views", "empty": ""}, "/catalog/tv/live/genre=News+%26+Views&skip=25.json"},
		{"subtitles", "movie", "tt1", map[string]string{"videoSize": "12345", "videoHash": "abc123"}, "/subtitles/movie/tt1/videoHash=abc123&videoSize=12345.json"},
	} {
		if got := Route(tc.resource, tc.typ, tc.id, tc.extra); got != tc.want {
			t.Errorf("Route(%q,%q,%q,%v) = %q, want %q", tc.resource, tc.typ, tc.id, tc.extra, got, tc.want)
		}
	}
}

// routes answers an Addon's Fetcher from a map of route path to body (or
// error) and records what was asked.
type routes struct {
	answers map[string]string
	errs    map[string]error
	asked   []string
	limits  []int64
}

func (r *routes) Fetch(_ context.Context, route string, limit int64) ([]byte, error) {
	r.asked = append(r.asked, route)
	r.limits = append(r.limits, limit)
	if err := r.errs[route]; err != nil {
		return nil, err
	}
	if body, ok := r.answers[route]; ok {
		return []byte(body), nil
	}
	return nil, &HTTPError{Status: http.StatusNotFound}
}

func TestAddonCatalogPaginatesShortPages(t *testing.T) {
	f := &routes{answers: map[string]string{
		"/catalog/movie/test.json":              `{"metas":[{"id":"tt0000001"}]}`,
		"/catalog/movie/test/skip=1.json":       `{"metas":[{"id":"tt0000002"}]}`,
		"/catalog/movie/test/skip=2.json":       `{"metas":[]}`,
		"/catalog/movie/test/skip=1&bogus.json": `x`,
	}}
	cat := Catalog{Type: "movie", ID: "test", Extra: []Extra{{Name: "skip"}}}
	items, err := Addon{Fetcher: f}.Catalog(t.Context(), cat, nil, 10)
	if err != nil || len(items) != 2 || items[0].ID != "tt0000001" || items[1].ID != "tt0000002" {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if len(f.asked) != 3 {
		t.Fatalf("asked %v", f.asked)
	}
}

func TestAddonCatalogWithoutSkipReadsOnePage(t *testing.T) {
	f := &routes{answers: map[string]string{"/catalog/movie/top.json": `{"metas":[{"id":"a"},{"id":"b"}]}`}}
	items, err := Addon{Fetcher: f}.Catalog(t.Context(), Catalog{Type: "movie", ID: "top"}, nil, 100)
	if err != nil || len(items) != 2 || len(f.asked) != 1 {
		t.Fatalf("items=%v err=%v asked=%v", items, err, f.asked)
	}
}

// fixtureFetcher models real shapes (a Cinemeta-like catalog, a tv add-on,
// a subtitle add-on), keys removed.
func fixtureFetcher(t *testing.T) *routes {
	t.Helper()
	f := &routes{answers: map[string]string{
		"/manifest.json": `{"id":"org.example.all","name":"Example","version":"1.0.0","types":["movie","series","tv"],
"idPrefixes":["tt","tmdb:"],"resources":["catalog","stream","meta","subtitles"],
"catalogs":[{"type":"movie","id":"top","name":"Top","extra":[{"name":"skip"}]},
{"type":"tv","id":"live","name":"Live","extra":[{"name":"skip"},{"name":"genre","options":["News"]}]}]}`,
		"/catalog/movie/top.json": `{"metas":[{"id":"tt0111161","type":"movie","name":"The Shawshank Redemption"},
{"id":"tmdb:550","type":"movie","name":"Fight Club"},{"id":"kitsu:99","type":"movie","name":"Unknown"}]}`,
		"/stream/tv/ch-1.json": `{"streams":[{"name":"HD","url":"https://cdn.example/ch1.m3u8"},{"name":"SD","url":"https://cdn.example/ch1-sd.m3u8"}]}`,
		"/meta/tv/ch-1.json": `{"meta":{"id":"ch-1","type":"tv","name":"Channel 1","videos":[
{"id":"v1","title":"Morning News","released":"2026-11-01T08:00:00Z","overview":"News"},
{"id":"v2","title":"Midday","released":"2026-11-01T09:00:00Z"}]}}`,
		"/subtitles/movie/tt0111161/videoHash=abc123&videoSize=12345.json": `{"subtitles":[{"id":"1","url":"https://subs.example/en.srt","lang":"eng"},{"id":"2","url":"https://subs.example/fr.srt","lang":"fre"}]}`,
	}}
	// A paged tv catalog: 25 per page via the skip extra, 30 channels.
	page := func(skip int) string {
		var b strings.Builder
		b.WriteString(`{"metas":[`)
		for i := skip; i < skip+25 && i < 30; i++ {
			if i > skip {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"id":"ch-%d","type":"tv","name":"Channel %d","logo":"https://img.example/%d.png","genres":["News"]}`, i, i, i)
		}
		b.WriteString(`]}`)
		return b.String()
	}
	f.answers["/catalog/tv/live.json"] = page(0)
	f.answers["/catalog/tv/live/skip=25.json"] = page(25)
	f.answers["/catalog/tv/live/skip=30.json"] = `{"metas":[]}`
	return f
}

func TestAddonCatalogTVMetaStreamsAndSubtitles(t *testing.T) {
	f := fixtureFetcher(t)
	body, _ := f.Fetch(t.Context(), "/manifest.json", MaxManifestBytes)
	m, err := DecodeManifest(body)
	if err != nil || !m.HasResource("catalog") || !m.Serves("catalog", "tv") || !m.Serves("subtitles", "movie") {
		t.Fatalf("manifest = %+v err=%v", m, err)
	}
	if got := m.CatalogsOfType("tv"); len(got) != 1 || !got[0].SupportsSkip() {
		t.Fatalf("tv catalogs = %+v", got)
	}
	a := Addon{Manifest: m, Fetcher: f}
	ctx := t.Context()
	top, _ := m.CatalogNamed("movie", "top")
	metas, err := a.Catalog(ctx, top, nil, 100)
	if err != nil || len(metas) != 3 {
		t.Fatalf("catalog = %v err=%v", metas, err)
	}
	if imdb, tmdb, tvdb := ParseCatalogRef(metas[0].ID); imdb != "tt0111161" || tmdb != 0 || tvdb != 0 {
		t.Fatalf("ref0 = %q %d %d", imdb, tmdb, tvdb)
	}
	if imdb, tmdb, _ := metas[1].Ref(); imdb != "" || tmdb != 550 {
		t.Fatalf("ref1 = %q %d", imdb, tmdb)
	}
	if imdb, tmdb, tvdb := metas[2].Ref(); imdb != "" || tmdb != 0 || tvdb != 0 {
		t.Fatalf("ref2 should be unmatched, got %q %d %d", imdb, tmdb, tvdb)
	}
	live, _ := m.CatalogNamed("tv", "live")
	tv, err := a.Catalog(ctx, live, nil, 2000)
	if err != nil || len(tv) != 30 {
		t.Fatalf("tv catalog = %d err=%v", len(tv), err)
	}
	streams, err := a.StreamsFor(ctx, "tv", "ch-1")
	if err != nil || len(streams) != 2 || streams[0].URL == "" {
		t.Fatalf("streams = %+v err=%v", streams, err)
	}
	meta, err := a.Meta(ctx, "tv", "ch-1")
	if err != nil || len(meta.Videos) != 2 || meta.Videos[0].Released.IsZero() {
		t.Fatalf("meta = %+v err=%v", meta, err)
	}
	subs, err := a.Subtitles(ctx, "movie", "tt0111161", map[string]string{"videoHash": "abc123", "videoSize": "12345"})
	if err != nil || len(subs) != 2 || subs[0].Lang != "eng" {
		t.Fatalf("subs = %+v err=%v", subs, err)
	}
}

func TestAddonCatalogMaxItemsClamp(t *testing.T) {
	f := fixtureFetcher(t)
	live := Catalog{Type: "tv", ID: "live", Extra: []Extra{{Name: "skip"}}}
	metas, err := Addon{Fetcher: f}.Catalog(t.Context(), live, nil, 10)
	if err != nil || len(metas) != 10 {
		t.Fatalf("metas = %d err=%v", len(metas), err)
	}
	// A non-positive or oversized request reads up to the library's bound.
	metas, err = Addon{Fetcher: f}.Catalog(t.Context(), live, nil, 1<<30)
	if err != nil || len(metas) != 30 {
		t.Fatalf("metas = %d err=%v", len(metas), err)
	}
}

// An add-on that ignores skip answers the same page again: reading stops.
func TestAddonCatalogStopsOnRepeatedPage(t *testing.T) {
	f := &routes{answers: map[string]string{}}
	same := `{"metas":[{"id":"a"},{"id":"b"}]}`
	f.answers["/catalog/movie/c.json"] = same
	f.answers["/catalog/movie/c/skip=2.json"] = same
	items, err := Addon{Fetcher: f}.Catalog(t.Context(), Catalog{Type: "movie", ID: "c", Extra: []Extra{{Name: "skip"}}}, nil, 100)
	if err != nil || len(items) != 2 || len(f.asked) != 2 {
		t.Fatalf("items=%v err=%v asked=%v", items, err, f.asked)
	}
}

// Fetcher errors reach the caller untouched and invalid JSON names nothing
// about the route.
func TestAddonErrorsDoNotExposeConfiguredPath(t *testing.T) {
	f := &routes{
		errs:    map[string]error{"/stream/movie/tt1.json": &HTTPError{Status: http.StatusInternalServerError}},
		answers: map[string]string{"/stream/movie/tt2.json": `<html>/SECRET/</html>`},
	}
	a := Addon{Fetcher: f}
	_, err := a.StreamsFor(t.Context(), "movie", "tt1")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 500 || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err %v", err)
	}
	if _, err = a.StreamsFor(t.Context(), "movie", "tt2"); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("invalid JSON err %v", err)
	}
	if _, err = (Addon{}).StreamsFor(t.Context(), "movie", "tt3"); err == nil {
		t.Fatal("an add-on without a fetcher answered")
	}
}

func TestCatalogValidateExtras(t *testing.T) {
	c := Catalog{Type: "tv", ID: "live", Extra: []Extra{{Name: "skip"}, {Name: "genre", Options: []string{"News"}, IsRequired: true}}}
	if err := c.ValidateExtras(map[string]string{"genre": "News", "skip": "10"}); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []map[string]string{nil, {"genre": "Sport"}, {"genre": "News", "bogus": "1"}} {
		if err := c.ValidateExtras(extra); err == nil {
			t.Errorf("%v accepted", extra)
		}
	}
}

func TestLiveEventCatalogsDetection(t *testing.T) {
	raw := `{
		"id": "community.sports.example",
		"version": "1.0.0",
		"name": "Sports Streams",
		"types": ["sport"],
		"resources": [
			{"name": "catalog", "types": ["sport", "other"]},
			{"name": "meta", "types": ["sport"], "idPrefixes": ["streamed", "sf"]},
			{"name": "stream", "types": ["sport"], "idPrefixes": ["streamed", "sf"]}
		],
		"catalogs": [
			{"type": "sport", "id": "sports_live", "name": "Live Now", "extra": [{"name": "search", "isRequired": false}, {"name": "skip", "isRequired": false}]},
			{"type": "sport", "id": "sports_football", "name": "Football"},
			{"type": "sport", "id": "sports_football", "name": "Football Duplicate"},
			{"type": "sport", "id": "sports_search", "name": "Search Only", "extra": [{"name": "search", "isRequired": true}]},
			{"type": "other", "id": "unstreamable", "name": "No Stream Type"},
			{"type": "movie", "id": "top", "name": "Movies"},
			{"type": "tv", "id": "channels", "name": "Classic TV"}
		]
	}`
	var m Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	cats := m.LiveEventCatalogs()
	if len(cats) != 2 || cats[0].ID != "sports_live" || cats[1].ID != "sports_football" {
		t.Fatalf("LiveEventCatalogs = %+v", cats)
	}
	if IsCustomLiveType("movie") || IsCustomLiveType("Series") || IsCustomLiveType("TV") || IsCustomLiveType("") {
		t.Fatal("standard or empty type treated as custom live type")
	}
	if !IsCustomLiveType("sport") || !IsCustomLiveType("events") || !IsCustomLiveType("channel") {
		t.Fatal("custom live type rejected")
	}
}

func TestCatalogLegacyGenresAndIDMappingAndPageBound(t *testing.T) {
	// Legacy manifests declare options in catalog.genres alongside extraSupported/extraRequired.
	cat := Catalog{
		Type:          TypeMovie,
		ID:            "legacy",
		ExtraRequired: []string{"genre"},
		Genres:        []string{"Action", "Sci-Fi"},
	}
	ex, ok := cat.ExtraNamed("genre")
	if !ok || !ex.IsRequired || len(ex.Options) != 2 || ex.Options[0] != "Action" {
		t.Fatalf("legacy genres extra = %+v, ok=%v", ex, ok)
	}
	if err := cat.ValidateExtras(map[string]string{"genre": "Action"}); err != nil {
		t.Fatalf("valid legacy genre rejected: %v", err)
	}
	if err := cat.ValidateExtras(map[string]string{"genre": "Horror"}); err == nil {
		t.Fatal("undeclared legacy genre accepted")
	}

	// ID mapping covers IMDb, TMDB, TVDB prefixes (colon and hyphen) plus explicit fields.
	for _, tc := range []struct {
		meta     MetaPreview
		wantIMDb string
		wantTMDB int64
		wantTVDB int64
	}{
		{MetaPreview{ID: "tt0133093"}, "tt0133093", 0, 0},
		{MetaPreview{ID: "imdb:tt0903747:1:1"}, "tt0903747", 0, 0},
		{MetaPreview{ID: "tmdb:550"}, "", 550, 0},
		{MetaPreview{ID: "tmdb:movie:603"}, "", 603, 0},
		{MetaPreview{ID: "tmdb-102"}, "", 102, 0},
		{MetaPreview{ID: "tvdb:81189"}, "", 0, 81189},
		{MetaPreview{ID: "tvdb-81189"}, "", 0, 81189},
		{MetaPreview{ID: "custom:1", IMDbIDAlt: "tt0111161", TMDBIDAlt: "278", TVDBIDAlt: 999}, "tt0111161", 278, 999},
	} {
		imdb, tmdb, tvdb := tc.meta.Ref()
		if imdb != tc.wantIMDb || tmdb != tc.wantTMDB || tvdb != tc.wantTVDB {
			t.Errorf("%+v -> (%q, %d, %d), want (%q, %d, %d)", tc.meta, imdb, tmdb, tvdb, tc.wantIMDb, tc.wantTMDB, tc.wantTVDB)
		}
	}

	// Bounded paging stops at MaxCatalogPages even when an add-on drips 1 item per page.
	calls := 0
	drip := funcFetcher(func(_ context.Context, route string, _ int64) ([]byte, error) {
		calls++
		return []byte(fmt.Sprintf(`{"metas":[{"id":"tt%07d"}]}`, calls)), nil
	})
	items, err := Addon{Fetcher: drip}.Catalog(t.Context(), Catalog{Type: TypeMovie, ID: "drip", Extra: []Extra{{Name: "skip"}}}, nil, MaxCatalogItems)
	if err != nil {
		t.Fatalf("drip catalog err = %v", err)
	}
	if calls != MaxCatalogPages || len(items) != MaxCatalogPages {
		t.Fatalf("drip paging fetched %d pages / %d items, want %d", calls, len(items), MaxCatalogPages)
	}
}

type funcFetcher func(context.Context, string, int64) ([]byte, error)

func (f funcFetcher) Fetch(ctx context.Context, route string, limit int64) ([]byte, error) {
	return f(ctx, route, limit)
}
