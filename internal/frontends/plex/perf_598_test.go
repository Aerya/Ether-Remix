package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestArtworkPolicyLookupIndexPlanAndParity(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)

	for _, row := range []struct {
		key      core.RatingKey
		kind     string
		tmdbID   int
		adult    int
		poster   string
		backdrop string
		still    string
		logo     string
	}{
		{core.MovieKey(901), "movie", 901, 0, "/p901.jpg", "/b901.jpg", "", "/l901.png"},
		{core.EpisodeKey(902, 1, 1), "episode", 902, 0, "", "", "/s902.jpg", ""},
		{core.MovieKey(903), "movie", 903, 1, "/adult.jpg", "", "", ""},
		{fightClub.Key, "movie", 550, 0, "/shared.jpg", "", "", ""},
		{core.MovieKey(904), "movie", 904, 0, "/shared.jpg", "", "", ""},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO catalog_titles(key,type,tmdb_id,title,year,adult,poster,backdrop,still,logo) VALUES(?,?,?,?,2020,?,?,?,?,?)`,
			int64(row.key), row.kind, row.tmdbID, "Title", row.adult, row.poster, row.backdrop, row.still, row.logo); err != nil {
			t.Fatal(err)
		}
	}

	// Plan assertion (both SQLite and PostgreSQL): all four partial artwork
	// indexes are used and catalog_titles is never scanned whole.
	var plan string
	if store.DialectOf(db) == store.Postgres {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, `SET enable_seqscan = off`); err != nil {
			t.Fatal(err)
		}
		rows, err := conn.QueryContext(ctx, "EXPLAIN "+artworkPolicyLookupSQL, "/p901.jpg", "/p901.jpg", "/p901.jpg", "/p901.jpg")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		plan = strings.Join(lines, "\n")
		if strings.Contains(plan, "Seq Scan on catalog_titles") {
			t.Fatalf("unexpected Seq Scan on catalog_titles:\n%s", plan)
		}
	} else {
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+artworkPolicyLookupSQL, "/p901.jpg", "/p901.jpg", "/p901.jpg", "/p901.jpg")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		plan = strings.Join(details, " | ")
		if strings.Contains(plan, "SCAN catalog_titles ") {
			t.Fatalf("unexpected full scan on catalog_titles: %s", plan)
		}
	}
	for _, wantIdx := range []string{
		"catalog_titles_artwork_poster",
		"catalog_titles_artwork_backdrop",
		"catalog_titles_artwork_still",
		"catalog_titles_artwork_logo",
	} {
		if !strings.Contains(plan, wantIdx) {
			t.Fatalf("expected artwork lookup plan to use %s, got:\n%s", wantIdx, plan)
		}
	}

	f := newFixture()
	defer f.srv.Close()
	f.setDB(db)
	f.catalog.titles[core.MovieKey(901)] = core.Title{Key: core.MovieKey(901), Type: core.TypeMovie, ContentRating: "PG"}
	f.catalog.titles[core.EpisodeKey(902, 1, 1)] = core.Title{Key: core.EpisodeKey(902, 1, 1), Type: core.TypeEpisode, ContentRating: "TV-G"}
	f.catalog.titles[core.MovieKey(903)] = core.Title{Key: core.MovieKey(903), Type: core.TypeMovie, ContentRating: "PG", Adult: true}
	f.catalog.titles[core.MovieKey(904)] = core.Title{Key: core.MovieKey(904), Type: core.TypeMovie, ContentRating: "PG"}

	req := httptest.NewRequest(http.MethodGet, "/photo/:/transcode", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &Request{User: &core.User{ID: 30, MaxContentRating: "PG"}}))
	for _, tc := range []struct {
		raw     string
		allowed bool
	}{
		{"https://image.tmdb.org/t/p/w500/p901.jpg", true},
		{"https://image.tmdb.org/t/p/w780/b901.jpg", true},
		{"https://image.tmdb.org/t/p/w300/s902.jpg", true},
		{"https://image.tmdb.org/t/p/w500/l901.png", true},
		{"https://image.tmdb.org/t/p/w500/shared.jpg", true},
		{"https://image.tmdb.org/t/p/w500/adult.jpg", false},
		{"https://image.tmdb.org/t/p/w500/missing.jpg", false},
		{"https://image.tmdb.org/", false},
	} {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.srv.remoteArtworkAllowed(req, u); got != tc.allowed {
			t.Errorf("%s: remoteArtworkAllowed=%v, want %v", tc.raw, got, tc.allowed)
		}
	}
}

func TestMetadataBatchParity(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	// Watch only the second episode (bbE2), leaving bbE1 unwatched so sparse
	// watch-state maps do not contain eps[0].
	f.state.set(ownerUser.ID, core.WatchStatus{Key: bbE2.Key, ViewCount: 1, LastViewedAt: fixedNow, UpdatedAt: fixedNow})

	pgMovie := core.Title{Key: core.MovieKey(777), Type: core.TypeMovie, Title: "Gentle Meadow", ContentRating: "PG", Year: 2021}
	f.catalog.titles[pgMovie.Key] = pgMovie
	f.srv.testAuth().users["kid"] = core.User{ID: 31, Username: "kid", MaxContentRating: "PG"}

	batchKeys := strings.Join([]string{
		fightClub.Key.String(),
		breakingBad.Key.String(),
		bbS1.Key.String(),
		bbE1.Key.String(),
		bbE2.Key.String(),
		core.MovieKey(999999).String(),
		pgMovie.Key.String(),
	}, ",")

	type item struct {
		RatingKey       string `json:"ratingKey"`
		Type            string `json:"type"`
		Title           string `json:"title"`
		LeafCount       *int   `json:"leafCount"`
		ViewedLeafCount *int   `json:"viewedLeafCount"`
		ViewCount       int    `json:"viewCount"`
	}
	var batch struct {
		MediaContainer struct {
			Size     int    `json:"size"`
			Metadata []item `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	const clientID = "perf-598-batch-parity"
	rec := f.get(t, "/library/metadata/"+batchKeys+"?includeGuids=1&includeExtras=1&includeOnDeck=1&includeRelated=1",
		"Accept", "application/json",
		"X-Plex-Client-Identifier", clientID,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	wantOrder := []core.RatingKey{fightClub.Key, breakingBad.Key, bbS1.Key, bbE1.Key, bbE2.Key, pgMovie.Key}
	if len(batch.MediaContainer.Metadata) != len(wantOrder) {
		t.Fatalf("got %d items, want %d: %+v", len(batch.MediaContainer.Metadata), len(wantOrder), batch.MediaContainer.Metadata)
	}
	for i, wantKey := range wantOrder {
		got := batch.MediaContainer.Metadata[i]
		if got.RatingKey != wantKey.String() {
			t.Fatalf("item[%d] ratingKey=%s, want %s", i, got.RatingKey, wantKey.String())
		}
		singleRec := f.get(t, "/library/metadata/"+wantKey.String()+"?includeGuids=1",
			"Accept", "application/json",
			"X-Plex-Client-Identifier", clientID,
		)
		if singleRec.Code != http.StatusOK {
			t.Fatalf("single %s status=%d", wantKey, singleRec.Code)
		}
		var single struct {
			MediaContainer struct {
				Metadata []item `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(singleRec.Body.Bytes(), &single); err != nil || len(single.MediaContainer.Metadata) != 1 {
			t.Fatalf("single %s invalid response: %v", wantKey, err)
		}
		want := single.MediaContainer.Metadata[0]
		if got.Type != want.Type || got.Title != want.Title || got.ViewCount != want.ViewCount {
			t.Fatalf("item %s mismatch: batch=%+v single=%+v", wantKey, got, want)
		}
		if (got.LeafCount == nil) != (want.LeafCount == nil) || (got.LeafCount != nil && *got.LeafCount != *want.LeafCount) {
			t.Fatalf("item %s leafCount mismatch: batch=%v single=%v", wantKey, got.LeafCount, want.LeafCount)
		}
		if (got.ViewedLeafCount == nil) != (want.ViewedLeafCount == nil) || (got.ViewedLeafCount != nil && *got.ViewedLeafCount != *want.ViewedLeafCount) {
			t.Fatalf("item %s viewedLeafCount mismatch: batch=%v single=%v", wantKey, got.ViewedLeafCount, want.ViewedLeafCount)
		}
	}
	// Specifically confirm breakingBad and bbS1 report 1 viewed out of 2 aired episodes.
	for _, idx := range []int{1, 2} {
		got := batch.MediaContainer.Metadata[idx]
		if got.LeafCount == nil || *got.LeafCount != 2 || got.ViewedLeafCount == nil || *got.ViewedLeafCount != 1 {
			t.Fatalf("item[%d] (%s) leaf/viewed=%v/%v, want 2/1", idx, got.RatingKey, got.LeafCount, got.ViewedLeafCount)
		}
	}

	// Restricted viewer receives only the PG title from the mixed batch.
	kidRec := f.request(t, http.MethodGet, "/library/metadata/"+batchKeys, map[string]string{
		"X-Plex-Token":             "kid",
		"X-Plex-Client-Identifier": clientID,
		"Accept":                   "application/json",
	})
	if kidRec.Code != http.StatusOK {
		t.Fatalf("kid batch status=%d body=%s", kidRec.Code, kidRec.Body.String())
	}
	var kidBatch struct {
		MediaContainer struct {
			Metadata []item `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(kidRec.Body.Bytes(), &kidBatch); err != nil {
		t.Fatal(err)
	}
	if len(kidBatch.MediaContainer.Metadata) != 1 || kidBatch.MediaContainer.Metadata[0].RatingKey != pgMovie.Key.String() {
		t.Fatalf("restricted batch leaked or dropped items: %+v", kidBatch.MediaContainer.Metadata)
	}
}
