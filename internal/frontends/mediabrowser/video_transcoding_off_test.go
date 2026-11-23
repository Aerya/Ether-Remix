package mediabrowser

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type mbTestPlayback struct {
	core.Playback
	sessions map[string]*core.PlaySession
	prepare  func(ctx context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps, release string, exact bool) (*core.PlaySession, error)
}

func (p *mbTestPlayback) Prepare(ctx context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	return p.PrepareSurface(ctx, u, key, caps, "", false)
}
func (p *mbTestPlayback) PrepareSurface(ctx context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	ps, err := p.prepare(ctx, u, key, caps, release, exact)
	if err == nil && ps != nil {
		if p.sessions == nil {
			p.sessions = map[string]*core.PlaySession{}
		}
		p.sessions[ps.ID] = ps
	}
	return ps, err
}
func (p *mbTestPlayback) Estimate(context.Context, *core.Title, core.ClientCaps) core.MediaInfo {
	return core.MediaInfo{}
}
func (p *mbTestPlayback) Session(id string) (*core.PlaySession, bool) {
	ps, ok := p.sessions[id]
	return ps, ok
}
func (p *mbTestPlayback) SessionFor(int64, string, core.RatingKey) (*core.PlaySession, bool) {
	return nil, false
}
func (p *mbTestPlayback) SessionForSurface(string, int64, string, core.RatingKey, string, bool) (*core.PlaySession, bool) {
	return nil, false
}
func (p *mbTestPlayback) MarkSurfaceOpened(string) bool                               { return true }
func (p *mbTestPlayback) MarkSurfacePrepared(string) bool                             { return true }
func (p *mbTestPlayback) InputURL(id string) string                                   { return "http://127.0.0.1/" + id }
func (p *mbTestPlayback) Stop(string)                                                 {}
func (p *mbTestPlayback) SetViewerState(string, string, time.Duration, time.Duration) {}
func (p *mbTestPlayback) ServeRange(http.ResponseWriter, *http.Request, string)       {}
func (p *mbTestPlayback) Sessions() []*core.PlaySession                               { return nil }
func (p *mbTestPlayback) SetDecision(string, core.Decision)                           {}

type mbTestTranscoder struct{}

type mbTestHLSSession struct{ core.HLSSession }

func (mbTestHLSSession) Decision() core.Decision { return core.Decision{} }

func (mbTestTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}
func (mbTestTranscoder) StartHLS(_ context.Context, req core.HLSRequest) (core.HLSSession, error) {
	if req.NoVideoTranscode && req.Decision.Kind == core.DecisionTranscode && !req.Decision.VideoCopy {
		return nil, core.ErrVideoTranscodingDisabled
	}
	return mbTestHLSSession{}, nil
}
func (mbTestTranscoder) Session(string) (core.HLSSession, bool) { return nil, false }
func (mbTestTranscoder) Stop(string)                            {}
func (mbTestTranscoder) Touch(string)                           {}
func (mbTestTranscoder) HWAccel() core.HWAccel                  { return core.HWAccel{} }
func (mbTestTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	return nil, core.ErrNotAvailable
}

type mbTestProgress struct {
	sessions.ProgressWriter
}

func TestPlaybackInfoErrorVideoTranscodingOff(t *testing.T) {
	i := &instance{protocol: "jellyfin"}
	rec := httptest.NewRecorder()
	i.playbackInfoError(rec, nil, "plan", frontend.ErrVideoTranscodingDisabled)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["ErrorCode"] != "NoCompatibleStream" {
		t.Fatalf("ErrorCode = %v, want NoCompatibleStream", body["ErrorCode"])
	}
	msg, _ := body["Message"].(string)
	if !strings.Contains(msg, "video transcode") || !strings.Contains(msg, "turned off") {
		t.Fatalf("Message = %q", msg)
	}

	// Emby streamError returns HTTP 422 with the clear viewer message.
	embyRec := httptest.NewRecorder()
	streamError("emby", embyRec, frontend.ErrVideoTranscodingDisabled)
	if embyRec.Code != http.StatusUnprocessableEntity || !strings.Contains(embyRec.Body.String(), "turned off") {
		t.Fatalf("emby streamError = %d %q", embyRec.Code, embyRec.Body.String())
	}

	// RequiredAudioLanguageError surfaces actionable message in Jellyfin PlaybackInfo and Emby streamError.
	langErr := &frontend.RequiredAudioLanguageError{Language: "English"}
	jfLangRec := httptest.NewRecorder()
	i.playbackInfoError(jfLangRec, nil, "plan", langErr)
	var jfLangBody map[string]any
	if err := json.Unmarshal(jfLangRec.Body.Bytes(), &jfLangBody); err != nil {
		t.Fatalf("json: %v", err)
	}
	if jfLangBody["Message"] != langErr.Error() {
		t.Fatalf("jellyfin Message = %v, want %q", jfLangBody["Message"], langErr.Error())
	}
	embyLangRec := httptest.NewRecorder()
	streamError("emby", embyLangRec, langErr)
	if embyLangRec.Code != http.StatusUnprocessableEntity || !strings.Contains(embyLangRec.Body.String(), langErr.Error()) {
		t.Fatalf("emby streamError(RequiredAudioLanguageError) = %d %q", embyLangRec.Code, embyLangRec.Body.String())
	}
}

func TestPlaybackInfoPerViewerNoVideoTranscode(t *testing.T) {
	itemKey := core.MovieKey(55)
	unrestricted := &core.Viewer{ID: 1, Username: "owner", IsOwner: true}
	restricted := &core.Viewer{ID: 2, Username: "friend"}

	hevcInfo := core.MediaInfo{
		Container:   "mkv",
		Duration:    time.Hour,
		BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
		},
	}
	h264TrueHDInfo := core.MediaInfo{
		Container:   "mkv",
		Duration:    time.Hour,
		BitrateKbps: 6000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "truehd", Channels: 6},
		},
	}

	currentInfo := hevcInfo
	pb := &mbTestPlayback{
		prepare: func(_ context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps, _ string, _ bool) (*core.PlaySession, error) {
			return &core.PlaySession{
				ID:         "ps-" + u.Username,
				UserID:     u.ID,
				ClientID:   caps.ClientID,
				Key:        key,
				Info:       currentInfo,
				Resolution: &core.Resolution{Release: core.Release{ID: "rel-1"}},
			}, nil
		},
	}

	active := func(_ context.Context, id int64) (*core.Viewer, error) {
		if id == restricted.ID {
			return restricted, nil
		}
		return unrestricted, nil
	}
	events := sessions.NewEventBus(nil, active)
	defer events.Close()

	broker, err := sessions.NewBroker(sessions.BrokerDeps{
		SurfacePolicy: SessionPolicy,
		Playback:      pb,
		Transcoder:    mbTestTranscoder{},
		Progress:      mbTestProgress{},
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k, Type: core.TypeMovie, Title: "Test Movie"}, nil
		},
		VideoTranscodingOff: func() bool { return false },
		ViewerVideoTranscodingOff: func(v *core.Viewer) bool {
			return v != nil && v.ID == restricted.ID
		},
		Events: events,
		Now:    time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(context.Background())

	ident := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token == "friend-token" {
			return restricted, nil
		}
		return unrestricted, nil
	}}
	h264ProfileBody := `{"IsPlayback":true,"DeviceProfile":{"DirectPlayProfiles":[{"Type":"Video","Container":"mp4","VideoCodec":"h264","AudioCodec":"aac"}],"TranscodingProfiles":[{"Type":"Video","Protocol":"hls","Container":"ts","VideoCodec":"h264","AudioCodec":"aac"}]}}`
	h264Caps := frontendtest.Capabilities{BuildFunc: func(_ context.Context, req frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
		return frontend.CapabilityResult{
			Client: core.ClientCaps{
				Protocol:      req.Device.Protocol,
				ClientID:      req.Device.ClientID,
				Product:       req.Device.Product,
				MaxHeight:     1080,
				Containers:    []string{"mp4"},
				HLSContainers: []string{"mpegts", "fmp4"},
				VideoCodecs:   []string{"h264"},
				AudioCodecs:   []string{"aac"},
				Profiles: &core.ClientCapabilityProfiles{
					DirectPlay: []core.ClientDirectPlayProfile{
						{MediaType: "Video", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
					},
					Transcoding: []core.ClientTranscodeProfile{
						{MediaType: "Video", Delivery: "hls", Containers: []string{"mpegts"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}},
					},
				},
			},
		}, nil
	}}

	// Jellyfin PlaybackInfo path:
	// - restricted viewer with HEVC source -> ErrorCode: NoCompatibleStream with per-account wording.
	// - unrestricted viewer with HEVC source -> succeeds (MediaSources populated).
	// - restricted viewer with H.264 + TrueHD -> succeeds (video copy + audio conversion).
	viewerPolicy := mbVideoOffPolicy{viewerID: restricted.ID}
	jfInst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:     ident,
		Sessions:     sessions.BindSurface(broker, "jellyfin"),
		Capabilities: h264Caps,
		ViewerPolicy: viewerPolicy,
		Log:          slog.Default(),
		SurfaceURL:   func(context.Context, *http.Request) (string, error) { return "https://ether.example/jellyfin", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	jfCall := func(token, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/Items/"+ids.RatingKeyGUID(itemKey)+"/PlaybackInfo", strings.NewReader(body))
		r.Header.Set("X-Emby-Token", token)
		r.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Jellyfin Web", Device="Chrome", DeviceId="dev-1", Version="10.9.0"`)
		jfInst.Handler().ServeHTTP(w, r)
		return w
	}

	w := jfCall("friend-token", h264ProfileBody)
	var jfResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &jfResp); err != nil {
		t.Fatalf("jellyfin restricted HEVC json: %v (status=%d body=%s)", err, w.Code, w.Body.String())
	}
	jfMsg, _ := jfResp["Message"].(string)
	if jfResp["ErrorCode"] != "NoCompatibleStream" || jfMsg != frontend.MsgViewerVideoTranscodingOff {
		t.Fatalf("jellyfin restricted HEVC = %d %+v, want NoCompatibleStream with per-account message %q", w.Code, jfResp, frontend.MsgViewerVideoTranscodingOff)
	}

	wOwner := jfCall("owner-token", h264ProfileBody)
	var ownerResp map[string]any
	if err := json.Unmarshal(wOwner.Body.Bytes(), &ownerResp); err != nil {
		t.Fatalf("jellyfin owner HEVC json: %v (%s)", err, wOwner.Body.String())
	}
	if ownerResp["ErrorCode"] != nil {
		t.Fatalf("jellyfin owner HEVC unexpectedly refused: %+v", ownerResp)
	}
	if sources, _ := ownerResp["MediaSources"].([]any); len(sources) == 0 {
		t.Fatalf("jellyfin owner HEVC expected MediaSources, got %+v", ownerResp)
	}

	currentInfo = h264TrueHDInfo
	wH264 := jfCall("friend-token", h264ProfileBody)
	var h264Resp map[string]any
	if err := json.Unmarshal(wH264.Body.Bytes(), &h264Resp); err != nil {
		t.Fatalf("jellyfin restricted H264+TrueHD json: %v (%s)", err, wH264.Body.String())
	}
	if h264Resp["ErrorCode"] != nil {
		t.Fatalf("jellyfin restricted H264+TrueHD unexpectedly refused: %+v", h264Resp)
	}
	if sources, _ := h264Resp["MediaSources"].([]any); len(sources) == 0 {
		t.Fatalf("jellyfin restricted H264+TrueHD expected MediaSources, got %+v", h264Resp)
	}

	// Emby PlaybackInfo path: restricted viewer against HEVC source returns NoCompatibleStream with per-account wording.
	currentInfo = hevcInfo
	embyInst, err := (Emby{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:     ident,
		Sessions:     sessions.BindSurface(broker, "emby"),
		Capabilities: h264Caps,
		ViewerPolicy: viewerPolicy,
		SurfaceURL:   func(context.Context, *http.Request) (string, error) { return "https://ether.example/emby", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	embyW := httptest.NewRecorder()
	embyR := httptest.NewRequest(http.MethodPost, "/Items/"+ids.RatingKeyGUID(itemKey)+"/PlaybackInfo", strings.NewReader(h264ProfileBody))
	embyR.Header.Set("X-Emby-Token", "friend-token")
	embyR.Header.Set("X-Emby-Authorization", `Emby Client="Emby Web", Device="Chrome", DeviceId="dev-2", Version="4.8.0"`)
	embyInst.Handler().ServeHTTP(embyW, embyR)
	var embyResp map[string]any
	if err := json.Unmarshal(embyW.Body.Bytes(), &embyResp); err != nil {
		t.Fatalf("emby restricted HEVC json: %v (status=%d body=%s)", err, embyW.Code, embyW.Body.String())
	}
	if embyResp["ErrorCode"] != "NoCompatibleStream" || embyResp["Message"] != frontend.MsgViewerVideoTranscodingOff {
		t.Fatalf("emby restricted HEVC = %d %+v, want ErrorCode=NoCompatibleStream with %q", embyW.Code, embyResp, frontend.MsgViewerVideoTranscodingOff)
	}
}

type mbVideoOffPolicy struct{ viewerID int64 }

func (p mbVideoOffPolicy) SetMaxRating(context.Context, *frontend.Viewer, string) error { return nil }
func (p mbVideoOffPolicy) Allowed(*core.User, *core.Title) bool                         { return true }
func (p mbVideoOffPolicy) DiscoveryAllowed(*core.User, *core.Title) bool                { return true }
func (p mbVideoOffPolicy) TitleFacts(*core.User) core.TitleFacts                        { return 0 }
func (p mbVideoOffPolicy) VideoTranscodingDisabled(u *core.User) bool {
	return u != nil && u.ID == p.viewerID
}
