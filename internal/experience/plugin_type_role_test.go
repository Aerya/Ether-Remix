package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// A plugin music level projects as a core show/season/episode but is asked
// for by its role (Plex type=8/9/10): the in-process recheck must accept the
// role, or every typed plugin listing that walks in process is empty (#777).
func TestMatchesUserQueryAcceptsPluginLevelRole(t *testing.T) {
	s := &Service{}
	artist := core.Title{Key: 1, Type: core.TypeShow, MediaRole: core.MediaRoleArtist, MBItemType: "MusicArtist"}
	track := core.Title{Key: 2, Type: core.TypeEpisode, MediaRole: core.MediaRoleTrack, MBItemType: "Audio"}
	cases := []struct {
		title core.Title
		types []frontend.MediaType
		want  bool
	}{
		{artist, []frontend.MediaType{"artist"}, true},
		{artist, []frontend.MediaType{"album"}, false},
		{track, []frontend.MediaType{"track"}, true},
		{track, []frontend.MediaType{"artist"}, false},
		{core.Title{Key: 3, Type: core.TypeMovie}, []frontend.MediaType{"artist"}, false},
	}
	for _, c := range cases {
		got, err := s.matchesUserQuery(context.Background(), nil, c.title, frontend.ItemQuery{Types: c.types})
		if err != nil || got != c.want {
			t.Fatalf("%s as %v = %v, %v; want %v", c.title.MediaRole, c.types, got, err, c.want)
		}
	}
}
