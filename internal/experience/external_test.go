package experience

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestResolveExternalVisibilityAndCanonicalEpisode(t *testing.T) {
	ctx := context.Background()
	user := &core.User{ID: 1}
	show := core.Title{Key: core.ShowKey(4), Type: core.TypeShow}
	ep := core.Title{Key: core.EpisodeKey(4, 1, 2), Type: core.TypeEpisode, ShowKey: show.Key, Season: 1, Episode: 2}
	movie := core.Title{Key: core.MovieKey(5), Type: core.TypeMovie}
	titles := map[core.RatingKey]*core.Title{show.Key: &show, ep.Key: &ep, movie.Key: &movie}
	calls := 0
	result := &show
	s := &Service{d: ServiceDeps{Active: func(_ context.Context, id int64) (*core.User, error) {
		if id != 1 {
			return nil, core.ErrUnauthorized
		}
		return user, nil
	}, ByExternalID: func(_ context.Context, source, id string) (*core.Title, error) {
		calls++
		if source != "imdb" || id != "tt123" {
			t.Fatal("lookup arguments", source, id)
		}
		return result, nil
	}, Get: func(_ context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
		if t := titles[k]; t != nil {
			return t, nil
		}
		return nil, core.ErrNotFound
	}, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }, GetTitle: func(_ context.Context, k core.RatingKey) (*core.Title, error) {
		if t := titles[k]; t != nil {
			return t, nil
		}
		return nil, core.ErrNotFound
	}}}}
	season, episode := 1, 2
	req := frontend.ExternalRequest{Source: "imdb", ID: "tt123", Type: core.TypeShow, Season: &season, Episode: &episode}
	key, err := s.ResolveExternal(ctx, user, core.Device{}, req)
	if err != nil || key != ep.Key || calls != 1 {
		t.Fatal(key, err, calls)
	}
	show.Adult = true
	if _, err = s.ResolveExternal(ctx, user, core.Device{}, req); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("show restriction", err)
	}
	show.Adult = false
	ep.Adult = true
	if _, err = s.ResolveExternal(ctx, user, core.Device{}, req); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("episode restriction", err)
	}
	ep.Adult = false
	result = &movie
	if _, err = s.ResolveExternal(ctx, user, core.Device{}, req); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("movie suffix", err)
	}
	req.Season = nil
	req.Episode = nil
	req.Type = core.TypeMovie
	if key, err = s.ResolveExternal(ctx, user, core.Device{}, req); err != nil || key != movie.Key {
		t.Fatal("movie", key, err)
	}
	result = &ep
	req.Type = core.TypeEpisode
	if key, err = s.ResolveExternal(ctx, user, core.Device{}, req); err != nil || key != ep.Key {
		t.Fatal("episode imdb", key, err)
	}
	calls = 0
	if _, err = s.ResolveExternal(ctx, &core.User{ID: 2}, core.Device{}, req); !errors.Is(err, core.ErrNotFound) || calls != 0 {
		t.Fatal("inactive lookup", err, calls)
	}
}

func TestResolveExternalRejectsAbusiveInputAndPropagatesLookupError(t *testing.T) {
	ctx := context.Background()
	calls := 0
	upstream := errors.New("upstream unavailable")
	s := &Service{d: ServiceDeps{Active: func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil }, ByExternalID: func(context.Context, string, string) (*core.Title, error) { calls++; return nil, upstream }}}
	bad := []frontend.ExternalRequest{{Source: "imdb", ID: "tt"}, {Source: "imdb", ID: "tt12x"}, {Source: "imdb", ID: "tt" + strings.Repeat("1", 17)}, {Source: "tmdb", ID: "tt123"}, {Source: "imdb", ID: "tt 123"}}
	season, episode := 1000, 1
	bad = append(bad, frontend.ExternalRequest{Source: "imdb", ID: "tt123", Season: &season, Episode: &episode}, frontend.ExternalRequest{Source: "imdb", ID: "tt123", Season: &season})
	for _, req := range bad {
		if _, err := s.ResolveExternal(ctx, &core.User{ID: 1}, core.Device{}, req); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("invalid %+v %v", req, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid requests reached catalog", calls)
	}
	if _, err := s.ResolveExternal(ctx, &core.User{ID: 1}, core.Device{}, frontend.ExternalRequest{Source: "imdb", ID: "tt123"}); !errors.Is(err, upstream) {
		t.Fatal(err)
	}
}

func TestResolveExternalRejectsUnrepresentableShowAndUnavailableEpisode(t *testing.T) {
	ctx := context.Background()
	u := &core.User{ID: 1}
	show := core.Title{Key: core.ShowKey(100000000), Type: core.TypeShow}
	season, episode := 1, 1
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return u, nil }, ByExternalID: func(context.Context, string, string) (*core.Title, error) { return &show, nil }}}
	req := frontend.ExternalRequest{Source: "imdb", ID: "tt123", Season: &season, Episode: &episode}
	if _, err := s.ResolveExternal(ctx, u, core.Device{}, req); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("unrepresentable show", err)
	}
	show.Key = core.ShowKey(4)
	ep := core.Title{Key: core.EpisodeKey(4, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key, Season: 1, Episode: 1}
	s.d.Get = func(context.Context, *core.User, core.RatingKey) (*core.Title, error) { return &ep, nil }
	s.d.Visibility = Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }, GetTitle: func(context.Context, core.RatingKey) (*core.Title, error) { return &show, nil }, FilterAvailable: func(context.Context, []core.Title) []core.Title { return nil }}
	if _, err := s.ResolveExternal(ctx, u, core.Device{}, req); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("unavailable", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	s.d.ByExternalID = func(ctx context.Context, _, _ string) (*core.Title, error) { return nil, ctx.Err() }
	if _, err := s.ResolveExternal(canceled, u, core.Device{}, req); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
}

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
