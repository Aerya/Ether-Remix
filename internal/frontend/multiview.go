package frontend

import (
	"context"
	"errors"
	"time"
)

// MaxMultiviewTiles is the maximum number of simultaneous live tiles in one
// multiview group (tile indices 0..MaxMultiviewTiles-1).
const MaxMultiviewTiles = 4

// ErrInvalidMultiview reports a malformed or out-of-range multiview request.
var ErrInvalidMultiview = errors.New("invalid multiview request")

// MultiviewError wraps a multiview group validation or ownership error (such
// as a missing, expired, superseded, or unauthorized group) so frontends can
// distinguish group errors from live channel tune errors that share a sentinel.
type MultiviewError struct {
	Cause error
}

func (e *MultiviewError) Error() string {
	if e == nil || e.Cause == nil {
		return "multiview error"
	}
	return e.Cause.Error()
}

func (e *MultiviewError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// MultiviewTarget binds a live playback request to a tile within an active
// multiview group owned by the same viewer, protocol and device.
type MultiviewTarget struct {
	GroupID   string
	Tile      int
	TileCount int // optional declared layout tile count (1..MaxMultiviewTiles)
	MaxHeight int // optional client-declared tile height ceiling in pixels
}

// MultiviewGroup describes a created multiview group.
type MultiviewGroup struct {
	GroupID   string
	MaxTiles  int
	ExpiresAt time.Time
}

// MultiviewSessions is implemented by session brokers that manage per-device
// multiview groups of concurrent live playback tiles.
type MultiviewSessions interface {
	MultiviewAvailable() bool
	CreateMultiviewGroup(ctx context.Context, access SessionAccess) (MultiviewGroup, error)
	DeleteMultiviewGroup(ctx context.Context, access SessionAccess, groupID string) error
}
