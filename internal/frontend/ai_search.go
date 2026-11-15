package frontend

import "context"

// AISearcher refines a description through the shared Collections Describe
// pipeline. Calls may await the model and belong in an explicit asynchronous
// protocol job, never in a browse, title search, or descriptive cache read.
// Implementations cache normalized queries and apply viewer policy and ranking.
type AISearcher interface {
	// AISearchVisible rechecks a bounded cached page using local title facts and
	// current viewer policy, without detail enrichment or provider calls.
	AISearchVisible(context.Context, *Viewer, []RatingKey) ([]Title, error)
	AISearchAvailable() bool
	AISearch(context.Context, *Viewer, Device, string, ItemQuery) (SearchPage, error)
}

// ProgressiveAISearcher publishes provisional, viewer-filtered pages during
// the same waiting AI job. Callers replace these with the returned final page.
type ProgressiveAISearcher interface {
	AISearchProgress(context.Context, *Viewer, Device, string, ItemQuery, func(SearchPage)) (SearchPage, error)
}
