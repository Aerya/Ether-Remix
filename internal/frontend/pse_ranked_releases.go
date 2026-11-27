package frontend

import "context"

// RankedReleases is an optional Host.Editions capability: every release the
// resolver accepts for this viewer and client, best first (the order Auto
// tries them), from the search cache only. Each ID is a release id that
// VersionHandles can issue as an exact version. Used by the Stremio stream
// list. Voir docs/pse/FORK.fr.md.
type RankedReleases interface {
	RankedReleases(context.Context, ResolveRequest) ([]ReleaseVariant, error)
}
