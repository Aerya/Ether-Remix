package plex

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

type liveSectionsExperience struct {
	frontend.Experience
	views []frontend.View
	err   error
}

func (e *liveSectionsExperience) PublishedViews(context.Context) ([]frontend.View, error) {
	return e.views, e.err
}

func TestLiveSectionsKeepIdentityAndHiddenStateOnRefreshError(t *testing.T) {
	exp := &liveSectionsExperience{Experience: frontend.NoopExperience(), views: []frontend.View{
		{ID: "3", LibraryID: 3, Name: "Picks", Native: plexBaseMovie},
	}}
	srv := newTestServer(testServerDeps{Experience: exp})
	defer srv.Close()
	before := srv.LibrarySections()
	if len(before) != 1 || before[0].ID != 3 {
		t.Fatalf("unexpected sections: %+v", before)
	}
	exp.views[0].Name = "Renamed"
	after := srv.LibrarySections()
	if len(after) != 1 || after[0].Title != "Renamed" || after[0].UUID != before[0].UUID {
		t.Fatalf("identity changed: %+v", after)
	}
	exp.err = errors.New("store unavailable")
	failed := srv.LibrarySections()
	if len(failed) != 1 || failed[0] != after[0] {
		t.Fatalf("failed refresh changed sections: %+v", failed)
	}
	exp.err = nil
	exp.views = []frontend.View{}
	if got := srv.LibrarySections(); len(got) != 0 {
		t.Fatalf("hidden sections resurrected: %+v", got)
	}
	exp.err = errors.New("store unavailable")
	if got := srv.LibrarySections(); len(got) != 0 {
		t.Fatalf("failed refresh resurrected hidden sections: %+v", got)
	}
}
