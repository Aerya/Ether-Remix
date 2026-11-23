package actions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/notify"
)

// NewNotifySend constructs the built-in notify.send action handler.
func NewNotifySend(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "notify.send",
		Owner:       events.OwnerBuiltin,
		Group:       "notifications",
		Label:       "Send notification",
		Description: "Deliver a message through one or more configured notification channels, with optional rich Discord/Slack embeds, poster thumbnail, priority, and HTML body.",
		Icon:        "bell",
		Idempotent:  false,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "channelIds", Type: events.FieldStringList, Label: "Channels", Description: "Target notification channel IDs, or [\"*\"] for all enabled channels.", Required: true, Template: true, Picker: flows.PickerChannel},
			{Key: "title", Type: events.FieldString, Label: "Title", Description: "Notification headline or email subject.", Required: true, Template: true},
			{Key: "message", Type: events.FieldString, Label: "Message", Description: "Plain-text notification body.", Required: true, Template: true},
			{Key: "html", Type: events.FieldString, Label: "HTML body", Description: "Optional HTML content for email and HTML-capable channels.", Template: true},
			{Key: "posterUrl", Type: events.FieldString, Label: "Poster URL", Description: "Optional image URL rendered as a thumbnail on rich channels.", Template: true},
			{Key: "color", Type: events.FieldString, Label: "Accent color", Description: "Optional hex color (for example #5865F2 or #EF4444) for rich embeds.", Template: true},
			{Key: "fields", Type: events.FieldObject, Label: "Embed fields", Description: "Optional key/value fields rendered inside Discord embeds or Slack attachments.", Template: true},
			{Key: "priority", Type: events.FieldInt, Label: "Priority", Description: "Optional transport priority (Pushover -2..2, ntfy 1..5, Gotify 0..10).", Template: true},
			{Key: "linkUrl", Type: events.FieldString, Label: "Link URL", Description: "Optional click-through URL attached to the notification.", Template: true},
			{Key: "footer", Type: events.FieldString, Label: "Footer", Description: "Optional footer text for rich embeds.", Template: true},
			{Key: "bypassDigest", Type: events.FieldBool, Label: "Bypass digest", Description: "Deliver immediately even when a channel uses digest batching.", Default: false},
		},
		Outputs: []events.FieldSchema{
			{Name: "sent", Type: events.FieldInt, Label: "Sent immediately"},
			{Name: "queued", Type: events.FieldInt, Label: "Queued for delivery"},
			{Name: "failed", Type: events.FieldInt, Label: "Failed deliveries"},
			{Name: "skipped", Type: events.FieldInt, Label: "Skipped channels"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		channelIDs := paramStringList(params, "channelIds")
		if len(channelIDs) == 0 {
			return flows.ActionResult{}, errors.New("notify.send: channelIds requires at least one channel ID or \"*\"")
		}
		title := strings.TrimSpace(paramString(params, "title", ""))
		if title == "" {
			return flows.ActionResult{}, errors.New("notify.send: title is required")
		}
		message := strings.TrimSpace(paramString(params, "message", ""))
		if message == "" {
			return flows.ActionResult{}, errors.New("notify.send: message is required")
		}
		priority, _, err := paramInt(params, "priority", 0)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("notify.send: %w", err)
		}
		bypassDigest, _, err := paramBool(params, "bypassDigest", false)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("notify.send: %w", err)
		}

		evKind := strings.TrimSpace(actx.Event.Type)
		if evKind == "" {
			evKind = "flow"
		}
		ev := notify.Event{
			Kind:      evKind,
			Title:     title,
			Message:   message,
			HTML:      paramString(params, "html", ""),
			PosterURL: paramString(params, "posterUrl", ""),
			Color:     paramString(params, "color", ""),
			Fields:    parseEmbedFields(params["fields"]),
			Priority:  priority,
			LinkURL:   paramString(params, "linkUrl", ""),
			Footer:    paramString(params, "footer", ""),
			At:        deps.now(),
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":     true,
					"sent":       0,
					"queued":     0,
					"failed":     0,
					"skipped":    0,
					"channelIds": channelIDs,
					"title":      ev.Title,
					"message":    ev.Message,
					"fieldCount": len(ev.Fields),
				},
			}, nil
		}
		if deps.Notify == nil {
			return flows.ActionResult{}, errors.New("notify.send: notification service unavailable")
		}

		results, err := deps.Notify.DeliverToChannels(ctx, channelIDs, ev, notify.DeliverOptions{
			BypassDigest: bypassDigest,
		})
		var sent, queued, failed, skipped int
		perChannel := make(map[string]any, len(results))
		for _, r := range results {
			switch r.Status {
			case "sent":
				sent++
			case "queued":
				queued++
			case "failed":
				failed++
			case "skipped":
				skipped++
			}
			entry := map[string]any{"status": r.Status}
			if r.Error != "" {
				entry["error"] = r.Error
			}
			perChannel[r.ChannelID] = entry
		}
		out := flows.ActionResult{
			Output: map[string]any{
				"dryRun":  false,
				"sent":    sent,
				"queued":  queued,
				"failed":  failed,
				"skipped": skipped,
				"results": perChannel,
			},
		}
		if err != nil {
			return out, fmt.Errorf("notify.send: %w", err)
		}
		return out, nil
	}}
}

func parseEmbedFields(raw any) []notify.EmbedField {
	if raw == nil {
		return nil
	}
	var out []notify.EmbedField
	switch v := raw.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			name := strings.TrimSpace(k)
			if name == "" || v[k] == nil {
				continue
			}
			if obj, ok := v[k].(map[string]any); ok {
				val := strings.TrimSpace(fmt.Sprint(obj["value"]))
				if val == "" || obj["value"] == nil {
					continue
				}
				inline := true
				if b, ok := obj["inline"].(bool); ok {
					inline = b
				}
				out = append(out, notify.EmbedField{Name: name, Value: val, Inline: inline})
				continue
			}
			val := strings.TrimSpace(fmt.Sprint(v[k]))
			if val == "" {
				continue
			}
			out = append(out, notify.EmbedField{Name: name, Value: val, Inline: true})
		}
	case []any:
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimSpace(fmt.Sprint(obj["name"]))
			val := strings.TrimSpace(fmt.Sprint(obj["value"]))
			if name == "" || val == "" || obj["name"] == nil || obj["value"] == nil {
				continue
			}
			inline := true
			if b, ok := obj["inline"].(bool); ok {
				inline = b
			}
			out = append(out, notify.EmbedField{Name: name, Value: val, Inline: inline})
		}
	}
	if len(out) > 25 {
		out = out[:25]
	}
	return out
}

// NewMQTTPublish constructs the built-in mqtt.publish action handler.
func NewMQTTPublish(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "mqtt.publish",
		Owner:       events.OwnerBuiltin,
		Group:       "home",
		Label:       "Publish MQTT message",
		Description: "Publish a message to a configured MQTT broker channel for Home Assistant lighting, media-room scenes, and presence automations.",
		Icon:        "radio",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "brokerId", Type: events.FieldString, Label: "MQTT broker", Description: "ID of a configured MQTT broker notification channel.", Required: true, Template: true, Picker: flows.PickerChannel},
			{Key: "topic", Type: events.FieldString, Label: "Topic", Description: "MQTT topic to publish to (wildcards are not allowed).", Required: true, Template: true},
			{Key: "payload", Type: events.FieldString, Label: "Payload", Description: "Message body (plain text or templated JSON).", Required: true, Template: true},
			{Key: "qos", Type: events.FieldInt, Label: "QoS", Description: "MQTT QoS level (0, 1, or 2).", Default: 0},
			{Key: "retain", Type: events.FieldBool, Label: "Retain", Description: "Ask the broker to retain the last published value on this topic.", Default: false},
		},
		Outputs: []events.FieldSchema{
			{Name: "published", Type: events.FieldBool, Label: "Published"},
			{Name: "brokerId", Type: events.FieldString, Label: "Broker ID"},
			{Name: "topic", Type: events.FieldString, Label: "Topic"},
			{Name: "bytes", Type: events.FieldInt, Label: "Payload bytes"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		brokerID := strings.TrimSpace(paramString(params, "brokerId", ""))
		if brokerID == "" {
			return flows.ActionResult{}, errors.New("mqtt.publish: brokerId is required")
		}
		topic := strings.TrimSpace(paramString(params, "topic", ""))
		if err := notify.ValidateMQTTTopic(topic); err != nil {
			return flows.ActionResult{}, fmt.Errorf("mqtt.publish: %w", err)
		}
		payload := paramString(params, "payload", "")
		if len(payload) > 256*1024 {
			return flows.ActionResult{}, errors.New("mqtt.publish: payload exceeds 256 KiB limit")
		}
		qos, _, err := paramInt(params, "qos", 0)
		if err != nil || qos < 0 || qos > 2 {
			return flows.ActionResult{}, errors.New("mqtt.publish: qos must be 0, 1, or 2")
		}
		retain, _, err := paramBool(params, "retain", false)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("mqtt.publish: %w", err)
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":    true,
					"published": false,
					"brokerId":  brokerID,
					"topic":     topic,
					"qos":       qos,
					"retain":    retain,
					"bytes":     len(payload),
				},
			}, nil
		}
		if deps.Notify == nil {
			return flows.ActionResult{}, errors.New("mqtt.publish: notification/MQTT service unavailable")
		}
		if err := deps.Notify.PublishMQTT(ctx, brokerID, topic, []byte(payload), byte(qos), retain); err != nil {
			return flows.ActionResult{}, fmt.Errorf("mqtt.publish: %w", err)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":    false,
				"published": true,
				"brokerId":  brokerID,
				"topic":     topic,
				"qos":       qos,
				"retain":    retain,
				"bytes":     len(payload),
			},
		}, nil
	}}
}
