package frontend

import "context"

// LiveAnchor identifies the first segment in one published media playlist.
// WindowStartSeconds is relative to the first published segment, never derived
// from sequence numbers, target duration, wall clock, or provider timestamps.
type LiveAnchor struct {
	MediaSequence      int64   `json:"mediaSequence"`
	WindowStartSeconds float64 `json:"windowStartSeconds"`
}

// LiveWindow describes an existing output; reading it must not start work.
// A subsequent playlist may have slid: its X-Ether-Live-Media-Sequence and
// X-Ether-Live-Window-Start response headers supersede this snapshot's anchor.
type LiveWindow struct {
	Anchor          LiveAnchor
	LiveEdgeSeconds float64
}

// LiveWindowReader is an internal session-owner capability. Callers must already
// have authorized the playback session (as with TranscodeFor).
type LiveWindowReader interface {
	LiveWindow(context.Context, string) (LiveWindow, error)
}
