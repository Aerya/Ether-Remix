package playback

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

type capturedLog struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

type logRecorder struct {
	mu      sync.Mutex
	records []capturedLog
	pre     []slog.Attr
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(r.pre)+rec.NumAttrs())
	for _, a := range r.pre {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	r.mu.Lock()
	r.records = append(r.records, capturedLog{
		level: rec.Level,
		msg:   rec.Message,
		attrs: attrs,
	})
	r.mu.Unlock()
	return nil
}

func (r *logRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &subLogRecorder{root: r, pre: append(slices.Clone(r.pre), attrs...)}
}

func (r *logRecorder) WithGroup(string) slog.Handler { return r }

func (r *logRecorder) has(level slog.Level, msg string, match map[string]string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.records {
		if rec.level != level || rec.msg != msg {
			continue
		}
		ok := true
		for k, want := range match {
			if rec.attrs[k] != want {
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

type subLogRecorder struct {
	root *logRecorder
	pre  []slog.Attr
}

func (s *subLogRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (s *subLogRecorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(s.pre)+rec.NumAttrs())
	for _, a := range s.pre {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	s.root.mu.Lock()
	s.root.records = append(s.root.records, capturedLog{
		level: rec.Level,
		msg:   rec.Message,
		attrs: attrs,
	})
	s.root.mu.Unlock()
	return nil
}
func (s *subLogRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &subLogRecorder{root: s.root, pre: append(slices.Clone(s.pre), attrs...)}
}
func (s *subLogRecorder) WithGroup(string) slog.Handler { return s }

func TestAudioLanguageSkipOpensNextCandidateWithoutFallbackTailReads(t *testing.T) {
	const (
		liaID   = "lia-sub"
		liaName = "[Lia] One Piece - 0293 [1080p].mkv"
		w4fID   = "w4f-dub"
		w4fName = "One.Piece.S01E293.DUBBED.720p.HDTV.x264-W4F.mkv"
	)
	animeKey := core.EpisodeKey(37854, 1, 293)

	lia := &fakeRelease{ID: liaID, Name: liaName, Size: 8 << 20, Duration: 24 * time.Minute, Height: 1080, AudioLanguages: []string{"ja"}}
	w4f := &fakeRelease{ID: w4fID, Name: w4fName, Size: 6 << 20, Duration: 24 * time.Minute, Height: 720, AudioLanguages: []string{"eng"}}

	var (
		trailerMu    sync.Mutex
		trailerCalls []string
		trailerDone  = make(chan string, 4)
	)
	logs := &logRecorder{}

	h := newHarness(t, []*fakeRelease{lia, w4f}, []string{liaID, w4fID}, func(h *harness, _ *Options) {
		h.nativeProbe = func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
			buf := make([]byte, headerLen)
			if _, err := src.ReadAt(buf, 0); err != nil {
				return nil, err
			}
			lang := "ja"
			height := 1080
			if src.ReleaseID() == w4fID {
				lang = "eng"
				height = 720
			}
			return &core.MediaInfo{
				Container: "mkv",
				Duration:  24 * time.Minute,
				Size:      src.Size(),
				Streams: []core.StreamInfo{
					{Index: 0, Type: "video", Codec: "h264", Width: height * 16 / 9, Height: height},
					{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: lang, Default: true},
				},
			}, nil
		}
		h.nativeTrailer = func(_ context.Context, src core.MediaSource, _ *core.MediaInfo) bool {
			trailerMu.Lock()
			trailerCalls = append(trailerCalls, src.ReleaseID())
			trailerMu.Unlock()
			trailerDone <- src.ReleaseID()
			return false
		}
	})
	h.m.log = slog.New(logs)
	h.catalog.titles[animeKey] = &core.Title{
		Key:       animeKey,
		Type:      core.TypeEpisode,
		ShowTitle: "One Piece",
		Season:    1,
		Episode:   293,
		Title:     "Bubble Master Kalifa!",
		Language:  "ja",
		Runtime:   24 * time.Minute,
	}
	h.resolver.candidates = []core.Release{
		{ID: liaID, Name: liaName, Size: lia.Size, Info: releaseparse.Parse(liaName)},
		{ID: w4fID, Name: w4fName, Size: w4f.Size, Info: releaseparse.Parse(w4fName)},
	}

	viewer := &core.User{
		ID:               2,
		Username:         "friend",
		AudioLanguages:   []string{"en"},
		AllowedLibraries: []core.LibraryID{2},
	}
	caps := defaultCaps("roku-client-1")
	caps.Protocol = "plex"
	caps.Product = "Plex for Roku"
	caps.Platform = "Roku"
	caps.Device = "Roku Ultra"

	ps, err := h.m.PrepareSurface(context.Background(), viewer, animeKey, caps, "", false)
	if err != nil {
		t.Fatalf("PrepareSurface failed: %v", err)
	}
	if ps.Resolution == nil || ps.Resolution.Release.ID != w4fID {
		t.Fatalf("PrepareSurface selected %+v, want %s", ps.Resolution, w4fID)
	}
	if ps.UserID != viewer.ID || ps.ClientID != caps.ClientID || ps.Key != animeKey || ps.Client.Device != caps.Device {
		t.Fatalf("session context mismatch: user=%d client=%q key=%v device=%q", ps.UserID, ps.ClientID, ps.Key, ps.Client.Device)
	}

	select {
	case got := <-trailerDone:
		if got != w4fID {
			t.Fatalf("first background trailer fill ran for %q, want adopted candidate %q", got, w4fID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("adopted generation never ran deferred trailer fill")
	}

	if !logs.has(slog.LevelInfo, "trying next candidate", map[string]string{
		"release": liaName,
		"reason":  "probed release lacks viewer audio language",
	}) {
		t.Fatalf("missing audio-language skip log in %+v", logs.records)
	}

	trailerMu.Lock()
	gotTrailers := slices.Clone(trailerCalls)
	trailerMu.Unlock()
	if slices.Contains(gotTrailers, liaID) {
		t.Fatalf("skipped candidate %s ran background trailer fill: %v", liaID, gotTrailers)
	}

	h.engine.mu.Lock()
	var liaSpans [][2]int64
	for _, src := range h.engine.sources {
		if src.ReleaseID() == liaID {
			liaSpans = append(liaSpans, src.readSpans()...)
		}
	}
	h.engine.mu.Unlock()
	for _, span := range liaSpans {
		if span[1] > headerLen {
			t.Fatalf("skipped candidate %s performed background tail/index read %v beyond probe header (%d)", liaID, span, headerLen)
		}
	}
}

type blockingNextEngine struct {
	*fakeEngine
	blockID string
}

func (e *blockingNextEngine) Open(ctx context.Context, releaseID string, nzb []byte) (core.MediaSource, error) {
	if releaseID == e.blockID {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return e.fakeEngine.Open(ctx, releaseID, nzb)
}

func TestAudioLanguageSkipLogsFailureAndFallsBackToKeptCandidate(t *testing.T) {
	const (
		liaID   = "lia-sub"
		liaName = "[Lia] One Piece - 0293 [1080p].mkv"
		w4fID   = "w4f-dub"
		w4fName = "One.Piece.S01E293.DUBBED.720p.HDTV.x264-W4F.mkv"
	)
	animeKey := core.EpisodeKey(37854, 1, 293)
	viewer := &core.User{ID: 2, Username: "friend", AudioLanguages: []string{"en"}}

	t.Run("next_candidate_open_error", func(t *testing.T) {
		lia := &fakeRelease{ID: liaID, Name: liaName, Size: 8 << 20, Duration: 24 * time.Minute, Height: 1080, AudioLanguages: []string{"ja"}}
		w4f := &fakeRelease{ID: w4fID, Name: w4fName, Size: 6 << 20, Duration: 24 * time.Minute, Height: 720, OpenErr: core.ErrSourceUnavailable}
		trailerDone := make(chan string, 2)
		logs := &logRecorder{}

		h := newHarness(t, []*fakeRelease{lia, w4f}, []string{liaID, w4fID}, func(h *harness, _ *Options) {
			h.nativeProbe = func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
				return &core.MediaInfo{
					Container: "mkv",
					Duration:  24 * time.Minute,
					Size:      src.Size(),
					Streams: []core.StreamInfo{
						{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
						{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "ja", Default: true},
					},
				}, nil
			}
			h.nativeTrailer = func(_ context.Context, src core.MediaSource, _ *core.MediaInfo) bool {
				trailerDone <- src.ReleaseID()
				return false
			}
		})
		h.m.log = slog.New(logs)
		h.catalog.titles[animeKey] = &core.Title{
			Key: animeKey, Type: core.TypeEpisode, ShowTitle: "One Piece",
			Season: 1, Episode: 293, Title: "Bubble Master Kalifa!", Language: "ja", Runtime: 24 * time.Minute,
		}
		h.resolver.candidates = []core.Release{
			{ID: liaID, Name: liaName, Size: lia.Size, Info: releaseparse.Parse(liaName)},
			{ID: w4fID, Name: w4fName, Size: w4f.Size, Info: releaseparse.Parse(w4fName)},
		}

		ps, err := h.m.Prepare(context.Background(), viewer, animeKey, defaultCaps("roku-err"))
		if err != nil {
			t.Fatalf("Prepare failed instead of falling back to kept candidate: %v", err)
		}
		if ps.Resolution == nil || ps.Resolution.Release.ID != liaID {
			t.Fatalf("Prepare selected %+v, want fallback %s", ps.Resolution, liaID)
		}
		if !logs.has(slog.LevelWarn, "release failed to open", map[string]string{"release": w4fName}) {
			t.Fatalf("missing warn log for failed next candidate in %+v", logs.records)
		}
		select {
		case got := <-trailerDone:
			if got != liaID {
				t.Fatalf("fallback trailer fill ran for %q, want %q", got, liaID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("adopted fallback generation never started deferred trailer fill")
		}
	})

	t.Run("next_candidate_prepare_timeout", func(t *testing.T) {
		lia := &fakeRelease{ID: liaID, Name: liaName, Size: 8 << 20, Duration: 24 * time.Minute, Height: 1080, AudioLanguages: []string{"ja"}}
		w4f := &fakeRelease{ID: w4fID, Name: w4fName, Size: 6 << 20, Duration: 24 * time.Minute, Height: 720, AudioLanguages: []string{"eng"}}
		logs := &logRecorder{}

		baseEngine := newFakeEngine(lia, w4f)
		h := newHarness(t, []*fakeRelease{lia, w4f}, []string{liaID, w4fID}, func(h *harness, o *Options) {
			o.PrepareTimeout = 120 * time.Millisecond
			h.engine = baseEngine
			h.testEngine = &blockingNextEngine{fakeEngine: baseEngine, blockID: w4fID}
			h.nativeProbe = func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
				return &core.MediaInfo{
					Container: "mkv",
					Duration:  24 * time.Minute,
					Size:      src.Size(),
					Streams: []core.StreamInfo{
						{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
						{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "ja", Default: true},
					},
				}, nil
			}
		})
		h.m.log = slog.New(logs)
		h.catalog.titles[animeKey] = &core.Title{
			Key: animeKey, Type: core.TypeEpisode, ShowTitle: "One Piece",
			Season: 1, Episode: 293, Title: "Bubble Master Kalifa!", Language: "ja", Runtime: 24 * time.Minute,
		}
		h.resolver.candidates = []core.Release{
			{ID: liaID, Name: liaName, Size: lia.Size, Info: releaseparse.Parse(liaName)},
			{ID: w4fID, Name: w4fName, Size: w4f.Size, Info: releaseparse.Parse(w4fName)},
		}

		ps, err := h.m.Prepare(context.Background(), viewer, animeKey, defaultCaps("roku-timeout"))
		if err != nil {
			t.Fatalf("Prepare timed out instead of falling back to kept candidate: %v", err)
		}
		if ps.Resolution == nil || ps.Resolution.Release.ID != liaID {
			t.Fatalf("Prepare selected %+v, want fallback %s", ps.Resolution, liaID)
		}
		if !logs.has(slog.LevelWarn, "release failed to open", map[string]string{"release": w4fName}) {
			t.Fatalf("missing warn log for timed-out next candidate in %+v", logs.records)
		}
	})

	t.Run("next_candidate_fetch_nzb_timeout", func(t *testing.T) {
		lia := &fakeRelease{ID: liaID, Name: liaName, Size: 8 << 20, Duration: 24 * time.Minute, Height: 1080, AudioLanguages: []string{"ja"}}
		w4f := &fakeRelease{ID: w4fID, Name: w4fName, Size: 6 << 20, Duration: 24 * time.Minute, Height: 720, AudioLanguages: []string{"eng"}}
		logs := &logRecorder{}

		h := newHarness(t, []*fakeRelease{lia, w4f}, []string{liaID, w4fID}, func(h *harness, o *Options) {
			o.PrepareTimeout = 120 * time.Millisecond
			h.nativeProbe = func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
				return &core.MediaInfo{
					Container: "mkv",
					Duration:  24 * time.Minute,
					Size:      src.Size(),
					Streams: []core.StreamInfo{
						{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
						{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "ja", Default: true},
					},
				}, nil
			}
		})
		h.m.log = slog.New(logs)
		h.m.deps.FetchNZB = func(ctx context.Context, r core.Release) ([]byte, error) {
			if r.ID == w4fID {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return []byte("nzb-" + r.ID), nil
		}
		h.catalog.titles[animeKey] = &core.Title{
			Key: animeKey, Type: core.TypeEpisode, ShowTitle: "One Piece",
			Season: 1, Episode: 293, Title: "Bubble Master Kalifa!", Language: "ja", Runtime: 24 * time.Minute,
		}
		h.resolver.candidates = []core.Release{
			{ID: liaID, Name: liaName, Size: lia.Size, Info: releaseparse.Parse(liaName)},
			{ID: w4fID, Name: w4fName, Size: w4f.Size, Info: releaseparse.Parse(w4fName)},
		}

		ps, err := h.m.Prepare(context.Background(), viewer, animeKey, defaultCaps("roku-fetch-timeout"))
		if err != nil {
			t.Fatalf("Prepare timed out during FetchNZB instead of falling back to kept candidate: %v", err)
		}
		if ps.Resolution == nil || ps.Resolution.Release.ID != liaID {
			t.Fatalf("Prepare selected %+v, want fallback %s", ps.Resolution, liaID)
		}
		if !logs.has(slog.LevelWarn, "alternate nzb fetch failed", map[string]string{"release": w4fName}) {
			t.Fatalf("missing warn log for timed-out FetchNZB in %+v", logs.records)
		}
	})
}
