package adapters

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pluginitems"
)

// maxDeferredExpansions bounds the container expansions waiting for a free
// background slot; the oldest waiting one gives way.
const maxDeferredExpansions = 64

// deferredRetry is how soon queued expansions try again when no running
// expansion finishes first.
const deferredRetry = 5 * time.Second

type deferredExpansion struct {
	rec       pluginitems.KindRecord
	lib       core.Library
	container core.KindItem
	at        time.Time
}

type deferredExpansions struct {
	mu      sync.Mutex
	pending map[string]deferredExpansion
	timer   *time.Timer
}

var expansionQueue deferredExpansions

// deferExpansion queues a container expansion the job pool could not take,
// to run as soon as a slot frees.
func (c *KindCatalog) deferExpansion(key string, rec pluginitems.KindRecord, lib core.Library, container core.KindItem) {
	q := &expansionQueue
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending == nil {
		q.pending = map[string]deferredExpansion{}
	}
	if _, ok := q.pending[key]; !ok && len(q.pending) >= maxDeferredExpansions {
		oldest, at := "", time.Time{}
		for k, d := range q.pending {
			if oldest == "" || d.at.Before(at) {
				oldest, at = k, d.at
			}
		}
		delete(q.pending, oldest)
	}
	q.pending[key] = deferredExpansion{rec: rec, lib: lib, container: container, at: time.Now()}
	if q.timer == nil {
		q.timer = time.AfterFunc(deferredRetry, c.kickDeferredExpansions)
	}
}

// kickDeferredExpansions starts queued expansions, oldest first, while the
// pool takes them; the rest stay queued for the next kick.
func (c *KindCatalog) kickDeferredExpansions() {
	q := &expansionQueue
	q.mu.Lock()
	q.timer = nil
	batch := make([]struct {
		key string
		d   deferredExpansion
	}, 0, len(q.pending))
	for k, d := range q.pending {
		batch = append(batch, struct {
			key string
			d   deferredExpansion
		}{k, d})
	}
	q.mu.Unlock()
	for i := 0; i < len(batch); i++ {
		for j := i + 1; j < len(batch); j++ {
			if batch[j].d.at.Before(batch[i].d.at) {
				batch[i], batch[j] = batch[j], batch[i]
			}
		}
	}
	for _, b := range batch {
		q.mu.Lock()
		if _, still := q.pending[b.key]; !still {
			q.mu.Unlock()
			continue
		}
		delete(q.pending, b.key)
		q.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		// Still busy (expandContainer queues it again) or fresh now.
		_, _ = c.expandContainer(ctx, b.d.rec, b.d.lib, b.d.container, false)
		cancel()
	}
}

// expandNamedContainer expands the one result container whose name is the
// query (an artist search) and adds its stored children to the results.
func (c *KindCatalog) expandNamedContainer(ctx context.Context, rec pluginitems.KindRecord, lib core.Library, query string, items []core.KindItem) ([]core.KindItem, core.RatingKey, map[core.RatingKey]bool) {
	if rec.Decl.Browse.Expand == nil {
		return items, 0, nil
	}
	want := searchName(query)
	if want == "" {
		return items, 0, nil
	}
	var hit *core.KindItem
	for i := range items {
		if rec.Decl.Browse.Expand.ExpandsLevel(items[i].Level) && searchName(items[i].Title) == want {
			hit = &items[i]
			break
		}
	}
	if hit == nil {
		return items, 0, nil
	}
	named := *hit
	c.expandContainer(ctx, rec, lib, named, true)
	page, err := c.items.Children(ctx, named.Ref, "", 200)
	if err != nil || len(page.Items) == 0 {
		return items, 0, nil
	}
	discography := make(map[core.RatingKey]bool, len(page.Items))
	seen := make(map[core.RatingKey]bool, len(items)+len(page.Items))
	for _, it := range items {
		seen[it.Key] = true
	}
	for _, it := range page.Items {
		if it.Key == 0 {
			continue
		}
		discography[it.Key] = true
		if !seen[it.Key] {
			seen[it.Key] = true
			items = append(items, it)
		}
	}
	return items, named.Key, discography
}

// searchName folds a name for "is this exactly what was searched": case,
// punctuation, spacing and a leading "the" do not matter.
func searchName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '&':
			b.WriteString("and")
		}
	}
	out := b.String()
	return strings.TrimPrefix(out, "the")
}
