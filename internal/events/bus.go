package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultSubscriberBuffer is the bounded channel capacity given to subscribers
// that do not specify a custom buffer size.
const DefaultSubscriberBuffer = 256

// BusOptions configures an in-process Bus.
type BusOptions struct {
	// NodeID stamps envelopes whose Node is empty.
	NodeID string
	// DefaultBuffer sets the default subscriber channel capacity (256 when <= 0).
	DefaultBuffer int
	// Now supplies the clock for missing OccurredAt timestamps (defaults to time.Now).
	Now func() time.Time
	// Logger receives rate-limited overload drop warnings (defaults to slog.Default()).
	Logger *slog.Logger
	// Registry, when provided, fills in default schema Version when Envelope.Version == 0.
	Registry *Registry
	// OnDrop is invoked (synchronously, must not block) whenever a subscriber
	// buffer overflows and an event is dropped.
	OnDrop func(subscriber string, ev Envelope)
}

// SubscriptionOptions configures one Bus subscriber.
type SubscriptionOptions struct {
	// Name identifies this subscriber in stats and drop logs.
	Name string
	// Buffer bounds unread events queued for this subscriber.
	Buffer int
	// Patterns selects events by exact type ("playback.session.started"),
	// wildcard prefix ("playback.*", "livetv.input.*", "plugin:com.acme/*"),
	// trailing delimiter prefix ("playback."), or "*" for all events.
	// Empty defaults to "*".
	Patterns []string
	// IncludeDryRun opts into synthetic test-run events (default false).
	IncludeDryRun bool
}

// SubscriberStats is a point-in-time snapshot of one subscriber's health.
type SubscriberStats struct {
	Name      string   `json:"name"`
	Patterns  []string `json:"patterns"`
	Delivered uint64   `json:"delivered"`
	Dropped   uint64   `json:"dropped"`
	Queued    int      `json:"queued"`
	Capacity  int      `json:"capacity"`
}

// BusStats summarizes total bus traffic and every active subscriber.
type BusStats struct {
	Published   uint64            `json:"published"`
	Dropped     uint64            `json:"dropped"`
	Subscribers []SubscriberStats `json:"subscribers"`
}

// Bus is Ether's non-blocking in-process event bus. Publishers never block on
// slow consumers; full subscriber buffers drop the event and increment counters.
type Bus struct {
	opts BusOptions

	mu     sync.RWMutex
	closed bool
	nextID uint64
	subs   map[uint64]*Subscription

	published atomic.Uint64
	dropped   atomic.Uint64
}

// Subscription receives matching Envelopes on a bounded channel.
type Subscription struct {
	bus           *Bus
	id            uint64
	name          string
	patterns      []string
	includeDryRun bool

	mu        sync.RWMutex
	closed    bool
	ch        chan Envelope
	delivered atomic.Uint64
	dropped   atomic.Uint64
}

// NewBus creates an in-process event bus.
func NewBus(opts BusOptions) *Bus {
	if opts.DefaultBuffer <= 0 {
		opts.DefaultBuffer = DefaultSubscriberBuffer
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Bus{
		opts: opts,
		subs: make(map[uint64]*Subscription),
	}
}

// Subscribe attaches a bounded subscriber matching opts.Patterns.
// Call Unsubscribe when done (or Close the bus) to release resources.
func (b *Bus) Subscribe(opts SubscriptionOptions) *Subscription {
	buf := opts.Buffer
	if buf <= 0 {
		buf = b.opts.DefaultBuffer
	}
	patterns := slices.Clone(opts.Patterns)
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "anonymous"
	}
	sub := &Subscription{
		bus:           b,
		name:          name,
		patterns:      patterns,
		includeDryRun: opts.IncludeDryRun,
		ch:            make(chan Envelope, buf),
	}
	b.mu.Lock()
	if b.closed {
		sub.closed = true
		close(sub.ch)
		b.mu.Unlock()
		return sub
	}
	b.nextID++
	sub.id = b.nextID
	b.subs[sub.id] = sub
	b.mu.Unlock()
	return sub
}

// SubscribeFunc subscribes and invokes fn sequentially in a background goroutine
// until ctx is cancelled, Unsubscribe is called, or the bus closes. Panics in
// fn are recovered and logged so one subscriber cannot crash the process.
func (b *Bus) SubscribeFunc(ctx context.Context, opts SubscriptionOptions, fn func(context.Context, Envelope)) *Subscription {
	sub := b.Subscribe(opts)
	if fn == nil {
		return sub
	}
	go func() {
		defer sub.Unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub.ch:
				if !ok {
					return
				}
				func() {
					defer func() {
						if r := recover(); r != nil {
							b.opts.Logger.Warn("events: subscriber panicked", "subscriber", sub.name, "event", ev.Type, "recover", r)
						}
					}()
					fn(ctx, ev)
				}()
			}
		}
	}()
	return sub
}

// Publish dispatches ev to every matching subscriber without ever blocking.
// Missing ID, OccurredAt, Node, Source, Version and Payload fields are defaulted.
func (b *Bus) Publish(ev Envelope) {
	if b == nil {
		return
	}
	if ev.ID == "" {
		ev.ID = NewID()
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = b.opts.Now().UTC()
	}
	if ev.Node == "" {
		ev.Node = b.opts.NodeID
	}
	if ev.Source == "" {
		ev.Source = SourceBuiltin
	}
	if ev.Version <= 0 {
		if b.opts.Registry != nil {
			if s, ok := b.opts.Registry.Get(ev.Type); ok {
				ev.Version = s.Version
			}
		}
		if ev.Version <= 0 {
			ev.Version = 1
		}
	}
	if len(ev.Payload) == 0 {
		ev.Payload = json.RawMessage(`{}`)
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	b.published.Add(1)
	targets := make([]*Subscription, 0, len(b.subs))
	for _, sub := range b.subs {
		if ev.Meta.DryRun && !sub.includeDryRun {
			continue
		}
		if sub.matches(ev.Type) {
			targets = append(targets, sub)
		}
	}
	b.mu.RUnlock()

	for _, sub := range targets {
		if !sub.tryDeliver(ev) {
			n := sub.dropped.Add(1)
			b.dropped.Add(1)
			if n == 1 || n%100 == 0 {
				b.opts.Logger.Warn("events: subscriber buffer full; dropping event",
					"subscriber", sub.name,
					"event", ev.Type,
					"dropped", n,
					"capacity", cap(sub.ch),
				)
			}
			if b.opts.OnDrop != nil {
				b.opts.OnDrop(sub.name, ev)
			}
		}
	}
}

// PublishTyped marshals payload into an Envelope and publishes it non-blockingly.
func (b *Bus) PublishTyped(eventType string, payload any, opts ...EnvelopeOption) error {
	if b != nil && b.opts.Registry != nil {
		if s, ok := b.opts.Registry.Get(eventType); ok && s.Version > 0 {
			opts = append([]EnvelopeOption{WithVersion(s.Version)}, opts...)
		}
	}
	ev, err := NewEnvelope(eventType, payload, opts...)
	if err != nil {
		return err
	}
	b.Publish(ev)
	return nil
}

// Stats snapshots bus publish/drop counts and every active subscriber's state.
func (b *Bus) Stats() BusStats {
	b.mu.RLock()
	subs := make([]*Subscription, 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.RUnlock()

	out := BusStats{
		Published:   b.published.Load(),
		Dropped:     b.dropped.Load(),
		Subscribers: make([]SubscriberStats, 0, len(subs)),
	}
	for _, sub := range subs {
		out.Subscribers = append(out.Subscribers, sub.Stats())
	}
	return out
}

// Close closes the bus and all active subscriber channels. It is idempotent.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*Subscription, 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	clear(b.subs)
	b.mu.Unlock()

	for _, sub := range subs {
		sub.closeChannel()
	}
}

// C returns the read-only delivery channel for this subscription.
func (s *Subscription) C() <-chan Envelope { return s.ch }

// Unsubscribe detaches this subscription and closes its delivery channel.
func (s *Subscription) Unsubscribe() {
	if s == nil || s.bus == nil {
		return
	}
	s.bus.mu.Lock()
	delete(s.bus.subs, s.id)
	s.bus.mu.Unlock()
	s.closeChannel()
}

// Stats returns point-in-time delivery and overflow counters for s.
func (s *Subscription) Stats() SubscriberStats {
	return SubscriberStats{
		Name:      s.name,
		Patterns:  slices.Clone(s.patterns),
		Delivered: s.delivered.Load(),
		Dropped:   s.dropped.Load(),
		Queued:    len(s.ch),
		Capacity:  cap(s.ch),
	}
}

func (s *Subscription) closeChannel() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	s.mu.Unlock()
}

func (s *Subscription) tryDeliver(ev Envelope) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return true
	}
	select {
	case s.ch <- ev:
		s.delivered.Add(1)
		return true
	default:
		return false
	}
}

func (s *Subscription) matches(eventType string) bool {
	for _, p := range s.patterns {
		if MatchPattern(p, eventType) {
			return true
		}
	}
	return false
}

// MatchPattern reports whether eventType matches a subscription pattern.
// Supported patterns:
//   - "*" matches every event type.
//   - "domain.*" or "domain.noun.*" matches any event starting with that prefix.
//   - "plugin:<id>/*" matches any event published by that plugin.
//   - "domain." or "plugin:<id>/" matches any event with that literal prefix.
//   - Any other pattern matches exact equality.
func MatchPattern(pattern, eventType string) bool {
	if pattern == "" || eventType == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		return strings.HasPrefix(eventType, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "/*") {
		return strings.HasPrefix(eventType, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, ".") || strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(eventType, pattern)
	}
	return pattern == eventType
}
