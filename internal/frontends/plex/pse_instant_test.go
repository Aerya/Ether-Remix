package plex

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// Ajout Remix : écrans immédiats. Un visionnage sert l'écran en cache tout de
// suite puis le reconstruit en arrière-plan ; un spectateur restreint et un
// changement de droits restent reconstruits sur la requête.
func TestPSEInstantHomeServesThenRefreshes(t *testing.T) {
	var mu sync.Mutex
	revs := map[int64]experience.HomeRevision{}
	setRev := func(id int64, fn func(*experience.HomeRevision)) {
		mu.Lock()
		r := revs[id]
		fn(&r)
		revs[id] = r
		mu.Unlock()
	}
	restricted := core.User{ID: 3, PlexID: 3003, Username: "kid", Title: "Kid", MaxContentRating: "NC-17"}
	f := newFixture(func(d *testServerDeps) {
		d.Recommender = &buildCountingRecommender{fakeRecommender: d.Recommender.(*fakeRecommender)}
		setDepsAuth(d, &fakeAuth{users: map[string]core.User{ownerToken: ownerUser, "kid-token": restricted}})
		d.HomeCache = &testHomeCache{Revision: func(_ context.Context, u *core.User) (experience.HomeRevision, error) {
			mu.Lock()
			defer mu.Unlock()
			return revs[u.ID], nil
		}}
	})
	defer f.srv.Close()
	home := func(token string) string {
		t.Helper()
		r := f.get(t, "/hubs/promoted?count=12", "Accept", "application/json", "X-Plex-Token", token, "X-Plex-Client-Identifier", "tv", "X-Plex-Product", "Plex for Apple TV", "X-Plex-Platform", "tvOS")
		if r.Code != 200 || !strings.Contains(r.Body.String(), "Trending build ") {
			t.Fatalf("status %d: %s", r.Code, r.Body.String())
		}
		return r.Body.String()
	}
	first := home(ownerToken)
	setRev(ownerUser.ID, func(r *experience.HomeRevision) { r.Viewer = "watched-more" })
	if home(ownerToken) != first {
		t.Fatal("un visionnage a fait attendre une reconstruction")
	}
	deadline := time.Now().Add(5 * time.Second)
	for home(ownerToken) == first {
		if time.Now().After(deadline) {
			t.Fatal("l'écran n'a pas été reconstruit en arrière-plan")
		}
		time.Sleep(20 * time.Millisecond)
	}

	kid := home("kid-token")
	setRev(restricted.ID, func(r *experience.HomeRevision) { r.Viewer = "watched-more" })
	if home("kid-token") == kid {
		t.Fatal("un spectateur restreint a reçu un écran périmé")
	}
}
