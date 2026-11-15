package frontend

import "context"

// DescriptiveSearcher is deterministic viewer-scoped collection search. The
// implementation must not wait for a model or provider, or count the library.
type DescriptiveSearcher interface {
	DescriptiveSearch(context.Context, *Viewer, Device, string, ItemQuery) (SearchPage, error)
	DescriptiveSearchAvailable() bool
}

// DescriptiveSearchState describes shared AI work without turning a model
// failure into a transport error.
type DescriptiveSearchState string

const (
	DescriptiveReady   DescriptiveSearchState = "ready"
	DescriptivePartial DescriptiveSearchState = "partial"
	DescriptivePending DescriptiveSearchState = "pending"
	DescriptiveBusy    DescriptiveSearchState = "busy"
	DescriptiveFailed  DescriptiveSearchState = "failed"
)

type DescriptiveSearchStatus struct {
	State             DescriptiveSearchState
	RetryAfterSeconds int
}
