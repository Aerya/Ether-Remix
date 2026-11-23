package plugins

import "strings"

// pluginText resolves a manifest label: plain text is returned as is, and a
// "$t:key" reference is looked up in the plugin's own strings (English
// first, then any locale that has it). An unknown key falls back to the key
// so a raw "$t:" never reaches the console.
func pluginText(strs map[string]map[string]string, val string) string {
	key, ok := strings.CutPrefix(strings.TrimSpace(val), "$t:")
	if !ok {
		return val
	}
	if v := strings.TrimSpace(strs["en"][key]); v != "" {
		return v
	}
	for _, doc := range strs {
		if v := strings.TrimSpace(doc[key]); v != "" {
			return v
		}
	}
	return key
}

// ResolveTextValue resolves a manifest label that may be plain text or a
// "$t:key" reference. Plain text is returned trimmed (or fallback when empty).
// A "$t:key" reference is looked up through resolve (when non-nil); if the key
// is missing, empty, or resolves to another "$t:" token, fallback is returned
// (falling back to key when fallback is empty) so an unresolved "$t:" key is
// never emitted anywhere.
func ResolveTextValue(val, fallback string, resolve func(string) (string, bool)) string {
	trimmed := strings.TrimSpace(val)
	key, isT := strings.CutPrefix(trimmed, "$t:")
	if !isT {
		if trimmed == "" {
			return fallback
		}
		return trimmed
	}
	if resolve != nil && key != "" {
		if resolved, ok := resolve(key); ok {
			resolved = strings.TrimSpace(resolved)
			if resolved != "" && !strings.HasPrefix(resolved, "$t:") {
				return resolved
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return key
}
