package flows

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

func TestEvalConditionOperatorsAndGroups(t *testing.T) {
	ev, err := events.NewEnvelope("playback.session.started", map[string]any{
		"titleName":   "The Matrix",
		"bitrateKbps": 18500,
		"ratio":       0.92,
		"remote":      true,
		"paused":      false,
		"delivery":    "transcode",
		"genres":      []string{"Action", "Sci-Fi", "Cyberpunk"},
		"startedAt":   "2026-11-19T22:00:00Z",
		"client": map[string]any{
			"platform": "tvOS",
			"name":     "Living Room Apple TV",
		},
	},
		events.WithID("evt_1"),
		events.WithNode("node-east"),
		events.WithOccurredAt(time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)),
		events.WithCorrelation(events.Correlation{SessionID: "sess_1", ViewerID: 7}),
	)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	ctx, err := NewEvalContext(
		ev,
		map[string]any{"threshold": 15000, "tag": "vip"},
		map[string]map[string]any{"win1": {"output": map[string]any{"count": 3}}},
		map[string]any{"kind": "event"},
	)
	if err != nil {
		t.Fatalf("NewEvalContext: %v", err)
	}

	cases := []struct {
		name string
		rule ConditionRule
		want bool
	}{
		{"eq string ci", ConditionRule{Field: "payload.titleName", Op: OpEq, Value: "the matrix"}, true},
		{"eq string cs mismatch", ConditionRule{Field: "payload.titleName", Op: OpEq, Value: "the matrix", CaseSensitive: true}, false},
		{"neq string", ConditionRule{Field: "titleName", Op: OpNeq, Value: "Dune"}, true},
		{"gt json.Number", ConditionRule{Field: "payload.bitrateKbps", Op: OpGt, Value: 15000}, true},
		{"gte float", ConditionRule{Field: "payload.ratio", Op: OpGte, Value: 0.92}, true},
		{"lt int", ConditionRule{Field: "payload.bitrateKbps", Op: OpLt, Value: 10000}, false},
		{"lte int", ConditionRule{Field: "correlation.viewerId", Op: OpLte, Value: 7}, true},
		{"timestamp gt", ConditionRule{Field: "payload.startedAt", Op: OpGt, Value: "2026-11-19T21:00:00Z"}, true},
		{"timestamp lt", ConditionRule{Field: "payload.startedAt", Op: OpLt, Value: "2026-11-19T21:00:00Z"}, false},
		{"contains string", ConditionRule{Field: "payload.client.name", Op: OpContains, Value: "apple tv"}, true},
		{"not_contains string", ConditionRule{Field: "payload.client.name", Op: OpNotContains, Value: "roku"}, true},
		{"starts_with", ConditionRule{Field: "payload.titleName", Op: OpStartsWith, Value: "the "}, true},
		{"ends_with", ConditionRule{Field: "payload.titleName", Op: OpEndsWith, Value: "matrix"}, true},
		{"matches regex", ConditionRule{Field: "payload.client.platform", Op: OpMatches, Value: "^tv(os|lite)$"}, true},
		{"in set", ConditionRule{Field: "payload.delivery", Op: OpIn, Value: []string{"remux", "transcode"}}, true},
		{"not_in set", ConditionRule{Field: "payload.delivery", Op: OpNotIn, Value: []string{"direct_play"}}, true},
		{"exists nested", ConditionRule{Field: "payload.client.platform", Op: OpExists}, true},
		{"not_exists missing", ConditionRule{Field: "payload.nonexistent", Op: OpNotExists}, true},
		{"is_true", ConditionRule{Field: "payload.remote", Op: OpIsTrue}, true},
		{"is_false", ConditionRule{Field: "payload.paused", Op: OpIsFalse}, true},
		{"list contains", ConditionRule{Field: "payload.genres", Op: OpContains, Value: "sci-fi"}, true},
		{"list contains_any", ConditionRule{Field: "payload.genres", Op: OpContainsAny, Value: []string{"Horror", "Cyberpunk"}}, true},
		{"list contains_all true", ConditionRule{Field: "payload.genres", Op: OpContainsAll, Value: []string{"Action", "Sci-Fi"}}, true},
		{"list contains_all false", ConditionRule{Field: "payload.genres", Op: OpContainsAll, Value: []string{"Action", "Comedy"}}, false},
		{"event meta", ConditionRule{Field: "event.node", Op: OpEq, Value: "node-east"}, true},
		{"event.payload path", ConditionRule{Field: "event.payload.delivery", Op: OpEq, Value: "transcode"}, true},
		{"vars path", ConditionRule{Field: "vars.tag", Op: OpEq, Value: "vip"}, true},
		{"steps output path", ConditionRule{Field: "steps.win1.output.count", Op: OpGte, Value: 3}, true},
		{"trigger path", ConditionRule{Field: "trigger.kind", Op: OpEq, Value: "event"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, trace, err := EvalConditionGroup(ConditionGroup{Op: LogicAnd, Rules: []ConditionRule{tc.rule}}, ctx)
			if err != nil {
				t.Fatalf("EvalConditionGroup error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v (trace: %+v)", got, tc.want, trace)
			}
			if len(trace.Rules) != 1 || trace.Rules[0].Matched != tc.want {
				t.Fatalf("unexpected rule trace: %+v", trace)
			}
		})
	}

	// Recursive AND + OR + Negate
	complex := ConditionGroup{
		Op: LogicAnd,
		Rules: []ConditionRule{
			{Field: "payload.remote", Op: OpIsTrue},
		},
		Groups: []ConditionGroup{
			{
				Op: LogicOr,
				Rules: []ConditionRule{
					{Field: "payload.bitrateKbps", Op: OpGt, Value: 25000},
					{Field: "payload.delivery", Op: OpEq, Value: "transcode"},
				},
			},
			{
				Op:     LogicAnd,
				Negate: true,
				Rules: []ConditionRule{
					{Field: "payload.client.platform", Op: OpEq, Value: "Android"},
				},
			},
		},
	}
	ok, trace, err := EvalConditionGroup(complex, ctx)
	if err != nil || !ok || !trace.Matched || len(trace.Groups) != 2 {
		t.Fatalf("complex group = %v, err=%v, trace=%+v", ok, err, trace)
	}

	// Empty group defaults true, negated defaults false.
	if ok, _, err := EvalConditionGroup(ConditionGroup{}, ctx); err != nil || !ok {
		t.Fatalf("empty group = %v, err=%v", ok, err)
	}
	if ok, _, err := EvalConditionGroup(ConditionGroup{Negate: true}, ctx); err != nil || ok {
		t.Fatalf("negated empty group = %v, err=%v", ok, err)
	}

	// Verify json.Number equality works with Go int/float values and exact int64 precision.
	if !valuesEqual(json.Number("42"), 42, false) {
		t.Fatal("json.Number should equal Go int")
	}
	const bigA int64 = 1<<53 + 1
	const bigB int64 = 1<<53 + 2
	if valuesEqual(json.Number("9007199254740993"), bigB, false) {
		t.Fatal("large int64 values should not lose precision via float64")
	}
	if !valuesEqual(json.Number("9007199254740993"), bigA, false) {
		t.Fatal("large int64 json.Number should equal exact int64")
	}
	if cmp, ok := compareOrdered(json.Number("9007199254740993"), bigB); !ok || cmp >= 0 {
		t.Fatalf("compareOrdered(bigA, bigB) = (%d, %v), want (<0, true)", cmp, ok)
	}
}
