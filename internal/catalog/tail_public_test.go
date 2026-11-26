package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// The embedded interfaces panic if the worker tries either kind of request
// while TMDB is disabled.
type tailWithoutKey struct{ TMDB }

func (tailWithoutKey) HasKey() bool { return false }

type tailWithoutExports struct{ ExportSource }

func TestTailExportsStayDormantWithoutAPIKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client TMDB
	}{{"no client", nil}, {"no key", tailWithoutKey{}}} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUniverse()
			c := universeCatalog(t, u, config.Catalog{}, true)
			c.tail.dir = filepath.Join(t.TempDir(), "tmdb-exports")
			c.SetTMDB(tc.client)
			c.exports = tailWithoutExports{}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			now := c.now()
			// Cancel at the scheduler's first wait, after the initial pass.
			c.setNow(func() time.Time {
				cancel()
				return now
			})
			c.RunTail(ctx)
			c.setNow(func() time.Time { return now })
			t.Cleanup(func() { c.installTail(nil) })
			if st := c.TailStatus(); st.Ready || st.LastError != "" {
				t.Fatalf("disabled TMDB started a tail build: %+v", st)
			}

			c.SetTMDB(u)
			c.exports = u
			if err := c.refreshTail(t.Context()); err != nil {
				t.Fatal(err)
			}
			key := core.MovieKey(u.movies[0].id)
			if _, ok := c.tailEntry(key); !ok {
				t.Fatal("configured TMDB did not build the tail index")
			}
			if c.getMeta(t.Context(), "tail_names_walked") == "" {
				t.Fatal("names walk did not resume after configuring TMDB")
			}

			c.SetTMDB(tc.client)
			c.exports = tailWithoutExports{}
			if err := c.refreshTail(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, ok := c.tailEntry(key); !ok {
				t.Fatal("disabling TMDB lost the cached index")
			}
		})
	}
}
