package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/streamgrant"
)

// Plex for Samsung tunes a channel and fetches start.m3u8 with its token,
// then hands the URL to the TV's native player, which requests the playlist
// again without credentials. Ether answered 401 and the TV stopped before
// asking for a segment. Only the same client address, for the live session
// it just received, may read that playlist again; nothing else becomes
// reachable without credentials.
func TestSamsungNativeLivePlaylistRefetch(t *testing.T) {
	f := newFixture()
	t.Cleanup(func() { f.srv.Close() })
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Unix(0, 0).UTC().Add(time.Hour + time.Minute + 44*time.Second)
	pb := newFakePlayback(core.MediaInfo{Duration: 2 * time.Hour, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
		{Index: 1, Type: "audio", Codec: "aac"},
	}})
	tc := newFakeTranscoder(core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mpegts"})
	service, err := livetv.New(livetv.Options{Transcoder: tc, Playback: pb, DB: db, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	if _, err = service.SaveChannel(context.Background(), livetv.Channel{
		Name: "Family Animation", Number: "4", Kind: "virtual", Enabled: true, Rating: "PG",
		Epoch: time.Unix(0, 0).UTC(),
		Items: []livetv.Item{{Key: core.MovieKey(1), Title: "Cartoon", DurationMS: int64((2 * time.Hour).Milliseconds()), Rating: "PG"}},
	}); err != nil {
		t.Fatal(err)
	}
	f.srv.setLiveTV(service.Frontend())
	f.srv.setTestSubscription(true)
	headers := map[string]string{"Accept": "application/json", "X-Plex-Product": "Plex for Samsung", "X-Plex-Platform": "Tizen", "X-Plex-Version": "5.94.1", "X-Plex-Session-Identifier": "tv-play-1"}
	tune := f.request(t, "POST", "/livetv/dvrs/1/channels/4/tune", headers)
	if tune.Code != 200 {
		t.Fatalf("tune %d %s", tune.Code, tune.Body.String())
	}
	tuned := decodeJSON(t, tune)
	var sessionPath string
	if md, ok := tuned["Metadata"].([]any); ok && len(md) > 0 {
		sessionPath = md[0].(map[string]any)["key"].(string)
	} else {
		sub := tuned["MediaSubscription"].([]any)[0].(map[string]any)
		op := sub["MediaGrabOperation"].([]any)[0].(map[string]any)
		sessionPath = op["Metadata"].(map[string]any)["key"].(string)
	}
	liveID := strings.TrimPrefix(sessionPath, "/livetv/sessions/")
	startPath := "/video/:/transcode/universal/start.m3u8?path=" + url.QueryEscape(sessionPath) + "&session=tv-transcode-1"

	nativeMethod := func(method, target, ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.RemoteAddr = ip + ":40000"
		req.Header.Set("User-Agent", "Mozilla/5.0 (SMART-TV; LINUX; Tizen 9.0)")
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		return rec
	}
	native := func(target, ip string) *httptest.ResponseRecorder { return nativeMethod(http.MethodGet, target, ip) }
	if rec := native(startPath, "192.168.1.50"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refetch before any authenticated playlist: %d", rec.Code)
	}
	if rec := f.request(t, "GET", "/video/:/transcode/universal/decision?path="+url.QueryEscape(sessionPath), headers); rec.Code != 200 {
		t.Fatalf("decision %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(t, "GET", startPath, headers); rec.Code != 200 || !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("authenticated start.m3u8 %d %s", rec.Code, rec.Body.String())
	}

	for _, target := range []string{startPath, "/video/:/transcode/universal/start.m3u8?session=tv-transcode-1", "/video/:/transcode/universal/start.m3u8?session=tv-play-1"} {
		rec := native(target, "192.168.1.50")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "#EXTM3U") {
			t.Fatalf("same-IP refetch %s: %d %s", target, rec.Code, rec.Body.String())
		}
		var segment string
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			if strings.Contains(line, "/relay/") {
				segment = strings.TrimSpace(line)
				break
			}
		}
		if !strings.HasPrefix(segment, sessionPath+"/0/relay/") || !strings.Contains(segment, streamgrant.StreamGrantParam+"=") {
			t.Fatalf("refetched playlist segment is not a rooted stream-grant URL:\n%s", rec.Body.String())
		}
		if media := native(segment, "192.168.1.50"); media.Code != 200 {
			t.Fatalf("segment from refetched playlist: %d", media.Code)
		}
	}

	refused := map[string]string{
		"other IP":           "192.168.1.77|" + startPath,
		"unknown session":    "192.168.1.50|/video/:/transcode/universal/start.m3u8?session=nope",
		"other live path":    "192.168.1.50|/video/:/transcode/universal/start.m3u8?path=" + url.QueryEscape("/livetv/sessions/nope"),
		"alias as path":      "192.168.1.50|/video/:/transcode/universal/start.m3u8?path=" + url.QueryEscape("/livetv/sessions/tv-transcode-1"),
		"non-live path":      "192.168.1.50|/video/:/transcode/universal/start.m3u8?path=" + url.QueryEscape("/library/metadata/1") + "&session=tv-transcode-1",
		"decision":           "192.168.1.50|/video/:/transcode/universal/decision?path=" + url.QueryEscape(sessionPath),
		"start.mpd":          "192.168.1.50|/video/:/transcode/universal/start.mpd?path=" + url.QueryEscape(sessionPath),
		"stop":               "192.168.1.50|/video/:/transcode/universal/stop?session=tv-transcode-1",
		"live session":       "192.168.1.50|" + sessionPath,
		"timeline":           "192.168.1.50|/:/timeline?key=" + url.QueryEscape(sessionPath) + "&state=stopped",
		"retune":             "192.168.1.50|/livetv/dvrs/1/channels/4/tune|POST",
		"subscription close": "192.168.1.50|/media/subscriptions/" + liveID + "|DELETE",
	}
	for name, spec := range refused {
		ip, target, _ := strings.Cut(spec, "|")
		method := http.MethodGet
		if t, m, ok := strings.Cut(target, "|"); ok {
			target, method = t, m
		}
		if rec := nativeMethod(method, target, ip); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if len(service.ViewingSessions()) != 1 {
		t.Fatal("refused requests changed the live session")
	}

	// The window closes two minutes after the authenticated playlist.
	f.srv.liveManifests.mu.Lock()
	e := f.srv.liveManifests.served[liveID]
	e.at = e.at.Add(-manifestRefetchWindow - time.Second)
	f.srv.liveManifests.served[liveID] = e
	f.srv.liveManifests.mu.Unlock()
	if rec := native(startPath, "192.168.1.50"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refetch after the window: %d", rec.Code)
	}
}
