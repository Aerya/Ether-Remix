package vela

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// refreshOutput reads only existing sessions. Codec claims come from real init
// bytes; planned and unused rungs never start work. The session must be
// unpublished or a private snapshot; never hold the live UI lock here.
func (i *instance) refreshOutput(ctx context.Context, id string, s *playSession) bool {
	liveChanged := i.refreshLiveWindow(ctx, id, s)
	hs, ok := i.host.Sessions.TranscodeFor(ctx, id)
	if !ok || hs == nil {
		return liveChanged
	}
	paths, _ := s.plan["paths"].([]object)
	before, _ := json.Marshal(object{"paths": paths, "preparation": s.plan["preparation"]})
	for _, path := range paths {
		url, _ := path["url"].(string)
		if path["transport"] != "hls" || url == "" {
			continue
		}
		if clock, ok := hs.(interface{ TimelineOrigin() time.Duration }); ok {
			path["timelineOriginSeconds"] = clock.TimelineOrigin().Seconds()
		}
		if ladder, ok := hs.(interface{ Rungs() []frontend.HLSRung }); ok {
			qualities := []object{}
			delete(path, "initialRenditionId")
			base := hlsDirectory(url)
			for _, r := range ladder.Rungs() {
				if r.Width <= 0 || r.Height <= 0 || r.Bandwidth <= 0 || r.ID == "" || strings.ContainsAny(r.ID, "/\\?#%\r\n") {
					continue
				}
				q := qualityTarget(r)
				q["id"], q["url"] = r.ID, base+r.ID+"/index.m3u8"
				if r.OutputCodecs != "" {
					codecs := strings.Split(r.OutputCodecs, ",")
					q["videoCodec"], q["codecStatus"] = codecs[0], "verified"
					if len(codecs) > 1 {
						q["audioCodec"] = codecs[1]
						if path["id"] == s.plan["selectedPathId"] {
							tracks, _ := s.plan["audioTracks"].([]object)
							for _, track := range tracks {
								if track["id"] == s.plan["selectedAudioTrackId"] && r.OutputAudioChannels > 0 {
									track["channels"] = r.OutputAudioChannels
								}
							}
						}
					}
				}
				initial := s.rungFor(path)
				if initial == "" {
					initial = "base"
				}
				if r.ID == initial {
					path["initialRenditionId"] = r.ID
					if path["quality"] != nil {
						target := qualityTarget(r)
						target["videoCodec"], target["codecStatus"] = q["videoCodec"], q["codecStatus"]
						path["quality"] = target
					}
				}
				qualities = append(qualities, q)
			}
			path["qualities"] = qualities
		}
	}
	if diagnostics, ok := hs.(frontend.HLSDiagnosticSession); ok {
		d := diagnostics.PlaybackDiagnostics()
		s.diagnostics = d.Records
		events := append([]frontend.HLSPreparationEvent{}, s.preparation...)
		events = append(events, d.Events...)
		s.plan["preparation"] = object{"sourceGeneration": s.source, "seekGeneration": s.seek, "events": events}
	}
	after, _ := json.Marshal(object{"paths": paths, "preparation": s.plan["preparation"]})
	return liveChanged || string(before) != string(after)
}

// refreshLiveWindow never opens an output. The playlist response headers remain
// authoritative when publication advances between this read and the fetch.
func (i *instance) refreshLiveWindow(ctx context.Context, id string, s *playSession) bool {
	live, ok := s.plan["live"].(object)
	if !ok {
		return false
	}
	before, _ := json.Marshal(live)
	delete(live, "anchor")
	delete(live, "windowStartSeconds")
	delete(live, "liveEdgeSeconds")
	if reader, ok := i.host.Sessions.(frontend.LiveWindowReader); ok {
		if window, err := reader.LiveWindow(ctx, id); err == nil && window.Anchor.MediaSequence >= 0 && window.Anchor.MediaSequence <= 1<<53-1 {
			live["anchor"] = window.Anchor
			live["windowStartSeconds"] = window.Anchor.WindowStartSeconds
			live["liveEdgeSeconds"] = window.LiveEdgeSeconds
		}
	}
	after, _ := json.Marshal(live)
	return string(before) != string(after)
}
