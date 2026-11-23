package resolver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/episodeorder"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/scenenumbering"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestAutomaticReleaseNumbering(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("origin") != "tvdb" {
			t.Error("wrong XEM origin")
		}
		switch r.URL.Query().Get("id") {
		case "777":
			// Real Inuyasha shape: scene stays absolute; TVDB splits seasons.
			_, _ = w.Write([]byte(`{"result":"success","data":[{"tvdb":{"season":2,"episode":1,"absolute":28},"scene":{"season":1,"episode":28,"absolute":28}}]}`))
		case "889":
			_, _ = w.Write([]byte(`{"result":"success","data":[{"tvdb":{"season":2,"episode":1,"absolute":28},"scene":{"season":1,"episode":28}},{"tvdb":{"season":3,"episode":1,"absolute":40},"scene":{"season":2,"episode":1}}]}`))
		case "890":
			_, _ = w.Write([]byte(`{"result":"success","data":[{"tvdb":{"season":2,"episode":1,"absolute":28},"scene":{"season":1,"episode":29}}]}`))
		case "891":
			_, _ = w.Write([]byte(`{"result":"success","data":[{"tvdb":{"season":2,"episode":1,"absolute":28},"scene":{"season":3,"episode":2}}]}`))
		case "892":
			_, _ = w.Write([]byte(`{"result":"success","data":[{"tvdb":{"season":2,"episode":1,"absolute":28},"scene":{"season":2,"episode":2}}]}`))
		default:
			_, _ = w.Write([]byte(`{"result":"failure","data":[],"message":"No mapping available"}`))
		}
	}))
	defer server.Close()
	scene := scenenumbering.New(scenenumbering.Options{URL: server.URL, Client: server.Client()})
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	orders := episodeorder.New(db, nil, nil)
	indexer := &fakeIndexer{}
	cat, err := catalog.New(db, nil, testCfg().Catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,show_key,season,child_count) VALUES(?,'season',?,1,60)`, core.SeasonKey(10, 1), core.ShowKey(10)); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 60; n++ {
		if _, err = db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,show_key,season,episode) VALUES(?,'episode',?,1,?)`, core.EpisodeKey(10, 1, n), core.ShowKey(10), n); err != nil {
			t.Fatal(err)
		}
	}
	canonicalLayout := func() (core.EpisodeLayout, error) { return cat.ReleaseEpisodeLayout(ctx, core.ShowKey(10)) }

	svc := New(indexer, nil, db, testCfg(), Options{EpisodeNumbering: func(ctx context.Context, req core.SearchRequest, cachedOnly bool) (core.ReleaseNumbering, error) {
		p, err := orders.ReleasePreference(ctx, core.ShowKey(req.IDs.TMDB))
		if err != nil || p.Mode == "tmdb" {
			return core.ReleaseNumbering{}, err
		}
		if cachedOnly {
			return scene.LookupCached(req, canonicalLayout)
		}
		return scene.Lookup(ctx, req, canonicalLayout)
	}})
	defer svc.Close()
	title := &core.Title{Key: core.EpisodeKey(10, 1, 28), Type: core.TypeEpisode, ShowKey: core.ShowKey(10), ShowTitle: "Show", Season: 1, Episode: 28, ShowIDs: core.IDs{TVDB: 777}}
	// Display preferences never select release numbering.
	if _, err := db.ExecContext(ctx, `INSERT INTO episodeorder_preferences(user_id,show_key,group_id) VALUES(0,?,'display-only')`, int64(title.ShowKey)); err != nil {
		t.Fatal(err)
	}
	canonical := releaseparse.Parse("Show S01E28 1080p")
	alternate := releaseparse.Parse("Show S02E01 1080p")
	// Canonical results settle discovery without loading XEM.
	indexer.releases = []core.Release{{ID: "canonical", Name: "Show S01E28 1080p"}}
	req := svc.searchRequest(ctx, title)
	if _, _, err := svc.searchWithNumbering(ctx, &req, true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("canonical discovery fetched XEM")
	}
	// A returned alternate result can be matched without another indexer call.
	indexer.releases = []core.Release{{ID: "alternate", Name: "Show S02E01 1080p"}}
	req = svc.searchRequest(ctx, title)
	before := indexer.searches.Load()
	if _, _, err := svc.searchWithNumbering(ctx, &req, true); err != nil {
		t.Fatal(err)
	}
	if req.AltSeason != 2 || req.AltEpisode != 1 || !episodeMatchesRequest(alternate, req) || !episodeMatchesRequest(canonical, req) || !episodeMatchesRequest(releaseparse.Parse("Show - 28 1080p"), req) {
		t.Fatalf("lost episode identity: %+v", req)
	}
	if indexer.searches.Load() != before+1 {
		t.Fatal("matching spent another indexer call")
	}
	pack := core.Release{ID: "pack", Protocol: core.ProtocolTorrent, Info: releaseparse.Parse("Show S02 Complete 1080p")}
	if !packCoversSeasonCour(pack.Info, req) {
		t.Fatal("mapped pack rejected")
	}
	target := withTarget(pack, req).Target
	if target == nil || target.Season != 2 || target.Episode != 1 || target.CanonicalSeason != 1 || target.CanonicalEpisode != 28 {
		t.Fatalf("wrong pack target: %+v", target)
	}
	// A different mapping enables alternate discovery only when needed.
	indexer.releases = nil
	req = svc.searchRequest(ctx, title)
	before = indexer.searches.Load()
	if _, _, err := svc.searchWithNumbering(ctx, &req, true); err != nil {
		t.Fatal(err)
	}
	if req.AltEpisode != 1 || indexer.searches.Load() != before+2 {
		t.Fatal("mapped episode did not enable alternate search")
	}
	indexer.releases = []core.Release{{ID: "alternate", Name: "Show S02E01 1080p"}}
	// No entry means no alternate search; the negative answer is cached.
	title.ShowIDs.TVDB = 888
	for range 2 {
		req = svc.searchRequest(ctx, title)
		before = indexer.searches.Load()
		if _, _, err := svc.searchWithNumbering(ctx, &req, true); err != nil {
			t.Fatal(err)
		}
		if req.AltEpisode != 0 || indexer.searches.Load() != before+1 {
			t.Fatal("no entry caused alternate search")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("per-show cache not reused: %d", calls.Load())
	}
	title.ShowIDs.TVDB = 777
	if err := orders.SaveReleasePreference(ctx, title.ShowKey, episodeorder.ReleasePreference{Mode: "tmdb"}); err != nil {
		t.Fatal(err)
	}
	req = svc.searchRequest(ctx, title)
	if _, _, err := svc.searchWithNumbering(ctx, &req, true); err != nil {
		t.Fatal(err)
	}
	if req.AltEpisode != 0 || calls.Load() != 2 {
		t.Fatal("TMDB override did not disable mapping")
	}
	// Shows without TVDB ids and detail/background requests never fetch XEM.
	if err := orders.SaveReleasePreference(ctx, title.ShowKey, episodeorder.ReleasePreference{Mode: "automatic"}); err != nil {
		t.Fatal(err)
	}
	title.ShowIDs.TVDB = 0
	req = svc.searchRequest(ctx, title)
	_, _, _ = svc.searchWithNumbering(ctx, &req, true)
	title.ShowIDs.TVDB = 999
	req = svc.searchRequest(ctx, title)
	_, _, _ = svc.searchWithNumbering(core.WithIndexerPurpose(ctx, core.IndexerPurposeDetail), &req, true)
	if calls.Load() != 2 {
		t.Fatal("detail or id-less request fetched XEM")
	}
	// A shared SxxEyy belonging to different absolute episodes is never an alias.
	title.ShowIDs.TVDB = 889
	req = svc.numberedRequest(ctx, svc.searchRequest(ctx, title), false)
	if len(core.EpisodeAliases(req)) != 0 || episodeMatchesRequest(alternate, req) {
		t.Fatal("cross-numbering collision accepted")
	}
	// Canonical wins when scene calls absolute 28 by canonical episode 29's name.
	title.ShowIDs.TVDB = 890
	req = svc.numberedRequest(ctx, svc.searchRequest(ctx, title), false)
	if episodeMatchesRequest(releaseparse.Parse("Show S01E29 1080p"), req) || !episodeMatchesRequest(alternate, req) {
		t.Fatal("canonical collision did not drop just the conflicting alias")
	}
	if !episodeMatchesRequest(canonical, req) {
		t.Fatal("alias conflict blocked authoritative canonical match")
	}
	// Both distinct safe aliases match, and each distinct season pack selects its own file.
	title.ShowIDs.TVDB = 891
	req = svc.numberedRequest(ctx, svc.searchRequest(ctx, title), false)
	aliases := core.EpisodeAliases(req)
	if len(aliases) != 2 || !episodeMatchesRequest(releaseparse.Parse("Show S03E02 1080p"), req) || !episodeMatchesRequest(alternate, req) {
		t.Fatalf("lost one alias: %+v", aliases)
	}
	for _, n := range aliases {
		info := core.ReleaseInfo{FullSeason: true, Seasons: []int{n.Season}}
		if season, episode, ok := packNumbers(info, req); !ok || season != n.Season || episode != n.Episode {
			t.Fatalf("wrong alias pack file: %d %d %v", season, episode, ok)
		}
	}
	if _, _, ok := packNumbers(core.ReleaseInfo{FullSeason: true, Seasons: []int{2, 3}}, req); ok {
		t.Fatal("multi-season pack selected ambiguous file")
	}
	query := req
	query.ReleaseAliasesOnly = true
	if len(core.EpisodeAliases(query)) != 2 {
		t.Fatal("alias-only search lost a distinct form")
	}
	// Same-season numberings that disagree on file identities cannot select a pack.
	title.ShowIDs.TVDB = 892
	req = svc.numberedRequest(ctx, svc.searchRequest(ctx, title), false)
	if _, _, ok := packNumbers(core.ReleaseInfo{FullSeason: true, Seasons: []int{2}}, req); ok {
		t.Fatal("conflicting same-season pack accepted")
	}
}
