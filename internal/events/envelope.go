// Package events provides Ether's typed domain event envelope, schema
// registry, non-blocking in-process event bus, durable outbox migration, and
// test recorder.
//
// Publishers emit events synchronously with Bus.Publish; delivery never blocks
// callers (slow subscribers drop with atomic counters). Flows, notifications,
// plugin outboxes and cluster bridges subscribe asynchronously.
package events

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// SourceBuiltin identifies events emitted by Ether itself.
	SourceBuiltin = "ether"
	// OwnerBuiltin identifies built-in schemas in Registry.
	OwnerBuiltin = "builtin"
	// PluginPrefix namespaces plugin-contributed event types ("plugin:<pluginId>/<key>").
	PluginPrefix = "plugin:"
	// RedactedValue masks Secret fields in persisted traces and API responses.
	RedactedValue = "[redacted]"
)

// Envelope is the canonical wire and in-process container for every Ether event.
type Envelope struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Version     int             `json:"version"`
	OccurredAt  time.Time       `json:"occurredAt"`
	Node        string          `json:"node,omitempty"`
	Source      string          `json:"source,omitempty"`
	Correlation Correlation     `json:"correlation,omitempty"`
	Meta        EnvelopeMeta    `json:"meta,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// Correlation links related events across playback, Live TV, viewers, libraries
// and plugins so flows can order runs and match wait_event nodes accurately.
type Correlation struct {
	// Key overrides the derived correlation key when explicitly set.
	Key       string `json:"key,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	PlayID    string `json:"playId,omitempty"`
	TitleID   string `json:"titleId,omitempty"`
	ViewerID  int64  `json:"viewerId,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
	SourceID  string `json:"sourceId,omitempty"`
	PluginID  string `json:"pluginId,omitempty"`
	DeviceID  string `json:"deviceId,omitempty"`
	LibraryID string `json:"libraryId,omitempty"`
}

// EffectiveKey returns the stable correlation key used for per-key ordering
// and wait_event matching.
func (c Correlation) EffectiveKey(fallbackNode, fallbackID string) string {
	switch {
	case c.Key != "":
		return c.Key
	case c.PlayID != "":
		return "play:" + c.PlayID
	case c.SessionID != "":
		return "session:" + c.SessionID
	case c.ChannelID != "":
		return "channel:" + c.ChannelID
	case c.TitleID != "":
		return "title:" + c.TitleID
	case c.ViewerID > 0:
		return "viewer:" + strconv.FormatInt(c.ViewerID, 10)
	case c.SourceID != "":
		return "source:" + c.SourceID
	case c.PluginID != "":
		return "plugin:" + c.PluginID
	case c.DeviceID != "":
		return "device:" + c.DeviceID
	case c.LibraryID != "":
		return "library:" + c.LibraryID
	case fallbackNode != "":
		return "node:" + fallbackNode
	default:
		return "event:" + fallbackID
	}
}

// ToMap returns correlation fields as a plain map for condition evaluation and
// template rendering.
func (c Correlation) ToMap() map[string]any {
	m := make(map[string]any, 10)
	if c.Key != "" {
		m["key"] = c.Key
	}
	if c.SessionID != "" {
		m["sessionId"] = c.SessionID
	}
	if c.PlayID != "" {
		m["playId"] = c.PlayID
	}
	if c.TitleID != "" {
		m["titleId"] = c.TitleID
	}
	if c.ViewerID != 0 {
		m["viewerId"] = c.ViewerID
	}
	if c.ChannelID != "" {
		m["channelId"] = c.ChannelID
	}
	if c.SourceID != "" {
		m["sourceId"] = c.SourceID
	}
	if c.PluginID != "" {
		m["pluginId"] = c.PluginID
	}
	if c.DeviceID != "" {
		m["deviceId"] = c.DeviceID
	}
	if c.LibraryID != "" {
		m["libraryId"] = c.LibraryID
	}
	return m
}

// EnvelopeMeta carries runtime delivery hints and loop-protection metadata.
type EnvelopeMeta struct {
	// Durable hints that the event should be written to events_outbox even if
	// no live local subscriber is attached.
	Durable bool `json:"durable,omitempty"`
	// FlowDepth counts how many nested flow executions produced this event.
	// The flow runtime refuses to trigger new flows once FlowDepth reaches its cap.
	FlowDepth int `json:"flowDepth,omitempty"`
	// OriginRunID identifies the flow run that caused this event, if any.
	OriginRunID string `json:"originRunId,omitempty"`
	// DryRun marks synthetic test-run events; normal bus subscribers skip them.
	DryRun bool `json:"dryRun,omitempty"`
}

// EnvelopeOption customizes an Envelope built by NewEnvelope or Bus.PublishTyped.
type EnvelopeOption func(*Envelope)

// WithID sets an explicit event ID.
func WithID(id string) EnvelopeOption { return func(e *Envelope) { e.ID = id } }

// WithVersion sets the event schema version.
func WithVersion(v int) EnvelopeOption { return func(e *Envelope) { e.Version = v } }

// WithOccurredAt sets the event timestamp.
func WithOccurredAt(t time.Time) EnvelopeOption { return func(e *Envelope) { e.OccurredAt = t } }

// WithNode sets the origin node ID.
func WithNode(node string) EnvelopeOption { return func(e *Envelope) { e.Node = node } }

// WithSource sets the event source ("ether" or "plugin:<id>").
func WithSource(src string) EnvelopeOption { return func(e *Envelope) { e.Source = src } }

// WithCorrelation attaches structured correlation IDs to the event.
func WithCorrelation(c Correlation) EnvelopeOption { return func(e *Envelope) { e.Correlation = c } }

// WithMeta sets envelope delivery and loop-protection metadata.
func WithMeta(m EnvelopeMeta) EnvelopeOption { return func(e *Envelope) { e.Meta = m } }

// WithDurable marks the envelope as durable.
func WithDurable() EnvelopeOption { return func(e *Envelope) { e.Meta.Durable = true } }

// NewID generates a random event ID ("evt_<16 hex>").
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "evt_" + hex.EncodeToString(b[:])
}

// NewEnvelope marshals payload into an Envelope for eventType.
func NewEnvelope(eventType string, payload any, opts ...EnvelopeOption) (Envelope, error) {
	if err := ValidateType(eventType); err != nil {
		return Envelope{}, err
	}
	var raw json.RawMessage
	switch v := payload.(type) {
	case nil:
		raw = json.RawMessage(`{}`)
	case json.RawMessage:
		if len(bytes.TrimSpace(v)) == 0 {
			raw = json.RawMessage(`{}`)
		} else {
			raw = append(json.RawMessage(nil), v...)
		}
	case []byte:
		if len(bytes.TrimSpace(v)) == 0 {
			raw = json.RawMessage(`{}`)
		} else {
			raw = append(json.RawMessage(nil), v...)
		}
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return Envelope{}, fmt.Errorf("events: marshal %s payload: %w", eventType, err)
		}
		raw = b
	}
	if !isJSONObject(raw) {
		return Envelope{}, fmt.Errorf("events: %s payload must be a JSON object", eventType)
	}
	ev := Envelope{
		Type:    eventType,
		Version: 1,
		Source:  SourceBuiltin,
		Payload: raw,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&ev)
		}
	}
	return ev, nil
}

// DecodePayload unmarshals ev.Payload into T.
func DecodePayload[T any](ev Envelope) (T, error) {
	var out T
	if len(ev.Payload) == 0 {
		return out, nil
	}
	err := json.Unmarshal(ev.Payload, &out)
	return out, err
}

// PayloadMap decodes ev.Payload into a map[string]any, preserving integer
// precision via json.Number.
func (e Envelope) PayloadMap() (map[string]any, error) {
	if len(bytes.TrimSpace(e.Payload)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(e.Payload))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("events: decode payload: %w", err)
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

// MetaMap returns the envelope's non-payload metadata as a plain map for
// condition evaluation and template interpolation under the "event" root.
func (e Envelope) MetaMap() map[string]any {
	return map[string]any{
		"id":          e.ID,
		"type":        e.Type,
		"domain":      DomainOf(e.Type),
		"version":     e.Version,
		"occurredAt":  e.OccurredAt.UTC().Format(time.RFC3339Nano),
		"node":        e.Node,
		"source":      e.Source,
		"correlation": e.Correlation.ToMap(),
	}
}

// CorrelationKey returns the envelope's effective correlation key.
func (e Envelope) CorrelationKey() string {
	return e.Correlation.EffectiveKey(e.Node, e.ID)
}

var (
	builtinTypeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
	pluginIDRe    = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9][a-z0-9-]*)+$`)
	pluginKeyRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){0,2}$`)
)

// ErrInvalidEventType is returned when an event type fails naming validation.
var ErrInvalidEventType = errors.New("events: invalid event type")

// ValidateType checks that eventType follows either built-in "domain.noun.verb"
// or plugin "plugin:<pluginId>/<key>" naming rules.
func ValidateType(eventType string) error {
	if len(eventType) == 0 || len(eventType) > 128 {
		return fmt.Errorf("%w: %q", ErrInvalidEventType, eventType)
	}
	if strings.HasPrefix(eventType, PluginPrefix) {
		if _, _, ok := ParsePluginEventType(eventType); !ok {
			return fmt.Errorf("%w: plugin event %q must be plugin:<pluginId>/<noun>.<verb>", ErrInvalidEventType, eventType)
		}
		return nil
	}
	if !builtinTypeRe.MatchString(eventType) {
		return fmt.Errorf("%w: %q must match domain.noun.verb", ErrInvalidEventType, eventType)
	}
	return nil
}

// PluginEventType formats a plugin-namespaced event type.
func PluginEventType(pluginID, key string) string {
	return PluginPrefix + pluginID + "/" + key
}

// IsPluginType reports whether eventType belongs to a plugin.
func IsPluginType(eventType string) bool {
	return strings.HasPrefix(eventType, PluginPrefix)
}

// ParsePluginEventType splits "plugin:<pluginId>/<key>" and validates both halves.
func ParsePluginEventType(eventType string) (pluginID, key string, ok bool) {
	if !strings.HasPrefix(eventType, PluginPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(eventType, PluginPrefix)
	pluginID, key, found := strings.Cut(rest, "/")
	if !found || !pluginIDRe.MatchString(pluginID) || !pluginKeyRe.MatchString(key) {
		return "", "", false
	}
	return pluginID, key, true
}

// DomainOf extracts the domain portion of an event type ("playback" for
// "playback.session.started", or "plugin:<pluginId>" for plugin events).
func DomainOf(eventType string) string {
	if pid, _, ok := ParsePluginEventType(eventType); ok {
		return PluginPrefix + pid
	}
	if domain, _, ok := strings.Cut(eventType, "."); ok {
		return domain
	}
	return ""
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' && json.Valid(trimmed)
}
