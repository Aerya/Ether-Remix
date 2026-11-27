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

// SearchMatch says what a unified search's results matched.
type SearchMatch string

const (
	// SearchMatchTitle: titles, franchises and people the query names.
	SearchMatchTitle SearchMatch = "title"
	// SearchMatchDescription: titles the AI chose for a described query.
	SearchMatchDescription SearchMatch = "description"
	// SearchMatchMixed: description matches lead, title matches follow.
	SearchMatchMixed SearchMatch = "mixed"
	// SearchMatchNone: nothing matched (yet, when Pending).
	SearchMatchNone SearchMatch = "none"
)

// UnifiedSearcher answers a search as one ranked list: title, franchise,
// person and fuzzy matches, and, when AI is configured and the query reads
// as a description (or names nothing), the Collections Describe pipeline's
// titles leading the list. It never waits on a model, a provider or a
// plugin's remote catalogue: SearchPage.Pending (with
// UnifiedSearchPage.RetryAfterSeconds) asks the client to repeat the same
// search for the rest.
type UnifiedSearcher interface {
	UnifiedSearch(context.Context, *Viewer, Device, string, ItemQuery) (UnifiedSearchPage, error)
}

// UnifiedSearchPage is a unified search's answer.
type UnifiedSearchPage struct {
	SearchPage
	Match SearchMatch
	// Described holds the keys of items the description matched.
	Described map[RatingKey]bool
	// RetryAfterSeconds is when to ask again while Pending.
	RetryAfterSeconds int
}
