package resolver

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

const maxLiveSearchResults = 4000

type liveSearchContextKey struct{}

// liveSearch tracks normalized search results arriving from an in-flight
// indexer search for one searchCacheKey. It allows concurrent version-header
// probing (and play-intent VersionSlots renders while a slow straggler indexer
// is still finishing) to see each version's release as soon as its indexer
// answers or debrid confirms cache status, without writing an incomplete fleet
// answer into searchCache.
type liveSearch struct {
	mu      sync.Mutex
	key     string
	req     core.SearchRequest
	all     []core.Release
	byID    map[string]int
	rev     uint64
	done    bool
	changed chan struct{}
}

func mergeLiveRelease(existing, incoming core.Release) core.Release {
	if len(incoming.DebridCached) == 0 {
		return existing
	}
	merged := existing
	if len(merged.DebridCached) == 0 {
		merged.DebridCached = append([]string(nil), incoming.DebridCached...)
		return merged
	}
	out := append([]string(nil), merged.DebridCached...)
	for _, kind := range incoming.DebridCached {
		if !slices.Contains(out, kind) {
			out = append(out, kind)
		}
	}
	merged.DebridCached = out
	return merged
}

func (ls *liveSearch) mergeLocked(norm []core.Release) {
	for _, r := range norm {
		if r.ID == "" {
			continue
		}
		if idx, ok := ls.byID[r.ID]; ok {
			ls.all[idx] = mergeLiveRelease(ls.all[idx], r)
			continue
		}
		if len(ls.all) >= maxLiveSearchResults {
			continue
		}
		ls.byID[r.ID] = len(ls.all)
		ls.all = append(ls.all, r)
	}
}

func (ls *liveSearch) add(s *Service, batch []core.Release) {
	if ls == nil || len(batch) == 0 {
		return
	}
	ls.mu.Lock()
	req := ls.req
	ls.mu.Unlock()
	norm := s.normalizeSearchResults(req, append([]core.Release(nil), batch...))
	if len(norm) == 0 {
		return
	}
	ls.mu.Lock()
	if ls.done {
		ls.mu.Unlock()
		return
	}
	ls.mergeLocked(norm)
	// Always bump rev and wake observers even when release IDs were already
	// present: debrid earlyCacheChecks re-emits newly confirmed cached torrents
	// under their existing ID after updating the debrid cache table.
	ls.rev++
	close(ls.changed)
	ls.changed = make(chan struct{})
	ls.mu.Unlock()
}

// retarget sets the request arriving batches are normalized for, before
// the search that feeds ls starts.
func (ls *liveSearch) retarget(req core.SearchRequest) {
	ls.mu.Lock()
	ls.req = req
	ls.mu.Unlock()
}

func (ls *liveSearch) snapshot() (results []core.Release, rev uint64, done bool, changed <-chan struct{}) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if len(ls.all) > 0 {
		results = append([]core.Release(nil), ls.all...)
	}
	return results, ls.rev, ls.done, ls.changed
}

func (s *Service) beginLiveSearch(req core.SearchRequest) *liveSearch {
	key := searchCacheKey(req)
	ls := &liveSearch{
		key:     key,
		req:     req,
		byID:    make(map[string]int),
		changed: make(chan struct{}),
	}
	s.liveMu.Lock()
	prev := s.liveSearches[key]
	s.liveMu.Unlock()
	if prev != nil {
		if seed, _, _, _ := prev.snapshot(); len(seed) > 0 {
			ls.mergeLocked(seed)
		}
	}
	s.liveMu.Lock()
	if s.liveSearches == nil {
		s.liveSearches = make(map[string]*liveSearch)
	}
	s.liveSearches[key] = ls
	waiters := s.liveWaiters[key]
	delete(s.liveWaiters, key)
	s.liveMu.Unlock()
	for _, w := range waiters {
		close(w)
	}
	return ls
}

// finishLiveSearch marks ls complete and removes it from s.liveSearches.
// Callers must write s.storeSearchCache before calling finishLiveSearch so
// observers see no gap between liveSearch completion and the populated cache.
// Safe to call more than once.
func (s *Service) finishLiveSearch(ls *liveSearch, final []core.Release) {
	if ls == nil {
		return
	}
	ls.mu.Lock()
	if ls.done {
		ls.mu.Unlock()
		return
	}
	if len(final) > 0 {
		ls.mergeLocked(final)
	}
	ls.done = true
	ls.rev++
	close(ls.changed)
	ls.mu.Unlock()

	s.liveMu.Lock()
	if s.liveSearches[ls.key] == ls {
		delete(s.liveSearches, ls.key)
	}
	s.liveMu.Unlock()
}

func (s *Service) activeLiveSearch(key string) *liveSearch {
	s.liveMu.Lock()
	ls := s.liveSearches[key]
	s.liveMu.Unlock()
	return ls
}

func (s *Service) peekLiveSearch(key string) []core.Release {
	ls := s.activeLiveSearch(key)
	if ls == nil {
		return nil
	}
	res, _, _, _ := ls.snapshot()
	return res
}

func (s *Service) notifyLiveWaiters(key string) {
	s.liveMu.Lock()
	waiters := s.liveWaiters[key]
	delete(s.liveWaiters, key)
	s.liveMu.Unlock()
	for _, w := range waiters {
		close(w)
	}
}

func (s *Service) registerLiveWaiter(key string) (*liveSearch, chan struct{}) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if ls := s.liveSearches[key]; ls != nil {
		return ls, nil
	}
	w := make(chan struct{})
	if s.liveWaiters == nil {
		s.liveWaiters = make(map[string][]chan struct{})
	}
	s.liveWaiters[key] = append(s.liveWaiters[key], w)
	return nil, w
}

func (s *Service) removeLiveWaiter(key string, w chan struct{}) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	list := s.liveWaiters[key]
	for i, ch := range list {
		if ch == w {
			list = append(list[:i], list[i+1:]...)
			if len(list) == 0 {
				delete(s.liveWaiters, key)
			} else {
				s.liveWaiters[key] = list
			}
			return
		}
	}
}

// WatchVersionSlots evaluates configured version slots for req from the search
// cache or an active live search for the title, invoking emit with the current
// variants immediately when releases are already known and again whenever
// arriving search batches update the candidate pool. It never starts an indexer
// search of its own.
//
// If waitForSearch is non-nil and neither the search cache nor a live search
// exists yet, WatchVersionSlots waits until a live search begins, searchCache
// is populated, waitForSearch closes, or ctx ends.
func (s *Service) WatchVersionSlots(
	ctx context.Context,
	req core.ResolveRequest,
	limit int,
	waitForSearch <-chan struct{},
	emit func([]core.ReleaseVariant),
) error {
	if req.Title == nil {
		return fmt.Errorf("resolver: nil title")
	}
	if emit == nil {
		return nil
	}
	if kind := req.Title.Key.Kind(); kind != core.KindMovie && kind != core.KindEpisode {
		return nil
	}
	key := searchCacheKey(searchRequestFor(req.Title))

	emitSlots := func(results []core.Release, searchWarm bool) {
		if ctx.Err() != nil {
			return
		}
		if vs, err := s.versionSlotsWithResults(ctx, req, limit, true, results, searchWarm); err == nil && len(vs) > 0 {
			emit(vs)
		}
	}

	if results, ok := s.peekSearchCache(key); ok {
		emitSlots(results, true)
		return nil
	}

	ls := s.activeLiveSearch(key)
	for ls == nil && waitForSearch != nil {
		var w chan struct{}
		ls, w = s.registerLiveWaiter(key)
		if ls != nil {
			break
		}
		// Re-check searchCache after registering the waiter: storeSearchCache
		// writes searchCache before calling notifyLiveWaiters, so this closes
		// the wakeup race without nesting locks.
		if results, ok := s.peekSearchCache(key); ok {
			s.removeLiveWaiter(key, w)
			emitSlots(results, true)
			return nil
		}
		select {
		case <-w:
			if results, ok := s.peekSearchCache(key); ok {
				emitSlots(results, true)
				return nil
			}
			ls = s.activeLiveSearch(key)
		case <-waitForSearch:
			s.removeLiveWaiter(key, w)
			if results, ok := s.peekSearchCache(key); ok {
				emitSlots(results, true)
				return nil
			}
			ls = s.activeLiveSearch(key)
			waitForSearch = nil
		case <-ctx.Done():
			s.removeLiveWaiter(key, w)
			return ctx.Err()
		}
	}

	if ls == nil {
		if results, ok := s.peekSearchCache(key); ok {
			emitSlots(results, true)
			return nil
		}
		emitSlots(nil, false)
		return nil
	}

	var lastRev uint64
	emitted := false
	for {
		snap, rev, done, ch := ls.snapshot()
		if done {
			if cached, ok := s.peekSearchCache(key); ok {
				emitSlots(cached, true)
				return nil
			}
			if !emitted || rev != lastRev {
				emitSlots(snap, false)
			}
			return nil
		}
		if len(snap) > 0 && (!emitted || rev != lastRev) {
			lastRev = rev
			emitted = true
			emitSlots(snap, false)
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
