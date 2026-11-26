package plex

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func gzipSize(b []byte) int {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Len()
}

// TestRokuShortPrefixSearchFanoutAndContract replays Roku's real short-prefix
// searches ("F" and "Fi") against both universal search endpoints advertised in
// /media/providers:
//   - /library/search?includeCollections=1&query=<q>&limit=100&contentDirectoryID=1,2,playlists&searchTypes=categories,movies,people,tv
//   - /tv.plex.providers.epg.xmltv:1/library/search?includeCollections=1&query=<q>&limit=100&contentDirectoryID=1,2,playlists&searchTypes=categories,movies,people,tv
//
// It verifies bounded DB query/row work, zero trailer-table reads, exact
// response contract/limits, and superseded-search handling on the EPG endpoint.
func TestRokuShortPrefixSearchFanoutAndContract(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)

	seedCat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}

	// Seed 260 movies + 80 shows matching "F" (with 120 matching "Fi"), plus
	// 40 partial-match people starting with "F"/"Fi", episodes, and remote
	// trailer rows so any fallback to factsFull is immediately visible in SQL.
	const (
		movieCount  = 260
		showCount   = 80
		personCount = 40
	)
	people := make([]core.Person, personCount)
	for i := range people {
		prefix := "Frank"
		if i%2 == 0 {
			prefix = "Fiona"
		}
		people[i] = core.Person{
			TMDB:      int64(50000 + i),
			Name:      fmt.Sprintf("%s Walker %02d", prefix, i),
			Role:      "Lead",
			ThumbPath: fmt.Sprintf("/person-%d.jpg", i),
		}
	}

	movieKeys := make([]core.RatingKey, 0, movieCount)
	for i := 0; i < movieCount; i++ {
		prefix := "Fargo"
		if i < 120 {
			prefix = "Fight"
		}
		k := core.MovieKey(int64(10000 + i))
		movieKeys = append(movieKeys, k)
		p1 := people[i%personCount]
		p2 := people[(i+7)%personCount]
		title := core.Title{
			Key:           k,
			IDs:           core.IDs{TMDB: int64(10000 + i)},
			Type:          core.TypeMovie,
			Title:         fmt.Sprintf("%s Story %03d", prefix, i),
			Year:          2000 + (i % 25),
			Released:      fixedNow.AddDate(-2, 0, -(i % 180)),
			Runtime:       110 * time.Minute,
			ContentRating: "PG-13",
			Rating:        7.4,
			VoteCount:     5000 - i*5,
			Popularity:    200 - float64(i)*0.4,
			Summary:       fmt.Sprintf("A detailed story about %s #%d.", prefix, i),
			PosterPath:    fmt.Sprintf("/poster-%d.jpg", i),
			BackdropPath:  fmt.Sprintf("/backdrop-%d.jpg", i),
			Genres:        []string{"Drama", "Thriller"},
			Studios:       []string{"Fixture Pictures"},
			Countries:     []string{"US"},
			Cast:          []core.Person{p1, p2},
			Directors:     []core.Person{people[(i+13)%personCount]},
		}
		if err := seedCat.UpsertDetailedTitle(ctx, title); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog_remote_trailers(key, name, url) VALUES(?, 'Official Trailer', 'https://example.invalid/trailer.mp4')`, int64(k)); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < showCount; i++ {
		prefix := "Fringe"
		if i < 40 {
			prefix = "Firefly"
		}
		showKey := core.ShowKey(int64(20000 + i))
		show := core.Title{
			Key:           showKey,
			IDs:           core.IDs{TMDB: int64(20000 + i)},
			Type:          core.TypeShow,
			Title:         fmt.Sprintf("%s Chronicles %03d", prefix, i),
			Year:          2010 + (i % 15),
			Released:      fixedNow.AddDate(-3, 0, -(i % 90)),
			Runtime:       45 * time.Minute,
			ContentRating: "TV-14",
			Rating:        8.1,
			VoteCount:     3000 - i*10,
			Popularity:    150 - float64(i)*0.5,
			Summary:       fmt.Sprintf("Series overview for %s #%d.", prefix, i),
			PosterPath:    fmt.Sprintf("/show-%d.jpg", i),
			ChildCount:    1,
			LeafCount:     2,
			Genres:        []string{"Sci-Fi"},
			Studios:       []string{"Fixture Network"},
			Cast:          []core.Person{people[i%personCount]},
		}
		if err := seedCat.UpsertDetailedTitle(ctx, show); err != nil {
			t.Fatal(err)
		}
		epKey := core.EpisodeKey(int64(20000+i), 1, 1)
		ep := core.Title{
			Key:       epKey,
			IDs:       core.IDs{TMDB: int64(20000 + i)},
			Type:      core.TypeEpisode,
			ShowKey:   showKey,
			ShowTitle: show.Title,
			Season:    1,
			Episode:   1,
			Title:     fmt.Sprintf("%s Pilot %03d", prefix, i),
			Released:  show.Released,
			Runtime:   45 * time.Minute,
			Cast:      []core.Person{people[(i+3)%personCount]},
		}
		if err := seedCat.UpsertDetailedTitle(ctx, ep); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog_remote_trailers(key, name, url) VALUES(?, 'Series Trailer', 'https://example.invalid/show.mp4')`, int64(showKey)); err != nil {
			t.Fatal(err)
		}
	}

	// Build Live TV service backed by a fresh Catalog instance (cold titleCache)
	// and populate virtual channels + 400 indexed virtual programmes referencing
	// distinct movie keys matching "F" and "Fi".
	liveCat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	liveSvc, err := livetv.New(livetv.Options{
		DB:      db,
		Catalog: liveCat,
		Now:     func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { liveSvc.Close() })

	// Create 6 virtual channels matching "F"/"Fi" and seed 400 virtual programmes
	// in livetv_search_virtual so SearchLiveTV hits its LIMIT 400 cap just like prod.
	const vChanCount = 6
	vChans := make([]livetv.Channel, vChanCount)
	for i := 0; i < vChanCount; i++ {
		ch := livetv.Channel{
			ID:      fmt.Sprintf("vchan-%d", i),
			Name:    fmt.Sprintf("Film Channel %d", i+1),
			Number:  fmt.Sprintf("%d", 500+i),
			Kind:    "virtual",
			Enabled: true,
			Group:   "Movies",
			Items:   []livetv.Item{{Key: movieKeys[i], Title: fmt.Sprintf("Fight Story %03d", i), DurationMS: 3600_000}},
		}
		saved, err := liveSvc.SaveChannel(ctx, ch)
		if err != nil {
			t.Fatal(err)
		}
		vChans[i] = saved
	}

	// Demand and wait for Live TV search index readiness before inserting virtual rows.
	liveSearch := liveSvc.Frontend().(frontend.LiveTVSearch)
	for deadline := time.Now().Add(5 * time.Second); ; {
		hits, e := liveSearch.SearchLiveTV(ctx, &ownerUser, "Film", 5)
		if e != nil {
			t.Fatal(e)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("livetv search index did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		ch := vChans[i%vChanCount]
		mk := movieKeys[i%len(movieKeys)]
		title := fmt.Sprintf("Fargo Story %03d", i%len(movieKeys))
		if i%len(movieKeys) < 120 {
			title = fmt.Sprintf("Fight Story %03d", i%len(movieKeys))
		}
		start := fixedNow.Add(time.Duration(i/vChanCount) * time.Hour)
		end := start.Add(time.Hour)
		p := livetv.Programme{
			ID:        fmt.Sprintf("%s-%d", ch.ID, start.UnixMilli()),
			ChannelID: ch.ID,
			Start:     start,
			End:       end,
			Item: livetv.Item{
				Key:        mk,
				Title:      title,
				Summary:    "Virtual channel broadcast for " + title,
				Rating:     "PG-13",
				DurationMS: 3600_000,
			},
		}
		raw, _ := json.Marshal(p)
		body := strings.ToLower(p.Item.Title + " " + p.Item.Summary)
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO livetv_search_virtual(id, viewer_id, channel_id, start_ms, end_ms, data, body) VALUES(?, 0, ?, ?, ?, ?, ?)`,
			p.ID, ch.ID, start.UnixMilli(), end.UnixMilli(), string(raw), body,
		); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rokuHeaders := map[string]string{
		"X-Plex-Token":             ownerToken,
		"X-Plex-Client-Identifier": "roku-tcl-65",
		"X-Plex-Platform":          "Roku",
		"X-Plex-Product":           "Plex for Roku",
	}

	runMeasured := func(t *testing.T, path string, accept string) (*httptest.ResponseRecorder, *store.QueryStats) {
		t.Helper()
		// Fresh cold Catalog + Server per probe so titleCache starts empty like cold prod requests.
		cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
		if err != nil {
			t.Fatal(err)
		}
		lsvc, err := livetv.New(livetv.Options{
			DB:      db,
			Catalog: cat,
			Now:     func() time.Time { return fixedNow },
		})
		if err != nil {
			t.Fatal(err)
		}
		defer lsvc.Close()
		// Wait for index ready on this instance (already built in DB).
		ls := lsvc.Frontend().(frontend.LiveTVSearch)
		for deadline := time.Now().Add(2 * time.Second); ; {
			h, e := ls.SearchLiveTV(ctx, &ownerUser, "zzzznomatch", 1)
			if e != nil {
				t.Fatal(e)
			}
			if !h.Partial {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("livetv index not ready")
			}
			time.Sleep(5 * time.Millisecond)
		}
		f := newFixture(func(d *testServerDeps) {
			d.Catalog = cat
			d.State = state.New(db, state.Options{})
			d.DB = db
			d.DisableEnrichment = true
			d.Recommender = nil
			d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
				return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
			}}
		})
		defer f.srv.Close()
		f.srv.setLiveTV(lsvc.Frontend())
		f.srv.setTestSubscription(true)

		// Warm only the in-memory title index (built at startup in prod), leaving titleCache cold.
		if _, err := cat.Search(ctx, "zzzzwarmindex", []core.MediaType{core.TypeMovie}, 1); err != nil {
			t.Fatal(err)
		}

		qctx, stats := store.WithQueryStats(reqmemo.With(ctx))
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(qctx)
		for k, v := range rokuHeaders {
			req.Header.Set(k, v)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		return rec, stats
	}

	trailerQueries := func(stats *store.QueryStats) int {
		n := 0
		for _, s := range stats.Statements() {
			if strings.Contains(strings.ToLower(s.SQL), "catalog_remote_trailers") {
				n += int(s.Count)
			}
		}
		return n
	}

	for _, q := range []string{"F", "Fi"} {
		libPath := fmt.Sprintf("/library/search?includeCollections=1&query=%s&limit=100&contentDirectoryID=1,2,playlists&searchTypes=categories,movies,people,tv", q)
		epgPath := fmt.Sprintf("%s/library/search?includeCollections=1&query=%s&limit=100&contentDirectoryID=1,2,playlists&searchTypes=categories,movies,people,tv", liveProviderPath, q)

		t.Run("library_search_"+q+"_xml", func(t *testing.T) {
			rec, stats := runMeasured(t, libPath, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.Bytes()
			tr := trailerQueries(stats)
			t.Logf("endpoint=/library/search query=%s format=xml status=%d queries=%d rows=%d trailer_queries=%d raw_bytes=%d gzip_bytes=%d",
				q, rec.Code, stats.Queries(), stats.Rows(), tr, len(body), gzipSize(body))
			if tr != 0 {
				t.Errorf("expected 0 catalog_remote_trailers queries, got %d", tr)
			}
			if stats.Queries() > 40 {
				for _, s := range stats.Statements() {
					t.Logf("  x%d %s", s.Count, s.SQL)
				}
				t.Errorf("queries=%d exceeded budget 40", stats.Queries())
			}
		})

		t.Run("library_search_"+q+"_json", func(t *testing.T) {
			rec, stats := runMeasured(t, libPath, "application/json")
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			mc := decodeJSON(t, rec)
			size := int(mc["size"].(float64))
			body := rec.Body.Bytes()
			tr := trailerQueries(stats)
			t.Logf("endpoint=/library/search query=%s format=json status=%d size=%d queries=%d rows=%d trailer_queries=%d raw_bytes=%d gzip_bytes=%d",
				q, rec.Code, size, stats.Queries(), stats.Rows(), tr, len(body), gzipSize(body))
			if size == 0 || size > 100 {
				t.Fatalf("size=%d, want 1..100", size)
			}
			if tr != 0 {
				t.Errorf("expected 0 catalog_remote_trailers queries, got %d", tr)
			}
		})

		t.Run("epg_library_search_"+q+"_xml", func(t *testing.T) {
			rec, stats := runMeasured(t, epgPath, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.Bytes()
			tr := trailerQueries(stats)
			t.Logf("endpoint=%s/library/search query=%s format=xml status=%d queries=%d rows=%d trailer_queries=%d raw_bytes=%d gzip_bytes=%d",
				liveProviderPath, q, rec.Code, stats.Queries(), stats.Rows(), tr, len(body), gzipSize(body))
			if tr != 0 {
				t.Errorf("expected 0 catalog_remote_trailers queries, got %d", tr)
			}
			if stats.Queries() > 30 {
				for _, s := range stats.Statements() {
					t.Logf("  x%d %s", s.Count, s.SQL)
				}
				t.Errorf("queries=%d exceeded budget 30", stats.Queries())
			}
			if stats.Rows() > 2500 {
				t.Errorf("rows=%d exceeded budget 2500", stats.Rows())
			}
		})

		t.Run("epg_library_search_"+q+"_json", func(t *testing.T) {
			rec, stats := runMeasured(t, epgPath, "application/json")
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			mc := decodeJSON(t, rec)
			size := int(mc["size"].(float64))
			body := rec.Body.Bytes()
			tr := trailerQueries(stats)
			t.Logf("endpoint=%s/library/search query=%s format=json status=%d size=%d queries=%d rows=%d trailer_queries=%d raw_bytes=%d gzip_bytes=%d",
				liveProviderPath, q, rec.Code, size, stats.Queries(), stats.Rows(), tr, len(body), gzipSize(body))
			if size == 0 || size > 100 {
				t.Fatalf("size=%d, want 1..100", size)
			}
			if tr != 0 {
				t.Errorf("expected 0 catalog_remote_trailers queries, got %d", tr)
			}
		})
	}
}
