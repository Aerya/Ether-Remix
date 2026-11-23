package sessions

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// sourceTrackIndex returns the absolute stream index at a zero-based position
// among the source's streams of one kind, in source order. Silo and every
// other client that counts positions see the same order. ok is false when the
// position names no stream, when the stream is an external sidecar (not an
// FFmpeg stream of the opened source; it needs its own scoped authorization)
// or when the index is ambiguous within the source.
func sourceTrackIndex(info core.MediaInfo, kind string, ordinal int) (int, bool) {
	if ordinal < 0 || info.Estimated {
		return -1, false
	}
	position := 0
	for _, stream := range info.Streams {
		if stream.Type != kind {
			continue
		}
		if position < ordinal {
			position++
			continue
		}
		if stream.External || stream.ExternalURL != "" || stream.Index < 0 {
			return -1, false
		}
		count := 0
		for _, other := range info.Streams {
			if other.Index == stream.Index {
				count++
			}
		}
		return stream.Index, count == 1
	}
	return -1, false
}

// dropStaleTracks clears every requested track position that the source
// cannot honour: an ordinal from another release, an older probe, an estimate
// or a remembered choice. A stale id is no choice. The viewer's rules then
// pick the track and playback starts; a start never fails over a track id.
func dropStaleTracks(intent *frontend.PlayIntent, info core.MediaInfo) {
	if intent.AudioOrdinal != nil && *intent.AudioOrdinal >= 0 && !intent.AudioSet {
		if _, ok := sourceTrackIndex(info, "audio", *intent.AudioOrdinal); !ok {
			intent.AudioOrdinal = nil
		}
	}
	if intent.SubtitleOrdinal != nil && *intent.SubtitleOrdinal >= 0 && !intent.SubtitleSet {
		if _, ok := sourceTrackIndex(info, "subtitle", *intent.SubtitleOrdinal); !ok {
			intent.SubtitleOrdinal = nil
		}
	}
}

// bindSourceTracks translates only against the pinned opened generation.
// SelectScopedTracks consumes absolute indices, not positions in the slice.
// Positions the source cannot honour are dropped first, so viewer rules choose.
// Only a malformed request (a negative position, or a position alongside an
// already explicit choice) is an error.
func bindSourceTracks(intent *frontend.PlayIntent, info core.MediaInfo) error {
	if intent.AudioOrdinal == nil && intent.SubtitleOrdinal == nil {
		return nil
	}
	if (intent.AudioOrdinal != nil && (intent.AudioSet || *intent.AudioOrdinal < 0)) ||
		(intent.SubtitleOrdinal != nil && (intent.SubtitleSet || *intent.SubtitleOrdinal < -1)) {
		return frontend.ErrTrackNotFound
	}
	dropStaleTracks(intent, info)
	if intent.AudioOrdinal != nil {
		index, _ := sourceTrackIndex(info, "audio", *intent.AudioOrdinal)
		intent.Options.AudioStream, intent.AudioSet = index, true
	}
	if intent.SubtitleOrdinal != nil {
		index := -1
		if *intent.SubtitleOrdinal >= 0 {
			index, _ = sourceTrackIndex(info, "subtitle", *intent.SubtitleOrdinal)
		}
		intent.Options.SubtitleStream, intent.SubtitleSet = index, true
	}
	return nil
}
