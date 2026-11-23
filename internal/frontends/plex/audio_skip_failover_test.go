package plex

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/store"
)

type w88LogEntry struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

type w88LogHandler struct {
	mu      sync.Mutex
	entries []w88LogEntry
	pre     []slog.Attr
}

func (h *w88LogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *w88LogHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]string, len(h.pre)+r.NumAttrs())
	for _, a := range h.pre {
		attrs[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	h.entries = append(h.entries, w88LogEntry{level: r.Level, msg: r.Message, attrs: attrs})
	h.mu.Unlock()
	return nil
}
func (h *w88LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &w88SubLogHandler{root: h, pre: append(slices.Clone(h.pre), attrs...)}
}
func (h *w88LogHandler) WithGroup(string) slog.Handler { return h }
func (h *w88LogHandler) has(level slog.Level, msg string, match map[string]string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		if e.level != level || e.msg != msg {
			continue
		}
		ok := true
		for k, v := range match {
			if e.attrs[k] != v {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

type w88SubLogHandler struct {
	root *w88LogHandler
	pre  []slog.Attr
}

func (s *w88SubLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (s *w88SubLogHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]string, len(s.pre)+r.NumAttrs())
	for _, a := range s.pre {
		attrs[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	s.root.mu.Lock()
	s.root.entries = append(s.root.entries, w88LogEntry{level: r.Level, msg: r.Message, attrs: attrs})
	s.root.mu.Unlock()
	return nil
}
func (s *w88SubLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &w88SubLogHandler{root: s.root, pre: append(slices.Clone(s.pre), attrs...)}
}
func (s *w88SubLogHandler) WithGroup(string) slog.Handler { return s }

type w88Engine struct {
	failID string
}

func (e w88Engine) Open(_ context.Context, id string, _ []byte) (core.MediaSource, error) {
	if id == e.failID {
		return nil, core.ErrSourceUnavailable
	}
	return &issue94Source{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), 2<<20)), id: id}, nil
}

func (w88Engine) Prefetch(context.Context, string, []byte, int64, int64) error { return nil }

func TestPlexDecisionAudioLanguageSkipStartsNextCandidateAndFallsBackOnFailure(t *testing.T) {
	const (
		liaID   = "lia-sub"
		liaName = "[Lia] One Piece - 0293 [1080p]"
		w4fID   = "w4f-dub"
		w4fName = "One.Piece.S01E293.DUBBED.720p.HDTV.x264-W4F"
	)

	onePieceShow := core.Title{
		Key: core.ShowKey(37854), Type: core.TypeShow, IDs: core.IDs{TMDB: 37854, TVDB: 81797},
		Title: "One Piece", Language: "ja", Year: 1999, ChildCount: 1, LeafCount: 1, UpdatedAt: fixedNow,
	}
	onePieceSeason := core.Title{
		Key: core.SeasonKey(37854, 1), Type: core.TypeSeason, ShowKey: onePieceShow.Key,
		ShowTitle: "One Piece", Language: "ja", Season: 1, Title: "Season 1", ChildCount: 1, UpdatedAt: fixedNow,
	}
	onePieceEp := core.Title{
		Key: core.EpisodeKey(37854, 1, 293), Type: core.TypeEpisode, ShowKey: onePieceShow.Key,
		ShowTitle: "One Piece", Language: "ja", Season: 1, Episode: 293,
		Title: "Bubble Master Kalifa!", Runtime: 24 * time.Minute,
		Released: time.Date(2007, 1, 14, 0, 0, 0, 0, time.UTC), UpdatedAt: fixedNow,
	}

	setup := func(t *testing.T, failSecond bool) (*fixture, *playback.Manager, *core.User, *w88LogHandler) {
		t.Helper()
		ctx := context.Background()
		dataDir := t.TempDir()
		cfg := config.Defaults(dataDir)
		cfg.Resolver.SameQualityAlternatives = 2

		db, err := store.Open(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })

		users, err := auth.New(ctx, db, auth.Options{Now: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		if err := users.BootstrapOwner(ctx, config.Plex{OwnerPlexID: 1001, OwnerUsername: "owner", Token: ownerToken}); err != nil {
			t.Fatal(err)
		}
		if err := users.SyncFriends(ctx, []plextv.Friend{{
			PlexID:   2002,
			Username: "friend",
			Token:    friendToken,
			Sections: []string{"1", "2"},
		}}); err != nil {
			t.Fatal(err)
		}
		initialFriend, err := users.Authenticate(ctx, friendToken)
		if err != nil {
			t.Fatal(err)
		}
		// Give the shared viewer an English audio preference, matching the Roku viewer in #596.
		updatedFriend := *initialFriend
		updatedFriend.AudioLanguages = []string{"en"}
		if err := users.UpdateUser(ctx, updatedFriend); err != nil {
			t.Fatal(err)
		}
		friend, err := users.Authenticate(ctx, friendToken)
		if err != nil {
			t.Fatal(err)
		}

		safetySvc, err := safety.New(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if !safetySvc.Restricted(friend) {
			t.Fatal("friend viewer must exercise restricted ConfirmAdmission path")
		}

		now := time.Now()
		releases := []core.Release{
			{
				ID: liaID, GUID: liaID, Indexer: "offline",
				Name: liaName, Size: 900 << 20, PubDate: now.Add(-24 * time.Hour), NZBURL: "offline",
				Info: releaseparse.Parse(liaName),
			},
			{
				ID: w4fID, GUID: w4fID, Indexer: "offline",
				Name: w4fName, Size: 500 << 20, PubDate: now.Add(-24 * time.Hour), NZBURL: "offline",
				Info: releaseparse.Parse(w4fName),
			},
		}
		rs := resolver.New(&issue94Indexer{releases: releases}, nil, nil, cfg, resolver.Options{})
		t.Cleanup(func() { rs.Close() })

		f := newFixture(func(d *testServerDeps) {
			d.DB = db
			d.DisableEnrichment = true
			setDepsAuth(d, users)
			setDepsConfig(d, func() config.Config { return cfg })
		})
		f.catalog.titles[onePieceShow.Key] = onePieceShow
		f.catalog.titles[onePieceSeason.Key] = onePieceSeason
		f.catalog.titles[onePieceEp.Key] = onePieceEp
		f.catalog.children[onePieceShow.Key] = []core.RatingKey{onePieceSeason.Key}
		f.catalog.children[onePieceSeason.Key] = []core.RatingKey{onePieceEp.Key}

		failID := ""
		if failSecond {
			failID = w4fID
		}
		tr := &issue94Transcoder{newFakeTranscoder(core.Decision{})}
		logs := &w88LogHandler{}
		mgr, err := playback.New(playback.Deps{
			Catalog:    f.catalog,
			State:      f.state,
			Resolver:   rs,
			FetchNZB:   func(_ context.Context, r core.Release) ([]byte, error) { return []byte(r.ID), nil },
			Engine:     w88Engine{failID: failID},
			Transcoder: tr,
			DB:         db,
			Logger:     slog.New(logs),
			SelectProfile: func(c core.ClientCaps, u *core.User, _ string) core.QualityProfile {
				return rs.ProfileFor(c, u)
			},
			NativeProbe: func(_ context.Context, s core.MediaSource) (*core.MediaInfo, error) {
				lang := "ja"
				height := 1080
				if s.ReleaseID() == w4fID {
					lang = "eng"
					height = 720
				}
				return &core.MediaInfo{
					Container:   "mkv",
					Duration:    24 * time.Minute,
					BitrateKbps: 3200,
					Size:        s.Size(),
					Streams: []core.StreamInfo{
						{Index: 0, Type: "video", Codec: "h264", Profile: "high", Level: 41, Width: height * 16 / 9, Height: height, BitDepth: 8},
						{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000, Language: lang, Default: true},
					},
				}, nil
			},
		}, cfg, playback.Options{
			SessionSurface: func(surface string) string {
				if surface == "plex" {
					return ""
				}
				return surface
			},
			DataDir: dataDir,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.srv.setPlayback(mgr)
		f.srv.setTranscoder(tr)

		// Replace the test broker with one wired to safety.Service (FreshPlay + CurrentPlay)
		// so openPlexPlan runs the real restricted-viewer fenced ConfirmAdmission path.
		var lrDeps sessions.LocalRuntimeDeps
		if rt := f.srv.testRuntime(); rt != nil {
			lrDeps = rt.Deps()
		}
		active := func(ctx context.Context, id int64) (*core.Viewer, error) {
			return users.ActiveUser(ctx, id)
		}
		events := sessions.NewEventBus(nil, active)
		broker, err := sessions.NewBroker(sessions.BrokerDeps{
			SurfacePolicy: func(protocol core.Protocol) core.SessionPolicy {
				if protocol == "plex" {
					return SessionPolicy()
				}
				return core.SessionPolicy{}
			},
			DB:               db,
			SessionHeartbeat: lrDeps.SessionHeartbeat,
			Coordination:     lrDeps.Coordination,
			Downloads:        lrDeps.Downloads,
			DecisionTraces:   lrDeps.DecisionTraces,
			Lookup: func(ctx context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
				return f.catalog.Get(ctx, key)
			},
			Playback:     mgr,
			Transcoder:   tr,
			Progress:     fixtureProgress{f: f},
			Trickplay:    fixtureTrickplay{f: f},
			ActiveViewer: active,
			CanPlay:      safetySvc.CanPlay,
			BitrateLimit: safetySvc.BitrateLimit,
			FreshPlay:    safetySvc.ConfirmAdmission,
			CurrentPlay:  safetySvc.ConfirmPlayback,
			OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
			Events:       events,
			Now:          f.srv.now,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.broker = broker
		f.srv.usePlaybackBroker(sessions.BindSurface(broker, "plex"))

		t.Cleanup(func() {
			f.srv.Close()
			mgr.Close()
		})
		return f, mgr, friend, logs
	}

	rokuHeaders := []string{
		"X-Plex-Token", friendToken,
		"X-Plex-Client-Identifier", "roku-living-room",
		"X-Plex-Session-Identifier", "roku-play-293",
		"X-Plex-Product", "Plex for Roku",
		"X-Plex-Platform", "Roku",
		"X-Plex-Device", "Roku Ultra",
		"Accept", "application/json",
	}

	t.Run("next_candidate_starts_with_same_viewer_device_session", func(t *testing.T) {
		f, mgr, friend, logs := setup(t, false)
		q := url.Values{
			"path":         {"/library/metadata/" + onePieceEp.Key.String()},
			"mediaIndex":   {"0"},
			"partIndex":    {"0"},
			"protocol":     {"hls"},
			"hasMDE":       {"1"},
			"directPlay":   {"0"},
			"directStream": {"1"},
			"session":      {"roku-transcode-293"},
		}

		rec := f.get(t, "/video/:/transcode/universal/decision?"+q.Encode(), rokuHeaders...)
		if rec.Code != http.StatusOK {
			t.Fatalf("decision status = %d, want 200: %s", rec.Code, rec.Body.String())
		}

		ps, ok := mgr.SessionFor(friend.ID, "roku-living-room", onePieceEp.Key)
		if !ok || ps == nil || ps.Resolution == nil {
			t.Fatalf("no playback session started for restricted Roku viewer: ok=%t ps=%+v", ok, ps)
		}
		if ps.Resolution.Release.ID != w4fID {
			t.Fatalf("session release = %s, want %s after skipping %s", ps.Resolution.Release.ID, w4fID, liaID)
		}
		if ps.UserID != friend.ID || ps.ClientID != "roku-living-room" {
			t.Fatalf("session viewer/device = (%d, %q), want (%d, %q)", ps.UserID, ps.ClientID, friend.ID, "roku-living-room")
		}

		access := frontend.SessionAccess{
			Viewer:   friend,
			Device:   core.Device{ID: "roku-living-room"},
			Protocol: "plex",
		}
		if gotSess, err := f.broker.PlexSessionForPlayback(context.Background(), access, onePieceEp.Key, "", "roku-transcode-293"); err != nil || gotSess != "roku-transcode-293" {
			t.Fatalf("broker PlexSessionForPlayback = (%q, %v), want (%q, nil)", gotSess, err, "roku-transcode-293")
		}

		startRec := f.get(t, "/video/:/transcode/universal/start.m3u8?"+q.Encode(), rokuHeaders...)
		if startRec.Code != http.StatusOK {
			t.Fatalf("start.m3u8 status = %d, want 200: %s", startRec.Code, startRec.Body.String())
		}

		if !logs.has(slog.LevelInfo, "trying next candidate", map[string]string{
			"release": liaName,
			"reason":  "probed release lacks viewer audio language",
		}) {
			t.Fatalf("expected audio-language skip log in %+v", logs.entries)
		}
	})

	t.Run("next_candidate_failure_logs_and_falls_back_to_kept_release", func(t *testing.T) {
		f, mgr, friend, logs := setup(t, true)
		q := url.Values{
			"path":         {"/library/metadata/" + onePieceEp.Key.String()},
			"mediaIndex":   {"0"},
			"partIndex":    {"0"},
			"protocol":     {"hls"},
			"hasMDE":       {"1"},
			"directPlay":   {"0"},
			"directStream": {"1"},
			"session":      {"roku-fallback-293"},
		}

		rec := f.get(t, "/video/:/transcode/universal/decision?"+q.Encode(), rokuHeaders...)
		if rec.Code != http.StatusOK {
			t.Fatalf("decision status = %d, want 200 on fallback release: %s", rec.Code, rec.Body.String())
		}

		ps, ok := mgr.SessionFor(friend.ID, "roku-living-room", onePieceEp.Key)
		if !ok || ps == nil || ps.Resolution == nil {
			t.Fatalf("no fallback playback session started: ok=%t ps=%+v", ok, ps)
		}
		if ps.Resolution.Release.ID != liaID {
			t.Fatalf("fallback session release = %s, want kept release %s", ps.Resolution.Release.ID, liaID)
		}
		if !logs.has(slog.LevelWarn, "release failed to open", map[string]string{"release": w4fName}) {
			t.Fatalf("expected warn log for failed second candidate in %+v", logs.entries)
		}
	})
}
