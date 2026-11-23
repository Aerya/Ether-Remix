package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontends/mediabrowser"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type infuseTestPlayback struct {
	mu           sync.Mutex
	sessions     map[string]*core.PlaySession
	liveness     map[string]core.SessionLivenessState
	info         core.MediaInfo
	releaseID    string
	limit, count int
}

func (p *infuseTestPlayback) Estimate(context.Context, *core.Title, core.ClientCaps) core.MediaInfo {
	return core.MediaInfo{Estimated: true, Container: p.info.Container}
}

func (p *infuseTestPlayback) PrepareSurface(ctx context.Context, u *core.User, key core.RatingKey, c core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	if exact && release != "" && release != p.releaseID {
		return nil, core.ErrNotAvailable
	}
	return p.Prepare(ctx, u, key, c)
}

func (p *infuseTestPlayback) SessionForSurface(_ string, user int64, client string, key core.RatingKey, _ string, _ bool) (*core.PlaySession, bool) {
	return p.SessionFor(user, client, key)
}

func (p *infuseTestPlayback) MarkSurfaceOpened(id string) bool   { _, ok := p.Session(id); return ok }
func (p *infuseTestPlayback) MarkSurfacePrepared(id string) bool { _, ok := p.Session(id); return ok }

func (p *infuseTestPlayback) Prepare(_ context.Context, u *core.User, key core.RatingKey, c core.ClientCaps) (*core.PlaySession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sessions {
		if s.UserID == u.ID && s.ClientID == c.ClientID && s.Key == key {
			return s, nil
		}
	}
	if len(p.sessions) >= p.limit {
		return nil, core.ErrStreamLimit
	}
	p.count++
	id := fmt.Sprintf("play-%d", p.count)
	rel := p.releaseID
	if rel == "" {
		rel = "rel-current"
	}
	s := &core.PlaySession{
		ID:       id,
		UserID:   u.ID,
		ClientID: c.ClientID,
		Client:   c,
		Key:      key,
		Info:     p.info,
		Resolution: &core.Resolution{
			Release: core.Release{ID: rel},
		},
	}
	p.sessions[id] = s
	if p.liveness != nil {
		p.liveness[id] = core.SessionLivenessStarting
	}
	return s, nil
}

func (p *infuseTestPlayback) Session(id string) (*core.PlaySession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	return s, ok
}

func (p *infuseTestPlayback) SessionFor(user int64, client string, key core.RatingKey) (*core.PlaySession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sessions {
		if s.UserID == user && s.ClientID == client && s.Key == key {
			return s, true
		}
	}
	return nil, false
}

func (p *infuseTestPlayback) SessionVisible(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.liveness == nil {
		_, ok := p.sessions[id]
		return ok
	}
	st := p.liveness[id]
	return st == core.SessionLivenessPlaying || st == core.SessionLivenessPaused
}

func (p *infuseTestPlayback) SessionLiveness(id string) core.SessionLivenessState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.liveness == nil {
		if _, ok := p.sessions[id]; ok {
			return core.SessionLivenessPlaying
		}
		return core.SessionLivenessStopped
	}
	return p.liveness[id]
}

func (p *infuseTestPlayback) ServeRange(w http.ResponseWriter, r *http.Request, id string) {
	p.mu.Lock()
	if p.liveness != nil {
		if _, ok := p.sessions[id]; ok {
			p.liveness[id] = core.SessionLivenessPlaying
		}
	}
	p.mu.Unlock()
	payload := []byte("0123456789abcdef")
	if r.Header.Get("Range") != "" {
		w.Header().Set("Content-Range", "bytes 0-3/16")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[:4])
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (p *infuseTestPlayback) InputURL(id string) string { return "http://loopback/" + id }
func (p *infuseTestPlayback) Failover(context.Context, string, time.Duration) (*core.PlaySession, error) {
	return nil, core.ErrNotAvailable
}
func (p *infuseTestPlayback) Stop(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions, id)
	if p.liveness != nil {
		delete(p.liveness, id)
	}
}
func (p *infuseTestPlayback) Sessions() []*core.PlaySession {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*core.PlaySession, 0, len(p.sessions))
	for _, s := range p.sessions {
		out = append(out, s)
	}
	return out
}

type fakeHLSSession struct {
	id string
}

func (s *fakeHLSSession) ID() string { return s.id }
func (s *fakeHLSSession) ServePlaylist(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("#EXTM3U\n"))
}
func (s *fakeHLSSession) ServeSegment(w http.ResponseWriter, _ *http.Request, _ int) {
	w.WriteHeader(http.StatusOK)
}
func (s *fakeHLSSession) SwapInput(context.Context, string, core.MediaInfo, time.Duration) error {
	return nil
}
func (s *fakeHLSSession) LastAccess() time.Time { return time.Now() }
func (s *fakeHLSSession) Close() error          { return nil }

type trackingHLSTranscoder struct {
	mu       sync.Mutex
	decide   func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision
	sessions map[string]core.HLSSession
	starts   int
	stops    int
}

func (t *trackingHLSTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	return nil, core.ErrNotAvailable
}
func (t *trackingHLSTranscoder) Decide(info core.MediaInfo, c core.ClientCaps, opts core.DecideOptions) core.Decision {
	if t.decide != nil {
		return t.decide(info, c, opts)
	}
	return transcode.Decide(info, c, opts, transcode.Policy{})
}
func (t *trackingHLSTranscoder) StartHLS(_ context.Context, req core.HLSRequest) (core.HLSSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.starts++
	if t.sessions == nil {
		t.sessions = map[string]core.HLSSession{}
	}
	hs := &fakeHLSSession{id: req.SessionID}
	t.sessions[req.SessionID] = hs
	return hs, nil
}
func (t *trackingHLSTranscoder) Session(id string) (core.HLSSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hs, ok := t.sessions[id]
	return hs, ok
}
func (t *trackingHLSTranscoder) Stop(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.sessions[id]; ok {
		delete(t.sessions, id)
		t.stops++
	}
}
func (t *trackingHLSTranscoder) HWAccel() core.HWAccel { return core.HWAccel{} }

func mountMediaBrowser(t *testing.T, protocol frontend.Protocol, host frontend.Host) frontend.Instance {
	t.Helper()
	var front frontend.Frontend
	if protocol == "emby" {
		front = mediabrowser.Emby{MachineID: "test"}
	} else {
		front = mediabrowser.Jellyfin{MachineID: "test"}
	}
	inst, err := front.Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })
	return inst
}

// Infuse posts a DeviceProfile with TranscodingProfiles and SubtitleProfiles
// but omits DirectPlayProfiles (nil, not []). On both Jellyfin and Emby, that
// must direct-play HDR10 + DTS-HD MKV without starting an HLS transcode, while
// an explicit empty DirectPlayProfiles: [] must still reject direct play.
func TestInfuseDeviceProfileDirectPlaysWithoutTranscode(t *testing.T) {
	infuseProfile := `{
		"EnableDirectPlay": true,
		"EnableDirectStream": true,
		"EnableTranscoding": true,
		"IsPlayback": true,
		"DeviceProfile": {
			"MaxStaticBitrate": 200000000,
			"MaxStreamingBitrate": 200000000,
			"TranscodingProfiles": [
				{"Container": "mp4", "Type": "Video", "VideoCodec": "hevc,h264", "AudioCodec": "eac3,ac3,aac", "Protocol": "hls", "Context": "Streaming"},
				{"Container": "ts", "Type": "Video", "VideoCodec": "h264", "AudioCodec": "aac", "Protocol": "hls", "Context": "Streaming"}
			],
			"SubtitleProfiles": [
				{"Format": "srt", "Method": "External"},
				{"Format": "ass", "Method": "External"}
			]
		}
	}`
	hdrDTSMedia := core.MediaInfo{
		Container:   "mkv",
		Duration:    45 * time.Minute,
		BitrateKbps: 18_000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, BitDepth: 10, ColorTransfer: "smpte2084", ColorPrimaries: "bt2020"},
			{Index: 1, Type: "audio", Codec: "dts", Channels: 6, Language: "eng", Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng"},
		},
	}

	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		t.Run(string(protocol), func(t *testing.T) {
			ctx := context.Background()
			db := store.OpenTest(t)
			active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
			events := NewEventBus(nil, active)
			defer events.Close()
			lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
				return &core.Title{Key: key, Type: core.TypeEpisode, Title: "Episode 7"}, nil
			}
			pb := &infuseTestPlayback{
				sessions: map[string]*core.PlaySession{},
				liveness: map[string]core.SessionLivenessState{},
				info:     hdrDTSMedia,
				limit:    2,
			}
			tc := &trackingHLSTranscoder{}
			broker, err := NewBroker(BrokerDeps{
				SurfacePolicy: testSessionPolicy,
				DB:            db,
				Events:        events,
				Playback:      pb,
				Transcoder:    tc,
				Progress:      &fakeProgress{},
				ActiveViewer:  active,
				CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
				BitrateLimit:  func(*core.Viewer) int { return 0 },
				OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
				Lookup:        lookup,
				MediaLookup:   lookup,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close(ctx)
			lazy, err := NewLazyStreams(db, broker, protocol, infuseGrant{}, time.Hour, func(*http.Request) bool { return false })
			if err != nil {
				t.Fatal(err)
			}
			viewer := &frontend.Viewer{ID: 1}
			inst := mountMediaBrowser(t, protocol, frontend.Host{
				Identity:    frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
				Sessions:    broker,
				LazyStreams: lazy,
				Capabilities: frontendtest.Capabilities{BuildFunc: func(ctx context.Context, req frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
					c, err := (caps.Builder{}).Build(ctx, req.Device, req.Profile, req.ProtocolInfo, req.UserAgent)
					return frontend.CapabilityResult{Client: c}, err
				}},
			})
			prefix := "/" + string(protocol)
			guid := ids.RatingKeyGUID(core.EpisodeKey(701, 2, 7))
			auth := `MediaBrowser Client="Infuse-Direct", Device="AppleTV", DeviceId="appletv", Version="8.5.10", Token="grant"`

			req := httptest.NewRequest(http.MethodPost, prefix+"/Items/"+guid+"/PlaybackInfo", strings.NewReader(infuseProfile))
			req.Header.Set("Authorization", auth)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("PlaybackInfo %d %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				PlaySessionId string
				MediaSources  []map[string]any
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.MediaSources) != 1 {
				t.Fatalf("decode %v body=%s", err, rec.Body.String())
			}
			src := resp.MediaSources[0]
			if src["SupportsDirectPlay"] != true {
				t.Fatalf("expected direct play, got %+v", src)
			}
			if _, hasTranscode := src["TranscodingUrl"]; hasTranscode {
				t.Fatalf("unexpected transcode offer for Infuse: %+v", src)
			}
			if tc.starts != 0 {
				t.Fatalf("StartHLS called %d times for direct-playable Infuse stream", tc.starts)
			}
			sourceID, _ := src["Id"].(string)
			streamReq := httptest.NewRequest(http.MethodGet, prefix+"/Videos/"+guid+"/stream?Static=true&MediaSourceId="+sourceID, nil)
			streamReq.Header.Set("Authorization", auth)
			streamReq.Header.Set("Range", "bytes=0-3")
			streamRec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(streamRec, streamReq)
			if streamRec.Code != http.StatusPartialContent && streamRec.Code != http.StatusOK {
				t.Fatalf("static stream %d %s", streamRec.Code, streamRec.Body.String())
			}
			if streamRec.Body.String() != "0123" {
				t.Fatalf("stream bytes = %q, want 0123", streamRec.Body.String())
			}

			// Explicit empty DirectPlayProfiles: [] (non-nil) still forbids direct play.
			emptyDP := &core.ClientCapabilityProfiles{
				DirectPlay:  []core.ClientDirectPlayProfile{},
				Transcoding: []core.ClientTranscodeProfile{{MediaType: "video", Delivery: "hls", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}},
			}
			if profileDirectAllowed(emptyDP, hdrDTSMedia, core.Decision{Kind: core.DecisionDirectPlay, AudioStream: 1}) {
				t.Fatal("explicit empty DirectPlayProfiles: [] must still reject direct play")
			}
		})
	}
}

// Even when PlaybackInfo starts an HLS transcode session (and before the first
// media byte moves liveness out of Starting), GET /Videos/{id}/stream?Static=true
// must locate the Starting session, stop HLS, switch it to direct, and serve
// bytes rather than 404 — while GET /Sessions keeps hiding Starting sessions.
func TestStaticStreamSwitchesFromHLSSessionAndFindsStartingSession(t *testing.T) {
	declaredProfile := `{
		"EnableDirectPlay": true,
		"EnableDirectStream": true,
		"EnableTranscoding": true,
		"IsPlayback": true,
		"DeviceProfile": {
			"DirectPlayProfiles": [{"Type": "Video", "Container": "mp4", "VideoCodec": "h264", "AudioCodec": "aac"}],
			"TranscodingProfiles": [{"Container": "ts", "Type": "Video", "VideoCodec": "h264", "AudioCodec": "aac", "Protocol": "hls", "Context": "Streaming"}]
		}
	}`
	hevcMedia := core.MediaInfo{
		Container: "mkv",
		Duration:  20 * time.Minute,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Default: true},
		},
	}

	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		t.Run(string(protocol), func(t *testing.T) {
			ctx := context.Background()
			db := store.OpenTest(t)
			active := func(_ context.Context, id int64) (*core.Viewer, error) {
				return &core.Viewer{ID: id, IsOwner: true}, nil
			}
			events := NewEventBus(nil, active)
			defer events.Close()
			lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
				return &core.Title{Key: key, Type: core.TypeMovie, Title: "TranscodeFallback"}, nil
			}
			pb := &infuseTestPlayback{
				sessions: map[string]*core.PlaySession{},
				liveness: map[string]core.SessionLivenessState{},
				info:     hevcMedia,
				limit:    2,
			}
			tc := &trackingHLSTranscoder{}
			broker, err := NewBroker(BrokerDeps{
				SurfacePolicy: testSessionPolicy,
				DB:            db,
				Events:        events,
				Playback:      pb,
				Transcoder:    tc,
				Progress:      &fakeProgress{},
				ActiveViewer:  active,
				CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
				BitrateLimit:  func(*core.Viewer) int { return 0 },
				OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
				Lookup:        lookup,
				MediaLookup:   lookup,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close(ctx)
			lazy, err := NewLazyStreams(db, broker, protocol, infuseGrant{}, time.Hour, func(*http.Request) bool { return false })
			if err != nil {
				t.Fatal(err)
			}
			viewer := &frontend.Viewer{ID: 1, IsOwner: true}
			inst := mountMediaBrowser(t, protocol, frontend.Host{
				Identity:    frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
				Sessions:    broker,
				LazyStreams: lazy,
				Capabilities: frontendtest.Capabilities{BuildFunc: func(ctx context.Context, req frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
					c, err := (caps.Builder{}).Build(ctx, req.Device, req.Profile, req.ProtocolInfo, req.UserAgent)
					return frontend.CapabilityResult{Client: c}, err
				}},
			})
			prefix := "/" + string(protocol)
			guid := ids.RatingKeyGUID(core.MovieKey(802))
			auth := `MediaBrowser Client="Infuse-Direct", Device="AppleTV", DeviceId="appletv", Version="8.5.10", Token="grant"`

			// 1. PlaybackInfo starts an HLS transcode; session liveness is Starting.
			playReq := httptest.NewRequest(http.MethodPost, prefix+"/Items/"+guid+"/PlaybackInfo", strings.NewReader(declaredProfile))
			playReq.Header.Set("Authorization", auth)
			playReq.Header.Set("Content-Type", "application/json")
			playRec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(playRec, playReq)
			if playRec.Code != http.StatusOK {
				t.Fatalf("PlaybackInfo %d %s", playRec.Code, playRec.Body.String())
			}
			var play struct {
				PlaySessionId string
				MediaSources  []map[string]any
			}
			if err := json.Unmarshal(playRec.Body.Bytes(), &play); err != nil || play.PlaySessionId == "" {
				t.Fatalf("decode %v body=%s", err, playRec.Body.String())
			}
			if tc.starts != 1 {
				t.Fatalf("expected PlaybackInfo to start HLS once, starts=%d", tc.starts)
			}

			// 2. VisibleOnly (/Sessions) still hides the Starting session.
			sessReq := httptest.NewRequest(http.MethodGet, prefix+"/Sessions", nil)
			sessReq.Header.Set("Authorization", auth)
			sessRec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(sessRec, sessReq)
			if sessRec.Code != http.StatusOK {
				t.Fatalf("Sessions %d %s", sessRec.Code, sessRec.Body.String())
			}
			var listed []map[string]any
			if err := json.Unmarshal(sessRec.Body.Bytes(), &listed); err != nil {
				t.Fatalf("decode sessions: %v", err)
			}
			if len(listed) != 0 {
				t.Fatalf("GET /Sessions must hide Starting session, got %+v", listed)
			}

			// 3. Static stream without PlaySessionId (Infuse pattern) finds the
			//    Starting HLS session, stops HLS, switches to direct, and serves bytes.
			streamReq := httptest.NewRequest(http.MethodGet, prefix+"/Videos/"+guid+"/stream?Static=true&MediaSourceId="+guid, nil)
			streamReq.Header.Set("Authorization", auth)
			streamReq.Header.Set("Range", "bytes=0-3")
			streamRec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(streamRec, streamReq)
			if streamRec.Code != http.StatusPartialContent && streamRec.Code != http.StatusOK {
				t.Fatalf("static stream after HLS %d %q", streamRec.Code, streamRec.Body.String())
			}
			if streamRec.Body.String() != "0123" {
				t.Fatalf("stream bytes = %q, want 0123", streamRec.Body.String())
			}
			if tc.stops != 1 {
				t.Fatalf("expected static fallback to stop HLS transcoder once, stops=%d", tc.stops)
			}

			// 4. Restart HLS on a second PlaybackInfo, then request the static stream
			//    with an explicit PlaySessionId; the 307 redirect must target .../file
			//    and serve bytes instead of 404.
			playReq2 := httptest.NewRequest(http.MethodPost, prefix+"/Items/"+guid+"/PlaybackInfo", strings.NewReader(declaredProfile))
			playReq2.Header.Set("Authorization", auth)
			playReq2.Header.Set("Content-Type", "application/json")
			playRec2 := httptest.NewRecorder()
			inst.Handler().ServeHTTP(playRec2, playReq2)
			var play2 struct {
				PlaySessionId string
			}
			if playRec2.Code != http.StatusOK || json.Unmarshal(playRec2.Body.Bytes(), &play2) != nil || play2.PlaySessionId == "" {
				t.Fatalf("second PlaybackInfo %d %s", playRec2.Code, playRec2.Body.String())
			}
			expReq := httptest.NewRequest(http.MethodGet, prefix+"/Videos/"+guid+"/stream?Static=true&PlaySessionId="+play2.PlaySessionId+"&MediaSourceId="+guid, nil)
			expReq.Header.Set("Authorization", auth)
			expRec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(expRec, expReq)
			if expRec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("explicit PlaySessionId static = %d %q, want 307", expRec.Code, expRec.Body.String())
			}
			loc := expRec.Header().Get("Location")
			if !strings.HasSuffix(loc, "/file") {
				t.Fatalf("redirect location = %q, want .../file", loc)
			}
			mediaReq := httptest.NewRequest(http.MethodGet, loc, nil)
			mediaReq.Header.Set("Authorization", auth)
			mediaReq.Header.Set("Range", "bytes=0-3")
			mediaRec := httptest.NewRecorder()
			inst.Handler().ServeHTTP(mediaRec, mediaReq)
			if mediaRec.Code != http.StatusPartialContent && mediaRec.Code != http.StatusOK {
				t.Fatalf("redirected direct file = %d %q", mediaRec.Code, mediaRec.Body.String())
			}
			if mediaRec.Body.String() != "0123" {
				t.Fatalf("redirected bytes = %q, want 0123", mediaRec.Body.String())
			}
		})
	}
}

// Stale MediaSourceIds (a withdrawn version handle or an expired detail-source
// handle — even after a new detail handle was issued for the same revision)
// map to the current source for that item and viewer on both Jellyfin and Emby,
// while foreign-device, cross-item, raw-release, and unknown handles still 404.
func TestStaleMediaSourceIdMapsToCurrentSourceAndRejectsForged(t *testing.T) {
	mp4Media := core.MediaInfo{
		Container: "mp4",
		Duration:  30 * time.Minute,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Default: true},
		},
	}

	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		t.Run(string(protocol), func(t *testing.T) {
			ctx := context.Background()
			db := store.OpenTest(t)
			now := time.Now().UTC()
			active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
			events := NewEventBus(nil, active)
			defer events.Close()
			lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
				return &core.Title{Key: key, Type: core.TypeMovie, Title: "StaleSource"}, nil
			}
			variants := []core.ReleaseVariant{{ID: "rel-old"}, {ID: "rel-current"}}
			pb := &infuseTestPlayback{
				sessions:  map[string]*core.PlaySession{},
				liveness:  map[string]core.SessionLivenessState{},
				info:      mp4Media,
				releaseID: "rel-current",
				limit:     4,
			}
			broker, err := NewBroker(BrokerDeps{
				SurfacePolicy: testSessionPolicy,
				DB:            db,
				Now:           func() time.Time { return now },
				Events:        events,
				Playback:      pb,
				Transcoder:    &trackingHLSTranscoder{},
				Progress:      &fakeProgress{},
				ActiveViewer:  active,
				CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
				BitrateLimit:  func(*core.Viewer) int { return 0 },
				OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
				Lookup:        lookup,
				MediaLookup:   lookup,
				Variants: func(context.Context, *core.Viewer, *core.Title, core.ClientCaps) ([]core.ReleaseVariant, error) {
					return variants, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close(ctx)
			vh, err := NewVersionHandles(db, broker, protocol, testVersionKey)
			if err != nil {
				t.Fatal(err)
			}
			lazy, err := NewLazyStreams(db, broker, protocol, infuseGrant{}, time.Hour, func(*http.Request) bool { return false })
			if err != nil {
				t.Fatal(err)
			}
			viewer := &frontend.Viewer{ID: 1}
			inst := mountMediaBrowser(t, protocol, frontend.Host{
				Identity:       frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
				Sessions:       broker,
				VersionHandles: vh,
				LazyStreams:    lazy,
				Capabilities: frontendtest.Capabilities{BuildFunc: func(_ context.Context, req frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
					return frontend.CapabilityResult{Client: core.ClientCaps{Protocol: protocol, ClientID: req.Device.ClientID, Product: req.Device.Product}}, nil
				}},
			})
			key := core.MovieKey(901)
			otherKey := core.MovieKey(902)
			guid := ids.RatingKeyGUID(key)
			scope := frontend.VersionScope{Viewer: viewer, Device: core.Device{ID: "appletv"}, Protocol: protocol}
			prefix := "/" + string(protocol)
			auth := `MediaBrowser Client="Infuse-Direct", Device="AppleTV", DeviceId="appletv", Version="8.5.10", Token="grant"`

			// Issue version handles for key (rel-old) and otherKey (rel-current).
			staleVer, err := vh.Issue(ctx, scope, key, "rel-old")
			if err != nil {
				t.Fatal(err)
			}
			otherItemVer, err := vh.Issue(ctx, scope, otherKey, "rel-current")
			if err != nil {
				t.Fatal(err)
			}
			// Withdraw rel-old so staleVer no longer resolves live.
			variants = []core.ReleaseVariant{{ID: "rel-current"}}

			// Issue detail sources for appletv and a foreign device, then expire them
			// and re-issue a fresh detail source for the same revision (testing that
			// expired handles remain mappable without UNIQUE collision).
			planReq := frontend.PlanRequest{Viewer: viewer, Device: scope.Device, Protocol: protocol, Item: key}
			ps := &core.PlaySession{Resolution: &core.Resolution{Release: core.Release{ID: "rel-old"}}}
			staleDetail, err := broker.issueDetailSource(ctx, planReq, ps, "rev-1")
			if err != nil {
				t.Fatal(err)
			}
			foreignReq := planReq
			foreignReq.Device = core.Device{ID: "foreign-device"}
			foreignDetail, err := broker.issueDetailSource(ctx, foreignReq, ps, "rev-1")
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(2 * time.Hour)
			reissuedDetail, err := broker.issueDetailSource(ctx, planReq, ps, "rev-1")
			if err != nil || reissuedDetail == "" || reissuedDetail == staleDetail {
				t.Fatalf("reissue after expiry: got=%q old=%q err=%v", reissuedDetail, staleDetail, err)
			}

			// Both stale handles succeed on PlaybackInfo and static stream.
			for name, handle := range map[string]string{"withdrawn-version": staleVer, "expired-detail": staleDetail} {
				pbReq := httptest.NewRequest(http.MethodPost, prefix+"/Items/"+guid+"/PlaybackInfo?MediaSourceId="+handle, strings.NewReader(`{"IsPlayback":true}`))
				pbReq.Header.Set("Authorization", auth)
				pbRec := httptest.NewRecorder()
				inst.Handler().ServeHTTP(pbRec, pbReq)
				if pbRec.Code != http.StatusOK {
					t.Fatalf("%s PlaybackInfo = %d %s", name, pbRec.Code, pbRec.Body.String())
				}
				stReq := httptest.NewRequest(http.MethodGet, prefix+"/Videos/"+guid+"/stream?Static=true&MediaSourceId="+handle, nil)
				stReq.Header.Set("Authorization", auth)
				stReq.Header.Set("Range", "bytes=0-3")
				stRec := httptest.NewRecorder()
				inst.Handler().ServeHTTP(stRec, stReq)
				if stRec.Code != http.StatusPartialContent && stRec.Code != http.StatusOK {
					t.Fatalf("%s static stream = %d %s", name, stRec.Code, stRec.Body.String())
				}
			}

			// Foreign-device, cross-item, raw release ID, and unknown handles still 404.
			for name, bad := range map[string]string{
				"foreign-device-detail": foreignDetail,
				"cross-item-version":    otherItemVer,
				"raw-release-id":        "rel-old",
				"unknown-forged":        "deadbeefdeadbeefdeadbeefdeadbeef",
			} {
				pbReq := httptest.NewRequest(http.MethodPost, prefix+"/Items/"+guid+"/PlaybackInfo?MediaSourceId="+bad, strings.NewReader(`{"IsPlayback":false}`))
				pbReq.Header.Set("Authorization", auth)
				pbRec := httptest.NewRecorder()
				inst.Handler().ServeHTTP(pbRec, pbReq)
				if pbRec.Code != http.StatusNotFound {
					t.Fatalf("%s PlaybackInfo = %d, want 404", name, pbRec.Code)
				}
				stReq := httptest.NewRequest(http.MethodGet, prefix+"/Videos/"+guid+"/stream?Static=true&MediaSourceId="+bad, nil)
				stReq.Header.Set("Authorization", auth)
				stRec := httptest.NewRecorder()
				inst.Handler().ServeHTTP(stRec, stReq)
				if stRec.Code != http.StatusNotFound {
					t.Fatalf("%s static stream = %d, want 404", name, stRec.Code)
				}
			}
		})
	}
}

func TestBrokerSubtitleURLsBatchAuthorization(t *testing.T) {
	ctx := context.Background()
	revoked := false
	active := func(_ context.Context, id int64) (*core.Viewer, error) {
		if revoked {
			return nil, core.ErrUnauthorized
		}
		return &core.Viewer{ID: id}, nil
	}
	events := NewEventBus(nil, active)
	defer events.Close()
	lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: key, Type: core.TypeMovie}, nil
	}
	pb := &infuseTestPlayback{
		sessions: map[string]*core.PlaySession{},
		info: core.MediaInfo{
			Container: "mkv",
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264"},
				{Index: 1, Type: "audio", Codec: "aac"},
				{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng"},
				{Index: 3, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "eng"},
				{Index: 4, Type: "subtitle", Codec: "ass", Language: "jpn"},
			},
		},
		limit: 2,
	}
	b, err := NewBroker(BrokerDeps{
		SurfacePolicy: testSessionPolicy,
		Events:        events,
		Playback:      pb,
		Transcoder:    &trackingHLSTranscoder{},
		Progress:      &fakeProgress{},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup:        lookup,
		MediaLookup:   lookup,
		ExtractSubtitle: func(_ context.Context, _ string, _ int, _ string, w io.Writer) error {
			_, err := w.Write([]byte("WEBVTT\n"))
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	viewer := &core.Viewer{ID: 1}
	dev := core.Device{ID: "appletv"}
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: dev, Protocol: "jellyfin", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true}}})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := b.Open(ctx, frontend.OpenRequest{Protocol: "jellyfin", PlanID: plan.ID, Viewer: viewer, Device: dev})
	if err != nil {
		t.Fatal(err)
	}
	access := frontend.SessionAccess{Viewer: viewer, Device: dev, Protocol: "jellyfin"}
	reqs := []frontend.SubtitleURLRequest{
		{Index: 2, Format: "vtt"},
		{Index: 3, Format: "vtt"}, // image subtitle -> empty entry, no batch error
		{Index: 4, Format: "vtt"},
		{Index: 99, Format: "vtt"}, // missing track -> empty entry
	}
	urls, err := b.SubtitleURLs(ctx, access, sess.ID, reqs)
	if err != nil {
		t.Fatalf("SubtitleURLs: %v", err)
	}
	if len(urls) != 4 || !strings.HasSuffix(urls[0], "/subtitles/2.vtt") || urls[1] != "" || !strings.HasSuffix(urls[2], "/subtitles/4.vtt") || urls[3] != "" {
		t.Fatalf("unexpected batch urls: %q", urls)
	}

	revoked = true
	if _, err := b.SubtitleURLs(ctx, access, sess.ID, reqs); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("revoked viewer err = %v, want ErrUnauthorized", err)
	}
}
