package mediabrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"net/http/httptest"
	"strings"
	"testing"
)

type baseSessions = frontendtest.Sessions
type subtitleFake struct {
	baseSessions
	calls int
}

func (f *subtitleFake) SubtitleURL(_ context.Context, a frontend.SessionAccess, id string, index int, format string) (string, error) {
	f.calls++
	if a.Protocol != "jellyfin" || id != "session" || index != 2 || format != "vtt" {
		panic("bad subtitle request")
	}
	return "/sessions/media/session/scoped/subtitles/2.vtt", nil
}
func TestSubtitleUsesScopedBrokerURL(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	broker := &subtitleFake{baseSessions: frontendtest.Sessions{SessionsFunc: func(context.Context, frontend.SessionAccess) ([]frontend.Session, error) {
		return []frontend.Session{{ID: "session", Item: 5, Protocol: "jellyfin"}}, nil
	}}}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Sessions: broker})
	if err != nil {
		t.Fatal(err)
	}
	id := ids.RatingKeyGUID(5)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/jellyfin/Videos/"+id+"/"+id+"/Subtitles/2/Stream.vtt?PlaySessionId=session&api_key=account-secret", nil)
	inst.Handler().ServeHTTP(w, r)
	location := w.Header().Get("Location")
	if w.Code != 307 || broker.calls != 1 || !strings.HasPrefix(location, "/jellyfin/sessions/media/") || strings.Contains(location, "account-secret") {
		t.Fatal(w.Code, location, broker.calls)
	}
}

func TestPlaybackInfoAdvertisesScopedTextSubtitle(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	broker := &subtitleFake{baseSessions: frontendtest.Sessions{
		PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
			return frontend.PlayPlan{ID: "plan", Decision: frontend.Decision{Kind: "directplay"}, Media: frontend.MediaInfo{Streams: []frontend.StreamInfo{{Type: "subtitle", Index: 2}}}}, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "session"}, nil
		},
		StartStreamFunc: func(context.Context, frontend.SessionAccess, string, frontend.StreamRequest) (string, error) {
			return "/sessions/media/session/scoped/file", nil
		},
	}}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Sessions: broker, Capabilities: frontendtest.Capabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo", strings.NewReader(`{"IsPlayback":true}`))
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"IsTextSubtitleStream":true`) || !strings.Contains(w.Body.String(), `"SupportsExternalStream":true`) || !strings.Contains(w.Body.String(), "/sessions/media/session/scoped/subtitles/") {
		t.Fatal(w.Code, w.Body.String())
	}
}

type hlsSubtitleFake struct{ baseSessions }

func (hlsSubtitleFake) SubtitleURL(_ context.Context, _ frontend.SessionAccess, _ string, index int, format string) (string, error) {
	if format == "hls" {
		if index == 2 {
			return "/sessions/media/session/scoped/text-2/index.m3u8", nil
		}
		return "", frontend.ErrNotAvailable
	}
	return fmt.Sprintf("/sessions/media/session/scoped/subtitles/%d.%s", index, format), nil
}

// A track the HLS stream carries is delivered there ("Hls", no sidecar URL),
// as Jellyfin does for native HLS players; other tracks stay External.
func TestPlaybackInfoDeliversHLSTextRendition(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	broker := hlsSubtitleFake{baseSessions: frontendtest.Sessions{
		PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
			return frontend.PlayPlan{ID: "plan", Decision: frontend.Decision{Kind: "transcode"}, Media: frontend.MediaInfo{Streams: []frontend.StreamInfo{{Type: "subtitle", Index: 2, Codec: "subrip"}, {Type: "subtitle", Index: 4, Codec: "ass"}}}}, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "session"}, nil
		},
		StartStreamFunc: func(context.Context, frontend.SessionAccess, string, frontend.StreamRequest) (string, error) {
			return "/sessions/media/session/scoped/master.m3u8", nil
		},
	}}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Sessions: broker, Capabilities: frontendtest.Capabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo", strings.NewReader(`{"IsPlayback":true}`))
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var body struct {
		MediaSources []struct {
			MediaStreams []map[string]any
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.MediaSources) != 1 {
		t.Fatal(err, w.Body.String())
	}
	streams := map[float64]map[string]any{}
	for _, s := range body.MediaSources[0].MediaStreams {
		if s["Type"] == "Subtitle" {
			streams[s["Index"].(float64)] = s
		}
	}
	if s := streams[2]; s == nil || s["DeliveryMethod"] != "Hls" || s["DeliveryUrl"] != nil || s["IsTextSubtitleStream"] != true {
		t.Fatalf("rendition track: %v", s)
	}
	if s := streams[4]; s == nil || s["DeliveryMethod"] != "External" || s["DeliveryUrl"] != "/sessions/media/session/scoped/subtitles/4.vtt" {
		t.Fatalf("sidecar track: %v", s)
	}
}

// A direct-played file's embedded tracks are "Embed" when the declared
// profile embeds that format, as Jellyfin answers; other formats stay
// External sidecars. Extracting an embedded track from a remote file first
// paused Jellyfin for Android TV and spun Wholphin.
func TestPlaybackInfoDirectPlayEmbedsDeclaredSubtitleFormats(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	broker := hlsSubtitleFake{baseSessions: frontendtest.Sessions{
		PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
			return frontend.PlayPlan{ID: "plan", Decision: frontend.Decision{Kind: "directplay"}, DirectPlay: true, Media: frontend.MediaInfo{Container: "mkv", Size: 1 << 30, Streams: []frontend.StreamInfo{
				{Type: "subtitle", Index: 2, Codec: "subrip"},
				{Type: "subtitle", Index: 3, Codec: "vobsub"},
				{Type: "subtitle", Index: 4, Codec: "ass"},
				{Type: "subtitle", Index: 5, Codec: "mov_text"},
				{Type: "subtitle", Index: 6, Codec: "webvtt"},
			}}}, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "session"}, nil
		},
		StartStreamFunc: func(context.Context, frontend.SessionAccess, string, frontend.StreamRequest) (string, error) {
			return "/sessions/media/session/scoped/file", nil
		},
	}}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Sessions: broker, Capabilities: frontendtest.Capabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo", strings.NewReader(`{"IsPlayback":true,"DeviceProfile":{"SubtitleProfiles":[{"Format":"srt","Method":"Embed"},{"Format":"dvdsub","Method":"Embed"},{"Format":"ssa","Method":"Embed"},{"Format":"tx3g","Method":"Embed"},{"Format":"vtt","Method":"External"}]}}`))
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	var body struct {
		MediaSources []struct {
			MediaStreams []map[string]any
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || len(body.MediaSources) != 1 {
		t.Fatal(w.Code, err, w.Body.String())
	}
	streams := map[float64]map[string]any{}
	for _, s := range body.MediaSources[0].MediaStreams {
		if s["Type"] == "Subtitle" {
			streams[s["Index"].(float64)] = s
		}
	}
	for _, idx := range []float64{2, 3, 4, 5} {
		if s := streams[idx]; s == nil || s["DeliveryMethod"] != "Embed" || s["DeliveryUrl"] != nil {
			t.Fatalf("embedded subtitle index %.0f: %v", idx, s)
		}
	}
	if s := streams[6]; s == nil || s["DeliveryMethod"] != "External" || s["DeliveryUrl"] != "/sessions/media/session/scoped/subtitles/6.vtt" {
		t.Fatalf("external VTT: %v", s)
	}
}
