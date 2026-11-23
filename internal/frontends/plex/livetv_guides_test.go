package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestOwnerGuideFilterDiscovery(t *testing.T) {
	f, service, ch := liveFixture(t)
	guides := []livetv.OwnerGroup{{ID: "family", Name: "Family", All: true}, {ID: "hidden", Name: "Hidden", All: true, Hidden: true}}
	if err := service.SaveOwnerGroups(context.Background(), guides); err != nil {
		t.Fatal(err)
	}
	for _, accept := range []string{"application/json", "application/xml"} {
		r := f.request(t, "GET", "/media/providers", map[string]string{"Accept": accept})
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "GridChannelFilter") || !strings.Contains(r.Body.String(), "ether.guide.family") || strings.Contains(r.Body.String(), "ether.guide.hidden") {
			t.Fatalf("discovery %s: %d %s", accept, r.Code, r.Body.String())
		}
	}
	r := f.request(t, "GET", liveProviderPath+"/guides/family/channels", map[string]string{"Accept": "application/json"})
	var out struct {
		MediaContainer struct {
			Channel []struct {
				ID string `json:"id"`
			} `json:"Channel"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || len(out.MediaContainer.Channel) != 1 || out.MediaContainer.Channel[0].ID != ch.ID {
		t.Fatalf("filter result %d: %s", r.Code, r.Body.String())
	}
	r = f.request(t, "GET", liveProviderPath+"/guides/hidden/channels", nil)
	if r.Code != 404 {
		t.Fatalf("hidden guide status %d", r.Code)
	}
	guides[0].Name = "Renamed"
	if err := service.SaveOwnerGroups(context.Background(), guides); err != nil {
		t.Fatal(err)
	}
	r = f.request(t, "GET", "/media/providers", map[string]string{"Accept": "application/json"})
	if !strings.Contains(r.Body.String(), "Renamed") || !strings.Contains(r.Body.String(), "ether.guide.family") {
		t.Fatal("rename changed identity or lost title")
	}
}

func TestFoldLiveGuideNameAndThresholds(t *testing.T) {
	pairs := [][2]string{
		{"Sports", "  spórts  "},
		{"Comédie", "comedie"},
		{"Français", "FRANCAIS"},
		{"Fußball", "fussball"},
	}
	for _, p := range pairs {
		if gotA, gotB := foldLiveGuideName(p[0]), foldLiveGuideName(p[1]); gotA == "" || gotA != gotB {
			t.Fatalf("expected %q and %q to fold identically, got %q vs %q", p[0], p[1], gotA, gotB)
		}
	}
	if foldLiveGuideName("Kids") == foldLiveGuideName("Kids & Family") {
		t.Fatal("substring/containment must not collide after folding")
	}

	cases := []struct {
		channels  int
		threshold int
	}{
		{0, 3},
		{1, 3},
		{10, 3},
		{60, 3},
		{61, 4},
		{80, 4},
		{81, 5},
		{100, 5},
		{200, 10},
	}
	for _, tc := range cases {
		if got := liveGuideGenreThreshold(tc.channels); got != tc.threshold {
			t.Fatalf("liveGuideGenreThreshold(%d) = %d, want %d", tc.channels, got, tc.threshold)
		}
	}

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-01 is a 25-hour autumn DST fall-back day in America/New_York;
	// 23:30 EST is 24h30m after local midnight, so queryFrom clamps within 24h.
	dstLate := time.Date(2026, 11, 1, 23, 30, 0, 0, ny)
	dayStart, dayEnd, queryFrom := liveGuideDayWindow(dstLate)
	if dayStart.Hour() != 0 || dayEnd.Day() != 2 || dstLate.Sub(queryFrom) >= 24*time.Hour {
		t.Fatalf("unexpected DST window: dayStart=%v dayEnd=%v queryFrom=%v", dayStart, dayEnd, queryFrom)
	}
}

func newClockLiveFixture(t *testing.T, nowFn func() time.Time, src livetv.Source) (*fixture, *livetv.Service) {
	t.Helper()
	f := newFixture(func(d *testServerDeps) {
		setDepsNow(d, nowFn)
	})
	t.Cleanup(func() { f.srv.Close() })
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nsegment.ts\n")
	}))
	t.Cleanup(up.Close)
	service, err := livetv.New(livetv.Options{DB: db, Now: nowFn, HTTPClient: up.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	if src.Playlist != "" {
		if _, err := service.ImportSource(context.Background(), src); err != nil {
			t.Fatal(err)
		}
	}
	f.srv.setLiveTV(service.Frontend())
	f.srv.setTestSubscription(true)
	return f, service
}

func warmGuideDay(t *testing.T, f *fixture) {
	t.Helper()
	for range 3 {
		r := f.request(t, "GET", liveProviderPath+"/lineups/dvr/channels?genre=movie", map[string]string{"Accept": "application/json"})
		if r.Code != http.StatusServiceUnavailable {
			return
		}
		f.srv.liveGuideDay.mu.Lock()
		ready := f.srv.liveGuideDay.ready
		f.srv.liveGuideDay.mu.Unlock()
		if ready != nil {
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("guide day snapshot did not finish")
			}
		}
	}
	t.Fatal("guide day snapshot stayed unavailable")
}

type filterItem struct {
	id, title, key, ratingKey string
}

func readProviderGuideFilters(t *testing.T, f *fixture) []filterItem {
	t.Helper()
	warmGuideDay(t, f)
	r := f.request(t, "GET", "/media/providers", map[string]string{"Accept": "application/json"})
	if r.Code != http.StatusOK {
		t.Fatalf("/media/providers: %d %s", r.Code, r.Body.String())
	}
	for _, raw := range decodeJSON(t, r)["MediaProvider"].([]any) {
		p := raw.(map[string]any)
		if p["identifier"] != livetv.Provider {
			continue
		}
		for _, fRaw := range p["Feature"].([]any) {
			feat := fRaw.(map[string]any)
			if feat["type"] != "grid" {
				continue
			}
			rawFilters, _ := feat["GridChannelFilter"].([]any)
			out := make([]filterItem, 0, len(rawFilters))
			for _, item := range rawFilters {
				m := item.(map[string]any)
				out = append(out, filterItem{
					id:        fmt.Sprint(m["identifier"]),
					title:     fmt.Sprint(m["title"]),
					key:       fmt.Sprint(m["key"]),
					ratingKey: fmt.Sprint(m["genreRatingKey"]),
				})
			}
			return out
		}
	}
	t.Fatalf("grid feature missing: %s", r.Body.String())
	return nil
}

func filterTitles(items []filterItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.title
	}
	return out
}

func filterIDs(items []filterItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

func TestGuideTabsOwnerGroupCollisionsAndOrdering(t *testing.T) {
	var playlist, guide strings.Builder
	playlist.WriteString("#EXTM3U\n")
	guide.WriteString("<tv>")
	channelCategories := [][]string{
		{"Movie", "Comedie", "Kids & Family"},
		{"Movie", "Comedie", "Kids & Family"},
		{"Movie", "Comedie", "Kids & Family"},
		{"Movie", "Comedie", "Kids & Family"},
		{"Sports", "Comedie", "Action"},
		{"Sports", "Action", "Drama"},
		{"Sports", "Action", "Drama"},
		{"Sports", "Kids", "Action", "Drama"},
		{"Kids", "News", "Sci-Fi"},
		{"Kids", "News", "Sci-Fi"},
	}
	for i, cats := range channelCategories {
		id := fmt.Sprintf("ch%d", i+1)
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=%q tvg-chno=%q,Channel %d\nhttp://127.0.0.1:9/%s.m3u8\n", id, fmt.Sprint(i+1), i+1, id)
		fmt.Fprintf(&guide, `<channel id=%q><display-name>Channel %d</display-name></channel>`, id, i+1)
		fmt.Fprintf(&guide, `<programme channel=%q start="20261018110000 +0000" stop="20261018130000 +0000"><title>Show %d</title>`, id, i+1)
		for _, c := range cats {
			fmt.Fprintf(&guide, `<category>%s</category>`, html.EscapeString(c))
		}
		guide.WriteString(`</programme>`)
	}
	guide.WriteString("</tv>")

	f, service := newClockLiveFixture(t, func() time.Time { return fixedNow }, livetv.Source{
		ID:       "main",
		Name:     "Main",
		Playlist: playlist.String(),
		Guide:    guide.String(),
	})

	// Without owner groups: built-in categories (>= 3 channels) first in canonical
	// order, then genres (>= max(3, ceil(5%*10))=3) sorted by count desc, name asc.
	// News (2), TV Shows (0), and Sci-Fi (2) stay unpublished.
	noGroups := readProviderGuideFilters(t, f)
	wantNoGroupTitles := []string{"Movies", "Sports", "Kids", "Comedie", "Action", "Kids & Family", "Drama"}
	if got := filterTitles(noGroups); !slices.Equal(got, wantNoGroupTitles) {
		t.Fatalf("tabs without owner groups = %v, want %v", got, wantNoGroupTitles)
	}

	// Add owner groups in explicit owner order:
	// - "  spórts  " collides with built-in "Sports" (case + diacritic + whitespace folding)
	// - "Kids & Family" collides with genre "Kids & Family", NOT with built-in "Kids"
	// - "Comédie" collides with genre "Comedie" (diacritic folding)
	groups := []livetv.OwnerGroup{
		{ID: "sports-custom", Name: "  spórts  ", All: true},
		{ID: "kids-family", Name: "Kids & Family", All: true},
		{ID: "comedie-fr", Name: "Comédie", All: true},
	}
	if err := service.SaveOwnerGroups(context.Background(), groups); err != nil {
		t.Fatal(err)
	}

	withGroups := readProviderGuideFilters(t, f)
	wantGroupTitles := []string{"spórts", "Kids & Family", "Comédie", "Movies", "Kids", "Action", "Drama"}
	wantGroupIDs := []string{
		"guide-sports-custom",
		"guide-kids-family",
		"guide-comedie-fr",
		"movie",
		"kids",
		liveGenreID("Action"),
		liveGenreID("Drama"),
	}
	if got := filterTitles(withGroups); !slices.Equal(got, wantGroupTitles) {
		t.Fatalf("tabs with owner groups = %v, want %v", got, wantGroupTitles)
	}
	if got := filterIDs(withGroups); !slices.Equal(got, wantGroupIDs) {
		t.Fatalf("tab IDs with owner groups = %v, want %v", got, wantGroupIDs)
	}
}

func TestGuideTabsFivePercentThresholdAndEightGenreCap(t *testing.T) {
	// 80 channels => 5% threshold is 4 channels (3 channels is below 5%).
	var playlist, guide strings.Builder
	playlist.WriteString("#EXTM3U\n")
	guide.WriteString("<tv>")
	genreSpecs := []struct {
		name  string
		count int
	}{
		{"GenreJ", 12},
		{"GenreB", 10},
		{"GenreA", 10}, // tied at 10 with GenreB -> GenreA sorts before GenreB
		{"GenreC", 9},
		{"GenreD", 8},
		{"GenreE", 7},
		{"GenreF", 6},
		{"GenreG", 5},
		{"GenreH", 4},
		{"GenreI", 4},
		{"TooSmallAtFivePct", 3}, // 3 < ceil(0.05 * 80) = 4 -> must not publish
	}
	for i := 0; i < 80; i++ {
		id := fmt.Sprintf("c%d", i+1)
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=%q tvg-chno=%q,Ch %d\nhttp://127.0.0.1:9/%s.m3u8\n", id, fmt.Sprint(i+1), i+1, id)
		fmt.Fprintf(&guide, `<channel id=%q><display-name>Ch %d</display-name></channel>`, id, i+1)
		fmt.Fprintf(&guide, `<programme channel=%q start="20261018110000 +0000" stop="20261018130000 +0000"><title>Prog %d</title>`, id, i+1)
		for _, gs := range genreSpecs {
			if i < gs.count {
				fmt.Fprintf(&guide, `<category>%s</category>`, gs.name)
			}
		}
		guide.WriteString(`</programme>`)
	}
	guide.WriteString("</tv>")

	f, service := newClockLiveFixture(t, func() time.Time { return fixedNow }, livetv.Source{
		ID:       "eighty",
		Name:     "Eighty",
		Playlist: playlist.String(),
		Guide:    guide.String(),
	})

	// Without owner groups: capped at 8 genres, sorted by (count desc, name asc).
	gotNoGroups := filterTitles(readProviderGuideFilters(t, f))
	wantNoGroups := []string{"GenreJ", "GenreA", "GenreB", "GenreC", "GenreD", "GenreE", "GenreF", "GenreG"}
	if !slices.Equal(gotNoGroups, wantNoGroups) {
		t.Fatalf("80-channel genres without groups = %v, want %v", gotNoGroups, wantNoGroups)
	}

	// Colliding owner group ("genrej") claims GenreJ and still allows 8 non-colliding
	// eligible genres (bringing in GenreH at count=4, while GenreI is 9th and
	// TooSmallAtFivePct at count=3 stays below the 5% threshold).
	if err := service.SaveOwnerGroups(context.Background(), []livetv.OwnerGroup{
		{ID: "j", Name: "genrej", All: true},
	}); err != nil {
		t.Fatal(err)
	}
	gotWithGroup := filterTitles(readProviderGuideFilters(t, f))
	wantWithGroup := []string{"genrej", "GenreA", "GenreB", "GenreC", "GenreD", "GenreE", "GenreF", "GenreG", "GenreH"}
	if !slices.Equal(gotWithGroup, wantWithGroup) {
		t.Fatalf("80-channel genres with colliding group = %v, want %v", gotWithGroup, wantWithGroup)
	}
}

func TestGuideTabsStableAcrossCalendarDayAndChannelFiltering(t *testing.T) {
	var currentNow atomic.Int64 // read by the Live TV refresh loop's goroutine
	currentNow.Store(time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC).UnixNano())
	nowFn := func() time.Time { return time.Unix(0, currentNow.Load()).UTC() }

	var playlist, guide strings.Builder
	playlist.WriteString("#EXTM3U\n")
	guide.WriteString("<tv>")
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("day%d", i)
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=%q tvg-chno=%q,Day %d\nhttp://127.0.0.1:9/%s.m3u8\n", id, fmt.Sprint(i), i, id)
		fmt.Fprintf(&guide, `<channel id=%q><display-name>Day %d</display-name></channel>`, id, i)
		if i <= 4 {
			// Morning-only genre (06:00-08:00 UTC) on 4 channels.
			fmt.Fprintf(&guide, `<programme channel=%q start="20261018060000 +0000" stop="20261018080000 +0000"><title>Sunrise %d</title><category>Morning Yoga</category></programme>`, id, i)
		}
		if i <= 3 {
			// Afternoon-only genre (14:00-16:00 UTC) on 3 channels.
			fmt.Fprintf(&guide, `<programme channel=%q start="20261018140000 +0000" stop="20261018160000 +0000"><title>Bake %d</title><category>Afternoon Baking</category></programme>`, id, i)
			// Night-only movie category (22:00-23:30 UTC) on 3 channels.
			fmt.Fprintf(&guide, `<programme channel=%q start="20261018220000 +0000" stop="20261018233000 +0000"><title>Feature %d</title><category>Movie</category></programme>`, id, i)
			// Next calendar day (2026-10-19) has a different genre.
			fmt.Fprintf(&guide, `<programme channel=%q start="20261019090000 +0000" stop="20261019110000 +0000"><title>Next %d</title><category>Next Day Doc</category></programme>`, id, i)
		}
	}
	guide.WriteString("</tv>")

	f, _ := newClockLiveFixture(t, nowFn, livetv.Source{
		ID:       "day",
		Name:     "Day",
		Playlist: playlist.String(),
		Guide:    guide.String(),
	})

	wantDay1 := []string{"Movies", "Morning Yoga", "Afternoon Baking"}
	morningFilters := readProviderGuideFilters(t, f)
	if got := filterTitles(morningFilters); !slices.Equal(got, wantDay1) {
		t.Fatalf("07:00 tabs = %v, want %v", got, wantDay1)
	}

	// Midday (after Morning Yoga ended) and late night (even with a cold cache)
	// must produce identical tabs and still resolve Morning Yoga's channel filter.
	for _, ts := range []time.Time{
		time.Date(2026, 10, 18, 12, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 18, 23, 15, 0, 0, time.UTC),
	} {
		currentNow.Store((ts).UnixNano())
		// Force a cold rebuild at 23:15 to prove stability comes from the day window,
		// not merely from a warm morning cache entry.
		if ts.Hour() == 23 {
			f.srv.liveGuideDay.mu.Lock()
			f.srv.liveGuideDay.entries = nil
			f.srv.liveGuideDay.mu.Unlock()
		}
		gotFilters := readProviderGuideFilters(t, f)
		if !slices.Equal(gotFilters, morningFilters) {
			t.Fatalf("tabs shifted at %v: got %+v, want %+v", ts, gotFilters, morningFilters)
		}

		yogaResp := f.request(t, "GET", morningFilters[1].key, map[string]string{"Accept": "application/json"})
		if yogaResp.Code != http.StatusOK {
			t.Fatalf("Morning Yoga filter at %v: %d %s", ts, yogaResp.Code, yogaResp.Body.String())
		}
		chs, _ := decodeJSON(t, yogaResp)["Channel"].([]any)
		if len(chs) != 4 {
			t.Fatalf("Morning Yoga channels at %v = %d, want 4: %s", ts, len(chs), yogaResp.Body.String())
		}
		allResp := f.request(t, "GET", liveProviderPath+"/lineups/dvr/channels", map[string]string{"Accept": "application/json"})
		firstCh := decodeJSON(t, allResp)["Channel"].([]any)[0].(map[string]any)
		if !strings.Contains(fmt.Sprint(firstCh["genreRatingKeys"]), morningFilters[1].ratingKey) {
			t.Fatalf("lineup GenreRatingKeys at %v missing %s: %v", ts, morningFilters[1].ratingKey, firstCh["genreRatingKeys"])
		}
	}

	// Rolling into the next calendar day switches to that day's guide window.
	currentNow.Store((time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)).UnixNano())
	nextDayTitles := filterTitles(readProviderGuideFilters(t, f))
	if !slices.Equal(nextDayTitles, []string{"Next Day Doc"}) {
		t.Fatalf("next calendar day tabs = %v, want [Next Day Doc]", nextDayTitles)
	}
}
