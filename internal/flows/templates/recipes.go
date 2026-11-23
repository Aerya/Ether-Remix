package templates

import (
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

func jbopsRecipeEntries() []templateEntry {
	channelParam := flows.ParamSchema{
		Key:         "channelIds",
		Type:        events.FieldStringList,
		Label:       "Alert channels",
		Description: "Notification channels to inform when this rule acts.",
		Default:     []string{"*"},
		Picker:      flows.PickerChannel,
	}

	return []templateEntry{
		// 6. Kill remote video transcodes (kill_stream.py)
		{
			meta: flows.Template{
				ID:          "jbops-kill-remote-video-transcodes",
				Name:        "Stop remote video transcodes",
				Description: "Automatically terminate remote (WAN) video transcodes above a chosen resolution threshold (default 4K/2160p) while exempting VIP viewers, displaying a helpful message on the viewer's player.",
				Category:    "playback",
				Icon:        "shield-ban",
				Params: []flows.ParamSchema{
					{Key: "minVideoHeight", Type: events.FieldInt, Label: "Minimum source height (px)", Description: "Set to 2160 to block 4K remote transcodes, or 0 to block all remote video transcodes.", Default: 2160},
					{Key: "exemptTag", Type: events.FieldString, Label: "Exempt viewer tag", Description: "Viewers with this tag are never stopped.", Default: "vip"},
					{Key: "viewerMessage", Type: events.FieldString, Label: "Message shown to viewer", Default: "Remote 4K video transcoding is disabled on this server. Please play in Original quality or choose the 1080p version."},
					channelParam,
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 120, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				minHeight := intParam(params, "minVideoHeight", 2160)
				exemptTag := strParam(params, "exemptTag", "vip")
				msg := strParam(params, "viewerMessage", "Remote 4K video transcoding is disabled on this server. Please play in Original quality or choose the 1080p version.")
				channels := listParam(params, "channelIds", []string{"*"})
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Playback started or decision changed", 80, 160, flows.EventTriggerConfig{
							Events: []string{"playback.session.started", "playback.stream.decision"},
						}),
						makeNode("check", flows.NodeCondition, "Remote video transcode?", 360, 160, flows.ConditionNodeConfig{
							Condition: flows.ConditionGroup{
								Op: flows.LogicAnd,
								Rules: []flows.ConditionRule{
									{Field: "payload.stream.videoDecision", Op: flows.OpEq, Value: "transcode"},
									{Field: "payload.source.videoHeight", Op: flows.OpGte, Value: minHeight},
									{Field: "payload.network.location", Op: flows.OpEq, Value: "wan"},
									{Field: "payload.viewer.tags", Op: flows.OpNotContains, Value: exemptTag},
								},
							},
						}),
						makeNode("stop", flows.NodeAction, "Stop transcode session", 660, 120, flows.ActionNodeConfig{
							ActionType: "playback.stop_session",
							Params: map[string]any{
								"selector":  "explicit",
								"sessionId": "{{ payload.sessionId }}",
								"message":   msg,
							},
						}),
						makeNode("alert", flows.NodeAction, "Notify owner", 960, 120, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "Stopped remote transcode",
								"message":    "Stopped {{ payload.viewer.username | default \"viewer\" }} transcoding {{ payload.titleName }} ({{ payload.source.videoResolution | upper }}).",
								"posterUrl":  "{{ payload.posterUrl }}",
								"color":      "#F59E0B",
								"fields": map[string]any{
									"Viewer": "{{ payload.viewer.username | default \"unknown\" }}",
									"Client": "{{ payload.client.name | default payload.client.product }}",
									"Source": "{{ payload.source.videoResolution | upper }} {{ payload.source.videoDynamicRange | upper }}",
								},
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "check"),
						makeEdge("e2", "check", flows.HandleTrue, "stop"),
						makeEdge("e3", "stop", flows.HandleNext, "alert"),
					},
				}, nil
			},
		},

		// 7. Kill paused streams idle over N minutes (kill_stream.py --paused)
		{
			meta: flows.Template{
				ID:          "jbops-kill-idle-paused",
				Name:        "Stop idle paused streams",
				Description: "Wait for a paused stream to resume or stop; if it stays paused past your timeout (default 15m), terminate it automatically with a polite message.",
				Category:    "playback",
				Icon:        "pause-circle",
				Params: []flows.ParamSchema{
					{Key: "pauseTimeout", Type: events.FieldString, Label: "Idle pause timeout", Description: "How long a stream may stay paused before being stopped (for example 15m or 30m).", Default: "15m"},
					{Key: "viewerMessage", Type: events.FieldString, Label: "Message shown to viewer", Default: "Your stream was stopped because it remained paused for too long."},
					channelParam,
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 120, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				timeout := strParam(params, "pauseTimeout", "15m")
				msg := strParam(params, "viewerMessage", "Your stream was stopped because it remained paused for too long.")
				channels := listParam(params, "channelIds", []string{"*"})
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Playback paused", 80, 160, flows.EventTriggerConfig{
							Events: []string{"playback.session.paused"},
						}),
						makeNode("wait", flows.NodeLogicWaitEvent, "Wait for resume or stop", 380, 160, flows.WaitEventNodeConfig{
							EventTypes:       []string{"playback.session.resumed", "playback.session.stopped"},
							MatchCorrelation: true,
							Timeout:          timeout,
						}),
						makeNode("stop", flows.NodeAction, "Stop idle paused session", 680, 220, flows.ActionNodeConfig{
							ActionType: "playback.stop_session",
							Params: map[string]any{
								"selector":  "explicit",
								"sessionId": "{{ payload.sessionId }}",
								"message":   msg,
							},
						}),
						makeNode("alert", flows.NodeAction, "Notify owner", 980, 220, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "Stopped idle paused stream",
								"message":    "Stopped {{ payload.viewer.username | default \"viewer\" }}'s paused stream of {{ payload.titleName }} after " + timeout + ".",
								"color":      "#64748B",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "wait"),
						makeEdge("e2", "wait", flows.HandleTimeout, "stop"),
						makeEdge("e3", "stop", flows.HandleNext, "alert"),
					},
				}, nil
			},
		},

		// 8. Cap streams per viewer (limiterr.py)
		{
			meta: flows.Template{
				ID:          "jbops-cap-streams-per-viewer",
				Name:        "Cap concurrent streams per viewer",
				Description: "When a viewer exceeds their concurrent stream limit, stop their oldest or excess stream with a clear message and alert the owner.",
				Category:    "playback",
				Icon:        "users",
				Params: []flows.ParamSchema{
					{
						Key:     "stopTarget",
						Type:    events.FieldEnum,
						Label:   "Which stream to stop",
						Default: "viewer_oldest",
						Enum: []events.EnumOption{
							{Value: "viewer_oldest", Label: "Viewer's oldest stream"},
							{Value: "explicit", Label: "Newest (triggering) stream"},
						},
					},
					{Key: "viewerMessage", Type: events.FieldString, Label: "Message shown to viewer", Default: "Your concurrent stream limit has been reached. One stream was stopped."},
					channelParam,
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 60, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				target := strParam(params, "stopTarget", "viewer_oldest")
				msg := strParam(params, "viewerMessage", "Your concurrent stream limit has been reached. One stream was stopped.")
				channels := listParam(params, "channelIds", []string{"*"})
				stopParams := map[string]any{
					"selector": target,
					"viewerId": "{{ payload.viewer.id }}",
					"message":  msg,
				}
				if target == "explicit" {
					stopParams["sessionId"] = "{{ payload.sessionId }}"
				}
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Concurrency limit exceeded", 80, 160, flows.EventTriggerConfig{
							Events: []string{"playback.concurrency.exceeded"},
						}),
						makeNode("dedupe", flows.NodeLogicDedupe, "Dedupe per viewer", 360, 160, flows.DedupeNodeConfig{
							KeyTemplate: "{{ payload.viewer.id }}",
							Cooldown:    "1m",
						}),
						makeNode("stop", flows.NodeAction, "Stop excess stream", 640, 160, flows.ActionNodeConfig{
							ActionType: "playback.stop_session",
							Params:     stopParams,
						}),
						makeNode("alert", flows.NodeAction, "Alert owner", 940, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "Stream limit enforced",
								"message":    "{{ payload.viewer.username | default \"Viewer\" }} reached {{ payload.activeStreams }}/{{ payload.limit }} concurrent streams across {{ payload.distinctActiveIPs }} IP(s).",
								"color":      "#F59E0B",
								"fields": map[string]any{
									"Viewer":         "{{ payload.viewer.username | default \"unknown\" }}",
									"Active streams": "{{ payload.activeStreams }} / {{ payload.limit }}",
									"Distinct IPs":   "{{ payload.distinctActiveIPs }}",
								},
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "dedupe"),
						makeEdge("e2", "dedupe", flows.HandlePass, "stop"),
						makeEdge("e3", "stop", flows.HandleNext, "alert"),
					},
				}, nil
			},
		},

		// 9. New device / IP alert (notify_newip.py)
		{
			meta: flows.Template{
				ID:          "jbops-new-device-ip-alert",
				Name:        "New device or IP alert",
				Description: "Get notified with rich context whenever a viewer signs in from a new IP address or registers a previously unseen playback device.",
				Category:    "security",
				Icon:        "shield",
				Params: []flows.ParamSchema{
					channelParam,
					{Key: "cooldown", Type: events.FieldString, Label: "Cooldown per viewer & IP", Default: "1h"},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 60, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				cooldown := strParam(params, "cooldown", "1h")
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "New IP or device seen", 80, 160, flows.EventTriggerConfig{
							Events: []string{
								"auth.session.new_ip",
								"auth.device.registered",
							},
						}),
						makeNode("dedupe", flows.NodeLogicDedupe, "Dedupe viewer + IP", 380, 160, flows.DedupeNodeConfig{
							KeyTemplate: "{{ event.type }}:{{ payload.viewerId }}:{{ payload.remoteIP }}:{{ payload.deviceId }}",
							Cooldown:    cooldown,
						}),
						makeNode("alert", flows.NodeAction, "Send security alert", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "{{#if event.type == \"auth.session.new_ip\"}}New IP detected for {{ payload.username | default \"viewer\" }}{{#else}}New device registered for {{ payload.username | default \"viewer\" }}{{/if}}",
								"message":    "{{ payload.username | default \"A viewer\" }} connected from {{ payload.remoteIP | default \"unknown IP\" }} using {{ payload.name | default payload.client | default \"unknown client\" }} ({{ payload.platform | default \"unknown platform\" }}).",
								"color":      "#8B5CF6",
								"fields": map[string]any{
									"Viewer":   "{{ payload.username | default \"unknown\" }}",
									"IP":       "{{ payload.remoteIP | default \"unknown\" }}",
									"Location": "{{ payload.city | default payload.country | default payload.location | default \"unknown\" }}",
									"Device":   "{{ payload.name | default payload.client | default \"unknown\" }}",
								},
								"footer": "Ether Security",
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

		// 10. Buffering alert & relieve transcode load (kill_else_if_buffering.py)
		{
			meta: flows.Template{
				ID:          "jbops-buffering-alert-relieve",
				Name:        "Buffering alert & transcode relief",
				Description: "Detect repeated buffering on any stream (N stalls within a window), alert the owner, and optionally stop the lowest-priority competing transcode to restore smooth playback.",
				Category:    "playback",
				Icon:        "activity",
				Params: []flows.ParamSchema{
					{Key: "stallThreshold", Type: events.FieldInt, Label: "Stall count threshold", Default: 3},
					{Key: "stallWindow", Type: events.FieldString, Label: "Stall window", Default: "2m"},
					{Key: "protectTags", Type: events.FieldStringList, Label: "Protected viewer tags", Default: []string{"vip", "owner"}},
					{Key: "stopLowestPriority", Type: events.FieldBool, Label: "Stop lowest-priority competing transcode", Default: true},
					channelParam,
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 120, MaxSteps: 25},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				threshold := intParam(params, "stallThreshold", 3)
				window := strParam(params, "stallWindow", "2m")
				protectTags := listParam(params, "protectTags", []string{"vip", "owner"})
				stopCompeting := boolParam(params, "stopLowestPriority", true)
				channels := listParam(params, "channelIds", []string{"*"})

				nodes := []flows.Node{
					makeNode("trig", flows.NodeTriggerEvent, "Stream stalled", 80, 180, flows.EventTriggerConfig{
						Events: []string{"playback.stream.stalled"},
					}),
					makeNode("win", flows.NodeLogicWindow, "Repeated stalls in window", 340, 180, flows.WindowNodeConfig{
						KeyTemplate:  "{{ payload.sessionId }}",
						Threshold:    threshold,
						Window:       window,
						ResetOnReach: true,
					}),
					makeNode("dedupe", flows.NodeLogicDedupe, "Cooldown per session", 600, 180, flows.DedupeNodeConfig{
						KeyTemplate: "{{ payload.sessionId }}",
						Cooldown:    "5m",
					}),
					makeNode("alert", flows.NodeAction, "Send buffering alert", 880, 100, flows.ActionNodeConfig{
						ActionType: "notify.send",
						Params: map[string]any{
							"channelIds": channels,
							"title":      "Buffering alert: {{ payload.titleName }}",
							"message":    "{{ payload.viewer.username | default \"Viewer\" }} experienced repeated buffering on {{ payload.client.name | default payload.client.product }} ({{ payload.stream.delivery }}).",
							"posterUrl":  "{{ payload.posterUrl }}",
							"color":      "#F59E0B",
							"fields": map[string]any{
								"Viewer":   "{{ payload.viewer.username | default \"unknown\" }}",
								"Delivery": "{{ payload.stream.delivery }}",
								"Bitrate":  "{{ payload.stream.bitrateKbps }} kbps",
							},
						},
					}),
				}
				edges := []flows.Edge{
					makeEdge("e1", "trig", flows.HandleNext, "win"),
					makeEdge("e2", "win", flows.HandleReached, "dedupe"),
					makeEdge("e3", "dedupe", flows.HandlePass, "alert"),
				}
				if stopCompeting {
					nodes = append(nodes, makeNode("relieve", flows.NodeAction, "Stop lowest-priority transcode", 880, 270, flows.ActionNodeConfig{
						ActionType: "playback.stop_session",
						Params: map[string]any{
							"selector":          "lowest_priority_transcode",
							"excludeSessionId":  "{{ payload.sessionId }}",
							"excludeViewerTags": protectTags,
							"message":           "Your transcode was paused to relieve server load.",
						},
					}))
					edges = append(edges, makeEdge("e4", "dedupe", flows.HandlePass, "relieve"))
				}
				return flows.Graph{SchemaVersion: flows.SchemaVersion, Nodes: nodes, Edges: edges}, nil
			},
		},

		// 11. New releases / Recently Added digest (newsletters.py)
		{
			meta: flows.Template{
				ID:          "jbops-new-releases-digest",
				Name:        "New releases digest",
				Description: "Send a scheduled Recently Added newsletter with poster thumbnails and episodes automatically collapsed into seasons and shows.",
				Category:    "notifications",
				Icon:        "sparkles",
				Params: []flows.ParamSchema{
					channelParam,
					{Key: "cron", Type: events.FieldString, Label: "Schedule (cron)", Default: "0 18 * * 5"},
					{Key: "window", Type: events.FieldString, Label: "Lookback window", Default: "7d"},
					{Key: "saveShareable", Type: events.FieldBool, Label: "Publish shareable HTML link", Default: false},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 10, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				cron := strParam(params, "cron", "0 18 * * 5")
				window := strParam(params, "window", "7d")
				share := boolParam(params, "saveShareable", false)
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("sched", flows.NodeTriggerSchedule, "Scheduled digest", 80, 160, flows.ScheduleTriggerConfig{
							Cron: cron,
						}),
						makeNode("digest", flows.NodeAction, "Build Recently Added digest", 380, 160, flows.ActionNodeConfig{
							ActionType: "digest.generate",
							Params: map[string]any{
								"window":          window,
								"kind":            "recently_added",
								"groupEpisodes":   true,
								"includeActivity": false,
								"saveShareable":   share,
							},
						}),
						makeNode("send", flows.NodeAction, "Send digest", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds":   channels,
								"title":        "{{ steps.digest.output.subject }}",
								"message":      "{{ steps.digest.output.text }}",
								"html":         "{{ steps.digest.output.html }}",
								"linkUrl":      "{{ steps.digest.output.shareUrl }}",
								"color":        "#6366F1",
								"bypassDigest": true,
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

		// 12. Intro/credits & playback webhook for cinema lights
		{
			meta: flows.Template{
				ID:          "jbops-cinema-lights-webhook",
				Name:        "Cinema lights webhook (play, pause, intro & credits)",
				Description: "Trigger smart lighting scenes (Home Assistant, Hue, WLED) via outbound HTTP webhook when playback starts, pauses, stops, or enters intro/credits markers on a chosen player.",
				Category:    "home",
				Icon:        "lamp",
				Params: []flows.ParamSchema{
					{Key: "webhookUrl", Type: events.FieldString, Label: "Lighting webhook URL", Description: "Destination HTTP(S) webhook URL (for example Home Assistant webhook).", Required: true, Default: "https://homeassistant.example.test/api/webhook/ether-lights"},
					{Key: "clientName", Type: events.FieldString, Label: "Player / client name filter", Description: "Only react to this playback device (leave empty for all devices).", Default: "Living Room Apple TV"},
					{Key: "bearerToken", Type: events.FieldString, Label: "Optional bearer token", Secret: true},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 120, MaxSteps: 25},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				webhookURL := strParam(params, "webhookUrl", "https://homeassistant.example.test/api/webhook/ether-lights")
				clientName := strParam(params, "clientName", "")
				bearer := strParam(params, "bearerToken", "")

				hookParams := func(scene string) map[string]any {
					p := map[string]any{
						"url":         webhookURL,
						"method":      "POST",
						"contentType": "application/json",
						"body":        `{"scene":"` + scene + `","event":"{{ event.type }}","title":"{{ payload.titleName }}","client":"{{ payload.client.name }}"}`,
					}
					if bearer != "" {
						p["bearerToken"] = bearer
					}
					return p
				}

				nodes := []flows.Node{
					makeNode("trig", flows.NodeTriggerEvent, "Playback & marker events", 60, 260, flows.EventTriggerConfig{
						Events: []string{
							"playback.session.started",
							"playback.session.paused",
							"playback.session.resumed",
							"playback.session.stopped",
							"playback.marker.entered",
						},
					}),
				}
				var edges []flows.Edge
				switchSource := "trig"
				switchHandle := flows.HandleNext
				switchX := 340.0

				if clientName != "" {
					nodes = append(nodes, makeNode("client_filter", flows.NodeCondition, "Match player device", 320, 260, flows.ConditionNodeConfig{
						Condition: flows.ConditionGroup{
							Op: flows.LogicAnd,
							Rules: []flows.ConditionRule{
								{Field: "payload.client.name", Op: flows.OpEq, Value: clientName},
							},
						},
					}))
					edges = append(edges, makeEdge("e_trig_filter", "trig", flows.HandleNext, "client_filter"))
					switchSource = "client_filter"
					switchHandle = flows.HandleTrue
					switchX = 580.0
				}

				nodes = append(nodes,
					makeNode("route", flows.NodeLogicSwitch, "Select lighting scene", switchX, 260, flows.SwitchNodeConfig{
						Cases: []flows.SwitchCase{
							{
								Key:   "playing",
								Label: "Play / Resume",
								Condition: flows.ConditionGroup{
									Op: flows.LogicOr,
									Rules: []flows.ConditionRule{
										{Field: "event.type", Op: flows.OpEq, Value: "playback.session.started"},
										{Field: "event.type", Op: flows.OpEq, Value: "playback.session.resumed"},
									},
								},
							},
							{
								Key:   "paused",
								Label: "Paused",
								Condition: flows.ConditionGroup{
									Op:    flows.LogicAnd,
									Rules: []flows.ConditionRule{{Field: "event.type", Op: flows.OpEq, Value: "playback.session.paused"}},
								},
							},
							{
								Key:   "marker",
								Label: "Intro / Credits marker",
								Condition: flows.ConditionGroup{
									Op:    flows.LogicAnd,
									Rules: []flows.ConditionRule{{Field: "event.type", Op: flows.OpEq, Value: "playback.marker.entered"}},
								},
							},
							{
								Key:   "stopped",
								Label: "Stopped",
								Condition: flows.ConditionGroup{
									Op:    flows.LogicAnd,
									Rules: []flows.ConditionRule{{Field: "event.type", Op: flows.OpEq, Value: "playback.session.stopped"}},
								},
							},
						},
					}),
					makeNode("scene_dim", flows.NodeAction, "Dim cinema lights", switchX+300, 60, flows.ActionNodeConfig{
						ActionType: "http.webhook",
						Params:     hookParams("cinema_dim"),
					}),
					makeNode("scene_pause", flows.NodeAction, "Raise lights softly", switchX+300, 190, flows.ActionNodeConfig{
						ActionType: "http.webhook",
						Params:     hookParams("cinema_pause"),
					}),
					makeNode("scene_marker", flows.NodeAction, "Marker scene (intro/credits)", switchX+300, 320, flows.ActionNodeConfig{
						ActionType: "http.webhook",
						Params: map[string]any{
							"url":         webhookURL,
							"method":      "POST",
							"contentType": "application/json",
							"body":        `{"scene":"{{#if payload.markerKind == "credits"}}cinema_credits{{#else}}cinema_intro{{/if}}","marker":"{{ payload.markerKind }}","title":"{{ payload.titleName }}"}`,
						},
					}),
					makeNode("scene_bright", flows.NodeAction, "Restore room lights", switchX+300, 450, flows.ActionNodeConfig{
						ActionType: "http.webhook",
						Params:     hookParams("room_normal"),
					}),
				)
				if bearer != "" {
					// Add bearerToken to marker node as well.
					var cfg flows.ActionNodeConfig
					cfg, _ = flows.DecodeConfig[flows.ActionNodeConfig](nodes[len(nodes)-2])
					cfg.Params["bearerToken"] = bearer
					nodes[len(nodes)-2].Config = mustJSON(cfg)
				}
				edges = append(edges,
					makeEdge("e_to_switch", switchSource, switchHandle, "route"),
					makeEdge("e_play", "route", "playing", "scene_dim"),
					makeEdge("e_pause", "route", "paused", "scene_pause"),
					makeEdge("e_marker", "route", "marker", "scene_marker"),
					makeEdge("e_stop", "route", "stopped", "scene_bright"),
				)
				return flows.Graph{SchemaVersion: flows.SchemaVersion, Nodes: nodes, Edges: edges}, nil
			},
		},

		// 13. Cinema lights via MQTT (Home Assistant native)
		{
			meta: flows.Template{
				ID:          "jbops-cinema-lights-mqtt",
				Name:        "Home Assistant cinema lighting (MQTT)",
				Description: "Publish real-time playback state and intro/credits markers to an MQTT broker for instant Home Assistant lighting and media-room automations.",
				Category:    "home",
				Icon:        "radio",
				Params: []flows.ParamSchema{
					{Key: "brokerId", Type: events.FieldString, Label: "MQTT broker channel", Required: true, Default: "mqtt-home", Picker: flows.PickerChannel},
					{Key: "topic", Type: events.FieldString, Label: "MQTT topic", Default: "ether/livingroom/playback"},
					{Key: "clientName", Type: events.FieldString, Label: "Player / client name filter", Default: "Living Room Apple TV"},
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 240, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				brokerID := strParam(params, "brokerId", "mqtt-home")
				topic := strParam(params, "topic", "ether/livingroom/playback")
				clientName := strParam(params, "clientName", "")

				nodes := []flows.Node{
					makeNode("trig", flows.NodeTriggerEvent, "Playback & marker events", 80, 160, flows.EventTriggerConfig{
						Events: []string{
							"playback.session.started",
							"playback.session.paused",
							"playback.session.resumed",
							"playback.session.stopped",
							"playback.marker.entered",
						},
					}),
				}
				var edges []flows.Edge
				pubSource := "trig"
				pubHandle := flows.HandleNext
				pubX := 380.0
				if clientName != "" {
					nodes = append(nodes, makeNode("client_filter", flows.NodeCondition, "Match player device", 360, 160, flows.ConditionNodeConfig{
						Condition: flows.ConditionGroup{
							Op:    flows.LogicAnd,
							Rules: []flows.ConditionRule{{Field: "payload.client.name", Op: flows.OpEq, Value: clientName}},
						},
					}))
					edges = append(edges, makeEdge("e1", "trig", flows.HandleNext, "client_filter"))
					pubSource = "client_filter"
					pubHandle = flows.HandleTrue
					pubX = 660.0
				}
				nodes = append(nodes, makeNode("pub", flows.NodeAction, "Publish MQTT scene payload", pubX, 160, flows.ActionNodeConfig{
					ActionType: "mqtt.publish",
					Params: map[string]any{
						"brokerId": brokerID,
						"topic":    topic,
						"payload":  `{"event":"{{ event.type }}","marker":"{{ payload.markerKind }}","title":"{{ payload.titleName }}","mediaType":"{{ payload.mediaType }}","client":"{{ payload.client.name }}"}`,
						"qos":      1,
						"retain":   true,
					},
				}))
				edges = append(edges, makeEdge("e2", pubSource, pubHandle, "pub"))
				return flows.Graph{SchemaVersion: flows.SchemaVersion, Nodes: nodes, Edges: edges}, nil
			},
		},

		// 14. Scheduled library metadata export (exporter.py)
		{
			meta: flows.Template{
				ID:          "jbops-scheduled-library-export",
				Name:        "Scheduled library metadata export",
				Description: "Automatically export server or library metadata (CSV, JSON, XML, or M3U) on a schedule and send a summary with the download link.",
				Category:    "library",
				Icon:        "download",
				Params: []flows.ParamSchema{
					{Key: "cron", Type: events.FieldString, Label: "Schedule (cron)", Default: "0 3 * * 0"},
					{
						Key:     "scope",
						Type:    events.FieldEnum,
						Label:   "Export scope",
						Default: "server",
						Enum: []events.EnumOption{
							{Value: "server", Label: "Entire server"},
							{Value: "library", Label: "Single library"},
						},
					},
					{Key: "targetId", Type: events.FieldString, Label: "Library ID (when scope is library)", Picker: flows.PickerLibrary},
					{
						Key:     "format",
						Type:    events.FieldEnum,
						Label:   "Format",
						Default: "csv",
						Enum: []events.EnumOption{
							{Value: "csv", Label: "CSV"},
							{Value: "json", Label: "JSON"},
							{Value: "xml", Label: "XML"},
							{Value: "m3u", Label: "M3U"},
						},
					},
					channelParam,
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 10, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				cron := strParam(params, "cron", "0 3 * * 0")
				scope := strParam(params, "scope", "server")
				targetID := strParam(params, "targetId", "")
				if scope == "library" && targetID == "" {
					targetID = "movies"
				}
				format := strParam(params, "format", "csv")
				channels := listParam(params, "channelIds", []string{"*"})

				expParams := map[string]any{
					"scope":          scope,
					"format":         format,
					"metadataLevel":  2,
					"mediaInfoLevel": 1,
					"artworkLevel":   1,
				}
				if targetID != "" {
					expParams["targetId"] = targetID
				}
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("sched", flows.NodeTriggerSchedule, "Export schedule", 80, 160, flows.ScheduleTriggerConfig{
							Cron: cron,
						}),
						makeNode("export", flows.NodeAction, "Export library metadata", 380, 160, flows.ActionNodeConfig{
							ActionType: "library.export",
							Params:     expParams,
						}),
						makeNode("notify", flows.NodeAction, "Notify export ready", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "Library export ready ({{ steps.export.output.format | upper }})",
								"message":    "Exported {{ steps.export.output.itemCount }} items ({{ steps.export.output.sizeBytes | bytes }}) to {{ steps.export.output.filename }}.",
								"linkUrl":    "{{ steps.export.output.downloadUrl }}",
								"color":      "#10B981",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "sched", flows.HandleNext, "export"),
						makeEdge("e2", "export", flows.HandleNext, "notify"),
					},
				}, nil
			},
		},

		// 15. Auto-unpin dead/unhealthy pinned release
		{
			meta: flows.Template{
				ID:          "ops-auto-unpin-unhealthy",
				Name:        "Auto-unpin dead release",
				Description: "Automatically clear a pinned release when health monitoring detects it has become unavailable, letting automatic release selection recover playback.",
				Category:    "operations",
				Icon:        "pin-off",
				Params: []flows.ParamSchema{
					channelParam,
				},
				Limits: flows.FlowLimits{RateLimitPerMin: 60, MaxSteps: 20},
			},
			build: func(params map[string]any) (flows.Graph, error) {
				channels := listParam(params, "channelIds", []string{"*"})
				return flows.Graph{
					SchemaVersion: flows.SchemaVersion,
					Nodes: []flows.Node{
						makeNode("trig", flows.NodeTriggerEvent, "Pinned release unhealthy", 80, 160, flows.EventTriggerConfig{
							Events: []string{"resolver.pin.unhealthy"},
						}),
						makeNode("unpin", flows.NodeAction, "Clear unhealthy pin", 380, 160, flows.ActionNodeConfig{
							ActionType: "release.unpin",
							Params: map[string]any{
								"titleKey": "{{ payload.titleKey }}",
							},
						}),
						makeNode("alert", flows.NodeAction, "Notify owner", 680, 160, flows.ActionNodeConfig{
							ActionType: "notify.send",
							Params: map[string]any{
								"channelIds": channels,
								"title":      "Cleared dead pin: {{ payload.titleName | default payload.titleKey }}",
								"message":    "Removed unhealthy pinned release ({{ payload.reason | default payload.status | default \"unavailable\" }}) so Ether can select a healthy replacement.",
								"color":      "#F59E0B",
							},
						}),
					},
					Edges: []flows.Edge{
						makeEdge("e1", "trig", flows.HandleNext, "unpin"),
						makeEdge("e2", "unpin", flows.HandleNext, "alert"),
					},
				}, nil
			},
		},
	}
}
