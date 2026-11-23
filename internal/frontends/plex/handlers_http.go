package plex

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// handleStartHTTP is /video/:/transcode/universal/start (no extension):
// Plex for Mac / Desktop's protocol=http Matroska stream.
func (s *Server) handleStartHTTP(w http.ResponseWriter, r *http.Request) {
	if s.refuseCapability(w, r) {
		return
	}
	if s.handleLiveTVPlayback(w, r) {
		return
	}
	if s.handleExtraPlayback(w, r) {
		return
	}
	ctx := r.Context()
	pr := FromContext(ctx)
	unlockOwner, locked := s.lockPlaybackRequest(w, r)
	if !locked {
		return
	}
	defer unlockOwner()

	if s.transcoder() == nil || s.playback() == nil {
		writePlaybackStatus(w, http.StatusServiceUnavailable)
		return
	}
	p, err := s.parsePlayRequest(r)
	if err != nil {
		writePlaybackStatus(w, http.StatusBadRequest)
		return
	}
	if !s.decisionArmed(pr, p.key) {
		if tt := chi.URLParam(r, "ttype"); tt == "audio" || tt == "music" {
			owner := decisionOwner(pr)
			owner.Generation = p.sessionID
			s.decisionArms.Arm(owner, p.key)
		} else {
			writePlaybackStatus(w, http.StatusBadRequest)
			return
		}
	}
	p.protocol = frontend.ProtocolHTTP
	p.opts.Protocol = frontend.ProtocolHTTP
	audioExt := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "ext")))
	if audioExt == "" {
		if tt := chi.URLParam(r, "ttype"); tt == "audio" || tt == "music" {
			if qContainer := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("container"))); qContainer != "" && qContainer != "mkv" {
				audioExt = qContainer
			}
		}
	}
	if audioExt != "" && audioExt != "mkv" {
		p.caps.HLSContainers = []string{audioExt}
		if len(p.caps.TranscodeAudioCodecs) == 0 {
			switch audioExt {
			case "mp3":
				p.caps.TranscodeAudioCodecs = []string{"mp3"}
			case "aac", "m4a", "mp4":
				p.caps.TranscodeAudioCodecs = []string{"aac"}
			case "opus", "ogg":
				p.caps.TranscodeAudioCodecs = []string{"opus"}
			case "flac":
				p.caps.TranscodeAudioCodecs = []string{"flac"}
			}
		}
	}
	unlockEncoder, lockErr := s.lockEncoder(ctx, p.sessionID)
	if lockErr != nil {
		return
	}
	defer unlockEncoder()

	if s.playbackOwners.Stale(pr.User.ID, pr.ClientID, p.key, pr.SessionID) {
		writePlaybackStatus(w, http.StatusGone)
		return
	}

	if !s.transcodes.claim(p.sessionID, pr.User.ID, pr.ClientID, s.now()) {
		writePlaybackStatus(w, http.StatusForbidden)
		return
	}
	if err, failed := s.transcodes.takeFailure(p.sessionID, pr.User.ID, p.key, s.now()); failed && !errors.Is(err, frontend.ErrPrepareSlow) && !errors.Is(err, frontend.ErrPreparing) && !errors.Is(err, frontend.ErrJobsBusy) {
		s.log.Info("start playback failed", "key", p.key, "reason", playbackMessage(err), "err", err, "decision", "remembered")
		s.writeStartErr(w, r, pr, err)
		return
	}
	priorEntry, _ := s.sessionEntryForStart(pr, p.sessionID, p.key)
	startPR := mergeSessionProfile(pr, priorEntry)
	entry, ok := s.transcodes.get(p.sessionID)
	var ps *frontend.PlaySession
	if ok && entry.key == p.key && entry.userID == pr.User.ID {
		ps, ok = s.playback().Session(entry.playID)
		if ok && ps != nil {
			if entry.info.Estimated && !ps.Info.Estimated {
				ok = false
			} else {
				ps = sessionWithInfo(ps, entry.info)
			}
		}
	}
	if !ok || ps == nil || !p.sameSettings(entryExpectedStart(entry)) {
		if _, ps, _, err = s.prepare(ctx, startPR, &p); err != nil {
			s.log.Info("start playback failed", "key", p.key, "reason", playbackMessage(err), "err", err)
			s.writeStartErr(w, r, pr, err)
			return
		}
		entry, _ = s.transcodes.get(p.sessionID)
	} else if dec, err := s.publishBrokerPlayback(ctx, startPR, replayPlay(p, entry), ps); err != nil {
		s.writeStartErr(w, r, pr, err)
		return
	} else {
		entry.decision = dec
	}
	s.playbackOwners.Bind(pr.User.ID, pr.ClientID, p.key, pr.SessionID, ps.ID, true, s.now())
	preserveStartIdentity(entry, pr, priorEntry)
	s.transcodes.put(entry)

	dec := frontend.HLSDecision(entry.decision, p.opts.DirectPlay, func() frontend.Decision {
		opts := entry.opts
		opts.DirectPlay = false
		opts.Protocol = frontend.ProtocolHTTP
		caps := entry.caps
		if s.cfg().Transcode.VideoTranscodingOff() {
			caps.NoVideoTranscode = true
		}
		if ps.Info.Video() == nil && audioExt != "" && audioExt != "mkv" {
			caps.HLSContainers = []string{audioExt}
			if len(p.caps.TranscodeAudioCodecs) > 0 {
				caps.TranscodeAudioCodecs = p.caps.TranscodeAudioCodecs
			}
		}
		return s.transcoder().Decide(ps.Info, caps, opts)
	}, func(dec frontend.Decision) frontend.Decision {
		caps := entry.caps
		if ps.Info.Video() == nil {
			if audioExt != "" && audioExt != "mkv" {
				caps.HLSContainers = []string{audioExt}
				if len(p.caps.TranscodeAudioCodecs) > 0 {
					caps.TranscodeAudioCodecs = p.caps.TranscodeAudioCodecs
				}
			}
			opts := entry.opts
			opts.DirectPlay = false
			opts.DirectStream = true
			opts.Protocol = frontend.ProtocolHTTP
			return s.transcoder().Decide(ps.Info, caps, opts)
		}
		if s.cfg().Transcode.VideoTranscodingOff() {
			caps.NoVideoTranscode = true
		}
		videoBlocked := ps.Info.Video() != nil && caps.EffectiveAlwaysEncodeVideo()
		audioBlocked := len(caps.TranscodeAudioCodecs) > 0 && dec.AudioCodec != "" && !slices.Contains(caps.TranscodeAudioCodecs, dec.AudioCodec)
		if videoBlocked || audioBlocked {
			opts := entry.opts
			opts.DirectPlay = false
			opts.DirectStream = !videoBlocked
			opts.Protocol = frontend.ProtocolHTTP
			return s.transcoder().Decide(ps.Info, caps, opts)
		}
		return dec
	})
	if existing, ok := s.plexHLSSession(p.sessionID); ok && existing != nil {
		if entry.started && entry.offset == p.offset {
			if hs, ok := existing.(frontend.HTTPSession); ok {
				if failed, ok := hs.(frontend.ProgressiveFailureSession); ok && failed.ProgressiveFailed() && r.Header.Get("Range") == "" {
					http.Error(w, "progressive transcode failed; start a new session", http.StatusBadGateway)
					return
				}
				s.rebindSessionTranscode(entry)
				s.notePlexEncoder(p.sessionID, frontend.StreamHTTP, dec, p.offset)
				unlockEncoder()
				unlockOwner()
				hs.ServeHTTP(w, r)
				return
			}
		}
		s.stopPlexHLS(p.sessionID)
	}
	hs, err := s.startPlexHLS(context.WithoutCancel(ctx), pr.User, ps.ID, p.sessionID, frontend.HLSRequest{
		SessionID:        p.sessionID,
		ReleaseID:        ps.SourceReleaseID(),
		InputURL:         s.playback().InputURL(ps.ID),
		NoVideoTranscode: entry.caps.NoVideoTranscode,
		Info:             ps.Info,
		Decision:         dec,
		StartOffset:      ps.SourcePosition(p.offset),
		Protocol:         frontend.ProtocolHTTP,
		CopyTimestamps:   p.copyTimestamps,
	})
	if err != nil {
		s.writeStartErr(w, r, pr, err)
		return
	}
	stream, ok := hs.(frontend.HTTPSession)
	if !ok {
		s.log.Error("transcoder returned a non-HTTP session", "session", p.sessionID)
		s.stopPlexHLS(p.sessionID)
		writePlaybackStatus(w, http.StatusInternalServerError)
		return
	}
	dec = liveSessionDecision(hs, dec)
	s.bindTranscodeStarted(entry, dec, p.offset)
	s.progressiveStops.clear(pr.User.ID, pr.ClientID, pr.SessionID, p.key)
	s.notePlexEncoder(p.sessionID, frontend.StreamHTTP, dec, p.offset)
	unlockEncoder()
	unlockOwner()
	stream.ServeHTTP(w, r)
}
