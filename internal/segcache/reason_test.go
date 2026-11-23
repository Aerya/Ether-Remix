package segcache

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestReleaseReasonRetention(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := openT(t, t.TempDir(), 1024)
	c.now = func() time.Time { return now }
	mustPut(t, c, "r", 0, []byte("x"))

	got := c.Releases()[0].Reason
	if got.Retention.Kind != "lru" || len(got.Causes) != 1 || got.Causes[0].Code != core.CacheCauseUnrecorded {
		t.Fatalf("default = %+v", got)
	}

	if err := c.Pin("r", "owner"); err != nil {
		t.Fatal(err)
	}
	got = c.Releases()[0].Reason
	if got.Retention.Kind != "pinned" || len(got.Retention.Pins) != 1 || got.Retention.Pins[0] != "owner" {
		t.Fatalf("pinned = %+v", got.Retention)
	}

	if err := c.Unpin("r", "owner"); err != nil {
		t.Fatal(err)
	}
	c.SetPolicy(Policy{Retention: 48 * time.Hour})
	got = c.Releases()[0].Reason
	if got.Retention.Kind != "expiry" || got.Retention.Days != 2 || !got.Retention.Until.After(now) {
		t.Fatalf("expiry = %+v", got.Retention)
	}

	c.SetPolicy(Policy{Mode: "adaptive", ReplayBoost: 24 * time.Hour})
	c.RecordPlay("r", "R", 100)
	c.RecordPlay("r", "R", 100)
	got = c.Releases()[0].Reason
	if got.Retention.Kind != "replay" || got.Retention.Hours != 24 {
		t.Fatalf("replay = %+v", got.Retention)
	}
}

func TestReleaseReasonAddLiveCause(t *testing.T) {
	c := openT(t, t.TempDir(), 1024)
	mustPut(t, c, "r", 0, []byte("x"))
	r := c.Releases()[0]
	if r.Reason.Causes[0].Code != core.CacheCauseUnrecorded {
		t.Fatalf("legacy causes = %+v", r.Reason.Causes)
	}
	at := time.Unix(2000, 0)
	r.Reason.AddCause(core.CacheCause{Code: core.CacheCausePlaying, FirstSeen: at})
	r.Reason.AddCause(core.CacheCause{Code: core.CacheCausePlaying, FirstSeen: at.Add(time.Hour)})
	if len(r.Reason.Causes) != 1 || r.Reason.Causes[0].Code != core.CacheCausePlaying || !r.Reason.Causes[0].FirstSeen.Equal(at) {
		t.Fatalf("live causes = %+v", r.Reason.Causes)
	}
}

func TestReleaseReasonOrdering(t *testing.T) {
	c := openT(t, t.TempDir(), 1024)
	mustPut(t, c, "r", 0, []byte("x"))
	c.RecordProvenance(core.CacheProvenance{ReleaseID: "r", Cause: core.CacheCausePaused})
	c.RecordProvenance(core.CacheProvenance{ReleaseID: "r", Cause: core.CacheCausePlaying})
	got := c.Releases()[0].Reason
	if len(got.Causes) != 2 || got.Causes[0].Code != core.CacheCausePlaying || got.Causes[1].Code != core.CacheCausePaused {
		t.Fatalf("cause order = %+v", got.Causes)
	}
}

func TestReleaseDetailAndProvenanceMetadata(t *testing.T) {
	now := time.Unix(5_000_000, 0)
	c := openT(t, t.TempDir(), 64<<10)
	c.now = func() time.Time { return now }

	mustPut(t, c, "dune", 0, make([]byte, 100))
	mustPut(t, c, "dune", 1, make([]byte, 100))
	mustPut(t, c, "dune", 5, make([]byte, 100))

	c.RecordProvenance(core.CacheProvenance{
		ReleaseID: "dune",
		RatingKey: 1001,
		Title:     "Dune: Part Two",
		File:      "Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.7.1.Atmos-FraMeSToR.mkv",
		Source:    "Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.7.1.Atmos-FraMeSToR",
		Indexer:   "NZBGeek",
		Origin:    "Usenet",
		Size:      1000,
		Cause:     core.CacheCauseShelf,
	})

	rels := c.Releases()
	if len(rels) != 1 {
		t.Fatalf("releases = %d, want 1", len(rels))
	}
	reason := rels[0].Reason
	if reason.Origin != "Usenet" || reason.Indexer != "NZBGeek" || reason.RatingKey != 1001 {
		t.Fatalf("metadata = %+v", reason)
	}
	if reason.CachedAt.IsZero() {
		t.Fatalf("expected CachedAt to be recorded")
	}
	if reason.Info == nil || reason.Info.Height != 2160 || !reason.Info.DolbyVision || !reason.Info.Atmos {
		t.Fatalf("parsed info = %+v", reason.Info)
	}

	detail, ok := c.ReleaseDetail("dune")
	if !ok {
		t.Fatal("expected ReleaseDetail for dune")
	}
	if detail.Segments != 3 || detail.SpanCount != 2 || len(detail.Spans) != 2 {
		t.Fatalf("detail spans = %+v", detail)
	}
	if detail.Spans[0].Start != 0 || detail.Spans[0].End != 1 || detail.Spans[1].Start != 5 || detail.Spans[1].End != 5 {
		t.Fatalf("unexpected span bounds = %+v", detail.Spans)
	}
	if len(detail.Coverage) != 10 || detail.Coverage[0] != 1 || detail.Coverage[5] != 1 || detail.Coverage[2] != 0 {
		t.Fatalf("unexpected coverage = %+v", detail.Coverage)
	}
}
