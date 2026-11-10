package discovery

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestFrontendPreservesArtworkPagePreparation(t *testing.T) {
	type pageKey struct{}
	viewer := &core.User{ID: 7}
	titles := []core.Title{{Title: "Example"}}
	d := &Features{PrepareArtworkPage: func(ctx context.Context, got *core.User, page []core.Title) context.Context {
		if got != viewer || len(page) != 1 || page[0].Title != "Example" {
			t.Fatal("artwork page inputs lost")
		}
		return context.WithValue(ctx, pageKey{}, true)
	}}
	adapted := d.Frontend()
	if adapted.PrepareArtworkPage == nil {
		t.Fatal("artwork batching lost by frontend adapter")
	}
	if adapted.PrepareArtworkPage(context.Background(), viewer, titles).Value(pageKey{}) != true {
		t.Fatal("prepared context lost")
	}
}
