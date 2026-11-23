package favorites

import (
	"regexp"
	"strings"
)

var (
	entityPluginIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,119}$`)
	entityFeedRe     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	entityKindRe     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// EntityKey is a parsed KindEntity subject identifier with canonical wire form
// "pluginID:feed:entityKind:id" (spec A4).
type EntityKey struct {
	PluginID string
	Feed     string
	Kind     string
	ID       string
}

// String formats k in its canonical "pluginID:feed:entityKind:id" form.
func (k EntityKey) String() string {
	return FormatEntityKey(k.PluginID, k.Feed, k.Kind, k.ID)
}

// FormatEntityKey builds a canonical KindEntity subject identifier.
func FormatEntityKey(pluginID, feed, kind, id string) string {
	return pluginID + ":" + feed + ":" + kind + ":" + id
}

// ParseEntityKey validates and parses a canonical KindEntity key of the form
// "pluginID:feed:entityKind:id". The final id segment may contain colons for
// namespaced external identifiers, while rejecting control chars, URL schemes,
// empty segments, and excessive length.
func ParseEntityKey(raw string) (EntityKey, bool) {
	if raw == "" || len(raw) > 512 || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\x00\r\n\t") || strings.Contains(raw, "://") {
		return EntityKey{}, false
	}
	parts := strings.SplitN(raw, ":", 4)
	if len(parts) != 4 {
		return EntityKey{}, false
	}
	pluginID, feed, kind, id := parts[0], parts[1], parts[2], parts[3]
	if !entityPluginIDRe.MatchString(pluginID) || !entityFeedRe.MatchString(feed) || !entityKindRe.MatchString(kind) {
		return EntityKey{}, false
	}
	if id == "" || id != strings.TrimSpace(id) {
		return EntityKey{}, false
	}
	return EntityKey{PluginID: pluginID, Feed: feed, Kind: kind, ID: id}, true
}
