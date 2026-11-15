package vela

import (
	"context"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// playbackTracks preserves source stream identities across output generations.
// Only the selected audio's delivery facts change; another track never inherits
// the previous encoder's codec or channel count.
func (i *instance) playbackTracks(ctx context.Context, access frontend.SessionAccess, id string, media frontend.MediaInfo, d frontend.Decision, selectedSubtitle int, original bool, formats []string, canBurn bool) ([]object, []object, string, any) {
	audio, subs := []object{}, []object{}
	selectedAudio := ""
	var selectedSub any
	for _, track := range media.Streams {
		if track.Type != "audio" && track.Type != "subtitle" {
			continue
		}
		t := object{"id": strconv.Itoa(track.Index), "streamIndex": track.Index, "kind": track.Type, "language": track.Language, "name": track.Title, "codec": track.Codec, "default": track.Default, "forced": track.Forced}
		if track.Type == "audio" {
			t["channels"] = track.Channels
			if track.Index == d.AudioStream {
				selectedAudio = t["id"].(string)
				if !d.AudioCopy && d.AudioCodec != "" {
					t["codec"] = d.AudioCodec
					delete(t, "channels")
				}
			}
			audio = append(audio, t)
			continue
		}
		embedded := false
		if original {
			for _, format := range formats {
				if strings.EqualFold(format, track.Codec) {
					embedded = true
					break
				}
			}
		}
		switch {
		case embedded:
			t["delivery"] = object{"kind": "embedded"}
		case frontend.IsImageSubtitle(track.Codec):
			if !canBurn {
				continue
			}
			t["delivery"] = object{"kind": "burn-in"}
		case frontend.IsTextSubtitle(track.Codec):
			url, err := i.host.Sessions.SubtitleURL(ctx, access, id, track.Index, "vtt")
			if err != nil {
				continue
			}
			if original && strings.Contains(url, "ether_window=1") {
				// No active playback tee exists for the original. Offer this
				// track after HLS activates, rather than claiming burn-in or
				// exposing an unusable cue window.
				continue
			}
			format := "webvtt"
			if strings.Contains(url, "ether_window=1") {
				format = "webvtt-window"
			}
			t["codec"], t["url"] = "webvtt", url
			t["delivery"] = object{"kind": "external", "format": format, "url": url}
		default:
			continue
		}
		subs = append(subs, t)
		if track.Index == selectedSubtitle {
			selectedSub = t["id"]
		}
	}
	return audio, subs, selectedAudio, selectedSub
}

func (i *instance) refreshTrackDelivery(ctx context.Context, access frontend.SessionAccess, id string, s *playSession) {
	reader, ok := i.host.Sessions.(frontend.StreamDecisionReader)
	if !ok || len(s.media.Streams) == 0 {
		return
	}
	d, err := reader.StreamDecision(ctx, access, id)
	if err != nil {
		return
	}
	selected := -1
	if track, ok := s.plan["selectedSubtitleTrackId"].(string); ok {
		selected, _ = strconv.Atoi(track)
	}
	audio, subs, selectedAudio, _ := i.playbackTracks(ctx, access, id, s.media, d, selected, false, s.subtitleFormats, s.canBurn)
	s.plan["audioTracks"], s.plan["subtitleTracks"] = audio, subs
	if selectedAudio != "" {
		s.plan["selectedAudioTrackId"] = selectedAudio
	}
	kind := "remux"
	if d.Kind == frontend.DecisionTranscode {
		kind = "transcode"
	}
	path := selectedPlaybackPath(s.plan, s.plan["paths"].([]object))
	path["kind"], path["decisionReason"] = kind, d.Reason
}
