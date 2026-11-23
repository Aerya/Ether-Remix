package liveevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// NormalizeEvent returns an immutable copy of ev with its ID namespaced to
// pluginID:feedID:eventID, Feed populated, and viewer-only fields cleared.
func NormalizeEvent(pluginID, feedID string, ev core.LiveEvent) core.LiveEvent {
	out := ev.Clone()
	out.Feed = feedID
	out.Spoiler = false
	out.Watch = nil
	prefix := pluginID + ":" + feedID + ":"
	if out.ID != "" && !strings.HasPrefix(out.ID, prefix) {
		out.ID = core.NamespaceLiveEventID(pluginID, feedID, out.ID)
	}
	return out
}

// NormalizeDetail returns an immutable copy of d with its embedded LiveEvent
// normalized to pluginID:feedID.
func NormalizeDetail(pluginID, feedID string, d core.LiveEventDetail) core.LiveEventDetail {
	out := d.Clone()
	out.LiveEvent = NormalizeEvent(pluginID, feedID, out.LiveEvent)
	return out
}

// ContentHash returns a deterministic SHA-256 hex digest of an event's semantic
// fields (excluding UpdatedAt, Spoiler, and viewer-scoped Watch targets).
func ContentHash(ev core.LiveEvent) string {
	cp := ev
	cp.UpdatedAt = ""
	cp.Spoiler = false
	cp.Watch = nil
	raw, _ := json.Marshal(cp)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// VisibleHash returns a deterministic SHA-256 hex digest of an event as delivered
// to a specific viewer (including Spoiler and Watch, excluding UpdatedAt).
// Used per subscriber so spoiler-free viewers do not receive noise frames when
// hidden fields change without affecting what they can see.
func VisibleHash(ev core.LiveEvent) string {
	cp := ev
	cp.UpdatedAt = ""
	raw, _ := json.Marshal(cp)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// IsStaleUpdate reports whether nextUpdatedAt is strictly older than
// prevUpdatedAt when both are valid RFC3339 timestamps.
func IsStaleUpdate(prevUpdatedAt, nextUpdatedAt string) bool {
	if prevUpdatedAt == "" || nextUpdatedAt == "" || prevUpdatedAt == nextUpdatedAt {
		return false
	}
	prev, err1 := time.Parse(time.RFC3339Nano, prevUpdatedAt)
	next, err2 := time.Parse(time.RFC3339Nano, nextUpdatedAt)
	if err1 != nil || err2 != nil {
		return false
	}
	return next.Before(prev)
}
