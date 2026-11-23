package app

import (
	"log/slog"
	"sync"

	"github.com/braedonsaunders/ether/internal/events"
)

func (a *App) initEventBus() {
	if a == nil || a.eventBus != nil {
		return
	}
	a.eventRegistry = events.NewBuiltinRegistry()

	var nodeID string
	if a.cluster != nil {
		nodeID = a.cluster.Self().ID
	}

	var dropMu sync.Mutex
	dropsBySub := make(map[string]uint64)

	var logger *slog.Logger
	if a.log != nil {
		logger = a.log.With("component", "events")
	}

	a.eventBus = events.NewBus(events.BusOptions{
		NodeID:   nodeID,
		Registry: a.eventRegistry,
		Logger:   logger,
		OnDrop: func(subscriber string, ev events.Envelope) {
			if ev.Type == "flows.bus.dropped" {
				return
			}
			dropMu.Lock()
			if len(dropsBySub) >= 512 {
				if _, exists := dropsBySub[subscriber]; !exists {
					dropMu.Unlock()
					return
				}
			}
			dropsBySub[subscriber]++
			dropped := dropsBySub[subscriber]
			dropMu.Unlock()

			if dropped != 1 && dropped%100 != 0 {
				return
			}
			capacity := 0
			if bus := a.eventBus; bus != nil {
				for _, st := range bus.Stats().Subscribers {
					if st.Name == subscriber {
						capacity = st.Capacity
						break
					}
				}
			}
			a.publishEvent("flows.bus.dropped", map[string]any{
				"subscriber":   subscriber,
				"eventType":    ev.Type,
				"droppedTotal": dropped,
				"capacity":     capacity,
			}, events.WithSource("events.bus"))
		},
	})
	a.closers = append(a.closers, a.eventBus.Close)
}

// EventBus returns the process-wide structured event bus.
func (a *App) EventBus() *events.Bus {
	if a == nil {
		return nil
	}
	return a.eventBus
}

// EventRegistry returns the schema registry backing EventBus.
func (a *App) EventRegistry() *events.Registry {
	if a == nil {
		return nil
	}
	return a.eventRegistry
}

// publishEvent publishes a typed event payload onto a.eventBus without ever
// blocking the caller. It is safe to call when a or a.eventBus is nil.
func (a *App) publishEvent(eventType string, payload any, opts ...events.EnvelopeOption) {
	if a == nil || a.eventBus == nil {
		return
	}
	if err := a.eventBus.PublishTyped(eventType, payload, opts...); err != nil && a.log != nil {
		a.log.Debug("events: publish failed", "type", eventType, "err", err)
	}
}
