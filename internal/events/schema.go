package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// FieldType classifies a typed field in an event payload or flow action parameter.
type FieldType string

const (
	FieldString     FieldType = "string"
	FieldInt        FieldType = "int"
	FieldFloat      FieldType = "float"
	FieldBool       FieldType = "bool"
	FieldDurationMS FieldType = "duration_ms"
	FieldTimestamp  FieldType = "timestamp"
	FieldEnum       FieldType = "enum"
	FieldStringList FieldType = "string_list"
	FieldObject     FieldType = "object"
)

// Valid reports whether t is a recognized FieldType.
func (t FieldType) Valid() bool {
	switch t {
	case FieldString, FieldInt, FieldFloat, FieldBool, FieldDurationMS,
		FieldTimestamp, FieldEnum, FieldStringList, FieldObject:
		return true
	default:
		return false
	}
}

// EnumOption describes one allowed value of a FieldEnum field.
type EnumOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// FieldSchema describes one typed field inside an event payload.
type FieldSchema struct {
	Name        string       `json:"name"`
	Type        FieldType    `json:"type"`
	Label       string       `json:"label"`
	Description string       `json:"description,omitempty"`
	Unit        string       `json:"unit,omitempty"`
	Enum        []EnumOption `json:"enum,omitempty"`
	PII         bool         `json:"pii,omitempty"`
	Secret      bool         `json:"secret,omitempty"`
	Filterable  bool         `json:"filterable,omitempty"`
	Example     any          `json:"example,omitempty"`
}

// HasEnumValue reports whether v is one of the field's declared enum values.
func (f FieldSchema) HasEnumValue(v string) bool {
	for _, opt := range f.Enum {
		if opt.Value == v {
			return true
		}
	}
	return false
}

// EventSchema defines one event type in the catalogue: its metadata, version,
// typed payload fields, legacy aliases, and sample payload.
type EventSchema struct {
	Type        string        `json:"type"`
	Version     int           `json:"version"`
	Domain      string        `json:"domain"`
	Label       string        `json:"label"`
	Description string        `json:"description,omitempty"`
	Owner       string        `json:"owner"`
	Critical    bool          `json:"critical,omitempty"`
	Deprecated  bool          `json:"deprecated,omitempty"`
	ReplacedBy  string        `json:"replacedBy,omitempty"`
	LegacyNames []string      `json:"legacyNames,omitempty"`
	Fields      []FieldSchema `json:"fields"`
	Sample      any           `json:"sample,omitempty"`
}

// Validate checks that s is well-formed and self-consistent.
func (s EventSchema) Validate() error {
	if err := ValidateType(s.Type); err != nil {
		return err
	}
	if s.Version < 1 {
		return fmt.Errorf("events: schema %q version must be >= 1", s.Type)
	}
	if strings.TrimSpace(s.Label) == "" {
		return fmt.Errorf("events: schema %q label is required", s.Type)
	}
	if s.Owner == "" {
		return fmt.Errorf("events: schema %q owner is required", s.Type)
	}
	if IsPluginType(s.Type) {
		pid, _, ok := ParsePluginEventType(s.Type)
		if !ok || s.Owner != pid {
			return fmt.Errorf("events: plugin schema %q owner %q must match plugin id", s.Type, s.Owner)
		}
	} else if s.Owner != OwnerBuiltin {
		return fmt.Errorf("events: non-plugin schema %q must have owner %q", s.Type, OwnerBuiltin)
	}
	seen := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		if strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("events: schema %q has field with empty name", s.Type)
		}
		if seen[f.Name] {
			return fmt.Errorf("events: schema %q has duplicate field %q", s.Type, f.Name)
		}
		seen[f.Name] = true
		if !f.Type.Valid() {
			return fmt.Errorf("events: schema %q field %q has invalid type %q", s.Type, f.Name, f.Type)
		}
		if f.Type == FieldEnum && len(f.Enum) == 0 {
			return fmt.Errorf("events: schema %q enum field %q requires enum values", s.Type, f.Name)
		}
	}
	return nil
}

// Field returns the schema for a dotted field path inside the event payload.
func (s EventSchema) Field(path string) (FieldSchema, bool) {
	for _, f := range s.Fields {
		if f.Name == path {
			return f, true
		}
	}
	return FieldSchema{}, false
}

// RedactOptions controls how EventSchema.Redact sanitizes a payload map.
type RedactOptions struct {
	// StripPII removes fields marked PII: true (used for plugins lacking viewer identity grants).
	StripPII bool
}

// Redact returns a deep copy of payload with Secret fields masked and
// (when opts.StripPII is set) PII fields removed.
func (s EventSchema) Redact(payload map[string]any, opts RedactOptions) map[string]any {
	out := deepCopyMap(payload)
	if out == nil {
		return map[string]any{}
	}
	for _, f := range s.Fields {
		switch {
		case f.Secret:
			setOrDeletePath(out, f.Name, RedactedValue, false)
		case opts.StripPII && f.PII:
			setOrDeletePath(out, f.Name, nil, true)
		}
	}
	return out
}

func (s EventSchema) clone() EventSchema {
	out := s
	if s.LegacyNames != nil {
		out.LegacyNames = slices.Clone(s.LegacyNames)
	}
	if s.Fields != nil {
		out.Fields = make([]FieldSchema, len(s.Fields))
		for i, f := range s.Fields {
			if f.Enum != nil {
				f.Enum = slices.Clone(f.Enum)
			}
			out.Fields[i] = f
		}
	}
	return out
}

// ErrDuplicateSchema is returned when registering an event type that already exists.
var ErrDuplicateSchema = errors.New("events: schema already registered")

// Registry stores typed EventSchema definitions for built-in and plugin events.
// It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	schemas map[string]EventSchema
	legacy  map[string]string // legacy alias -> canonical event type
	version uint64
}

// NewRegistry returns an empty event schema registry.
func NewRegistry() *Registry {
	return &Registry{
		schemas: map[string]EventSchema{},
		legacy:  map[string]string{},
	}
}

// Register adds a single EventSchema to the registry.
func (r *Registry) Register(s EventSchema) error {
	s = normalizeSchema(s)
	if err := s.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.schemas[s.Type]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateSchema, s.Type)
	}
	r.schemas[s.Type] = s.clone()
	for _, alias := range s.LegacyNames {
		if alias != "" {
			r.legacy[alias] = s.Type
		}
	}
	r.version++
	return nil
}

// MustRegister registers s and panics on error (intended for static built-in schemas).
func (r *Registry) MustRegister(s EventSchema) {
	if err := r.Register(s); err != nil {
		panic(err)
	}
}

// RegisterStruct reflects payload struct type T (reading `json` and `event` tags),
// merges any explicit meta.Fields overrides, and registers the resulting schema.
func RegisterStruct[T any](r *Registry, meta EventSchema) error {
	var zero T
	rt := reflect.TypeOf(zero)
	if rt == nil {
		return fmt.Errorf("events: RegisterStruct %q requires a struct type", meta.Type)
	}
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return fmt.Errorf("events: RegisterStruct %q requires a struct type, got %s", meta.Type, rt.Kind())
	}
	reflected := fieldsFromStruct(rt, "")
	meta.Fields = mergeFields(reflected, meta.Fields)
	if meta.Sample == nil {
		meta.Sample = sampleFromFields(meta.Fields)
	}
	return r.Register(meta)
}

// ReplacePlugin atomically swaps every EventSchema owned by pluginID.
func (r *Registry) ReplacePlugin(pluginID string, schemas []EventSchema) error {
	if !pluginIDRe.MatchString(pluginID) {
		return fmt.Errorf("events: invalid plugin id %q", pluginID)
	}
	prepared := make([]EventSchema, 0, len(schemas))
	seen := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		s.Owner = pluginID
		s = normalizeSchema(s)
		if err := s.Validate(); err != nil {
			return err
		}
		if seen[s.Type] {
			return fmt.Errorf("%w: %s", ErrDuplicateSchema, s.Type)
		}
		seen[s.Type] = true
		prepared = append(prepared, s.clone())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, existing := range r.schemas {
		if existing.Owner == pluginID {
			delete(r.schemas, k)
		}
	}
	for _, s := range prepared {
		r.schemas[s.Type] = s
	}
	r.version++
	return nil
}

// RemovePlugin removes every EventSchema owned by pluginID.
func (r *Registry) RemovePlugin(pluginID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for k, existing := range r.schemas {
		if existing.Owner == pluginID && pluginID != OwnerBuiltin {
			delete(r.schemas, k)
			changed = true
		}
	}
	if changed {
		r.version++
	}
}

// Get returns the EventSchema for eventType.
func (r *Registry) Get(eventType string) (EventSchema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[eventType]
	if !ok {
		return EventSchema{}, false
	}
	return s.clone(), true
}

// GetByLegacy resolves a legacy notify or plugin event name to its canonical schema.
func (r *Registry) GetByLegacy(legacyName string) (EventSchema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	canonical, ok := r.legacy[legacyName]
	if !ok {
		return EventSchema{}, false
	}
	s, ok := r.schemas[canonical]
	if !ok {
		return EventSchema{}, false
	}
	return s.clone(), true
}

// List returns all registered schemas sorted by domain then type.
func (r *Registry) List() []EventSchema {
	r.mu.RLock()
	out := make([]EventSchema, 0, len(r.schemas))
	for _, s := range r.schemas {
		out = append(out, s.clone())
	}
	r.mu.RUnlock()
	sortSchemas(out)
	return out
}

// ListByDomain returns all registered schemas in domain, sorted by type.
func (r *Registry) ListByDomain(domain string) []EventSchema {
	r.mu.RLock()
	var out []EventSchema
	for _, s := range r.schemas {
		if s.Domain == domain {
			out = append(out, s.clone())
		}
	}
	r.mu.RUnlock()
	sortSchemas(out)
	return out
}

// Domains returns the sorted distinct domains currently registered.
func (r *Registry) Domains() []string {
	r.mu.RLock()
	seen := make(map[string]bool)
	for _, s := range r.schemas {
		seen[s.Domain] = true
	}
	r.mu.RUnlock()
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Version monotonically increments on every registry mutation.
func (r *Registry) Version() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// ValidatePayload validates a JSON payload against the registered EventSchema.
// Unknown extra fields are permitted so additive schema changes stay backwards-compatible.
func (r *Registry) ValidatePayload(eventType string, raw json.RawMessage) error {
	if !isJSONObject(raw) {
		return fmt.Errorf("events: payload for %q must be a JSON object", eventType)
	}
	schema, ok := r.Get(eventType)
	if !ok {
		return fmt.Errorf("events: unknown event type %q", eventType)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("events: decode payload for %q: %w", eventType, err)
	}
	for _, f := range schema.Fields {
		val, present := lookupDotPath(doc, f.Name)
		if !present || val == nil {
			continue
		}
		if err := validateFieldValue(f, val); err != nil {
			return fmt.Errorf("events: %s.%s: %w", eventType, f.Name, err)
		}
	}
	return nil
}

func validateFieldValue(f FieldSchema, val any) error {
	switch f.Type {
	case FieldString:
		if _, ok := val.(string); !ok {
			return fmt.Errorf("expected string, got %T", val)
		}
	case FieldEnum:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected enum string, got %T", val)
		}
		if s != "" && !f.HasEnumValue(s) {
			return fmt.Errorf("value %q is not a valid enum option", s)
		}
	case FieldInt, FieldDurationMS:
		switch n := val.(type) {
		case json.Number:
			if _, err := n.Int64(); err != nil {
				return fmt.Errorf("expected integer, got %q", n.String())
			}
		case float64:
			if n != float64(int64(n)) {
				return fmt.Errorf("expected integer, got %v", n)
			}
		default:
			return fmt.Errorf("expected integer, got %T", val)
		}
	case FieldFloat:
		switch n := val.(type) {
		case json.Number:
			if _, err := n.Float64(); err != nil {
				return fmt.Errorf("expected number, got %q", n.String())
			}
		case float64:
		default:
			return fmt.Errorf("expected number, got %T", val)
		}
	case FieldBool:
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("expected bool, got %T", val)
		}
	case FieldTimestamp:
		switch v := val.(type) {
		case string:
			if v != "" {
				if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
					if _, err2 := time.Parse(time.RFC3339, v); err2 != nil {
						return fmt.Errorf("expected RFC3339 timestamp, got %q", v)
					}
				}
			}
		case json.Number:
			if _, err := v.Int64(); err != nil {
				return fmt.Errorf("expected timestamp, got %q", v.String())
			}
		default:
			return fmt.Errorf("expected timestamp string or unix integer, got %T", val)
		}
	case FieldStringList:
		items, ok := val.([]any)
		if !ok {
			return fmt.Errorf("expected string list, got %T", val)
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("expected string element in list, got %T", item)
			}
		}
	case FieldObject:
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("expected object, got %T", val)
		}
	}
	return nil
}

func normalizeSchema(s EventSchema) EventSchema {
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Owner == "" {
		if pid, _, ok := ParsePluginEventType(s.Type); ok {
			s.Owner = pid
		} else {
			s.Owner = OwnerBuiltin
		}
	}
	if s.Domain == "" {
		s.Domain = DomainOf(s.Type)
	}
	return s
}

func sortSchemas(out []EventSchema) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Type < out[j].Type
	})
}

var (
	timeType     = reflect.TypeFor[time.Time]()
	durationType = reflect.TypeFor[time.Duration]()
	rawMsgType   = reflect.TypeFor[json.RawMessage]()
)

func fieldsFromStruct(rt reflect.Type, prefix string) []FieldSchema {
	var out []FieldSchema
	for i := range rt.NumField() {
		sf := rt.Field(i)
		if !sf.IsExported() {
			continue
		}
		jsonName, skip := parseJSONFieldName(sf)
		if skip {
			continue
		}
		ft := sf.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		// Flatten anonymous embedded structs just like encoding/json.
		if sf.Anonymous && jsonName == "" && ft.Kind() == reflect.Struct && ft != timeType {
			out = append(out, fieldsFromStruct(ft, prefix)...)
			continue
		}
		if jsonName == "" {
			jsonName = sf.Name
		}
		fullName := jsonName
		if prefix != "" {
			fullName = prefix + "." + jsonName
		}
		// Recurse into nested named structs unless explicitly typed as object or time.Time.
		tag := parseEventTag(sf.Tag.Get("event"))
		if ft.Kind() == reflect.Struct && ft != timeType && !tag.asObject {
			out = append(out, fieldsFromStruct(ft, fullName)...)
			continue
		}
		fieldType := inferFieldType(ft, tag)
		label := tag.label
		if label == "" {
			label = humanizeFieldName(jsonName)
		}
		filterable := fieldType != FieldObject
		if tag.noFilter {
			filterable = false
		}
		fs := FieldSchema{
			Name:        fullName,
			Type:        fieldType,
			Label:       label,
			Description: tag.desc,
			Unit:        tag.unit,
			Enum:        tag.enum,
			PII:         tag.pii,
			Secret:      tag.secret,
			Filterable:  filterable,
			Example:     parseExample(fieldType, tag.example),
		}
		out = append(out, fs)
	}
	return out
}

type parsedEventTag struct {
	label      string
	desc       string
	unit       string
	enum       []EnumOption
	pii        bool
	secret     bool
	durationMS bool
	asObject   bool
	noFilter   bool
	example    string
}

func parseEventTag(raw string) parsedEventTag {
	var t parsedEventTag
	if raw == "" {
		return t
	}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, hasVal := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "label":
			t.label = v
		case "desc", "description":
			t.desc = v
		case "unit":
			t.unit = v
		case "example":
			t.example = v
		case "enum":
			if hasVal && v != "" {
				for _, opt := range strings.Split(v, "|") {
					opt = strings.TrimSpace(opt)
					if opt != "" {
						t.enum = append(t.enum, EnumOption{Value: opt})
					}
				}
			}
		case "pii":
			t.pii = true
		case "secret":
			t.secret = true
		case "duration_ms":
			t.durationMS = true
		case "object":
			t.asObject = true
		case "nofilter":
			t.noFilter = true
		}
	}
	return t
}

func parseJSONFieldName(sf reflect.StructField) (name string, skip bool) {
	tag := sf.Tag.Get("json")
	if tag == "-" {
		return "", true
	}
	if tag == "" {
		if sf.Anonymous {
			return "", false
		}
		return sf.Name, false
	}
	head, _, _ := strings.Cut(tag, ",")
	if head == "" {
		if sf.Anonymous {
			return "", false
		}
		return sf.Name, false
	}
	return head, false
}

func inferFieldType(ft reflect.Type, tag parsedEventTag) FieldType {
	if tag.asObject {
		return FieldObject
	}
	if len(tag.enum) > 0 {
		return FieldEnum
	}
	if tag.durationMS {
		return FieldDurationMS
	}
	if ft == timeType {
		return FieldTimestamp
	}
	if ft == durationType {
		return FieldDurationMS
	}
	if ft == rawMsgType {
		return FieldObject
	}
	switch ft.Kind() {
	case reflect.String:
		return FieldString
	case reflect.Bool:
		return FieldBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return FieldInt
	case reflect.Float32, reflect.Float64:
		return FieldFloat
	case reflect.Slice, reflect.Array:
		if ft.Elem().Kind() == reflect.String {
			return FieldStringList
		}
		return FieldObject
	case reflect.Map, reflect.Struct, reflect.Interface:
		return FieldObject
	default:
		return FieldString
	}
}

func parseExample(ft FieldType, raw string) any {
	if raw == "" {
		return nil
	}
	switch ft {
	case FieldInt, FieldDurationMS:
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n
		}
	case FieldFloat:
		if n, err := strconv.ParseFloat(raw, 64); err == nil {
			return n
		}
	case FieldBool:
		if b, err := strconv.ParseBool(raw); err == nil {
			return b
		}
	case FieldStringList:
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return raw
}

func mergeFields(base, overrides []FieldSchema) []FieldSchema {
	if len(overrides) == 0 {
		return base
	}
	index := make(map[string]int, len(base))
	out := make([]FieldSchema, len(base))
	copy(out, base)
	for i, f := range out {
		index[f.Name] = i
	}
	for _, ov := range overrides {
		if idx, ok := index[ov.Name]; ok {
			cur := out[idx]
			if ov.Type != "" {
				cur.Type = ov.Type
			}
			if ov.Label != "" {
				cur.Label = ov.Label
			}
			if ov.Description != "" {
				cur.Description = ov.Description
			}
			if ov.Unit != "" {
				cur.Unit = ov.Unit
			}
			if len(ov.Enum) > 0 {
				cur.Enum = ov.Enum
			}
			if ov.PII {
				cur.PII = true
			}
			if ov.Secret {
				cur.Secret = true
			}
			if ov.Example != nil {
				cur.Example = ov.Example
			}
			out[idx] = cur
		} else {
			index[ov.Name] = len(out)
			out = append(out, ov)
		}
	}
	return out
}

func sampleFromFields(fields []FieldSchema) map[string]any {
	sample := map[string]any{}
	for _, f := range fields {
		var v any
		switch {
		case f.Secret:
			v = RedactedValue
		case f.Example != nil:
			v = f.Example
		case f.Type == FieldEnum && len(f.Enum) > 0:
			v = f.Enum[0].Value
		case f.Type == FieldString:
			v = ""
		case f.Type == FieldInt, f.Type == FieldDurationMS:
			v = int64(0)
		case f.Type == FieldFloat:
			v = float64(0)
		case f.Type == FieldBool:
			v = false
		case f.Type == FieldTimestamp:
			v = "2026-01-01T00:00:00Z"
		case f.Type == FieldStringList:
			v = []string{}
		case f.Type == FieldObject:
			v = map[string]any{}
		}
		setNestedSample(sample, f.Name, v)
	}
	return sample
}

func setNestedSample(root map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	cur := root
	for i := 0; i < len(parts)-1; i++ {
		next, ok := cur[parts[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[parts[i]] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = val
}

func humanizeFieldName(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if r == '_' || r == '-' {
			b.WriteByte(' ')
			continue
		}
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte(' ')
			}
		}
		if b.Len() == 0 {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.TrimSpace(b.String())
}

func lookupDotPath(doc map[string]any, path string) (any, bool) {
	if doc == nil || path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = doc
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[p]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func setOrDeletePath(doc map[string]any, path string, val any, del bool) {
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
	if _, exists := cur[last]; !exists {
		return
	}
	if del {
		delete(cur, last)
	} else {
		cur[last] = val
	}
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCopyMap(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepCopyValue(item)
		}
		return out
	case []string:
		return slices.Clone(x)
	default:
		return v
	}
}
