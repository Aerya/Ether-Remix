package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"math"
)

func (s *Server) timelineBandwidths(pr *Request, key frontend.RatingKey, ids []string, ps *frontend.PlaySession) []model.TimelineBandwidths {
	if pr.Product == "Plex for Windows" || pr.Product == "Plex for Windows Phone" || !s.decisionArmed(pr, key) {
		return nil
	}
	var info frontend.MediaInfo
	var dec frontend.Decision
	found := false
	if te, ok := s.activeTranscodeForClient(pr.User.ID, pr.ClientID, pr.SessionID, key, ""); ok {
		live := s.liveTranscode(te)
		info, dec, found = live.info, live.decision, true
	}
	if !found {
		var best *transcodeEntry
		for _, id := range ids {
			if e, ok := s.transcodes.get(id); ok && e.failErr == nil && e.userID == pr.User.ID && e.clientID == pr.ClientID && e.key == key && (e.playbackID == pr.SessionID || e.playbackID == "") {
				if best == nil || e.updatedAt.After(best.updatedAt) || (e.updatedAt.Equal(best.updatedAt) && e.id > best.id) {
					best = e
				}
			}
		}
		if best != nil {
			info, dec, found = best.info, best.decision, true
		}
	}
	if !found && ps != nil {
		info = ps.Info
		dec.Kind = ps.Decision
		if ps.DeliveryDecision != nil {
			dec = *ps.DeliveryDecision
		}
		found = true
	}
	if !found {
		return nil
	}
	w, h, srcH := 0, 0, 0
	if v := info.Video(); v != nil {
		srcH = v.Height
		w, h = transcodeOutputSize(v, dec)
	}

	return []model.TimelineBandwidths{{Bandwidth: []model.TimelineBandwidth{{Time: 0, Bandwidth: plannedBandwidth(info, dec), Resolution: transcodeResolutionLabel(w, h, srcH, dec)}}}}
}

func (s *Server) timelineTranscodeSessions(pr *Request, key frontend.RatingKey, ids []string) []model.TranscodeSess {
	te, ok := s.activeTranscodeForClient(pr.User.ID, pr.ClientID, pr.SessionID, key, "")
	if !ok {
		return nil
	}
	live := s.liveTranscode(te)
	if live.decision.Kind == frontend.DecisionDirectPlay && live.decision.VideoCopy && live.decision.AudioCopy {
		return nil
	}
	return []model.TranscodeSess{s.servedTranscodeSessionModel(te)}
}

func plannedBandwidth(info frontend.MediaInfo, dec frontend.Decision) int {
	bitrate := info.BitrateKbps
	factor := 2.1
	if dec.Kind == frontend.DecisionRemux || dec.Kind == frontend.DecisionTranscode {
		factor = 1.05
		if !dec.VideoCopy && dec.BitrateKbps > 0 {
			bitrate = dec.BitrateKbps
		}
	}
	scaled := math.Max(0, math.Round(float64(bitrate)*factor))
	rate := int(^uint(0) >> 1)
	if scaled < float64(rate) {
		rate = int(scaled)
	}
	return rate
}

func (s *Server) sessionBandwidth(e playEntry, activeTE *transcodeEntry, md model.Metadata) int {
	if activeTE != nil {
		live := s.liveTranscode(activeTE)
		dec := live.decision
		if dec.Kind == "" || dec.Kind == frontend.DecisionDirectPlay {
			if !dec.VideoCopy {
				dec.Kind = frontend.DecisionTranscode
			} else {
				dec.Kind = frontend.DecisionRemux
			}
		}
		return plannedBandwidth(live.info, dec)
	}
	if e.node == "" {
		if pb := s.playback(); pb != nil {
			if ps, ok := pb.SessionFor(e.user.ID, e.client.ClientID, e.key); ok && ps != nil {
				return plannedBandwidth(ps.Info, frontend.Decision{Kind: frontend.DecisionDirectPlay})
			}
		}
		return 0
	}
	if len(md.Media) > 0 && md.Media[0].Bitrate > 0 {
		dec := frontend.Decision{Kind: frontend.DecisionDirectPlay}
		if md.TranscodeSession != nil {
			dec.Kind = frontend.DecisionRemux
		}
		return plannedBandwidth(frontend.MediaInfo{BitrateKbps: md.Media[0].Bitrate}, dec)
	}
	return 0
}
