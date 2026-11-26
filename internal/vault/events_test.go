package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestVaultEmitsAllEvents(t *testing.T) {
	ctx := context.Background()
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	dir := t.TempDir()
	db := store.OpenTest(t)
	s, err := Open(ctx, Options{
		Dir:      dir,
		DB:       db,
		NodeID:   "node-a",
		CapBytes: 0,
		Events:   bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	lib := s.(*library)

	// 1. vault.record.stored (+ 5. vault.quota.warning once >= 80% of cap)
	k1, err := s.Put(ctx, PutRequest{
		NZB:     []byte(fixture),
		Release: core.Release{ID: "rel-1", Name: "The.Matrix.1999.2160p.mkv", IndexerID: "idx1", Size: 1022},
	})
	if err != nil {
		t.Fatal(err)
	}
	evStored := rec.WaitFor(t, events.EventVaultRecordStored, time.Second)
	if err := reg.ValidatePayload(evStored.Type, evStored.Payload); err != nil {
		t.Fatalf("stored payload invalid: %v", err)
	}
	stored, err := events.DecodePayload[events.VaultRecordStoredPayload](evStored)
	if err != nil {
		t.Fatal(err)
	}
	if stored.VaultKey != string(k1) || stored.Kind != string(KindUsenet) || stored.CompressedBytes <= 0 || stored.PackID == "" {
		t.Fatalf("unexpected stored payload: %+v", stored)
	}

	// A tiny cap must leave the NZB live. A torrent contributes to the quota.
	lib.SetCapBytes(1)
	if _, err := s.Get(ctx, k1); err != nil {
		t.Fatal(err)
	}
	lib.SetCapBytes(stored.CompressedBytes / 2)
	const fixture2 = `<nzb><file subject="Second.Movie.2021.mkv" poster="A"><groups><group>test</group></groups><segments><segment bytes="600" number="1">cc@a</segment></segments></file></nzb>`
	k2, err := s.Put(ctx, PutRequest{NZB: []byte(fixture2), Release: core.Release{ID: "rel-2", IndexerID: "idx1"}})
	if err != nil {
		t.Fatal(err)
	}
	torrentKey, err := s.Put(ctx, PutRequest{Torrent: &Torrent{
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Magnet:   "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
	}})
	if err != nil {
		t.Fatal(err)
	}
	evQuota := rec.WaitFor(t, events.EventVaultQuotaWarning, time.Second)
	if err := reg.ValidatePayload(evQuota.Type, evQuota.Payload); err != nil {
		t.Fatalf("quota payload invalid: %v", err)
	}
	quota, err := events.DecodePayload[events.VaultQuotaWarningPayload](evQuota)
	if err != nil {
		t.Fatal(err)
	}
	if quota.BudgetBytes <= 0 || quota.PercentUsed < 80 {
		t.Fatalf("unexpected quota payload: %+v", quota)
	}
	// Turn off the torrent quota for the remaining event checks.
	lib.SetCapBytes(0)

	// 2. vault.record.dead
	if err := s.MarkHealth(ctx, k2, HealthDead); err != nil {
		t.Fatal(err)
	}
	evDead := rec.WaitFor(t, events.EventVaultRecordDead, time.Second)
	if err := reg.ValidatePayload(evDead.Type, evDead.Payload); err != nil {
		t.Fatalf("dead payload invalid: %v", err)
	}
	dead, err := events.DecodePayload[events.VaultRecordDeadPayload](evDead)
	if err != nil {
		t.Fatal(err)
	}
	if dead.VaultKey != string(k2) || dead.Reason == "" {
		t.Fatalf("unexpected dead payload: %+v", dead)
	}

	// 3. vault.pack.compacted: only the torrent frame may become reclaimable.
	if err := lib.Purge(ctx, k1); !errors.Is(err, ErrNZBRetained) {
		t.Fatalf("purge NZB: %v", err)
	}
	if err := lib.Purge(ctx, k2); !errors.Is(err, ErrNZBRetained) {
		t.Fatalf("purge marked NZB: %v", err)
	}
	_, err = db.ExecContext(ctx, `UPDATE vault_releases SET dead=1 WHERE key=?`, torrentKey)
	if err != nil {
		t.Fatal(err)
	}
	// SetLayout leaves unreachable original frames for compaction to reclaim.
	for range 6 {
		if err := lib.SetLayout(ctx, k1, Layout{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	evCompacted := rec.WaitFor(t, events.EventVaultPackCompacted, time.Second)
	if err := reg.ValidatePayload(evCompacted.Type, evCompacted.Payload); err != nil {
		t.Fatalf("compacted payload invalid: %v", err)
	}
	compacted, err := events.DecodePayload[events.VaultPackCompactedPayload](evCompacted)
	if err != nil {
		t.Fatal(err)
	}
	if compacted.PacksCompacted < 1 || compacted.BytesReclaimed <= 0 {
		t.Fatalf("unexpected compacted payload: %+v", compacted)
	}

	// 4. vault.payload.missing: store a fresh record then remove its pack file.
	k3, err := s.Put(ctx, PutRequest{
		NZB:     []byte(fixture),
		Release: core.Release{ID: "rel-3", Name: "Missing.Pack.2022.mkv", IndexerID: "idx1", Size: 1022},
	})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := lib.loc(ctx, db, k3)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "packs", loc.pack)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, k3); !errors.Is(err, ErrPayloadMissing) {
		t.Fatalf("expected ErrPayloadMissing, got %v", err)
	}
	evMissing := rec.WaitFor(t, events.EventVaultPayloadMissing, time.Second)
	if err := reg.ValidatePayload(evMissing.Type, evMissing.Payload); err != nil {
		t.Fatalf("missing payload invalid: %v", err)
	}
	missing, err := events.DecodePayload[events.VaultPayloadMissingPayload](evMissing)
	if err != nil {
		t.Fatal(err)
	}
	if missing.VaultKey != string(k3) || missing.PackID != loc.pack || missing.NodeID != "node-a" {
		t.Fatalf("unexpected missing payload: %+v", missing)
	}
}
