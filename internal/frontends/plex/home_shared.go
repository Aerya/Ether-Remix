package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/homefilter"
)

// homeSharedEntry carries only shared discovery candidate hubs across a
// client's per-directory Home fan-out, never mixed household recommendation
// rows or authorized wire metadata. Every section filters its own media type
// and visibility before rendering.
type homeSharedEntry struct {
	done      chan struct{}
	seen      map[string]bool
	expires   time.Time
	discovery []frontend.Hub
	err       error
	filter    *homefilter.Snapshot
}

func (s *Server) sharedDiscovery(ctx context.Context, pr *Request, hc frontend.HubContext, directory, pinned string, discoveryCount int) ([]frontend.Hub, *homefilter.Snapshot, error) {
	filter := &homefilter.Snapshot{}
	load := func() ([]frontend.Hub, error) {
		ctx := homefilter.With(ctx, filter)
		var discovery []frontend.Hub
		for _, spec := range s.discoveryHubs(ctx, pr, discoveryCount) {
			discovery = append(discovery, spec.Hub)
		}
		return discovery, nil
	}
	if pr.User == nil || hc.ClientID == "" {
		discovery, err := load()
		return discovery, filter, err
	}
	u := pr.User
	key := fmt.Sprintf("%d|%t|%q|%v|%q|%q|%q|%q|%q|%q|%t|%s|%d|%q", u.ID, u.IsOwner, u.MaxContentRating, u.AllowedLibraries, hc.ClientID, hc.SessionID, hc.Product, hc.Platform, hc.Device, hc.Daypart, hc.Weekend, hc.Now.Format("2006-01-02"), discoveryCount, pinned)
	now := time.Now()
	s.homeSharedMu.Lock()
	if s.homeShared == nil {
		s.homeShared = make(map[string]*homeSharedEntry)
	}
	for k, e := range s.homeShared {
		if !e.expires.IsZero() && now.After(e.expires) {
			delete(s.homeShared, k)
		}
	}
	if e := s.homeShared[key]; e != nil && !e.seen[directory] {
		e.seen[directory] = true
		s.homeSharedMu.Unlock()
		select {
		case <-e.done:
			if e.err != nil && ctx.Err() == nil {
				discovery, err := load()
				return discovery, filter, err
			}
			return e.discovery, e.filter, e.err
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if len(s.homeShared) >= 8 {
		for k := range s.homeShared {
			delete(s.homeShared, k)
			break
		}
	}
	e := &homeSharedEntry{done: make(chan struct{}), seen: map[string]bool{directory: true}, filter: filter}
	s.homeShared[key] = e
	s.homeSharedMu.Unlock()
	completed := false
	defer func() {
		if completed {
			return
		}
		s.homeSharedMu.Lock()
		e.err = errors.New("home discovery build interrupted")
		if s.homeShared[key] == e {
			delete(s.homeShared, key)
		}
		close(e.done)
		s.homeSharedMu.Unlock()
	}()
	discovery, err := load()
	size := 0
	for _, h := range discovery {
		size += len(h.Items)
	}
	cacheable := err == nil && len(discovery) <= 64 && size <= 512
	if cacheable {
		encoded, encodeErr := json.Marshal(discovery)
		cacheable = encodeErr == nil && len(encoded) <= 1<<20
	}
	s.homeSharedMu.Lock()
	e.discovery, e.err = discovery, err
	e.expires = time.Now().Add(10 * time.Second)
	if !cacheable {
		if s.homeShared[key] == e {
			delete(s.homeShared, key)
		}
	}
	close(e.done)
	completed = true
	s.homeSharedMu.Unlock()
	return discovery, filter, err
}
