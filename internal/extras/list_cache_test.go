package extras

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type flakyVideos struct {
	fakeVideos
	fail bool
}

func (f *flakyVideos) MovieVideos(context.Context, int64, string) ([]tmdb.Video, error) {
	if f.fail {
		return nil, errors.New("tmdb: HTTP 503")
	}
	return f.vs, nil
}

// A transient TMDB failure must not be cached as "no extras" for the TTL.
func TestListDoesNotCacheFetchFailure(t *testing.T) {
	v := &flakyVideos{fakeVideos: fakeVideos{[]tmdb.Video{{Site: "YouTube", Key: "trlOffEN001", Type: "Trailer", Official: true}}}, fail: true}
	s := New(Options{Videos: v, Config: enabledCfg("yt-dlp"), LookPath: lookOK, Now: func() time.Time { return testNow }})
	if got, _ := s.List(context.Background(), movie(), "en"); len(got) != 0 {
		t.Fatalf("failed fetch returned extras: %+v", got)
	}
	v.fail = false
	if got, _ := s.List(context.Background(), movie(), "en"); len(got) != 1 {
		t.Fatalf("recovered fetch still served cached failure: %+v", got)
	}
}

func TestListFollowsResolverAvailability(t *testing.T) {
	available := false
	v := &fakeVideos{[]tmdb.Video{{Site: "YouTube", Key: "trlOffEN001", Type: "Trailer", Official: true}}}
	s := New(Options{Videos: v, Config: enabledCfg("yt-dlp"), LookPath: func(name string) (string, error) {
		if !available {
			return "", errors.New("not installed")
		}
		return name, nil
	}})
	if got, _ := s.List(context.Background(), movie(), "en"); len(got) != 0 {
		t.Fatal("advertised unavailable resolver")
	}
	available = true
	if got, _ := s.List(context.Background(), movie(), "en"); len(got) != 1 {
		t.Fatalf("installation hidden by empty cache: %+v", got)
	}
	available = false
	if got, _ := s.List(context.Background(), movie(), "en"); len(got) != 0 {
		t.Fatal("advertised cached extras after resolver removal")
	}
}

type countingVideos struct {
	fakeVideos
	calls atomic.Int32
}

func (c *countingVideos) MovieVideos(ctx context.Context, id int64, lang string) ([]tmdb.Video, error) {
	c.calls.Add(1)
	return c.fakeVideos.MovieVideos(ctx, id, lang)
}

// Durable extras lists and clip keys survive service restarts for days,
// verified-empty lists use the shorter negative TTL, and Prune removes expired rows.
func TestListPersistsAcrossRestartAndPrunes(t *testing.T) {
	db := store.OpenTest(t)
	now := testNow
	clock := func() time.Time { return now }

	v := &countingVideos{fakeVideos: fakeVideos{[]tmdb.Video{{Site: "YouTube", Key: "trlOffEN001", Name: "Official Trailer", Type: "Trailer", Official: true, Size: 1080, ISO639: "en"}}}}
	s1 := New(Options{DB: db, Videos: v, Config: enabledCfg("yt-dlp"), LookPath: lookOK, Now: clock})
	got, err := s1.List(context.Background(), movie(), "en")
	if err != nil || len(got) != 1 {
		t.Fatalf("first list = %d, %v", len(got), err)
	}
	clipKey := got[0].Key
	s1.Close()

	// Five days later on a fresh Service (simulating restart): list and Lookup
	// both hit the durable DB without calling TMDB again.
	now = now.Add(5 * 24 * time.Hour)
	s2 := New(Options{DB: db, Videos: v, Config: enabledCfg("yt-dlp"), LookPath: lookOK, Now: clock})
	defer s2.Close()
	if looked, ok := s2.Lookup(clipKey); !ok || looked.SiteKey != "trlOffEN001" {
		t.Fatalf("Lookup across restart = %+v, %v", looked, ok)
	}
	local := core.WithColdDetail(core.WithLocalResponse(context.Background()), movie().Key)
	again, err := s2.List(local, movie(), "en")
	if err != nil || len(again) != 1 || again[0].Key != clipKey {
		t.Fatalf("persisted list = %+v, %v", again, err)
	}
	if v.calls.Load() != 1 {
		t.Fatalf("upstream called %d times, want 1", v.calls.Load())
	}

	// Past the 7-day TTL, Prune clears expired rows and the next fetch refreshes.
	now = testNow.Add(8 * 24 * time.Hour)
	pruned, err := s2.Prune(context.Background())
	if err != nil || pruned < 2 {
		t.Fatalf("Prune = %d, %v", pruned, err)
	}
	refreshed, err := s2.List(context.Background(), movie(), "en")
	if err != nil || len(refreshed) != 1 || v.calls.Load() != 2 {
		t.Fatalf("post-expiry list = %d calls=%d err=%v", len(refreshed), v.calls.Load(), err)
	}
}

type blockingVideos struct {
	fakeVideos
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (b *blockingVideos) MovieVideos(ctx context.Context, id int64, lang string) ([]tmdb.Video, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer close(b.finished)
	return b.fakeVideos.MovieVideos(ctx, id, lang)
}

// A cold single-item detail open waits briefly so fast lookups return trailers
// on the first open, while a slow lookup returns empty + pending without
// blocking past ColdWait and finishes populating the durable cache in background.
func TestColdDetailBoundedWaitAndFallback(t *testing.T) {
	db := store.OpenTest(t)
	v := &countingVideos{fakeVideos: fakeVideos{[]tmdb.Video{{Site: "YouTube", Key: "trlOffEN001", Name: "Official Trailer", Type: "Trailer", Official: true, ISO639: "en"}}}}
	s := New(Options{DB: db, Videos: v, Config: enabledCfg("yt-dlp"), LookPath: lookOK, Now: func() time.Time { return testNow }})
	defer s.Close()

	coldCtx, pending := core.WithDetailPending(core.WithColdDetail(core.WithLocalResponse(context.Background()), movie().Key), movie().Key)
	got, err := s.List(coldCtx, movie(), "en")
	if err != nil || len(got) != 1 || len(pending.Fields()) != 0 {
		t.Fatalf("fast first open = %d pending=%v err=%v", len(got), pending.Fields(), err)
	}

	// Slow lookup exceeds ColdWait, reports pending, and completes detached.
	bv := &blockingVideos{
		fakeVideos: fakeVideos{[]tmdb.Video{{Site: "YouTube", Key: "trlSlow0001", Name: "Slow Trailer", Type: "Trailer", Official: true, ISO639: "en"}}},
		started:    make(chan struct{}, 1),
		release:    make(chan struct{}),
		finished:   make(chan struct{}),
	}
	s2 := New(Options{DB: store.OpenTest(t), Videos: bv, Config: enabledCfg("yt-dlp"), LookPath: lookOK, ColdWait: 25 * time.Millisecond, Now: func() time.Time { return testNow }})
	defer s2.Close()

	slowCtx, slowPending := core.WithDetailPending(core.WithColdDetail(core.WithLocalResponse(context.Background()), movie().Key), movie().Key)
	first, err := s2.List(slowCtx, movie(), "en")
	if err != nil || len(first) != 0 {
		t.Fatalf("slow first open = %d, %v", len(first), err)
	}
	if fields := slowPending.Fields(); len(fields) != 1 || fields[0] != "extras" {
		t.Fatalf("expected extras pending, got %v", fields)
	}
	<-bv.started
	close(bv.release)
	<-bv.finished
	s2.listWG.Wait()

	second, err := s2.List(core.WithColdDetail(core.WithLocalResponse(context.Background()), movie().Key), movie(), "en")
	if err != nil || len(second) != 1 || second[0].SiteKey != "trlSlow0001" {
		t.Fatalf("second open after detached fill = %+v, %v", second, err)
	}
}

// A transient plexguid resolution error must not poison the durable extras cache.
func TestPlexDiscoverTransientGUIDErrorNotCached(t *testing.T) {
	db := store.OpenTest(t)
	const hexID = "5d7768265af944001f1f6976"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/"+hexID+"/extras" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"ex1","title":"Official Trailer","subtype":"trailer","extraType":1,"Media":[{"container":"mp4","videoCodec":"h264","audioCodec":"aac","height":1080,"url":"https://cdn.example/trailer.mp4"}]}]}}`))
	}))
	defer srv.Close()

	failGUID := true
	discover := NewPlexDiscover(PlexDiscoverOptions{
		BaseURL: srv.URL,
		Token:   func() string { return "tok" },
		ResolveGUID: func(context.Context, core.RatingKey) (string, error) {
			if failGUID {
				return "", errors.New("plex.tv timeout")
			}
			return "plex://movie/" + hexID, nil
		},
		Now: func() time.Time { return testNow },
	})
	s := New(Options{
		DB:       db,
		Discover: discover,
		Config:   func() config.Extras { return config.Extras{Enabled: true} },
		LookPath: func(string) (string, error) { return "", errors.New("no yt-dlp") },
		Now:      func() time.Time { return testNow },
	})
	defer s.Close()

	coldCtx := core.WithColdDetail(core.WithLocalResponse(context.Background()), movie().Key)
	if got, _ := s.List(coldCtx, movie(), "en"); len(got) != 0 {
		t.Fatalf("expected empty on transient GUID error, got %+v", got)
	}
	failGUID = false
	got, err := s.List(coldCtx, movie(), "en")
	if err != nil || len(got) != 1 || got[0].Source != SourcePlexDiscover {
		t.Fatalf("recovered discover list = %+v, %v", got, err)
	}
}
