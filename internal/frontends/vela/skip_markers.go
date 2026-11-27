package vela

import (
	"context"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// skipMarkerReadBudget bounds the local marker read so a slow database can
// never hold a playback response.
const skipMarkerReadBudget = 100 * time.Millisecond

// skipMarkers is PlaybackPlan.markers: SkipMarker[] where SkipMarker is
// {kind: "intro"|"credits", startSeconds, endSeconds, final?}, on the plan's
// timeline in seconds. It is the same data Plex serves as Marker elements
// (chapters, the marker cache and plugin sources), read from local storage
// only: no fetch, alignment or detection runs here. A stacked title plays as
// one source, so ranges are already on the whole title's timeline. final is
// set only on the last credits range when the source marks it as the tail,
// exactly as Plex sets final="1". Nil (field omitted) means no claim.
func (i *instance) skipMarkers(ctx context.Context, key frontend.RatingKey, media frontend.MediaInfo) []object {
	if i.host.SkipMarkers == nil || key == 0 || media.Estimated || media.Duration <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, skipMarkerReadBudget)
	defer cancel()
	ranges, _ := i.host.SkipMarkers.CachedMarkers(ctx, key, media)
	return skipMarkerWire(ranges, media.Duration)
}

func skipMarkerWire(ranges []frontend.MarkerRange, duration time.Duration) []object {
	ranges = append([]frontend.MarkerRange(nil), ranges...)
	sort.SliceStable(ranges, func(a, b int) bool { return ranges[a].StartMS < ranges[b].StartMS })
	lastCredits := -1
	for n, r := range ranges {
		if r.Type == "credits" {
			lastCredits = n
		}
	}
	var out []object
	for n, r := range ranges {
		if (r.Type != "intro" && r.Type != "credits") || r.StartMS < 0 || r.EndMS <= r.StartMS || r.EndMS > duration.Milliseconds() {
			continue
		}
		m := object{"kind": r.Type, "startSeconds": float64(r.StartMS) / 1000, "endSeconds": float64(r.EndMS) / 1000}
		if r.Final && n == lastCredits {
			m["final"] = true
		}
		out = append(out, m)
	}
	return out
}

// setSkipMarkers writes or clears PlaybackPlan.markers.
func setSkipMarkers(plan object, markers []object) {
	if len(markers) == 0 {
		delete(plan, "markers")
		return
	}
	plan["markers"] = markers
}
