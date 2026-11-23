package libraryfilter

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// InvalidFilterError marks rule validation or compilation failures so HTTP
// handlers can report 400 instead of 500 regardless of library kind.
type InvalidFilterError struct {
	Err error
}

func (e *InvalidFilterError) Error() string { return e.Err.Error() }
func (e *InvalidFilterError) Unwrap() error { return e.Err }

// MarkInvalidFilter wraps err as an InvalidFilterError when non-nil.
func MarkInvalidFilter(err error) error {
	if err == nil {
		return nil
	}
	var target *InvalidFilterError
	if errors.As(err, &target) {
		return err
	}
	return &InvalidFilterError{Err: err}
}

// IsInvalidFilter reports whether err came from an invalid visibility rule or policy.
func IsInvalidFilter(err error) bool {
	var target *InvalidFilterError
	return errors.As(err, &target)
}

// PreviewableRules omits unfinished conditions and rule groups (such as a newly
// added rule whose values have not been chosen yet) while preserving malformed
// rules so the compiler still rejects invalid input. indices maps each returned
// rule back to its position in specs.
func PreviewableRules(specs []core.LibraryExclusion) (kept []core.LibraryExclusion, indices []int) {
	if len(specs) > 34 {
		return specs, seqIndices(len(specs))
	}
	normalized := core.NormalizeLibraryRules(specs)
	kept = make([]core.LibraryExclusion, 0, len(normalized))
	indices = make([]int, 0, len(normalized))
	for i, s := range normalized {
		if !ruleEnvelopeValid(s) {
			kept = append(kept, s)
			indices = append(indices, i)
			continue
		}
		if len(s.Rules) == 0 {
			continue
		}
		conds := make([]core.CollectionRule, 0, len(s.Rules))
		for _, r := range s.Rules {
			if pr, ok := previewableCondition(r, 0); ok {
				conds = append(conds, pr)
			}
		}
		if len(conds) == 0 {
			continue
		}
		s.Rules = conds
		kept = append(kept, s)
		indices = append(indices, i)
	}
	return kept, indices
}

func seqIndices(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func ruleEnvelopeValid(s core.LibraryExclusion) bool {
	if len(strings.TrimSpace(s.Name)) > 120 {
		return false
	}
	if s.Action != "" && s.Action != "show" && s.Action != "hide" {
		return false
	}
	if s.MediaType != "movie" && s.MediaType != "show" && s.MediaType != "mixed" {
		return false
	}
	if s.Match != "all" && s.Match != "any" {
		return false
	}
	switch s.Scope {
	case "", core.VisibilityEverywhere:
	case core.VisibilityDiscovery, core.VisibilityLibrary:
		if s.Action != "" && s.Action != "hide" {
			return false
		}
	default:
		return false
	}
	return len(s.Rules) <= 64
}

func knownPreviewField(field string) bool {
	if strings.HasPrefix(field, "browse:") {
		return len(strings.TrimPrefix(field, "browse:")) > 0
	}
	switch field {
	case "group", "viewer", "title", "contentRating",
		"country", "language", "genre", "studio", "network", "keyword",
		"year", "rating", "votes", "popularity", "runtime":
		return true
	default:
		return false
	}
}

func previewableCondition(r core.CollectionRule, depth int) (core.CollectionRule, bool) {
	if depth > 8 {
		return r, true
	}
	if strings.TrimSpace(r.Field) == "" {
		return core.CollectionRule{}, false
	}
	if !knownPreviewField(r.Field) {
		return r, true
	}
	if r.Field == "group" {
		if r.Op != "is" && r.Op != "in" && r.Op != "isNot" {
			return r, true
		}
		var children []core.CollectionRule
		if err := json.Unmarshal(r.Value, &children); err != nil || len(children) > 64 {
			return r, true
		}
		kept := make([]core.CollectionRule, 0, len(children))
		for _, child := range children {
			if pr, ok := previewableCondition(child, depth+1); ok {
				kept = append(kept, pr)
			}
		}
		if len(kept) == 0 {
			return core.CollectionRule{}, false
		}
		raw, err := json.Marshal(kept)
		if err != nil {
			return r, true
		}
		r.Value = raw
		return r, true
	}
	if r.Op == "unknown" || r.Op == "known" {
		return r, true
	}
	raw := bytes.TrimSpace(r.Value)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return core.CollectionRule{}, false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return core.CollectionRule{}, false
		}
		return r, true
	}
	var arr []any
	if json.Unmarshal(raw, &arr) == nil {
		if len(arr) == 0 {
			return core.CollectionRule{}, false
		}
		if r.Op == "between" {
			if len(arr) < 2 || isEmptyScalar(arr[0]) || isEmptyScalar(arr[1]) {
				return core.CollectionRule{}, false
			}
			return r, true
		}
		allEmpty := true
		for _, item := range arr {
			if !isEmptyScalar(item) {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			return core.CollectionRule{}, false
		}
		return r, true
	}
	if r.Field == "viewer" {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && len(m) == 0 {
			return core.CollectionRule{}, false
		}
	}
	return r, true
}

func isEmptyScalar(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}
