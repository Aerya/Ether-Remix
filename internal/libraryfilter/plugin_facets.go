package libraryfilter

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func envelopeFacet(key string) bool { return key == "year" || key == "rating" || key == "released" }

// SplitDateDecadeFacet splits a "<dateField>-decade" synthetic facet key into
// its underlying declared date field key.
func SplitDateDecadeFacet(key string) (string, bool) {
	base, ok := strings.CutSuffix(key, "-decade")
	if !ok || strings.TrimSpace(base) == "" {
		return "", false
	}
	return base, true
}

// ParseDecadeBucket resolves a decade bucket ("1990" or "1990s") into inclusive
// [startYear, endYear] bounds (e.g. [1990, 1999]).
func ParseDecadeBucket(id string) (int, int, bool) {
	s := strings.TrimSpace(id)
	s = strings.TrimSuffix(s, "s")
	s = strings.TrimSuffix(s, "S")
	if len(s) != 4 {
		return 0, 0, false
	}
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, 0, false
		}
	}
	y, err := strconv.Atoi(s)
	if err != nil || y < 1000 || y > 9990 {
		return 0, 0, false
	}
	d := (y / 10) * 10
	return d, d + 9, true
}

// ParseDateDecadeBucket resolves a decade bucket ("1990" or "1990s") into
// inclusive UTC Unix second bounds covering all 10 years of the decade.
func ParseDateDecadeBucket(id string) (int64, int64, bool) {
	loY, hiY, ok := ParseDecadeBucket(id)
	if !ok {
		return 0, 0, false
	}
	start := time.Date(loY, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(hiY+1, 1, 1, 0, 0, 0, 0, time.UTC).Unix() - 1
	return start, end, true
}

// DurationBucketDef describes a discrete duration facet bucket (seconds).
type DurationBucketDef struct {
	ID    string
	Label string
	Min   int64
	Max   int64 // inclusive; math.MaxInt64 means unbounded upper end
}

// DurationBucketDefs are the canonical discrete duration facet buckets.
var DurationBucketDefs = []DurationBucketDef{
	{ID: "under-10m", Label: "Under 10 min", Min: 0, Max: 599},
	{ID: "10-30m", Label: "10-30 min", Min: 600, Max: 1799},
	{ID: "30-60m", Label: "30-60 min", Min: 1800, Max: 3599},
	{ID: "1-2h", Label: "1-2 h", Min: 3600, Max: 7199},
	{ID: "over-2h", Label: "Over 2 h", Min: 7200, Max: math.MaxInt64},
}

// ParseDurationBucket resolves a duration bucket ID ("under-10m", "10-30m", ...)
// to its inclusive [min, max] second bounds.
func ParseDurationBucket(id string) (int64, int64, bool) {
	for _, b := range DurationBucketDefs {
		if b.ID == id {
			return b.Min, b.Max, true
		}
	}
	return 0, 0, false
}

// ParseDateYearBucket resolves a 4-digit UTC year bucket ("2024") into inclusive
// Unix second bounds [Jan 1 00:00:00 UTC, Dec 31 23:59:59 UTC].
func ParseDateYearBucket(id string) (int64, int64, bool) {
	if len(id) != 4 {
		return 0, 0, false
	}
	for i := 0; i < 4; i++ {
		if id[i] < '0' || id[i] > '9' {
			return 0, 0, false
		}
	}
	y, err := strconv.Atoi(id)
	if err != nil || y < 1000 || y > 9999 {
		return 0, 0, false
	}
	start := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC).Unix() - 1
	return start, end, true
}

// IntRange describes a one- or two-sided integer range with inclusive or
// exclusive bounds.
type IntRange struct {
	HasLo bool
	Lo    int64
	LoInc bool // true for >=, false for >
	HasHi bool
	Hi    int64
	HiInc bool // true for <=, false for <
}

// ExcludesZero reports whether 0 falls strictly outside the range, allowing
// callers to use the stricter `f.v_int <> 0` partial-index predicate.
func (r IntRange) ExcludesZero() bool {
	if r.HasLo && (r.Lo > 0 || (r.Lo == 0 && !r.LoInc)) {
		return true
	}
	if r.HasHi && (r.Hi < 0 || (r.Hi == 0 && !r.HiInc)) {
		return true
	}
	return false
}

// SQL renders the range predicate on col and returns its bound arguments.
func (r IntRange) SQL(col string) (string, []any) {
	switch {
	case r.HasLo && r.HasHi:
		if r.LoInc && r.HiInc {
			return col + ` BETWEEN ? AND ?`, []any{r.Lo, r.Hi}
		}
		loOp := `>`
		if r.LoInc {
			loOp = `>=`
		}
		hiOp := `<`
		if r.HiInc {
			hiOp = `<=`
		}
		return `(` + col + ` ` + loOp + ` ? AND ` + col + ` ` + hiOp + ` ?)`, []any{r.Lo, r.Hi}
	case r.HasLo:
		if r.LoInc {
			return col + ` >= ?`, []any{r.Lo}
		}
		return col + ` > ?`, []any{r.Lo}
	default:
		if r.HiInc {
			return col + ` <= ?`, []any{r.Hi}
		}
		return col + ` < ?`, []any{r.Hi}
	}
}

// FloatRange describes a one- or two-sided floating-point range with inclusive
// or exclusive bounds.
type FloatRange struct {
	HasLo bool
	Lo    float64
	LoInc bool // true for >=, false for >
	HasHi bool
	Hi    float64
	HiInc bool // true for <=, false for <
}

// SQL renders the range predicate on col and returns its bound arguments.
func (r FloatRange) SQL(col string) (string, []any) {
	switch {
	case r.HasLo && r.HasHi:
		if r.LoInc && r.HiInc {
			return col + ` BETWEEN ? AND ?`, []any{r.Lo, r.Hi}
		}
		loOp := `>`
		if r.LoInc {
			loOp = `>=`
		}
		hiOp := `<`
		if r.HiInc {
			hiOp = `<=`
		}
		return `(` + col + ` ` + loOp + ` ? AND ` + col + ` ` + hiOp + ` ?)`, []any{r.Lo, r.Hi}
	case r.HasLo:
		if r.LoInc {
			return col + ` >= ?`, []any{r.Lo}
		}
		return col + ` > ?`, []any{r.Lo}
	default:
		if r.HiInc {
			return col + ` <= ?`, []any{r.Hi}
		}
		return col + ` < ?`, []any{r.Hi}
	}
}

func stripLoPrefix(s string) (num string, inc bool) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, ">="); ok {
		return strings.TrimSpace(rest), true
	}
	if rest, ok := strings.CutPrefix(s, ">"); ok {
		return strings.TrimSpace(rest), false
	}
	return s, true
}

func stripHiPrefix(s string) (num string, inc bool) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "<="); ok {
		return strings.TrimSpace(rest), true
	}
	if rest, ok := strings.CutPrefix(s, "<"); ok {
		return strings.TrimSpace(rest), false
	}
	return s, true
}

// ParseIntRange parses closed (`lo..hi`), open (`lo..`, `..hi`), and exclusive
// (`<hi`, `>lo`, `..<hi`, `>lo..`, `<=hi`, `>=lo`) integer range expressions.
func ParseIntRange(v string) (IntRange, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return IntRange{}, false
	}
	if strings.Contains(v, "..") {
		parts := strings.SplitN(v, "..", 2)
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])
		if left == "" && right == "" {
			return IntRange{}, false
		}
		var r IntRange
		if left != "" {
			num, inc := stripLoPrefix(left)
			lo, err := strconv.ParseInt(num, 10, 64)
			if err != nil {
				return IntRange{}, false
			}
			r.HasLo, r.Lo, r.LoInc = true, lo, inc
		}
		if right != "" {
			num, inc := stripHiPrefix(right)
			hi, err := strconv.ParseInt(num, 10, 64)
			if err != nil {
				return IntRange{}, false
			}
			r.HasHi, r.Hi, r.HiInc = true, hi, inc
		}
		if r.HasLo && r.HasHi {
			if r.Lo > r.Hi || (r.Lo == r.Hi && (!r.LoInc || !r.HiInc)) {
				return IntRange{}, false
			}
		}
		return r, true
	}
	switch {
	case strings.HasPrefix(v, "<="):
		hi, err := strconv.ParseInt(strings.TrimSpace(v[2:]), 10, 64)
		if err != nil {
			return IntRange{}, false
		}
		return IntRange{HasHi: true, Hi: hi, HiInc: true}, true
	case strings.HasPrefix(v, ">="):
		lo, err := strconv.ParseInt(strings.TrimSpace(v[2:]), 10, 64)
		if err != nil {
			return IntRange{}, false
		}
		return IntRange{HasLo: true, Lo: lo, LoInc: true}, true
	case strings.HasPrefix(v, "<"):
		hi, err := strconv.ParseInt(strings.TrimSpace(v[1:]), 10, 64)
		if err != nil {
			return IntRange{}, false
		}
		return IntRange{HasHi: true, Hi: hi, HiInc: false}, true
	case strings.HasPrefix(v, ">"):
		lo, err := strconv.ParseInt(strings.TrimSpace(v[1:]), 10, 64)
		if err != nil {
			return IntRange{}, false
		}
		return IntRange{HasLo: true, Lo: lo, LoInc: false}, true
	default:
		return IntRange{}, false
	}
}

// ParseFloatRange parses closed (`lo..hi`), open (`lo..`, `..hi`), and
// exclusive (`<hi`, `>lo`, `..<hi`, `>lo..`, `<=hi`, `>=lo`) floating-point
// range expressions.
func ParseFloatRange(v string) (FloatRange, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return FloatRange{}, false
	}
	parseNum := func(s string) (float64, bool) {
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	}
	if strings.Contains(v, "..") {
		parts := strings.SplitN(v, "..", 2)
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])
		if left == "" && right == "" {
			return FloatRange{}, false
		}
		var r FloatRange
		if left != "" {
			num, inc := stripLoPrefix(left)
			lo, ok := parseNum(num)
			if !ok {
				return FloatRange{}, false
			}
			r.HasLo, r.Lo, r.LoInc = true, lo, inc
		}
		if right != "" {
			num, inc := stripHiPrefix(right)
			hi, ok := parseNum(num)
			if !ok {
				return FloatRange{}, false
			}
			r.HasHi, r.Hi, r.HiInc = true, hi, inc
		}
		if r.HasLo && r.HasHi {
			if r.Lo > r.Hi || (r.Lo == r.Hi && (!r.LoInc || !r.HiInc)) {
				return FloatRange{}, false
			}
		}
		return r, true
	}
	switch {
	case strings.HasPrefix(v, "<="):
		hi, ok := parseNum(v[2:])
		if !ok {
			return FloatRange{}, false
		}
		return FloatRange{HasHi: true, Hi: hi, HiInc: true}, true
	case strings.HasPrefix(v, ">="):
		lo, ok := parseNum(v[2:])
		if !ok {
			return FloatRange{}, false
		}
		return FloatRange{HasLo: true, Lo: lo, LoInc: true}, true
	case strings.HasPrefix(v, "<"):
		hi, ok := parseNum(v[1:])
		if !ok {
			return FloatRange{}, false
		}
		return FloatRange{HasHi: true, Hi: hi, HiInc: false}, true
	case strings.HasPrefix(v, ">"):
		lo, ok := parseNum(v[1:])
		if !ok {
			return FloatRange{}, false
		}
		return FloatRange{HasLo: true, Lo: lo, LoInc: false}, true
	default:
		return FloatRange{}, false
	}
}

// AppendPluginFacetClauses appends ` AND EXISTS (...)` (or envelope ` AND (...)`)
// predicates on plugin_item_fields / plugin_items for each facet key to where.
// Multiple values within a key combine with OR; multiple keys combine with AND.
func AppendPluginFacetClauses(where *strings.Builder, args *[]any, facets map[string][]string, types map[string]string) {
	if len(facets) == 0 {
		return
	}
	keys := make([]string, 0, len(facets))
	for k := range facets {
		if strings.TrimSpace(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		rawVals := facets[key]
		var cleanVals []string
		for _, v := range rawVals {
			v = strings.TrimSpace(v)
			if v != "" {
				cleanVals = append(cleanVals, v)
			}
		}
		if len(cleanVals) == 0 {
			continue
		}

		if key == "decade" && types["decade"] == "" {
			var disjuncts []string
			var dargs []any
			for _, value := range cleanVals {
				if lo, hi, ok := ParseDecadeBucket(value); ok {
					disjuncts = append(disjuncts, `i.year BETWEEN ? AND ?`)
					dargs = append(dargs, lo, hi)
				}
			}
			if len(disjuncts) == 0 {
				where.WriteString(` AND 0=1`)
				continue
			}
			if len(disjuncts) == 1 {
				where.WriteString(` AND ` + disjuncts[0])
			} else {
				where.WriteString(` AND (` + strings.Join(disjuncts, ` OR `) + `)`)
			}
			*args = append(*args, dargs...)
			continue
		}

		if base, ok := SplitDateDecadeFacet(key); ok && types[base] == "date" {
			var disjuncts []string
			var dargs []any
			for _, value := range cleanVals {
				if lo, hi, ok := ParseDateDecadeBucket(value); ok {
					disjuncts = append(disjuncts, `f.v_int BETWEEN ? AND ?`)
					dargs = append(dargs, lo, hi)
				}
			}
			if len(disjuncts) == 0 {
				where.WriteString(` AND 0=1`)
				continue
			}
			*args = append(*args, base)
			*args = append(*args, dargs...)
			pred := disjuncts[0]
			if len(disjuncts) > 1 {
				pred = `(` + strings.Join(disjuncts, ` OR `) + `)`
			}
			where.WriteString(` AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text = '' AND ` + pred + `)`)
			continue
		}

		if envelopeFacet(key) {
			var disjuncts []string
			var dargs []any
			var exactPh []string
			var exactArgs []any
			col := `i.` + key
			for _, value := range cleanVals {
				if key == "released" {
					if lo, hi, ok := ParseDateYearBucket(value); ok {
						disjuncts = append(disjuncts, `i.released BETWEEN ? AND ?`)
						dargs = append(dargs, lo, hi)
						continue
					}
				}
				if key == "year" || key == "released" {
					if r, ok := ParseIntRange(value); ok {
						q, rargs := r.SQL(col)
						if key == "year" && (!r.HasLo || r.Lo < 0 || (r.Lo == 0 && r.LoInc)) {
							q = `(i.year > 0 AND ` + q + `)`
						} else if key == "released" && !r.ExcludesZero() {
							q = `(i.released <> 0 AND ` + q + `)`
						}
						disjuncts = append(disjuncts, q)
						dargs = append(dargs, rargs...)
						continue
					}
					if n, err := strconv.ParseInt(value, 10, 64); err == nil {
						exactPh = append(exactPh, "?")
						exactArgs = append(exactArgs, n)
						continue
					}
				}
				if r, ok := ParseFloatRange(value); ok {
					q, rargs := r.SQL(col)
					if key == "rating" && (!r.HasLo || r.Lo < 0 || (r.Lo == 0 && r.LoInc)) {
						q = `(i.rating > 0 AND ` + q + `)`
					} else if key == "year" && (!r.HasLo || r.Lo < 0 || (r.Lo == 0 && r.LoInc)) {
						q = `(i.year > 0 AND ` + q + `)`
					} else if key == "released" && (!r.HasLo || r.Lo <= 0) && (!r.HasHi || r.Hi >= 0) {
						q = `(i.released <> 0 AND ` + q + `)`
					}
					disjuncts = append(disjuncts, q)
					dargs = append(dargs, rargs...)
					continue
				}
				number, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
					continue
				}
				exactPh = append(exactPh, "?")
				exactArgs = append(exactArgs, number)
			}
			if len(exactPh) > 0 {
				disjuncts = append(disjuncts, col+` IN (`+strings.Join(exactPh, ",")+`)`)
				dargs = append(dargs, exactArgs...)
			}
			if len(disjuncts) == 0 {
				where.WriteString(` AND 0=1`)
				continue
			}
			if len(disjuncts) == 1 {
				where.WriteString(` AND ` + disjuncts[0])
			} else {
				where.WriteString(` AND (` + strings.Join(disjuncts, ` OR `) + `)`)
			}
			*args = append(*args, dargs...)
			continue
		}

		ftype := types[key]
		switch ftype {
		case "int", "date", "duration":
			appendIntFieldClause(where, args, key, ftype, cleanVals)
		case "float":
			appendFloatFieldClause(where, args, key, cleanVals)
		case "people":
			appendPeopleFieldClause(where, args, key, cleanVals)
		default:
			// Untyped caller passing "min..max" integer ranges (legacy/direct Browse).
			if ftype == "" && allIntRanges(cleanVals) {
				appendIntFieldClause(where, args, key, "int", cleanVals)
				continue
			}
			placeholders := make([]string, len(cleanVals))
			*args = append(*args, key)
			for idx, v := range cleanVals {
				placeholders[idx] = "?"
				*args = append(*args, v)
			}
			where.WriteString(` AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text IN (` + strings.Join(placeholders, ", ") + `))`)
		}
	}
}

func allIntRanges(vals []string) bool {
	if len(vals) == 0 {
		return false
	}
	for _, v := range vals {
		if _, ok := ParseIntRange(v); !ok {
			return false
		}
	}
	return true
}

func appendIntFieldClause(where *strings.Builder, args *[]any, key, ftype string, vals []string) {
	var disjuncts []string
	var dargs []any
	var exactPh []string
	var exactArgs []any
	for _, v := range vals {
		if ftype == "duration" {
			if lo, hi, ok := ParseDurationBucket(v); ok {
				if hi == math.MaxInt64 {
					disjuncts = append(disjuncts, `f.v_int >= ?`)
					dargs = append(dargs, lo)
				} else {
					disjuncts = append(disjuncts, `f.v_int BETWEEN ? AND ?`)
					dargs = append(dargs, lo, hi)
				}
				continue
			}
		}
		if ftype == "date" {
			if lo, hi, ok := ParseDateYearBucket(v); ok {
				disjuncts = append(disjuncts, `f.v_int BETWEEN ? AND ?`)
				dargs = append(dargs, lo, hi)
				continue
			}
		}
		if r, ok := ParseIntRange(v); ok {
			q, rargs := r.SQL(`f.v_int`)
			disjuncts = append(disjuncts, q)
			dargs = append(dargs, rargs...)
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		exactPh = append(exactPh, "?")
		exactArgs = append(exactArgs, n)
	}
	if len(exactPh) > 0 {
		disjuncts = append(disjuncts, `f.v_int IN (`+strings.Join(exactPh, ", ")+`)`)
		dargs = append(dargs, exactArgs...)
	}
	if len(disjuncts) == 0 {
		where.WriteString(` AND 0=1`)
		return
	}
	*args = append(*args, key)
	*args = append(*args, dargs...)
	pred := disjuncts[0]
	if len(disjuncts) > 1 {
		pred = `(` + strings.Join(disjuncts, ` OR `) + `)`
	}
	where.WriteString(` AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text = '' AND ` + pred + `)`)
}

func appendFloatFieldClause(where *strings.Builder, args *[]any, key string, vals []string) {
	var disjuncts []string
	var dargs []any
	var exactPh []string
	var exactArgs []any
	for _, v := range vals {
		if r, ok := ParseFloatRange(v); ok {
			q, rargs := r.SQL(`f.v_real`)
			disjuncts = append(disjuncts, q)
			dargs = append(dargs, rargs...)
			continue
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			continue
		}
		exactPh = append(exactPh, "?")
		exactArgs = append(exactArgs, n)
	}
	if len(exactPh) > 0 {
		disjuncts = append(disjuncts, `f.v_real IN (`+strings.Join(exactPh, ", ")+`)`)
		dargs = append(dargs, exactArgs...)
	}
	if len(disjuncts) == 0 {
		where.WriteString(` AND 0=1`)
		return
	}
	*args = append(*args, key)
	*args = append(*args, dargs...)
	pred := disjuncts[0]
	if len(disjuncts) > 1 {
		pred = `(` + strings.Join(disjuncts, ` OR `) + `)`
	}
	where.WriteString(` AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text = '' AND ` + pred + `)`)
}

func appendPeopleFieldClause(where *strings.Builder, args *[]any, key string, vals []string) {
	var pids []int64
	for _, v := range vals {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && core.IsPluginPersonID(n) {
			pids = append(pids, n)
			continue
		}
		if pid := core.PluginPersonID(v); pid > 0 {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		where.WriteString(` AND 0=1`)
		return
	}
	placeholders := make([]string, len(pids))
	*args = append(*args, key)
	for idx, pid := range pids {
		placeholders[idx] = "?"
		*args = append(*args, pid)
	}
	where.WriteString(` AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_int <> 0 AND f.v_int IN (` + strings.Join(placeholders, ", ") + `))`)
}
