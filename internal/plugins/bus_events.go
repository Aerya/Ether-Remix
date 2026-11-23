package plugins

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

func (m *Manager) emitBusEvent(eventType, pluginID string, payload any, durable bool) {
	if m == nil {
		return
	}
	bus := m.flowsEventBus()
	if bus == nil {
		return
	}
	opts := []events.EnvelopeOption{
		events.WithSource("plugins"),
		events.WithCorrelation(events.Correlation{PluginID: pluginID}),
	}
	if durable {
		opts = append(opts, events.WithDurable())
	}
	if err := bus.PublishTyped(eventType, payload, opts...); err != nil && m.log != nil {
		m.log.Debug("plugins: emit bus event", "type", eventType, "plugin", pluginID, "err", err)
	}
}

func (m *Manager) pluginDisplayName(ctx context.Context, pluginID string) string {
	if m == nil || m.store == nil {
		return pluginID
	}
	if r, err := m.store.record(ctx, pluginID); err == nil && r.Name != "" {
		return r.Name
	}
	return pluginID
}

func (m *Manager) markDegradedSinceLocked(pluginID string, at time.Time) {
	if m.degradedSince == nil {
		m.degradedSince = map[string]time.Time{}
	}
	if _, ok := m.degradedSince[pluginID]; !ok {
		m.degradedSince[pluginID] = at
	}
}

func (m *Manager) clearDegradedSinceLocked(pluginID string, now time.Time) int64 {
	if m.degradedSince == nil {
		return 0
	}
	since, ok := m.degradedSince[pluginID]
	delete(m.degradedSince, pluginID)
	if !ok || since.IsZero() {
		return 0
	}
	return max(now.Sub(since).Milliseconds(), int64(0))
}
