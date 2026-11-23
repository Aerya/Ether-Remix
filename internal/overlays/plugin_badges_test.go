package overlays

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

func testPosterBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 500, 750))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{24, 34, 48, 255}), image.Point{}, draw.Src)
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, img, nil); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestPluginBadgesFiftyTitlePageUsesSingleExtraQuery(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	images := &fakeImages{data: testPosterBytes(t)}

	var activeSets []PluginBadgeSet
	s := New(db, Options{
		Images:       images,
		PluginBadges: func(context.Context) []PluginBadgeSet { return activeSets },
	})
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Status: false, Quality: false, Ratings: false, Details: false}); err != nil {
		t.Fatal(err)
	}

	titles := make([]core.Title, 50)
	for i := range titles {
		tmdbID := int64(1000 + i)
		titles[i] = core.Title{
			Key:        core.MovieKey(tmdbID),
			Type:       core.TypeMovie,
			Title:      fmt.Sprintf("Title %d", i),
			PosterPath: "/default.jpg",
			IDs:        core.IDs{TMDB: tmdbID, IMDB: fmt.Sprintf("tt%07d", tmdbID)},
		}
	}

	// Seed badges across canonical keys, pre-pushed TMDB keys and pre-pushed IMDb keys,
	// plus rows from a disabled/unlisted plugin to confirm read-time filtering.
	insertBadge := func(pluginID, setKey, titleKey, labelsJSON string) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO plugin_poster_badges(plugin_id,set_key,title_key,labels_json,updated_at) VALUES(?,?,?,?,1)`,
			pluginID, setKey, titleKey, labelsJSON,
		); err != nil {
			t.Fatal(err)
		}
	}
	insertBadge("plug.dubs", "audio", titles[0].Key.String(), `["EN DUB","DUAL AUDIO"]`)
	insertBadge("plug.dubs", "cuts", fmt.Sprintf("tmdb:movie:%d", titles[17].IDs.TMDB), `["DIRECTOR'S CUT"]`)
	insertBadge("plug.fest", "awards", fmt.Sprintf("imdb:movie:%s", titles[49].IDs.IMDB), `["PALME D'OR"]`)
	insertBadge("plug.disabled", "secret", titles[0].Key.String(), `["HIDDEN"]`)

	// Verify SQLite uses plugin_poster_badges_title_key index for IN (...) lookups.
	planRows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT plugin_id, set_key, title_key, labels_json FROM plugin_poster_badges WHERE title_key IN (?,?,?)`,
		titles[0].Key.String(), "tmdb:movie:1017", "imdb:movie:tt0001049")
	if err != nil {
		t.Fatal(err)
	}
	var planDetails []string
	for planRows.Next() {
		var id, parent, notused int
		var detail string
		if err := planRows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		planDetails = append(planDetails, detail)
	}
	_ = planRows.Close()
	if !strings.Contains(strings.Join(planDetails, " "), "plugin_poster_badges_title_key") {
		t.Fatalf("query plan did not use plugin_poster_badges_title_key: %v", planDetails)
	}

	// 1) Baseline 50-title page when NO plugin badge sets are active.
	baseCtx, err := s.WithTitlePage(ctx, nil, titles)
	if err != nil {
		t.Fatal(err)
	}
	for i := range titles {
		_ = s.Version(baseCtx, nil, &titles[i], "en")
		if got := s.Labels(baseCtx, nil, &titles[i]); len(got) != 0 {
			t.Fatalf("unexpected labels with no active sets: %v", got)
		}
	}

	// 2) Active plugin badge sets across 50 titles.
	activeSets = []PluginBadgeSet{
		{PluginID: "plug.dubs", PluginName: "Dub Tracker", Key: "audio", Label: "Dubbed Audio", Description: "Tracks dubbed audio"},
		{PluginID: "plug.dubs", PluginName: "Dub Tracker", Key: "cuts", Label: "Special Cuts"},
		{PluginID: "plug.fest", PluginName: "Festival Picks", Key: "awards", Label: "Awards"},
	}
	pageCtx, err := s.WithTitlePage(ctx, nil, titles)
	if err != nil {
		t.Fatal(err)
	}
	for i := range titles {
		_ = s.Version(pageCtx, nil, &titles[i], "en")
		_ = s.Labels(pageCtx, nil, &titles[i])
		_, _ = s.Inspect(pageCtx, nil, &titles[i])
	}

	if got := s.Labels(pageCtx, nil, &titles[0]); !reflect.DeepEqual(got, []string{"EN DUB", "DUAL AUDIO"}) {
		t.Fatalf("title[0] labels = %v", got)
	}
	if got := s.Labels(pageCtx, nil, &titles[17]); !reflect.DeepEqual(got, []string{"DIRECTOR'S CUT"}) {
		t.Fatalf("title[17] tmdb fallback labels = %v", got)
	}
	if got := s.Labels(pageCtx, nil, &titles[49]); !reflect.DeepEqual(got, []string{"PALME D'OR"}) {
		t.Fatalf("title[49] imdb fallback labels = %v", got)
	}
	if got := s.Labels(pageCtx, nil, &titles[5]); len(got) != 0 {
		t.Fatalf("unbadged title[5] labels = %v", got)
	}

	rendered, ct, err := s.Poster(pageCtx, nil, &titles[0], "en")
	if err != nil || ct != "image/jpeg" || bytes.Equal(rendered, images.data) {
		t.Fatalf("Poster render failed: ct=%s err=%v", ct, err)
	}

	singleCtx := reqmemo.With(ctx)
	_ = s.Version(singleCtx, nil, &titles[0], "en")
	_, _, err = s.Poster(singleCtx, nil, &titles[0], "en")
	if err != nil {
		t.Fatal(err)
	}
}

func TestPluginBadgesChangeVersionAndOwnerOptOutHidesSet(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rawPoster := testPosterBytes(t)
	images := &fakeImages{data: rawPoster}
	activeSets := []PluginBadgeSet{
		{PluginID: "plug.dubs", PluginName: "Dub Tracker", Key: "audio", Label: "Dubbed Audio", Description: "Dubbed releases"},
		{PluginID: "plug.dubs", PluginName: "Dub Tracker", Key: "cuts", Label: "Special Cuts"},
	}
	s := New(db, Options{
		Images:       images,
		PluginBadges: func(context.Context) []PluginBadgeSet { return activeSets },
	})
	if err := s.SaveSettings(ctx, Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	title := &core.Title{
		Key:        core.MovieKey(603),
		Type:       core.TypeMovie,
		Title:      "The Matrix",
		PosterPath: "/matrix.jpg",
		IDs:        core.IDs{TMDB: 603, IMDB: "tt0133093"},
	}

	// (4) Changing a title's badges changes Version and busts rendered poster bytes.
	vUnbadged := s.Version(ctx, nil, title, "en")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO plugin_poster_badges(plugin_id,set_key,title_key,labels_json,updated_at) VALUES('plug.dubs','audio',?,'["EN DUB"]',1)`,
		title.Key.String(),
	); err != nil {
		t.Fatal(err)
	}
	vDub := s.Version(ctx, nil, title, "en")
	if vDub == vUnbadged {
		t.Fatalf("adding badge did not change Version (%s)", vDub)
	}
	posterDub, _, err := s.Poster(ctx, nil, title, "en")
	if err != nil || bytes.Equal(posterDub, rawPoster) {
		t.Fatalf("badged poster was not painted: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE plugin_poster_badges SET labels_json='["JA + EN"]' WHERE plugin_id='plug.dubs' AND set_key='audio' AND title_key=?`,
		title.Key.String(),
	); err != nil {
		t.Fatal(err)
	}
	vUpdated := s.Version(ctx, nil, title, "en")
	if vUpdated == vDub || vUpdated == vUnbadged {
		t.Fatalf("changing badge label did not change Version: before=%s after=%s", vDub, vUpdated)
	}
	posterUpdated, _, err := s.Poster(ctx, nil, title, "en")
	if err != nil || bytes.Equal(posterUpdated, posterDub) {
		t.Fatal("changing badge label did not update rendered poster")
	}

	// Add a second set ("cuts") so we can opt out of "audio" while keeping "cuts".
	if _, err := db.ExecContext(ctx,
		`INSERT INTO plugin_poster_badges(plugin_id,set_key,title_key,labels_json,updated_at) VALUES('plug.dubs','cuts',?,'["IMAX"]',1)`,
		title.Key.String(),
	); err != nil {
		t.Fatal(err)
	}
	vBoth := s.Version(ctx, nil, title, "en")
	if got := s.Labels(ctx, nil, title); !reflect.DeepEqual(got, []string{"JA + EN", "IMAX"}) {
		t.Fatalf("labels with both sets = %v", got)
	}

	// (5) Owner opt-out via PUT /settings (DisabledBadgeSets) hides that set in Labels, Inspect, Version and Poster,
	// and GET /badge-sets lists active sets for the console.
	router := s.Router(previewCatalog{title: *title}, nil)

	wSets := httptest.NewRecorder()
	router.ServeHTTP(wSets, httptest.NewRequest("GET", "/badge-sets", nil))
	if wSets.Code != 200 {
		t.Fatalf("GET /badge-sets = %d %s", wSets.Code, wSets.Body.String())
	}
	var listedResp struct {
		Sets []PluginBadgeSet `json:"sets"`
	}
	if err := json.Unmarshal(wSets.Body.Bytes(), &listedResp); err != nil || len(listedResp.Sets) != 2 || listedResp.Sets[0].LayerKey != "plugin:plug.dubs:audio" {
		t.Fatalf("GET /badge-sets = %+v (err=%v)", listedResp, err)
	}

	wSave := httptest.NewRecorder()
	router.ServeHTTP(wSave, httptest.NewRequest("PUT", "/settings", strings.NewReader(`{"enabled":true,"disabledBadgeSets":["plugin:plug.dubs:audio"]}`)))
	if wSave.Code != 200 {
		t.Fatalf("PUT /settings = %d %s", wSave.Code, wSave.Body.String())
	}

	if got := s.Labels(ctx, nil, title); !reflect.DeepEqual(got, []string{"IMAX"}) {
		t.Fatalf("labels after opting out of audio = %v, want [IMAX]", got)
	}
	vAfterOptOut := s.Version(ctx, nil, title, "en")
	if vAfterOptOut == vBoth {
		t.Fatal("opting out of badge set did not change Version")
	}

	pres, err := s.Inspect(ctx, nil, title)
	if err != nil {
		t.Fatal(err)
	}
	var audioLayer, cutsLayer *LayerReport
	for i := range pres.Layers {
		switch pres.Layers[i].Key {
		case "plugin:plug.dubs:audio":
			audioLayer = &pres.Layers[i]
		case "plugin:plug.dubs:cuts":
			cutsLayer = &pres.Layers[i]
		}
	}
	if audioLayer == nil || audioLayer.State != "off" || len(audioLayer.Labels) != 0 {
		t.Fatalf("expected opted-out audio layer state=off, got %+v", audioLayer)
	}
	if cutsLayer == nil || cutsLayer.State != "ready" || !reflect.DeepEqual(cutsLayer.Labels, []string{"IMAX"}) {
		t.Fatalf("expected active cuts layer state=ready [IMAX], got %+v", cutsLayer)
	}
}
