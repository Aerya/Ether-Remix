package plex

import (
	"context"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// resolveSearchGUIDs fills cold identities for the leading movie and show
// results under one wait budget per render. The bounded resolver coalesces
// concurrent requests for the same title and saves verified results even if
// the client types another character before the provider responds.
func resolveSearchGUIDs(ctx context.Context, source frontend.LibraryGUIDs, titles []frontend.Title, guids map[frontend.RatingKey]string) map[frontend.RatingKey]string {
	resolver, ok := source.(boundedGUIDResolver)
	if !ok || ctx.Err() != nil {
		return guids
	}
	const perKind = 2
	const wait = 350 * time.Millisecond
	var keys []frontend.RatingKey
	remaining := map[frontend.KeyKind]int{frontend.KindMovie: perKind, frontend.KindShow: perKind}
	for _, title := range titles {
		key := title.Key
		kind := key.Kind()
		if remaining[kind] == 0 {
			continue
		}
		remaining[kind]--
		if guids[key] == "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return guids
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	results := make([]string, len(keys))
	var calls sync.WaitGroup
	for i, key := range keys {
		calls.Go(func() {
			results[i] = resolver.ResolveBounded(ctx, key, wait)
		})
	}
	calls.Wait()
	for i, guid := range results {
		if guid == "" {
			continue
		}
		if guids == nil {
			guids = make(map[frontend.RatingKey]string, len(keys))
		}
		guids[keys[i]] = guid
	}
	return guids
}

// maxGUIDResolves bounds the plex.tv identities queued for background
// resolution at once.
const maxGUIDResolves = 64

// resolveGUIDsLater resolves plex.tv movie and show identities that have no
// local mapping yet, off the request path, so a client that names them again
// (Infuse reloads its Watchlist row with every Home) finds their titles. The
// lookup stores each verified mapping; identities it cannot verify stay
// unresolved.
func (s *Server) resolveGUIDsLater(guids []string) {
	guidSrc := s.libraryGUIDs()
	if guidSrc == nil || len(guids) == 0 {
		return
	}
	s.guidResolveMu.Lock()
	if s.guidResolving == nil {
		s.guidResolving = map[string]bool{}
	}
	var queued []string
	for _, g := range guids {
		if len(s.guidResolving) >= maxGUIDResolves {
			break
		}
		if !s.guidResolving[g] {
			s.guidResolving[g] = true
			queued = append(queued, g)
		}
	}
	s.guidResolveMu.Unlock()
	if len(queued) == 0 {
		return
	}
	go func() {
		for _, g := range queued {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if _, err := guidSrc.Lookup(ctx, g); err != nil {
				s.log.Debug("plex identity unresolved", "guid", g, "err", err)
			}
			cancel()
			s.guidResolveMu.Lock()
			delete(s.guidResolving, g)
			s.guidResolveMu.Unlock()
		}
	}()
}
