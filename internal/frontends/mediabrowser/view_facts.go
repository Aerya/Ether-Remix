package mediabrowser

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// libraryCounter is the indexed library size. The view list must not browse
// or hydrate titles to learn it.
type libraryCounter interface {
	LibraryCount(context.Context, *frontend.Viewer, string) (int, error)
}

type indexedLibraryCounter interface {
	IndexedLibraryCount(context.Context, *frontend.Viewer, string) (int, error)
}

type viewFactGen struct {
	catalog   uint64
	libraries uint64
	kindSync  uint64
}

type viewFact struct {
	gen   viewFactGen
	count int
}

type viewFactKey struct {
	viewer   int64
	view     string
	rating   string
	sections string
}

// viewFactCacheLimit bounds remembered (viewer, view) counts. Entries
// are keyed by viewer, view, rating and section set, so without a cap
// the map grows with every distinct combination ever browsed and is
// never pruned (a stale generation only replaces its own key).
const viewFactCacheLimit = 4096

type viewFactCache struct {
	mu sync.Mutex
	m  map[viewFactKey]viewFact
}

func (i *instance) fillJellyfinViewFacts(r *http.Request, viewer *frontend.Viewer, viewID string, item map[string]any) {
	if r == nil || item == nil {
		return
	}
	if count, ok := i.cachedLibraryCount(r.Context(), viewer, viewID); ok {
		item["ChildCount"] = count
	}
}

func (i *instance) cachedLibraryCount(ctx context.Context, viewer *frontend.Viewer, viewID string) (int, bool) {
	if i == nil || viewer == nil || i.host.Experience == nil {
		return 0, false
	}
	var countFn func(context.Context, *frontend.Viewer, string) (int, error)
	if ic, ok := i.host.Experience.(indexedLibraryCounter); ok {
		countFn = ic.IndexedLibraryCount
	} else if lc, ok := i.host.Experience.(libraryCounter); ok {
		countFn = lc.LibraryCount
	} else {
		return 0, false
	}
	key, gen, cacheable := i.viewFactKey(viewer, viewID)
	if cacheable {
		if fact, hit := i.views.get(key); hit && fact.gen == gen {
			return fact.count, true
		}
	}
	count, err := countFn(ctx, viewer, viewID)
	if err != nil {
		return 0, false
	}
	if cacheable {
		i.views.store(key, viewFact{gen: gen, count: count})
	}
	return count, true
}

func (c *viewFactCache) get(key viewFactKey) (viewFact, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fact, ok := c.m[key]
	return fact, ok
}

func (c *viewFactCache) store(key viewFactKey, fact viewFact) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[viewFactKey]viewFact{}
	}
	if _, exists := c.m[key]; !exists && len(c.m) >= viewFactCacheLimit {
		// Counts are cheap to recompute and generation-checked on
		// read, so dropping the whole map when full is safe.
		clear(c.m)
	}
	c.m[key] = fact
}

func (i *instance) viewFactKey(viewer *frontend.Viewer, viewID string) (viewFactKey, viewFactGen, bool) {
	if i.host.PageFreshness == nil || viewer == nil {
		return viewFactKey{}, viewFactGen{}, false
	}
	sections := append([]frontend.LibraryID(nil), viewer.AllowedLibraries...)
	slices.Sort(sections)
	f := i.host.PageFreshness()
	return viewFactKey{viewer: viewer.ID, view: viewID, rating: viewer.MaxContentRating, sections: fmt.Sprint(sections)}, viewFactGen{catalog: f.Catalog, libraries: f.Libraries, kindSync: f.KindSync}, true
}
