package experience

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type jellyfinSearchThemes struct {
	views []frontend.PSETheme
	keys  map[string][]core.RatingKey
}

func (t jellyfinSearchThemes) Themes() []frontend.PSETheme { return t.views }

func (t jellyfinSearchThemes) RowKeys(_ context.Context, row string, _ *core.User) []core.RatingKey {
	return t.keys[row]
}

func TestPSEJellyfinSearchAndPlaybackUseThemeGrants(t *testing.T) {
	show := core.Title{Key: core.ShowKey(1), Type: core.TypeShow, ContentRating: "TV-G", Title: "Animation"}
	episode := core.Title{Key: core.EpisodeKey(1, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-G", Title: "Animation: début"}
	adult := core.Title{Key: core.EpisodeKey(1, 1, 2), Type: core.TypeEpisode, ShowKey: show.Key, ContentRating: "TV-MA", Title: "Animation: suite"}
	unknownShow := core.Title{Key: core.ShowKey(2), Type: core.TypeShow, ContentRating: "TV-G", Title: "Autre animation"}
	unknownEpisode := core.Title{Key: core.EpisodeKey(2, 1, 1), Type: core.TypeEpisode, ShowKey: unknownShow.Key, ContentRating: "TV-G", Title: "Autre animation: début"}
	frontend.SetPSEThemes(jellyfinSearchThemes{
		views: []frontend.PSETheme{
			{Section: 5101, Title: "Enfants", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "enfants"}}},
			{Section: 5102, Title: "Autres", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "autres"}}},
		},
		keys: map[string][]core.RatingKey{"enfants": {show.Key}, "autres": {unknownShow.Key}},
	})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	viewer := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{5100, 5101}, MaxContentRating: "PG"}
	titles := []core.Title{unknownEpisode, adult, episode, show, unknownShow}
	get := func(_ context.Context, key core.RatingKey) (*core.Title, error) {
		for _, title := range titles {
			if title.Key == key {
				return &title, nil
			}
		}
		return nil, core.ErrNotFound
	}
	libs := []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}}
	vis := BindVisibility(nil, get, nil, nil, libs)
	var gotTypes []core.MediaType
	service := &Service{d: ServiceDeps{
		Protocol: "jellyfin", Libraries: libs, Visibility: vis,
		Active: func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Get:    func(ctx context.Context, _ *core.User, key core.RatingKey) (*core.Title, error) { return get(ctx, key) },
		Search: Search{Visibility: vis, Catalog: func(_ context.Context, _ string, types []core.MediaType, _ int) ([]core.Title, error) {
			gotTypes = slices.Clone(types)
			return titles, nil
		}},
	}}
	query := frontend.ItemQuery{Types: []core.MediaType{core.TypeEpisode}, Limit: 10}
	page, err := service.Search(t.Context(), viewer, core.Device{}, "animation", query)
	if err != nil || !slices.Equal(gotTypes, []core.MediaType{core.TypeEpisode}) || len(page.Page.Items) != 1 || page.Page.Items[0].Key != episode.Key {
		t.Fatalf("search types: %v, results: %+v, error: %v", gotTypes, page.Page, err)
	}
	for _, test := range []struct {
		key     core.RatingKey
		allowed bool
	}{{episode.Key, true}, {adult.Key, false}, {unknownEpisode.Key, false}} {
		_, err := service.playable(t.Context(), viewer, test.key)
		if test.allowed && err != nil || !test.allowed && !errors.Is(err, core.ErrNotFound) {
			t.Errorf("playback permission for %v, want %v, got %v", test.key, test.allowed, err)
		}
	}
}
