package flows

import (
	"strings"
	"testing"
	"time"
)

func TestParseDurationAcceptsDaysAndWeeks(t *testing.T) {
	cases := map[string]time.Duration{
		"90s":    90 * time.Second,
		"500ms":  500 * time.Millisecond,
		"7d":     7 * 24 * time.Hour,
		"1d12h":  36 * time.Hour,
		"1.5d":   36 * time.Hour,
		"2w":     14 * 24 * time.Hour,
		" 2d ":   48 * time.Hour,
		"1w2d3h": (9*24 + 3) * time.Hour,
	}
	for in, want := range cases {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "d", "7x", "seven days", "1dd"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) succeeded; want error", bad)
		}
	}
}

// The editor offers "7 days" (7d) for delays, waits and cooldowns; those
// flows must save, and a typo must say it is not a duration rather than
// quoting Go's 720h0m0s bound.
func TestValidateDayDurations(t *testing.T) {
	evReg, actReg, trigReg := testRegistries(t)
	graph := func(delay, timeout string) Graph {
		return Graph{
			SchemaVersion: SchemaVersion,
			Nodes: []Node{
				{ID: "t", Kind: NodeTriggerEvent, Config: MustMarshalConfig(EventTriggerConfig{Events: []string{"playback.session.failed"}})},
				{ID: "d", Kind: NodeLogicDelay, Config: MustMarshalConfig(DelayNodeConfig{Duration: delay})},
				{ID: "w", Kind: NodeLogicWaitEvent, Config: MustMarshalConfig(WaitEventNodeConfig{EventTypes: []string{"livetv.input.recovered"}, Timeout: timeout})},
				{ID: "a", Kind: NodeAction, Config: MustMarshalConfig(ActionNodeConfig{ActionType: "notify.send", Params: map[string]any{"channelId": "c", "title": "x"}})},
			},
			Edges: []Edge{
				{ID: "e1", Source: "t", Target: "d", SourceHandle: HandleNext},
				{ID: "e2", Source: "d", Target: "w", SourceHandle: HandleNext},
				{ID: "e3", Source: "w", Target: "a", SourceHandle: HandleTimeout},
			},
		}
	}
	opts := ValidateOptions{Events: evReg, Actions: actReg, Triggers: trigReg}
	if res := ValidateFlow(Flow{Name: "Days", Graph: graph("7d", "2d")}, opts); !res.Valid {
		t.Fatalf("7d delay / 2d wait rejected: %+v", res.Errors)
	}
	res := ValidateFlow(Flow{Name: "Typo", Graph: graph("7x", "31d")}, opts)
	if res.Valid {
		t.Fatal("7x delay / 31d wait accepted")
	}
	var msgs []string
	for _, e := range res.Errors {
		msgs = append(msgs, e.Message)
	}
	joined := strings.Join(msgs, " | ")
	if !strings.Contains(joined, `"7x" is not a duration`) || !strings.Contains(joined, "between 1ms and 30d") {
		t.Fatalf("unexpected messages: %s", joined)
	}
}

// "Preview on canvas" used to save gallery placeholders verbatim; they
// render empty at run time, so validation names them.
func TestValidateUnresolvedTemplateParam(t *testing.T) {
	evReg, actReg, trigReg := testRegistries(t)
	g := Graph{
		SchemaVersion: SchemaVersion,
		Nodes: []Node{
			{ID: "t", Kind: NodeTriggerEvent, Config: MustMarshalConfig(EventTriggerConfig{Events: []string{"playback.session.failed"}})},
			{ID: "a", Kind: NodeAction, Config: MustMarshalConfig(ActionNodeConfig{ActionType: "notify.send", Params: map[string]any{"channelId": "{{ param.brokerId }}", "title": "{{param.topic}} {{ payload.titleName }}"}})},
		},
		Edges: []Edge{{ID: "e1", Source: "t", Target: "a", SourceHandle: HandleNext}},
	}
	res := ValidateFlow(Flow{Name: "Preview", Graph: g}, ValidateOptions{Events: evReg, Actions: actReg, Triggers: trigReg})
	var got []string
	for _, e := range res.Errors {
		if e.Code == "unresolved_template_param" && e.NodeID == "a" {
			got = append(got, e.Message)
		}
	}
	if len(got) != 2 || !strings.Contains(got[0], `"brokerId"`) || !strings.Contains(got[1], `"topic"`) {
		t.Fatalf("unresolved params = %v (all errors %+v)", got, res.Errors)
	}
}
