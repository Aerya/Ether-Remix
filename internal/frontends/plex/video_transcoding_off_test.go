package plex

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type viewerVideoOffPolicy struct {
	disabled map[int64]bool
}

func (p viewerVideoOffPolicy) Allowed(*frontend.User, *frontend.Title) bool { return true }
func (p viewerVideoOffPolicy) SetMaxRating(context.Context, *frontend.Viewer, string) error {
	return nil
}
func (p viewerVideoOffPolicy) VideoTranscodingDisabled(u *frontend.User) bool {
	return u != nil && p.disabled[u.ID]
}

type realDecidePlexTranscoder struct{ *fakeTranscoder }

func (r realDecidePlexTranscoder) Decide(info frontend.MediaInfo, caps frontend.ClientCaps, opts frontend.DecideOptions) frontend.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}

func TestPlaybackMessageVideoTranscodingOff(t *testing.T) {
	msg := playbackMessage(fmt.Errorf("plan: %w", frontend.ErrVideoTranscodingDisabled))
	if msg != msgVideoTranscodingOff || !strings.Contains(msg, "video transcode") || !strings.Contains(msg, "turned off") {
		t.Fatalf("playbackMessage(ErrVideoTranscodingDisabled) = %q", msg)
	}
	langErr := &frontend.RequiredAudioLanguageError{Language: "English"}
	if got := playbackMessage(fmt.Errorf("plan: %w", langErr)); got != langErr.Error() {
		t.Fatalf("playbackMessage(RequiredAudioLanguageError) = %q, want %q", got, langErr.Error())
	}
}

func TestPlexDecisionPerViewerNoVideoTranscode(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	pol := viewerVideoOffPolicy{disabled: map[int64]bool{friendUser.ID: true}}
	f.srv.setTestPolicy(pol)
	tr := realDecidePlexTranscoder{fakeTranscoder: f.trans}
	f.srv.setTranscoder(tr)

	// Re-mount the fixture broker with ViewerVideoTranscodingOff wired to pol.
	active := func(_ context.Context, id int64) (*core.Viewer, error) {
		if id == friendUser.ID {
			return &friendUser, nil
		}
		return &ownerUser, nil
	}
	events := sessions.NewEventBus(nil, active)
	defer events.Close()
	broker, err := sessions.NewBroker(sessions.BrokerDeps{
		SurfacePolicy: func(protocol core.Protocol) core.SessionPolicy {
			if protocol == "plex" {
				return SessionPolicy()
			}
			return core.SessionPolicy{}
		},
		Lookup: func(ctx context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
			return f.catalog.Get(ctx, key)
		},
		Playback:                  &fixturePlayback{f: f},
		Transcoder:                tr,
		Progress:                  fixtureProgress{f: f},
		Trickplay:                 fixtureTrickplay{f: f},
		ActiveViewer:              active,
		CanPlay:                   func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:              func(*core.Viewer) int { return 0 },
		VideoTranscodingOff:       func() bool { return false },
		ViewerVideoTranscodingOff: pol.VideoTranscodingDisabled,
		OnTimeline:                func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Events:                    events,
		Now:                       f.srv.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(context.Background())
	f.broker = broker
	f.srv.usePlaybackBroker(sessions.BindSurface(broker, "plex"))

	// H.264-only Chrome client requesting an HEVC source:
	// - friend (VideoTranscodingDisabled=true) is refused with msgVideoTranscodingOff.
	// - owner (VideoTranscodingDisabled=false) succeeds with transcode.
	f.playback.info = core.MediaInfo{
		Container:   "mkv",
		Duration:    2 * time.Hour,
		BitrateKbps: 8000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
		},
	}
	chromeProfile := "add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.height&value=1080)"
	decPath := "/video/:/transcode/universal/decision?path=/library/metadata/" + fightClub.Key.String() + "&protocol=hls&directPlay=1&directStream=1&hasMDE=1"

	friendRec := f.request(t, http.MethodGet, decPath+"&session=friend-hevc", map[string]string{
		"Accept":                      "application/json",
		"X-Plex-Token":                friendToken,
		"X-Plex-Product":              "Plex Web",
		"X-Plex-Platform":             "Firefox",
		"X-Plex-Client-Identifier":    "friend-web",
		"X-Plex-Client-Profile-Extra": chromeProfile,
	})
	if friendRec.Code != http.StatusOK || !strings.Contains(friendRec.Body.String(), frontend.MsgViewerVideoTranscodingOff) {
		t.Fatalf("friend HEVC decision = %d %s, want %q", friendRec.Code, friendRec.Body.String(), frontend.MsgViewerVideoTranscodingOff)
	}

	ownerRec := f.request(t, http.MethodGet, decPath+"&session=owner-hevc", map[string]string{
		"Accept":                      "application/json",
		"X-Plex-Token":                ownerToken,
		"X-Plex-Product":              "Plex Web",
		"X-Plex-Platform":             "Firefox",
		"X-Plex-Client-Identifier":    "owner-web",
		"X-Plex-Client-Profile-Extra": chromeProfile,
	})
	if ownerRec.Code != http.StatusOK || strings.Contains(ownerRec.Body.String(), msgVideoTranscodingOff) || strings.Contains(ownerRec.Body.String(), frontend.MsgViewerVideoTranscodingOff) {
		t.Fatalf("owner HEVC decision should not be refused: %d %s", ownerRec.Code, ownerRec.Body.String())
	}

	// H.264 + TrueHD source for friend (even with AlwaysEncodeVideo override) succeeds with video copy.
	f.playback.info = core.MediaInfo{
		Container:   "mkv",
		Duration:    2 * time.Hour,
		BitrateKbps: 6000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "truehd", Channels: 6},
		},
	}
	cfg := f.srv.cfg()
	cfg.Clients.Overrides = []core.ClientCapabilityOverride{
		{Product: "Plex Web", ProductExact: true, AlwaysEncodeVideo: true},
	}
	f.srv.host.HostConfig = func() frontend.HostConfig { return cfg }
	friendH264Rec := f.request(t, http.MethodGet, decPath+"&directPlay=0&session=friend-h264", map[string]string{
		"Accept":                   "application/json",
		"X-Plex-Token":             friendToken,
		"X-Plex-Product":           "Plex Web",
		"X-Plex-Platform":          "Chrome",
		"X-Plex-Client-Identifier": "friend-web-2",
	})
	if friendH264Rec.Code != http.StatusOK || strings.Contains(friendH264Rec.Body.String(), msgVideoTranscodingOff) {
		t.Fatalf("friend H.264+TrueHD decision with AlwaysEncodeVideo conflict failed: %d %s", friendH264Rec.Code, friendH264Rec.Body.String())
	}

	// sourceSelectionCaps preserves low bitrate ceilings (<1500 kbps) when NoVideoTranscode is on.
	lowCapReq := playRequest{
		caps: frontend.ClientCaps{MaxBitrateKbps: 800, NoVideoTranscode: true},
		opts: frontend.DecideOptions{DirectPlay: true, DirectStream: true},
	}
	if got := lowCapReq.sourceSelectionCaps().MaxBitrateKbps; got != 800 {
		t.Fatalf("sourceSelectionCaps().MaxBitrateKbps = %d, want 800 when NoVideoTranscode is set", got)
	}

	// liveTuneCaps clears NoVideoTranscode so Live TV stays unaffected.
	if live := liveTuneCaps(frontend.ClientCaps{NoVideoTranscode: true}); live.NoVideoTranscode {
		t.Fatal("liveTuneCaps must clear NoVideoTranscode")
	}
}
