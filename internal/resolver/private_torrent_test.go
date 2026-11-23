package resolver

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type queuingTorrentIndexer struct {
	*fakeIndexer
	mu      sync.Mutex
	private map[string]bool
	queued  []string
}

func (q *queuingTorrentIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	if q.private[r.ID] || r.Private {
		return nil, core.ErrPrivateUncached
	}
	return nil, core.ErrNotCached
}

func (q *queuingTorrentIndexer) QueueDownload(_ context.Context, r core.Release) ([]byte, error) {
	q.mu.Lock()
	q.queued = append(q.queued, r.ID)
	q.mu.Unlock()
	if r.Private {
		return nil, core.ErrPrivateUncached
	}
	return nil, core.ErrDownloadQueued
}

func TestPrivateTorrentUncachedRecordsHonestReasonInTraceAndHistoryAndNeverQueues(t *testing.T) {
	ctx := context.Background()
	priv := torrent(rel("priv-1", "The.Signal.1999.1080p.BluRay.x264-PRIV", 9000))
	priv.Private = true

	idx := &queuingTorrentIndexer{
		fakeIndexer: &fakeIndexer{releases: []core.Release{priv}},
		private:     map[string]bool{"priv-1": true},
	}
	cfg := testCfg()
	cfg.Debrid.CachedOnly = false // downloads allowed for public torrents, still forbidden for private
	svc := New(idx, fakeHealth{}, nil, cfg, Options{})

	_, err := svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()})
	if !errors.Is(err, core.ErrNotCached) || !errors.Is(err, core.ErrPrivateUncached) {
		t.Fatalf("resolve err = %v, want ErrPrivateUncached", err)
	}
	if len(idx.queued) != 0 {
		t.Fatalf("private release was offered to QueueDownload: %v", idx.queued)
	}

	traces := svc.Traces()
	if len(traces) != 1 {
		t.Fatalf("traces = %d", len(traces))
	}
	if traces[0].Err != core.PrivateTorrentReason {
		t.Fatalf("trace.Err = %q, want %q", traces[0].Err, core.PrivateTorrentReason)
	}
	if len(traces[0].Candidates) != 1 || traces[0].Candidates[0].Reason != core.PrivateTorrentReason {
		t.Fatalf("candidate trace = %+v, want Reason=%q", traces[0].Candidates, core.PrivateTorrentReason)
	}

	hist, total, err := svc.History(ctx, HistoryFilter{TitleKey: matrixTitle().Key})
	if err != nil || total != 1 || len(hist) != 1 {
		t.Fatalf("history total=%d len=%d err=%v", total, len(hist), err)
	}
	if hist[0].Error != core.PrivateTorrentReason {
		t.Fatalf("history Error = %q, want %q", hist[0].Error, core.PrivateTorrentReason)
	}

	// When a lower-ranked public uncached release is also present, the private
	// candidate keeps its private reason and only the public candidate is queued.
	pub := torrent(rel("pub-1", "The.Signal.1999.1080p.WEB-DL.x264-PUB", 8000))
	idx2 := &queuingTorrentIndexer{
		fakeIndexer: &fakeIndexer{releases: []core.Release{priv, pub}},
		private:     map[string]bool{"priv-1": true},
	}
	svc2 := New(idx2, fakeHealth{}, nil, cfg, Options{})
	_, err = svc2.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()})
	if !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("mixed resolve err = %v, want ErrDownloadQueued", err)
	}
	if len(idx2.queued) != 1 || idx2.queued[0] != "pub-1" {
		t.Fatalf("queued = %v, want [pub-1]", idx2.queued)
	}
	reasons := map[string]string{}
	for _, c := range svc2.Traces()[0].Candidates {
		reasons[c.ReleaseID] = c.Reason
	}
	if reasons["priv-1"] != core.PrivateTorrentReason {
		t.Fatalf("priv-1 reason = %q, want %q", reasons["priv-1"], core.PrivateTorrentReason)
	}
}
