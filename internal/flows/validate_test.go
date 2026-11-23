package flows

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/events"
)

func testRegistries(t *testing.T) (*events.Registry, *InMemoryActionRegistry, *InMemoryTriggerRegistry) {
	t.Helper()
	evReg := events.NewRegistry()
	evReg.MustRegister(events.EventSchema{
		Type:  "playback.session.failed",
		Label: "Playback failed",
		Fields: []events.FieldSchema{
			{Name: "titleName", Type: events.FieldString, Label: "Title"},
			{Name: "bitrateKbps", Type: events.FieldInt, Label: "Bitrate", Unit: "kbps"},
			{Name: "delivery", Type: events.FieldEnum, Label: "Delivery", Enum: []events.EnumOption{{Value: "direct_play"}, {Value: "remux"}, {Value: "transcode"}}},
			{Name: "remote", Type: events.FieldBool, Label: "Remote"},
			{Name: "tags", Type: events.FieldStringList, Label: "Tags"},
		},
	})
	evReg.MustRegister(events.EventSchema{
		Type:       "playback.session.stopped",
		Label:      "Playback stopped",
		Deprecated: true,
		ReplacedBy: "playback.session.completed",
		Fields: []events.FieldSchema{
			{Name: "titleName", Type: events.FieldString, Label: "Title"},
		},
	})
	evReg.MustRegister(events.EventSchema{
		Type:  "livetv.input.recovered",
		Label: "Live TV input recovered",
		Fields: []events.FieldSchema{
			{Name: "channelId", Type: events.FieldString, Label: "Channel"},
		},
	})

	actReg := NewActionRegistry()
	actReg.MustRegister(ActionFunc{
		ActionSpec: ActionSpec{
			Type:       "notify.send",
			Group:      "notifications",
			Label:      "Send notification",
			SideEffect: true,
			Params: []ParamSchema{
				{Key: "channelId", Type: events.FieldString, Label: "Channel", Required: true, Picker: "channel"},
				{Key: "title", Type: events.FieldString, Label: "Title", Required: true, Template: true},
				{Key: "message", Type: events.FieldString, Label: "Message", Template: true},
				{Key: "severity", Type: events.FieldEnum, Label: "Severity", Default: "info", Enum: []events.EnumOption{{Value: "info"}, {Value: "warn"}, {Value: "critical"}}},
			},
		},
		Run: func(_ context.Context, _ ActionContext, _ map[string]any) (ActionResult, error) {
			return ActionResult{}, nil
		},
	})

	return evReg, actReg, NewTriggerRegistry()
}

func hasIssue(issues []ValidationIssue, code string) bool {
	for _, iss := range issues {
		if iss.Code == code {
			return true
		}
	}
	return false
}

func TestValidateValidRichFlow(t *testing.T) {
	evReg, actReg, trigReg := testRegistries(t)
	g := Graph{
		SchemaVersion: SchemaVersion,
		Nodes: []Node{
			{
				ID:   "trig1",
				Kind: NodeTriggerEvent,
				Config: MustMarshalConfig(EventTriggerConfig{
					Events: []string{"playback.session.failed"},
					Filter: &ConditionGroup{
						Op: LogicAnd,
						Rules: []ConditionRule{
							{Field: "payload.delivery", Op: OpEq, Value: "transcode"},
						},
					},
				}),
			},
			{
				ID:   "cond1",
				Kind: NodeCondition,
				Config: MustMarshalConfig(ConditionNodeConfig{
					Condition: ConditionGroup{
						Op: LogicAnd,
						Rules: []ConditionRule{
							{Field: "payload.bitrateKbps", Op: OpGte, Value: 10000},
							{Field: "payload.remote", Op: OpIsTrue},
						},
					},
				}),
			},
			{
				ID:   "win1",
				Kind: NodeLogicWindow,
				Config: MustMarshalConfig(WindowNodeConfig{
					KeyTemplate:  "{{ payload.titleName }}",
					Threshold:    3,
					Window:       "10m",
					ResetOnReach: true,
				}),
			},
			{
				ID:   "dedupe1",
				Kind: NodeLogicDedupe,
				Config: MustMarshalConfig(DedupeNodeConfig{
					KeyTemplate: "{{ correlation.titleId }}",
					Cooldown:    "30m",
				}),
			},
			{
				ID:   "delay1",
				Kind: NodeLogicDelay,
				Config: MustMarshalConfig(DelayNodeConfig{
					Duration: "5s",
				}),
			},
			{
				ID:   "wait1",
				Kind: NodeLogicWaitEvent,
				Config: MustMarshalConfig(WaitEventNodeConfig{
					EventTypes:       []string{"livetv.input.recovered"},
					MatchCorrelation: true,
					Timeout:          "2m",
				}),
			},
			{
				ID:   "act1",
				Kind: NodeAction,
				Config: MustMarshalConfig(ActionNodeConfig{
					ActionType: "notify.send",
					Params: map[string]any{
						"channelId": "ops-discord",
						"title":     "Repeated failures on {{ payload.titleName }}",
						"severity":  "critical",
					},
					Timeout: "15s",
				}),
			},
		},
		Edges: []Edge{
			{ID: "e1", Source: "trig1", Target: "cond1", SourceHandle: HandleNext},
			{ID: "e2", Source: "cond1", Target: "win1", SourceHandle: HandleTrue},
			{ID: "e3", Source: "win1", Target: "dedupe1", SourceHandle: HandleReached},
			{ID: "e4", Source: "dedupe1", Target: "delay1", SourceHandle: HandlePass},
			{ID: "e5", Source: "delay1", Target: "wait1", SourceHandle: HandleNext},
			{ID: "e6", Source: "wait1", Target: "act1", SourceHandle: HandleTimeout},
		},
	}

	res := ValidateFlow(Flow{Name: "Repeated playback failure alert", Graph: g}, ValidateOptions{
		Events:   evReg,
		Actions:  actReg,
		Triggers: trigReg,
	})
	if !res.Valid {
		t.Fatalf("expected valid flow, got errors: %+v", res.Errors)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected no warnings, got: %+v", res.Warnings)
	}

	patterns := g.SubscribedEventPatterns()
	if len(patterns) != 2 {
		t.Fatalf("SubscribedEventPatterns = %v, want 2 entries", patterns)
	}
}

func TestValidateGraphCatchesCyclesUnknownNodesAndHandles(t *testing.T) {
	evReg, actReg, trigReg := testRegistries(t)
	g := Graph{
		SchemaVersion: SchemaVersion,
		Nodes: []Node{
			{ID: "t1", Kind: NodeTriggerManual},
			{ID: "a", Kind: NodeLogicDelay, Config: MustMarshalConfig(DelayNodeConfig{Duration: "5s"})},
			{ID: "b", Kind: NodeLogicDelay, Config: MustMarshalConfig(DelayNodeConfig{Duration: "5s"})},
			{ID: "orphan", Kind: NodeLogicDelay, Config: MustMarshalConfig(DelayNodeConfig{Duration: "5s"})},
		},
		Edges: []Edge{
			{ID: "e1", Source: "t1", Target: "a"},
			{ID: "e2", Source: "a", Target: "b"},
			{ID: "e3", Source: "b", Target: "a"}, // cycle
			{ID: "e4", Source: "a", Target: "missing_node"},
			{ID: "e5", Source: "b", Target: "t1"},                    // edge into trigger
			{ID: "e6", Source: "a", Target: "a"},                     // self-loop
			{ID: "e7", Source: "a", Target: "b", SourceHandle: "no"}, // bad handle
		},
	}

	res := ValidateGraph(g, ValidateOptions{Events: evReg, Actions: actReg, Triggers: trigReg})
	if res.Valid {
		t.Fatal("expected invalid graph")
	}
	for _, wantCode := range []string{"cycle", "unknown_node", "trigger_target", "self_loop", "invalid_source_handle"} {
		if !hasIssue(res.Errors, wantCode) {
			t.Errorf("expected error code %q in %+v", wantCode, res.Errors)
		}
	}
	if !hasIssue(res.Warnings, "unreachable_node") {
		t.Errorf("expected unreachable_node warning in %+v", res.Warnings)
	}
}

func TestValidateSchemaTypingAndActionParams(t *testing.T) {
	evReg, actReg, trigReg := testRegistries(t)
	g := Graph{
		SchemaVersion: SchemaVersion,
		Nodes: []Node{
			{
				ID:   "t1",
				Kind: NodeTriggerEvent,
				Config: MustMarshalConfig(EventTriggerConfig{
					Events: []string{"playback.session.failed", "playback.session.stopped"},
				}),
			},
			{
				ID:   "c1",
				Kind: NodeCondition,
				Config: MustMarshalConfig(ConditionNodeConfig{
					Condition: ConditionGroup{
						Op: LogicAnd,
						Rules: []ConditionRule{
							{Field: "payload.nonexistent", Op: OpEq, Value: "x"},
							{Field: "payload.bitrateKbps", Op: OpMatches, Value: "[bad"},
							{Field: "payload.delivery", Op: OpEq, Value: "invalid_mode"},
						},
					},
				}),
			},
			{
				ID:   "act1",
				Kind: NodeAction,
				Config: MustMarshalConfig(ActionNodeConfig{
					ActionType: "notify.send",
					Params: map[string]any{
						// missing required channelId
						"title":   "Broken {{ unclosed",
						"unknown": 123,
					},
				}),
			},
		},
		Edges: []Edge{
			{ID: "e1", Source: "t1", Target: "c1"},
			{ID: "e2", Source: "c1", Target: "act1", SourceHandle: HandleTrue},
		},
	}

	res := ValidateGraph(g, ValidateOptions{Events: evReg, Actions: actReg, Triggers: trigReg})
	if res.Valid {
		t.Fatal("expected validation failure")
	}
	for _, code := range []string{"unknown_payload_field", "invalid_regex", "invalid_enum_value", "missing_required_param", "invalid_template", "unknown_param"} {
		if !hasIssue(res.Errors, code) {
			t.Errorf("expected error %q, got %+v", code, res.Errors)
		}
	}
	if !hasIssue(res.Warnings, "deprecated_event") {
		t.Errorf("expected deprecated_event warning, got %+v", res.Warnings)
	}
	if !hasIssue(res.Warnings, "partial_payload_field") {
		t.Errorf("expected partial_payload_field warning for fields only on playback.session.failed, got %+v", res.Warnings)
	}
}

func TestValidateScheduleCronAndSwitchHandles(t *testing.T) {
	good := Graph{
		SchemaVersion: SchemaVersion,
		Nodes: []Node{
			{
				ID:   "sched",
				Kind: NodeTriggerSchedule,
				Config: MustMarshalConfig(ScheduleTriggerConfig{
					Cron:     "*/15 8-22 * * 1-5",
					Timezone: "America/New_York",
				}),
			},
			{
				ID:   "sw",
				Kind: NodeLogicSwitch,
				Config: MustMarshalConfig(SwitchNodeConfig{
					Cases: []SwitchCase{
						{Key: "high", Condition: ConditionGroup{Rules: []ConditionRule{{Field: "vars.n", Op: OpGt, Value: 5}}}},
					},
				}),
			},
			{ID: "d1", Kind: NodeLogicDelay, Config: MustMarshalConfig(DelayNodeConfig{Duration: "1s"})},
			{ID: "d2", Kind: NodeLogicDelay, Config: MustMarshalConfig(DelayNodeConfig{Duration: "1s"})},
		},
		Edges: []Edge{
			{ID: "e1", Source: "sched", Target: "sw"},
			{ID: "e2", Source: "sw", Target: "d1", SourceHandle: "high"},
			{ID: "e3", Source: "sw", Target: "d2", SourceHandle: HandleDefault},
		},
	}
	if res := ValidateGraph(good, ValidateOptions{}); !res.Valid {
		t.Fatalf("expected valid schedule+switch flow, got %+v", res.Errors)
	}

	bad := Graph{
		SchemaVersion: SchemaVersion,
		Nodes: []Node{
			{
				ID:   "sched",
				Kind: NodeTriggerSchedule,
				Config: MustMarshalConfig(ScheduleTriggerConfig{
					Cron:     "99 * * * *",
					Timezone: "Mars/Olympus_Mons",
				}),
			},
		},
	}
	res := ValidateGraph(bad, ValidateOptions{})
	if !hasIssue(res.Errors, "invalid_cron") || !hasIssue(res.Errors, "invalid_timezone") {
		t.Fatalf("expected invalid_cron & invalid_timezone, got %+v", res.Errors)
	}
}

func TestActionAndTriggerRegistriesAndIdempotencyKey(t *testing.T) {
	actReg := NewActionRegistry()
	err := actReg.ReplacePlugin("com.acme.tracker", []ActionHandler{
		ActionFunc{
			ActionSpec: ActionSpec{
				Type:  PluginActionType("com.acme.tracker", "sync_now"),
				Group: "plugin",
				Label: "Sync tracker",
			},
		},
	})
	if err != nil {
		t.Fatalf("ReplacePlugin: %v", err)
	}
	if _, ok := actReg.Get("plugin:com.acme.tracker/sync_now"); !ok {
		t.Fatal("plugin action not found")
	}
	actReg.RemovePlugin("com.acme.tracker")
	if len(actReg.List()) != 0 {
		t.Fatalf("expected empty registry after RemovePlugin, got %d", len(actReg.List()))
	}

	trigReg := NewTriggerRegistry()
	if len(trigReg.List()) != 4 {
		t.Fatalf("expected 4 built-in triggers, got %d", len(trigReg.List()))
	}

	k1 := IdempotencyKey("run_1", "act_1", 1)
	k2 := IdempotencyKey("run_1", "act_1", 1)
	k3 := IdempotencyKey("run_1", "act_1", 2)
	if k1 != k2 || k1 == k3 || len(k1) < 10 {
		t.Fatalf("unexpected idempotency keys: %q %q %q", k1, k2, k3)
	}
}
