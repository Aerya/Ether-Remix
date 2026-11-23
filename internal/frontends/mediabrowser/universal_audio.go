package mediabrowser

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

func (i *instance) isBookKey(r *http.Request, v *frontend.Viewer, key frontend.RatingKey) bool {
	if i == nil || i.host.Experience == nil || v == nil || key == 0 {
		return false
	}
	d, err := i.host.Experience.Item(r.Context(), frontend.ItemRequest{
		Viewer: v, Device: device(r), Key: key,
	})
	return err == nil && mbIsBookItem(d.Title)
}

func mbIsHLSContainer(container string) bool {
	switch strings.ToLower(strings.TrimSpace(container)) {
	case "fmp4", "mpegts", "ts", "mkv":
		return true
	default:
		return false
	}
}

// buildUniversalAudioProfile constructs a Jellyfin/Emby audio DeviceProfile
// from GET /Audio/{id}/universal query parameters (Container,
// TranscodingContainer, TranscodingProtocol, AudioCodec, MaxStreamingBitrate,
// AudioBitRate, MaxAudioChannels).
func buildUniversalAudioProfile(r *http.Request) (caps.DeviceProfile, string, time.Duration) {
	q := r.URL.Query()
	first := func(name string) string {
		if vals := route.Query(q, name); len(vals) > 0 {
			return strings.TrimSpace(vals[0])
		}
		return ""
	}
	var profile caps.DeviceProfile
	if raw := first("MaxStreamingBitrate"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			profile.MaxStreamingBitrate = n
			profile.MaxStaticBitrate = n
		}
	}
	if raw := first("AudioBitRate"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			profile.MusicStreamingTranscodingBitrate = n
			if profile.MaxStreamingBitrate == 0 || n < profile.MaxStreamingBitrate {
				profile.MaxStreamingBitrate = n
			}
		}
	}
	// Jellyfin clients pass Container as comma-separated entries of either
	// "container" or "container|codec1,codec2" (for example
	// "opus,webm|opus,mp3,aac,m4a|aac,m4b|aac,flac,webma,wav,ogg").
	if raw := first("Container"); raw != "" {
		for _, entry := range strings.Split(raw, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			cont, codecs, hasPipe := strings.Cut(entry, "|")
			cont = strings.TrimSpace(cont)
			if cont == "" {
				continue
			}
			if !hasPipe {
				codecs = ""
			}
			profile.DirectPlayProfiles = append(profile.DirectPlayProfiles, caps.DirectPlayProfile{
				Type:       "Audio",
				Container:  cont,
				AudioCodec: strings.TrimSpace(codecs),
			})
		}
	}
	proto := strings.ToLower(first("TranscodingProtocol"))
	if proto == "" {
		proto = "http"
	}
	transCont := first("TranscodingContainer")
	audioCodec := first("AudioCodec")
	maxCh := first("MaxAudioChannels")
	if transCont == "" && audioCodec == "" {
		if proto == "hls" {
			transCont = "ts"
			audioCodec = "aac"
		} else {
			transCont = "mp3"
			audioCodec = "mp3"
		}
	} else if transCont == "" {
		switch strings.ToLower(strings.Split(audioCodec, ",")[0]) {
		case "mp3":
			transCont = "mp3"
		case "opus":
			transCont = "ogg"
		case "flac":
			transCont = "flac"
		default:
			if proto == "hls" {
				transCont = "ts"
			} else {
				transCont = "mp4"
			}
		}
	} else if audioCodec == "" {
		switch strings.ToLower(transCont) {
		case "mp3":
			audioCodec = "mp3"
		case "ogg", "opus", "webm":
			audioCodec = "opus"
		case "flac":
			audioCodec = "flac"
		default:
			audioCodec = "aac"
		}
	}
	profile.TranscodingProfiles = append(profile.TranscodingProfiles, caps.TranscodingProfile{
		Type:             "Audio",
		Protocol:         proto,
		Container:        transCont,
		AudioCodec:       audioCodec,
		MaxAudioChannels: maxCh,
	})
	var startPos time.Duration
	if raw := first("StartTimeTicks"); raw != "" {
		if ticks, err := strconv.ParseInt(raw, 10, 64); err == nil && ticks > 0 {
			startPos = time.Duration(ticks) * 100
		}
	}
	return profile, proto, startPos
}

// serveUniversalAudio handles standalone GET/HEAD /Audio/{id}/universal when
// no PlaybackInfo session exists: builds a synthetic audio profile from the
// query parameters, plans/opens/starts a session on the fly, and streams the
// direct or transcoded audio directly (redirecting only for HLS playlists so
// relative segment URLs resolve against the session media path).
func (i *instance) serveUniversalAudio(w http.ResponseWriter, r *http.Request, viewer *frontend.Viewer, access frontend.SessionAccess, key frontend.RatingKey, version, detailSource, expectedETag string) {
	if i.host.Sessions == nil || i.host.Capabilities == nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	if key.IsPluginKey() && i.isBookKey(r, viewer, key) {
		http.NotFound(w, r)
		return
	}
	dev := access.Device
	profile, wantProto, startPos := buildUniversalAudioProfile(r)
	capability, err := i.host.Capabilities.Build(i.identityContext(r), frontend.CapabilityRequest{
		Viewer:    viewer,
		Device:    caps.DeviceInfo{Protocol: i.protocol, ClientID: dev.ID, Product: dev.Client, DeviceName: dev.DeviceName, Version: dev.Version},
		Profile:   profile,
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	allowed := true
	opts := frontend.DecideOptions{
		DirectPlay:     len(profile.DirectPlayProfiles) > 0,
		DirectStream:   true,
		AudioStream:    -1,
		SubtitleStream: -1,
	}
	if wantProto == "http" {
		opts.Protocol = frontend.ProtocolHTTP
	}
	plan, err := i.host.Sessions.Plan(r.Context(), frontend.PlanRequest{
		Viewer:             viewer,
		Device:             dev,
		Protocol:           i.protocol,
		Item:               key,
		Version:            version,
		DetailSource:       detailSource,
		ExpectedSourceETag: expectedETag,
		Caps:               capability.Client,
		Intent:             frontend.PlayIntent{ExactVersion: version != "", DeclaredCaps: true, Options: opts, AllowTranscoding: &allowed, Position: startPos},
	})
	if err != nil {
		streamError(i.protocol, w, err)
		return
	}
	session, err := i.host.Sessions.Open(r.Context(), frontend.OpenRequest{
		Protocol: i.protocol,
		PlanID:   plan.ID,
		Viewer:   viewer,
		Device:   dev,
	})
	if err != nil {
		streamError(i.protocol, w, err)
		return
	}
	format := frontend.StreamDirect
	if plan.Decision.Kind != frontend.DecisionDirectPlay {
		if wantProto == "hls" || (!plan.HTTP && plan.HLS) {
			format = frontend.StreamHLS
		} else {
			format = frontend.StreamHTTP
		}
	}
	mediaURL, err := i.host.Sessions.StartStream(r.Context(), access, session.ID, frontend.StreamRequest{
		Format:   format,
		Position: startPos,
	})
	if err != nil {
		_ = i.host.Sessions.Stop(r.Context(), access, session.ID)
		streamError(i.protocol, w, err)
		return
	}
	if !strings.HasPrefix(mediaURL, "/sessions/media/") || strings.ContainsAny(mediaURL, "\r\n") {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	if format == frontend.StreamHLS {
		target := clientnet.RedirectPath(requestRedirectPrefix(r, i.host.Origin), "", mediaURL)
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	req := r.Clone(r.Context())
	u := *req.URL
	u.Path = mediaURL
	u.RawPath = ""
	req.URL = &u
	if i.protocol == "emby" {
		i.embyMediaHeaders(w, req)
	}
	i.host.Sessions.ServeMedia(w, req)
}
