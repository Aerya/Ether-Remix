package collections_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestCollectionCardProbesLiveFacetWithoutMaterializingMembers(t *testing.T) {
	e := newEnv(t, withOpts(collections.WithCollectionCardFilter(func(_ context.Context, u *core.User, typ core.MediaType) (core.Filter, bool, error) {
		f := core.Filter{Type: typ, LocalOnly: true}
		if !u.IsOwner {
			f.ExcludeGenres = []string{"Science Fiction"}
		}
		return f, true, nil
	})))
	sp := spec("Science Fiction cards", "movie", catalogSource(), rl("genre", "is", "Science Fiction"))
	sp.LocalOnly = true
	sp.Artwork = core.CollectionArtwork{Mode: "custom", URL: "/custom-card.jpg"}
	saved, err := e.svc.CreateSpecDeferred(e.ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	key, err := core.ParseRatingKey(saved.RatingKey)
	if err != nil {
		t.Fatal(err)
	}
	db := e.db.(*sql.DB)
	for _, viewer := range []*core.User{{ID: 1, IsOwner: true}, {ID: 2}} {
		headers, err := e.svc.CollectionCardHeaders(e.ctx, viewer, []core.RatingKey{key})
		if err != nil {
			t.Fatal(err)
		}
		h := headers[key]
		if viewer.IsOwner {
			if h == nil || h.PosterPath != sp.Artwork.URL || h.ChildCount == 0 {
				t.Fatalf("missing owner card or assigned artwork: %+v", h)
			}
		} else if h != nil {
			t.Fatal("a facet with no visible titles produced a card")
		}
	}
	var built bool
	if err := db.QueryRowContext(e.ctx, "SELECT EXISTS (SELECT 1 FROM collections_members WHERE spec_id=?)", key.TMDB()).Scan(&built); err != nil || built {
		t.Fatalf("reading cards materialized facet members: built=%v err=%v", built, err)
	}
}
