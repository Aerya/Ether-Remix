package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extras"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plexguid"
	"github.com/braedonsaunders/ether/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type detailWarmer struct {
	core.Playback
	calls int
}

func (w *detailWarmer) Prewarm(context.Context, *core.User, core.RatingKey, core.ClientCaps, core.PrewarmOptions) bool {
	w.calls++
	return true
}

func TestBulkMetadataUsesCachedGUIDsWithoutNetwork(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?),(?,?,?,?,?)`, int64(fightClub.Key), "movie", 550, "Fjord Harbor", 1999, int64(breakingBad.Key), "show", 1396, "Boundary Bay", 2008); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"plex://movie/5d7768265af944001f1f6976","type":"movie","title":"Fjord Harbor","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`)
	}))
	defer upstream.Close()
	f := newFixture()
	defer f.srv.Close()
	warmer := &detailWarmer{Playback: f.srv.playback()}
	f.srv.setPlayback(warmer)
	f.srv.setPlexGUID(plexguid.New(db, plexguid.Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }}))
	rec := f.get(t, "/library/metadata/"+fightClub.Key.String()+","+alien.Key.String())
	if rec.Code != 200 || (calls.Load() != 0 || warmer.calls != 0) {
		t.Fatalf("bulk status=%d network=%d", rec.Code, calls.Load())
	}
	rec = f.get(t, "/library/metadata/"+fightClub.Key.String())
	if rec.Code != 200 || (calls.Load() != 0 || warmer.calls != 1) {
		t.Fatalf("detail status=%d network=%d", rec.Code, calls.Load())
	}
}

func TestHubsWarmPlexGUIDsInBackgroundAndEpisodesRenderHierarchy(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?),(?,?,?,?,?)`, int64(fightClub.Key), "movie", 550, "Fjord Harbor", 1999, int64(breakingBad.Key), "show", 1396, "Boundary Bay", 2008); err != nil {
		t.Fatal(err)
	}

	const (
		fcGUID     = "plex://movie/5d7768265af944001f1f6976"
		showGUID   = "plex://show/5d9c086c46115600200aa2fe"
		seasonGUID = "plex://season/602e5e2194f08e002d001122"
		ep1GUID    = "plex://episode/602e5e2294f08e002d003344"
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/matches":
			q := r.URL.Query()
			switch {
			case q.Get("type") == "1" && q.Get("guid") == "tmdb://550":
				io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+fcGUID+`","type":"movie","title":"Fjord Harbor","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`)
				return
			case q.Get("type") == "2" && q.Get("guid") == "tmdb://1396":
				io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+showGUID+`","type":"show","title":"Boundary Bay","year":2008,"Guid":[{"id":"tmdb://1396"}]}]}}`)
				return
			case q.Get("type") == "4" && q.Get("guid") == "tmdb://1396" && q.Get("parentIndex") == "1" && q.Get("index") == "1":
				io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+ep1GUID+`","type":"episode","index":1,"parentIndex":1,"parentGuid":"`+seasonGUID+`","grandparentGuid":"`+showGUID+`","Guid":[{"id":"tmdb://1396/1/1"}]}]}}`)
				return
			}
			io.WriteString(w, `{"MediaContainer":{"Metadata":[]}}`)
		case "/library/metadata/5d7768265af944001f1f6976":
			io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+fcGUID+`","type":"movie","title":"Fjord Harbor","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`)
		case "/library/metadata/5d9c086c46115600200aa2fe":
			io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+showGUID+`","type":"show","title":"Boundary Bay","year":2008,"Guid":[{"id":"tmdb://1396"}]}]}}`)
		case "/library/metadata/5d9c086c46115600200aa2fe/children":
			io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+seasonGUID+`","type":"season","index":1}]}}`)
		case "/library/metadata/602e5e2194f08e002d001122/children":
			io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+ep1GUID+`","type":"episode","index":1,"parentIndex":1}]}}`)

		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	f := newFixture()
	defer f.srv.Close()
	pg := plexguid.New(db, plexguid.Options{
		BaseURL:    upstream.URL,
		Token:      func() string { return "fixture" },
		Background: true,
	})
	defer pg.Close()
	f.srv.setPlexGUID(pg)

	// First /hubs render schedules background resolution for hub items.
	rec := f.get(t, "/hubs")
	if rec.Code != 200 {
		t.Fatalf("first /hubs status=%d", rec.Code)
	}
	f.srv.waitBackground()

	// Second /hubs render serves the resolved plex://movie/... GUID on the hub tile.
	rec = f.get(t, "/hubs")
	if rec.Code != 200 || !containsSubstr(rec.Body.String(), fcGUID) {
		t.Fatalf("expected %s in /hubs after background warm, got: %s", fcGUID, rec.Body.String())
	}

	// A detail open schedules episode + parent season + grandparent identities.
	// Later renders use the verified cache, without making Play wait for it.
	rec = f.get(t, "/library/metadata/"+bbE1.Key.String())
	if rec.Code != 200 {
		t.Fatalf("first detail status=%d", rec.Code)
	}
	pg.Wait()
	rec = f.get(t, "/library/metadata/"+bbE1.Key.String())
	body := rec.Body.String()
	if rec.Code != 200 || !containsSubstr(body, ep1GUID) || !containsSubstr(body, seasonGUID) || !containsSubstr(body, showGUID) {
		t.Fatalf("expected episode/season/show plex:// GUIDs in episode detail (ep %v season %v show %v)", containsSubstr(body, ep1GUID), containsSubstr(body, seasonGUID), containsSubstr(body, showGUID))
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDetailDoesNotWaitForRemotePlexIdentity(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?),(?,?,?,?,?)`, int64(fightClub.Key), "movie", 550, "Fjord Harbor", 1999, int64(breakingBad.Key), "show", 1396, "Boundary Bay", 2008); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"MediaContainer":{"Metadata":[]}}`)
	}))
	defer upstream.Close()
	f := newFixture()
	defer f.srv.Close()
	pg := plexguid.New(db, plexguid.Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }, Background: true})
	defer pg.Close()
	// Release the fake upstream before Close joins its worker even if this fails.
	defer close(release)
	f.srv.setPlexGUID(pg)
	done := make(chan int, 1)
	go func() { done <- f.get(t, "/library/metadata/"+fightClub.Key.String()).Code }()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("detail HTTP %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("remote identity enrichment blocked the detail response")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("identity was not scheduled in background")
	}
}

type keyWarmer struct {
	core.Playback
	keys []core.RatingKey
}

func (w *keyWarmer) Prewarm(_ context.Context, _ *core.User, k core.RatingKey, _ core.ClientCaps, _ core.PrewarmOptions) bool {
	w.keys = append(w.keys, k)
	return true
}

func TestShowPageWarmsItsOnDeckEpisode(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	warmer := &keyWarmer{Playback: f.srv.playback()}
	f.srv.setPlayback(warmer)
	if rec := f.get(t, "/library/metadata/2000000000001396?includeChildren=1&includeOnDeck=1"); rec.Code != 200 {
		t.Fatalf("show detail: %d", rec.Code)
	}
	for _, k := range warmer.keys {
		if k.Episode() > 0 {
			return
		}
	}
	t.Fatalf("no episode warmed: %v", warmer.keys)
}

type testExtrasServiceAdapter struct{ s *extras.Service }

func (a testExtrasServiceAdapter) Enabled() bool { return a.s.Enabled() }
func (a testExtrasServiceAdapter) List(ctx context.Context, parent *core.Title, lang string) ([]frontend.ExtraClip, error) {
	list, err := a.s.List(ctx, parent, lang)
	if err != nil {
		return nil, err
	}
	out := make([]frontend.ExtraClip, 0, len(list))
	for _, e := range list {
		out = append(out, frontend.ExtraClip{
			Key: e.Key, ParentKey: e.ParentKey, ParentTitle: e.ParentTitle,
			Title: e.Title, Subtype: e.Subtype, ExtraType: e.ExtraType,
			Site: e.Site, SiteKey: e.SiteKey, DiscoverKey: e.DiscoverKey,
			Language: e.Language, Official: e.Official, PublishedAt: e.PublishedAt,
			Size: e.Size, Duration: e.Duration, Source: e.Source,
		})
	}
	return out, nil
}
func (a testExtrasServiceAdapter) PrewarmBytes() int64 { return a.s.PrewarmBytes() }

// A cold detail page open (?includeExtras=1) must return both the verified
// plex:// GUID (needed by Plex TV/mobile for Watchlist and Discover Cast) and
// the Discover trailer + primaryExtraKey on the first open, and persist the
// extras list durably across restarts.
func TestFirstOpenDetailReturnsPlexGUIDAndExtras(t *testing.T) {
	const (
		hexID  = "5d7768265af944001f1f6976"
		fcGUID = "plex://movie/" + hexID
	)
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?)`, int64(fightClub.Key), "movie", 550, "Fjord Harbor", 1999); err != nil {
		t.Fatal(err)
	}
	var extrasCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/matches", "/library/metadata/" + hexID:
			io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+fcGUID+`","type":"movie","title":"Fjord Harbor","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`)
		case "/library/metadata/" + hexID + "/extras":
			extrasCalls.Add(1)
			io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"ex1","title":"Official Trailer","subtype":"trailer","extraType":1,"Media":[{"container":"mp4","videoCodec":"h264","audioCodec":"aac","height":1080,"url":"https://cdn.example/trailer.mp4"}]}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	pg := plexguid.New(db, plexguid.Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }})
	defer pg.Close()

	newExtras := func() *extras.Service {
		discover := extras.NewPlexDiscover(extras.PlexDiscoverOptions{
			BaseURL: upstream.URL,
			HTTP:    upstream.Client(),
			Token:   func() string { return "fixture" },
			ResolveGUID: func(ctx context.Context, key core.RatingKey) (string, error) {
				if g := pg.Cached(ctx, key); g != "" {
					return g, nil
				}
				return pg.Resolve(ctx, key)
			},
		})
		return extras.New(extras.Options{
			DB:       db,
			Discover: discover,
			Config:   func() config.Extras { return config.Extras{Enabled: true} },
			LookPath: func(string) (string, error) { return "", io.EOF },
		})
	}

	ex1 := newExtras()
	defer ex1.Close()
	f := newFixture(func(d *testServerDeps) {
		d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
			return core.WithLocalResponse(ctx)
		}}
	})
	defer f.srv.Close()
	f.srv.setExtras(testExtrasServiceAdapter{ex1})
	f.srv.setPlexGUID(pg)

	rec := f.get(t, "/library/metadata/"+fightClub.Key.String()+"?includeExtras=1")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("first open HTTP %d: %s", rec.Code, body)
	}
	if !strings.Contains(body, `guid="`+fcGUID+`"`) {
		t.Fatalf("first open missing verified plex:// GUID: %s", body)
	}
	if !strings.Contains(body, `primaryExtraKey="/library/metadata/`) || !strings.Contains(body, `subtype="trailer"`) {
		t.Fatalf("first open missing trailer/primaryExtraKey: %s", body)
	}
	if extrasCalls.Load() != 1 {
		t.Fatalf("extras upstream calls = %d, want 1", extrasCalls.Load())
	}

	// Simulate restart with a fresh extras.Service on the same DB: no second upstream extras call.
	ex2 := newExtras()
	defer ex2.Close()
	f2 := newFixture(func(d *testServerDeps) {
		d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
			return core.WithLocalResponse(ctx)
		}}
	})
	defer f2.srv.Close()
	f2.srv.setExtras(testExtrasServiceAdapter{ex2})
	f2.srv.setPlexGUID(pg)

	rec2 := f2.get(t, "/library/metadata/"+fightClub.Key.String()+"?includeExtras=1")
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), `subtype="trailer"`) || extrasCalls.Load() != 1 {
		t.Fatalf("post-restart open HTTP %d calls=%d body=%s", rec2.Code, extrasCalls.Load(), rec2.Body.String())
	}
}
