package libraryfilter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestParseIntRange(t *testing.T) {
	good := []struct {
		in       string
		want     IntRange
		wantSQL  string
		wantArgs []any
		exclZero bool
	}{
		{
			in:       "10..20",
			want:     IntRange{HasLo: true, Lo: 10, LoInc: true, HasHi: true, Hi: 20, HiInc: true},
			wantSQL:  "f.v_int BETWEEN ? AND ?",
			wantArgs: []any{int64(10), int64(20)},
			exclZero: true,
		},
		{
			in:       "10..",
			want:     IntRange{HasLo: true, Lo: 10, LoInc: true},
			wantSQL:  "f.v_int >= ?",
			wantArgs: []any{int64(10)},
			exclZero: true,
		},
		{
			in:       "..20",
			want:     IntRange{HasHi: true, Hi: 20, HiInc: true},
			wantSQL:  "f.v_int <= ?",
			wantArgs: []any{int64(20)},
			exclZero: false,
		},
		{
			in:       "<20",
			want:     IntRange{HasHi: true, Hi: 20, HiInc: false},
			wantSQL:  "f.v_int < ?",
			wantArgs: []any{int64(20)},
			exclZero: false,
		},
		{
			in:       "..<20",
			want:     IntRange{HasHi: true, Hi: 20, HiInc: false},
			wantSQL:  "f.v_int < ?",
			wantArgs: []any{int64(20)},
			exclZero: false,
		},
		{
			in:       ">0",
			want:     IntRange{HasLo: true, Lo: 0, LoInc: false},
			wantSQL:  "f.v_int > ?",
			wantArgs: []any{int64(0)},
			exclZero: true,
		},
		{
			in:       ">10..",
			want:     IntRange{HasLo: true, Lo: 10, LoInc: false},
			wantSQL:  "f.v_int > ?",
			wantArgs: []any{int64(10)},
			exclZero: true,
		},
		{
			in:       "<-100",
			want:     IntRange{HasHi: true, Hi: -100, HiInc: false},
			wantSQL:  "f.v_int < ?",
			wantArgs: []any{int64(-100)},
			exclZero: true,
		},
		{
			in:       "-500..-100",
			want:     IntRange{HasLo: true, Lo: -500, LoInc: true, HasHi: true, Hi: -100, HiInc: true},
			wantSQL:  "f.v_int BETWEEN ? AND ?",
			wantArgs: []any{int64(-500), int64(-100)},
			exclZero: true,
		},
		{
			in:       ">10..<20",
			want:     IntRange{HasLo: true, Lo: 10, LoInc: false, HasHi: true, Hi: 20, HiInc: false},
			wantSQL:  "(f.v_int > ? AND f.v_int < ?)",
			wantArgs: []any{int64(10), int64(20)},
			exclZero: true,
		},
		{
			in:       "<=0",
			want:     IntRange{HasHi: true, Hi: 0, HiInc: true},
			wantSQL:  "f.v_int <= ?",
			wantArgs: []any{int64(0)},
			exclZero: false,
		},
		{
			in:       "<0",
			want:     IntRange{HasHi: true, Hi: 0, HiInc: false},
			wantSQL:  "f.v_int < ?",
			wantArgs: []any{int64(0)},
			exclZero: true,
		},
		{
			in:       ">=5",
			want:     IntRange{HasLo: true, Lo: 5, LoInc: true},
			wantSQL:  "f.v_int >= ?",
			wantArgs: []any{int64(5)},
			exclZero: true,
		},
	}
	for _, tc := range good {
		got, ok := ParseIntRange(tc.in)
		if !ok {
			t.Fatalf("ParseIntRange(%q) failed, want %+v", tc.in, tc.want)
		}
		if got != tc.want {
			t.Fatalf("ParseIntRange(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
		if got.ExcludesZero() != tc.exclZero {
			t.Fatalf("ParseIntRange(%q).ExcludesZero() = %v, want %v", tc.in, got.ExcludesZero(), tc.exclZero)
		}
		sql, args := got.SQL("f.v_int")
		if sql != tc.wantSQL || len(args) != len(tc.wantArgs) {
			t.Fatalf("ParseIntRange(%q).SQL() = (%q, %v), want (%q, %v)", tc.in, sql, args, tc.wantSQL, tc.wantArgs)
		}
		for i := range args {
			if args[i] != tc.wantArgs[i] {
				t.Fatalf("ParseIntRange(%q) arg[%d] = %v, want %v", tc.in, i, args[i], tc.wantArgs[i])
			}
		}
	}

	bad := []string{"", "..", "  ..  ", "20..10", ">10..10", "10..<10", ">10..<10", "42", "<", ">", "<=", ">=", "abc..10", "10..def", "1.5..2.5"}
	for _, in := range bad {
		if r, ok := ParseIntRange(in); ok {
			t.Fatalf("ParseIntRange(%q) unexpectedly succeeded: %+v", in, r)
		}
	}
}

func TestParseFloatRange(t *testing.T) {
	good := []struct {
		in      string
		want    FloatRange
		wantSQL string
	}{
		{
			in:      "7.5..9.5",
			want:    FloatRange{HasLo: true, Lo: 7.5, LoInc: true, HasHi: true, Hi: 9.5, HiInc: true},
			wantSQL: "f.v_real BETWEEN ? AND ?",
		},
		{
			in:      "7.5..",
			want:    FloatRange{HasLo: true, Lo: 7.5, LoInc: true},
			wantSQL: "f.v_real >= ?",
		},
		{
			in:      "..8.5",
			want:    FloatRange{HasHi: true, Hi: 8.5, HiInc: true},
			wantSQL: "f.v_real <= ?",
		},
		{
			in:      "<8.0",
			want:    FloatRange{HasHi: true, Hi: 8.0, HiInc: false},
			wantSQL: "f.v_real < ?",
		},
		{
			in:      ">7.5",
			want:    FloatRange{HasLo: true, Lo: 7.5, LoInc: false},
			wantSQL: "f.v_real > ?",
		},
		{
			in:      ">7.5..<9",
			want:    FloatRange{HasLo: true, Lo: 7.5, LoInc: false, HasHi: true, Hi: 9, HiInc: false},
			wantSQL: "(f.v_real > ? AND f.v_real < ?)",
		},
	}
	for _, tc := range good {
		got, ok := ParseFloatRange(tc.in)
		if !ok || got != tc.want {
			t.Fatalf("ParseFloatRange(%q) = (%+v, %v), want (%+v, true)", tc.in, got, ok, tc.want)
		}
		sql, _ := got.SQL("f.v_real")
		if sql != tc.wantSQL {
			t.Fatalf("ParseFloatRange(%q).SQL() = %q, want %q", tc.in, sql, tc.wantSQL)
		}
	}

	bad := []string{"", "..", "9..7", "8..<8", ">8..8", "NaN..5", "0..Inf", "-Inf..5", "<NaN", ">Inf", "7.5"}
	for _, in := range bad {
		if r, ok := ParseFloatRange(in); ok {
			t.Fatalf("ParseFloatRange(%q) unexpectedly succeeded: %+v", in, r)
		}
	}
}

func TestAppendPluginFacetClausesOpenRangesAndEnvelopeGuards(t *testing.T) {
	types := map[string]string{
		"pages":     "int",
		"score":     "float",
		"runtime":   "duration",
		"published": "date",
	}

	cases := []struct {
		name     string
		facets   map[string][]string
		wantFrag string
		wantArgs []any
	}{
		{
			name:     "envelope year open upper excludes unset zero",
			facets:   map[string][]string{"year": {"<2010"}},
			wantFrag: `(i.year > 0 AND i.year < ?)`,
			wantArgs: []any{int64(2010)},
		},
		{
			name:     "envelope year open lower",
			facets:   map[string][]string{"year": {"2010.."}},
			wantFrag: `i.year >= ?`,
			wantArgs: []any{int64(2010)},
		},
		{
			name:     "envelope released open upper excludes zero but allows negative timestamps",
			facets:   map[string][]string{"released": {"<1704067200"}},
			wantFrag: `(i.released <> 0 AND i.released < ?)`,
			wantArgs: []any{int64(1704067200)},
		},
		{
			name:     "envelope released pre-1970 open upper omits redundant guard",
			facets:   map[string][]string{"released": {"<-1000"}},
			wantFrag: `i.released < ?`,
			wantArgs: []any{int64(-1000)},
		},
		{
			name:     "envelope rating exclusive lower",
			facets:   map[string][]string{"rating": {">7.5"}},
			wantFrag: `i.rating > ?`,
			wantArgs: []any{float64(7.5)},
		},
		{
			name:     "envelope rating open upper excludes unset zero",
			facets:   map[string][]string{"rating": {"..7.5"}},
			wantFrag: `(i.rating > 0 AND i.rating <= ?)`,
			wantArgs: []any{float64(7.5)},
		},
		{
			name:     "int open upper",
			facets:   map[string][]string{"pages": {"..300"}},
			wantFrag: `f.field = ? AND f.v_text = '' AND f.v_int <= ?`,
			wantArgs: []any{"pages", int64(300)},
		},
		{
			name:     "duration exclusive lower",
			facets:   map[string][]string{"runtime": {">1800"}},
			wantFrag: `f.field = ? AND f.v_text = '' AND f.v_int > ?`,
			wantArgs: []any{"runtime", int64(1800)},
		},
		{
			name:     "float exclusive upper",
			facets:   map[string][]string{"score": {"<8.5"}},
			wantFrag: `f.field = ? AND f.v_text = '' AND f.v_real < ?`,
			wantArgs: []any{"score", float64(8.5)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w strings.Builder
			var args []any
			AppendPluginFacetClauses(&w, &args, tc.facets, types)
			if !strings.Contains(w.String(), tc.wantFrag) {
				t.Fatalf("SQL = %q, want fragment %q", w.String(), tc.wantFrag)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, tc.wantArgs)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Fatalf("args[%d] = %#v, want %#v", i, args[i], tc.wantArgs[i])
				}
			}
		})
	}
}

func TestCompilePluginPolicyMultiChipModes(t *testing.T) {
	declared := map[string]string{"tags": "strings"}

	// Any of: single browse condition with array value compiles to IN.
	anyPol, err := CompilePluginPolicy([]core.LibraryExclusion{{
		Action: "show", MediaType: "movie", Match: "all",
		Rules: []core.CollectionRule{{Field: "browse:tags", Op: "is", Value: json.RawMessage(`["Sci-Fi","Cyberpunk"]`)}},
	}}, "hide", declared)
	if err != nil {
		t.Fatalf("any policy: %v", err)
	}
	sql, args := anyPol.PluginSQLShownForViewer(core.TypeMovie, nil, 0)
	if !strings.Contains(sql, "f.v_text IN (?, ?)") || len(args) != 3 {
		t.Fatalf("any SQL = %q args=%v", sql, args)
	}

	// All of: group(op="is") compiles to AND of individual EXISTS predicates.
	allPol, err := CompilePluginPolicy([]core.LibraryExclusion{{
		Action: "show", MediaType: "movie", Match: "all",
		Rules: []core.CollectionRule{{
			Field: "group",
			Op:    "is",
			Value: json.RawMessage(`[{"field":"browse:tags","op":"is","value":"Sci-Fi"},{"field":"browse:tags","op":"is","value":"Cyberpunk"}]`),
		}},
	}}, "hide", declared)
	if err != nil {
		t.Fatalf("all policy: %v", err)
	}
	sql, args = allPol.PluginSQLShownForViewer(core.TypeMovie, nil, 0)
	if strings.Count(sql, "EXISTS (SELECT 1 FROM plugin_item_fields") != 2 || !strings.Contains(sql, " AND ") || len(args) != 4 {
		t.Fatalf("all SQL = %q args=%v", sql, args)
	}

	// None of: browse(op="isNot") with array value compiles to NOT (EXISTS (... IN (?, ?))).
	nonePol, err := CompilePluginPolicy([]core.LibraryExclusion{{
		Action: "show", MediaType: "movie", Match: "all",
		Rules: []core.CollectionRule{{Field: "browse:tags", Op: "isNot", Value: json.RawMessage(`["Sci-Fi","Cyberpunk"]`)}},
	}}, "hide", declared)
	if err != nil {
		t.Fatalf("none policy: %v", err)
	}
	sql, args = nonePol.PluginSQLShownForViewer(core.TypeMovie, nil, 0)
	if !strings.Contains(sql, "NOT (EXISTS (SELECT 1 FROM plugin_item_fields") || !strings.Contains(sql, "f.v_text IN (?, ?)") || len(args) != 3 {
		t.Fatalf("none SQL = %q args=%v", sql, args)
	}
}
