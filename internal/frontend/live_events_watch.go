package frontend

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// LiveEventRef aliases core.LiveEventRef for Vela playback snapshots and
// session SSE `event: liveEvent` frames (A6).
type LiveEventRef = core.LiveEventRef

// LiveTickerItem aliases core.LiveTickerItem for GET /vela/v0/live/ticker (A6).
type LiveTickerItem = core.LiveTickerItem

// LiveEventLinker resolves the live event matching the programme currently on
// air for a viewer's live channel (reverse of A5, cached per programme).
// programmeKey identifies the airing programme so Vela detects programme
// rollovers and emits `event: liveEvent` on the session SSE.
type LiveEventLinker interface {
	LiveEventForChannel(ctx context.Context, viewer *Viewer, channelID string) (programmeKey string, ref *LiveEventRef, err error)
}

// LiveEventTicker supplies compact, viewer-scoped, spoiler-filtered live
// events for GET /vela/v0/live/ticker?screen=.
type LiveEventTicker interface {
	LiveTicker(ctx context.Context, viewer *Viewer, screen string) ([]LiveTickerItem, error)
}
