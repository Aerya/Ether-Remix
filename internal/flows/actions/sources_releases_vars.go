package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

var validVarKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// NewSourceRefresh constructs the built-in source.refresh action handler.
func NewSourceRefresh(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "source.refresh",
		Owner:       events.OwnerBuiltin,
		Group:       "sources",
		Label:       "Refresh external source",
		Description: "Trigger a refresh of a Live TV guide/playlist/VOD source, watchlist, RSS feed, or plugin source.",
		Icon:        "rss",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{
				Key:      "sourceKind",
				Type:     events.FieldEnum,
				Label:    "Source kind",
				Required: true,
				Enum: []events.EnumOption{
					{Value: "livetv_guide", Label: "Live TV guide (all sources)"},
					{Value: "livetv_source", Label: "Live TV playlist / source"},
					{Value: "livetv_vod", Label: "Live TV VOD source"},
					{Value: "watchlist", Label: "Viewer watchlist"},
					{Value: "watchlist_rss", Label: "Watchlist RSS feeds"},
					{Value: "plugin", Label: "Plugin source"},
				},
			},
			{Key: "sourceId", Type: events.FieldString, Label: "Source ID", Description: "Specific source, viewer, or plugin ID (required for livetv_source, livetv_vod, and plugin).", Template: true, Picker: flows.PickerSource},
		},
		Outputs: []events.FieldSchema{
			{Name: "refreshed", Type: events.FieldBool, Label: "Refreshed"},
			{Name: "sourceKind", Type: events.FieldString, Label: "Source kind"},
			{Name: "sourceId", Type: events.FieldString, Label: "Source ID"},
			{Name: "items", Type: events.FieldInt, Label: "Items refreshed"},
			{Name: "durationMs", Type: events.FieldDurationMS, Label: "Duration", Unit: "ms"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		sourceKind := strings.ToLower(strings.TrimSpace(paramString(params, "sourceKind", "")))
		switch sourceKind {
		case "livetv_guide", "livetv_source", "livetv_vod", "watchlist", "watchlist_rss", "plugin":
		default:
			return flows.ActionResult{}, fmt.Errorf("source.refresh: unsupported sourceKind %q", sourceKind)
		}
		sourceID := strings.TrimSpace(paramString(params, "sourceId", ""))
		switch sourceKind {
		case "livetv_source", "livetv_vod", "plugin":
			if sourceID == "" {
				return flows.ActionResult{}, fmt.Errorf("source.refresh: sourceId is required for sourceKind %q", sourceKind)
			}
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":     true,
					"refreshed":  false,
					"sourceKind": sourceKind,
					"sourceId":   sourceID,
					"items":      0,
					"durationMs": int64(0),
				},
			}, nil
		}
		if deps.Sources == nil {
			return flows.ActionResult{}, errors.New("source.refresh: source refresher unavailable")
		}

		start := deps.now()
		res, err := deps.Sources.RefreshSource(ctx, sourceKind, sourceID)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("source.refresh: %w", err)
		}
		if res.DurationMS == 0 {
			if d := deps.now().Sub(start).Milliseconds(); d > 0 {
				res.DurationMS = d
			}
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":     false,
				"refreshed":  true,
				"sourceKind": sourceKind,
				"sourceId":   sourceID,
				"items":      res.Items,
				"durationMs": res.DurationMS,
			},
		}, nil
	}}
}

// NewReleasePin constructs the built-in release.pin action handler.
func NewReleasePin(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "release.pin",
		Owner:       events.OwnerBuiltin,
		Group:       "resolver",
		Label:       "Pin release",
		Description: "Pin a specific release to a title so future playback prefers it, invalidating cached playback decisions.",
		Icon:        "pin",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "titleKey", Type: events.FieldString, Label: "Title key", Description: "Canonical title key (for example movie:tmdb:550 or {{ payload.titleKey }}).", Required: true, Template: true},
			{Key: "releaseId", Type: events.FieldString, Label: "Release ID", Description: "Release ID from the resolver.", Required: true, Template: true},
			{Key: "note", Type: events.FieldString, Label: "Pin note", Description: "Optional note recorded alongside the pin.", Template: true},
		},
		Outputs: []events.FieldSchema{
			{Name: "pinned", Type: events.FieldBool, Label: "Pinned"},
			{Name: "titleKey", Type: events.FieldString, Label: "Title key"},
			{Name: "releaseId", Type: events.FieldString, Label: "Release ID"},
			{Name: "releaseName", Type: events.FieldString, Label: "Release name"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		titleKey := strings.TrimSpace(paramString(params, "titleKey", actx.Event.Correlation.TitleID))
		if titleKey == "" {
			return flows.ActionResult{}, errors.New("release.pin: titleKey is required")
		}
		releaseID := strings.TrimSpace(paramString(params, "releaseId", ""))
		if releaseID == "" {
			return flows.ActionResult{}, errors.New("release.pin: releaseId is required")
		}
		note := strings.TrimSpace(paramString(params, "note", ""))

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":      true,
					"pinned":      false,
					"titleKey":    titleKey,
					"releaseId":   releaseID,
					"releaseName": "",
				},
			}, nil
		}
		if deps.Releases == nil {
			return flows.ActionResult{}, errors.New("release.pin: release pinner unavailable")
		}
		res, err := deps.Releases.PinRelease(ctx, titleKey, releaseID, note)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("release.pin: %w", err)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":      false,
				"pinned":      true,
				"titleKey":    res.TitleKey,
				"releaseId":   res.ReleaseID,
				"releaseName": res.ReleaseName,
			},
		}, nil
	}}
}

// NewReleaseUnpin constructs the built-in release.unpin action handler.
func NewReleaseUnpin(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "release.unpin",
		Owner:       events.OwnerBuiltin,
		Group:       "resolver",
		Label:       "Unpin release",
		Description: "Remove a title's pinned release and invalidate cached playback decisions so automatic resolution takes over.",
		Icon:        "pin-off",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "titleKey", Type: events.FieldString, Label: "Title key", Description: "Canonical title key whose pin should be removed.", Required: true, Template: true},
		},
		Outputs: []events.FieldSchema{
			{Name: "unpinned", Type: events.FieldBool, Label: "Unpinned"},
			{Name: "titleKey", Type: events.FieldString, Label: "Title key"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		titleKey := strings.TrimSpace(paramString(params, "titleKey", actx.Event.Correlation.TitleID))
		if titleKey == "" {
			return flows.ActionResult{}, errors.New("release.unpin: titleKey is required")
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":   true,
					"unpinned": false,
					"titleKey": titleKey,
				},
			}, nil
		}
		if deps.Releases == nil {
			return flows.ActionResult{}, errors.New("release.unpin: release pinner unavailable")
		}
		if err := deps.Releases.UnpinRelease(ctx, titleKey); err != nil {
			return flows.ActionResult{}, fmt.Errorf("release.unpin: %w", err)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":   false,
				"unpinned": true,
				"titleKey": titleKey,
			},
		}, nil
	}}
}

// NewFlowSetVariable constructs the built-in flow.set_variable action handler.
// Because setting run-scoped variables has no external side effect, it executes
// even during DryRun so downstream conditions and templates evaluate faithfully.
func NewFlowSetVariable(_ Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "flow.set_variable",
		Owner:       events.OwnerBuiltin,
		Group:       "flow",
		Label:       "Set flow variable",
		Description: "Set or transform a run-scoped variable accessible to downstream nodes via vars.<key>.",
		Icon:        "variable",
		Idempotent:  true,
		SideEffect:  false,
		Params: []flows.ParamSchema{
			{Key: "key", Type: events.FieldString, Label: "Variable name", Description: "Identifier stored under vars.<key> (letters, digits, underscores).", Required: true, Template: false},
			{Key: "value", Type: events.FieldString, Label: "Value", Description: "Value or template expression to store.", Required: true, Template: true},
			{
				Key:     "valueType",
				Type:    events.FieldEnum,
				Label:   "Value type",
				Default: "auto",
				Enum: []events.EnumOption{
					{Value: "auto", Label: "Auto (preserve expression type)"},
					{Value: "string", Label: "String"},
					{Value: "int", Label: "Integer"},
					{Value: "float", Label: "Number"},
					{Value: "bool", Label: "Boolean"},
					{Value: "json", Label: "JSON"},
				},
			},
		},
		Outputs: []events.FieldSchema{
			{Name: "key", Type: events.FieldString, Label: "Variable name"},
			{Name: "value", Type: events.FieldString, Label: "Stored value"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(_ context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		key := strings.TrimSpace(paramString(params, "key", ""))
		if !validVarKeyRe.MatchString(key) {
			return flows.ActionResult{}, fmt.Errorf("flow.set_variable: invalid variable key %q", key)
		}
		rawVal, ok := params["value"]
		if !ok {
			return flows.ActionResult{}, errors.New("flow.set_variable: value is required")
		}
		vtype := strings.ToLower(strings.TrimSpace(paramString(params, "valueType", "auto")))
		val, err := coerceVariableValue(rawVal, vtype)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("flow.set_variable: %w", err)
		}
		if actx.SetVar != nil {
			actx.SetVar(key, val)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"key":   key,
				"value": val,
			},
		}, nil
	}}
}

func coerceVariableValue(raw any, valueType string) (any, error) {
	switch valueType {
	case "", "auto":
		return raw, nil
	case "string":
		if raw == nil {
			return "", nil
		}
		return fmt.Sprint(raw), nil
	case "int":
		switch v := raw.(type) {
		case int:
			return int64(v), nil
		case int64:
			return v, nil
		case float64:
			return int64(v), nil
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse %q as int", v)
			}
			return n, nil
		default:
			return nil, fmt.Errorf("cannot coerce %T to int", raw)
		}
	case "float":
		switch v := raw.(type) {
		case float64:
			return v, nil
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse %q as float", v)
			}
			return f, nil
		default:
			return nil, fmt.Errorf("cannot coerce %T to float", raw)
		}
	case "bool":
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(strings.ToLower(v)))
			if err != nil {
				return nil, fmt.Errorf("cannot parse %q as bool", v)
			}
			return b, nil
		default:
			return nil, fmt.Errorf("cannot coerce %T to bool", raw)
		}
	case "json":
		if s, ok := raw.(string); ok {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err != nil {
				return nil, fmt.Errorf("invalid JSON value: %w", err)
			}
			return parsed, nil
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported valueType %q", valueType)
	}
}
