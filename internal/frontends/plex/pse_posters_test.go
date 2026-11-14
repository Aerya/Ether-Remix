package plex

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestPSEPosterCacheControl(t *testing.T) {
	if got := psePosterCacheControl(nil); got != "private, max-age=0, must-revalidate" {
		t.Fatalf("sans témoin : %q", got)
	}
	_, origin := frontend.WithPosterOrigin(context.Background())
	if got := psePosterCacheControl(origin); got != "private, max-age=0, must-revalidate" {
		t.Fatalf("repli TMDB : %q", got)
	}
}

func TestPSEPosterSize(t *testing.T) {
	for _, c := range []struct {
		element string
		width   int
		want    string
	}{
		{"thumb", 600, "w500"}, {"poster", 600, "w500"}, {"thumb", 300, "w300"}, {"thumb", 400, "w342"},
		{"thumb", 1000, "w780"}, {"thumb", 0, "original"},
		{"art", 600, "w780"}, {"art", 1280, "w1280"}, {"background", 1920, "original"}, {"clearLogo", 600, "w780"},
	} {
		if got := psePosterSize(c.element, c.width); got != c.want {
			t.Errorf("psePosterSize(%q, %d) = %q, attendu %q", c.element, c.width, got, c.want)
		}
	}
}
