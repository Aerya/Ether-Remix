package livetv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestFoldDuplicateChannelName(t *testing.T) {
	sameCases := []string{
		"FOX News",
		"US | FOX News HD",
		"CA: FOX News [720p60]",
		"FOX News (UK) 1080p60",
		"FOX News | FHD | 60fps",
		"FOX News 4K UHD HEVC",
		"[US] FOX News (1080p)",
	}
	for _, raw := range sameCases {
		if got := foldDuplicateChannelName(raw); got != "foxnews" {
			t.Fatalf("foldDuplicateChannelName(%q) = %q, want %q", raw, got, "foxnews")
		}
	}

	if a, b := foldDuplicateChannelName("FOX News"), foldDuplicateChannelName("FOX News +1"); a == b {
		t.Fatalf("timeshift channel collided: %q == %q", a, b)
	}
	if a, b := foldDuplicateChannelName("ESPN HD"), foldDuplicateChannelName("ESPN+ HD"); a == b {
		t.Fatalf("plus service collided: %q == %q", a, b)
	}
	if got := foldDuplicateChannelName("This Is Us HD"); got != "thisisus" {
		t.Fatalf("title-case word stripped as region: got %q, want %q", got, "thisisus")
	}
	if got := foldDuplicateChannelName("1080"); got != "1080" {
		t.Fatalf("numeric-only channel name folded to %q, want %q", got, "1080")
	}
}

func TestAutoBackupsImportMatchingSameAndCrossSource(t *testing.T) {
	now := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	ctx := context.Background()
	s := testService(t, Options{Now: func() time.Time { return now }})

	buildXMLTV := func(schedules map[string][]string) string {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tv>`)
		for chID, titles := range schedules {
			fmt.Fprintf(&b, `<channel id=%q><display-name>%s</display-name></channel>`, chID, chID)
			for i, title := range titles {
				start := now.Add(time.Duration(i) * time.Hour).Format("20060102150405 +0000")
				stop := now.Add(time.Duration(i+1) * time.Hour).Format("20060102150405 +0000")
				fmt.Fprintf(&b, `<programme start=%q stop=%q channel=%q><title>%s</title></programme>`, start, stop, chID, title)
			}
		}
		b.WriteString(`</tv>`)
		return b.String()
	}

	srcUS := Source{
		ID:   "src-us",
		Name: "US Provider",
		Playlist: strings.Join([]string{
			"#EXTM3U",
			`#EXTINF:-1 tvg-chno="2113" tvg-id="FOXNews.us" group-title="US | News",US | FOX News 1080p60`,
			"https://us.invalid/fox-2113.m3u8",
			`#EXTINF:-1 tvg-chno="1873" tvg-id="FOXNews.us" group-title="CA | News",CA | FOX News 720p60`,
			"https://us.invalid/fox-1873.m3u8",
			`#EXTINF:-1 tvg-chno="310" tvg-id="us.skynews" group-title="US | News",Sky News FHD`,
			"https://us.invalid/sky-310.m3u8",
			`#EXTINF:-1 tvg-chno="410" tvg-id="us.sports" group-title="US | Sports",Sports Net HD`,
			"https://us.invalid/sports-410.m3u8",
		}, "\n") + "\n",
		Guide: buildXMLTV(map[string][]string{
			"us.skynews": {"Morning Briefing", "World Desk", "Live Report", "Afternoon Edition", "Prime Hour", "Special Report"},
			"us.sports":  {"Hockey Live", "Baseball Tonight", "Sports Desk", "Golf Weekly", "Tennis Open", "Late Highlights"},
		}),
	}

	srcCA := Source{
		ID:   "src-ca",
		Name: "CA Provider",
		Playlist: strings.Join([]string{
			"#EXTM3U",
			`#EXTINF:-1 tvg-chno="2900" tvg-id="foxnews.us" group-title="CA | News",FOX News [CA] (720p)`,
			"https://ca.invalid/fox-2900.m3u8",
			`#EXTINF:-1 tvg-chno="1310" tvg-id="uk.skynews" group-title="UK | News",UK: Sky News 720p`,
			"https://ca.invalid/sky-1310.m3u8",
			`#EXTINF:-1 tvg-chno="1410" tvg-id="ca.sports" group-title="CA | Sports",CA | Sports Net 720p`,
			"https://ca.invalid/sports-1410.m3u8",
		}, "\n") + "\n",
		// 5 of 6 programmes align for Sky News (83.3% >= 80%), while only 2 of 6 align for Sports Net (33.3% < 80%).
		Guide: buildXMLTV(map[string][]string{
			"uk.skynews": {"Morning Briefing", "World Desk", "Live Report", "Afternoon Edition", "Prime Hour", "UK Weather"},
			"ca.sports":  {"Hockey Live", "Curling Classic", "CFL Rewind", "Lacrosse Night", "Skiing World", "Late Highlights"},
		}),
	}

	if _, err := s.ImportSource(ctx, srcUS); err != nil {
		t.Fatalf("import US source: %v", err)
	}
	if _, err := s.ImportSource(ctx, srcCA); err != nil {
		t.Fatalf("import CA source: %v", err)
	}

	all, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 7 {
		t.Fatalf("expected all 7 standalone channels in lineup, got %d", len(all))
	}
	allowed, err := s.AllowedChannels(ctx, &core.User{ID: 1})
	if err != nil || len(allowed) != 7 {
		t.Fatalf("expected all 7 standalone channels visible to viewers, got %d (err=%v)", len(allowed), err)
	}

	byNum := map[string]Channel{}
	for _, c := range all {
		if c.MergedInto != "" {
			t.Fatalf("duplicate channel %s (%s) was hidden instead of staying standalone", c.Number, c.Name)
		}
		byNum[c.Number] = c
	}

	ch2113 := byNum["2113"]
	ch1873 := byNum["1873"]
	ch2900 := byNum["2900"]
	if len(ch2113.StreamSources) != 3 {
		t.Fatalf("#2113 StreamSources = %+v, want own primary + 2 automatic backups", ch2113.StreamSources)
	}
	if ch2113.StreamSources[0].URL != "https://us.invalid/fox-2113.m3u8" || ch2113.StreamSources[0].AutoFrom != "" {
		t.Fatalf("#2113 did not keep its own primary first: %+v", ch2113.StreamSources[0])
	}
	// Same-source 720p60 (#1873) precedes cross-source 720p (#2900) when health and resolution match.
	if ch2113.StreamSources[1].AutoFrom != ch1873.ID || ch2113.StreamSources[1].AutoNumber != "1873" {
		t.Fatalf("#2113 first auto backup = %+v, want #1873 (%s)", ch2113.StreamSources[1], ch1873.ID)
	}
	if ch2113.StreamSources[2].AutoFrom != ch2900.ID || ch2113.StreamSources[2].AutoNumber != "2900" || ch2113.StreamSources[2].AuthSourceID != "src-ca" {
		t.Fatalf("#2113 second auto backup = %+v, want #2900 (%s) with AuthSourceID=src-ca", ch2113.StreamSources[2], ch2900.ID)
	}

	if len(ch1873.StreamSources) != 3 || ch1873.StreamSources[0].URL != "https://us.invalid/fox-1873.m3u8" {
		t.Fatalf("#1873 did not keep its own primary first: %+v", ch1873.StreamSources)
	}

	// 6h schedule alignment >= 80% links Sky News across sources despite distinct tvg-ids.
	skyUS, skyUK := byNum["310"], byNum["1310"]
	if len(skyUS.StreamSources) != 2 || skyUS.StreamSources[1].AutoFrom != skyUK.ID {
		t.Fatalf("Sky News 6h schedule match failed: %+v", skyUS.StreamSources)
	}
	if len(skyUK.StreamSources) != 2 || skyUK.StreamSources[1].AutoFrom != skyUS.ID {
		t.Fatalf("Sky News reverse 6h schedule match failed: %+v", skyUK.StreamSources)
	}

	// < 80% overlap does not link Sports Net.
	sportsUS, sportsCA := byNum["410"], byNum["1410"]
	if len(sportsUS.StreamSources) != 1 || hasAutoStreamSources(sportsUS) || len(sportsCA.StreamSources) != 1 || hasAutoStreamSources(sportsCA) {
		t.Fatalf("channels with <80%% 6h programme overlap were linked: us=%+v ca=%+v", sportsUS.StreamSources, sportsCA.StreamSources)
	}
}

func TestAutoBackupsPrecedenceHealthResolutionAndSameSource(t *testing.T) {
	now := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	ctx := context.Background()
	s := testService(t, Options{Now: func() time.Time { return now }})

	srcA := Source{
		ID:   "src-a",
		Name: "Source A",
		Playlist: strings.Join([]string{
			"#EXTM3U",
			`#EXTINF:-1 tvg-chno="2113" tvg-id="FOXNews.us" group-title="US | News",FOX News 1080p60`,
			"https://a.invalid/2113.ts",
			`#EXTINF:-1 tvg-chno="102" tvg-id="FOXNews.us" group-title="US | News",FOX News 1080p`,
			"https://a.invalid/102.ts",
			`#EXTINF:-1 tvg-chno="1873" tvg-id="FOXNews.us" group-title="CA | News",FOX News 720p60`,
			"https://a.invalid/1873.ts",
			`#EXTINF:-1 tvg-chno="104" tvg-id="FOXNews.us" group-title="US | News",FOX News SD`,
			"https://a.invalid/104.ts",
		}, "\n") + "\n",
	}
	srcB := Source{
		ID:   "src-b",
		Name: "Source B",
		Playlist: strings.Join([]string{
			"#EXTM3U",
			`#EXTINF:-1 tvg-chno="101" tvg-id="FOXNews.us" group-title="US | News",FOX News 1080p`,
			"https://b.invalid/101.ts",
		}, "\n") + "\n",
	}
	if _, err := s.ImportSource(ctx, srcA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSource(ctx, srcB); err != nil {
		t.Fatal(err)
	}

	all, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byNum := map[string]Channel{}
	for _, c := range all {
		byNum[c.Number] = c
	}
	ch2113 := byNum["2113"]
	ch102 := byNum["102"]

	wantURLs := []string{
		"https://a.invalid/2113.ts", // own primary first
		"https://a.invalid/102.ts",  // 1080p, same source
		"https://b.invalid/101.ts",  // 1080p, cross source
		"https://a.invalid/1873.ts", // 720p60, closer to 1080p60 than SD
		"https://a.invalid/104.ts",  // SD
	}
	gotCands := s.orderedSourceCandidates(ch2113)
	if len(gotCands) != len(wantURLs) {
		t.Fatalf("candidates = %+v, want %d", gotCands, len(wantURLs))
	}
	for i, want := range wantURLs {
		if gotCands[i].URL != want {
			t.Fatalf("candidate[%d] = %s, want %s (full=%+v)", i, gotCands[i].URL, want, gotCands)
		}
	}

	// Record stalls on #102's own primary input: runtime ordering demotes #102 behind
	// cleaner backups while keeping #2113's own primary first.
	s.noteSourceEvidence(ch102.ID, ch102.StreamSources[0], SourceEvidence{
		Media:  10 * time.Minute,
		Stalls: 6,
	})
	gotAfterStalls := s.orderedSourceCandidates(ch2113)
	if gotAfterStalls[0].URL != "https://a.invalid/2113.ts" {
		t.Fatalf("own primary lost first slot: %+v", gotAfterStalls)
	}
	if gotAfterStalls[1].URL != "https://b.invalid/101.ts" {
		t.Fatalf("clean 1080p backup did not advance ahead of stalling #102: %+v", gotAfterStalls)
	}
	if gotAfterStalls[len(gotAfterStalls)-1].URL != "https://a.invalid/102.ts" {
		t.Fatalf("stalling #102 was not ranked behind zero-stall backups: %+v", gotAfterStalls)
	}

	// Even if a backup played during an outage and primary recovers on a check,
	// own primary remains first.
	s.recordSourceHealth(ch2113.ID, gotAfterStalls[1], nil, true)
	s.recordSourceHealth(ch2113.ID, ch2113.StreamSources[0], nil, false)
	if recovered := s.orderedSourceCandidates(ch2113); recovered[0].URL != "https://a.invalid/2113.ts" {
		t.Fatalf("recovered own primary did not stay ahead of previously played auto backup: %+v", recovered)
	}

	// Probe-cached heights rank unlabelled duplicates by closeness to target height.
	s.probeMu.Lock()
	if s.probeCache == nil {
		s.probeCache = map[string]liveProbeEntry{}
	}
	s.probeCache["https://a.invalid/104.ts"] = liveProbeEntry{
		info:    core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Width: 1920, Height: 1080}}},
		expires: now.Add(time.Hour),
	}
	s.probeMu.Unlock()
	probedOrder := s.orderedSourceCandidates(ch2113)
	// #104 probed at 1080p on same source src-a now outranks cross-source 1080p #101 and 720p #1873.
	if probedOrder[1].URL != "https://a.invalid/104.ts" {
		t.Fatalf("probe-cached 1080p height on same source did not outrank cross-source/720p backups: %+v", probedOrder)
	}
}

func TestAutoBackupsOwnerControlAndSuppression(t *testing.T) {
	now := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	ctx := context.Background()
	s := testService(t, Options{Now: func() time.Time { return now }})

	src := Source{
		ID:   "src-main",
		Name: "Main",
		Playlist: strings.Join([]string{
			"#EXTM3U",
			`#EXTINF:-1 tvg-chno="2113" tvg-id="FOXNews.us" group-title="US | News",US | FOX News 1080p60`,
			"https://main.invalid/2113.m3u8",
			`#EXTINF:-1 tvg-chno="1873" tvg-id="FOXNews.us" group-title="CA | News",CA | FOX News 720p60`,
			"https://main.invalid/1873.m3u8",
			`#EXTINF:-1 tvg-chno="1950" tvg-id="FOXNews.us" group-title="UK | News",UK | FOX News 720p`,
			"https://main.invalid/1950.m3u8",
		}, "\n") + "\n",
	}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}

	all, _ := s.Channels(ctx)
	byNum := map[string]Channel{}
	for _, c := range all {
		byNum[c.Number] = c
	}
	ch2113 := byNum["2113"]
	ch1873 := byNum["1873"]
	ch1950 := byNum["1950"]
	if len(ch2113.StreamSources) != 3 {
		t.Fatalf("initial #2113 sources = %+v, want 3", ch2113.StreamSources)
	}

	// Owner removes #1873 auto backup and adds a custom backup source ahead of #1950.
	var edited []StreamSource
	for _, input := range ch2113.StreamSources {
		if input.AutoFrom == ch1873.ID {
			continue
		}
		if input.AutoFrom == "" {
			edited = append(edited, input)
			edited = append(edited, StreamSource{
				ID:      "owner-custom",
				Name:    "Owner Custom Backup",
				URL:     "https://custom.invalid/fox.m3u8",
				Enabled: true,
			})
			continue
		}
		edited = append(edited, input)
	}
	for i := range edited {
		edited[i].Priority = i
	}
	ch2113.StreamSources = edited
	saved, err := s.SaveChannel(ctx, ch2113)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.OwnerStreamSources || len(saved.SuppressedAutoBackups) != 1 || saved.SuppressedAutoBackups[0] != ch1873.ID {
		t.Fatalf("owner suppression/override state not persisted: %+v", saved)
	}

	// Re-importing the source and re-running MatchGuides must keep #1873 removed and preserve owner order.
	src.Playlist = strings.ReplaceAll(src.Playlist, "/1950.m3u8", "/1950-refreshed.m3u8")
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MatchGuides(ctx, GuideMatchRequest{}); err != nil {
		t.Fatal(err)
	}

	after, err := s.Channel(ctx, ch2113.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.StreamSources) != 3 {
		t.Fatalf("after refresh #2113 sources = %+v, want 3 (own + owner-custom + #1950)", after.StreamSources)
	}
	if after.StreamSources[0].URL != "https://main.invalid/2113.m3u8" ||
		after.StreamSources[1].ID != "owner-custom" ||
		after.StreamSources[2].AutoFrom != ch1950.ID ||
		after.StreamSources[2].URL != "https://main.invalid/1950-refreshed.m3u8" {
		t.Fatalf("owner order or refreshed surviving auto backup broken: %+v", after.StreamSources)
	}
	for _, input := range after.StreamSources {
		if input.AutoFrom == ch1873.ID {
			t.Fatalf("removed auto backup #1873 was recreated on refresh: %+v", after.StreamSources)
		}
	}
}

func TestAutoBackupsFailoverKeepsRenditionAndLogsChannelNumbers(t *testing.T) {
	now := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	ctx := context.Background()

	var primaryDown atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2113.m3u8":
			if primaryDown.Load() {
				http.Error(w, "gone", http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\n2113-bad.ts\n")
		case "/2113-bad.ts":
			http.NotFound(w, r)
		case "/1873.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\n1873-ok.ts\n")
		case "/1873-ok.ts":
			fmt.Fprint(w, "BACKUP_1873_SEGMENT")
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()

	var logBuf lockedBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := testService(t, Options{
		HTTPClient: up.Client(),
		Logger:     logger,
		Now:        func() time.Time { return now },
	})

	src := Source{
		ID:   "src-news",
		Name: "News Lineup",
		Playlist: strings.Join([]string{
			"#EXTM3U",
			`#EXTINF:-1 tvg-chno="2113" tvg-id="FOXNews.us" group-title="US | News",FOX News 1080p60`,
			up.URL + "/2113.m3u8",
			`#EXTINF:-1 tvg-chno="1873" tvg-id="FOXNews.us" group-title="CA | News",FOX News 720p60`,
			up.URL + "/1873.m3u8",
		}, "\n") + "\n",
	}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}

	all, _ := s.Channels(ctx)
	var ch2113 Channel
	for _, c := range all {
		if c.Number == "2113" {
			ch2113 = c
			break
		}
	}
	if len(ch2113.StreamSources) != 2 || ch2113.StreamSources[1].AutoNumber != "1873" {
		t.Fatalf("#2113 automatic backup not wired: %+v", ch2113.StreamSources)
	}

	// 1. Owned passthrough failover logs both channel numbers (#2113 and #1873).
	viewer := &core.User{ID: 1}
	sessInfo, err := s.Tune(ctx, viewer, ch2113.ID, core.ClientCaps{})
	if err != nil {
		t.Fatalf("tune #2113: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	pl := httptest.NewRecorder()
	s.ServePlaylist(pl, req, sessInfo.ID, viewer)
	if pl.Code != http.StatusOK {
		t.Fatalf("playlist status=%d body=%s", pl.Code, pl.Body.String())
	}
	seg := httptest.NewRecorder()
	s.ServeRelaySegment(seg, req, sessInfo.ID, 0, viewer)
	if seg.Code != http.StatusOK || seg.Body.String() != "BACKUP_1873_SEGMENT" {
		t.Fatalf("backup segment status=%d body=%q", seg.Code, seg.Body.String())
	}
	s.Stop(ctx, sessInfo.ID, viewer)

	logs := logBuf.String()
	if !strings.Contains(logs, "from_channel=#2113") || !strings.Contains(logs, "to_channel=#1873") {
		t.Fatalf("passthrough failover log missing duplicate channel numbers:\n%s", logs)
	}

	// 2. Steady broadcast planner failover from 1080p60 (#2113) to 720p60 (#1873)
	// keeps the 1920x1080@60 decoder/output format unchanged and logs both channel numbers.
	logBuf.Reset()
	s.sourceMu.Lock()
	s.sourceHealth = nil
	s.sourceMu.Unlock()
	primaryDown.Store(false)

	shape := steadyShapeFor(&core.MediaInfo{
		Streams: []core.StreamInfo{
			{Type: "video", Width: 1920, Height: 1080, FrameRate: 60},
			{Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
		},
	})
	if shape.format.Width != 1920 || shape.format.Height != 1080 || shape.format.FPSNum != 60 || shape.format.FPSDen != 1 {
		t.Fatalf("unexpected 1080p60 shape: %+v", shape.format)
	}

	var mu sync.Mutex
	var decoderFormats []transcode.RawFormat
	runner := &broadcastFakeRunner{
		decoder: func(_ context.Context, req transcode.DecoderRequest) (transcode.Decoder, error) {
			mu.Lock()
			decoderFormats = append(decoderFormats, req.Format)
			mu.Unlock()
			return &broadcastFakeDecoder{format: req.Format, origin: now, closed: make(chan struct{})}, nil
		},
	}

	planner := &providerBroadcastPlanner{
		s:          s,
		channel:    ch2113,
		format:     shape.format,
		runner:     runner,
		ladder:     newSteadyLadder(),
		guideFrom:  now.Add(-time.Minute),
		guideUntil: now.Add(time.Hour),
	}

	dec1, rel1, err := planner.open(ctx)
	if err != nil {
		t.Fatalf("initial steady open (#2113): %v", err)
	}
	_ = dec1.Close()

	// #2113 fails upstream; planner.failed + planner.open switches to #1873 (720p60)
	// without restarting any encoder or altering p.format (1920x1080@60).
	primaryDown.Store(true)
	_ = planner.failed(errSteadyFirstFrame)

	dec2, rel2, err := planner.open(ctx)
	if err != nil {
		t.Fatalf("backup steady open (#1873): %v", err)
	}
	planner.commitReplacement()
	if rel1 != nil {
		rel1()
	}
	_ = dec2.Close()
	if rel2 != nil {
		rel2()
	}
	planner.mu.Lock()
	activeSrc := StreamSource{}
	if planner.attempt != nil {
		activeSrc = planner.attempt.src
	}
	link := planner.link
	planner.link = nil
	planner.attempt = nil
	planner.mu.Unlock()
	link.close(s)

	if activeSrc.AutoNumber != "1873" {
		t.Fatalf("steady planner did not fail over to #1873: active=%+v", activeSrc)
	}
	if planner.format != shape.format {
		t.Fatalf("steady output format mutated on 720p60 backup: got %+v, want %+v", planner.format, shape.format)
	}
	mu.Lock()
	formatsCopy := append([]transcode.RawFormat(nil), decoderFormats...)
	mu.Unlock()
	if len(formatsCopy) != 2 || formatsCopy[0] != shape.format || formatsCopy[1] != shape.format {
		t.Fatalf("steady decoders were not normalized to constant 1080p60 format: %+v", formatsCopy)
	}
	steadyLogs := logBuf.String()
	if !strings.Contains(steadyLogs, "from_channel=#2113") || !strings.Contains(steadyLogs, "to_channel=#1873") {
		t.Fatalf("steady failover log missing duplicate channel numbers:\n%s", steadyLogs)
	}
}

func TestAutoBackupsRequestPathCostDoesNotScaleWithChannelCount(t *testing.T) {
	now := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)

	measure := func(channelPairs int) (listQueries, getQueries, saveQueries int64) {
		t.Helper()
		db, err := store.OpenMemory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		s := testService(t, Options{DB: db, Now: func() time.Time { return now }})
		var lines []string
		lines = append(lines, "#EXTM3U")
		for i := 0; i < channelPairs; i++ {
			tvg := fmt.Sprintf("net-%d.us", i)
			lines = append(lines,
				fmt.Sprintf(`#EXTINF:-1 tvg-chno="%d" tvg-id="%s" group-title="US | General",US | Network %d 1080p60`, 1000+i, tvg, i),
				fmt.Sprintf("https://us.invalid/ch-%d.m3u8", i),
				fmt.Sprintf(`#EXTINF:-1 tvg-chno="%d" tvg-id="%s" group-title="CA | General",CA | Network %d 720p60`, 5000+i, tvg, i),
				fmt.Sprintf("https://ca.invalid/ch-%d.m3u8", i),
			)
		}
		if _, err := s.ImportSource(context.Background(), Source{
			ID:       "src-scale",
			Name:     "Scale Test",
			Playlist: strings.Join(lines, "\n") + "\n",
		}); err != nil {
			t.Fatal(err)
		}

		// Warm snapshot once as normal steady-state operation does after import.
		all, err := s.Channels(context.Background())
		if err != nil || len(all) != channelPairs*2 {
			t.Fatalf("channels = %d, err = %v", len(all), err)
		}
		target := all[0]
		if !hasAutoStreamSources(target) {
			t.Fatalf("expected auto backup on %s: %+v", target.Name, target.StreamSources)
		}

		ctxList, statsList := store.WithQueryStats(context.Background())
		if _, err := s.AllowedChannels(ctxList, &core.User{ID: 1}); err != nil {
			t.Fatal(err)
		}
		for _, st := range statsList.Statements() {
			if strings.Contains(st.SQL, "livetv_programmes") {
				t.Fatalf("lineup request queried livetv_programmes: %s", st.SQL)
			}
		}

		ctxGet, statsGet := store.WithQueryStats(context.Background())
		got, err := s.Channel(ctxGet, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.orderedSourceCandidates(got)
		for _, st := range statsGet.Statements() {
			if strings.Contains(st.SQL, "livetv_programmes") {
				t.Fatalf("channel lookup/candidate order queried livetv_programmes: %s", st.SQL)
			}
		}

		// Owner removes the automatic backup via SaveChannel: cost must stay O(1)
		// and never scan livetv_programmes.
		got.StreamSources = got.StreamSources[:1]
		ctxSave, statsSave := store.WithQueryStats(context.Background())
		if _, err := s.SaveChannel(ctxSave, got); err != nil {
			t.Fatal(err)
		}
		for _, st := range statsSave.Statements() {
			if strings.Contains(st.SQL, "livetv_programmes") {
				t.Fatalf("SaveChannel queried livetv_programmes: %s", st.SQL)
			}
		}

		return statsList.Queries(), statsGet.Queries(), statsSave.Queries()
	}

	smallList, smallGet, smallSave := measure(5)
	largeList, largeGet, largeSave := measure(120)

	if largeList > smallList+2 {
		t.Fatalf("AllowedChannels scaled with channel count: small=%d large=%d", smallList, largeList)
	}
	if largeGet > smallGet+2 {
		t.Fatalf("Channel+orderedSourceCandidates scaled with channel count: small=%d large=%d", smallGet, largeGet)
	}
	if largeSave > smallSave+2 {
		t.Fatalf("SaveChannel scaled with channel count: small=%d large=%d", smallSave, largeSave)
	}
}

type autoBackupRaceDecoder struct {
	format  transcode.RawFormat
	video   []byte
	gate    chan struct{} // when non-nil, blocks before first picture until closed
	stall   atomic.Bool
	unblock chan struct{} // when closed while stalled, resumes producing frames
	tick    int64
	closed  atomic.Bool
	closeAt atomic.Int64
	done    chan struct{}
	once    sync.Once
}

func (d *autoBackupRaceDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if d.tick == 0 && d.gate != nil {
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, ctx.Err()
		case <-d.done:
			return transcode.RawFrame{}, io.EOF
		case <-d.gate:
		}
	}
	for d.stall.Load() {
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, ctx.Err()
		case <-d.done:
			return transcode.RawFrame{}, io.EOF
		case <-d.unblock:
			d.stall.Store(false)
		}
	}
	fps := d.format.FPSNum / d.format.FPSDen
	frameDur := time.Second / time.Duration(fps)
	if d.tick > 0 {
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, ctx.Err()
		case <-d.done:
			return transcode.RawFrame{}, io.EOF
		case <-time.After(frameDur):
		}
	}
	samples := tickSamples(d.format, d.tick)
	pts := time.Duration(d.tick) * frameDur
	d.tick++
	return transcode.RawFrame{
		Video:          d.video,
		PCM:            make([]byte, samples*d.format.Channels*2),
		SourcePTS:      pts,
		SourcePTSValid: true,
	}, nil
}

func (d *autoBackupRaceDecoder) Close() error {
	d.once.Do(func() {
		d.closed.Store(true)
		d.closeAt.Store(time.Now().UnixNano())
		close(d.done)
	})
	return nil
}

func TestAutoBackupsFailoverUsesParallelReplacementRace(t *testing.T) {
	if os.Getenv("ETHER_TEST_POSTGRES") != "" {
		t.Skip("synctest bubbles cannot own PostgreSQL connections; the race is store-independent")
	}
	runCase := func(t *testing.T, maxConns int, recoverPrimary bool) {
		t.Helper()
		db, err := store.OpenMemory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		synctest.Test(t, func(t *testing.T) {
			var logBuf lockedBuffer
			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			s := testService(t, Options{
				DB:     db,
				Logger: logger,
				Now:    time.Now,
			})
			defer func() {
				s.Close()
				synctest.Wait()
			}()
			s.inputRelay.host = "127.0.0.1:1"
			s.inputRelay.grants = map[[sha256.Size]byte]*inputGrant{}
			s.inputRelay.bySess = map[*session]*inputGrant{}

			ctx := context.Background()
			if _, err := s.ImportSource(ctx, Source{
				ID:             "src-news",
				Name:           "News Lineup",
				MaxConnections: maxConns,
				Playlist: strings.Join([]string{
					"#EXTM3U",
					`#EXTINF:-1 tvg-chno="2113" tvg-id="FOXNews.us" group-title="US | News",FOX News 1080p60`,
					"https://provider.invalid/2113.ts",
					`#EXTINF:-1 tvg-chno="1873" tvg-id="FOXNews.us" group-title="CA | News",FOX News 720p60`,
					"https://provider.invalid/1873.ts",
				}, "\n") + "\n",
			}); err != nil {
				t.Fatal(err)
			}
			e, err := s.sourceEntry(ctx, "src-news")
			if err != nil {
				t.Fatal(err)
			}
			e.limit = maxConns
			s.setSourceEntry("src-news", e)

			all, err := s.Channels(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var ch2113 Channel
			for _, c := range all {
				if c.Number == "2113" {
					ch2113 = c
					break
				}
			}
			if len(ch2113.StreamSources) != 2 || ch2113.StreamSources[1].AutoNumber != "1873" {
				t.Fatalf("#2113 automatic backup not wired: %+v", ch2113.StreamSources)
			}

			format := transcode.RawFormat{
				Width:       64,
				Height:      36,
				FPSNum:      25,
				FPSDen:      1,
				SampleRate:  48000,
				Channels:    2,
				PixelFormat: "yuv420p",
			}
			baseVid := blackFrame(format, 0).Video

			var mu sync.Mutex
			var primaryDec, backupDec *autoBackupRaceDecoder
			var backupSessionID string
			var decoderFormats []transcode.RawFormat
			backupOpened := make(chan struct{})
			backupGate := make(chan struct{})
			primaryResume := make(chan struct{})
			var backupOnce sync.Once

			runner := &broadcastFakeRunner{
				decoder: func(_ context.Context, req transcode.DecoderRequest) (transcode.Decoder, error) {
					if !strings.HasPrefix(req.InputURL, "http://127.0.0.1:1/") {
						return &autoBackupRaceDecoder{format: req.Format, video: baseVid, done: make(chan struct{})}, nil
					}
					sourceURL := ""
					s.inputRelay.mu.Lock()
					for ss := range s.inputRelay.bySess {
						if ss.info.ID == req.SessionID {
							sourceURL = ss.inputURL
							break
						}
					}
					s.inputRelay.mu.Unlock()
					mu.Lock()
					decoderFormats = append(decoderFormats, req.Format)
					mu.Unlock()
					if strings.HasSuffix(sourceURL, "/1873.ts") {
						d := &autoBackupRaceDecoder{format: req.Format, video: baseVid, gate: backupGate, done: make(chan struct{})}
						mu.Lock()
						backupDec = d
						backupSessionID = req.SessionID
						mu.Unlock()
						backupOnce.Do(func() { close(backupOpened) })
						return d, nil
					}
					d := &autoBackupRaceDecoder{format: req.Format, video: baseVid, unblock: primaryResume, done: make(chan struct{})}
					mu.Lock()
					if primaryDec == nil {
						primaryDec = d
					}
					mu.Unlock()
					return d, nil
				},
			}

			now := time.Now()
			planner := &providerBroadcastPlanner{
				s:          s,
				channel:    ch2113,
				format:     format,
				runner:     runner,
				ladder:     newSteadyLadder(),
				guideFrom:  now.Add(-time.Minute),
				guideUntil: now.Add(time.Hour),
			}
			b, err := newBroadcast(ctx, runner, planner, format)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				b.close()
				synctest.Wait()
			}()
			if _, err := b.addRendition(ctx, "main", core.Decision{}); err != nil {
				t.Fatal(err)
			}
			b.start()

			// Let primary #2113 start airing.
			time.Sleep(time.Second)
			planner.mu.Lock()
			firstAttempt := planner.attempt
			planner.mu.Unlock()
			if firstAttempt == nil || firstAttempt.link == nil || firstAttempt.src.ID != ch2113.StreamSources[0].ID {
				t.Fatalf("expected primary #2113 airing, got %+v", firstAttempt)
			}

			// Mark primary's transport failed and stall its decoder so keepStalled
			// triggers failover to the #1873 auto-backup.
			primaryGrant := s.healthGrant(firstAttempt.link.ss)
			if primaryGrant == nil {
				t.Fatalf("missing primary health grant")
			}
			primaryGrant.snap.Store(&healthSnap{h: UpstreamHealth{Kind: "ts", Failed: true}})
			mu.Lock()
			pdec := primaryDec
			mu.Unlock()
			pdec.stall.Store(true)

			// Wait for the auto-backup replacement to open and enter the parallel race.
			select {
			case <-backupOpened:
			case <-time.After(10 * time.Second):
				t.Fatalf("timed out waiting for auto-backup #1873 replacement open; logs:\n%s", logBuf.String())
			}
			time.Sleep(2 * time.Second)

			mu.Lock()
			bdec := backupDec
			bsid := backupSessionID
			mu.Unlock()
			if bsid == "broadcast-"+ch2113.ID {
				t.Fatalf("auto-backup reused static reservation ID %q", bsid)
			}

			if maxConns != 1 {
				// Multi-connection / unlimited: primary stays open while backup races.
				if pdec.closed.Load() {
					t.Fatalf("primary #2113 decoder closed before auto-backup #1873 delivered a picture")
				}
				if firstAttempt.link.closed.Load() {
					t.Fatalf("primary #2113 link closed before auto-backup #1873 delivered a picture")
				}
				if strings.Contains(logBuf.String(), "live input switched source") {
					t.Fatalf("logged source switch before replacement delivered a picture:\n%s", logBuf.String())
				}
			}

			if recoverPrimary {
				// Primary recovers before backup delivers a picture: backup must be
				// aborted and primary must remain on air without logging a switch.
				primaryGrant.snap.Store(&healthSnap{h: UpstreamHealth{Kind: "ts", Connected: true, LiveSocket: true, StallAfter: tapStallAfter}})
				close(primaryResume)
				time.Sleep(time.Second)

				if !bdec.closed.Load() {
					t.Fatalf("aborted auto-backup #1873 decoder was not closed after primary recovered")
				}
				if pdec.closed.Load() {
					t.Fatalf("primary #2113 decoder closed despite recovering before backup delivered")
				}
				planner.mu.Lock()
				curAttempt := planner.attempt
				stalled := planner.stalledAttempt
				planner.mu.Unlock()
				if curAttempt == nil || curAttempt.src.ID != ch2113.StreamSources[0].ID {
					t.Fatalf("expected primary #2113 to remain active after recovery, got %+v", curAttempt)
				}
				if stalled != nil {
					t.Fatalf("expected stalledAttempt cleared after abortReplacement")
				}
				if strings.Contains(logBuf.String(), "live input switched source") {
					t.Fatalf("unexpected source switch log after primary recovered:\n%s", logBuf.String())
				}
				return
			}

			// Release backup's first picture and verify clean splice + deferred primary close.
			releaseAt := time.Now()
			close(backupGate)
			time.Sleep(time.Second)

			if !pdec.closed.Load() {
				t.Fatalf("primary #2113 decoder did not close after auto-backup #1873 delivered a picture")
			}
			if maxConns != 1 {
				if closeAt := time.Unix(0, pdec.closeAt.Load()); closeAt.Before(releaseAt) {
					t.Fatalf("primary #2113 closed at %v before backup picture release at %v", closeAt, releaseAt)
				}
			}
			planner.mu.Lock()
			curAttempt := planner.attempt
			stalled := planner.stalledAttempt
			planner.mu.Unlock()
			if curAttempt == nil || curAttempt.src.AutoNumber != "1873" {
				t.Fatalf("expected auto-backup #1873 active after commit, got %+v", curAttempt)
			}
			if stalled != nil {
				t.Fatalf("expected stalledAttempt cleared after commitReplacement")
			}
			mu.Lock()
			for _, f := range decoderFormats {
				if f != format {
					t.Fatalf("decoder format changed across auto-backup failover: got %+v, want %+v", f, format)
				}
			}
			mu.Unlock()
			logs := logBuf.String()
			if !strings.Contains(logs, "live input switched source") || !strings.Contains(logs, "from_channel=#2113") || !strings.Contains(logs, "to_channel=#1873") {
				t.Fatalf("expected committed switch log with duplicate channel numbers:\n%s", logs)
			}
		})
	}

	t.Run("backup_wins_after_delivering_picture", func(t *testing.T) {
		runCase(t, 2, false)
	})
	t.Run("primary_recovers_before_backup_picture", func(t *testing.T) {
		runCase(t, 2, true)
	})
	t.Run("single_connection_same_account_releases_stalled_slot", func(t *testing.T) {
		runCase(t, 1, false)
	})
}
