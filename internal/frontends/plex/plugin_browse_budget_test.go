package plex

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type pluginBrowseExperience struct {
	frontend.Experience
	items map[core.RatingKey]core.Title
}

func (e *pluginBrowseExperience) Item(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
	if t, ok := e.items[req.Key]; ok {
		return frontend.ItemDetail{Title: t}, nil
	}
	return frontend.ItemDetail{}, core.ErrNotFound
}

func (e *pluginBrowseExperience) Children(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ core.RatingKey, _ frontend.ItemQuery) (core.Page, error) {
	return core.Page{}, nil
}

type spyPluginDetailVariants struct {
	mu           sync.Mutex
	refillCalls  int
	labelCalls   int
	noLabelCalls int
}

func (s *spyPluginDetailVariants) Variants(ctx context.Context, req core.ResolveRequest, max int) ([]core.ReleaseVariant, error) {
	return s.VersionSlots(ctx, req, max, true)
}

func (s *spyPluginDetailVariants) VersionSlots(_ context.Context, _ core.ResolveRequest, _ int, labels bool) ([]core.ReleaseVariant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if labels {
		s.labelCalls++
	} else {
		s.noLabelCalls++
	}
	return nil, nil
}

func (s *spyPluginDetailVariants) RefillSearchCache(*core.Title) <-chan struct{} {
	s.mu.Lock()
	s.refillCalls++
	s.mu.Unlock()
	ch := make(chan struct{})
	close(ch)
	return ch
}

type spyPrewarmPlayback struct {
	*fakePlayback
	mu           sync.Mutex
	prewarmCalls int
	fileChecks   int
}

func (p *spyPrewarmPlayback) Prewarm(_ context.Context, _ *core.User, _ core.RatingKey, _ core.ClientCaps, _ core.PrewarmOptions) bool {
	p.mu.Lock()
	p.prewarmCalls++
	p.mu.Unlock()
	return true
}

func (p *spyPrewarmPlayback) CheckFileAvailability(_ context.Context, _ *core.User, _ core.RatingKey, _ core.ClientCaps, _ string) (bool, error) {
	p.mu.Lock()
	p.fileChecks++
	p.mu.Unlock()
	return true, nil
}

func TestPlexPluginMetadataDetailSkipsBuiltInRefillPrewarmWatchAndBatchChildren(t *testing.T) {
	k1 := core.PluginKey(601)
	k2 := core.PluginKey(602)
	t1 := core.Title{Key: k1, Type: core.TypeMovie, Title: "Album One", Year: 2024, Runtime: 40 * time.Minute}
	t2 := core.Title{Key: k2, Type: core.TypeMovie, Title: "Album Two", Year: 2024, Runtime: 45 * time.Minute}

	variants := &spyPluginDetailVariants{}
	pb := &spyPrewarmPlayback{fakePlayback: newFakePlayback(core.MediaInfo{})}
	exp := &pluginBrowseExperience{
		Experience: frontend.NoopExperience(),
		items: map[core.RatingKey]core.Title{
			k1: t1,
			k2: t2,
		},
	}
	f := newFixture(func(d *testServerDeps) {
		d.DisableEnrichment = true
		d.Experience = exp
	})
	t.Cleanup(func() { f.srv.Close() })
	f.srv.setPlayback(pb)
	f.srv.setVariants(variants)

	// 1. Single plugin detail view with checkFiles=1 must NOT trigger built-in
	// RefillSearchCache, Prewarm, CheckFileAvailability, or background watchVariants.
	rec := f.get(t, "/library/metadata/"+k1.String()+"?checkFiles=1", jsonAccept...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET detail = %d: %s", rec.Code, rec.Body.String())
	}

	variants.mu.Lock()
	refillsAfterDetail := variants.refillCalls
	labelsAfterDetail := variants.labelCalls
	variants.mu.Unlock()
	if refillsAfterDetail != 0 {
		t.Fatalf("plugin detail view triggered %d built-in RefillSearchCache calls, want 0", refillsAfterDetail)
	}
	pb.mu.Lock()
	prewarms, checks := pb.prewarmCalls, pb.fileChecks
	pb.mu.Unlock()
	if prewarms != 0 {
		t.Fatalf("plugin detail view triggered %d built-in Prewarm calls, want 0", prewarms)
	}
	if checks != 0 {
		t.Fatalf("plugin detail view triggered %d built-in CheckFileAvailability calls, want 0", checks)
	}
	f.srv.variantWatch.mu.Lock()
	pendingWatches := len(f.srv.variantWatch.pending)
	f.srv.variantWatch.mu.Unlock()
	if pendingWatches != 0 {
		t.Fatalf("plugin detail view registered %d background variant watches, want 0", pendingWatches)
	}

	// 2. Batch metadata fetch with includeChildren=1 must NOT fan out VersionSlots(labels=true).
	batchRec := f.get(t, "/library/metadata/"+k1.String()+","+k2.String()+"?includeChildren=1", jsonAccept...)
	if batchRec.Code != http.StatusOK {
		t.Fatalf("GET batch detail = %d: %s", batchRec.Code, batchRec.Body.String())
	}
	variants.mu.Lock()
	labelsAfterBatch := variants.labelCalls
	variants.mu.Unlock()
	if labelsAfterBatch != labelsAfterDetail {
		t.Fatalf("batch metadata with includeChildren=1 fanned out %d resolving VersionSlots calls, want 0", labelsAfterBatch-labelsAfterDetail)
	}
}
