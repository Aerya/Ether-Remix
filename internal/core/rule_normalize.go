package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NormalizeRuleMediaType accepts historical labels while preserving unknown
// values so validation can report them instead of silently changing meaning.
func NormalizeRuleMediaType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "movie", "movies", "film", "films":
		return "movie"
	case "show", "shows", "tv", "tv shows", "series":
		return "show"
	case "mixed":
		return "mixed"
	}
	return s
}

// NormalizeRuleDocument repairs media-type fields and canonicalises valid
// "title" conditions without touching names, search text, or malformed values
// (which remain intact so validation rejects them).
func NormalizeRuleDocument(raw []byte) ([]byte, bool) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !json.Valid(raw) || dec.Decode(&doc) != nil {
		return raw, false
	}
	changed := false
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case []any:
			for _, child := range v {
				walk(child)
			}
		case map[string]any:
			if v["field"] == "title" && normalizeTitleRuleMap(v) {
				changed = true
			}
			for k, child := range v {
				if s, ok := child.(string); ok && (k == "mediaType" || (k == "value" && v["field"] == "browse:type")) {
					if canonical := NormalizeRuleMediaType(s); canonical != s {
						v[k] = canonical
						changed = true
					}
				}
				walk(child)
			}
		}
	}
	walk(doc)
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return raw, false
	}
	return out, true
}

func normalizeTitleRuleMap(v map[string]any) bool {
	changed := false
	if op, ok := v["op"].(string); ok {
		switch op {
		case "is":
			v["op"] = "in"
			changed = true
		case "isNot":
			v["op"] = "notIn"
			changed = true
		}
	}
	rawVal, hasVal := v["value"]
	if !hasVal || rawVal == nil {
		return changed
	}
	var items []any
	switch x := rawVal.(type) {
	case []any:
		items = x
	case string, json.Number, map[string]any:
		items = []any{x}
	default:
		return changed
	}
	if len(items) == 0 || len(items) > MaxTitleRuleKeys {
		return changed
	}
	var existingLabels []string
	if rawLabels, ok := v["labels"].([]any); ok {
		existingLabels = make([]string, len(rawLabels))
		for i, entry := range rawLabels {
			if s, ok := entry.(string); ok {
				existingLabels[i] = strings.TrimSpace(s)
			}
		}
	}
	var canonKeys []any
	var canonLabels []string
	seen := make(map[RatingKey]int, len(items))
	for i, item := range items {
		keyStr, objLabel, ok := extractTitleRuleEntry(item)
		if !ok {
			return changed
		}
		rk, err := ParseRatingKey(keyStr)
		if err != nil || !rk.IsTitleKey() {
			return changed
		}
		lbl := ""
		if i < len(existingLabels) && existingLabels[i] != "" {
			lbl = existingLabels[i]
		} else if objLabel != "" {
			lbl = objLabel
		}
		if prev, dup := seen[rk]; dup {
			if canonLabels[prev] == "" && lbl != "" {
				canonLabels[prev] = lbl
			}
			continue
		}
		seen[rk] = len(canonKeys)
		canonKeys = append(canonKeys, rk.String())
		canonLabels = append(canonLabels, lbl)
	}
	if !sameStringAnySlice(rawVal, canonKeys) {
		v["value"] = canonKeys
		changed = true
	}
	for len(canonLabels) > 0 && canonLabels[len(canonLabels)-1] == "" {
		canonLabels = canonLabels[:len(canonLabels)-1]
	}
	if len(canonLabels) == 0 {
		if _, exists := v["labels"]; exists {
			delete(v, "labels")
			changed = true
		}
	} else if !sameStringSliceAny(v["labels"], canonLabels) {
		outLabels := make([]any, len(canonLabels))
		for i, s := range canonLabels {
			outLabels[i] = s
		}
		v["labels"] = outLabels
		changed = true
	}
	return changed
}

func extractTitleRuleEntry(item any) (keyStr, label string, ok bool) {
	switch x := item.(type) {
	case string:
		return strings.TrimSpace(x), "", true
	case json.Number:
		return strings.TrimSpace(x.String()), "", true
	case map[string]any:
		rawKey, hasKey := x["key"]
		if !hasKey {
			return "", "", false
		}
		switch k := rawKey.(type) {
		case string:
			keyStr = strings.TrimSpace(k)
		case json.Number:
			keyStr = strings.TrimSpace(k.String())
		default:
			return "", "", false
		}
		for _, field := range []string{"title", "label", "name"} {
			if s, ok := x[field].(string); ok && strings.TrimSpace(s) != "" {
				label = strings.TrimSpace(s)
				break
			}
		}
		return keyStr, label, true
	default:
		return "", "", false
	}
}

func sameStringAnySlice(raw any, want []any) bool {
	got, ok := raw.([]any)
	if !ok || len(got) != len(want) {
		return false
	}
	for i := range got {
		s, ok := got[i].(string)
		if !ok || s != want[i].(string) {
			return false
		}
	}
	return true
}

func sameStringSliceAny(raw any, want []string) bool {
	got, ok := raw.([]any)
	if !ok || len(got) != len(want) {
		return false
	}
	for i := range got {
		s, ok := got[i].(string)
		if !ok || s != want[i] {
			return false
		}
	}
	return true
}

// NormalizeLibraryRules returns canonical rules, retaining malformed values
// unchanged so the compiler can reject them.
func NormalizeLibraryRules(rules []LibraryExclusion) []LibraryExclusion {
	raw, err := json.Marshal(rules)
	if err != nil {
		return rules
	}
	raw, _ = NormalizeRuleDocument(raw)
	var out []LibraryExclusion
	_ = json.Unmarshal(raw, &out)
	return out
}
