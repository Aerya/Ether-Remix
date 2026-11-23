package stream

import (
	"bytes"
	"io"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/segcache"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/vault"
)

func TestVaultEpisodeReplayAndStaleExtent(t *testing.T) {
	f := newFakeFetcher()
	a, b := mkvBytes(1, 20000), mkvBytes(2, 40000)
	xml := post(t, f, 2048, postFile{name: "Show.S01E01.mkv", data: a}, postFile{name: "Show.S01E02.mkv", data: b})
	learned := make(chan vault.Layout, 1)
	engine := newTestEngine(t, f, nil, Options{OnLayout: func(_ string, l vault.Layout) { learned <- l }})
	s := openSource(t, engine, "show", xml)
	defer s.Close()
	l := <-learned
	doc, err := nzb.Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	doc.Meta = map[string][]string{nzb.MetaEpisode: {(nzb.EpisodeHint{Season: 1, Episode: 1}).String()}}
	for _, stale := range []bool{false, true} {
		if stale {
			for i := range l.Members {
				m := &l.Members[i]
				if m.Episode == 1 {
					m.Extents[0].Offset++
					m.Extents[0].Size--
					m.Size--
				}
			}
		}
		e := newTestEngine(t, f, nil, Options{})
		src, err := e.OpenRecord(t.Context(), "show-ep1", &vault.Record{NZB: doc, Layout: &l})
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
		if err != nil || !bytes.Equal(got, a) {
			t.Fatalf("stale %t: wrong episode or extent: %v", stale, err)
		}
		if src.(*source).plan.restored == stale {
			t.Fatalf("stale %t: unexpected planner", stale)
		}
		src.Close()
	}
}

// The second file starts at slot zero in the resolver's episode NZB and
// after episode one in the original posting. A cold vault player must read
// the exact warmed bytes even after every provider goes offline.
func TestVaultReopensPreloadedEpisodeOffline(t *testing.T) {
	f := newFakeFetcher()
	episode := mkvBytes(2, 40000)
	xml := post(t, f, 2048, postFile{name: "Show.S01E01.mkv", data: mkvBytes(1, 20000)}, postFile{name: "Show.S01E02.mkv", data: episode})
	doc, err := nzb.Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := doc.Subset(func(file *nzb.File) bool { return file.Index == 1 })
	if !ok {
		t.Fatal("empty episode subset")
	}
	cache, err := segcache.Open(t.TempDir(), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	tgt := core.ReleaseTarget{Season: 1, Episode: 2, PackID: "original-pack"}
	id := core.TargetReleaseID(tgt.PackID, tgt)
	library, err := vault.Open(t.Context(), vault.Options{DB: store.OpenTest(t), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	key, err := library.Put(t.Context(), vault.PutRequest{Release: core.Release{ID: id, IndexerID: "original", Name: "Show.S01.COMPLETE", Target: &tgt}, NZB: xml})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := library.Entry(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	lib := vault.ReleaseFor(entry)
	tgt.PackID = lib.ID
	lib.Target = &tgt
	lib.ID = core.TargetReleaseID(tgt.PackID, tgt)
	if lib.ID != id || vault.KeyOf(lib) != key {
		t.Fatalf("library replay lost cache identity: %v", lib.ID)
	}
	if k, ok, err := library.ByAlias(t.Context(), "original", tgt.PackID); !ok || err != nil || k != key {
		t.Fatalf("missing pack alias: %v", err)
	}

	if err := cache.Pin(id, "playback"); err != nil {
		t.Fatal(err)
	}
	warm := newTestEngine(t, f, cache, Options{})
	if err := warm.DurablePrefetch(t.Context(), id, target, 0, int64(len(episode))); err != nil {
		t.Fatal(err)
	}
	offline := newFakeFetcher()
	offline.failAll = true
	healthy := make(chan string, 1)
	replay := newTestEngine(t, offline, cache, Options{OnHealthy: func(id string) { healthy <- id }})
	doc.Meta = map[string][]string{nzb.MetaEpisode: {(nzb.EpisodeHint{Season: 1, Episode: 2}).String()}}
	src, err := replay.OpenRecordAlias(t.Context(), lib.ID, "vault-feedback", &vault.Record{NZB: doc}, target)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
	if err != nil || !bytes.Equal(got, episode) {
		t.Fatalf("offline replay differs: %v", err)
	}
	src.Close()
	replay.Close()
	select {
	case <-healthy:
		t.Fatal("cached bytes reported provider health")
	default:
	}
	online := newTestEngine(t, f, nil, Options{OnHealthy: func(id string) { healthy <- id }})
	src, err = online.OpenRecordAlias(t.Context(), id, "vault-feedback", &vault.Record{NZB: doc}, target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.ReadAt(make([]byte, 1), 0)
	src.Close()
	online.Close()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case k := <-healthy:
		if k != "vault-feedback" {
			t.Fatalf("unexpected feedback ID %s", k)
		}
	default:
		t.Fatal("missing live playback health")
	}
}
