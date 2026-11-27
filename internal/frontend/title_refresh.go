package frontend

import "context"

// TitleRefresher refreshes one title's catalogue metadata when the owner asks
// an app to ("Refresh metadata" in Plex, Jellyfin and Emby apps). The caller
// has already authorized the viewer and the title. It returns once the
// refresh is queued; the refresh itself is bounded to that title (a season
// or episode refreshes its show) and never walks a library.
type TitleRefresher interface {
	RefreshTitleMetadata(ctx context.Context, viewer *Viewer, key RatingKey) error
}

// TitleMatchCandidate carries the selected provider identity and display facts.
type TitleMatchCandidate struct {
	Ref   ProviderRef
	Title Title
}

// TitleMatcher searches configured metadata sources and applies an explicit
// provider identity. An empty refs slice searches by name and year.
type TitleMatcher interface {
	SearchTitleMatches(context.Context, MediaType, string, int, []ProviderRef) ([]TitleMatchCandidate, error)
	ApplyTitleMatch(context.Context, RatingKey, ProviderRef) (RatingKey, error)
}
