package pluginitems

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Store and item validation caps (spec §2.4, §3.2, §3.3, §3.5, §10).
const (
	DefaultMaxPluginItems = plugins.DefaultMaxPluginItems
	MaxTitleRunes         = 500
	MaxSummaryRunes       = 20_000
	MaxFieldRunes         = 4_000
	MaxPersonThumbBytes   = 2_048
	MaxArtRoles           = plugins.MaxKindArtRoles
	MaxHintsBytes         = plugins.MaxKindHintsBytes
	MaxFTSTextBytes       = 8 << 10
	DefaultBrowseLimit    = 200
	MaxBrowseLimit        = 1000
	// MaxBrowseFacetValues bounds the total facet values across the
	// any/all/none facet maps of one browse or search query, so the
	// generated indexed predicates stay bounded.
	MaxBrowseFacetValues = 256
	MaxBrowseBodyBytes   = 8 << 20
	MaxSyncConcurrency   = 4
	DeleteGuardRatio     = 0.5
)

var (
	// ErrMaxItemsExceeded is returned when an upsert would push a plugin
	// past its total item cap across kinds (spec §2.4).
	ErrMaxItemsExceeded = fmt.Errorf("%w: plugin item cap exceeded", plugins.ErrQuotaExceeded)
	// ErrInvalidItem is returned when an item fails envelope or field validation.
	ErrInvalidItem = errors.New("pluginitems: invalid item")
	// ErrDeleteGuard is returned when a sync walk or changes batch would
	// delete more than 50% of a library's items (spec §3.5).
	ErrDeleteGuard = errors.New("pluginitems: sync aborted by 50% delete guard")
)

// SyncDeleteGuard captures structured details when the 50% delete guard pauses
// a sync so the console can explain what happened in plain words and offer the
// owner an explicit keep-or-remove choice.
type SyncDeleteGuard struct {
	Source    string `json:"source"`              // "walk" or "changes"
	Missing   int    `json:"missing"`             // items absent from walk or slated for deletion
	Total     int    `json:"total"`               // eligible existing items compared against
	Protected int    `json:"protected,omitempty"` // opened/played/favourited/searched items excluded from pruning
}

// PlainMessage returns a plain-language summary of the paused delete guard.
func (g SyncDeleteGuard) PlainMessage() string {
	if g.Source == "changes" {
		return fmt.Sprintf("Sync paused: the plugin asked to remove %d of %d items. Ether kept your existing items until you choose whether to remove them.", g.Missing, g.Total)
	}
	return fmt.Sprintf("Sync paused: %d of %d items were not returned in the walk. Ether kept your existing items in case the source returned a partial or rotating list.", g.Missing, g.Total)
}

// DeleteGuardError wraps ErrDeleteGuard with exact counts so callers and the
// console API never have to parse an error string to build the owner choice.
type DeleteGuardError struct {
	PluginID  string
	KindKey   string
	Source    string
	Missing   int
	Total     int
	Protected int
}

func (e *DeleteGuardError) Unwrap() error { return ErrDeleteGuard }

func (e *DeleteGuardError) State() SyncDeleteGuard {
	return SyncDeleteGuard{
		Source:    e.Source,
		Missing:   e.Missing,
		Total:     e.Total,
		Protected: e.Protected,
	}
}

func (e *DeleteGuardError) PlainMessage() string {
	return e.State().PlainMessage()
}

func (e *DeleteGuardError) Error() string {
	if e.Source == "changes" {
		return fmt.Sprintf("%s: attempting to delete %d of %d items in %s/%s",
			ErrDeleteGuard.Error(), e.Missing, e.Total, e.PluginID, e.KindKey)
	}
	return fmt.Sprintf("%s: %d of %d items missing in walk for %s/%s",
		ErrDeleteGuard.Error(), e.Missing, e.Total, e.PluginID, e.KindKey)
}

var (
	legacyWalkGuardRE    = regexp.MustCompile(`50% delete guard:\s*(\d+)\s+of\s+(\d+)\s+items missing in walk`)
	legacyChangesGuardRE = regexp.MustCompile(`50% delete guard:\s*attempting to delete\s+(\d+)\s+of\s+(\d+)\s+items`)
)

// ParseDeleteGuardError extracts SyncDeleteGuard details from a persisted
// legacy v0.3.17 last_error string when guard_json has not yet been written.
func ParseDeleteGuardError(raw string) *SyncDeleteGuard {
	if m := legacyWalkGuardRE.FindStringSubmatch(raw); len(m) == 3 {
		missing, err1 := strconv.Atoi(m[1])
		total, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil && missing > 0 && total > 0 {
			return &SyncDeleteGuard{Source: "walk", Missing: missing, Total: total}
		}
	}
	if m := legacyChangesGuardRE.FindStringSubmatch(raw); len(m) == 3 {
		missing, err1 := strconv.Atoi(m[1])
		total, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil && missing > 0 && total > 0 {
			return &SyncDeleteGuard{Source: "changes", Missing: missing, Total: total}
		}
	}
	return nil
}

// IndexedFieldValue is one typed EAV row for plugin_item_fields (spec §2.3).
// HasThumb is in-memory only and records whether a people-field credit carried
// a validated portrait thumb URL, used to maintain plugin_item_person_tally.thumb_ref.
type IndexedFieldValue struct {
	Field    string
	Text     string
	Fold     string
	Int      int64
	Real     float64
	HasThumb bool
}

// ValidatedItem holds a cleaned KindItem plus its extracted indexed field rows
// and FTS search text.
type ValidatedItem struct {
	Item          core.KindItem
	IndexedFields []IndexedFieldValue
	SearchFields  string
	SearchPeople  string
	DroppedFields int
}

// ValidateItem validates one KindItem against its MediaKindMeta declaration
// (spec §3.3): level and optional parentLevel, field types and caps, string
// lengths, art roles and URLs, hints JSON object cap, and SortTitle derivation.
// Unknown field keys are dropped and counted in DroppedFields; wrong field
// types or violated caps reject the item with an error wrapping ErrInvalidItem.
func ValidateItem(decl plugins.MediaKindMeta, it core.KindItem, parentLevel string) (ValidatedItem, error) {
	if msg := plugins.CheckKindItem(decl, it); msg != "" {
		return ValidatedItem{}, fmt.Errorf("%w: item %q: %s", ErrInvalidItem, it.ID, msg)
	}
	if it.Unavailable != nil {
		if strings.TrimSpace(it.Unavailable.Code) == "" || len(it.Unavailable.Code) > 128 || len(it.Unavailable.Detail) > 2000 {
			return ValidatedItem{}, fmt.Errorf("%w: invalid unavailable reason", ErrInvalidItem)
		}
	}
	lv, at, ok := decl.Level(it.Level)
	if !ok {
		return ValidatedItem{}, fmt.Errorf("%w: item %q: undeclared level %q", ErrInvalidItem, it.ID, it.Level)
	}
	if at > 0 && parentLevel != "" {
		expectedParent := decl.LevelParent(it.Level)
		if parentLevel != expectedParent {
			return ValidatedItem{}, fmt.Errorf("%w: item %q: level %q requires parent level %q, got %q",
				ErrInvalidItem, it.ID, it.Level, expectedParent, parentLevel)
		}
	}
	if !lv.Playable {
		it.Playable = false
	}

	declaredArt := make(map[string]bool, len(decl.Art)+len(plugins.BuiltinKindArtRoles))
	for r := range plugins.BuiltinKindArtRoles {
		declaredArt[r] = true
	}
	for _, a := range decl.Art {
		declaredArt[a.Role] = true
	}
	for _, a := range it.Art {
		if !declaredArt[a.Role] {
			return ValidatedItem{}, fmt.Errorf("%w: item %q: undeclared art role %q", ErrInvalidItem, it.ID, a.Role)
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.URL)), "blob:") {
			return ValidatedItem{}, fmt.Errorf("%w: item %q: blob: art refs are only allowed from resolver/container paths", ErrInvalidItem, it.ID)
		}
	}

	it.SortTitle = core.EffectiveSortTitle(it.Title, it.SortTitle)
	if it.SortTitle == "" {
		it.SortTitle = strings.ToLower(strings.TrimSpace(it.Title))
	}

	if len(it.Hints) == 0 {
		it.Hints = json.RawMessage("{}")
	}

	declaredFields := make(map[string]plugins.KindField, len(decl.Fields))
	for _, f := range decl.Fields {
		declaredFields[f.Key] = f
	}
	searchFieldSet := make(map[string]bool, len(decl.Browse.SearchFields))
	for _, sf := range decl.Browse.SearchFields {
		searchFieldSet[sf] = true
	}

	var (
		cleanedFields map[string]json.RawMessage
		indexed       []IndexedFieldValue
		searchParts   []string
		peopleParts   []string
		dropped       int
	)
	if len(it.Fields) > 0 {
		cleanedFields = make(map[string]json.RawMessage, len(it.Fields))
		fieldKeys := make([]string, 0, len(it.Fields))
		for k := range it.Fields {
			fieldKeys = append(fieldKeys, k)
		}
		sort.Strings(fieldKeys)
		for _, k := range fieldKeys {
			raw := it.Fields[k]
			fdecl, exists := declaredFields[k]
			if !exists {
				dropped++
				continue
			}
			vals, texts, err := validateFieldValue(fdecl, raw)
			if err != nil {
				return ValidatedItem{}, fmt.Errorf("%w: item %q field %q: %v", ErrInvalidItem, it.ID, k, err)
			}
			cleanedFields[k] = raw
			indexed = append(indexed, vals...)
			if isPeopleIndexedField(fdecl) {
				if len(texts) > 0 {
					peopleParts = append(peopleParts, strings.Join(texts, " "))
				}
			}
			if fdecl.Type != "people" && searchFieldSet[k] && len(texts) > 0 {
				searchParts = append(searchParts, strings.Join(texts, " "))
			}
		}
	}
	it.Fields = cleanedFields

	searchText := strings.Join(searchParts, " ")
	if len(searchText) > MaxFTSTextBytes {
		searchText = clipUTF8(searchText, MaxFTSTextBytes)
	}
	peopleText := strings.Join(peopleParts, " ")
	if len(peopleText) > MaxFTSTextBytes {
		peopleText = clipUTF8(peopleText, MaxFTSTextBytes)
	}

	return ValidatedItem{
		Item:          it,
		IndexedFields: indexed,
		SearchFields:  searchText,
		SearchPeople:  peopleText,
		DroppedFields: dropped,
	}, nil
}

func validatePersonThumb(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", nil
	}
	if len(u) > MaxPersonThumbBytes {
		return "", fmt.Errorf("person thumb URL exceeds %d bytes", MaxPersonThumbBytes)
	}
	for _, r := range u {
		if r <= 0x20 || r == 0x7f {
			return "", errors.New("person thumb URL must not contain whitespace or control characters")
		}
	}
	low := strings.ToLower(u)
	if strings.HasPrefix(low, "blob:") {
		return "", errors.New("blob: person thumb URLs are not allowed")
	}
	if !strings.HasPrefix(low, "https://") && !strings.HasPrefix(low, "http://") {
		return "", errors.New("person thumb URL must use http:// or https://")
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return "", errors.New("person thumb URL is malformed")
	}
	return u, nil
}

func validateFieldValue(f plugins.KindField, raw json.RawMessage) ([]IndexedFieldValue, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, errors.New("field value cannot be null")
	}
	switch f.Type {
	case "string":
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, nil, errors.New("must be a string")
		}
		if utf8.RuneCountInString(s) > MaxFieldRunes {
			return nil, nil, fmt.Errorf("string exceeds %d runes", MaxFieldRunes)
		}
		var pid int64
		if isArtistStringField(f) {
			if clean := strings.TrimSpace(s); clean != "" {
				pid = core.PluginPersonID(clean)
			}
		}
		return []IndexedFieldValue{{Field: f.Key, Text: s, Fold: FoldFieldText(s), Int: pid}}, []string{s}, nil

	case "enum":
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, nil, errors.New("must be an enum string")
		}
		if utf8.RuneCountInString(s) > MaxFieldRunes {
			return nil, nil, fmt.Errorf("enum exceeds %d runes", MaxFieldRunes)
		}
		allowed := false
		for _, v := range f.Enum {
			if v == s {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, nil, fmt.Errorf("value %q is not in declared enum", s)
		}
		return []IndexedFieldValue{{Field: f.Key, Text: s, Fold: FoldFieldText(s)}}, []string{s}, nil

	case "int", "date", "duration":
		n, err := parseJSONInt64(trimmed)
		if err != nil {
			return nil, nil, fmt.Errorf("must be an integer (%s)", f.Type)
		}
		if f.Type == "duration" && n < 0 {
			return nil, nil, errors.New("duration must not be negative")
		}
		return []IndexedFieldValue{{Field: f.Key, Int: n}}, nil, nil

	case "float":
		var v float64
		if err := json.Unmarshal(trimmed, &v); err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, nil, errors.New("must be a number")
		}
		return []IndexedFieldValue{{Field: f.Key, Real: v}}, nil, nil

	case "bool":
		var b bool
		if err := json.Unmarshal(trimmed, &b); err != nil {
			return nil, nil, errors.New("must be a boolean")
		}
		var iv int64
		txt := "false"
		if b {
			iv = 1
			txt = "true"
		}
		return []IndexedFieldValue{{Field: f.Key, Text: txt, Fold: txt, Int: iv}}, nil, nil

	case "strings":
		var arr []string
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, nil, errors.New("must be an array of strings")
		}
		if len(arr) > 256 {
			return nil, nil, errors.New("strings array has more than 256 values")
		}
		seen := make(map[string]bool, len(arr))
		out := make([]IndexedFieldValue, 0, len(arr))
		texts := make([]string, 0, len(arr))
		artistString := isArtistStringField(f)
		for _, s := range arr {
			if utf8.RuneCountInString(s) > MaxFieldRunes {
				return nil, nil, fmt.Errorf("string element exceeds %d runes", MaxFieldRunes)
			}
			texts = append(texts, s)
			if !seen[s] {
				seen[s] = true
				var pid int64
				if artistString {
					if clean := strings.TrimSpace(s); clean != "" {
						pid = core.PluginPersonID(clean)
					}
				}
				out = append(out, IndexedFieldValue{Field: f.Key, Text: s, Fold: FoldFieldText(s), Int: pid})
			}
		}
		return out, texts, nil

	case "people":
		var strArr []string
		if err := json.Unmarshal(trimmed, &strArr); err == nil {
			if len(strArr) > 256 {
				return nil, nil, errors.New("people array has more than 256 entries")
			}
			seen := make(map[int64]bool, len(strArr))
			out := make([]IndexedFieldValue, 0, len(strArr))
			texts := make([]string, 0, len(strArr))
			for _, name := range strArr {
				cleanName := strings.TrimSpace(name)
				if utf8.RuneCountInString(name) > MaxFieldRunes || cleanName == "" {
					return nil, nil, errors.New("person name must be non-empty and within 4000 runes")
				}
				texts = append(texts, cleanName)
				pid := core.PluginPersonID(cleanName)
				if pid > 0 && !seen[pid] {
					seen[pid] = true
					out = append(out, IndexedFieldValue{Field: f.Key, Text: cleanName, Fold: FoldFieldText(cleanName), Int: pid})
				}
			}
			return out, texts, nil
		}
		var objArr []struct {
			Name  string `json:"name"`
			Role  string `json:"role,omitempty"`
			Thumb string `json:"thumb,omitempty"`
		}
		if err := json.Unmarshal(trimmed, &objArr); err != nil {
			return nil, nil, errors.New("must be an array of person names or {name, role, thumb} objects")
		}
		if len(objArr) > 256 {
			return nil, nil, errors.New("people array has more than 256 entries")
		}
		seenIdx := make(map[int64]int, len(objArr))
		out := make([]IndexedFieldValue, 0, len(objArr))
		texts := make([]string, 0, len(objArr))
		for _, p := range objArr {
			cleanName := strings.TrimSpace(p.Name)
			if cleanName == "" || utf8.RuneCountInString(p.Name) > MaxFieldRunes || utf8.RuneCountInString(p.Role) > MaxFieldRunes {
				return nil, nil, errors.New("person name must be non-empty and within 4000 runes")
			}
			thumb, err := validatePersonThumb(p.Thumb)
			if err != nil {
				return nil, nil, err
			}
			texts = append(texts, cleanName)
			pid := core.PluginPersonID(cleanName)
			if pid > 0 {
				if idx, ok := seenIdx[pid]; !ok {
					seenIdx[pid] = len(out)
					out = append(out, IndexedFieldValue{
						Field:    f.Key,
						Text:     cleanName,
						Fold:     FoldFieldText(cleanName),
						Int:      pid,
						HasThumb: thumb != "",
					})
				} else if thumb != "" {
					out[idx].HasThumb = true
				}
			}
		}
		return out, texts, nil

	default:
		return nil, nil, fmt.Errorf("unsupported field type %q", f.Type)
	}
}

func parseJSONInt64(raw []byte) (int64, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var num json.Number
	if err := dec.Decode(&num); err != nil {
		return 0, err
	}
	if dec.More() {
		return 0, errors.New("trailing data")
	}
	if n, err := num.Int64(); err == nil {
		return n, nil
	}
	f, err := num.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return 0, errors.New("not an integer")
	}
	if f < float64(math.MinInt64) || f >= float64(math.MaxInt64) {
		return 0, errors.New("integer overflow")
	}
	return int64(f), nil
}

func clipUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
