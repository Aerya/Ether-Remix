package discovery

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/overlays"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestArtworkSharesVersionAcrossKinds(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	calls := 0
	o := overlays.New(db, overlays.Options{FactsFor: func(context.Context, *core.User, *core.Title, bool, bool) overlays.Facts {
		calls++
		return overlays.Facts{}
	}})
	if err := o.SaveSettings(ctx, overlays.Settings{Enabled: true, Status: true}); err != nil {
		t.Fatal(err)
	}
	d := &Features{Overlays: o}
	selected := d.SelectArtwork(ctx, &core.User{ID: 1}, &core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, PosterPath: "/poster.jpg", LogoPath: "/logo.png"}, "fr")
	if selected.PosterVersion == "" || selected.PosterVersion != selected.LogoVersion {
		t.Fatalf("versions: %+v", selected)
	}
	if calls != 1 {
		t.Fatalf("facts loaded %d times for one title", calls)
	}
}
