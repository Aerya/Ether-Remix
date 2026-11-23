package flows

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/events"
)

// Canonical console editor picker IDs for ParamSchema.Picker.
// Keep in sync with FLOW_PICKER in web/src/pages/flows/types.ts and
// FlowActionParamMeta.picker in sdk/typescript/src/types.ts
// (enforced by TestPickerConstantsMatchWeb).
const (
	PickerChannel = "channel"
	PickerViewer  = "viewer"
	PickerLibrary = "library"
	PickerSource  = "source"
	PickerEvent   = "event"
	PickerPlugin  = "plugin"
)

// AllPickers lists every canonical ParamSchema.Picker value.
var AllPickers = []string{
	PickerChannel,
	PickerViewer,
	PickerLibrary,
	PickerSource,
	PickerEvent,
	PickerPlugin,
}

// ValidPicker reports whether picker is empty or one of the canonical picker IDs.
func ValidPicker(picker string) bool {
	switch picker {
	case "", PickerChannel, PickerViewer, PickerLibrary, PickerSource, PickerEvent, PickerPlugin:
		return true
	default:
		return false
	}
}

// ParamSchema defines one configurable parameter of a Flow action or manual trigger.
type ParamSchema struct {
	Key         string              `json:"key"`
	Type        events.FieldType    `json:"type"`
	Label       string              `json:"label"`
	Description string              `json:"description,omitempty"`
	Required    bool                `json:"required,omitempty"`
	Secret      bool                `json:"secret,omitempty"`
	Template    bool                `json:"template,omitempty"`
	Default     any                 `json:"default,omitempty"`
	Enum        []events.EnumOption `json:"enum,omitempty"`
	Unit        string              `json:"unit,omitempty"`
	// Picker is an optional console editor hint (one of the Picker* constants).
	Picker string `json:"picker,omitempty"`
}

// HasEnumValue reports whether v is one of p.Enum's values.
func (p ParamSchema) HasEnumValue(v string) bool {
	for _, opt := range p.Enum {
		if opt.Value == v {
			return true
		}
	}
	return false
}

// ActionSpec describes one action available in the Flow editor and runtime.
type ActionSpec struct {
	Type        string               `json:"type"`
	Owner       string               `json:"owner"`
	Group       string               `json:"group"`
	Label       string               `json:"label"`
	Description string               `json:"description,omitempty"`
	Icon        string               `json:"icon,omitempty"`
	Idempotent  bool                 `json:"idempotent,omitempty"`
	SideEffect  bool                 `json:"sideEffect,omitempty"`
	Params      []ParamSchema        `json:"params,omitempty"`
	Outputs     []events.FieldSchema `json:"outputs,omitempty"`
}

var (
	builtinActionRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
	pluginActionRe  = regexp.MustCompile(`^plugin:([a-z0-9]+(?:\.[a-z0-9][a-z0-9-]*)+)/([a-z][a-z0-9_]{0,63})$`)
)

// PluginActionType formats a plugin-namespaced action type ("plugin:<pluginId>/<key>").
func PluginActionType(pluginID, key string) string {
	return "plugin:" + pluginID + "/" + key
}

// Validate checks that s is well-formed.
func (s ActionSpec) Validate() error {
	if s.Type == "" || len(s.Type) > 128 {
		return fmt.Errorf("flows: invalid action type %q", s.Type)
	}
	if strings.HasPrefix(s.Type, "plugin:") {
		m := pluginActionRe.FindStringSubmatch(s.Type)
		if m == nil {
			return fmt.Errorf("flows: plugin action type %q must be plugin:<pluginId>/<key>", s.Type)
		}
		if s.Owner != m[1] {
			return fmt.Errorf("flows: plugin action %q owner %q must match plugin id", s.Type, s.Owner)
		}
	} else {
		if !builtinActionRe.MatchString(s.Type) {
			return fmt.Errorf("flows: built-in action type %q must match domain.action", s.Type)
		}
		if s.Owner != "" && s.Owner != events.OwnerBuiltin {
			return fmt.Errorf("flows: built-in action %q owner must be %q", s.Type, events.OwnerBuiltin)
		}
	}
	if strings.TrimSpace(s.Label) == "" {
		return fmt.Errorf("flows: action %q label is required", s.Type)
	}
	seen := make(map[string]bool, len(s.Params))
	for _, p := range s.Params {
		if strings.TrimSpace(p.Key) == "" {
			return fmt.Errorf("flows: action %q has param with empty key", s.Type)
		}
		if seen[p.Key] {
			return fmt.Errorf("flows: action %q has duplicate param %q", s.Type, p.Key)
		}
		seen[p.Key] = true
		if !p.Type.Valid() {
			return fmt.Errorf("flows: action %q param %q has invalid type %q", s.Type, p.Key, p.Type)
		}
		if p.Type == events.FieldEnum && len(p.Enum) == 0 {
			return fmt.Errorf("flows: action %q enum param %q requires enum values", s.Type, p.Key)
		}
		if !ValidPicker(p.Picker) {
			return fmt.Errorf("flows: action %q param %q has unknown picker %q", s.Type, p.Key, p.Picker)
		}
	}
	return nil
}

// Param looks up a parameter schema by key.
func (s ActionSpec) Param(key string) (ParamSchema, bool) {
	for _, p := range s.Params {
		if p.Key == key {
			return p, true
		}
	}
	return ParamSchema{}, false
}

func (s ActionSpec) clone() ActionSpec {
	out := s
	if s.Params != nil {
		out.Params = make([]ParamSchema, len(s.Params))
		for i, p := range s.Params {
			if p.Enum != nil {
				p.Enum = slices.Clone(p.Enum)
			}
			out.Params[i] = p
		}
	}
	if s.Outputs != nil {
		out.Outputs = slices.Clone(s.Outputs)
	}
	return out
}

// ActionContext provides execution metadata and variable mutation to ActionHandler.Execute.
type ActionContext struct {
	FlowID         string
	RunID          string
	NodeID         string
	IdempotencyKey string
	DryRun         bool
	FlowDepth      int
	Event          events.Envelope
	Vars           map[string]any
	SetVar         func(key string, val any)
}

// ActionResult is returned by an ActionHandler.Execute call.
type ActionResult struct {
	Output map[string]any `json:"output,omitempty"`
	Branch string         `json:"branch,omitempty"` // defaults to HandleNext
}

// ActionHandler implements one executable Flow action.
type ActionHandler interface {
	Spec() ActionSpec
	Execute(ctx context.Context, actx ActionContext, params map[string]any) (ActionResult, error)
}

// ActionFunc adapts a Spec + function into an ActionHandler.
type ActionFunc struct {
	ActionSpec ActionSpec
	Run        func(ctx context.Context, actx ActionContext, params map[string]any) (ActionResult, error)
}

func (f ActionFunc) Spec() ActionSpec { return f.ActionSpec }

func (f ActionFunc) Execute(ctx context.Context, actx ActionContext, params map[string]any) (ActionResult, error) {
	if f.Run == nil {
		return ActionResult{}, errors.New("flows: action handler not implemented")
	}
	return f.Run(ctx, actx, params)
}

// ErrDuplicateAction is returned when registering an action type that already exists.
var ErrDuplicateAction = errors.New("flows: action already registered")

// ActionRegistry is the lookup and registration contract for built-in and plugin actions.
type ActionRegistry interface {
	Register(h ActionHandler) error
	MustRegister(h ActionHandler)
	ReplacePlugin(pluginID string, handlers []ActionHandler) error
	RemovePlugin(pluginID string)
	Get(actionType string) (ActionHandler, bool)
	Spec(actionType string) (ActionSpec, bool)
	List() []ActionSpec
}

// InMemoryActionRegistry is the thread-safe default ActionRegistry implementation.
type InMemoryActionRegistry struct {
	mu       sync.RWMutex
	handlers map[string]ActionHandler
	specs    map[string]ActionSpec
}

// NewActionRegistry returns an empty ActionRegistry.
func NewActionRegistry() *InMemoryActionRegistry {
	return &InMemoryActionRegistry{
		handlers: map[string]ActionHandler{},
		specs:    map[string]ActionSpec{},
	}
}

func (r *InMemoryActionRegistry) Register(h ActionHandler) error {
	if h == nil {
		return errors.New("flows: nil action handler")
	}
	spec := h.Spec()
	if spec.Owner == "" && !strings.HasPrefix(spec.Type, "plugin:") {
		spec.Owner = events.OwnerBuiltin
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[spec.Type]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateAction, spec.Type)
	}
	r.handlers[spec.Type] = h
	r.specs[spec.Type] = spec.clone()
	return nil
}

func (r *InMemoryActionRegistry) MustRegister(h ActionHandler) {
	if err := r.Register(h); err != nil {
		panic(err)
	}
}

func (r *InMemoryActionRegistry) ReplacePlugin(pluginID string, handlers []ActionHandler) error {
	if pluginID == "" || pluginID == events.OwnerBuiltin {
		return fmt.Errorf("flows: invalid plugin id %q", pluginID)
	}
	prepared := make([]ActionSpec, 0, len(handlers))
	seen := make(map[string]bool, len(handlers))
	for _, h := range handlers {
		if h == nil {
			return errors.New("flows: nil plugin action handler")
		}
		spec := h.Spec()
		spec.Owner = pluginID
		if err := spec.Validate(); err != nil {
			return err
		}
		if seen[spec.Type] {
			return fmt.Errorf("%w: %s", ErrDuplicateAction, spec.Type)
		}
		seen[spec.Type] = true
		prepared = append(prepared, spec.clone())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, spec := range r.specs {
		if spec.Owner == pluginID {
			delete(r.specs, k)
			delete(r.handlers, k)
		}
	}
	for i, spec := range prepared {
		r.specs[spec.Type] = spec
		r.handlers[spec.Type] = handlers[i]
	}
	return nil
}

func (r *InMemoryActionRegistry) RemovePlugin(pluginID string) {
	if pluginID == "" || pluginID == events.OwnerBuiltin {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, spec := range r.specs {
		if spec.Owner == pluginID {
			delete(r.specs, k)
			delete(r.handlers, k)
		}
	}
}

func (r *InMemoryActionRegistry) Get(actionType string) (ActionHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[actionType]
	return h, ok
}

func (r *InMemoryActionRegistry) Spec(actionType string) (ActionSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.specs[actionType]
	if !ok {
		return ActionSpec{}, false
	}
	return s.clone(), true
}

func (r *InMemoryActionRegistry) List() []ActionSpec {
	r.mu.RLock()
	out := make([]ActionSpec, 0, len(r.specs))
	for _, s := range r.specs {
		out = append(out, s.clone())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// TriggerSpec describes one trigger node kind for the console palette and validator.
type TriggerSpec struct {
	Kind        NodeKind `json:"kind"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Icon        string   `json:"icon,omitempty"`
}

// TriggerRegistry lists supported trigger node kinds.
type TriggerRegistry interface {
	Register(spec TriggerSpec) error
	Get(kind NodeKind) (TriggerSpec, bool)
	List() []TriggerSpec
}

// InMemoryTriggerRegistry is the default thread-safe TriggerRegistry.
type InMemoryTriggerRegistry struct {
	mu    sync.RWMutex
	specs map[NodeKind]TriggerSpec
}

// NewTriggerRegistry creates a TriggerRegistry pre-populated with Ether's
// four built-in triggers (event, schedule, webhook, manual).
func NewTriggerRegistry() *InMemoryTriggerRegistry {
	r := &InMemoryTriggerRegistry{specs: map[NodeKind]TriggerSpec{}}
	for _, spec := range BuiltinTriggerSpecs() {
		_ = r.Register(spec)
	}
	return r
}

// BuiltinTriggerSpecs returns Ether's four built-in trigger descriptors.
func BuiltinTriggerSpecs() []TriggerSpec {
	return []TriggerSpec{
		{
			Kind:        NodeTriggerEvent,
			Label:       "Event",
			Description: "Start when one or more Ether or plugin events occur.",
			Icon:        "zap",
		},
		{
			Kind:        NodeTriggerSchedule,
			Label:       "Schedule",
			Description: "Start on a recurring cron expression or fixed interval.",
			Icon:        "clock",
		},
		{
			Kind:        NodeTriggerWebhook,
			Label:       "Incoming webhook",
			Description: "Start when an external HTTP POST calls this flow's webhook URL.",
			Icon:        "webhook",
		},
		{
			Kind:        NodeTriggerManual,
			Label:       "Manual",
			Description: "Start on demand from the console or API.",
			Icon:        "play",
		},
	}
}

func (r *InMemoryTriggerRegistry) Register(spec TriggerSpec) error {
	if spec.Kind == "" || !spec.Kind.IsTrigger() {
		return fmt.Errorf("flows: invalid trigger kind %q", spec.Kind)
	}
	if strings.TrimSpace(spec.Label) == "" {
		return fmt.Errorf("flows: trigger %q label is required", spec.Kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[spec.Kind]; exists {
		return fmt.Errorf("flows: trigger %q already registered", spec.Kind)
	}
	r.specs[spec.Kind] = spec
	return nil
}

func (r *InMemoryTriggerRegistry) Get(kind NodeKind) (TriggerSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.specs[kind]
	return s, ok
}

func (r *InMemoryTriggerRegistry) List() []TriggerSpec {
	r.mu.RLock()
	out := make([]TriggerSpec, 0, len(r.specs))
	for _, s := range r.specs {
		out = append(out, s)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
