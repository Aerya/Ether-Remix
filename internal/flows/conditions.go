package flows

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

// LogicOp combines rules and sub-groups inside a ConditionGroup.
type LogicOp string

const (
	LogicAnd LogicOp = "and"
	LogicOr  LogicOp = "or"
)

// Operator is one typed comparison inside a ConditionRule.
type Operator string

const (
	OpEq          Operator = "eq"
	OpNeq         Operator = "neq"
	OpGt          Operator = "gt"
	OpGte         Operator = "gte"
	OpLt          Operator = "lt"
	OpLte         Operator = "lte"
	OpContains    Operator = "contains"
	OpNotContains Operator = "not_contains"
	OpStartsWith  Operator = "starts_with"
	OpEndsWith    Operator = "ends_with"
	OpMatches     Operator = "matches"
	OpIn          Operator = "in"
	OpNotIn       Operator = "not_in"
	OpExists      Operator = "exists"
	OpNotExists   Operator = "not_exists"
	OpIsTrue      Operator = "is_true"
	OpIsFalse     Operator = "is_false"
	OpContainsAny Operator = "contains_any"
	OpContainsAll Operator = "contains_all"
)

// Valid reports whether o is a supported condition operator.
func (o Operator) Valid() bool {
	switch o {
	case OpEq, OpNeq, OpGt, OpGte, OpLt, OpLte,
		OpContains, OpNotContains, OpStartsWith, OpEndsWith, OpMatches,
		OpIn, OpNotIn, OpExists, OpNotExists, OpIsTrue, OpIsFalse,
		OpContainsAny, OpContainsAll:
		return true
	default:
		return false
	}
}

// RequiresValue reports whether o expects a right-hand comparison Value.
func (o Operator) RequiresValue() bool {
	switch o {
	case OpExists, OpNotExists, OpIsTrue, OpIsFalse:
		return false
	default:
		return true
	}
}

// OperatorsForFieldType returns the valid operators for a schema FieldType,
// in console display order.
func OperatorsForFieldType(t events.FieldType) []Operator {
	switch t {
	case events.FieldString:
		return []Operator{OpEq, OpNeq, OpContains, OpNotContains, OpStartsWith, OpEndsWith, OpMatches, OpIn, OpNotIn, OpExists, OpNotExists}
	case events.FieldEnum:
		return []Operator{OpEq, OpNeq, OpIn, OpNotIn, OpExists, OpNotExists}
	case events.FieldInt, events.FieldFloat, events.FieldDurationMS, events.FieldTimestamp:
		return []Operator{OpEq, OpNeq, OpGt, OpGte, OpLt, OpLte, OpIn, OpNotIn, OpExists, OpNotExists}
	case events.FieldBool:
		return []Operator{OpIsTrue, OpIsFalse, OpEq, OpNeq, OpExists, OpNotExists}
	case events.FieldStringList:
		return []Operator{OpContains, OpNotContains, OpContainsAny, OpContainsAll, OpExists, OpNotExists}
	case events.FieldObject:
		return []Operator{OpExists, OpNotExists}
	default:
		return []Operator{OpEq, OpNeq, OpExists, OpNotExists}
	}
}

// OperatorAllowedForField reports whether op is valid for field type t.
func OperatorAllowedForField(t events.FieldType, op Operator) bool {
	for _, allowed := range OperatorsForFieldType(t) {
		if allowed == op {
			return true
		}
	}
	return false
}

// ConditionRule compares a field path against an optional value.
type ConditionRule struct {
	Field         string   `json:"field"`
	Op            Operator `json:"op"`
	Value         any      `json:"value,omitempty"`
	CaseSensitive bool     `json:"caseSensitive,omitempty"`
}

// ConditionGroup is a recursive AND/OR condition tree. An empty group matches true
// (or false when Negate is true).
type ConditionGroup struct {
	Op     LogicOp          `json:"op,omitempty"`
	Negate bool             `json:"negate,omitempty"`
	Rules  []ConditionRule  `json:"rules,omitempty"`
	Groups []ConditionGroup `json:"groups,omitempty"`
}

// IsEmpty reports whether g has no rules and no nested groups.
func (g ConditionGroup) IsEmpty() bool {
	return len(g.Rules) == 0 && len(g.Groups) == 0
}

// EffectiveOp returns g.Op, defaulting to LogicAnd.
func (g ConditionGroup) EffectiveOp() LogicOp {
	if g.Op == LogicOr {
		return LogicOr
	}
	return LogicAnd
}

// EvalContext is the read-only data view available to condition evaluation and
// template rendering during a flow run.
type EvalContext struct {
	Event       map[string]any
	Payload     map[string]any
	Correlation map[string]any
	Vars        map[string]any
	Steps       map[string]map[string]any
	Trigger     map[string]any
}

// NewEvalContext builds an EvalContext from an event Envelope plus run state.
func NewEvalContext(ev events.Envelope, vars map[string]any, steps map[string]map[string]any, trigger map[string]any) (EvalContext, error) {
	payload, err := ev.PayloadMap()
	if err != nil {
		return EvalContext{}, err
	}
	meta := ev.MetaMap()
	meta["payload"] = payload
	return EvalContext{
		Event:       meta,
		Payload:     payload,
		Correlation: ev.Correlation.ToMap(),
		Vars:        vars,
		Steps:       steps,
		Trigger:     trigger,
	}, nil
}

// RuleTrace records the evaluation details of a single ConditionRule for
// console inspection and dry-run debugging.
type RuleTrace struct {
	Field    string   `json:"field"`
	Op       Operator `json:"op"`
	Expected any      `json:"expected,omitempty"`
	Actual   any      `json:"actual,omitempty"`
	Found    bool     `json:"found"`
	Matched  bool     `json:"matched"`
}

// ConditionTrace records the recursive evaluation trace of a ConditionGroup.
type ConditionTrace struct {
	Op      LogicOp          `json:"op"`
	Negate  bool             `json:"negate,omitempty"`
	Matched bool             `json:"matched"`
	Rules   []RuleTrace      `json:"rules,omitempty"`
	Groups  []ConditionTrace `json:"groups,omitempty"`
}

// EvalConditionGroup evaluates g against ctx and returns the boolean verdict
// along with a structured trace of every rule and nested group.
func EvalConditionGroup(g ConditionGroup, ctx EvalContext) (bool, ConditionTrace, error) {
	op := g.EffectiveOp()
	trace := ConditionTrace{Op: op, Negate: g.Negate}

	if g.IsEmpty() {
		res := !g.Negate
		trace.Matched = res
		return res, trace, nil
	}

	var matched bool
	if op == LogicAnd {
		matched = true
	}

	for _, r := range g.Rules {
		ok, rt, err := evalRule(r, ctx)
		if err != nil {
			return false, trace, err
		}
		trace.Rules = append(trace.Rules, rt)
		if op == LogicAnd {
			matched = matched && ok
		} else {
			matched = matched || ok
		}
	}

	for _, sub := range g.Groups {
		ok, st, err := EvalConditionGroup(sub, ctx)
		if err != nil {
			return false, trace, err
		}
		trace.Groups = append(trace.Groups, st)
		if op == LogicAnd {
			matched = matched && ok
		} else {
			matched = matched || ok
		}
	}

	if g.Negate {
		matched = !matched
	}
	trace.Matched = matched
	return matched, trace, nil
}

// ResolvePath resolves a dotted path against ctx.
// Supported roots:
//   - "payload.<path>" -> ctx.Payload
//   - "event.<path>" -> ctx.Event (also supports "event.payload.<path>")
//   - "correlation.<path>" -> ctx.Correlation
//   - "vars.<path>" -> ctx.Vars
//   - "steps.<nodeId>.<path>" -> ctx.Steps[nodeId]
//   - "trigger.<path>" -> ctx.Trigger
//   - bare "<path>" (no recognized root) -> ctx.Payload
func ResolvePath(path string, ctx EvalContext) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	head, rest, hasRest := strings.Cut(path, ".")
	switch head {
	case "payload":
		if !hasRest {
			return ctx.Payload, ctx.Payload != nil
		}
		return walkPath(ctx.Payload, rest)
	case "event":
		if !hasRest {
			return ctx.Event, ctx.Event != nil
		}
		if rest == "payload" {
			return ctx.Payload, ctx.Payload != nil
		}
		if after, ok := strings.CutPrefix(rest, "payload."); ok {
			return walkPath(ctx.Payload, after)
		}
		return walkPath(ctx.Event, rest)
	case "correlation":
		if !hasRest {
			return ctx.Correlation, ctx.Correlation != nil
		}
		return walkPath(ctx.Correlation, rest)
	case "vars":
		if !hasRest {
			return ctx.Vars, ctx.Vars != nil
		}
		return walkPath(ctx.Vars, rest)
	case "trigger":
		if !hasRest {
			return ctx.Trigger, ctx.Trigger != nil
		}
		return walkPath(ctx.Trigger, rest)
	case "steps":
		if !hasRest || ctx.Steps == nil {
			return nil, false
		}
		nodeID, sub, hasSub := strings.Cut(rest, ".")
		stepMap, ok := ctx.Steps[nodeID]
		if !ok {
			return nil, false
		}
		if !hasSub {
			return stepMap, true
		}
		return walkPath(stepMap, sub)
	default:
		return walkPath(ctx.Payload, path)
	}
}

func walkPath(root map[string]any, path string) (any, bool) {
	if root == nil || path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		next, exists := m[p]
		if !exists {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func evalRule(r ConditionRule, ctx EvalContext) (bool, RuleTrace, error) {
	actual, found := ResolvePath(r.Field, ctx)
	trace := RuleTrace{
		Field:    r.Field,
		Op:       r.Op,
		Expected: r.Value,
		Actual:   actual,
		Found:    found,
	}

	var matched bool
	var err error
	switch r.Op {
	case OpExists:
		matched = found && actual != nil
	case OpNotExists:
		matched = !found || actual == nil
	case OpIsTrue:
		b, ok := asBool(actual)
		matched = found && ok && b
	case OpIsFalse:
		b, ok := asBool(actual)
		matched = found && ok && !b
	case OpEq:
		matched = found && valuesEqual(actual, r.Value, r.CaseSensitive)
	case OpNeq:
		matched = !found || !valuesEqual(actual, r.Value, r.CaseSensitive)
	case OpGt, OpGte, OpLt, OpLte:
		if found {
			cmp, ok := compareOrdered(actual, r.Value)
			if ok {
				switch r.Op {
				case OpGt:
					matched = cmp > 0
				case OpGte:
					matched = cmp >= 0
				case OpLt:
					matched = cmp < 0
				case OpLte:
					matched = cmp <= 0
				}
			}
		}
	case OpContains:
		matched = found && evalContains(actual, r.Value, r.CaseSensitive)
	case OpNotContains:
		matched = !found || !evalContains(actual, r.Value, r.CaseSensitive)
	case OpStartsWith:
		if found {
			a, okA := asString(actual)
			b, okB := asString(r.Value)
			if okA && okB {
				if !r.CaseSensitive {
					a, b = strings.ToLower(a), strings.ToLower(b)
				}
				matched = strings.HasPrefix(a, b)
			}
		}
	case OpEndsWith:
		if found {
			a, okA := asString(actual)
			b, okB := asString(r.Value)
			if okA && okB {
				if !r.CaseSensitive {
					a, b = strings.ToLower(a), strings.ToLower(b)
				}
				matched = strings.HasSuffix(a, b)
			}
		}
	case OpMatches:
		if found {
			matched, err = evalMatches(actual, r.Value, r.CaseSensitive)
		}
	case OpIn:
		matched = found && valueInSet(actual, r.Value, r.CaseSensitive)
	case OpNotIn:
		matched = !found || !valueInSet(actual, r.Value, r.CaseSensitive)
	case OpContainsAny:
		matched = found && evalListOverlap(actual, r.Value, r.CaseSensitive, false)
	case OpContainsAll:
		matched = found && evalListOverlap(actual, r.Value, r.CaseSensitive, true)
	default:
		return false, trace, fmt.Errorf("flows: unknown operator %q", r.Op)
	}
	if err != nil {
		return false, trace, err
	}
	trace.Matched = matched
	return matched, trace, nil
}

func evalMatches(actual, patternVal any, caseSensitive bool) (bool, error) {
	pat, ok := asString(patternVal)
	if !ok {
		return false, fmt.Errorf("flows: matches operator requires string pattern")
	}
	if len(pat) > MaxRegexLen {
		return false, fmt.Errorf("flows: regex pattern exceeds %d bytes", MaxRegexLen)
	}
	if !caseSensitive && !strings.HasPrefix(pat, "(?") {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return false, fmt.Errorf("flows: invalid regex %q: %w", pat, err)
	}
	s, ok := asString(actual)
	if !ok {
		return false, nil
	}
	return re.MatchString(s), nil
}

func evalContains(actual, needle any, caseSensitive bool) bool {
	if list, ok := asSlice(actual); ok {
		for _, item := range list {
			if valuesEqual(item, needle, caseSensitive) {
				return true
			}
		}
		return false
	}
	a, okA := asString(actual)
	b, okB := asString(needle)
	if !okA || !okB {
		return false
	}
	if !caseSensitive {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return strings.Contains(a, b)
}

func valueInSet(actual, setVal any, caseSensitive bool) bool {
	items, ok := asSlice(setVal)
	if !ok {
		return valuesEqual(actual, setVal, caseSensitive)
	}
	for _, item := range items {
		if valuesEqual(actual, item, caseSensitive) {
			return true
		}
	}
	return false
}

func evalListOverlap(actual, wantVal any, caseSensitive, requireAll bool) bool {
	haystack, okH := asSlice(actual)
	if !okH {
		return false
	}
	needles, okN := asSlice(wantVal)
	if !okN {
		needles = []any{wantVal}
	}
	if len(needles) == 0 {
		return requireAll
	}
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if valuesEqual(h, n, caseSensitive) {
				found = true
				break
			}
		}
		if requireAll && !found {
			return false
		}
		if !requireAll && found {
			return true
		}
	}
	return requireAll
}

func valuesEqual(a, b any, caseSensitive bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if ba, okA := asBool(a); okA {
		if bb, okB := asBool(b); okB {
			return ba == bb
		}
	}
	if ia, okA := asInt64(a); okA {
		if ib, okB := asInt64(b); okB {
			return ia == ib
		}
	}
	if fa, okA := asFloat(a); okA {
		if fb, okB := asFloat(b); okB {
			return fa == fb
		}
	}
	if ta, okA := asTime(a); okA {
		if tb, okB := asTime(b); okB {
			return ta.Equal(tb)
		}
	}
	if sa, okA := asString(a); okA {
		if sb, okB := asString(b); okB {
			if caseSensitive {
				return sa == sb
			}
			return strings.EqualFold(sa, sb)
		}
	}
	return reflect.DeepEqual(a, b)
}

func compareOrdered(a, b any) (int, bool) {
	if ia, okA := asInt64(a); okA {
		if ib, okB := asInt64(b); okB {
			switch {
			case ia < ib:
				return -1, true
			case ia > ib:
				return 1, true
			default:
				return 0, true
			}
		}
	}
	if fa, okA := asFloat(a); okA {
		if fb, okB := asFloat(b); okB {
			if math.IsNaN(fa) || math.IsNaN(fb) {
				return 0, false
			}
			switch {
			case fa < fb:
				return -1, true
			case fa > fb:
				return 1, true
			default:
				return 0, true
			}
		}
	}
	if ta, okA := asTime(a); okA {
		if tb, okB := asTime(b); okB {
			switch {
			case ta.Before(tb):
				return -1, true
			case ta.After(tb):
				return 1, true
			default:
				return 0, true
			}
		}
	}
	return 0, false
}

func asBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		if strings.EqualFold(x, "true") {
			return true, true
		}
		if strings.EqualFold(x, "false") {
			return false, true
		}
	}
	return false, false
}

func asInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		if uint64(x) <= math.MaxInt64 {
			return int64(x), true
		}
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		if x <= math.MaxInt64 {
			return int64(x), true
		}
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		if s := strings.TrimSpace(x); s != "" {
			f, err := strconv.ParseFloat(s, 64)
			return f, err == nil
		}
	}
	return 0, false
}

func asTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, !x.IsZero()
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func asString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case fmt.Stringer:
		return x.String(), true
	}
	return "", false
}

func asSlice(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out, true
	case []int:
		out := make([]any, len(x))
		for i, n := range x {
			out[i] = n
		}
		return out, true
	case []int64:
		out := make([]any, len(x))
		for i, n := range x {
			out[i] = n
		}
		return out, true
	case []float64:
		out := make([]any, len(x))
		for i, n := range x {
			out[i] = n
		}
		return out, true
	default:
		return nil, false
	}
}
