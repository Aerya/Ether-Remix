package core

import (
	"net/url"
	"strings"
)

// PluginListRowID names a plugin list row (a plugin list source or a
// Stremio add-on catalogue placed directly on a Recommendations page) for
// CollectionLayoutSource.LayoutRow: the source kind ("plugin:<plugin>/<key>")
// and, when set, its parameters as a sorted query ("?genre=Action").
func PluginListRowID(kind string, params map[string]string) string {
	q := url.Values{}
	for k, v := range params {
		if v = strings.TrimSpace(v); k != "" && v != "" {
			q.Set(k, v)
		}
	}
	if len(q) == 0 {
		return kind
	}
	return kind + "?" + q.Encode()
}

// ParsePluginListRowID splits a PluginListRowID; ok is false for anything
// that isn't a plugin list row.
func ParsePluginListRowID(id string) (kind string, params map[string]string, ok bool) {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "plugin:") {
		return "", nil, false
	}
	kind, raw, _ := strings.Cut(id, "?")
	if !strings.Contains(kind, "/") {
		return "", nil, false
	}
	params = map[string]string{}
	if raw != "" {
		q, err := url.ParseQuery(raw)
		if err != nil {
			return "", nil, false
		}
		for k := range q {
			params[k] = q.Get(k)
		}
	}
	return kind, params, true
}
