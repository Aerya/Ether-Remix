package livetv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type recordedLiveObs struct {
	session core.PlaySession
	ended   bool
	problem string
}

func TestLiveTVObserveSessionLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var obs []recordedLiveObs
	record := func(p *core.PlaySession, ended bool, problem string) {
		mu.Lock()
		defer mu.Unlock()
		obs = append(obs, recordedLiveObs{session: *p, ended: ended, problem: problem})
	}

	s := testService(t, Options{
		Now:            func() time.Time { return now },
		ObserveSession: record,
	})

	// 1. Segmented media delivery -> emits once on first delivered segment, then once on stop.
	ss := &session{
		info: SessionInfo{ID: "seg-1", UserID: 7, Channel: Channel{ID: "ch-1", Name: "ESPN", Number: "206"}, StartedAt: now},
		last: now,
		view: core.PlaySession{
			ID: "seg-1", UserID: 7, Username: "Sam", ClientID: "tv-1",
			TitleName: "ESPN", StartedAt: now, Decision: core.DecisionRemux,
			DecisionVideoCopy: true, DecisionAudioCopy: true, Origin: "Live TV",
			LiveChannel: &core.LiveChannel{ID: "ch-1", Name: "ESPN", Number: "206"},
		},
	}
	now = now.Add(2 * time.Second)
	for range 2 {
		rec := httptest.NewRecorder()
		w := s.observeMedia(rec, ss)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ts-segment-bytes"))
		w.done()
	}
	now = now.Add(5 * time.Minute)
	ss.close(s)

	mu.Lock()
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations (delivery + stop), got %d: %+v", len(obs), obs)
	}
	if obs[0].ended || obs[0].problem != "" || obs[0].session.ViewerState != "playing" || obs[0].session.LiveChannel == nil || obs[0].session.LiveChannel.ID != "ch-1" {
		t.Fatalf("unexpected delivery observation: %+v", obs[0])
	}
	if !obs[1].ended || obs[1].problem != "" || obs[1].session.BytesServed != int64(2*len("ts-segment-bytes")) || !obs[1].session.LastActive.Equal(now) {
		t.Fatalf("unexpected stop observation: %+v", obs[1])
	}
	obs = nil
	mu.Unlock()

	// 2. Continuous stream delivery -> emits immediately on first 2xx Write and caps LastActive on idle expiry.
	now = now.Add(time.Minute)
	cs := &session{
		info: SessionInfo{ID: "cont-1", UserID: 7, Channel: Channel{ID: "ch-2", Name: "CNN"}, StartedAt: now},
		last: now,
		view: core.PlaySession{
			ID: "cont-1", UserID: 7, Username: "Sam", ClientID: "tv-1",
			TitleName: "CNN", StartedAt: now, Decision: core.DecisionRemux,
			DecisionVideoCopy: true, DecisionAudioCopy: true, Origin: "Live TV",
			LiveChannel: &core.LiveChannel{ID: "ch-2", Name: "CNN"},
		},
	}
	rec := httptest.NewRecorder()
	cw := s.observeContinuousMedia(rec, cs)
	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("chunk-1"))
	now = now.Add(3 * time.Minute)
	_, _ = cw.Write([]byte("chunk-2"))
	lastMedia := now
	cw.done()
	now = now.Add(30 * time.Second)
	cs.closeExpired(s)

	mu.Lock()
	if len(obs) != 2 {
		t.Fatalf("expected 2 continuous observations (first write + expire), got %d: %+v", len(obs), obs)
	}
	if obs[0].ended || obs[0].session.ViewerState != "playing" {
		t.Fatalf("expected immediate delivery observation on continuous write: %+v", obs[0])
	}
	if !obs[1].ended || !obs[1].session.LastActive.Equal(lastMedia) || obs[1].session.BytesServed != int64(len("chunk-1")+len("chunk-2")) {
		t.Fatalf("expected expired continuous session to cap LastActive at lastMedia (%v), got %+v", lastMedia, obs[1])
	}
	obs = nil
	mu.Unlock()

	// 3. Unplayed session closed before any media delivery emits nothing.
	unplayed := &session{
		info: SessionInfo{ID: "unplayed-1", UserID: 7, Channel: Channel{ID: "ch-3", Name: "BBC"}, StartedAt: now},
		last: now,
		view: core.PlaySession{ID: "unplayed-1", UserID: 7, TitleName: "BBC", LiveChannel: &core.LiveChannel{ID: "ch-3", Name: "BBC"}},
	}
	unplayed.close(s)
	mu.Lock()
	if len(obs) != 0 {
		t.Fatalf("unplayed session must not emit history observations: %+v", obs)
	}
	mu.Unlock()
}

func TestLiveTVObserveSessionTuneFailureAndCancel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "SECRET_BODY", http.StatusServiceUnavailable)
	}))
	defer up.Close()

	var mu sync.Mutex
	var obs []recordedLiveObs
	s := testService(t, Options{
		HTTPClient: up.Client(),
		ObserveSession: func(p *core.PlaySession, ended bool, problem string) {
			mu.Lock()
			defer mu.Unlock()
			obs = append(obs, recordedLiveObs{session: *p, ended: ended, problem: problem})
		},
	})
	ctx := context.Background()
	if _, err := s.ImportSource(ctx, Source{
		ID:             "provider",
		Name:           "Provider",
		MaxConnections: 1,
		Playlist:       "#EXTM3U\n#EXTINF:-1,News 24\n" + up.URL + "/SECRET_USER/SECRET_PASS/1.ts\n",
	}); err != nil {
		t.Fatal(err)
	}
	channels, err := s.Channels(ctx)
	if err != nil || len(channels) == 0 {
		t.Fatalf("channels: %v %+v", err, channels)
	}

	// Cancelled tune must not record a failure.
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, _ = s.Tune(cancelledCtx, &core.User{ID: 7, Username: "Sam"}, channels[0].ID, core.ClientCaps{ClientID: "tv-1"})
	mu.Lock()
	if len(obs) != 0 {
		t.Fatalf("cancelled tune should not record failure: %+v", obs)
	}
	mu.Unlock()

	// Real 503 tune failure records a credential-safe failed play with channel identity.
	_, err = s.Tune(ctx, &core.User{ID: 7, Username: "Sam"}, channels[0].ID, core.ClientCaps{ClientID: "tv-1"})
	if err == nil {
		t.Fatal("expected tune error")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(obs) != 1 {
		t.Fatalf("expected 1 tune failure observation, got %d: %+v", len(obs), obs)
	}
	got := obs[0]
	if !got.ended || got.problem == "" || got.session.ViewerState != "" || got.session.LiveChannel == nil || got.session.LiveChannel.ID != channels[0].ID {
		t.Fatalf("unexpected tune failure observation: %+v", got)
	}
	for _, secret := range []string{"SECRET_USER", "SECRET_PASS", "SECRET_BODY", up.URL} {
		if strings.Contains(got.problem, secret) {
			t.Fatalf("tune failure observation leaked %q: %s", secret, got.problem)
		}
	}
}
