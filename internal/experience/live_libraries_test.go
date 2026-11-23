package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestViewsFollowLiveNamesAndSectionPolicy(t *testing.T) {
	movies, shows := "Movies", "TV Shows"
	s := &Service{d: ServiceDeps{
		Active: func(_ context.Context, id int64) (*core.User, error) {
			if id == 2 {
				return &core.User{ID: id, AllowedLibraries: []core.LibraryID{1}}, nil
			}
			return &core.User{ID: id, IsOwner: true}, nil
		},
		LiveLibraries: func() []Library {
			return []Library{{ID: 1, Name: movies, Type: core.TypeMovie}, {ID: 2, Name: shows, Type: core.TypeShow}}
		},
	}}
	owner, err := s.Views(context.Background(), &core.User{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(owner) != 2 || owner[0].ID != "1" || owner[0].Name != "Movies" || owner[1].ID != "2" || owner[1].Name != "TV Shows" || owner[0].Virtual || owner[1].Virtual {
		t.Fatalf("owner views = %+v", owner)
	}
	movies, shows = "Basement Films", "Basement Series"
	owner, err = s.Views(context.Background(), &core.User{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(owner) != 2 || owner[0].ID != "1" || owner[0].Name != "Basement Films" || owner[1].ID != "2" || owner[1].Name != "Basement Series" {
		t.Fatalf("renamed owner views = %+v", owner)
	}
	restricted, err := s.Views(context.Background(), &core.User{ID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(restricted) != 1 || restricted[0].ID != "1" || restricted[0].Name != "Basement Films" {
		t.Fatalf("restricted views = %+v", restricted)
	}
}

func TestViewsCopyVirtualOriginAndSectionPolicy(t *testing.T) {
	s := &Service{d: ServiceDeps{
		Active: func(_ context.Context, id int64) (*core.User, error) {
			if id == 2 {
				return &core.User{ID: id, AllowedLibraries: []core.LibraryID{1}}, nil
			}
			return &core.User{ID: id, IsOwner: true}, nil
		},
		Libraries: []Library{{ID: 1, Name: "Movies", Type: core.TypeMovie, Virtual: true}, {ID: 2, Name: "TV Shows", Type: core.TypeShow, Virtual: true}},
	}}
	owner, err := s.Views(context.Background(), &core.User{ID: 1})
	if err != nil || len(owner) != 2 || !owner[0].Virtual || !owner[1].Virtual || owner[0].ID != "1" || owner[1].ID != "2" {
		t.Fatalf("owner views = %+v %v", owner, err)
	}
	restricted, err := s.Views(context.Background(), &core.User{ID: 2})
	if err != nil || len(restricted) != 1 || restricted[0].ID != "1" || !restricted[0].Virtual {
		t.Fatalf("restricted views = %+v %v", restricted, err)
	}
}

func TestDeletedLibrariesFailClosedAndRemainEmpty(t *testing.T) {
	ctx := context.Background()
	store := &fakePluginLibraries{}
	s := &Service{d: ServiceDeps{
		PluginLibraries: store,
		Libraries:       []Library{{ID: 1, Type: core.TypeMovie}},
		LiveLibraries:   func() []Library { return nil },
		Active:          func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id, IsOwner: true}, nil },
	}}
	if len(s.libraries()) != 0 {
		t.Fatal("empty live list fell back to startup defaults")
	}
	for _, viewer := range []*core.User{{ID: 1, IsOwner: true}, {ID: 2}, {ID: 3, AllowedLibraries: []core.LibraryID{1}}} {
		views, err := s.Views(ctx, viewer)
		if err != nil || len(views) != 0 {
			t.Fatalf("phantom views: %+v %v", views, err)
		}
		if s.sectionOrFilterAllowed(ctx, viewer, &core.Title{Key: core.MovieKey(1), Type: core.TypeMovie}, func(core.MediaType) (int, bool) { return 1, true }) {
			t.Fatal("missing default library granted access")
		}
		if _, err := s.LibraryItems(ctx, viewer, core.Device{}, "1", frontend.ItemQuery{}); err == nil {
			t.Fatal("deleted library route remained available")
		}
	}
}
