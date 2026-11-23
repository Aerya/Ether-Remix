package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// VersionScope binds a version handle to an authenticated viewer, surface,
// device and host-built client capabilities. It grants no playback by itself.
type VersionScope struct {
	Viewer   *core.Viewer
	Device   core.Device
	Protocol Protocol
	Caps     core.ClientCaps
}

// VersionTarget is the authorized parent associated with an opaque handle.
// A caller must still open playback and revalidate the selected source.
type VersionTarget struct {
	Item core.RatingKey
	// Release is the candidate id stored with the handle. Empty when the
	// handle was resolved by an older caller that only needed the parent.
	Release string
}

type VersionHandles interface {
	Issue(context.Context, VersionScope, core.RatingKey, string) (string, error)
	Resolve(context.Context, VersionScope, string) (VersionTarget, error)
	// MatchSession verifies that a scoped handle denotes the source bound to
	// this authenticated local playback session.
	MatchSession(context.Context, VersionScope, string, string) error
}

// StaleVersionResolver identifies a previously issued version handle on this
// surface whose release is no longer offered, while still verifying the viewer
// can access its parent item.
type StaleVersionResolver interface {
	ResolveStaleParent(context.Context, VersionScope, string) (core.RatingKey, error)
}

// Editions is an optional local-media catalogue and release variant resolver.
type Editions interface {
	Editions(context.Context, *core.Viewer, core.Device, core.RatingKey) ([]Version, error)
	Variants(ctx context.Context, req core.ResolveRequest, max int) ([]core.ReleaseVariant, error)
	RefillSearchCache(t *core.Title) <-chan struct{}
	Available(ctx context.Context, key core.RatingKey) (bool, bool)
}

// EditionBatch resolves local copies for a page in one lookup.
type EditionBatch interface {
	EditionsMany(context.Context, *core.Viewer, core.Device, []core.RatingKey) (map[core.RatingKey][]Version, error)
}

// VersionHandleBatch is an optional bounded batch capability. It validates the
// whole requested offer set once and issues all handles atomically.
type VersionHandleBatch interface {
	IssueBatch(context.Context, VersionScope, core.RatingKey, []string) (map[string]string, error)
}

// VersionSlots is the cache-independent, configured version menu capability.
// labels permits cached release facts on detail responses; false is the cheap
// browse projection and must never search or resolve a title.
type VersionSlots interface {
	VersionSlots(context.Context, core.ResolveRequest, int, bool) ([]core.ReleaseVariant, error)
}

// VersionChoices inspects and clears a viewer's remembered version choice
// (stored beside owner pins in the resolver).
type VersionChoices interface {
	ViewerChoiceFor(context.Context, *core.Viewer, *core.Title) (*core.ViewerChoiceSummary, error)
	ClearViewerChoice(context.Context, *core.Viewer, *core.Title, string) (bool, error)
}

// VersionSwitcher swaps the underlying source of an active playback session to
// another offered version at the given position. When the target version needs
// a long fetch it leaves the active session undisturbed and returns ErrPreparing.
type VersionSwitcher interface {
	SwitchVersion(ctx context.Context, a SessionAccess, sessionID, versionHandle string, position time.Duration) (PlayPlan, error)
}
