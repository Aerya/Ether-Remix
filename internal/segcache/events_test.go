package segcache

import (
	"bytes"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

func TestSegcacheEmitsBudgetAndEvictionEvents(t *testing.T) {
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	c, err := Open(t.TempDir(), 4096, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetEventBus(bus)

	// Pin release "p" so usage hits 100% of 4096 budget -> cache.budget.full.
	if err := c.Pin("p", "playback"); err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("a"), 1024)
	for i := range 4 {
		if err := c.Put("p", i, chunk); err != nil {
			t.Fatal(err)
		}
	}
	evFull := rec.WaitFor(t, events.EventCacheBudgetFull, time.Second)
	if err := reg.ValidatePayload(evFull.Type, evFull.Payload); err != nil {
		t.Fatalf("budget full payload invalid: %v", err)
	}
	full, err := events.DecodePayload[events.CacheBudgetFullPayload](evFull)
	if err != nil {
		t.Fatal(err)
	}
	if full.CacheKind != "segment" || full.UsedBytes < 4096 || full.PercentUsed < 100 {
		t.Fatalf("unexpected budget full payload: %+v", full)
	}

	// Add an unpinned release "u" and lower budget slightly or unpin "p" + shrink budget to force eviction spike & recovery.
	if err := c.Unpin("p", "playback"); err != nil {
		t.Fatal(err)
	}
	// Remove release "p" to drop usage below 90% -> cache.budget.recovered.
	if err := c.Remove("p"); err != nil {
		t.Fatal(err)
	}
	evRec := rec.WaitFor(t, events.EventCacheBudgetRecovered, time.Second)
	if err := reg.ValidatePayload(evRec.Type, evRec.Payload); err != nil {
		t.Fatalf("budget recovered payload invalid: %v", err)
	}
	recovered, err := events.DecodePayload[events.CacheBudgetRecoveredPayload](evRec)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.CacheKind != "segment" || recovered.UsedBytes != 0 {
		t.Fatalf("unexpected budget recovered payload: %+v", recovered)
	}

	// Fill cache and push over budget to evict >= 25% of budget -> cache.eviction.spike.
	for i := range 6 {
		if err := c.Put("u", i, chunk); err != nil {
			t.Fatal(err)
		}
	}
	c.evict()
	evSpike := rec.WaitFor(t, events.EventCacheEvictionSpike, time.Second)
	if err := reg.ValidatePayload(evSpike.Type, evSpike.Payload); err != nil {
		t.Fatalf("eviction spike payload invalid: %v", err)
	}
	spike, err := events.DecodePayload[events.CacheEvictionSpikePayload](evSpike)
	if err != nil {
		t.Fatal(err)
	}
	if spike.CacheKind != "segment" || spike.EvictedBytes < 1024 || spike.EvictedEntries < 1 || spike.WindowSec <= 0 {
		t.Fatalf("unexpected eviction spike payload: %+v", spike)
	}
}
