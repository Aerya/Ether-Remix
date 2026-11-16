package core

import (
	"context"
	"net/url"
	"strings"
)

// SeedstremPlay identifies both new and already persisted Seedstrem play links.
func SeedstremPlay(r Release) bool {
	u, err := url.Parse(r.StreamURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(p) < 3 || p[len(p)-3] != "stremio" || p[len(p)-2] != "play" {
		return false
	}
	h := p[len(p)-1]
	if len(h) != 40 {
		return false
	}
	for _, c := range strings.ToLower(h) {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

type playbackIntentKey struct{}

// PlaybackIntent excludes diagnostics, availability overrides and unpromoted
// warm-ups. A promoted playback warm-up retains its warmup purpose tag.
func PlaybackIntent(ctx context.Context) bool {
	if IsSpeculative(ctx) || IsUnattendedSearch(ctx) || IsBackgroundCheck(ctx) || PSEAvailabilityCheck(ctx) || !OnDemandStreamsAllowed(ctx) {
		return false
	}
	p := IndexerPurpose(ctx)
	if p == IndexerPurposeRescue {
		v, _ := ctx.Value(playbackIntentKey{}).(bool)
		return v
	}
	return p == IndexerPurposePlayback || p == IndexerPurposeWarmup
}

// PreservePlaybackIntent carries a real Play through rescue purpose tags.
func PreservePlaybackIntent(ctx context.Context) context.Context {
	if PlaybackIntent(ctx) {
		return context.WithValue(ctx, playbackIntentKey{}, true)
	}
	return ctx
}
