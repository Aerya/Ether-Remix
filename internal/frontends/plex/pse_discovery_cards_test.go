package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"strings"
	"testing"
)

type navigationCollections struct{ *fakeCollections }

func (c navigationCollections) ListComplete(ctx context.Context, u *core.User, typ core.MediaType) ([]core.Collection, error) {
	return c.List(ctx, u, typ)
}
func (c navigationCollections) ListNavigation(ctx context.Context, u *core.User, typ core.MediaType) ([]core.Collection, error) {
	return c.List(ctx, u, typ)
}

type navigationExperience struct {
	frontend.Experience
	cards []core.Collection
}

func (e navigationExperience) Collections(_ context.Context, u *core.User, _ core.Device, q frontend.CollectionQuery) (frontend.CollectionPage, error) {
	if u == nil || !q.NavigationOnly {
		return frontend.CollectionPage{}, core.ErrNotFound
	}
	return frontend.CollectionPage{Items: e.cards, Total: len(e.cards)}, nil
}
func TestNavigationHomePublishesClickableCollectionsAndRestores(t *testing.T) {
	f := newFixture()
	f.srv.Close()
	key := core.SmartKey(23)
	f.cols.cols[key] = core.Collection{Key: key, Title: "A24 · Films", NavigationGroup: "studios", Type: core.TypeMovie, ChildCount: 1}
	f.cols.items[key] = []core.Title{alien}
	d := f.srv.currentTestDeps()
	d.Collections = navigationCollections{f.cols}
	d.Experience = navigationExperience{Experience: d.experienceService(), cards: []core.Collection{f.cols.cols[key]}}
	enabled := true
	setDepsConfig(&d, func() config.Config { return config.Config{DiscoveryCards: enabled} })
	f.srv = newTestServer(d)
	defer f.srv.Close()
	rec := f.get(t, "/hubs", jsonAccept...)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "remix.navigation.studios") || !strings.Contains(rec.Body.String(), "/library/collections/"+key.String()+"/children") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = f.get(t, "/library/collections/"+key.String()+"/children", jsonAccept...)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Alien") {
		t.Fatal("card target", rec.Code, rec.Body.String())
	}
	enabled = false
	rec = f.get(t, "/hubs", jsonAccept...)
	if strings.Contains(rec.Body.String(), "remix.navigation.studios") {
		t.Fatal("previous home not restored")
	}
}

func TestNavigationHomeCountryGroups(t *testing.T) {
	f := newFixture()
	f.srv.Close()
	d := f.srv.currentTestDeps()
	cards := []core.Collection{}
	for n, group := range []string{"services-FR", "services-US", "genres"} {
		cards = append(cards, core.Collection{Key: core.SmartKey(int64(n + 101)), Title: group, NavigationGroup: group, Type: core.TypeMovie, ChildCount: 1})
	}
	d.Experience = navigationExperience{Experience: d.experienceService(), cards: cards}
	setDepsConfig(&d, func() config.Config { return config.Config{DiscoveryCards: true} })
	f.srv = newTestServer(d)
	defer f.srv.Close()
	rec := f.get(t, "/hubs", jsonAccept...)
	body := rec.Body.String()
	for _, id := range []string{"remix.navigation.services-FR", "remix.navigation.services-US", "remix.navigation.genres"} {
		if !strings.Contains(body, id) {
			t.Fatalf("missing country hub %s: %s", id, body)
		}
	}
	if strings.Index(body, "remix.navigation.genres") < strings.Index(body, "remix.navigation.services-US") {
		t.Fatal("genres precede services")
	}
}
