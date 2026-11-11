package submaker

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestReadyReleasesFollowsCachedBinding(t *testing.T) {
	s, req, rel, _ := fixture(t, false)
	ctx := context.Background()
	other := core.Release{Name: "Example.Movie.2024.1080p.WEB-DL.x264-OTHER", InfoHash: strings.Repeat("c", 40)}
	if got := s.ReadyReleases(ctx, req, []core.Release{rel, other}); got != nil {
		t.Fatalf("nothing is ready yet: %v", got)
	}
	s.Ensure(ctx, req, rel)
	if await(t, s, req, rel) != "ready" {
		t.Fatal("not ready")
	}
	unhashed := rel
	unhashed.InfoHash = ""
	rehashed := rel
	rehashed.InfoHash = strings.Repeat("b", 40)
	got := s.ReadyReleases(ctx, req, []core.Release{other, rel, unhashed, rehashed})
	if len(got) != 4 || got[0] || !got[1] || !got[2] || got[3] {
		t.Fatalf("got %v", got)
	}
	episode := req
	episode.Type, episode.Season, episode.Episode = core.TypeEpisode, 1, 2
	if s.ReadyReleases(ctx, episode, []core.Release{rel}) != nil {
		t.Fatal("another title has no prepared subtitle")
	}
	s.scopes.Global = false
	if s.ReadyReleases(ctx, req, []core.Release{rel}) != nil {
		t.Fatal("a title outside the scope is never preferred")
	}
}
