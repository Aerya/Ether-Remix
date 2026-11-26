// Package libattr attributes catalogue titles to the library that lists them.
//
// Several libraries can share a media type and a source and differ only by
// their rules (a built-in Shows library that hides one country's titles and a
// filter library that shows only them). A title then belongs to the library
// whose rules list it, not to the first library of its type. Frontends that
// stamp a library on each item (Plex LibrarySectionID, Emby/Jellyfin
// ParentId) resolve it here, with the same browse-scope membership the
// library's own browse uses.
//
// Cost does not scale with the library: a page of titles costs at most one
// indexed membership read per candidate library of the same type, none when a
// type has a single candidate, and titles resolved once in a request are not
// read again.
package libattr

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/reqmemo"
)

// Members reports which keys a library's browse scope lists, in one indexed
// read. scoped is false when the library has no per-library scope and lists
// every title of its type (frontend.LibraryScopeReader.LibraryMembers).
type Members func(ctx context.Context, lib frontend.LibraryID, keys []frontend.RatingKey) (members map[frontend.RatingKey]bool, scoped bool, err error)

// Base is the library type a title of type t is listed under: seasons and
// episodes live in show libraries.
func Base(t frontend.MediaType) frontend.MediaType {
	switch t {
	case frontend.TypeShow, frontend.TypeSeason, frontend.TypeEpisode:
		return frontend.TypeShow
	}
	return t
}

// Target is the key whose membership decides t's library: a season or an
// episode belongs wherever its show does.
func Target(t *frontend.Title) frontend.RatingKey {
	if t.Type == frontend.TypeSeason || t.Type == frontend.TypeEpisode {
		if t.ShowKey != 0 {
			return t.ShowKey
		}
		parent := t.Key.Parent()
		if t.Type == frontend.TypeEpisode {
			parent = parent.Parent()
		}
		if parent != 0 {
			return parent
		}
	}
	return t.Key
}

// Resolver attributes titles to libraries.
type Resolver struct {
	Members Members
	// Candidates lists, per base type (movie, show), the libraries a title of
	// that type may be attributed to, in preference order: the library being
	// browsed first, then a deterministic order. Plugin libraries own their
	// items by key and are never candidates.
	Candidates map[frontend.MediaType][]frontend.LibraryID
	// Memo names this resolver's request memo. It must change whenever the
	// candidates do (viewer, preferred library), so a request never reuses
	// an attribution made under another preference.
	Memo string
}

type memo struct {
	mu sync.Mutex
	// m holds the attributed library per target key; 0 records a title no
	// candidate lists, so it is not read again.
	m map[frontend.RatingKey]frontend.LibraryID
}

func (r Resolver) memo(ctx context.Context) *memo {
	m, _ := reqmemo.Do(ctx, "libattr:"+r.Memo, func() (*memo, error) {
		return &memo{m: map[frontend.RatingKey]frontend.LibraryID{}}, nil
	})
	return m
}

// Resolve returns the library that lists each title, keyed by title key.
// Plugin titles, titles of a type without candidates and titles no candidate
// lists are absent; the caller keeps its own fallback for them.
func (r Resolver) Resolve(ctx context.Context, titles []frontend.Title) map[frontend.RatingKey]frontend.LibraryID {
	out := make(map[frontend.RatingKey]frontend.LibraryID, len(titles))
	if len(titles) == 0 || len(r.Candidates) == 0 {
		return out
	}
	m := r.memo(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := map[frontend.MediaType][]frontend.RatingKey{}
	queued := map[frontend.RatingKey]bool{}
	for i := range titles {
		t := &titles[i]
		if t.Key.IsPluginKey() {
			continue
		}
		cands := r.Candidates[Base(t.Type)]
		switch len(cands) {
		case 0:
			continue
		case 1:
			// One library of the type: nothing to tell apart, nothing to read.
			out[t.Key] = cands[0]
			continue
		}
		target := Target(t)
		if _, done := m.m[target]; done || queued[target] {
			continue
		}
		queued[target] = true
		pending[Base(t.Type)] = append(pending[Base(t.Type)], target)
	}
	for base, keys := range pending {
		for _, lib := range r.Candidates[base] {
			if len(keys) == 0 {
				break
			}
			if r.Members == nil {
				break
			}
			members, scoped, err := r.Members(ctx, lib, keys)
			if err != nil {
				continue
			}
			if !scoped {
				// An unscoped library lists every title of its type.
				for _, k := range keys {
					m.m[k] = lib
				}
				keys = nil
				break
			}
			rest := keys[:0]
			for _, k := range keys {
				if members[k] {
					m.m[k] = lib
				} else {
					rest = append(rest, k)
				}
			}
			keys = rest
		}
		for _, k := range keys {
			m.m[k] = 0
		}
	}
	for i := range titles {
		t := &titles[i]
		if _, ok := out[t.Key]; ok || t.Key.IsPluginKey() {
			continue
		}
		if lib := m.m[Target(t)]; lib != 0 {
			out[t.Key] = lib
		}
	}
	return out
}
