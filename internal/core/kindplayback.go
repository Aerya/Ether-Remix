package core

import "context"

// KindInput is a non-catalogue playback input: a plugin kind leaf whose bytes
// arrive as a random-access MediaSource rather than through the release
// pipeline. It exists so plugin video/audio items run through the same
// transcode, decision, HLS/DASH and stream-limit machinery as catalogue
// titles (spec §15.1).
type KindInput struct {
	// Title is the projected kind item. Its Key must be a plugin key
	// (RatingKey.IsPluginKey); its Type selects the profile media scope.
	Title *Title
	// Open returns the item's byte stream. It is called once per open, inside
	// the prepare flight, so stream-limit accounting and cancellation apply.
	Open func(ctx context.Context) (MediaSource, error)
	// OpenCandidate, when set, opens the next non-excluded candidate from the
	// item's kind resolution and returns its concrete Release metadata so
	// mid-stream failover can switch to alternative plugin candidates.
	OpenCandidate func(ctx context.Context, exclude []string) (MediaSource, Release, error)
	// SourceID is a stable identity for the underlying source, used as the
	// synthetic release id when OpenCandidate is not provided.
	SourceID string
	// PreferSourceID, when non-empty, requests opening the version whose
	// candidate source id matches, instead of the default winner. The id is
	// the stable release id of one of the item's /resolve sources.
	PreferSourceID string
	// Exact, when true alongside a non-empty PreferSourceID, forbids falling
	// back to another candidate if PreferSourceID is unavailable or fails
	// mid-stream.
	Exact bool
	// DeferSupersession keeps the prepared session off Activity and leaves
	// other live sessions untouched until MarkSurfaceOpened is called on
	// admission (used by broker Plan and SwitchVersion).
	DeferSupersession bool
}

// KindPlayback opens a non-catalogue (plugin kind) item through the session
// and decision pipeline. It mirrors Playback.Prepare but takes a KindInput
// instead of resolving a catalogue title.
type KindPlayback interface {
	PrepareKind(ctx context.Context, user *User, in KindInput, caps ClientCaps) (*PlaySession, error)
	// SessionForKind finds the live kind session for a viewer, client and item.
	SessionForKind(userID int64, clientID string, key RatingKey) (*PlaySession, bool)
}
