package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/markers"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/store"
)

type surfaceSessionFinder struct {
	plain   *core.PlaySession
	surface map[string]*core.PlaySession
}

func (f surfaceSessionFinder) SessionFor(int64, string, core.RatingKey) (*core.PlaySession, bool) {
	if f.plain == nil {
		return nil, false
	}
	return f.plain, true
}

func (f surfaceSessionFinder) SessionForSurfaceClient(surface string, _ int64, _ string, _ core.RatingKey) (*core.PlaySession, bool) {
	if sess := f.surface[surface]; sess != nil {
		return sess, true
	}
	return nil, false
}

func TestLookupPlaySessionUsesSurfaceVariant(t *testing.T) {
	jf := &core.PlaySession{ID: "sess-jf"}
	f := surfaceSessionFinder{surface: map[string]*core.PlaySession{"jellyfin": jf}}

	if _, ok := f.SessionFor(1, "dev", 42); ok {
		t.Fatal("SessionFor should miss the broker identity")
	}
	got := lookupPlaySession(f, "jellyfin", 1, "dev", 42)
	if got == nil || got.ID != "sess-jf" {
		t.Fatalf("jellyfin lookup = %+v", got)
	}
	if got := lookupPlaySession(f, "emby", 1, "dev", 42); got != nil {
		t.Fatalf("emby lookup = %+v", got)
	}
	if got := lookupPlaySession(f, "", 1, "dev", 42); got != nil {
		t.Fatalf("plex lookup used SessionFor miss = %+v", got)
	}

	plex := &core.PlaySession{ID: "sess-plex"}
	got = lookupPlaySession(surfaceSessionFinder{plain: plex}, "plex", 1, "dev", 42)
	if got == nil || got.ID != "sess-plex" {
		t.Fatalf("plex surface = %+v", got)
	}
}

func TestOnPlaybackMarkerEventWarmsNonPreparedPlaybackStartsOnly(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"intro":[{"start_ms":13000,"end_ms":97000}]}`)
	}))
	defer srv.Close()

	svc := markers.New(markers.Options{DB: db, Enabled: true, BaseURL: srv.URL})
	defer svc.Close()
	a := &App{markers: svc}

	epKey := core.EpisodeKey(247718, 2, 1)
	info := core.MediaInfo{Duration: 52 * time.Minute}

	// PreparedOnly browse/plan events and estimated durations must not fetch TheIntroDB.
	a.onPlaybackMarkerEvent(playback.Event{Kind: "start", Session: &core.PlaySession{Key: epKey, Info: info, PreparedOnly: true}})
	a.onPlaybackMarkerEvent(playback.Event{Kind: "surface", Session: &core.PlaySession{Key: epKey, Info: core.MediaInfo{Duration: info.Duration, Estimated: true}}})
	svc.Close()
	if calls.Load() != 0 {
		t.Fatalf("prepared/estimated event fetched remote markers: calls=%d", calls.Load())
	}

	// Actual Jellyfin/Emby/Silo/Vela playback open ("surface", PreparedOnly: false) warms markers_cache.
	svc = markers.New(markers.Options{DB: db, Enabled: true, BaseURL: srv.URL})
	defer svc.Close()
	a.markers = svc
	a.onPlaybackMarkerEvent(playback.Event{Kind: "surface", Session: &core.PlaySession{Key: epKey, Info: info}})
	// Calling Warm for the same (key, duration) joins the in-flight warm without issuing a second fetch.
	<-svc.Warm(epKey, info)
	if calls.Load() != 1 {
		t.Fatalf("expected 1 single-flight warm fetch, got %d", calls.Load())
	}
	cached, err := svc.CachedMarkers(ctx, epKey, info)
	if err != nil || len(cached) != 1 || cached[0].StartMS != 13000 || cached[0].EndMS != 97000 {
		t.Fatalf("cached after warm = %+v err=%v", cached, err)
	}
}
