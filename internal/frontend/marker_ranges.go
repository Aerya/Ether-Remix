package frontend

import "context"

// MarkerRange is a source-specific skip range. Final identifies a terminal
// credits range; adapters decide how to encode that hint on their wire.
type MarkerRange struct {
	Type    string `json:"type"`
	StartMS int64  `json:"startTimeOffset"`
	EndMS   int64  `json:"endTimeOffset"`
	Final   bool   `json:"final"`
	Source  string `json:"source"`
}

type MarkerRanges interface {
	Markers(context.Context, RatingKey, MediaInfo) ([]MarkerRange, error)
}

// CachedMarkerRanges reads skip ranges already held locally for a probed
// source: embedded chapters, the marker cache and plugin results. It never
// fetches, aligns or detects, so request paths can call it without waiting.
type CachedMarkerRanges interface {
	CachedMarkers(context.Context, RatingKey, MediaInfo) ([]MarkerRange, error)
}
