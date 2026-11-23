package prefetch

import (
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

func TestPrefetchEmitsCompletedAndFailedEvents(t *testing.T) {
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	cat := &fakeCatalog{
		next: map[core.RatingKey]*core.Title{
			ep1: {Key: ep2, Type: core.TypeEpisode, ShowTitle: "Severance", Season: 1, Episode: 2, Title: "Half Loop"},
			ep2: {Key: ep3, Type: core.TypeEpisode, ShowTitle: "Severance", Season: 1, Episode: 3, Title: "In Perpetuity"},
		},
	}
	res := &fakeResolver{}
	eng := &fakeEngine{}
	cfg := config.Cache{
		PrefetchNextEpisode: true,
		PrefetchBytes:       32 << 20,
		NextEpisodeAt:       0.8,
	}
	p, err := New(Deps{
		Catalog:  cat,
		Resolver: res,
		Engine:   eng,
		Events:   bus,
	}, cfg, Options{Concurrency: 1, WarmInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	u := &core.User{ID: 7, Username: "alice"}

	// 1. Successful next-episode prefetch -> cache.prefetch.completed.
	p.OnTimeline(u, ep1, 45*time.Minute, 50*time.Minute, "playing")
	evDone := rec.WaitFor(t, events.EventCachePrefetchCompleted, 2*time.Second)
	if err := reg.ValidatePayload(evDone.Type, evDone.Payload); err != nil {
		t.Fatalf("completed payload invalid: %v", err)
	}
	done, err := events.DecodePayload[events.CachePrefetchCompletedPayload](evDone)
	if err != nil {
		t.Fatal(err)
	}
	if done.Kind != "next_episode" || done.BytesWarmed != 32<<20 || done.TitleKey != ep2.String() {
		t.Fatalf("unexpected completed payload: %+v", done)
	}

	// 2. Failing next-episode prefetch -> cache.prefetch.failed.
	res.mu.Lock()
	res.err = errors.New("indexer timeout")
	res.mu.Unlock()
	p.OnTimeline(u, ep2, 45*time.Minute, 50*time.Minute, "playing")
	evFail := rec.WaitFor(t, events.EventCachePrefetchFailed, 2*time.Second)
	if err := reg.ValidatePayload(evFail.Type, evFail.Payload); err != nil {
		t.Fatalf("failed payload invalid: %v", err)
	}
	failed, err := events.DecodePayload[events.CachePrefetchFailedPayload](evFail)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Kind != "next_episode" || failed.Error == "" {
		t.Fatalf("unexpected failed payload: %+v", failed)
	}
}
