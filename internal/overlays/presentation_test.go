package overlays

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/ratings"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPresentationUsesRealCatalogueWithoutProvider(t *testing.T) {
	ctx := context.Background()
	db, e := store.OpenMemory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	s := New(db, Options{Now: func() time.Time { return now }, Facts: func(context.Context, *core.User, *core.Title) Facts { return Facts{Returning: true} }})
	title := &core.Title{Key: core.ShowKey(1), Type: core.TypeShow, Year: 2020, Runtime: 45 * time.Minute, ContentRating: "TV-14", Rating: 8.3, VoteCount: 300, Released: now.Add(-365 * 24 * time.Hour)}
	ctx = context.WithValue(ctx, previewSettingsKey{}, Settings{Enabled: true, Status: true, Quality: true, Ratings: true, Details: true})
	report, e := s.Inspect(ctx, nil, title)
	if e != nil {
		t.Fatal(e)
	}
	labels := strings.Join(report.Labels, "|")
	if labels != "TMDB 8.3/10|2020 · 45m · TV-14" {
		t.Fatalf("unexpected labels: %q", labels)
	}
	if report.Layers[0].State != "unavailable" || report.Layers[1].State != "unavailable" {
		t.Fatalf("missing facts not explained: %+v", report)
	}
	for _, v := range []float64{0, -1, 11, math.NaN(), math.Inf(1)} {
		title.Rating = v
		if strings.Contains(strings.Join(s.Labels(ctx, nil, title), "|"), "TMDB") {
			t.Fatal("invalid score rendered")
		}
	}
	title.Rating = 8.3
	title.VoteCount = 0
	if strings.Contains(strings.Join(s.Labels(ctx, nil, title), "|"), "TMDB") {
		t.Fatal("unrated estimate rendered")
	}
	title.Released = now.Add(-24 * time.Hour)
	if !strings.Contains(strings.Join(s.Labels(ctx, nil, title), "|"), "NEW RELEASE") {
		t.Fatal("recent release omitted")
	}
	title.Released = now.Add(24 * time.Hour)
	if strings.Contains(strings.Join(s.Labels(ctx, nil, title), "|"), "NEW RELEASE") {
		t.Fatal("future release rendered")
	}
}

func TestCombinedRatingNormalisesScalesExplainsSourcesAndUpdatesCacheKey(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	img := image.NewRGBA(image.Rect(0, 0, 500, 750))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{18, 28, 38, 255}), image.Point{}, draw.Src)
	var srcPoster bytes.Buffer
	if err := jpeg.Encode(&srcPoster, img, nil); err != nil {
		t.Fatal(err)
	}

	r := ratings.New(db, ratings.Options{Now: func() time.Time { return now }})
	if err := r.SaveSettings(ctx, ratings.Settings{
		Enabled:    true,
		Provider:   "omdb",
		APIKey:     "test-key",
		Critic:     "tomatoes",
		Audience:   "imdb",
		CacheHours: 168,
	}); err != nil {
		t.Fatal(err)
	}

	s := New(db, Options{
		Now:     func() time.Time { return now },
		Ratings: r,
		Images:  &fakeImages{data: srcPoster.Bytes()},
	})

	// Critic slot ("tomatoes") is absent from cache so it falls back to TMDB 7.0/10;
	// Audience slot ("imdb") resolves to IMDb 8.0/10. Combined average is 7.5.
	movie := &core.Title{
		Key:        core.MovieKey(101),
		Type:       core.TypeMovie,
		PosterPath: "/movie.jpg",
		Rating:     7.0,
		VoteCount:  250,
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO ratings_cache(item_key,provider,value,updated) VALUES(?,?,?,?)`,
		movie.Key, "omdb", `[{"source":"imdb","value":8.0,"scale":10}]`, now.Unix(),
	); err != nil {
		t.Fatal(err)
	}

	if err := s.SaveSettings(ctx, Settings{Enabled: true, Ratings: true, RatingStyle: "sources"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Labels(ctx, nil, movie); !reflect.DeepEqual(got, []string{"TMDB 7.0/10", "IMDB 8.0/10"}) {
		t.Fatalf("sources labels = %v, want [TMDB 7.0/10 IMDB 8.0/10]", got)
	}
	vSources := s.Version(ctx, nil, movie, "en")
	posterSources, _, err := s.Poster(ctx, nil, movie, "en")
	if err != nil {
		t.Fatal(err)
	}
	rendersBefore := s.CacheStats().Renders

	// Switch to combined: HideDenominator=false must still produce bare "7.5" with no source label or denominator.
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Ratings: true, RatingStyle: "combined", HideDenominator: false}); err != nil {
		t.Fatal(err)
	}
	report, err := s.Inspect(ctx, nil, movie)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Labels, []string{"7.5"}) {
		t.Fatalf("combined labels = %v, want [7.5]", report.Labels)
	}
	var ratingsLayer *LayerReport
	for i := range report.Layers {
		if report.Layers[i].Key == "ratings" {
			ratingsLayer = &report.Layers[i]
			break
		}
	}
	if ratingsLayer == nil || ratingsLayer.State != "ready" || !reflect.DeepEqual(ratingsLayer.Labels, []string{"7.5"}) {
		t.Fatalf("unexpected ratings layer: %+v", ratingsLayer)
	}
	if !strings.Contains(ratingsLayer.Reason, "7.5") || !strings.Contains(ratingsLayer.Reason, "TMDB") || !strings.Contains(ratingsLayer.Reason, "IMDB") {
		t.Fatalf("Inspect reason did not report combined value and sources: %q", ratingsLayer.Reason)
	}

	vCombined := s.Version(ctx, nil, movie, "en")
	if vCombined == vSources {
		t.Fatal("combined rating style did not change poster Version")
	}
	posterCombined, _, err := s.Poster(ctx, nil, movie, "en")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(posterCombined, posterSources) {
		t.Fatal("combined rating style reused source-labelled poster bytes")
	}
	if s.CacheStats().Renders <= rendersBefore {
		t.Fatal("combined rating style did not miss generated-poster cache")
	}

	// A 3.5/5 source normalises to 7.0 on the 0–10 scale.
	fivePoint := &core.Title{
		Key:        core.MovieKey(102),
		Type:       core.TypeMovie,
		PosterPath: "/five.jpg",
		VoteCount:  0,
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE ratings_settings SET value=? WHERE id=1`,
		`{"enabled":true,"provider":"mdblist","apiKey":"test-key","critic":"letterboxd","audience":"none","missing":"none","cacheHours":168}`,
	); err != nil {
		t.Fatal(err)
	}
	sFive := New(db, Options{
		Now:     func() time.Time { return now },
		Ratings: ratings.New(db, ratings.Options{Now: func() time.Time { return now }}),
	})
	if _, err := db.ExecContext(ctx,
		`INSERT INTO ratings_cache(item_key,provider,value,updated) VALUES(?,?,?,?)`,
		fivePoint.Key, "mdblist", `[{"source":"letterboxd","value":3.5,"scale":5}]`, now.Unix(),
	); err != nil {
		t.Fatal(err)
	}
	reportFive, err := sFive.Inspect(ctx, nil, fivePoint)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reportFive.Labels, []string{"7.0"}) {
		t.Fatalf("3.5/5 normalised labels = %v, want [7.0]", reportFive.Labels)
	}
	for _, l := range reportFive.Layers {
		if l.Key == "ratings" && (!strings.Contains(l.Reason, "7.0") || !strings.Contains(l.Reason, "LETTERBOXD")) {
			t.Fatalf("3.5/5 Inspect reason missing value or source: %q", l.Reason)
		}
	}

	// With no valid sources, combined mode draws no badge.
	unrated := &core.Title{Key: core.MovieKey(103), Type: core.TypeMovie, PosterPath: "/unrated.jpg", Rating: 8.5, VoteCount: 0}
	reportEmpty, err := sFive.Inspect(ctx, nil, unrated)
	if err != nil {
		t.Fatal(err)
	}
	if len(reportEmpty.Labels) != 0 {
		t.Fatalf("expected no badge for unrated title, got %v", reportEmpty.Labels)
	}
	for _, l := range reportEmpty.Layers {
		if l.Key == "ratings" && (l.State != "unavailable" || len(l.Labels) != 0) {
			t.Fatalf("expected ratings layer unavailable with empty labels, got %+v", l)
		}
	}
}

func TestDisabledEvidenceDoesNoWorkAndVersionIgnoresNonVisualChanges(t *testing.T) {
	ctx := context.Background()
	db, e := store.OpenMemory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	factsCalls := 0
	s := New(db, Options{Facts: func(context.Context, *core.User, *core.Title) Facts { factsCalls++; return Facts{} }})
	title := &core.Title{PosterPath: "/a.jpg", Rating: 8, VoteCount: 10}
	cfg := Settings{Enabled: true, Ratings: true}
	ctx = context.WithValue(ctx, previewSettingsKey{}, cfg)
	before := s.Version(ctx, &core.User{ID: 1}, title, "en")
	title.UpdatedAt = time.Now()
	after := s.Version(ctx, &core.User{ID: 2}, title, "fr")
	if before != after {
		t.Fatal("unrelated metadata/viewer forced cache churn")
	}
	if factsCalls != 0 {
		t.Fatal("ratings-only preset looked up quality/status")
	}
	title.Rating = 9
	if before == s.Version(ctx, nil, title, "en") {
		t.Fatal("changed visible score did not change version")
	}
	cfg.Enabled = false
	ctx = context.WithValue(ctx, previewSettingsKey{}, cfg)
	before = s.Version(ctx, nil, title, "en")
	cfg.Style = "solid"
	cfg.Accent = "#ffffff"
	ctx = context.WithValue(ctx, previewSettingsKey{}, cfg)
	if before != s.Version(ctx, nil, title, "en") {
		t.Fatal("hidden badge style invalidated original")
	}
}

func TestExplanationMatchesPreviewAndEnforcesViewerPolicy(t *testing.T) {
	ctx := context.Background()
	db, e := store.OpenMemory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db, Options{})
	cat := previewCatalog{title: core.Title{Key: core.MovieKey(1), Rating: 8.4, VoteCount: 12, ContentRating: "R"}}
	router := s.Router(cat, func(*http.Request, int64) (*core.User, error) { return &core.User{ID: 1}, nil })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/explain/"+cat.title.Key.String()+"?settings="+url.QueryEscape(`{"enabled":true,"ratings":true}`), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "TMDB 8.4/10") {
		t.Fatalf("explain %d %s", w.Code, w.Body.String())
	}
	restricted := s.Router(cat, func(*http.Request, int64) (*core.User, error) { return &core.User{ID: 2, MaxContentRating: "PG"}, nil })
	w = httptest.NewRecorder()
	restricted.ServeHTTP(w, httptest.NewRequest("GET", "/explain/"+cat.title.Key.String(), nil))
	if w.Code != 404 {
		t.Fatal("restricted title leaked explanation")
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/explain/"+cat.title.Key.String()+"?settings="+url.QueryEscape(`{"style":"invalid"}`), nil))
	if w.Code != 400 {
		t.Fatal("invalid draft accepted")
	}
}

func TestQualityClassifiesCroppedWidescreenWithoutInventingAvailability(t *testing.T) {
	ctx := context.Background()
	db, e := store.OpenMemory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	facts := Facts{}
	s := New(db, Options{Facts: func(context.Context, *core.User, *core.Title) Facts { return facts }})
	ctx = context.WithValue(ctx, previewSettingsKey{}, Settings{Enabled: true, Quality: true})
	for _, tc := range []struct {
		w, h int
		want string
	}{{3840, 1600, "PLAYED 4K"}, {1920, 800, "PLAYED 1080p"}, {1280, 534, "PLAYED 720p"}, {720, 480, "PLAYED SD"}} {
		facts.Width, facts.Height = tc.w, tc.h
		got := s.Labels(ctx, nil, &core.Title{})
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%dx%d: %v, want %s", tc.w, tc.h, got, tc.want)
		}
	}
}

func TestSolidCustomAccentChoosesReadableInk(t *testing.T) {
	for _, tc := range []struct {
		background color.NRGBA
		dark       bool
	}{
		{color.NRGBA{G: 160, A: 255}, true},
		{color.NRGBA{R: 255, G: 255, B: 255, A: 255}, true},
		{color.NRGBA{A: 255}, false},
	} {
		got := readableInk(tc.background)
		if (got.R < 128) != tc.dark {
			t.Fatalf("accent %+v chose unreadable ink %+v", tc.background, got)
		}
	}
}
