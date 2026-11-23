package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/notify"
)

// legacyNotifyToCanonical maps legacy internal/notify event kinds to their
// canonical v0.3.17 event bus types (docs/design/flows.md §3.2).
var legacyNotifyToCanonical = map[string]string{
	"server.started":     "system.server.started",
	"server.stopping":    "system.server.stopping",
	"server.crash":       "system.server.crashed",
	"provider.outage":    "provider.service.outage",
	"provider.recovered": "provider.service.recovered",
	"indexer.outage":     "indexer.service.outage",
	"indexer.recovered":  "indexer.service.recovered",
	"indexer.budget":     "indexer.budget.exhausted",
	"cache.full":         "cache.budget.full",
	"cache.recovered":    "cache.budget.recovered",
	"playback.start":     "playback.session.started",
	"playback.stop":      "playback.session.stopped",
	"playback.error":     "playback.session.failed",
	"playback.failover":  "playback.source.failover",
	"cert.renewed":       "auth.certificate.renewed",
	"update.available":   "system.update.available",
	"update.installed":   "system.update.installed",
	"update.rolledback":  "system.update.rolled_back",
	"backup.failed":      "system.backup.failed",
	"user.joined":        "viewer.account.joined",
	"title.available":    "library.title.available",
}

// DefaultFlowTemplates returns Ether's curated starter flow templates, covering
// the one-click notification presets and common operational automation recipes.
func DefaultFlowTemplates() []flows.Template {
	channelParam := flows.ParamSchema{
		Key:     "channelIds",
		Type:    events.FieldStringList,
		Label:   "Notification channels",
		Default: []string{"*"},
		Picker:  flows.PickerChannel,
	}

	return []flows.Template{
		// ---- Notification presets (Section 8) ----
		{
			ID:           "notify-critical-alerts",
			Name:         "Critical server alerts",
			Description:  "Notify immediately on provider/indexer outages, cache exhaustion, backup failures, update rollbacks, and playback errors.",
			Category:     "notifications",
			Icon:         "alert-triangle",
			NotifyPreset: "critical",
			Params: []flows.ParamSchema{
				channelParam,
				{
					Key:     "cooldown",
					Type:    events.FieldString,
					Label:   "Dedupe cooldown",
					Default: "5m",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Critical events",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"provider.service.outage",
								"indexer.service.outage",
								"cache.budget.full",
								"system.backup.failed",
								"system.update.rolled_back",
								"playback.session.failed",
							},
						}),
					},
					{
						ID:       "dedupe_1",
						Kind:     flows.NodeLogicDedupe,
						Label:    "Cooldown per event",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.DedupeNodeConfig{
							KeyTemplate: "{{ event.type }}",
							Cooldown:    "5m",
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Send alert",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds":   "{{ param.channelIds }}",
								"title":        "Critical alert: {{ event.type }}",
								"message":      "Event {{ event.type }} occurred on {{ event.node }}.",
								"color":        "#EF4444",
								"priority":     1,
								"bypassDigest": true,
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "dedupe_1"},
					{ID: "e2", Source: "dedupe_1", SourceHandle: flows.HandlePass, Target: "notify_1"},
				},
			},
		},
		{
			ID:           "notify-playback-issues",
			Name:         "Playback issues",
			Description:  "Alert when playback sessions fail or fall back to another source repeatedly within a sliding window.",
			Category:     "notifications",
			Icon:         "play-circle",
			NotifyPreset: "playback_issues",
			Params: []flows.ParamSchema{
				channelParam,
				{
					Key:     "threshold",
					Type:    events.FieldInt,
					Label:   "Failures before alerting",
					Default: 1,
				},
				{
					Key:     "window",
					Type:    events.FieldString,
					Label:   "Sliding window",
					Default: "10m",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Playback failure or failover",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"playback.session.failed",
								"playback.source.failover",
							},
						}),
					},
					{
						ID:       "window_1",
						Kind:     flows.NodeLogicWindow,
						Label:    "Failure threshold",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.WindowNodeConfig{
							KeyTemplate: "{{ payload.titleName }}",
							Threshold:   1,
							Window:      "10m",
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Send playback alert",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "Playback issue: {{ payload.titleName }}",
								"message":    "{{ event.type }} for {{ payload.viewerName }} ({{ payload.titleName }}).",
								"color":      "#F59E0B",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "window_1"},
					{ID: "e2", Source: "window_1", SourceHandle: flows.HandleReached, Target: "notify_1"},
				},
			},
		},
		{
			ID:           "notify-provider-outages",
			Name:         "Provider and indexer outages",
			Description:  "Notify whenever a Usenet, debrid, or indexer service enters an outage or recovers.",
			Category:     "notifications",
			Icon:         "cloud-off",
			NotifyPreset: "provider_outages",
			Params:       []flows.ParamSchema{channelParam},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Outage or recovery",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"provider.service.outage",
								"provider.service.recovered",
								"indexer.service.outage",
								"indexer.service.recovered",
							},
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Send status notice",
						Position: flows.Position{X: 420, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "Service status: {{ event.type }}",
								"message":    "Status changed to {{ event.type }} ({{ payload.name }}).",
								"color":      "#F59E0B",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "notify_1"},
				},
			},
		},
		{
			ID:           "notify-updates",
			Name:         "Server updates",
			Description:  "Notify when a new Ether release is available, installed, or rolled back.",
			Category:     "notifications",
			Icon:         "download",
			NotifyPreset: "updates",
			Params:       []flows.ParamSchema{channelParam},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Update events",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"system.update.available",
								"system.update.installed",
								"system.update.rolled_back",
							},
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Send update notice",
						Position: flows.Position{X: 420, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "Ether update: {{ event.type }}",
								"message":    "Version {{ payload.version }} ({{ event.type }}).",
								"color":      "#3B82F6",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "notify_1"},
				},
			},
		},
		{
			ID:           "notify-weekly-newsletter",
			Name:         "Weekly Recently Added newsletter",
			Description:  "Build and send a weekly digest of titles added over the last 7 days.",
			Category:     "notifications",
			Icon:         "mail",
			NotifyPreset: "weekly_newsletter",
			Params: []flows.ParamSchema{
				channelParam,
				{
					Key:     "cron",
					Type:    events.FieldString,
					Label:   "Schedule (cron)",
					Default: "0 17 * * 5",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerSchedule,
						Label:    "Every Friday at 17:00",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.ScheduleTriggerConfig{
							Cron: "0 17 * * 5",
						}),
					},
					{
						ID:       "digest_1",
						Kind:     flows.NodeAction,
						Label:    "Build weekly digest",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "digest.generate",
							Params: map[string]any{
								"window":        "7d",
								"groupEpisodes": true,
							},
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Deliver newsletter",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds":   "{{ param.channelIds }}",
								"title":        "Weekly Recently Added",
								"message":      "{{ steps.digest_1.output.text }}",
								"html":         "{{ steps.digest_1.output.html }}",
								"bypassDigest": true,
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "digest_1"},
					{ID: "e2", Source: "digest_1", SourceHandle: flows.HandleNext, Target: "notify_1"},
				},
			},
		},

		// ---- Power-user playback & operational templates (§4.6, §7) ----
		{
			ID:          "block-4k-remote-transcode",
			Name:        "Block 4K remote transcodes",
			Description: "Stop remote 4K video transcodes automatically and show the viewer a clear playback message.",
			Category:    "playback",
			Icon:        "shield-alert",
			Params: []flows.ParamSchema{
				{
					Key:     "minHeight",
					Type:    events.FieldInt,
					Label:   "Minimum video height (px)",
					Default: 2160,
				},
				{
					Key:     "message",
					Type:    events.FieldString,
					Label:   "Viewer message",
					Default: "4K transcoding is disabled on remote connections. Please select Original quality or a 1080p version.",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Session started or decision",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"playback.session.started",
								"playback.stream.decision",
							},
						}),
					},
					{
						ID:       "cond_1",
						Kind:     flows.NodeCondition,
						Label:    "Remote 4K transcode?",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.ConditionNodeConfig{
							Condition: flows.ConditionGroup{
								Rules: []flows.ConditionRule{
									{Field: "payload.stream.videoDecision", Op: flows.OpEq, Value: "transcode"},
									{Field: "payload.network.location", Op: flows.OpEq, Value: "wan"},
									{Field: "payload.source.videoHeight", Op: flows.OpGte, Value: 2160},
								},
							},
						}),
					},
					{
						ID:       "stop_1",
						Kind:     flows.NodeAction,
						Label:    "Stop session",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "playback.stop_session",
							Params: map[string]any{
								"sessionId": "{{ correlation.sessionId }}",
								"message":   "{{ param.message }}",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "cond_1"},
					{ID: "e2", Source: "cond_1", SourceHandle: flows.HandleTrue, Target: "stop_1"},
				},
			},
		},
		{
			ID:          "stop-idle-paused-stream",
			Name:        "Terminate idle paused streams",
			Description: "Wait after a session pauses; if it is not resumed or stopped within the timeout, terminate it.",
			Category:    "playback",
			Icon:        "pause-circle",
			Params: []flows.ParamSchema{
				{
					Key:     "timeout",
					Type:    events.FieldString,
					Label:   "Idle pause timeout",
					Default: "15m",
				},
				{
					Key:     "message",
					Type:    events.FieldString,
					Label:   "Termination message",
					Default: "Stream stopped after remaining paused.",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Playback paused",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{"playback.session.paused"},
						}),
					},
					{
						ID:       "wait_1",
						Kind:     flows.NodeLogicWaitEvent,
						Label:    "Wait for resume or stop",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.WaitEventNodeConfig{
							EventTypes:       []string{"playback.session.resumed", "playback.session.stopped"},
							MatchCorrelation: true,
							Timeout:          "15m",
						}),
					},
					{
						ID:       "stop_1",
						Kind:     flows.NodeAction,
						Label:    "Stop paused stream",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "playback.stop_session",
							Params: map[string]any{
								"sessionId": "{{ correlation.sessionId }}",
								"message":   "{{ param.message }}",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "wait_1"},
					{ID: "e2", Source: "wait_1", SourceHandle: flows.HandleTimeout, Target: "stop_1"},
				},
			},
		},
		{
			ID:          "relieve-buffering-transcode",
			Name:        "Buffering relief for transcode streams",
			Description: "When a session stalls repeatedly within a short window, notify the owner or stop the stalled stream.",
			Category:    "playback",
			Icon:        "activity",
			Params: []flows.ParamSchema{
				{
					Key:     "threshold",
					Type:    events.FieldInt,
					Label:   "Stall count",
					Default: 3,
				},
				{
					Key:     "window",
					Type:    events.FieldString,
					Label:   "Sliding window",
					Default: "2m",
				},
				channelParam,
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Stream stalled",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{"playback.stream.stalled"},
						}),
					},
					{
						ID:       "window_1",
						Kind:     flows.NodeLogicWindow,
						Label:    "3 stalls in 2m",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.WindowNodeConfig{
							KeyTemplate: "{{ correlation.sessionId }}",
							Threshold:   3,
							Window:      "2m",
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Alert owner",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "Repeated buffering: {{ payload.titleName }}",
								"message":    "Session {{ correlation.sessionId }} stalled repeatedly for {{ payload.viewerName }}.",
								"color":      "#F59E0B",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "window_1"},
					{ID: "e2", Source: "window_1", SourceHandle: flows.HandleReached, Target: "notify_1"},
				},
			},
		},
		{
			ID:          "concurrent-stream-guard",
			Name:        "Concurrent stream and new-IP alert",
			Description: "Alert with deduplication when a viewer exceeds stream limits or signs in from a new IP address.",
			Category:    "security",
			Icon:        "shield",
			Params: []flows.ParamSchema{
				channelParam,
				{
					Key:     "cooldown",
					Type:    events.FieldString,
					Label:   "Alert cooldown",
					Default: "15m",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Concurrency or new IP",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"playback.concurrency.exceeded",
								"auth.session.new_ip",
							},
						}),
					},
					{
						ID:       "dedupe_1",
						Kind:     flows.NodeLogicDedupe,
						Label:    "Dedupe per viewer",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.DedupeNodeConfig{
							KeyTemplate: "{{ event.type }}:{{ correlation.viewerId }}",
							Cooldown:    "15m",
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Send security alert",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "Account alert: {{ event.type }}",
								"message":    "Viewer {{ payload.viewerName }} triggered {{ event.type }}.",
								"color":      "#F59E0B",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "dedupe_1"},
					{ID: "e2", Source: "dedupe_1", SourceHandle: flows.HandlePass, Target: "notify_1"},
				},
			},
		},
		{
			ID:          "home-assistant-cinema-lighting",
			Name:        "Home Assistant / MQTT cinema lighting",
			Description: "Publish lighting scene transitions over MQTT when playback starts, pauses, or reaches end credits.",
			Category:    "smart-home",
			Icon:        "lightbulb",
			Params: []flows.ParamSchema{
				{
					Key:      "brokerId",
					Type:     events.FieldString,
					Label:    "MQTT broker",
					Required: true,
					Picker:   flows.PickerChannel,
				},
				{
					Key:     "topic",
					Type:    events.FieldString,
					Label:   "MQTT topic",
					Default: "home/theater/lights/scene",
				},
				{
					Key:     "clientName",
					Type:    events.FieldString,
					Label:   "Player device name",
					Default: "Living Room",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Playback state change",
						Position: flows.Position{X: 80, Y: 140},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"playback.session.started",
								"playback.session.paused",
								"playback.marker.entered",
							},
						}),
					},
					{
						ID:       "switch_1",
						Kind:     flows.NodeLogicSwitch,
						Label:    "Route by event",
						Position: flows.Position{X: 380, Y: 140},
						Config: flows.MustMarshalConfig(flows.SwitchNodeConfig{
							Cases: []flows.SwitchCase{
								{
									Key:   "playing",
									Label: "Started",
									Condition: flows.ConditionGroup{
										Rules: []flows.ConditionRule{
											{Field: "event.type", Op: flows.OpEq, Value: "playback.session.started"},
										},
									},
								},
								{
									Key:   "paused",
									Label: "Paused",
									Condition: flows.ConditionGroup{
										Rules: []flows.ConditionRule{
											{Field: "event.type", Op: flows.OpEq, Value: "playback.session.paused"},
										},
									},
								},
							},
						}),
					},
					{
						ID:       "mqtt_dim",
						Kind:     flows.NodeAction,
						Label:    "Dim lights",
						Position: flows.Position{X: 700, Y: 60},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "mqtt.publish",
							Params: map[string]any{
								"brokerId": "{{ param.brokerId }}",
								"topic":    "{{ param.topic }}",
								"payload":  `{"scene":"cinema","client":"{{ param.clientName }}"}`,
							},
						}),
					},
					{
						ID:       "mqtt_bright",
						Kind:     flows.NodeAction,
						Label:    "Restore lights",
						Position: flows.Position{X: 700, Y: 220},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "mqtt.publish",
							Params: map[string]any{
								"brokerId": "{{ param.brokerId }}",
								"topic":    "{{ param.topic }}",
								"payload":  `{"scene":"intermission","client":"{{ param.clientName }}"}`,
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "switch_1"},
					{ID: "e2", Source: "switch_1", SourceHandle: "playing", Target: "mqtt_dim"},
					{ID: "e3", Source: "switch_1", SourceHandle: "paused", Target: "mqtt_bright"},
					{ID: "e4", Source: "switch_1", SourceHandle: flows.HandleDefault, Target: "mqtt_bright"},
				},
			},
		},
		{
			ID:          "scheduled-library-export",
			Name:        "Scheduled library export",
			Description: "Export library metadata on a schedule to JSON or CSV and optionally notify when complete.",
			Category:    "library",
			Icon:        "file-output",
			Params: []flows.ParamSchema{
				{
					Key:     "cron",
					Type:    events.FieldString,
					Label:   "Schedule (cron)",
					Default: "0 3 * * 0",
				},
				{
					Key:     "format",
					Type:    events.FieldEnum,
					Label:   "Export format",
					Default: "json",
					Enum: []events.EnumOption{
						{Value: "json", Label: "JSON"},
						{Value: "csv", Label: "CSV"},
					},
				},
				{
					Key:     "scope",
					Type:    events.FieldEnum,
					Label:   "Library scope",
					Default: "server",
					Enum: []events.EnumOption{
						{Value: "server", Label: "Entire server"},
						{Value: "library", Label: "Library"},
						{Value: "collection", Label: "Collection"},
						{Value: "playlist", Label: "Playlist"},
						{Value: "item", Label: "Single item"},
					},
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerSchedule,
						Label:    "Weekly at 03:00",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.ScheduleTriggerConfig{
							Cron: "0 3 * * 0",
						}),
					},
					{
						ID:       "export_1",
						Kind:     flows.NodeAction,
						Label:    "Export library",
						Position: flows.Position{X: 420, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "library.export",
							Params: map[string]any{
								"format": "{{ param.format }}",
								"scope":  "{{ param.scope }}",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "export_1"},
				},
			},
		},
		{
			ID:          "auto-unpin-dead-pin",
			Name:        "Auto-unpin unhealthy release pin",
			Description: "Automatically unpin a pinned release when it becomes unhealthy and notify the owner.",
			Category:    "resolver",
			Icon:        "pin-off",
			Params:      []flows.ParamSchema{channelParam},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Pin unhealthy",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{"resolver.pin.unhealthy"},
						}),
					},
					{
						ID:       "unpin_1",
						Kind:     flows.NodeAction,
						Label:    "Unpin release",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "release.unpin",
							Params: map[string]any{
								"titleKey": "{{ payload.titleKey }}",
							},
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Notify owner",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "Unpinned unhealthy release",
								"message":    "Automatically unpinned unhealthy release for {{ payload.titleKey }}.",
								"color":      "#F59E0B",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "unpin_1"},
					{ID: "e2", Source: "unpin_1", SourceHandle: flows.HandleNext, Target: "notify_1"},
				},
			},
		},
		{
			ID:          "new-device-alert",
			Name:        "New viewer device alert",
			Description: "Notify when a viewer signs in on a brand-new device for the first time.",
			Category:    "security",
			Icon:        "smartphone",
			Params:      []flows.ParamSchema{channelParam},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Device registered",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{"auth.device.registered"},
						}),
					},
					{
						ID:       "cond_1",
						Kind:     flows.NodeCondition,
						Label:    "First seen for viewer?",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.ConditionNodeConfig{
							Condition: flows.ConditionGroup{
								Rules: []flows.ConditionRule{
									{Field: "payload.isFirstSeenForViewer", Op: flows.OpIsTrue},
								},
							},
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Send alert",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "New device: {{ payload.deviceName }}",
								"message":    "{{ payload.viewerName }} signed in on a new device ({{ payload.deviceName }}).",
								"color":      "#3B82F6",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "cond_1"},
					{ID: "e2", Source: "cond_1", SourceHandle: flows.HandleTrue, Target: "notify_1"},
				},
			},
		},
		{
			ID:          "livetv-failover-alert",
			Name:        "Live TV input lost and failover alert",
			Description: "Alert when a Live TV channel loses its input or fails over to a backup source.",
			Category:    "livetv",
			Icon:        "tv",
			Params: []flows.ParamSchema{
				channelParam,
				{
					Key:     "cooldown",
					Type:    events.FieldString,
					Label:   "Alert cooldown",
					Default: "5m",
				},
			},
			Graph: flows.Graph{
				SchemaVersion: flows.SchemaVersion,
				Nodes: []flows.Node{
					{
						ID:       "trigger_1",
						Kind:     flows.NodeTriggerEvent,
						Label:    "Live TV failover or input lost",
						Position: flows.Position{X: 80, Y: 120},
						Config: flows.MustMarshalConfig(flows.EventTriggerConfig{
							Events: []string{
								"livetv.input.lost",
								"livetv.source.failover",
							},
						}),
					},
					{
						ID:       "dedupe_1",
						Kind:     flows.NodeLogicDedupe,
						Label:    "Cooldown per channel",
						Position: flows.Position{X: 380, Y: 120},
						Config: flows.MustMarshalConfig(flows.DedupeNodeConfig{
							KeyTemplate: "{{ event.type }}:{{ correlation.channelId }}",
							Cooldown:    "5m",
						}),
					},
					{
						ID:       "notify_1",
						Kind:     flows.NodeAction,
						Label:    "Send Live TV alert",
						Position: flows.Position{X: 680, Y: 120},
						Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": "{{ param.channelIds }}",
								"title":      "Live TV: {{ event.type }}",
								"message":    "Channel {{ correlation.channelId }} triggered {{ event.type }}.",
								"color":      "#F59E0B",
							},
						}),
					},
				},
				Edges: []flows.Edge{
					{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "dedupe_1"},
					{ID: "e2", Source: "dedupe_1", SourceHandle: flows.HandlePass, Target: "notify_1"},
				},
			},
		},
	}
}

type fromTemplateRequest struct {
	Name        string           `json:"name,omitempty"`
	Description string           `json:"description,omitempty"`
	Enabled     *bool            `json:"enabled,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	Limits      flows.FlowLimits `json:"limits,omitempty"`
	Params      map[string]any   `json:"params,omitempty"`
}

var templateParamPlaceholderRe = regexp.MustCompile(`\{\{\s*param\.([A-Za-z0-9_.-]+)\s*\}\}`)

// InstantiateFlowTemplate validates params against tmpl.Params and materializes
// a concrete Flow ready for validation and persistence.
func InstantiateFlowTemplate(tmpl flows.Template, name, description string, enabled *bool, params map[string]any) (flows.Flow, []FieldError) {
	return instantiateFlowTemplate(tmpl, fromTemplateRequest{
		Name:        name,
		Description: description,
		Enabled:     enabled,
		Params:      params,
	})
}

// instantiateFlowTemplate validates params against tmpl.Params and materializes
// a concrete Flow ready for validation and persistence.
func instantiateFlowTemplate(tmpl flows.Template, req fromTemplateRequest) (flows.Flow, []FieldError) {
	resolved := make(map[string]any, len(tmpl.Params))
	var errs []FieldError

	for _, p := range tmpl.Params {
		val, provided := req.Params[p.Key]
		if !provided || isEmptyParamValue(val) {
			if p.Default != nil {
				val = p.Default
			} else if p.Required {
				errs = append(errs, FieldError{
					Field:   "params." + p.Key,
					Message: fmt.Sprintf("parameter %q is required", p.Key),
				})
				continue
			} else {
				continue
			}
		}
		coerced, err := coerceTemplateParam(p, val)
		if err != nil {
			errs = append(errs, FieldError{
				Field:   "params." + p.Key,
				Message: err.Error(),
			})
			continue
		}
		resolved[p.Key] = coerced
	}
	if len(errs) > 0 {
		return flows.Flow{}, errs
	}

	graph := cloneGraph(tmpl.Graph)
	if graph.SchemaVersion == 0 {
		graph.SchemaVersion = flows.SchemaVersion
	}

	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if len(n.Config) == 0 {
			continue
		}
		var raw any
		if err := json.Unmarshal(n.Config, &raw); err != nil {
			continue
		}
		replaced := substituteParamPlaceholders(raw, resolved)
		replaced = applyTypedTemplateOverrides(n.Kind, replaced, resolved)
		if b, err := json.Marshal(replaced); err == nil {
			n.Config = b
		}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = tmpl.Name
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		desc = tmpl.Description
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	tags := req.Tags

	return flows.Flow{
		Name:        name,
		Description: desc,
		Enabled:     enabled,
		Graph:       graph,
		Limits:      req.Limits,
		TemplateID:  tmpl.ID,
		Tags:        tags,
	}, nil
}

func isEmptyParamValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		for _, item := range x {
			if s, ok := item.(string); !ok || strings.TrimSpace(s) != "" {
				return false
			}
		}
		return true
	case []string:
		for _, s := range x {
			if strings.TrimSpace(s) != "" {
				return false
			}
		}
		return true
	}
	return false
}

func coerceTemplateParam(p flows.ParamSchema, v any) (any, error) {
	switch p.Type {
	case events.FieldString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a string", p.Key)
		}
		return s, nil
	case events.FieldEnum:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a string", p.Key)
		}
		if len(p.Enum) > 0 {
			valid := false
			for _, opt := range p.Enum {
				if opt.Value == s {
					valid = true
					break
				}
			}
			if !valid {
				return nil, fmt.Errorf("parameter %q value %q is not an allowed option", p.Key, s)
			}
		}
		return s, nil
	case events.FieldBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("parameter %q must be a boolean", p.Key)
		}
		return b, nil
	case events.FieldInt, events.FieldDurationMS:
		switch n := v.(type) {
		case int:
			return n, nil
		case int64:
			return int(n), nil
		case float64:
			if n != float64(int64(n)) {
				return nil, fmt.Errorf("parameter %q must be an integer", p.Key)
			}
			return int(n), nil
		default:
			return nil, fmt.Errorf("parameter %q must be an integer", p.Key)
		}
	case events.FieldFloat:
		switch n := v.(type) {
		case int:
			return float64(n), nil
		case int64:
			return float64(n), nil
		case float64:
			return n, nil
		default:
			return nil, fmt.Errorf("parameter %q must be a number", p.Key)
		}
	case events.FieldStringList:
		switch list := v.(type) {
		case []string:
			return list, nil
		case []any:
			out := make([]string, 0, len(list))
			for _, item := range list {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("parameter %q must be a list of strings", p.Key)
				}
				if strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
			if p.Required && len(out) == 0 {
				return nil, fmt.Errorf("parameter %q requires at least one entry", p.Key)
			}
			return out, nil
		case string:
			// Convenience: allow a single string to become a 1-element list.
			if strings.TrimSpace(list) == "" {
				return []string{}, nil
			}
			return []string{list}, nil
		default:
			return nil, fmt.Errorf("parameter %q must be a list of strings", p.Key)
		}
	default:
		return v, nil
	}
}

func substituteParamPlaceholders(v any, params map[string]any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = substituteParamPlaceholders(val, params)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = substituteParamPlaceholders(item, params)
		}
		return out
	case string:
		trimmed := strings.TrimSpace(x)
		if m := templateParamPlaceholderRe.FindStringSubmatch(trimmed); m != nil && m[0] == trimmed {
			if val, ok := params[m[1]]; ok {
				return val
			}
		}
		return templateParamPlaceholderRe.ReplaceAllStringFunc(x, func(match string) string {
			sub := templateParamPlaceholderRe.FindStringSubmatch(match)
			if len(sub) < 2 {
				return match
			}
			val, ok := params[sub[1]]
			if !ok {
				return match
			}
			return formatParamString(val)
		})
	default:
		return v
	}
}

func formatParamString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []string:
		return strings.Join(x, ",")
	default:
		return fmt.Sprint(x)
	}
}

// applyTypedTemplateOverrides applies well-known typed template parameters
// (cron, timeout, window, threshold, cooldown, minHeight) directly onto
// schedule/logic/condition node configs while keeping the base template graph
// itself 100% valid before instantiation.
func applyTypedTemplateOverrides(kind flows.NodeKind, cfgAny any, params map[string]any) any {
	m, ok := cfgAny.(map[string]any)
	if !ok {
		return cfgAny
	}
	switch kind {
	case flows.NodeTriggerSchedule:
		if s, ok := params["cron"].(string); ok && s != "" {
			m["cron"] = s
		}
		if s, ok := params["every"].(string); ok && s != "" {
			m["every"] = s
		}
	case flows.NodeLogicWaitEvent:
		if s, ok := params["timeout"].(string); ok && s != "" {
			m["timeout"] = s
		}
	case flows.NodeLogicWindow:
		if s, ok := params["window"].(string); ok && s != "" {
			m["window"] = s
		}
		if n, ok := params["threshold"].(int); ok && n > 0 {
			m["threshold"] = n
		}
	case flows.NodeLogicDedupe:
		if s, ok := params["cooldown"].(string); ok && s != "" {
			m["cooldown"] = s
		}
	case flows.NodeCondition:
		if n, ok := params["minHeight"].(int); ok && n > 0 {
			if cond, ok := m["condition"].(map[string]any); ok {
				if rules, ok := cond["rules"].([]any); ok {
					for _, r := range rules {
						if rm, ok := r.(map[string]any); ok &&
							(rm["field"] == "payload.source.videoHeight" || rm["field"] == "payload.videoHeight") {
							rm["value"] = n
						}
					}
				}
			}
		}
	}
	return m
}

func cloneGraph(g flows.Graph) flows.Graph {
	raw, err := json.Marshal(g)
	if err != nil {
		return g
	}
	var out flows.Graph
	if err := json.Unmarshal(raw, &out); err != nil {
		return g
	}
	return out
}

// ---- "Customize as Flow" for existing legacy notification rules (§8) ----

type customizeNotificationRequest struct {
	ChannelID        string   `json:"channelId"`
	Event            string   `json:"event,omitempty"`
	Events           []string `json:"events,omitempty"`
	Name             string   `json:"name,omitempty"`
	Description      string   `json:"description,omitempty"`
	Enabled          *bool    `json:"enabled,omitempty"`
	RemoveLegacyRule *bool    `json:"removeLegacyRule,omitempty"` // default true
}

var (
	errCrashWatcherOnly = errors.New("server.crash is delivered by the out-of-process crash watcher and cannot be moved exclusively into an in-process flow")
	errNoMatchingRules  = errors.New("channel does not have any matching legacy notification rules to customize")
)

func resolveLegacyNotifyEvent(legacyKind string, reg *events.Registry) (string, bool) {
	if reg != nil {
		if s, ok := reg.GetByLegacy(legacyKind); ok {
			return s.Type, true
		}
	}
	if canonical, ok := legacyNotifyToCanonical[legacyKind]; ok {
		return canonical, true
	}
	return "", false
}

func allLegacyNotifyKinds() []string {
	return append([]string(nil), notify.Kinds...)
}

// buildFlowFromNotification converts one or more legacy rules on ch into a
// concrete editable Flow and returns the updated notify.Channel with those
// legacy rules removed (preserving server.crash for the out-of-process watcher).
func buildFlowFromNotification(
	ch notify.Channel,
	req customizeNotificationRequest,
	reg *events.Registry,
) (flow flows.Flow, updated notify.Channel, migrated []string, kept []string, notes []string, err error) {
	var requested []string
	if e := strings.TrimSpace(req.Event); e != "" {
		requested = append(requested, e)
	}
	for _, e := range req.Events {
		if s := strings.TrimSpace(e); s != "" && !slices.Contains(requested, s) {
			requested = append(requested, s)
		}
	}
	if len(requested) == 0 {
		requested = append([]string(nil), ch.Events...)
	}
	if len(requested) == 0 {
		return flows.Flow{}, ch, nil, nil, nil, errNoMatchingRules
	}

	channelHasWildcard := slices.Contains(ch.Events, "*")
	// Expand requested "*" to all known legacy event kinds.
	var expandedRequested []string
	for _, r := range requested {
		if r == "*" {
			for _, k := range allLegacyNotifyKinds() {
				if !slices.Contains(expandedRequested, k) {
					expandedRequested = append(expandedRequested, k)
				}
			}
			continue
		}
		if !channelHasWildcard && !slices.Contains(ch.Events, r) {
			return flows.Flow{}, ch, nil, nil, nil, fmt.Errorf("channel %q is not subscribed to legacy rule %q", ch.Name, r)
		}
		if !slices.Contains(expandedRequested, r) {
			expandedRequested = append(expandedRequested, r)
		}
	}

	// Filter out server.crash (crash watcher exception) and detect weekly newsletter.
	var canonicalEvents []string
	isNewsletter := false
	hasCrash := false
	for _, k := range expandedRequested {
		if k == "server.crash" {
			hasCrash = true
			notes = append(notes, "server.crash remains on the notification channel because it is delivered by the out-of-process crash watcher.")
			continue
		}
		if k == "newsletter.weekly" {
			isNewsletter = true
			migrated = append(migrated, k)
			continue
		}
		canonical, ok := resolveLegacyNotifyEvent(k, reg)
		if !ok {
			return flows.Flow{}, ch, nil, nil, nil, fmt.Errorf("unknown legacy notification event %q", k)
		}
		migrated = append(migrated, k)
		if !slices.Contains(canonicalEvents, canonical) {
			canonicalEvents = append(canonicalEvents, canonical)
		}
	}

	if len(migrated) == 0 {
		if hasCrash {
			return flows.Flow{}, ch, nil, ch.Events, notes, errCrashWatcherOnly
		}
		return flows.Flow{}, ch, nil, ch.Events, notes, errNoMatchingRules
	}

	// Build updated channel event list removing migrated rules while keeping
	// everything else (expanding "*" if only a subset was migrated).
	removeLegacy := true
	if req.RemoveLegacyRule != nil {
		removeLegacy = *req.RemoveLegacyRule
	}
	updated = ch
	if removeLegacy {
		if channelHasWildcard {
			for _, k := range allLegacyNotifyKinds() {
				if !slices.Contains(migrated, k) {
					kept = append(kept, k)
				}
			}
		} else {
			for _, k := range ch.Events {
				if !slices.Contains(migrated, k) {
					kept = append(kept, k)
				}
			}
		}
		updated.Events = kept
	} else {
		kept = append([]string(nil), ch.Events...)
	}

	// Build the Flow graph.
	var graph flows.Graph
	bypassDigest := ch.DigestMinutes <= 0
	if isNewsletter && len(canonicalEvents) == 0 {
		graph = flows.Graph{
			SchemaVersion: flows.SchemaVersion,
			Nodes: []flows.Node{
				{
					ID:       "trigger_1",
					Kind:     flows.NodeTriggerSchedule,
					Label:    "Weekly schedule",
					Position: flows.Position{X: 80, Y: 120},
					Config:   flows.MustMarshalConfig(flows.ScheduleTriggerConfig{Cron: "0 17 * * 5"}),
				},
				{
					ID:       "digest_1",
					Kind:     flows.NodeAction,
					Label:    "Generate weekly digest",
					Position: flows.Position{X: 380, Y: 120},
					Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
						ActionType: "digest.generate",
						Params: map[string]any{
							"window":        "7d",
							"groupEpisodes": true,
						},
					}),
				},
				{
					ID:       "notify_1",
					Kind:     flows.NodeAction,
					Label:    "Notify " + ch.Name,
					Position: flows.Position{X: 680, Y: 120},
					Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
						ActionType: "notify.send",
						Params: map[string]any{
							"channelIds":   []string{ch.ID},
							"title":        "Weekly Recently Added",
							"message":      "{{ steps.digest_1.output.text }}",
							"html":         "{{ steps.digest_1.output.html }}",
							"bypassDigest": true,
						},
					}),
				},
			},
			Edges: []flows.Edge{
				{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "digest_1"},
				{ID: "e2", Source: "digest_1", SourceHandle: flows.HandleNext, Target: "notify_1"},
			},
		}
	} else {
		if isNewsletter {
			notes = append(notes, "newsletter.weekly uses a weekly schedule and can be customized separately via the Weekly Recently Added template.")
			migrated = slices.DeleteFunc(migrated, func(s string) bool { return s == "newsletter.weekly" })
			if removeLegacy && !slices.Contains(kept, "newsletter.weekly") {
				kept = append(kept, "newsletter.weekly")
				updated.Events = kept
			}
		}
		graph = flows.Graph{
			SchemaVersion: flows.SchemaVersion,
			Nodes: []flows.Node{
				{
					ID:       "trigger_1",
					Kind:     flows.NodeTriggerEvent,
					Label:    "Notification events",
					Position: flows.Position{X: 80, Y: 120},
					Config:   flows.MustMarshalConfig(flows.EventTriggerConfig{Events: canonicalEvents}),
				},
				{
					ID:       "notify_1",
					Kind:     flows.NodeAction,
					Label:    "Notify " + ch.Name,
					Position: flows.Position{X: 420, Y: 120},
					Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
						ActionType: "notify.send",
						Params: map[string]any{
							"channelIds":   []string{ch.ID},
							"title":        "Ether alert: {{ event.type }}",
							"message":      "Event {{ event.type }} on {{ event.node }}.",
							"bypassDigest": bypassDigest,
						},
					}),
				},
			},
			Edges: []flows.Edge{
				{ID: "e1", Source: "trigger_1", SourceHandle: flows.HandleNext, Target: "notify_1"},
			},
		}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		if len(migrated) == 1 {
			name = fmt.Sprintf("%s: %s", ch.Name, migrated[0])
		} else {
			name = fmt.Sprintf("%s notifications", ch.Name)
		}
		if len(name) > 120 {
			name = name[:120]
		}
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		desc = fmt.Sprintf("Customized from notification channel %q (%s).", ch.Name, strings.Join(migrated, ", "))
	}
	enabled := ch.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	var limits flows.FlowLimits
	if ch.PerMinute > 0 && ch.PerMinute <= flows.HardRateLimitPerMin {
		limits.RateLimitPerMin = ch.PerMinute
	}

	flow = flows.Flow{
		Name:        name,
		Description: desc,
		Enabled:     enabled,
		Graph:       graph,
		Limits:      limits,
	}
	return flow, updated, migrated, kept, notes, nil
}
