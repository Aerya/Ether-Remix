// Package templates ships Ether's curated starter Flow templates: the five
// notification presets plus native power-user recipes (Tautulli & JBOPS parity
// without external scripts).
package templates

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

type templateEntry struct {
	meta  flows.Template
	build func(params map[string]any) (flows.Graph, error)
}

var entries = buildEntries()

func buildEntries() []templateEntry {
	var all []templateEntry
	all = append(all, notificationPresetEntries()...)
	all = append(all, jbopsRecipeEntries()...)
	return all
}

// All returns deep copies of all curated starter templates with valid default graphs.
func All() []flows.Template {
	out := make([]flows.Template, 0, len(entries))
	for _, e := range entries {
		out = append(out, materializeTemplate(e))
	}
	return out
}

// ByID looks up one curated starter template by its stable ID.
func ByID(id string) (flows.Template, bool) {
	for _, e := range entries {
		if e.meta.ID == id {
			return materializeTemplate(e), true
		}
	}
	return flows.Template{}, false
}

// ByCategory returns all templates matching category (case-insensitive).
func ByCategory(category string) []flows.Template {
	cat := strings.ToLower(strings.TrimSpace(category))
	var out []flows.Template
	for _, e := range entries {
		if strings.ToLower(e.meta.Category) == cat {
			out = append(out, materializeTemplate(e))
		}
	}
	return out
}

// ByNotifyPreset looks up the template corresponding to a legacy notification
// preset key (critical, playback, providers, updates, newsletter).
func ByNotifyPreset(preset string) (flows.Template, bool) {
	key := strings.ToLower(strings.TrimSpace(preset))
	for _, e := range entries {
		if e.meta.NotifyPreset != "" && strings.ToLower(e.meta.NotifyPreset) == key {
			return materializeTemplate(e), true
		}
	}
	return flows.Template{}, false
}

// Instantiate creates a ready-to-save Flow from templateID using owner-chosen
// parameter overrides merged over the template's declared defaults.
func Instantiate(templateID string, overrides map[string]any) (flows.Flow, error) {
	var entry *templateEntry
	for i := range entries {
		if entries[i].meta.ID == templateID {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return flows.Flow{}, fmt.Errorf("flows/templates: unknown template %q", templateID)
	}

	resolved, err := resolveTemplateParams(entry.meta.Params, overrides)
	if err != nil {
		return flows.Flow{}, fmt.Errorf("flows/templates: %s: %w", templateID, err)
	}
	graph, err := entry.build(resolved)
	if err != nil {
		return flows.Flow{}, fmt.Errorf("flows/templates: %s: %w", templateID, err)
	}

	name := entry.meta.Name
	if customName, ok := overrides["name"].(string); ok && strings.TrimSpace(customName) != "" {
		name = strings.TrimSpace(customName)
	}

	tags := []string{entry.meta.Category}
	if entry.meta.NotifyPreset != "" {
		tags = append(tags, "preset:"+entry.meta.NotifyPreset)
	}

	return flows.Flow{
		Name:        name,
		Description: entry.meta.Description,
		Enabled:     true,
		Version:     1,
		Graph:       cloneGraph(graph),
		Limits:      entry.meta.Limits,
		Tags:        tags,
		TemplateID:  entry.meta.ID,
	}, nil
}

func materializeTemplate(e templateEntry) flows.Template {
	defaults := defaultParamMap(e.meta.Params)
	g, err := e.build(defaults)
	if err != nil {
		panic(fmt.Sprintf("flows/templates: invalid built-in template %s: %v", e.meta.ID, err))
	}
	tpl := e.meta
	tpl.Params = append([]flows.ParamSchema(nil), e.meta.Params...)
	tpl.Graph = cloneGraph(g)
	return tpl
}

func defaultParamMap(schemas []flows.ParamSchema) map[string]any {
	out := make(map[string]any, len(schemas))
	for _, p := range schemas {
		if p.Default != nil {
			out[p.Key] = p.Default
		}
	}
	return out
}

func resolveTemplateParams(schemas []flows.ParamSchema, overrides map[string]any) (map[string]any, error) {
	resolved := defaultParamMap(schemas)
	for _, p := range schemas {
		if overrides != nil {
			if v, ok := overrides[p.Key]; ok && v != nil {
				resolved[p.Key] = v
			}
		}
		v, ok := resolved[p.Key]
		if !ok || isEmptyParamValue(v) {
			if p.Required {
				return nil, fmt.Errorf("missing required parameter %q", p.Key)
			}
			continue
		}
		coerced, err := coerceTemplateParam(p, v)
		if err != nil {
			return nil, err
		}
		resolved[p.Key] = coerced
	}
	return resolved, nil
}

func isEmptyParamValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []string:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

func coerceTemplateParam(p flows.ParamSchema, v any) (any, error) {
	switch p.Type {
	case events.FieldString:
		s, ok := v.(string)
		if !ok {
			return fmt.Sprint(v), nil
		}
		return s, nil
	case events.FieldEnum:
		s, ok := v.(string)
		if !ok || !p.HasEnumValue(s) {
			return nil, fmt.Errorf("parameter %q value %v is not an allowed option", p.Key, v)
		}
		return s, nil
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
		case string:
			i, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil {
				return nil, fmt.Errorf("parameter %q must be an integer", p.Key)
			}
			return i, nil
		default:
			return nil, fmt.Errorf("parameter %q must be an integer", p.Key)
		}
	case events.FieldBool:
		switch b := v.(type) {
		case bool:
			return b, nil
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(strings.ToLower(b)))
			if err != nil {
				return nil, fmt.Errorf("parameter %q must be a boolean", p.Key)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("parameter %q must be a boolean", p.Key)
		}
	case events.FieldStringList:
		switch items := v.(type) {
		case []string:
			out := make([]string, 0, len(items))
			for _, s := range items {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
			return out, nil
		case []any:
			out := make([]string, 0, len(items))
			for _, item := range items {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("parameter %q elements must be strings", p.Key)
				}
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
			return out, nil
		case string:
			var out []string
			for _, part := range strings.Split(items, ",") {
				if t := strings.TrimSpace(part); t != "" {
					out = append(out, t)
				}
			}
			return out, nil
		default:
			return nil, fmt.Errorf("parameter %q must be a list of strings", p.Key)
		}
	default:
		return v, nil
	}
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

// --- compact graph builder helpers ---

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func makeNode(id string, kind flows.NodeKind, label string, x, y float64, cfg any) flows.Node {
	return flows.Node{
		ID:       id,
		Kind:     kind,
		Label:    label,
		Position: flows.Position{X: x, Y: y},
		Config:   mustJSON(cfg),
	}
}

func makeEdge(id, source, handle, target string) flows.Edge {
	return flows.Edge{
		ID:           id,
		Source:       source,
		SourceHandle: handle,
		Target:       target,
	}
}

func strParam(params map[string]any, key, def string) string {
	if v, ok := params[key].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func intParam(params map[string]any, key string, def int) int {
	if v, ok := params[key].(int); ok {
		return v
	}
	return def
}

func boolParam(params map[string]any, key string, def bool) bool {
	if v, ok := params[key].(bool); ok {
		return v
	}
	return def
}

func listParam(params map[string]any, key string, def []string) []string {
	if v, ok := params[key].([]string); ok && len(v) > 0 {
		return append([]string(nil), v...)
	}
	return append([]string(nil), def...)
}
