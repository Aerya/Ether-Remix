package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/overlays"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/ratings"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestPlexSectionAllQueryBudget guards the query ceiling of
// GET /library/sections/{1,2}/all?X-Plex-Container-Size=50&X-Plex-Container-Start=0&includeCollections=1&includeMeta=1
// on a seeded library (~20k titles, ~100 collections including rotating + personal
// watch rules, playable mode with retained releases, discovery overlays, external
// ratings, watch state, and playback resume/probes).
func TestPlexSectionAllQueryBudget(t *testing.T) {
	a := setupTestAppConfigured(t, true, Options{}, func(c *config.Config) {
		c.Availability.Mode = "playable"
		c.Usenet.Providers = []config.UsenetProvider{{Host: "fake.invalid", Port: 563, Enabled: true}}
		c.Indexers = []config.Indexer{{ID: "nzb1", Name: "FakeNZB", Protocol: "usenet", URL: "http://127.0.0.1:1", APIKey: "x", Enabled: true}}
	})
	ctx := t.Context()

	if err := a.users.SetOwner(ctx, plextv.Account{ID: 99, Username: "owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	yr := now.Year()
	oldRel := now.Add(-120 * 24 * time.Hour).Unix()
	recentRel := now.Add(-5 * 24 * time.Hour).Unix()

	const (
		movieCount = 10000
		showCount  = 10000
	)

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Seed 10,000 movies and 10,000 shows (~20k browsable titles) plus seasons
	// and recent aired episodes for the top shows. First 40 movies/shows sort
	// at the top ("AAA ...") alongside 10 inline collections ("AAB Collection ...")
	// so page 1 (size=50) renders both 40 media items and 10 collection rows.
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE seq(n) AS (
	SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<?
)
INSERT INTO catalog_titles(key,type,tmdb_id,title,sort_title,year,released_ts,added_at,fetched_at,detail,detail_at,browsable,adult,popularity,rating,vote_count,runtime_sec,content_rating,poster,backdrop,logo,language)
SELECT ?+n,'movie',n,
	CASE WHEN n<=40 THEN printf('AAA Movie %05d',n) ELSE printf('Movie %05d',n) END,
	CASE WHEN n<=40 THEN printf('aaa movie %05d',n) ELSE printf('movie %05d',n) END,
	?, ?,?,?,1,?,1,0,20000-n,7.5,500,7200,'PG-13','/poster.jpg','/backdrop.jpg','/logo.png','en'
FROM seq`,
		movieCount, int64(core.MovieKey(1))-1, yr, oldRel, now.Unix(), now.Unix(), now.Unix()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE seq(n) AS (
	SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<?
)
INSERT INTO catalog_titles(key,type,tmdb_id,title,sort_title,year,released_ts,added_at,fetched_at,detail,detail_at,browsable,adult,popularity,rating,vote_count,runtime_sec,content_rating,child_count,leaf_count,poster,backdrop,logo,language)
SELECT ?+n,'show',n,
	CASE WHEN n<=40 THEN printf('AAA Series %05d',n) ELSE printf('Series %05d',n) END,
	CASE WHEN n<=40 THEN printf('aaa series %05d',n) ELSE printf('series %05d',n) END,
	?, CASE WHEN n<=1500 THEN ? ELSE ? END,?,?,1,?,1,0,20000-n,8.1,800,2700,'TV-MA',1,4,'/poster.jpg','/backdrop.jpg','/logo.png','en'
FROM seq`,
		showCount, int64(core.ShowKey(1))-1, yr, recentRel, oldRel, now.Unix(), now.Unix(), now.Unix()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	// Seasons & episodes for the first 1500 shows so recent-episode badges, aired
	// counts, leaf counts, and playable-episode seeding all run against real rows.
	for id := int64(1); id <= 1500; id++ {
		showKey := int64(core.ShowKey(id))
		seasonKey := int64(core.SeasonKey(id, 1))
		showTitle := fmt.Sprintf("Series %05d", id)
		if id <= 40 {
			showTitle = fmt.Sprintf("AAA Series %05d", id)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,tmdb_id,show_key,show_tmdb,season,title,sort_title,year,released_ts,added_at,fetched_at,detail,detail_at,browsable,adult,child_count,language) VALUES(?,'season',?,?,?,1,'Season 1','season 01',?,?,?,?,1,?,1,0,2,'en')`,
			seasonKey, id, showKey, id, yr, recentRel, now.Unix(), now.Unix(), now.Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		for epNum := 1; epNum <= 2; epNum++ {
			epKey := int64(core.EpisodeKey(id, 1, epNum))
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,tmdb_id,show_key,show_tmdb,season,episode,title,sort_title,show_title,year,released_ts,added_at,fetched_at,detail,detail_at,browsable,adult,runtime_sec,still,language) VALUES(?,'episode',?,?,?,1,?,printf('Episode %d',?),printf('episode %02d',?),?,?,?,?,?,1,?,1,0,2700,'/still.jpg','en')`,
				epKey, id, showKey, id, epNum, epNum, epNum, showTitle, yr, recentRel, now.Unix(), now.Unix(), now.Unix()); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
	}

	// Retained playable observations (~3,000 rows across movies and S01E01 premieres)
	// with availability_gate_policy NOT yet seeded, reproducing upgrade / policy-change first browse.
	for i := int64(1); i <= 1500; i++ {
		mKey := core.MovieKey(i)
		mTitle := fmt.Sprintf("Movie %05d", i)
		mName := fmt.Sprintf("Movie.%05d.%d.2160p.WEB-DL.DDP5.1.H.265", i, yr)
		if i <= 40 {
			mTitle = fmt.Sprintf("AAA Movie %05d", i)
			mName = fmt.Sprintf("AAA.Movie.%05d.%d.2160p.WEB-DL.DDP5.1.H.265", i, yr)
		}
		mPayload := fmt.Sprintf(`{"ID":"rel-m-%d","Name":%q,"Protocol":"usenet","Size":8000000000}`, i, mName)
		if _, err := tx.ExecContext(ctx, `INSERT INTO availability_releases(id,rating_key,source_id,generation,payload,title,name,protocol,first_seen,last_seen,first_verified,verified_at,state,revision) VALUES(?,?,'probe','',?,?,?,'usenet',?,?,?,?, 'verified',0)`,
			fmt.Sprintf("rel-m-%d", i), int64(mKey), mPayload, mTitle, mName, recentRel, now.Unix(), recentRel, now.Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		eKey := core.EpisodeKey(i, 1, 1)
		sTitle := fmt.Sprintf("Series %05d", i)
		eName := fmt.Sprintf("Series.%05d.S01E01.2160p.WEB-DL.DDP5.1.H.265", i)
		if i <= 40 {
			sTitle = fmt.Sprintf("AAA Series %05d", i)
			eName = fmt.Sprintf("AAA.Series.%05d.S01E01.2160p.WEB-DL.DDP5.1.H.265", i)
		}
		ePayload := fmt.Sprintf(`{"ID":"rel-e-%d","Name":%q,"Protocol":"usenet","Size":3000000000}`, i, eName)
		if _, err := tx.ExecContext(ctx, `INSERT INTO availability_releases(id,rating_key,source_id,generation,payload,title,name,protocol,first_seen,last_seen,first_verified,verified_at,state,revision) VALUES(?,?,'probe','',?,?,?,'usenet',?,?,?,?, 'verified',0)`,
			fmt.Sprintf("rel-e-%d", i), int64(eKey), ePayload, sTitle, eName, recentRel, now.Unix(), recentRel, now.Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}

	// Seed watch state, external ratings cache, and artwork variants across listed items.
	badgesJSON := `[{"source":"imdb","value":8.2,"scale":10},{"source":"popcorn","value":88,"scale":100}]`
	artJSON := `{"posters":[{"path":"/alt-poster.jpg","language":"en","tags":["drama"]}],"backdrops":[{"path":"/alt-backdrop.jpg","tags":["drama"]}]}`
	for i := int64(1); i <= 100; i++ {
		mk := int64(core.MovieKey(i))
		sk := int64(core.ShowKey(i))
		if _, err := tx.ExecContext(ctx, `INSERT INTO ratings_cache(item_key,provider,value,updated) VALUES(?,'mdblist',?,?),(?,'mdblist',?,?)`,
			mk, badgesJSON, now.Unix(), sk, badgesJSON, now.Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO overlays_artwork(item_key,value) VALUES(?,?),(?,?)`,
			mk, artJSON, sk, artJSON); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if i <= 20 {
			ek := int64(core.EpisodeKey(i, 1, 1))
			tsNano := now.UnixNano()
			if _, err := tx.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at,updated_at) VALUES(?,?,1,?,?),(?,?,1,?,?)`,
				owner.ID, mk, tsNano, tsNano, owner.ID, ek, tsNano, tsNano); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Seed 20 playback resume + versioned probe + track preference records across
	// the first-page movies: 1..10 via Remember (live in-memory resume + DB probe)
	// and 11..20 directly in SQLite + on-disk NZB (simulating post-restart cold DB rows).
	probeJSON := fmt.Sprintf(`{"factsVersion":%d,"info":{"size":8000000000,"duration":7200000000000,"streams":[{"index":0,"type":"video","codec":"hevc","width":3840,"height":2160,"hdr":true,"dolbyVision":true},{"index":1,"type":"audio","codec":"eac3","channels":6,"language":"eng","atmos":true}]}}`, core.ProbeFactsVersion)
	nzbDir := filepath.Join(a.opts.DataDir, "playback", "nzb")
	if err := os.MkdirAll(nzbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var storedProfile string
	for i := int64(1); i <= 20; i++ {
		mKey := core.MovieKey(i)
		mName := fmt.Sprintf("AAA.Movie.%05d.%d.2160p.WEB-DL.DDP5.1.H.265", i, yr)
		relID := fmt.Sprintf("rel-m-%d", i)
		nzb := []byte(fmt.Sprintf("<nzb><file subject=%q/></nzb>", mName))
		sum := sha256.Sum256(nzb)
		hash := hex.EncodeToString(sum[:])
		if i <= 10 {
			if err := a.playback.Remember(ctx, owner, mKey, core.ClientCaps{}, &core.Resolution{
				Release: core.Release{ID: relID, Name: mName, Protocol: "usenet", Size: 8000000000},
				NZB:     nzb,
			}); err != nil {
				t.Fatal(err)
			}
			if storedProfile == "" {
				if err := a.db.QueryRowContext(ctx, `SELECT profile FROM playback_resume WHERE user_id=? AND rating_key=?`, owner.ID, int64(mKey)).Scan(&storedProfile); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.db.ExecContext(ctx, `UPDATE playback_resume SET size=8000000000, duration_ms=7200000 WHERE user_id=? AND rating_key=?`, owner.ID, int64(mKey)); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(filepath.Join(nzbDir, hash+".nzb"), nzb, 0o600); err != nil {
				t.Fatal(err)
			}
			relJSON := fmt.Sprintf(`{"ID":%q,"Name":%q,"Protocol":"usenet","Size":8000000000}`, relID, mName)
			if _, err := a.db.ExecContext(ctx, `INSERT INTO playback_resume(user_id,rating_key,release_id,release_json,nzb_hash,profile,original_language,size,duration_ms,updated_at) VALUES(?,?,?,?,?,?,'en',8000000000,7200000,?)`,
				owner.ID, int64(mKey), relID, relJSON, hash, storedProfile, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.db.ExecContext(ctx, `INSERT INTO playback_probe(user_id,rating_key,release_id,nzb_hash,size,info_json,updated_at) VALUES(?,?,?,?,8000000000,?,?)`,
			owner.ID, int64(mKey), relID, hash, probeJSON, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if err := a.trackprefs.Remember(ctx, owner.ID, mKey, "audio", &core.StreamInfo{
			Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Warm the collection engine's compact catalogue index once (via Preview on a
	// local rule) BEFORE inserting 100 built collections so s.idx.current() is
	// populated while all 100 collections' member sets start cold in s.members.
	if _, err := a.collections.Preview(ctx, core.CollectionSpec{
		Name: "warm", MediaType: "movie", LocalOnly: true, Limit: 1,
	}, 1); err != nil {
		t.Fatal(err)
	}

	tx, err = a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Seed 100 collections:
	// - 1..50: inline CollectionsTab collections over ids 41..200 (10 sort as
	//   "AAB Collection ..." on page 1 alongside uncollapsed AAA Movie/Series 1..40).
	// - 51..100: rotating HomeRow/SectionRow collections with unwatched rules over
	//   ids 1..60 so LastChance evaluates all 50 rotating collections against
	//   the 40 uncollapsed page-1 items without collapsing them out of the grid.
	for cID := int64(1); cID <= 100; cID++ {
		mediaType := core.TypeMovie
		if cID%2 == 0 {
			mediaType = core.TypeShow
		}
		name := fmt.Sprintf("ZZZ Collection %03d", cID)
		if cID <= 20 {
			name = fmt.Sprintf("AAB Collection %03d", cID)
		}
		targets := core.CollectionTargets{CollectionsTab: true, HomeRow: true, SectionRow: true}
		if cID > 50 {
			targets = core.CollectionTargets{CollectionsTab: false, HomeRow: true, SectionRow: true}
		}
		spec := core.CollectionSpec{
			Name:      name,
			MediaType: string(mediaType),
			Sort:      "popularity",
			SortDesc:  true,
			Enabled:   true,
			Targets:   targets,
		}
		if cID > 50 {
			spec.Rotate = &core.CollectionRotate{Size: 10, Every: "daily"}
			spec.Rules = []core.CollectionRule{{Field: "watched", Op: "is", Value: json.RawMessage(`false`)}}
		}
		rawSpec, err := json.Marshal(spec)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO collections_specs(id,slug,builtin,owner_user_id,spec_json,enabled,created_at,updated_at,last_built_at,item_count,warnings_json,build_hash,changed_at) VALUES(?,'',0,?,?,1,?,?,?,25,'[]',?,?)`,
			cID, owner.ID, string(rawSpec), now.Unix(), now.Unix(), now.Unix(), fmt.Sprintf("h-%d", cID), now.Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		for pos := 0; pos < 25; pos++ {
			var id int64
			if cID <= 50 {
				id = 41 + (cID*3+int64(pos))%160
			} else {
				id = (cID*3+int64(pos))%60 + 1
			}
			key := core.MovieKey(id)
			if mediaType == core.TypeShow {
				key = core.ShowKey(id)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO collections_members(spec_id,pos,key,shared) VALUES(?,?,?,1)`,
				cID, pos, int64(key)); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Enable discovery ratings and poster overlays (status + quality + ratings + personalize).
	if err := a.ratings.SaveSettings(ctx, ratings.Settings{
		Enabled:    true,
		Provider:   "mdblist",
		APIKey:     "offline-key",
		Critic:     "imdb",
		Audience:   "popcorn",
		Missing:    "tmdb",
		CacheHours: 24,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.overlays.SaveSettings(ctx, overlays.Settings{
		Enabled:     true,
		Status:      true,
		Quality:     true,
		Ratings:     true,
		Details:     true,
		Personalize: true,
	}); err != nil {
		t.Fatal(err)
	}

	doSectionAll := func(sectionID int, label string) *store.QueryStats {
		t.Helper()
		path := fmt.Sprintf("/library/sections/%d/all?X-Plex-Container-Size=50&X-Plex-Container-Start=0&includeCollections=1&includeMeta=1", sectionID)
		mctx, stats := store.WithQueryStats(reqmemo.With(ctx))
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil).WithContext(mctx)
		req.RemoteAddr = "127.0.0.1:42000"
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Token", "test-owner")
		req.Header.Set("X-Plex-Client-Identifier", "plex-web-budget")
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s (%s): status=%d body=%s", label, path, w.Code, w.Body.String())
		}
		var resp struct {
			MediaContainer struct {
				Size     int               `json:"size"`
				Metadata []json.RawMessage `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s decode: %v", label, err)
		}
		if len(resp.MediaContainer.Metadata) != 50 {
			t.Fatalf("%s: got %d items, want 50", label, len(resp.MediaContainer.Metadata))
		}
		stmts := stats.Statements()
		sort.Slice(stmts, func(i, j int) bool { return stmts[i].Count > stmts[j].Count })
		t.Logf("%s: queries=%d writes=%d rows=%d", label, stats.Queries(), stats.Writes(), stats.Rows())
		return stats
	}

	const queryCeiling = 180
	for _, call := range []struct {
		sec   int
		label string
	}{
		{1, "cold-movies-sec1"},
		{2, "cold-shows-sec2"},
		{1, "steady-movies-sec1"},
		{2, "steady-shows-sec2"},
	} {
		if stats := doSectionAll(call.sec, call.label); stats.Queries() > queryCeiling {
			t.Errorf("%s executed %d SQL statements (want <= %d)", call.label, stats.Queries(), queryCeiling)
		}
	}
}
