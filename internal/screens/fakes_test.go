package screens

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

type fakeHub struct {
	mu           sync.Mutex
	events       []core.LiveEvent
	tables       []core.LiveEventTable
	entities     []core.LiveEventEntity
	eventCalls   int
	eventReqs    []EventQuery
	standingsReq []string
	entityReqs   []EntityQuery
}

func (f *fakeHub) Events(_ context.Context, _ *core.User, q EventQuery) ([]core.LiveEvent, error) {
	f.mu.Lock()
	f.eventCalls++
	f.eventReqs = append(f.eventReqs, q)
	f.mu.Unlock()
	var out []core.LiveEvent
	for _, ev := range f.events {
		if q.Feed != "" && ev.Feed != "" && ev.Feed != q.Feed {
			continue
		}
		if q.League != "" && ev.League.ID != q.League {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func (f *fakeHub) Standings(_ context.Context, _ *core.User, _, _, league string) ([]core.LiveEventTable, error) {
	f.mu.Lock()
	f.standingsReq = append(f.standingsReq, league)
	f.mu.Unlock()
	return f.tables, nil
}

func (f *fakeHub) Entities(_ context.Context, _ *core.User, q EntityQuery) ([]core.LiveEventEntity, error) {
	f.mu.Lock()
	f.entityReqs = append(f.entityReqs, q)
	f.mu.Unlock()
	var idSet map[string]struct{}
	if len(q.IDs) > 0 {
		idSet = make(map[string]struct{}, len(q.IDs))
		for _, id := range q.IDs {
			idSet[id] = struct{}{}
		}
	}
	var out []core.LiveEventEntity
	for _, e := range f.entities {
		if q.League != "" && e.League != q.League {
			continue
		}
		if len(idSet) > 0 {
			if _, ok := idSet[e.ID]; !ok {
				continue
			}
		}
		out = append(out, e)
	}
	return out, nil
}
