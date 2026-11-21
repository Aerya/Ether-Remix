package experience

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Un spectateur [5100, 5101] sans les sections de base ouvre les titres de son
// thème (film, épisode via ShowKey) et rien d'autre ; l'ancien réglage, le
// propriétaire et la liste vide sont inchangés.
func TestResolveExternalThemeGrant(t *testing.T) {
	ctx := context.Background()
	show := core.Title{Key: core.ShowKey(4), Type: core.TypeShow}
	ep := core.Title{Key: core.EpisodeKey(4, 1, 2), Type: core.TypeEpisode, ShowKey: show.Key, Season: 1, Episode: 2}
	movie := core.Title{Key: core.MovieKey(5), Type: core.TypeMovie}
	other := core.Title{Key: core.MovieKey(6), Type: core.TypeMovie}
	titles := map[core.RatingKey]*core.Title{show.Key: &show, ep.Key: &ep, movie.Key: &movie, other.Key: &other}
	granted := map[core.RatingKey]bool{show.Key: true, movie.Key: true}
	var result *core.Title
	var user *core.User
	s := &Service{d: ServiceDeps{
		Active:       func(context.Context, int64) (*core.User, error) { return user, nil },
		ByExternalID: func(context.Context, string, string) (*core.Title, error) { return result, nil },
		Get: func(_ context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
			if t := titles[k]; t != nil {
				return t, nil
			}
			return nil, core.ErrNotFound
		},
		Visibility: Visibility{
			SectionFor: func(typ core.MediaType) (int, bool) {
				if typ == core.TypeMovie {
					return 1, true
				}
				return 2, true
			},
			GetTitle: func(_ context.Context, k core.RatingKey) (*core.Title, error) {
				if t := titles[k]; t != nil {
					return t, nil
				}
				return nil, core.ErrNotFound
			},
			ThemeGrant: func(u *core.User, t *core.Title) bool {
				return frontend.ThemeExplicit(u) && (granted[t.Key] || (t.ShowKey != 0 && granted[t.ShowKey]))
			},
		},
	}}
	season, episode := 1, 2
	resolve := func(u *core.User, r *core.Title, withEpisode bool) error {
		user, result = u, r
		req := frontend.ExternalRequest{Source: "imdb", ID: "tt123", Type: r.Type}
		if withEpisode {
			req.Season, req.Episode = &season, &episode
		}
		_, err := s.ResolveExternal(ctx, u, core.Device{}, req)
		return err
	}
	kid := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{5100, 5101}}
	if err := resolve(kid, &movie, false); err != nil {
		t.Fatal("film du thème refusé", err)
	}
	if err := resolve(kid, &show, true); err != nil {
		t.Fatal("épisode du thème refusé", err)
	}
	if err := resolve(kid, &other, false); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("film hors thème accessible", err)
	}
	if _, err := s.playable(ctx, kid, ep.Key); err != nil {
		t.Fatal("lecture de l'épisode du thème refusée", err)
	}
	if _, err := s.playable(ctx, kid, other.Key); err == nil {
		t.Fatal("lecture hors thème accessible")
	}
	legacy := &core.User{ID: 2, AllowedLibraries: []core.LibraryID{2}}
	if err := resolve(legacy, &movie, false); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("ancien réglage : film sans la section Films", err)
	}
	if err := resolve(&core.User{ID: 3, IsOwner: true}, &other, false); err != nil {
		t.Fatal("propriétaire", err)
	}
	if err := resolve(&core.User{ID: 4}, &other, false); err != nil {
		t.Fatal("liste vide", err)
	}
}
