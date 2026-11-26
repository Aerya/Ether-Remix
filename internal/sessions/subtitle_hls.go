package sessions

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// A native HLS player (AVPlayer, as Swiftfin and other Apple TV and iOS
// Jellyfin apps use it) declares WebVTT delivered by "Hls". It reads subtitles
// only as renditions of the HLS master and never fetches a sidecar
// DeliveryUrl, and a sidecar of an embedded track could only be served after
// the whole release had been read. Such a client gets each embedded SRT or
// WebVTT track as a WebVTT rendition that rides the same input read as the
// picture, as Plex's Apple clients do, so the cues around the playhead arrive
// with the video.

type hlsTextSubtitles interface {
	HasTextSubtitle(int) bool
	ServeTextSubtitlePlaylist(http.ResponseWriter, *http.Request, int)
	ServeTextSubtitleSegment(http.ResponseWriter, *http.Request, int, int)
}

// hlsTextRenditionClient reports a Jellyfin client that takes WebVTT as HLS
// renditions in a server HLS stream. That is a client whose only text
// delivery is HLS, or one that asks for subtitles in the manifest
// (EnableSubtitlesInManifest) and lists a WebVTT Hls profile, as Jellyfin
// itself requires, even when it also lists External. A sidecar of an
// embedded track has to extract the whole track from the remote source
// first: Wholphin's subtitle in a transcode of a 16 GB debrid remux timed out
// (502) on every retry, while the rendition rides the input the encoder
// already reads. Direct play keeps its embedded and sidecar delivery.
func hlsTextRenditionClient(protocol string, caps core.ClientCaps) bool {
	if caps.HLSTextRenditions || hlsOnlyTextClient(protocol, caps) {
		return true
	}
	if protocol != "jellyfin" || caps.Profiles == nil {
		return false
	}
	manifest := false
	for _, t := range caps.Profiles.Transcoding {
		if t.SubtitlesInManifest && strings.EqualFold(t.Delivery, "hls") && (t.MediaType == "" || t.MediaType == "video") {
			manifest = true
		}
	}
	if !manifest {
		return false
	}
	for _, p := range caps.Profiles.Subtitles {
		switch strings.ToLower(p.Format) {
		case "vtt", "webvtt":
			if strings.EqualFold(p.Method, "hls") {
				return true
			}
		}
	}
	return false
}

// hlsOnlyTextClient is a Jellyfin client whose only way to show an embedded
// text track is an HLS rendition, so selecting one rules out direct play.
func hlsOnlyTextClient(protocol string, caps core.ClientCaps) bool {
	if protocol != "jellyfin" || caps.Profiles == nil {
		return false
	}
	for _, p := range caps.Profiles.Subtitles {
		switch strings.ToLower(p.Format) {
		case "vtt", "webvtt":
			if strings.EqualFold(p.Method, "hls") {
				return true
			}
			if strings.EqualFold(p.Method, "external") {
				return false
			}
		case "srt", "subrip":
			if strings.EqualFold(p.Method, "external") {
				return false
			}
		}
	}
	return false
}

// hlsTextTracks are the embedded text tracks such a client receives as HLS
// renditions.
func hlsTextTracks(protocol string, caps core.ClientCaps, info core.MediaInfo) []int {
	if !hlsTextRenditionClient(protocol, caps) {
		return nil
	}
	var tracks []int
	for _, track := range info.Streams {
		if track.Type == "subtitle" && !track.External && hlsTextCodec(track.Codec) {
			tracks = append(tracks, track.Index)
		}
	}
	return tracks
}

func hlsTextCodec(codec string) bool {
	switch codec {
	case "subrip", "srt", "webvtt", "vtt":
		return true
	}
	return false
}

// textSubtitleStreams are the tracks an HLS session tees from its input:
// the progressive web player's and the native HLS client's.
func textSubtitleStreams(protocol string, caps core.ClientCaps, info core.MediaInfo) []int {
	tracks := progressiveTextTracks(protocol, caps, info)
	for _, index := range hlsTextTracks(protocol, caps, info) {
		if !slices.Contains(tracks, index) {
			tracks = append(tracks, index)
		}
	}
	return tracks
}

// hlsTextRenditionURL is the rendition playlist of a track the session's HLS
// stream carries; ok is false when the track is not delivered that way.
func (b *Broker) hlsTextRenditionURL(s *brokerSession, info core.MediaInfo, index int) (string, bool) {
	if s.format != frontend.StreamHLS || !slices.Contains(hlsTextTracks(string(s.public.Protocol), s.caps, info), index) {
		return "", false
	}
	if hs, ok := b.transcodeSession(s.public.ID); ok && hs != nil {
		source, ok := hs.(hlsTextSubtitles)
		if !ok || !source.HasTextSubtitle(index) {
			return "", false
		}
	}
	return fmt.Sprintf("/sessions/media/%s/%s/text-%d/index.m3u8", s.public.ID, s.grant, index), true
}

// textRenditionTags lists the session's text renditions for its master and
// the SUBTITLES attribute its variants reference them with.
func textRenditionTags(s *brokerSession, hs core.HLSSession, info core.MediaInfo) (string, string) {
	source, ok := hs.(hlsTextSubtitles)
	if !ok || s.format != frontend.StreamHLS {
		return "", ""
	}
	tracks := hlsTextTracks(string(s.public.Protocol), s.caps, info)
	// All values in quoted HLS attributes must fit one line.
	clean := strings.NewReplacer("\"", "'", "\r", " ", "\n", " ").Replace
	var tags strings.Builder
	for _, track := range info.Streams {
		if track.Type != "subtitle" || !slices.Contains(tracks, track.Index) || !source.HasTextSubtitle(track.Index) {
			continue
		}
		selected := "NO"
		if track.Index == s.opts.SubtitleStream {
			selected = "YES"
		}
		forced := "NO"
		if track.Forced {
			forced = "YES"
		}
		fmt.Fprintf(&tags, "#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"%s\",NAME=\"%s\",", frontend.HLSTextRenditionGroup, frontend.HLSTextRenditionName(track.Title, track.Language, track.Index))
		if track.Language != "" {
			fmt.Fprintf(&tags, "LANGUAGE=\"%s\",", clean(track.Language))
		}
		fmt.Fprintf(&tags, "DEFAULT=%s,AUTOSELECT=YES,FORCED=%s,URI=\"text-%d/index.m3u8\"\n", selected, forced, track.Index)
	}
	if tags.Len() == 0 {
		return "", ""
	}
	return tags.String(), ",SUBTITLES=\"subs\""
}

// serveTextRendition answers text-N/index.m3u8 and text-N/NNNNN.vtt.
func serveTextRendition(w http.ResponseWriter, r *http.Request, hs core.HLSSession, tail []string) bool {
	if len(tail) != 2 || !strings.HasPrefix(tail[0], "text-") {
		return false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(tail[0], "text-"))
	source, ok := hs.(hlsTextSubtitles)
	if err != nil || index < 0 || !ok || !source.HasTextSubtitle(index) {
		http.NotFound(w, r)
		return true
	}
	if tail[1] == "index.m3u8" {
		source.ServeTextSubtitlePlaylist(w, r, index)
		return true
	}
	if base, found := strings.CutSuffix(tail[1], ".vtt"); found {
		if n, err := strconv.Atoi(base); err == nil && n >= 0 {
			source.ServeTextSubtitleSegment(w, r, index, n)
			return true
		}
	}
	http.NotFound(w, r)
	return true
}
