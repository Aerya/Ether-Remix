package frontend

import "context"

// RatingScore preserves a stored provider's native scale. Missing values are
// omitted; State is supplied only when the provider's score determines it.
type RatingScore struct {
	Source string  `json:"source"`
	Kind   string  `json:"kind"`
	Value  float64 `json:"value"`
	Scale  float64 `json:"scale"`
	State  string  `json:"state,omitempty"`
	Votes  int64   `json:"votes,omitempty"`
}

// CachedRatings never refreshes providers or queues work on a cache miss.
// Callers pass only titles already authorized for the viewer.
type CachedRatings interface {
	PageCachedRatings(context.Context, *Viewer, []Title) map[RatingKey][]RatingScore
}
