package templates

import (
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

func notificationPresetEntries() []templateEntry {
	channelParam := flows.ParamSchema{
		Key:         "channelIds",
		Type:        events.FieldStringList,
		Label:       "Notification channels",
		Description: "Destination notification channels (defaults to all enabled channels).",
		Default:     []string{"*"},
		Picker:      flows.PickerChannel,
	}

	return []templateEntry{
		// 1. Critical alerts
		{
			meta: flows.Template{
				ID:           "notify-critical-alerts",
				Name:         "Critical alerts",
				Description:  "Immediate alerts for critical server, storage, provider, indexer, database, and playback failures, bypassing channel digest delays.",
				Category:     "notifications",
				Icon:         "alert-triangle",
				NotifyPreset: "critical",
				Params: []flows.ParamSchema{
					channelParam,
					{Key: "cooldown", Type: events.FieldString, Label: "Dedupe cooldown", Description: "Suppress repeated identical alerts for this duration.", Default: "5m"},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 60, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				cooldown := strParam(params, "cooldown", "5m")
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Critical system & service events", 80, 160, flows.EventTriggerConfig{
							Events: []string{
								"system.server.crashed",
								"system.health.changed",
								"system.reachability.changed",
								"system.backup.failed",
								"system.disk.low",
								"provider.service.outage",
								"indexer.service.outage",
								"cache.budget.full",
								"playback.session.failed",
								"cluster.database.unavailable",
								"flows.flow.auto_disabled",
							},
						}),
						makeNode("dedupe", flows.NodeLogicDedupe, "Dedupe bursts", 380, 160, flows.DedupeNodeConfig{
							KeyTemplate: "{{ event.type }}:{{ payload.providerId | default payload.indexerId | default payload.component | default payload.flowId }}",
							Cooldown:    cooldown,
						}),
						makeNode("alert", flows.NodeAction, "Send critical alert", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds":   channels,
								"title":        "Critical alert: {{ event.type }}",
								"message":      "{{ payload.error | default payload.summary | default payload.reason | default \"A critical server event occurred.\" }}",
								"color":        "#EF4444",
								"priority":     1,
								"bypassDigest": true,
								"fields": map[string]any{
									"Event": "{{ event.type }}",
									"Node":  "{{ event.node | default \"primary\" }}",
								},
								"footer": "Ether Critical Alerts",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "dedupe"),
						makeEdge("e2", "dedupe", flows.HandlePass, "alert"),
					},
				}, nil
			},
		},

		// 2. Playback issues
		{
			meta: flows.Template{
				ID:           "notify-playback-issues",
				Name:         "Playback issues",
				Description:  "Notify when a viewer stream fails or falls back to an alternate release, with rich title and viewer context.",
				Category:     "notifications",
				Icon:         "play-circle",
				NotifyPreset: "playback",
				Params: []flows.ParamSchema{
					channelParam,
					{Key: "cooldown", Type: events.FieldString, Label: "Dedupe cooldown", Default: "3m"},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 60, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				cooldown := strParam(params, "cooldown", "3m")
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Playback failure or failover", 80, 160, flows.EventTriggerConfig{
							Events: []string{
								"playback.session.failed",
								"playback.source.failover",
							},
						}),
						makeNode("dedupe", flows.NodeLogicDedupe, "Avoid duplicate alerts", 380, 160, flows.DedupeNodeConfig{
							KeyTemplate: "{{ event.type }}:{{ payload.sessionId | default payload.titleKey }}",
							Cooldown:    cooldown,
						}),
						makeNode("alert", flows.NodeAction, "Notify playback issue", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "{{#if event.type == \"playback.source.failover\"}}Playback failover: {{ payload.titleName | default payload.titleKey }}{{#else}}Playback error: {{ payload.titleName | default payload.titleKey }}{{/if}}",
								"message":    "{{ payload.error | default payload.reason | default \"Playback encountered an issue.\" }}",
								"posterUrl":  "{{ payload.posterUrl }}",
								"color":      "#F59E0B",
								"fields": map[string]any{
									"Viewer": "{{ payload.viewer.username | default \"unknown\" }}",
									"Client": "{{ payload.client.name | default payload.client.product | default \"unknown\" }}",
								},
								"footer": "Ether Playback",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "dedupe"),
						makeEdge("e2", "dedupe", flows.HandlePass, "alert"),
					},
				}, nil
			},
		},

		// 3. Provider & indexer outages
		{
			meta: flows.Template{
				ID:           "notify-provider-outages",
				Name:         "Provider & indexer outages",
				Description:  "Alert when a Usenet/debrid provider or indexer goes down, recovers, or exhausts its daily budget.",
				Category:     "notifications",
				Icon:         "cloud-off",
				NotifyPreset: "providers",
				Params: []flows.ParamSchema{
					channelParam,
					{Key: "cooldown", Type: events.FieldString, Label: "Dedupe cooldown", Default: "5m"},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 60, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				cooldown := strParam(params, "cooldown", "5m")
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Provider & indexer health", 80, 160, flows.EventTriggerConfig{
							Events: []string{
								"provider.service.outage",
								"provider.service.recovered",
								"provider.auth.rejected",
								"indexer.service.outage",
								"indexer.service.recovered",
								"indexer.budget.exhausted",
							},
						}),
						makeNode("dedupe", flows.NodeLogicDedupe, "Dedupe per service", 380, 160, flows.DedupeNodeConfig{
							KeyTemplate: "{{ event.type }}:{{ payload.providerId | default payload.indexerId }}",
							Cooldown:    cooldown,
						}),
						makeNode("alert", flows.NodeAction, "Send service status alert", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "{{ payload.providerName | default payload.indexerName | default \"Service\" }}: {{ event.type }}",
								"message":    "{{ payload.error | default \"Service state changed.\" }}",
								"color":      "{{#if event.type contains \"recovered\"}}#10B981{{#else}}#EF4444{{/if}}",
								"footer":     "Ether Providers & Indexers",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "dedupe"),
						makeEdge("e2", "dedupe", flows.HandlePass, "alert"),
					},
				}, nil
			},
		},

		// 4. Updates & maintenance
		{
			meta: flows.Template{
				ID:           "notify-updates",
				Name:         "Updates & maintenance",
				Description:  "Stay informed when Ether updates are available, installed, rolled back, or when TLS certificates renew.",
				Category:     "notifications",
				Icon:         "arrow-up-circle",
				NotifyPreset: "updates",
				Params: []flows.ParamSchema{
					channelParam,
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 30, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Update & certificate events", 80, 160, flows.EventTriggerConfig{
							Events: []string{
								"system.update.available",
								"system.update.installed",
								"system.update.rolled_back",
								"auth.certificate.renewed",
							},
						}),
						makeNode("alert", flows.NodeAction, "Notify owner", 420, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "{{#if event.type == \"system.update.available\"}}Ether update {{ payload.newVersion }} available{{#else}}{{#if event.type == \"system.update.installed\"}}Ether updated to {{ payload.toVersion }}{{#else}}{{#if event.type == \"system.update.rolled_back\"}}Ether update rolled back to {{ payload.restoredVersion }}{{#else}}TLS certificate renewed{{/if}}{{/if}}{{/if}}",
								"message":    "{{ payload.reason | default payload.issuer | default \"Maintenance event completed.\" }}",
								"color":      "{{#if event.type == \"system.update.rolled_back\"}}#EF4444{{#else}}#3B82F6{{/if}}",
								"footer":     "Ether Maintenance",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "alert"),
					},
				}, nil
			},
		},

		// 5. Weekly newsletter
		{
			meta: flows.Template{
				ID:           "notify-weekly-newsletter",
				Name:         "Weekly newsletter",
				Description:  "Send a scheduled weekly HTML and plain-text digest combining recently added movies/shows (episodes grouped by season) with playback activity.",
				Category:     "notifications",
				Icon:         "newspaper",
				NotifyPreset: "newsletter",
				Params: []flows.ParamSchema{
					channelParam,
					{Key: "cron", Type: events.FieldString, Label: "Schedule (cron)", Description: "5-field cron expression (default Fridays at 17:00).", Default: "0 17 * * 5"},
					{Key: "window", Type: events.FieldString, Label: "Lookback window", Default: "7d"},
					{Key: "title", Type: events.FieldString, Label: "Newsletter title", Default: "Weekly Ether Digest"},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 10, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				cron := strParam(params, "cron", "0 17 * * 5")
				window := strParam(params, "window", "7d")
				title := strParam(params, "title", "Weekly Ether Digest")
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("sched", flows.NodeTriggerSchedule, "Weekly schedule", 80, 160, flows.ScheduleTriggerConfig{
							Cron: cron,
						}),
						makeNode("digest", flows.NodeAction, "Generate weekly digest", 380, 160, flows.ActionNodeConfig{
							ActionType: "digest.generate",
							Params: map[string]any{
								"window":          window,
								"kind":            "combined",
								"groupEpisodes":   true,
								"includeActivity": true,
								"title":           title,
							},
						}),
						makeNode("send", flows.NodeAction, "Deliver newsletter", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds":   channels,
								"title":        "{{ steps.digest.output.subject }}",
								"message":      "{{ steps.digest.output.text }}",
								"html":         "{{ steps.digest.output.html }}",
								"color":        "#6366F1",
								"bypassDigest": true,
								"footer":       "Ether Weekly Digest",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "sched", flows.HandleNext, "digest"),
						makeEdge("e2", "digest", flows.HandleNext, "send"),
					},
				}, nil
			},
		},
	}
}
