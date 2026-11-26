package core

import ()

// Watch target kinds returned by A5 "Where to watch".
const (
	WatchTargetKindChannel = LiveEventWatchChannel
	WatchTargetKindAddon   = LiveEventWatchAddon
	WatchTargetKindPlugin  = LiveEventWatchPlugin
)

// WatchTarget aliases LiveEventWatchTarget so W255, W256, and W257 share one
// canonical watch-target representation across core, hub, screens, and Vela.
type WatchTarget = LiveEventWatchTarget

// LiveEventRef links a playing live channel programme back to its live event
// for the Vela playback snapshot and session SSE (A6).
type LiveEventRef struct {
	ID   string `json:"id"`
	Feed string `json:"feed"`
}
