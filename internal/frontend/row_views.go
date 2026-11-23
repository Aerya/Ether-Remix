package frontend

import (
	"context"
	"time"
)

// RowView identifies one server-owned, viewer/device/surface-scoped visit.
// ID binds one immutable snapshot and changes on refresh.
type RowView struct {
	// ExpiresAt bounds caching of this immutable visit. Zero means unknown.
	ExpiresAt   time.Time
	ID          string
	Title, Kind string
	Purpose     RowPurpose
	Layout      RowLayout
	Rank        int
}

type RowViewRequest struct {
	Viewer    *Viewer
	Device    Device
	Protocol  Protocol
	Placement Placement
	ViewID    string
	Item      RatingKey
	// Refresh starts new snapshots and invalidates earlier page generations.
	Refresh bool
}

type RowViewPageRequest struct {
	Viewer        *Viewer
	Device        Device
	Protocol      Protocol
	ID            string
	Offset, Limit int
	Kinds         []RowEntryKind
}

type RowViewPage struct {
	// ExpiresAt is the earliest expiry of the visit and its page receipts.
	ExpiresAt        time.Time
	RankedTop10      bool
	Entries          []RowEntry
	ImpressionHandle string
	Total            int
	TotalExact       bool
	More             bool
	// Truncated means the snapshot is a bounded prefix of the source row.
	// More only indicates entries available in this snapshot.
	Truncated bool
}

// RowViews is optional; IDs are not credentials. Implementations
// must reauthorize each call and issue page-scoped interaction handles.
type RowViews interface {
	ListRowViews(context.Context, RowViewRequest) ([]RowView, error)
	ResolveRowView(context.Context, RowViewPageRequest) (RowViewPage, error)
}

// RowViewPages resolves one bounded group of pages in request order. Every
// page belongs to the same viewer/device/surface; admission remains atomic.
type RowViewPages interface {
	ResolveRowViews(context.Context, []RowViewPageRequest) ([]RowViewPage, error)
}
