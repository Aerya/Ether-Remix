package sessions

import (
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// DecisionRequest contains the source and client inputs for a playback
// decision. PreferRemux is the adapter's signal that source changes need
// refreshed container headers; the source and requested direct-stream mode
// still determine whether the override applies.
type DecisionRequest struct {
	Session              *core.PlaySession
	Caps                 core.ClientCaps
	Options              core.DecideOptions
	PreferRemux          bool
	Protocol             string
	RemoteMaxBitrateKbps int
	// PreserveSourceOnFailure lets a broker avoid stopping a reused source
	// when an attempted decision cannot be served without a transcoder.
	PreserveSourceOnFailure bool
	// NoVideoTranscode keeps a direct-playable source on DirectPlay rather than
	// letting optional PreferRemux promote it into a video encode.
	NoVideoTranscode bool
}

// Decide preserves the playback remux override for on-demand sources and
// stops the source when a required converter is unavailable.
func Decide(playback core.Playback, transcoder core.Transcoder, r DecisionRequest) (core.Decision, error) {
	ps := r.Session
	if ps == nil {
		return core.Decision{}, core.ErrNotFound
	}
	if r.NoVideoTranscode {
		r.Caps.NoVideoTranscode = true
	}
	dec := core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: !ps.Info.AudioOnly(), AudioCopy: true, Container: ps.Info.Container, AudioStream: r.Options.AudioStream, SubtitleStream: r.Options.SubtitleStream}
	if transcoder != nil {
		dec = transcoder.Decide(ps.Info, r.Caps, r.Options)
	} else {
		dec = transcode.Decide(ps.Info, r.Caps, r.Options, transcode.Policy{RemoteMaxBitrateKbps: r.RemoteMaxBitrateKbps})
		if dec.Kind != core.DecisionDirectPlay {
			if playback != nil && !r.PreserveSourceOnFailure {
				playback.Stop(ps.ID)
			}
			return core.Decision{}, &frontend.NoCompatiblePathError{RejectedPaths: []frontend.RejectedPlaybackPath{{Path: "conversion", Reasons: []string{"media conversion requires FFmpeg"}}}}
		}
	}
	// Usenet sources can change during failover. Copy-only remux gives MDE
	// clients fresh container headers; RequiresRemux applies to all clients.
	// Debrid's stable HTTP file does not need an optional remux.
	onDemand := ps.Resolution != nil && !strings.HasPrefix(ps.Resolution.Release.ID, "local:")
	requiresRemux := ps.RequiresRemux
	if rr, ok := playback.(core.RemuxRequirer); ok && rr.RequiresRemux(ps.ID) {
		requiresRemux = true
	}
	healthSafety := onDemand && !ps.Resolution.HealthVerified
	preferRemux := (r.PreferRemux || healthSafety) && onDemand && ps.Resolution.Release.Protocol != core.ProtocolTorrent &&
		ps.Resolution.Release.Protocol != core.ProtocolHTTP
	// Keep native Dolby Vision file playback on Android when the decision
	// accepted the container, tracks and DV profile. Optional source-change
	// remuxing must not send it through a different player/demuxer. A required
	// remux and an explicit client rejection of direct play still win.
	if v := ps.Info.Video(); v != nil && v.DolbyVision && r.Options.DirectPlay &&
		strings.EqualFold(r.Caps.Platform, "Android") && transcode.SupportsDVProfile(r.Caps, v.DolbyVisionProfile) {
		preferRemux = false
	}
	// Keep native file playback when the client Direct Plays the file's video
	// codec (for example 4K HEVC HDR10 on webOS/VIDAA TVs) while its segmented
	// streaming target only accepts another codec (such as H.264). Optional
	// source-change remuxing must not push Direct Play into an unplayable HEVC
	// HLS copy or an unnecessary 4K transcode (#408). A required remux or an
	// explicit client rejection of direct play still wins.
	if v := ps.Info.Video(); v != nil && v.Codec != "" && r.Options.DirectPlay &&
		len(r.Caps.TranscodeVideoCodecs) > 0 && !slices.Contains(r.Caps.TranscodeVideoCodecs, v.Codec) {
		preferRemux = false
	}
	if healthSafety && !r.PreferRemux && r.Caps.Profiles != nil && len(r.Caps.Profiles.DirectPlay) > 0 && len(r.Caps.Profiles.Transcoding) == 0 {
		preferRemux = false
	}
	original := dec
	if (preferRemux || requiresRemux) && r.Options.DirectStream && onDemand && dec.Kind == core.DecisionDirectPlay && transcoder != nil {
		dec.Kind = core.DecisionRemux
		dec.VideoCopy, dec.AudioCopy = !ps.Info.AudioOnly(), true
		videoCodec, audioCodec := "", ""
		if v := ps.Info.Video(); v != nil {
			videoCodec = v.Codec
		}
		for _, st := range ps.Info.Streams {
			if st.Type == "audio" && st.Index == dec.AudioStream {
				audioCodec = st.Codec
				break
			}
		}
		if r.Protocol == core.ProtocolDASH {
			dec.Container = transcode.ContainerFMP4
		} else {
			dec.Container = transcode.HLSContainer(r.Caps, videoCodec, audioCodec)
		}
		dec.Reason = "Direct stream preserves playback across source changes."
		if healthSafety {
			dec.Reason = "remux for failover safety: health unverified"
		}
		videoCopyOK := !(ps.Info.Video() != nil && r.Caps.EffectiveAlwaysEncodeVideo()) && (videoCodec == "" || transcode.SegmentedVideoCopyOK(r.Caps, videoCodec))
		audioCopyOK := audioCodec == "" || transcode.SegmentedAudioCopyOK(r.Caps, audioCodec)
		if !videoCopyOK || !audioCopyOK {
			opts := r.Options
			opts.DirectPlay = false
			if !videoCopyOK {
				opts.DirectStream = false
			}
			dec = transcoder.Decide(ps.Info, r.Caps, opts)
		}
	}
	// Optional health safety must never force quality loss or an encoder on a
	// client that can only play the original representation. Likewise, when
	// video transcoding is off, an optional PreferRemux must never push a
	// direct-playable file into a video encode.
	if original.Kind == core.DecisionDirectPlay && !requiresRemux &&
		((healthSafety && !r.PreferRemux && (dec.EncodesVideo(ps.Info) || !dec.AudioCopy)) ||
			(r.NoVideoTranscode && dec.EncodesVideo(ps.Info))) {
		dec = original
	}
	// The optional health-safety remux above is assembled from per-codec
	// checks. Take it only when the real segmented decision is a lossless
	// copy: a stream that drops Dolby Vision to its base layer or cannot
	// carry the source audio (TrueHD in fMP4) is worse than the file the
	// client already plays, and AVPlayer refuses some of them outright.
	if healthSafety && !requiresRemux && !r.PreferRemux && original.Kind == core.DecisionDirectPlay && dec.Kind == core.DecisionRemux && transcoder != nil {
		opts := r.Options
		opts.DirectPlay = false
		segmented := transcoder.Decide(ps.Info, r.Caps, opts)
		if segmented.EncodesVideo(ps.Info) || !segmented.AudioCopy || (segmented.StripDolbyVision && !original.StripDolbyVision) {
			dec = original
		}
		// The codec lists say the copy fits, but a declared streaming profile
		// may not take it (Wholphin, Jellyfin for Android and Silo stream only
		// H.264 in TS). The profile check would then replace the optional
		// copy with a full encode; the original file plays as it is.
		if dec.Kind == core.DecisionRemux && r.Caps.Profiles != nil && len(r.Caps.Profiles.Transcoding) > 0 {
			delivery := "hls"
			if r.Protocol == core.ProtocolDASH {
				delivery = "dash"
			}
			if !profileDeliveryAllowed(r.Caps.Profiles, dec, ps.Info, delivery) {
				dec = original
			}
		}
	}
	return dec, nil
}

// recordPlaybackDecision tells the playback manager which delivery was
// actually started for a source, so its session snapshots (Activity ›
// Now playing, /status/sessions) show the current decision instead of a
// stale direct read or none at all. A remux or transcode also satisfies
// a pending RequiresRemux on the playback session. Callers must not hold
// the broker lock: the playback manager notifies its own subscribers.
func (b *Broker) recordPlaybackDecision(playID string, dec core.Decision) {
	if b == nil || playID == "" || dec.Kind == "" {
		return
	}
	if rec, ok := b.deps.Playback.(core.PlaybackDecisionRecorder); ok {
		rec.SetDecision(playID, dec)
	}
}

// HLSDecision lives in internal/frontend (host_sessions_experience.go).
var HLSDecision = frontend.HLSDecision

// playMethod maps a broker decision to the MediaBrowser PlayMethod vocabulary.
func playMethod(kind core.DecisionKind) string {
	switch kind {
	case core.DecisionDirectPlay:
		return "DirectPlay"
	case core.DecisionRemux:
		return "DirectStream"
	case core.DecisionTranscode:
		return "Transcode"
	}
	return ""
}
