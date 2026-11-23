package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/logredact"
)

const (
	// DefaultEventPublishPerMin bounds custom events published per plugin per minute.
	DefaultEventPublishPerMin = 30
	// MaxPublishedEventPayloadBytes caps the JSON payload size of a plugin-published event.
	MaxPublishedEventPayloadBytes = 64 * 1024
	// DefaultPluginFlowActionTimeout is applied when a flow action omits timeout.
	DefaultPluginFlowActionTimeout = 30 * time.Second
	// MaxPluginFlowActionTimeout is the hard ceiling for any plugin flow action.
	MaxPluginFlowActionTimeout = 60 * time.Second
	// MaxPluginFlowActionResponseBytes caps a plugin flow action JSON response body.
	MaxPluginFlowActionResponseBytes = 64 * 1024
	// MaxPluginPublishedEvents caps how many published event schemas a plugin may declare.
	MaxPluginPublishedEvents = 64
	// MaxPluginFlowActions caps how many flow actions a plugin may declare.
	MaxPluginFlowActions = 64
	// MaxEventFields caps how many fields an event schema or action output may declare.
	MaxEventFields = 64
	// MaxFlowActionParams caps how many parameters a flow action may declare.
	MaxFlowActionParams = 64
	// HeaderIdempotencyKey carries the Flow step idempotency key on plugin action calls.
	HeaderIdempotencyKey = "X-Ether-Idempotency-Key"
)

type flowActionOrigin struct {
	depth       int
	originRunID string
}

type flowActionOriginKey struct{}

func withFlowActionOrigin(ctx context.Context, depth int, originRunID string) context.Context {
	return context.WithValue(ctx, flowActionOriginKey{}, flowActionOrigin{
		depth:       depth,
		originRunID: originRunID,
	})
}

func flowActionOriginFromContext(ctx context.Context) (flowActionOrigin, bool) {
	if ctx == nil {
		return flowActionOrigin{}, false
	}
	v, ok := ctx.Value(flowActionOriginKey{}).(flowActionOrigin)
	return v, ok
}

func isSafePluginRoute(p string) bool {
	if p == "" || len(p) > 256 {
		return false
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	if strings.ContainsAny(p, "\r\n\\") || strings.Contains(p, "://") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// SetFlowsBackends wires the Events bus, Events schema registry, and Flows
// action registry after Manager construction and immediately registers all
// currently installed enabled plugins' events and actions.
func (m *Manager) SetFlowsBackends(bus *events.Bus, reg *events.Registry, actions flows.ActionRegistry) {
	m.mu.Lock()
	m.deps.EventBus = bus
	m.deps.EventRegistry = reg
	m.deps.FlowActions = actions
	m.mu.Unlock()

	if reg == nil && actions == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if recs, err := m.store.list(ctx); err == nil {
		for _, r := range recs {
			m.syncFlowsPlugin(ctx, r)
		}
	}
}

func (m *Manager) flowsEventBus() *events.Bus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deps.EventBus
}

func (m *Manager) flowsRegistries() (*events.Registry, flows.ActionRegistry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deps.EventRegistry, m.deps.FlowActions
}

func (m *Manager) resolvePluginCopy(ctx context.Context, pluginID, raw, fallback string) string {
	if strings.HasPrefix(raw, "$t:") {
		if resolved, ok := m.ResolveText(ctx, pluginID, strings.TrimPrefix(raw, "$t:"), "en"); ok && strings.TrimSpace(resolved) != "" {
			return resolved
		}
		if fallback != "" {
			return fallback
		}
		return strings.TrimPrefix(raw, "$t:")
	}
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return raw
}

func defaultCopyResolver(raw, fallback string) string {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return raw
}

func convertEventEnums(opts []EventEnumOption, resolve func(raw, fallback string) string) []events.EnumOption {
	if len(opts) == 0 {
		return nil
	}
	if resolve == nil {
		resolve = defaultCopyResolver
	}
	out := make([]events.EnumOption, len(opts))
	for i, opt := range opts {
		lbl := opt.Label
		if lbl != "" {
			lbl = resolve(lbl, opt.Value)
		}
		out[i] = events.EnumOption{
			Value: opt.Value,
			Label: lbl,
		}
	}
	return out
}

func convertEventFields(fields []EventFieldMeta, resolve func(raw, fallback string) string) []events.FieldSchema {
	if len(fields) == 0 {
		return nil
	}
	if resolve == nil {
		resolve = defaultCopyResolver
	}
	out := make([]events.FieldSchema, len(fields))
	for i, f := range fields {
		out[i] = events.FieldSchema{
			Name:        f.Name,
			Type:        events.FieldType(f.Type),
			Label:       resolve(f.Label, f.Name),
			Description: resolve(f.Description, ""),
			Unit:        f.Unit,
			Enum:        convertEventEnums(f.Enum, resolve),
			PII:         f.PII,
			Secret:      f.Secret,
			Filterable:  f.Filterable,
			Example:     f.Example,
		}
	}
	return out
}

// BuildPluginEventSchemas builds the events.EventSchema entries contributed by
// man for pluginID. resolve may be nil to keep literal labels.
func BuildPluginEventSchemas(pluginID string, man Manifest, resolve func(raw, fallback string) string) []events.EventSchema {
	man.Ether = man.EffectiveEther()
	if man.Ether == nil || !man.HasCapability(CapEvents) || man.Ether.Contributes.Events == nil {
		return nil
	}
	decls := man.Ether.Contributes.Events.Publish
	if len(decls) == 0 {
		return nil
	}
	if resolve == nil {
		resolve = func(raw, fallback string) string {
			if strings.TrimSpace(raw) == "" {
				return fallback
			}
			return raw
		}
	}
	out := make([]events.EventSchema, 0, len(decls))
	for _, pe := range decls {
		ver := pe.Version
		if ver <= 0 {
			ver = 1
		}
		out = append(out, events.EventSchema{
			Type:        events.PluginEventType(pluginID, pe.Key),
			Version:     ver,
			Domain:      events.PluginPrefix + pluginID,
			Label:       resolve(pe.Label, pe.Key),
			Description: resolve(pe.Description, ""),
			Owner:       pluginID,
			Fields:      convertEventFields(pe.Fields, resolve),
		})
	}
	return out
}

// BuildPluginActionSpec builds the flows.ActionSpec for one manifest-declared
// plugin flow action.
func BuildPluginActionSpec(pluginID, group string, act FlowActionMeta, resolve func(raw, fallback string) string) flows.ActionSpec {
	if resolve == nil {
		resolve = func(raw, fallback string) string {
			if strings.TrimSpace(raw) == "" {
				return fallback
			}
			return raw
		}
	}
	params := make([]flows.ParamSchema, len(act.Params))
	for i, p := range act.Params {
		params[i] = flows.ParamSchema{
			Key:         p.Key,
			Type:        events.FieldType(p.Type),
			Label:       resolve(p.Label, p.Key),
			Description: resolve(p.Description, ""),
			Required:    p.Required,
			Secret:      p.Secret,
			Template:    p.Template,
			Default:     p.Default,
			Enum:        convertEventEnums(p.Enum, resolve),
			Unit:        p.Unit,
			Picker:      p.Picker,
		}
	}
	sideEffect := true
	if act.SideEffect != nil {
		sideEffect = *act.SideEffect
	}
	if group == "" {
		group = pluginID
	}
	return flows.ActionSpec{
		Type:        flows.PluginActionType(pluginID, act.Key),
		Owner:       pluginID,
		Group:       group,
		Label:       resolve(act.Label, act.Key),
		Description: resolve(act.Description, ""),
		Icon:        act.Icon,
		Idempotent:  act.Idempotent,
		SideEffect:  sideEffect,
		Params:      params,
		Outputs:     convertEventFields(act.Outputs, resolve),
	}
}

// ValidatePublishEventCall verifies capability, declared key, payload size,
// and payload schema against man. Exported for conformance kit and Studio sandboxes.
func ValidatePublishEventCall(man Manifest, key string, payload json.RawMessage) (PublishedEventMeta, events.EventSchema, error) {
	man.Ether = man.EffectiveEther()
	if man.Ether == nil || !man.HasCapability(CapEvents) {
		return PublishedEventMeta{}, events.EventSchema{}, fmt.Errorf("%w: %s", ErrPermissionDenied, CapEvents)
	}
	if !pluginEventKeyRe.MatchString(key) {
		return PublishedEventMeta{}, events.EventSchema{}, &ValidationError{Fields: []FieldError{{
			Field:   "key",
			Message: "must match ^[a-z][a-z0-9_]*(\\.[a-z][a-z0-9_]*){0,2}$",
		}}}
	}
	var decl *PublishedEventMeta
	if man.Ether.Contributes.Events != nil {
		for i := range man.Ether.Contributes.Events.Publish {
			if man.Ether.Contributes.Events.Publish[i].Key == key {
				decl = &man.Ether.Contributes.Events.Publish[i]
				break
			}
		}
	}
	if decl == nil {
		return PublishedEventMeta{}, events.EventSchema{}, &ValidationError{Fields: []FieldError{{
			Field:   "key",
			Message: fmt.Sprintf("event %q is not declared in ether.contributes.events.publish", key),
		}}}
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		trimmed = json.RawMessage(`{}`)
	}
	if len(trimmed) > MaxPublishedEventPayloadBytes {
		return PublishedEventMeta{}, events.EventSchema{}, &ValidationError{Fields: []FieldError{{
			Field:   "payload",
			Message: fmt.Sprintf("exceeds %d byte limit", MaxPublishedEventPayloadBytes),
		}}}
	}
	pid := man.ID
	if !idRe.MatchString(pid) {
		pid = "dev.example.plugin"
	}
	ver := decl.Version
	if ver <= 0 {
		ver = 1
	}
	schema := events.EventSchema{
		Type:        events.PluginEventType(pid, decl.Key),
		Version:     ver,
		Domain:      events.PluginPrefix + pid,
		Label:       decl.Label,
		Description: decl.Description,
		Owner:       pid,
		Fields:      convertEventFields(decl.Fields, nil),
	}
	if strings.TrimSpace(schema.Label) == "" {
		schema.Label = decl.Key
	}
	reg := events.NewRegistry()
	if err := reg.Register(schema); err != nil {
		return PublishedEventMeta{}, events.EventSchema{}, &ValidationError{Fields: []FieldError{{
			Field:   "schema",
			Message: err.Error(),
		}}}
	}
	if err := reg.ValidatePayload(schema.Type, trimmed); err != nil {
		return PublishedEventMeta{}, events.EventSchema{}, &ValidationError{Fields: []FieldError{{
			Field:   "payload",
			Message: err.Error(),
		}}}
	}
	return *decl, schema, nil
}

// syncFlowsPlugin atomically replaces or removes a plugin's published event
// schemas and flow action handlers in the wired registries.
func (m *Manager) syncFlowsPlugin(ctx context.Context, r Record) {
	reg, actions := m.flowsRegistries()
	if reg == nil && actions == nil {
		return
	}
	man := r.Manifest
	man.Ether = man.EffectiveEther()
	var g Grants
	if m.store != nil {
		if loaded, err := m.store.grants(ctx, r.ID); err == nil {
			g = loaded
		}
	}
	resolve := func(raw, fallback string) string {
		return m.resolvePluginCopy(ctx, r.ID, raw, fallback)
	}

	if reg != nil {
		if !r.Enabled || man.Ether == nil || !man.HasCapability(CapEvents) || g.revoked(string(CapEvents)) {
			reg.RemovePlugin(r.ID)
		} else {
			schemas := BuildPluginEventSchemas(r.ID, man, resolve)
			if len(schemas) == 0 {
				reg.RemovePlugin(r.ID)
			} else if err := reg.ReplacePlugin(r.ID, schemas); err != nil {
				m.log.Warn("plugins: event schema sync", "plugin", r.ID, "err", err)
			}
		}
	}

	if actions != nil {
		if !r.Enabled || man.Ether == nil || !man.HasCapability(CapFlowActions) || g.revoked(string(CapFlowActions)) {
			actions.RemovePlugin(r.ID)
		} else if len(man.Ether.Contributes.FlowActions) == 0 {
			actions.RemovePlugin(r.ID)
		} else {
			handlers := make([]flows.ActionHandler, 0, len(man.Ether.Contributes.FlowActions))
			for _, act := range man.Ether.Contributes.FlowActions {
				handlers = append(handlers, m.pluginFlowActionHandler(r, act, resolve))
			}
			if err := actions.ReplacePlugin(r.ID, handlers); err != nil {
				m.log.Warn("plugins: flow action sync", "plugin", r.ID, "err", err)
			}
		}
	}
}

func (m *Manager) pluginFlowActionHandler(r Record, act FlowActionMeta, resolve func(raw, fallback string) string) flows.ActionHandler {
	pluginID := r.ID
	spec := BuildPluginActionSpec(pluginID, r.Name, act, resolve)
	timeout := DefaultPluginFlowActionTimeout
	if act.Timeout != "" {
		if d, err := time.ParseDuration(act.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	if timeout > MaxPluginFlowActionTimeout {
		timeout = MaxPluginFlowActionTimeout
	}
	secretOutputs := map[string]bool{}
	for _, out := range act.Outputs {
		if out.Secret {
			secretOutputs[out.Name] = true
		}
	}

	return flows.ActionFunc{
		ActionSpec: spec,
		Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
			live, err := m.store.record(ctx, pluginID)
			if err != nil {
				return flows.ActionResult{}, err
			}
			if !live.Enabled {
				return flows.ActionResult{}, ErrDisabled
			}
			man := live.Manifest
			man.Ether = man.EffectiveEther()
			g, err := m.store.grants(ctx, pluginID)
			if err != nil {
				return flows.ActionResult{}, err
			}
			if g.revoked(string(CapFlowActions)) || man.Ether == nil || !man.HasCapability(CapFlowActions) {
				(&host{m: m, plugin: pluginID}).noteDenied(string(CapFlowActions))
				return flows.ActionResult{}, fmt.Errorf("%w: %s", ErrPermissionDenied, CapFlowActions)
			}

			route := ""
			for _, declared := range man.Ether.Contributes.FlowActions {
				if declared.Key == act.Key {
					route = declared.Route
					break
				}
			}
			if route == "" {
				return flows.ActionResult{}, fmt.Errorf("%w: flow action %q is no longer declared", ErrNotFound, act.Key)
			}

			if params == nil {
				params = map[string]any{}
			}
			reqPayload := map[string]any{
				"flowId":         actx.FlowID,
				"runId":          actx.RunID,
				"nodeId":         actx.NodeID,
				"idempotencyKey": actx.IdempotencyKey,
				"dryRun":         actx.DryRun,
				"params":         params,
			}

			if live.Shadow {
				inv, _ := json.Marshal(reqPayload)
				invRedacted := m.store.redactKnown(ctx, pluginID, redactJSON(inv))
				_, _ = m.store.journal(ctx, JournalEntry{
					Plugin:  pluginID,
					Op:      "flow_action",
					Detail:  act.Key,
					Shadow:  true,
					Inverse: json.RawMessage(invRedacted),
				})
				return flows.ActionResult{
					Output: map[string]any{
						"shadow": true,
						"action": spec.Type,
					},
				}, nil
			}

			body, err := json.Marshal(reqPayload)
			if err != nil {
				return flows.ActionResult{}, fmt.Errorf("plugins: marshal flow action request: %w", err)
			}
			headers := map[string]string{}
			if actx.IdempotencyKey != "" {
				headers[HeaderIdempotencyKey] = actx.IdempotencyKey
			}

			callCtx, cancel := context.WithTimeout(withFlowActionOrigin(ctx, actx.FlowDepth+1, actx.RunID), timeout)
			defer cancel()

			resp, err := m.Call(callCtx, pluginID, Request{
				Method:  http.MethodPost,
				Path:    route,
				Headers: headers,
				Body:    body,
			})
			if err != nil {
				return flows.ActionResult{}, err
			}
			if resp.Status < 200 || resp.Status >= 300 {
				return flows.ActionResult{}, m.formatActionError(ctx, pluginID, act.Key, resp)
			}
			if len(resp.Body) > MaxPluginFlowActionResponseBytes {
				return flows.ActionResult{}, fmt.Errorf("plugins: flow action %s response exceeds %d byte limit", spec.Type, MaxPluginFlowActionResponseBytes)
			}
			return m.decodeActionResult(ctx, pluginID, resp.Body, secretOutputs)
		},
	}
}

func (m *Manager) formatActionError(ctx context.Context, pluginID, actionKey string, resp Response) error {
	msg := ""
	raw := unwrapJSONStringBody(resp.Body)
	if len(raw) > 0 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &env) == nil {
			switch {
			case env.Error.Message != "":
				msg = env.Error.Message
			case env.Message != "":
				msg = env.Message
			}
		}
	}
	if msg != "" {
		msg = logredact.String(m.store.redactKnown(ctx, pluginID, msg))
		return fmt.Errorf("plugins: flow action %s/%s failed with status %d: %s", pluginID, actionKey, resp.Status, truncate(msg, 240))
	}
	return fmt.Errorf("plugins: flow action %s/%s failed with status %d", pluginID, actionKey, resp.Status)
}

func (m *Manager) decodeActionResult(ctx context.Context, pluginID string, raw json.RawMessage, secretOutputs map[string]bool) (flows.ActionResult, error) {
	body := unwrapJSONStringBody(raw)
	if len(body) == 0 {
		return flows.ActionResult{Output: map[string]any{}}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return flows.ActionResult{}, fmt.Errorf("plugins: flow action returned invalid JSON object: %w", err)
	}
	if doc == nil {
		return flows.ActionResult{Output: map[string]any{}}, nil
	}

	var res flows.ActionResult
	if branch, ok := doc["branch"].(string); ok {
		res.Branch = branch
	}
	if inner, ok := doc["output"].(map[string]any); ok {
		res.Output = inner
	} else {
		res.Output = doc
	}
	if res.Output == nil {
		res.Output = map[string]any{}
	}

	encoded, err := json.Marshal(res.Output)
	if err != nil {
		return flows.ActionResult{}, fmt.Errorf("plugins: flow action output could not be encoded: %w", err)
	}
	clean := m.store.redactKnown(ctx, pluginID, string(encoded))
	if err := json.Unmarshal([]byte(clean), &res.Output); err != nil {
		return flows.ActionResult{}, fmt.Errorf("plugins: flow action output could not be safely decoded: %w", err)
	}
	redactValue(res.Output)
	for field := range secretOutputs {
		maskNestedField(res.Output, field)
	}
	return res, nil
}

func unwrapJSONStringBody(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return bytes.TrimSpace([]byte(s))
		}
	}
	return trimmed
}

func maskNestedField(doc map[string]any, path string) {
	if doc == nil || path == "" {
		return
	}
	parts := strings.Split(path, ".")
	cur := doc
	for i := 0; i < len(parts)-1; i++ {
		next, ok := cur[parts[i]].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	last := parts[len(parts)-1]
	if _, exists := cur[last]; exists {
		cur[last] = events.RedactedValue
	}
}

// PublishEvent emits a declared plugin event onto Ether's event bus.
func (h *host) PublishEvent(ctx context.Context, key string, payload json.RawMessage, corr events.Correlation) error {
	if err := h.requireWritableToolContext(ctx); err != nil {
		return err
	}
	r, g, err := h.record(ctx)
	if err != nil {
		return err
	}
	man := r.Manifest
	man.Ether = man.EffectiveEther()
	if g.revoked(string(CapEvents)) || man.Ether == nil || !man.HasCapability(CapEvents) {
		h.noteDenied(string(CapEvents))
		return fmt.Errorf("%w: %s", ErrPermissionDenied, CapEvents)
	}

	_, schema, err := ValidatePublishEventCall(man, key, payload)
	if err != nil {
		if errors.Is(err, ErrPermissionDenied) {
			h.noteDenied(string(CapEvents))
		}
		return err
	}

	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		trimmed = json.RawMessage(`{}`)
	}

	h.m.mu.Lock()
	limit := h.m.eventPublishLimit
	h.m.mu.Unlock()
	if limit <= 0 {
		limit = DefaultEventPublishPerMin
	}
	if !h.m.rateOK(h.m.eventPublishTimes, h.plugin, limit, time.Minute) {
		return fmt.Errorf("%w: event publishing is limited to %d per minute", ErrRateLimited, limit)
	}

	if corr.PluginID == "" {
		corr.PluginID = h.plugin
	}

	if r.Shadow {
		inv, _ := json.Marshal(map[string]any{
			"key":         key,
			"type":        schema.Type,
			"payload":     json.RawMessage(trimmed),
			"correlation": corr,
		})
		invRedacted := h.m.store.redactKnown(ctx, h.plugin, redactJSON(inv))
		_, _ = h.m.store.journal(ctx, JournalEntry{
			Plugin:  h.plugin,
			Op:      "events.publish",
			Detail:  key,
			Shadow:  true,
			Inverse: json.RawMessage(invRedacted),
		})
		return nil
	}

	bus := h.m.flowsEventBus()
	if bus == nil {
		return ErrUnavailable
	}

	var meta events.EnvelopeMeta
	if origin, ok := flowActionOriginFromContext(ctx); ok {
		meta.FlowDepth = origin.depth
		meta.OriginRunID = origin.originRunID
	}

	env := events.Envelope{
		ID:          events.NewID(),
		Type:        events.PluginEventType(h.plugin, key),
		Version:     schema.Version,
		OccurredAt:  h.m.now().UTC(),
		Source:      events.PluginPrefix + h.plugin,
		Correlation: corr,
		Meta:        meta,
		Payload:     append(json.RawMessage(nil), trimmed...),
	}
	bus.Publish(env)
	return nil
}
