package flows

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/store"
)

// Cluster bus topics used to coordinate Flows across cluster nodes.
const (
	// TopicSubscriptions carries leader-broadcast active event subscription patterns.
	TopicSubscriptions = "flows:subs"
	// TopicOutboxWake wakes the leader to drain events_outbox or claim newly created runs.
	TopicOutboxWake = "flows:outbox"
	// TopicFlowsChanged tells the leader that a flow definition changed on another node.
	TopicFlowsChanged = "flows:changed"
)

const (
	defaultOutboxBatch   = 64
	defaultBridgeBuffer  = 512
	defaultOutboxMaxAge  = 7 * 24 * time.Hour
	outboxPublishTimeout = 2 * time.Second
)

// OutboxRow is one pending entry read from events_outbox.
type OutboxRow struct {
	ID        int64
	EventID   string
	EventType string
	Envelope  json.RawMessage
}

// OutboxStore reads and writes the shared events_outbox table (events/1)
// across SQLite and PostgreSQL.
type OutboxStore struct {
	db  *sql.DB
	now func() time.Time
}

// NewOutboxStore creates an OutboxStore over db.
func NewOutboxStore(db *sql.DB, now func() time.Time) *OutboxStore {
	if now == nil {
		now = time.Now
	}
	return &OutboxStore{db: db, now: now}
}

// Enqueue persists ev into events_outbox idempotently (deduplicated by event_id).
func (o *OutboxStore) Enqueue(ctx context.Context, ev events.Envelope) (bool, error) {
	now := o.now().UTC()
	if ev.ID == "" {
		ev.ID = events.NewID()
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	if ev.Version <= 0 {
		ev.Version = 1
	}
	if len(ev.Payload) == 0 {
		ev.Payload = json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return false, err
	}
	res, err := o.db.ExecContext(ctx,
		`INSERT INTO events_outbox(event_id, event_type, version, occurred_at_ms, node_id, correlation_key, envelope_json, created_at_ms, acked_at_ms)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, 0)
		 ON CONFLICT(event_id) DO NOTHING`,
		ev.ID, ev.Type, ev.Version, timeToMS(ev.OccurredAt), ev.Node,
		ev.CorrelationKey(), string(raw), timeToMS(now),
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListPending returns up to limit unacknowledged rows ordered by id ascending.
func (o *OutboxStore) ListPending(ctx context.Context, limit int) ([]OutboxRow, error) {
	if limit <= 0 {
		limit = defaultOutboxBatch
	}
	if limit > 256 {
		limit = 256
	}
	rows, err := o.db.QueryContext(ctx,
		`SELECT id, event_id, event_type, envelope_json
		 FROM events_outbox
		 WHERE acked_at_ms = 0
		 ORDER BY id ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxRow
	for rows.Next() {
		var r OutboxRow
		var envStr string
		if err := rows.Scan(&r.ID, &r.EventID, &r.EventType, &envStr); err != nil {
			return nil, err
		}
		r.Envelope = json.RawMessage(envStr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Ack marks one events_outbox row acknowledged.
func (o *OutboxStore) Ack(ctx context.Context, id int64, ackedAt time.Time) error {
	if ackedAt.IsZero() {
		ackedAt = o.now().UTC()
	}
	_, err := o.db.ExecContext(ctx,
		`UPDATE events_outbox SET acked_at_ms=? WHERE id=? AND acked_at_ms=0`,
		timeToMS(ackedAt), id,
	)
	return err
}

// PruneAcked removes acknowledged events_outbox rows older than olderThan in batches.
func (o *OutboxStore) PruneAcked(ctx context.Context, olderThan time.Time) (int64, error) {
	if olderThan.IsZero() {
		return 0, nil
	}
	return store.PruneBatched(ctx, o.db,
		`DELETE FROM events_outbox WHERE id IN (
			SELECT id FROM events_outbox WHERE acked_at_ms > 0 AND created_at_ms < ? ORDER BY created_at_ms ASC LIMIT ?
		)`,
		timeToMS(olderThan),
	)
}

type subsBroadcast struct {
	Origin   string   `json:"origin,omitempty"`
	Leader   string   `json:"leader,omitempty"`
	Version  uint64   `json:"version"`
	Patterns []string `json:"patterns,omitempty"`
	Reload   bool     `json:"reload,omitempty"`
}

func encodeSubsBroadcast(origin, leader string, version uint64, patterns []string) []byte {
	sorted := normalizePatterns(patterns)
	msg := subsBroadcast{
		Origin:   origin,
		Leader:   leader,
		Version:  version,
		Patterns: sorted,
	}
	raw, err := json.Marshal(msg)
	if err == nil && len(raw) <= clusterbus.MaxPayload {
		return raw
	}
	fallback, _ := json.Marshal(subsBroadcast{
		Origin:  origin,
		Leader:  leader,
		Version: version,
		Reload:  true,
	})
	return fallback
}

func normalizePatterns(patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// OutboxBridgeOptions configures a follower→leader events_outbox bridge.
type OutboxBridgeOptions struct {
	DB            *sql.DB
	Bus           *events.Bus
	ClusterBus    clusterbus.Bus
	NodeID        string
	IsLeader      func() bool
	LoadSubs      func(ctx context.Context) ([]string, error)
	Now           func() time.Time
	Logger        *slog.Logger
	BufferSize    int
	OnForwarded   func(ev events.Envelope)
	OnSubsUpdated func(patterns []string)
}

// OutboxBridge subscribes to a node's local events.Bus and forwards only
// events matching the leader-broadcast subscription set (or Meta.Durable) to
// events_outbox + a clusterbus wake ping when this node is not leading.
// Events that no flow or waiter subscribes to cause zero database writes.
type OutboxBridge struct {
	opts      OutboxBridgeOptions
	outbox    *OutboxStore
	logger    *slog.Logger
	reloadCh  chan struct{}
	ready     chan struct{}
	readyOnce sync.Once

	mu       sync.RWMutex
	patterns []string
	leader   string
	version  uint64
}

// NewOutboxBridge creates a follower→leader outbox bridge.
func NewOutboxBridge(opts OutboxBridgeOptions) *OutboxBridge {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBridgeBuffer
	}
	return &OutboxBridge{
		opts:     opts,
		outbox:   NewOutboxStore(opts.DB, opts.Now),
		logger:   opts.Logger,
		reloadCh: make(chan struct{}, 1),
		ready:    make(chan struct{}),
	}
}

// Ready returns a channel closed once the bridge has subscribed to the bus and
// loaded its initial subscription set.
func (b *OutboxBridge) Ready() <-chan struct{} { return b.ready }

// SetOnSubsUpdated installs a callback invoked after the bridge commits a new
// subscription pattern set.
func (b *OutboxBridge) SetOnSubsUpdated(fn func(patterns []string)) {
	b.mu.Lock()
	b.opts.OnSubsUpdated = fn
	b.mu.Unlock()
}

// Patterns returns a snapshot of the bridge's currently active subscription patterns.
func (b *OutboxBridge) Patterns() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return slices.Clone(b.patterns)
}

// SetPatterns replaces the bridge's in-memory subscription pattern filter.
func (b *OutboxBridge) SetPatterns(patterns []string) {
	norm := normalizePatterns(patterns)
	b.mu.Lock()
	b.patterns = norm
	b.mu.Unlock()
}

// Matches reports whether eventType matches any active subscription pattern.
func (b *OutboxBridge) Matches(eventType string) bool {
	b.mu.RLock()
	patterns := b.patterns
	b.mu.RUnlock()
	for _, p := range patterns {
		if events.MatchPattern(p, eventType) {
			return true
		}
	}
	return false
}

// Run subscribes to clusterbus subscription updates and the local events.Bus,
// forwarding matching follower events into events_outbox until ctx ends.
func (b *OutboxBridge) Run(ctx context.Context) {
	if b.opts.DB == nil || b.opts.Bus == nil {
		<-ctx.Done()
		return
	}

	var unsubs []func()
	if b.opts.ClusterBus != nil {
		unsubs = append(unsubs,
			b.opts.ClusterBus.Subscribe(TopicSubscriptions, b.onSubsMessage),
			b.opts.ClusterBus.Subscribe(clusterbus.TopicResync, func([]byte) {
				b.triggerReload()
			}),
		)
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	sub := b.opts.Bus.Subscribe(events.SubscriptionOptions{
		Name:          "flows-outbox-bridge",
		Buffer:        b.opts.BufferSize,
		Patterns:      []string{"*"},
		IncludeDryRun: false,
	})
	defer sub.Unsubscribe()

	// Initial load from shared DB so a follower that boots after the leader
	// already broadcast flows:subs starts with an accurate subscription set.
	b.reloadSubs(ctx)
	b.readyOnce.Do(func() { close(b.ready) })

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.reloadCh:
			b.reloadSubs(ctx)
		case ev, ok := <-sub.C():
			if !ok {
				return
			}
			b.handleEvent(ctx, ev)
		}
	}
}

func (b *OutboxBridge) onSubsMessage(payload []byte) {
	var msg subsBroadcast
	if err := json.Unmarshal(payload, &msg); err != nil {
		return
	}
	if msg.Reload {
		b.triggerReload()
		return
	}
	norm := normalizePatterns(msg.Patterns)
	updated := false
	b.mu.Lock()
	if msg.Leader != b.leader || msg.Version == 0 || msg.Version >= b.version {
		b.patterns = norm
		b.leader = msg.Leader
		b.version = msg.Version
		updated = true
	}
	cb := b.opts.OnSubsUpdated
	b.mu.Unlock()
	if updated && cb != nil {
		cb(slices.Clone(norm))
	}
}

func (b *OutboxBridge) triggerReload() {
	select {
	case b.reloadCh <- struct{}{}:
	default:
	}
}

func (b *OutboxBridge) reloadSubs(ctx context.Context) {
	if b.opts.LoadSubs == nil {
		return
	}
	patterns, err := b.opts.LoadSubs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			b.logger.Warn("flows: bridge failed to load subscription patterns", "err", err)
		}
		return
	}
	b.SetPatterns(patterns)
	b.mu.RLock()
	cb := b.opts.OnSubsUpdated
	b.mu.RUnlock()
	if cb != nil {
		cb(b.Patterns())
	}
}

func (b *OutboxBridge) handleEvent(ctx context.Context, ev events.Envelope) {
	if ev.Meta.DryRun {
		return
	}
	if b.opts.IsLeader != nil && b.opts.IsLeader() {
		return
	}
	if !ev.Meta.Durable && !b.Matches(ev.Type) {
		return
	}
	inserted, err := b.outbox.Enqueue(ctx, ev)
	if err != nil {
		if ctx.Err() == nil {
			b.logger.Warn("flows: bridge enqueue events_outbox failed", "event", ev.Type, "id", ev.ID, "err", err)
		}
		return
	}
	if b.opts.ClusterBus != nil {
		pctx, cancel := context.WithTimeout(ctx, outboxPublishTimeout)
		if err := b.opts.ClusterBus.Publish(pctx, TopicOutboxWake, []byte(b.opts.NodeID)); err != nil && ctx.Err() == nil {
			b.logger.Warn("flows: bridge outbox wake publish failed", "err", err)
		}
		cancel()
	}
	if inserted && b.opts.OnForwarded != nil {
		b.opts.OnForwarded(ev)
	}
}
