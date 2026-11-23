package plex

import (
	"bytes"
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// plezyColdPlayback estimates without subtitle tracks until the
// play-time probe runs, then serves the probed facts as a warm probe —
// the cold-title shape of #618. Nothing probes unless ProbePlayMetadata
// is called, so any probed track in a response was waited for.
type plezyColdPlayback struct {
	*fakePlayback
	probed     core.MediaInfo
	ready      atomic.Bool
	calls      atomic.Int32
	block      <-chan struct{}
	entered    chan struct{}
	mu         sync.Mutex
	lastCtx    context.Context
	lastBudget time.Duration
	hasDL      bool
	wasLocal   bool
}

func (c *plezyColdPlayback) Estimate(context.Context, *core.Title, core.ClientCaps) core.MediaInfo {
	return estimateInfo
}

func (c *plezyColdPlayback) ProbePlayMetadata(ctx context.Context, _ *core.User, _ core.RatingKey, _ core.ClientCaps) error {
	c.calls.Add(1)
	dl, hasDL := ctx.Deadline()
	var budget time.Duration
	if hasDL {
		budget = time.Until(dl)
	}
	c.mu.Lock()
	c.lastCtx = ctx
	c.lastBudget = budget
	c.hasDL = hasDL
	c.wasLocal = core.LocalResponse(ctx)
	c.mu.Unlock()
	if c.entered != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
	}
	if c.block != nil {
		select {
		case <-c.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.ready.Store(true)
	return nil
}

func (c *plezyColdPlayback) WarmProbeInfo(*core.User, core.ClientCaps, core.RatingKey) (core.MediaInfo, bool) {
	if !c.ready.Load() {
		return core.MediaInfo{}, false
	}
	return c.probed, true
}

// TestPlezyFirstPlayRefetchSelectsRuleSubtitle reproduces the exact #618
// Plezy 2.26.0 TV-mode request pattern on a never-played title:
//
//   - Opening the detail screen sends 3 GET /library/metadata/<id> within 1 s.
//     None of the burst fetches may wait or trigger a probe, and the arm must
//     survive the burst.
//   - Pressing Play 14 s later sends a 4th identical fetch. That re-ask must
//     wait on the probe (with a >4 s / <=12 s wait budget and without
//     LocalResponse) and return the probed streams with the viewer's
//     en/Always subtitle selected and accessible="1".
func TestPlezyFirstPlayRefetchSelectsRuleSubtitle(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 25 * time.Minute, BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
		},
	}
	f, _, _ := newIssue459Fixture(t, info, "en")
	var clock atomic.Int64
	clock.Store(fixedNow.UnixNano())
	f.srv.setTestNow(func() time.Time { return time.Unix(0, clock.Load()).UTC() })

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	cold := &plezyColdPlayback{
		fakePlayback: f.playback,
		probed:       info,
		block:        release,
		entered:      entered,
	}
	f.srv.setPlayback(cold)
	f.playback.info = estimateInfo
	key := fightClub.Key.String()
	hdr := []string{
		"X-Plex-Product", "Plezy",
		"X-Plex-Version", "2.26.0",
		"X-Plex-Platform", "Android",
		"X-Plex-Device", "AFTKRT",
		"X-Plex-Client-Identifier", "plezy-618",
		"X-Plex-Session-Identifier", "plezy-618-session",
	}
	detail := "/library/metadata/" + key + "?includeChapters=1&includeMarkers=1&checkFiles=1&includeStreams=1"

	// Opening the detail screen sends three fetches within ~1 s. Because
	// release is still open, any wait here would block forever.
	for i, step := range []time.Duration{0, 300 * time.Millisecond, 600 * time.Millisecond} {
		if step > 0 {
			clock.Add(int64(step))
		}
		resp := f.get(t, detail, hdr...)
		if resp.Code != 200 {
			t.Fatalf("burst fetch %d: %d %s", i+1, resp.Code, resp.Body.String())
		}
		if cold.calls.Load() != 0 || cold.ready.Load() {
			t.Fatalf("burst fetch %d waited/probed while browsing (calls=%d)", i+1, cold.calls.Load())
		}
		if strings.Contains(partTag(resp.Body.String()), `accessible=`) {
			t.Fatalf("burst fetch %d claims file-checked completeness: %s", i+1, partTag(resp.Body.String()))
		}
		for _, tag := range subtitleTags(resp.Body.String()) {
			if strings.Contains(tag, `index="2"`) {
				t.Fatalf("burst fetch %d already lists the embedded track: %s", i+1, tag)
			}
		}
	}

	// 14 s after the arming fetch, the user presses Play.
	clock.Store(fixedNow.Add(14 * time.Second).UnixNano())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.get(t, detail, hdr...)
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("play-time re-ask at +14 s did not start/wait on the probe")
	}
	close(release)

	playResp := <-done
	if playResp.Code != 200 {
		t.Fatalf("play re-fetch %d: %s", playResp.Code, playResp.Body.String())
	}
	if cold.calls.Load() != 1 || !cold.ready.Load() {
		t.Fatalf("play re-fetch probe calls=%d ready=%v, want 1/true", cold.calls.Load(), cold.ready.Load())
	}
	cold.mu.Lock()
	hasDL, budget, wasLocal := cold.hasDL, cold.lastBudget, cold.wasLocal
	cold.mu.Unlock()
	if wasLocal {
		t.Fatal("play re-fetch passed LocalResponse context to the probe")
	}
	if !hasDL {
		t.Fatal("play re-fetch ran without a bounded deadline")
	}
	if budget <= 4*time.Second || budget > plezyPlayProbeWait {
		t.Fatalf("play re-fetch wait budget = %s, want within (4s, %s]", budget, plezyPlayProbeWait)
	}
	selected := selectedTag(subtitleTags(playResp.Body.String()))
	if !strings.Contains(selected, `index="2"`) || !strings.Contains(selected, `languageCode="eng"`) {
		t.Fatalf("play-time re-fetch did not select the English subtitle: %q", selected)
	}
	if !strings.Contains(partTag(playResp.Body.String()), `accessible="1"`) {
		t.Fatalf("probed Part does not carry file-checked completeness: %s", partTag(playResp.Body.String()))
	}
}

// TestPlezyFirstPlayWaitBoundedByRequestContext ensures cancelling the HTTP
// request stops the play-time metadata wait promptly without hanging.
func TestPlezyFirstPlayWaitBoundedByRequestContext(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 25 * time.Minute, BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
		},
	}
	f, _, _ := newIssue459Fixture(t, info, "en")
	var clock atomic.Int64
	clock.Store(fixedNow.UnixNano())
	f.srv.setTestNow(func() time.Time { return time.Unix(0, clock.Load()).UTC() })

	never := make(chan struct{})
	entered := make(chan struct{}, 1)
	cold := &plezyColdPlayback{fakePlayback: f.playback, probed: info, block: never, entered: entered}
	f.srv.setPlayback(cold)
	f.playback.info = estimateInfo

	detail := "/library/metadata/" + fightClub.Key.String() + "?includeChapters=1&includeMarkers=1&checkFiles=1&includeStreams=1"
	hdr := []string{
		"X-Plex-Product", "Plezy",
		"X-Plex-Version", "2.26.0",
		"X-Plex-Platform", "Android",
		"X-Plex-Client-Identifier", "plezy-cancel",
	}
	if r := f.get(t, detail, hdr...); r.Code != 200 {
		t.Fatalf("arm fetch %d", r.Code)
	}

	clock.Store(fixedNow.Add(14 * time.Second).UnixNano())
	rctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, detail, nil).WithContext(rctx)
	req.Header.Set("X-Plex-Token", ownerToken)
	for i := 0; i < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.srv.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("play re-ask did not enter probe")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled play re-ask did not return promptly")
	}
}

type plezyLabIndexer struct {
	release      core.Release
	specFailed   chan struct{}
	rejectSpec   bool
	playSearches atomic.Int32
	specBlocked  atomic.Int32
}

func (*plezyLabIndexer) Name() string { return "offline-plezy-lab" }

func (i *plezyLabIndexer) Search(ctx context.Context, _ core.SearchRequest) ([]core.Release, error) {
	if core.IsSpeculative(ctx) || core.IndexerPurpose(ctx) == core.IndexerPurposeWarmup || core.SpeculativeIndexerWork(ctx) {
		if i.rejectSpec {
			i.specBlocked.Add(1)
			if i.specFailed != nil {
				select {
				case i.specFailed <- struct{}{}:
				default:
				}
			}
			return nil, core.ErrForegroundOnly
		}
		return []core.Release{i.release}, nil
	}
	i.playSearches.Add(1)
	return []core.Release{i.release}, nil
}

func (i *plezyLabIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	return []byte("nzb-" + r.ID), nil
}

// TestPlezyFirstPlayRealManagerLabPattern exercises the real playback.Manager,
// resolver, trackprefs, and production NoProviderWait (LocalResponse) against
// the lab pattern: 3 metadata fetches in 1 s, then Play 14 s later, then
// file.mkv adopting the same resolved+probed warm flight.
func TestPlezyFirstPlayRealManagerLabPattern(t *testing.T) {
	for _, tc := range []struct {
		name               string
		disableSpeculation bool
		detailWarmup       string
		rejectSpec         bool
	}{
		{
			name:       "after-speculative-warm-miss",
			rejectSpec: true,
		},
		{
			name:               "detail-warmup-disabled",
			disableSpeculation: true,
			detailWarmup:       "off",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config.Defaults(dir)
			cfg.Quality.Default = "lab1080"
			cfg.Quality.Remote = ""
			cfg.Quality.Rules = nil
			cfg.Quality.Profiles = []core.QualityProfile{{Name: "lab1080", MaxHeight: 1080}}
			cfg.Cache.DisableSpeculation = tc.disableSpeculation
			cfg.Cache.DetailWarmup = tc.detailWarmup

			relName := "Fjord.Harbor.1999.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-LAB"
			const relSize = int64(4) << 30
			blockProbe := make(chan struct{})
			probeStarted := make(chan struct{}, 1)
			specFailed := make(chan struct{}, 4)
			idx := &plezyLabIndexer{
				release: core.Release{
					ID: "plezy-remux-rel", GUID: "plezy-remux-rel", Indexer: "offline-plezy-lab",
					Name: relName, Size: relSize, PubDate: fixedNow.Add(-24 * time.Hour),
					NZBURL: "offline", Info: releaseparse.Parse(relName),
				},
				specFailed: specFailed,
				rejectSpec: tc.rejectSpec,
			}
			engine := &shieldLocalEngine{size: relSize}

			var clock atomic.Int64
			clock.Store(fixedNow.UnixNano())
			nowFn := func() time.Time { return time.Unix(0, clock.Load()).UTC() }

			f := newFixture(func(d *testServerDeps) {
				setDepsConfig(d, func() config.Config { return cfg })
				setDepsNow(d, nowFn)
				d.DisableEnrichment = true
				// Match production: handleMetadata wraps ctx with LocalResponse.
				d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
					return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
				}}
			})
			defer f.srv.Close()

			prefs := trackprefs.New(store.OpenTest(t))
			f.srv.setTrackPreferences(prefs)
			rules := trackprefs.Default()
			rules.SubtitleLanguages = []string{"en"}
			rules.SubtitleMode = "always"
			if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
				t.Fatal(err)
			}

			rs := resolver.New(idx, nil, nil, cfg, resolver.Options{Now: nowFn})
			defer rs.Close()

			db, err := store.Open(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			var probes atomic.Int32
			probedInfo := core.MediaInfo{
				Container: "mkv", Duration: fightClub.Runtime, BitrateKbps: 25000,
				Streams: []core.StreamInfo{
					{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
					{Index: 1, Type: "audio", Codec: "dca", Language: "en", Channels: 6, Default: true},
					{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
				},
			}
			mgr, err := playback.New(playback.Deps{
				Catalog:       f.catalog,
				State:         f.state,
				Resolver:      rs,
				Engine:        engine,
				Transcoder:    f.trans,
				SelectProfile: func(c core.ClientCaps, u *core.User, _ string) core.QualityProfile { return rs.ProfileFor(c, u) },
				DB:            db,
				NativeProbe: func(ctx context.Context, s core.MediaSource) (*core.MediaInfo, error) {
					probes.Add(1)
					select {
					case probeStarted <- struct{}{}:
					default:
					}
					select {
					case <-blockProbe:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					info := core.CloneMediaInfo(probedInfo)
					info.Size = s.Size()
					return &info, nil
				},
			}, cfg, playback.Options{
				SessionSurface: func(surface string) string {
					if surface == "plex" {
						return ""
					}
					return surface
				},
				DataDir: dir,
				Now:     nowFn,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer mgr.Close()

			f.srv.setPlayback(mgr)
			f.srv.setVariants(rs)

			key := fightClub.Key.String()
			detail := "/library/metadata/" + key + "?includeChapters=1&includeMarkers=1&checkFiles=1&includeStreams=1"
			hdr := []string{
				"X-Plex-Product", "Plezy",
				"X-Plex-Version", "2.26.0",
				"X-Plex-Platform", "Android",
				"X-Plex-Device", "AFTKRT",
				"X-Plex-Client-Identifier", "plezy-real-618",
			}

			// 3 detail fetches within 1 s: none may wait on blockProbe or return probed streams.
			for i, step := range []time.Duration{0, 300 * time.Millisecond, 600 * time.Millisecond} {
				if step > 0 {
					clock.Add(int64(step))
				}
				resp := f.get(t, detail, hdr...)
				if resp.Code != 200 {
					t.Fatalf("burst fetch %d: %d %s", i+1, resp.Code, resp.Body.String())
				}
				if strings.Contains(partTag(resp.Body.String()), `accessible=`) {
					t.Fatalf("burst fetch %d claims completeness: %s", i+1, partTag(resp.Body.String()))
				}
			}
			if idx.playSearches.Load() != 0 || probes.Load() != 0 {
				t.Fatalf("browsing burst ran play probe: playSearches=%d probes=%d", idx.playSearches.Load(), probes.Load())
			}
			if tc.rejectSpec {
				// Let the background speculative detail warm-up fail and record its warmMiss.
				select {
				case <-specFailed:
				case <-time.After(5 * time.Second):
					t.Fatal("speculative detail warm-up did not run")
				}
				// Wait for the failed speculative warm-up goroutine to publish its warmMiss and exit.
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if !mgr.Prewarm(context.Background(), &ownerUser, fightClub.Key, core.ClientCaps{Protocol: "plex", Product: "Plezy", ClientID: "plezy-real-618"}, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}

			// 14 s later: Play re-ask must join/start the foreground resolve+probe flight and wait.
			clock.Store(fixedNow.Add(14 * time.Second).UnixNano())
			playDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				playDone <- f.get(t, detail, hdr...)
			}()

			select {
			case <-probeStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("play re-ask at +14 s short-circuited instead of running foreground resolve+probe")
			}
			close(blockProbe)

			playResp := <-playDone
			if playResp.Code != 200 {
				t.Fatalf("play re-ask: %d %s", playResp.Code, playResp.Body.String())
			}
			selected := selectedTag(subtitleTags(playResp.Body.String()))
			if !strings.Contains(selected, `index="2"`) || !strings.Contains(selected, `languageCode="eng"`) {
				t.Fatalf("play re-ask did not select English SRT: %q (body=%s)", selected, playResp.Body.String())
			}
			if !strings.Contains(partTag(playResp.Body.String()), `accessible="1"`) {
				t.Fatalf("play re-ask Part missing accessible=1: %s", partTag(playResp.Body.String()))
			}

			// Immediately following file.mkv (libmpv drops Plex headers, sends only token
			// and User-Agent: libmpv) must adopt the same warm entry with no second
			// resolve or probe.
			searchesBeforePart := idx.playSearches.Load()
			probesBeforePart := probes.Load()
			partReq := httptest.NewRequest(http.MethodGet, "/library/parts/"+key+"/0/file.mkv?X-Plex-Token="+ownerToken, nil)
			partReq.Header.Set("User-Agent", "libmpv")
			partReq.Header.Set("Range", "bytes=0-15")
			partRec := httptest.NewRecorder()
			f.srv.ServeHTTP(partRec, partReq)
			if partRec.Code != http.StatusPartialContent && partRec.Code != http.StatusOK {
				t.Fatalf("file.mkv %d: %s", partRec.Code, partRec.Body.String())
			}
			if idx.playSearches.Load() != searchesBeforePart || probes.Load() != probesBeforePart {
				t.Fatalf("file.mkv repeated work instead of adopting warm flight: searches %d->%d probes %d->%d",
					searchesBeforePart, idx.playSearches.Load(), probesBeforePart, probes.Load())
			}
		})
	}
}

// TestPlezyFirstPlayDoesNotAffectOtherClients ensures non-Plezy clients keep
// their existing detail behaviour on the same 3+1 fetch pattern.
func TestPlezyFirstPlayDoesNotAffectOtherClients(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 25 * time.Minute, BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
		},
	}
	f, _, _ := newIssue459Fixture(t, info, "en")
	var clock atomic.Int64
	clock.Store(fixedNow.UnixNano())
	f.srv.setTestNow(func() time.Time { return time.Unix(0, clock.Load()).UTC() })

	cold := &plezyColdPlayback{fakePlayback: f.playback, probed: info}
	f.srv.setPlayback(cold)
	f.playback.info = estimateInfo

	detail := "/library/metadata/" + fightClub.Key.String() + "?includeChapters=1&includeMarkers=1&checkFiles=1&includeStreams=1"
	hdr := []string{"X-Plex-Product", "Plex Web", "X-Plex-Client-Identifier", "plex-web-618"}
	for _, at := range []time.Duration{0, 300 * time.Millisecond, 900 * time.Millisecond, 14 * time.Second} {
		clock.Store(fixedNow.Add(at).UnixNano())
		if r := f.get(t, detail, hdr...); r.Code != 200 {
			t.Fatalf("Plex Web at +%s: %d", at, r.Code)
		}
	}
	if cold.calls.Load() != 0 {
		t.Fatalf("Plezy play probe ran for Plex Web (%d calls)", cold.calls.Load())
	}
}

func partTag(body string) string {
	start := strings.Index(body, "<Part")
	if start < 0 {
		return ""
	}
	rest := body[start:]
	if end := strings.Index(rest, ">"); end >= 0 {
		return rest[:end+1]
	}
	return rest
}

// Plezy on Android has no episode detail page: a season row plays directly,
// so the play-time fetch is the title's first. Its query shape (Plezy's
// playback read lists includeMarkers first) is play intent on its own, while
// the browsing shape (season-row focus header) never waits.
func TestPlezyFirstPlaySeasonRowPlaybackShape(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 25 * time.Minute, BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
		},
	}
	f, _, _ := newIssue459Fixture(t, info, "en")
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	cold := &plezyColdPlayback{fakePlayback: f.playback, probed: info, block: release, entered: entered}
	f.srv.setPlayback(cold)
	f.playback.info = estimateInfo
	hdr := []string{"X-Plex-Product", "Plezy", "X-Plex-Version", "2.26.0", "X-Plex-Platform", "Android", "X-Plex-Client-Identifier", "plezy-row", "X-Plex-Session-Identifier", "plezy-row-session"}
	base := "/library/metadata/" + fightClub.Key.String()

	if resp := f.get(t, base+"?includeChapters=1&includeMarkers=1&checkFiles=1&includeStreams=1", hdr...); resp.Code != 200 || cold.calls.Load() != 0 {
		t.Fatalf("focus fetch waited or failed: %d calls=%d", resp.Code, cold.calls.Load())
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- f.get(t, base+"?includeMarkers=1&includeChapters=1&checkFiles=1&includeStreams=1", hdr...)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("playback-shaped first fetch did not wait on the probe")
	}
	close(release)
	resp := <-done
	selected := selectedTag(subtitleTags(resp.Body.String()))
	if resp.Code != 200 || !strings.Contains(selected, `index="2"`) {
		t.Fatalf("season-row play did not select the English subtitle: %d %q", resp.Code, selected)
	}
}

type plezyMultiReleaseIndexer struct {
	releases []core.Release
}

func (*plezyMultiReleaseIndexer) Name() string { return "offline-plezy-multi" }
func (i *plezyMultiReleaseIndexer) Search(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	return append([]core.Release(nil), i.releases...), nil
}
func (*plezyMultiReleaseIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	return []byte("nzb-" + r.ID), nil
}

type plezyBytesSource struct {
	id   string
	name string
	data []byte
}

func (s *plezyBytesSource) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *plezyBytesSource) Size() int64       { return int64(len(s.data)) }
func (s *plezyBytesSource) Name() string      { return s.name }
func (s *plezyBytesSource) ReleaseID() string { return s.id }
func (s *plezyBytesSource) Close() error      { return nil }

type plezySlotVariants struct {
	rs  *resolver.Service
	mgr *playback.Manager
}

func (v plezySlotVariants) Variants(ctx context.Context, req core.ResolveRequest, max int) ([]core.ReleaseVariant, error) {
	out, err := v.rs.Variants(ctx, req, max)
	if err == nil && v.mgr != nil {
		out = v.mgr.WithCachedVariantInfo(ctx, req.User, req.Title, out)
	}
	return out, err
}

func (v plezySlotVariants) VersionSlots(ctx context.Context, req core.ResolveRequest, max int, labels bool) ([]core.ReleaseVariant, error) {
	out, err := v.rs.VersionSlots(ctx, req, max, labels)
	if err == nil && labels && v.mgr != nil {
		out = v.mgr.WithCachedVariantInfo(ctx, req.User, req.Title, out)
	}
	return out, err
}

// TestPlezyPlayVersionProbesNonAutoAndKeepsPlayedVersionInOwnSlot verifies #678
// end to end:
//  1. A Plezy Play Version fetch (includeMarkers before includeChapters) probes
//     free-source non-Auto versions so the 4K Media entry carries real streams,
//     a viewer rule-selected English subtitle, exists/accessible flags, and a
//     descriptive title on first play.
//  2. After playing that 4K version (<5% watch) and stopping, the played 4K
//     release stays behind the 4K Media entry with its stored probe (real
//     streams, selected subtitle, descriptive title) instead of being promoted
//     to Auto and refilling 4K with another unprobed 4K release.
func TestPlezyPlayVersionProbesNonAutoAndKeepsPlayedVersionInOwnSlot(t *testing.T) {
	mkvBytes, err := os.ReadFile(filepath.Join("..", "..", "transcode", "testdata", "nativeprobe", "h264_aac_srt_chapters.mkv"))
	if err != nil {
		t.Fatalf("read mkv fixture: %v", err)
	}
	mkvBytes = bytes.Replace(mkvBytes, []byte("jpn"), []byte("eng"), 1)
	dir := t.TempDir()
	cfg := config.Defaults(dir)
	cfg.Quality.Default = "lab1080"
	cfg.Quality.Remote = ""
	cfg.Quality.Rules = nil
	cfg.Quality.Profiles = []core.QualityProfile{{Name: "lab1080", MaxHeight: 1080}}
	cfg.Resolver.SameQualityAlternatives = 0
	cfg.Resolver.OfferHigherVersions = true
	cfg.Resolver.AlternativesPerResolution = true
	cfg.Cache.DisableSpeculation = true
	cfg.Cache.DetailWarmup = "off"

	rel1080Name := "Fjord.Harbor.1999.1080p.WEB-DL.DDP5.1.H.264-AUTOGRP"
	rel4K1Name := "Fjord.Harbor.1999.2160p.WEB-DL.HDR10.DDP5.1.HEVC-FOURK1"
	rel4K2Name := "Fjord.Harbor.1999.2160p.WEB-DL.HDR10.DDP5.1.HEVC-FOURK2"
	rel1080 := core.Release{
		ID: "rel-1080", GUID: "rel-1080", Indexer: "offline-plezy-multi",
		Name: rel1080Name, Size: int64(3) << 30, PubDate: fixedNow.Add(-24 * time.Hour),
		Protocol: core.ProtocolUsenet, NZBURL: "offline", Info: releaseparse.Parse(rel1080Name),
	}
	rel4K1 := core.Release{
		ID: "rel-4k-1", GUID: "rel-4k-1", Indexer: "offline-plezy-multi",
		Name: rel4K1Name, Size: int64(10) << 30, PubDate: fixedNow.Add(-24 * time.Hour),
		Protocol: core.ProtocolTorrent, InfoHash: "1111111111111111111111111111111111111111",
		NZBURL: "offline", Info: releaseparse.Parse(rel4K1Name),
	}
	rel4K2 := core.Release{
		ID: "rel-4k-2", GUID: "rel-4k-2", Indexer: "offline-plezy-multi",
		Name: rel4K2Name, Size: int64(7) << 30, PubDate: fixedNow.Add(-24 * time.Hour),
		Protocol: core.ProtocolUsenet, NZBURL: "offline", Info: releaseparse.Parse(rel4K2Name),
	}
	idx := &plezyMultiReleaseIndexer{releases: []core.Release{rel1080, rel4K1, rel4K2}}
	engine := &shieldLocalEngine{size: int64(8) << 30}
	nowFn := func() time.Time { return fixedNow }

	f := newFixture(func(d *testServerDeps) {
		setDepsConfig(d, func() config.Config { return cfg })
		setDepsNow(d, nowFn)
		d.DisableEnrichment = true
		d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
			return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
		}}
	})
	defer f.srv.Close()

	prefs := trackprefs.New(store.OpenTest(t))
	f.srv.setTrackPreferences(prefs)
	rules := trackprefs.Default()
	rules.SubtitleLanguages = []string{"en"}
	rules.SubtitleMode = "always"
	if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
		t.Fatal(err)
	}

	rs := resolver.New(idx, nil, nil, cfg, resolver.Options{Now: nowFn})
	defer rs.Close()

	db, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	autoProbed := core.MediaInfo{
		Container: "mkv", Duration: fightClub.Runtime, Size: rel1080.Size, BitrateKbps: 6000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
		},
	}
	fourKProbed := core.MediaInfo{
		Container: "mkv", Duration: fightClub.Runtime, Size: rel4K1.Size, BitrateKbps: 18000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, DolbyVision: true, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "ass", Language: "en"},
		},
	}

	mgr, err := playback.New(playback.Deps{
		Catalog:       f.catalog,
		State:         f.state,
		Resolver:      rs,
		Engine:        engine,
		Transcoder:    f.trans,
		SelectProfile: func(c core.ClientCaps, u *core.User, _ string) core.QualityProfile { return rs.ProfileFor(c, u) },
		DB:            db,
		OnVersionProbed: func(_ core.RatingKey, _ core.Release) {
			rs.RefreshDetailVariants()
		},
		OpenCachedDebrid: func(_ context.Context, rel core.Release) (core.MediaSource, error) {
			if rel.ID != rel4K1.ID {
				return nil, core.ErrNotFound
			}
			return &plezyBytesSource{id: rel.ID, name: rel.Name + ".mkv", data: mkvBytes}, nil
		},
		NativeProbe: func(_ context.Context, s core.MediaSource) (*core.MediaInfo, error) {
			if s.ReleaseID() == rel4K1.ID {
				cp := core.CloneMediaInfo(fourKProbed)
				return &cp, nil
			}
			cp := core.CloneMediaInfo(autoProbed)
			return &cp, nil
		},
	}, cfg, playback.Options{
		SessionSurface: func(surface string) string {
			if surface == "plex" {
				return ""
			}
			return surface
		},
		DataDir: dir,
		Now:     nowFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	rs.SetProbedMedia(mgr.CachedReleaseProbe)

	f.srv.setPlayback(mgr)
	f.srv.setVariants(plezySlotVariants{rs: rs, mgr: mgr})

	key := fightClub.Key.String()
	hdr := []string{
		"X-Plex-Product", "Plezy",
		"X-Plex-Version", "2.26.0",
		"X-Plex-Platform", "tvOS",
		"X-Plex-Device", "Apple TV",
		"X-Plex-Client-Identifier", "plezy-678",
		"X-Plex-Session-Identifier", "plezy-678-sess",
	}

	type xmlStream struct {
		StreamType int `xml:"streamType,attr"`
		Selected   int `xml:"selected,attr"`
	}
	type xmlPart struct {
		Exists     *int        `xml:"exists,attr"`
		Accessible *int        `xml:"accessible,attr"`
		Stream     []xmlStream `xml:"Stream"`
	}
	type xmlMedia struct {
		ID              int64     `xml:"id,attr"`
		Title           string    `xml:"title,attr"`
		VideoResolution string    `xml:"videoResolution,attr"`
		VideoCodec      string    `xml:"videoCodec,attr"`
		Part            []xmlPart `xml:"Part"`
	}
	type xmlContainer struct {
		Video []struct {
			Media []xmlMedia `xml:"Media"`
		} `xml:"Video"`
	}
	hasSelectedSubtitle := func(m xmlMedia) bool {
		if len(m.Part) == 0 {
			return false
		}
		for _, st := range m.Part[0].Stream {
			if st.StreamType == 3 && st.Selected == 1 {
				return true
			}
		}
		return false
	}

	// Step 1: Plezy Play Version fetch on a cold title probes Auto and the
	// free-source 4K version so 4K has real streams, selected subtitle,
	// exists/accessible, and a descriptive title before first play.
	playFetch := "/library/metadata/" + key + "?includeMarkers=1&includeChapters=1&checkFiles=1&includeStreams=1"
	r1 := f.get(t, playFetch, hdr...)
	if r1.Code != http.StatusOK {
		t.Fatalf("first play fetch %d: %s", r1.Code, r1.Body.String())
	}
	var mc1 xmlContainer
	if err := xml.Unmarshal(r1.Body.Bytes(), &mc1); err != nil {
		t.Fatalf("unmarshal first response: %v\n%s", err, r1.Body.String())
	}
	if len(mc1.Video) != 1 || len(mc1.Video[0].Media) < 2 {
		t.Fatalf("expected Auto + 4K Media entries, got: %s", r1.Body.String())
	}

	var media4K *xmlMedia
	for i := range mc1.Video[0].Media {
		m := &mc1.Video[0].Media[i]
		if m.ID != int64(fightClub.Key) && strings.Contains(m.Title, "FOURK1") {
			media4K = m
			break
		}
	}
	if media4K == nil {
		t.Fatalf("4K Media entry missing after Play Version fetch:\n%s", r1.Body.String())
	}
	if !hasSelectedSubtitle(*media4K) {
		t.Fatalf("4K Media entry missing rule-selected subtitle on first play:\n%s", r1.Body.String())
	}
	if media4K.Part[0].Exists == nil || *media4K.Part[0].Exists != 1 || media4K.Part[0].Accessible == nil || *media4K.Part[0].Accessible != 1 {
		t.Fatalf("probed 4K Part missing exists/accessible flags:\n%s", r1.Body.String())
	}
	if media4K.Title == "" || media4K.Title == "4K" || !strings.Contains(media4K.Title, " · ") {
		t.Fatalf("4K Media title = %q, want descriptive probed title", media4K.Title)
	}

	// Step 2: Play the explicit 4K version (<5% watch) and stop.
	slot4K := ""
	caps := (&Request{User: &ownerUser, Product: "Plezy", Platform: "tvOS", Device: "Apple TV", ClientID: "plezy-678"}).ClientCaps(0)
	slots, err := rs.VersionSlots(context.Background(), core.ResolveRequest{Title: &fightClub, Client: caps, User: &ownerUser}, core.MaxVariants, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, sl := range slots {
		if sl.Release.ID == rel4K1.ID {
			slot4K = sl.ID
			break
		}
	}
	if slot4K == "" {
		t.Fatalf("could not locate slot selector for %s in %+v", rel4K1.ID, slots)
	}
	sess, err := mgr.PrepareVariant(context.Background(), &ownerUser, fightClub.Key, caps, slot4K)
	if err != nil {
		t.Fatalf("PrepareVariant(slot4K): %v", err)
	}
	if sess.Resolution.Release.ID != rel4K1.ID {
		t.Fatalf("PrepareVariant(slot4K) opened %s, want %s", sess.Resolution.Release.ID, rel4K1.ID)
	}
	mgr.Stop(sess.ID)

	// Step 3: Re-fetch metadata after stop. Auto must stay 1080p (not promoted
	// to 4K), and 4K must stay on rel4K1 with its stored probe (hevc, 4k,
	// rule-selected subtitle, descriptive title) instead of refilling with rel4K2.
	r2 := f.get(t, playFetch, hdr...)
	if r2.Code != http.StatusOK {
		t.Fatalf("post-stop play fetch %d: %s", r2.Code, r2.Body.String())
	}
	var mc2 xmlContainer
	if err := xml.Unmarshal(r2.Body.Bytes(), &mc2); err != nil {
		t.Fatalf("unmarshal post-stop response: %v\n%s", err, r2.Body.String())
	}
	if len(mc2.Video) != 1 || len(mc2.Video[0].Media) < 2 {
		t.Fatalf("expected Auto + 4K Media after stop, got:\n%s", r2.Body.String())
	}
	autoMedia := mc2.Video[0].Media[0]
	if autoMedia.ID != int64(fightClub.Key) || autoMedia.VideoResolution != "1080" || autoMedia.VideoCodec != "h264" {
		t.Fatalf("Auto was promoted/overwritten by explicit 4K play: id=%d res=%s codec=%s title=%q\n%s",
			autoMedia.ID, autoMedia.VideoResolution, autoMedia.VideoCodec, autoMedia.Title, r2.Body.String())
	}
	if !hasSelectedSubtitle(autoMedia) {
		t.Fatalf("Auto lost its own probed subtitle after explicit 4K play:\n%s", r2.Body.String())
	}
	var post4K *xmlMedia
	for i := range mc2.Video[0].Media[1:] {
		m := &mc2.Video[0].Media[i+1]
		if m.ID == media4K.ID {
			post4K = m
			break
		}
	}
	if post4K == nil {
		t.Fatalf("4K Media entry (id=%d) missing after stop:\n%s", media4K.ID, r2.Body.String())
	}
	if post4K.VideoCodec != "hevc" || post4K.VideoResolution != "4k" || !hasSelectedSubtitle(*post4K) {
		t.Fatalf("4K Media lost stored probe after stop: codec=%s res=%s title=%q\n%s",
			post4K.VideoCodec, post4K.VideoResolution, post4K.Title, r2.Body.String())
	}
	if !strings.Contains(post4K.Title, "FOURK1") || strings.Contains(post4K.Title, "FOURK2") {
		t.Fatalf("4K Media was refilled with another release: title=%q", post4K.Title)
	}
}

type plezyStreamingIndexer struct {
	batch1      []core.Release
	batch2      []core.Release
	releaseSlow <-chan struct{}
}

func (*plezyStreamingIndexer) Name() string { return "offline-plezy-stream" }
func (i *plezyStreamingIndexer) Search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	return i.SearchWith(ctx, req, nil)
}
func (i *plezyStreamingIndexer) SearchWith(ctx context.Context, _ core.SearchRequest, arrived func([]core.Release)) ([]core.Release, error) {
	if arrived != nil && len(i.batch1) > 0 {
		arrived(append([]core.Release(nil), i.batch1...))
	}
	if i.releaseSlow != nil {
		select {
		case <-i.releaseSlow:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if arrived != nil && len(i.batch2) > 0 {
		arrived(append([]core.Release(nil), i.batch2...))
	}
	all := make([]core.Release, 0, len(i.batch1)+len(i.batch2))
	all = append(all, i.batch1...)
	all = append(all, i.batch2...)
	return all, nil
}
func (*plezyStreamingIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	return []byte("nzb-" + r.ID), nil
}

// TestPlezyFirstPlayProbesAutoAndVersionsConcurrentlyAsSlotsFill verifies #715:
// on a cold Plezy play-intent fetch, non-Auto free-source version header probes
// run CONCURRENTLY with Auto's probe and begin as soon as each slot fills from
// arriving search batches — BEFORE Auto's probe returns AND BEFORE slow
// straggler indexers complete — so the first Play Version response carries real
// probed streams and the viewer's rule-selected subtitle for 4K.
func TestPlezyFirstPlayProbesAutoAndVersionsConcurrentlyAsSlotsFill(t *testing.T) {
	mkvBytes, err := os.ReadFile(filepath.Join("..", "..", "transcode", "testdata", "nativeprobe", "h264_aac_srt_chapters.mkv"))
	if err != nil {
		t.Fatalf("read mkv fixture: %v", err)
	}
	mkvBytes = bytes.Replace(mkvBytes, []byte("jpn"), []byte("eng"), 1)
	dir := t.TempDir()
	cfg := config.Defaults(dir)
	cfg.Quality.Default = "lab1080"
	cfg.Quality.Remote = ""
	cfg.Quality.Rules = nil
	cfg.Quality.Profiles = []core.QualityProfile{{Name: "lab1080", MaxHeight: 1080}}
	cfg.Resolver.SameQualityAlternatives = 0
	cfg.Resolver.OfferHigherVersions = true
	cfg.Resolver.AlternativesPerResolution = true
	cfg.Cache.DisableSpeculation = true
	cfg.Cache.DetailWarmup = "off"

	rel1080Name := "Fjord.Harbor.1999.1080p.WEB-DL.DDP5.1.H.264-AUTOGRP"
	rel4KName := "Fjord.Harbor.1999.2160p.WEB-DL.HDR10.DDP5.1.HEVC-FOURK1"
	rel720Name := "Fjord.Harbor.1999.720p.WEB-DL.DDP5.1.H.264-SLOW720"
	rel1080 := core.Release{
		ID: "rel-1080-715", GUID: "rel-1080-715", Indexer: "offline-plezy-stream",
		Name: rel1080Name, Size: int64(3) << 30, PubDate: fixedNow.Add(-24 * time.Hour),
		Protocol: core.ProtocolUsenet, NZBURL: "offline", Info: releaseparse.Parse(rel1080Name),
	}
	rel4K := core.Release{
		ID: "rel-4k-715", GUID: "rel-4k-715", Indexer: "offline-plezy-stream",
		Name: rel4KName, Size: int64(10) << 30, PubDate: fixedNow.Add(-24 * time.Hour),
		Protocol: core.ProtocolTorrent, InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		NZBURL: "offline", Info: releaseparse.Parse(rel4KName),
	}
	rel720 := core.Release{
		ID: "rel-720-715", GUID: "rel-720-715", Indexer: "offline-plezy-stream",
		Name: rel720Name, Size: int64(2) << 30, PubDate: fixedNow.Add(-24 * time.Hour),
		Protocol: core.ProtocolUsenet, NZBURL: "offline", Info: releaseparse.Parse(rel720Name),
	}

	releaseSlow := make(chan struct{})
	releaseAuto := make(chan struct{})
	autoProbeEntered := make(chan struct{}, 1)
	fourKOpened := make(chan struct{}, 1)

	idx := &plezyStreamingIndexer{
		batch1:      []core.Release{rel1080, rel4K},
		batch2:      []core.Release{rel720},
		releaseSlow: releaseSlow,
	}
	engine := &shieldLocalEngine{size: int64(8) << 30}
	nowFn := func() time.Time { return fixedNow }

	f := newFixture(func(d *testServerDeps) {
		setDepsConfig(d, func() config.Config { return cfg })
		setDepsNow(d, nowFn)
		d.DisableEnrichment = true
		d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
			return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
		}}
	})
	defer f.srv.Close()

	prefs := trackprefs.New(store.OpenTest(t))
	f.srv.setTrackPreferences(prefs)
	rules := trackprefs.Default()
	rules.SubtitleLanguages = []string{"en"}
	rules.SubtitleMode = "always"
	if err := prefs.PutRules(context.Background(), ownerUser.ID, rules); err != nil {
		t.Fatal(err)
	}

	rs := resolver.New(idx, nil, nil, cfg, resolver.Options{Now: nowFn})
	defer rs.Close()

	db, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	autoProbed := core.MediaInfo{
		Container: "mkv", Duration: fightClub.Runtime, Size: rel1080.Size, BitrateKbps: 6000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, Default: true},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
		},
	}

	mgr, err := playback.New(playback.Deps{
		Catalog:       f.catalog,
		State:         f.state,
		Resolver:      rs,
		Engine:        engine,
		Transcoder:    f.trans,
		SelectProfile: func(c core.ClientCaps, u *core.User, _ string) core.QualityProfile { return rs.ProfileFor(c, u) },
		DB:            db,
		OnVersionProbed: func(_ core.RatingKey, _ core.Release) {
			rs.RefreshDetailVariants()
		},
		OpenCachedDebrid: func(_ context.Context, rel core.Release) (core.MediaSource, error) {
			if rel.ID != rel4K.ID {
				return nil, core.ErrNotFound
			}
			select {
			case fourKOpened <- struct{}{}:
			default:
			}
			return &plezyBytesSource{id: rel.ID, name: rel.Name + ".mkv", data: mkvBytes}, nil
		},
		NativeProbe: func(pctx context.Context, s core.MediaSource) (*core.MediaInfo, error) {
			select {
			case autoProbeEntered <- struct{}{}:
			default:
			}
			select {
			case <-releaseAuto:
			case <-pctx.Done():
				return nil, pctx.Err()
			}
			cp := core.CloneMediaInfo(autoProbed)
			return &cp, nil
		},
	}, cfg, playback.Options{
		SessionSurface: func(surface string) string {
			if surface == "plex" {
				return ""
			}
			return surface
		},
		DataDir: dir,
		Now:     nowFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	rs.SetProbedMedia(mgr.CachedReleaseProbe)

	f.srv.setPlayback(mgr)
	f.srv.setVariants(plezySlotVariants{rs: rs, mgr: mgr})

	key := fightClub.Key.String()
	hdr := []string{
		"X-Plex-Product", "Plezy",
		"X-Plex-Version", "2.26.0",
		"X-Plex-Platform", "tvOS",
		"X-Plex-Device", "Apple TV",
		"X-Plex-Client-Identifier", "plezy-715",
		"X-Plex-Session-Identifier", "plezy-715-sess",
	}
	playFetch := "/library/metadata/" + key + "?includeMarkers=1&includeChapters=1&checkFiles=1&includeStreams=1"

	respCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		respCh <- f.get(t, playFetch, hdr...)
	}()

	// 4K header probe MUST open while BOTH the slow indexer AND Auto's probe
	// are still blocked: proves concurrent execution and incremental launch.
	select {
	case <-fourKOpened:
	case <-time.After(4 * time.Second):
		close(releaseSlow)
		close(releaseAuto)
		t.Fatal("4K header probe did not start concurrently before slow indexer and Auto finished")
	}
	select {
	case <-autoProbeEntered:
	case <-time.After(4 * time.Second):
		close(releaseSlow)
		close(releaseAuto)
		t.Fatal("Auto probe never started")
	}

	// Unblock Auto and the slow indexer; response must carry probed 4K with
	// rule-selected English subtitle on first play.
	close(releaseAuto)
	close(releaseSlow)

	resp := <-respCh
	if resp.Code != http.StatusOK {
		t.Fatalf("play fetch %d: %s", resp.Code, resp.Body.String())
	}
	type xmlStream struct {
		StreamType int `xml:"streamType,attr"`
		Selected   int `xml:"selected,attr"`
	}
	type xmlPart struct {
		Exists     *int        `xml:"exists,attr"`
		Accessible *int        `xml:"accessible,attr"`
		Stream     []xmlStream `xml:"Stream"`
	}
	type xmlMedia struct {
		ID    int64     `xml:"id,attr"`
		Title string    `xml:"title,attr"`
		Part  []xmlPart `xml:"Part"`
	}
	type xmlContainer struct {
		Video []struct {
			Media []xmlMedia `xml:"Media"`
		} `xml:"Video"`
	}
	var mc xmlContainer
	if err := xml.Unmarshal(resp.Body.Bytes(), &mc); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, resp.Body.String())
	}
	var media4K *xmlMedia
	for i := range mc.Video[0].Media {
		m := &mc.Video[0].Media[i]
		if m.ID != int64(fightClub.Key) && strings.Contains(m.Title, "FOURK1") {
			media4K = m
			break
		}
	}
	if media4K == nil {
		t.Fatalf("4K Media entry missing:\n%s", resp.Body.String())
	}
	hasSelectedSub := false
	for _, st := range media4K.Part[0].Stream {
		if st.StreamType == 3 && st.Selected == 1 {
			hasSelectedSub = true
			break
		}
	}
	if !hasSelectedSub {
		t.Fatalf("4K Media entry missing rule-selected subtitle on first play:\n%s", resp.Body.String())
	}
}
