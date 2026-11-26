package plex

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// A page row's Look reaches the Plex hubs: Hero rows go out as style=hero
// (a shelf while no title has hero art), Wide is a landscape shelf, watched
// titles leave a Hide watched row, and plugin surface rows are skipped.
func TestOwnerRowStyleAndHideWatchedReachPlexHubs(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.setRecommender(presentLayout{
		fakeRecommender: &fakeRecommender{},
		order:           []string{"row.lead", "surface:sports:sports.tonight", "row.hero", "row.cold", "row.wide"},
		present: map[string]core.RowPresentation{
			"row.lead":                      {RowID: "rlead0001", HideWatched: true},
			"surface:sports:sports.tonight": {RowID: "rsurf0001", Surface: &core.RowSurface{Plugin: "sports", Surface: "sports.tonight"}},
			"row.hero":                      {RowID: "rhero0001", Layout: core.RowStyleHero},
			"row.cold":                      {RowID: "rcold0001", Layout: core.RowStyleHero},
			"row.wide":                      {RowID: "rwide0001", Layout: core.RowStyleWide},
		},
	})

	art := func(key string) model.Metadata {
		return model.Metadata{RatingKey: key, Type: "movie", Title: key, Thumb: "/t/" + key, Art: "/a/" + key}
	}
	watched := art("1")
	watched.ViewCount = 1
	cold := model.Metadata{RatingKey: "5", Type: "movie", Title: "5", Thumb: "/t/5"}
	rows := []model.Hub{
		{HubIdentifier: "row.wide", Style: "shelf", Size: 1, Metadata: []model.Metadata{art("6")}},
		{HubIdentifier: "row.cold", Style: "shelf", Size: 1, Metadata: []model.Metadata{cold}},
		{HubIdentifier: "row.hero", Style: "shelf", Size: 1, Metadata: []model.Metadata{art("3")}},
		{HubIdentifier: "row.lead", Style: "shelf", Size: 2, Metadata: []model.Metadata{watched, art("2")}},
	}
	got := heroReadyHomeHubs(stableHomeHubs(nil, f.srv.finalizeHomeRows(context.Background(), &Request{User: &ownerUser}, core.HubContext{Now: fixedNow}, "home", rows)))
	byID := map[string]model.Hub{}
	order := []string{}
	for _, h := range got {
		byID[h.HubIdentifier] = h
		order = append(order, h.HubIdentifier)
	}
	if len(order) != 4 || order[0] != "row.lead" || order[1] != "row.hero" {
		t.Fatalf("order = %v", order)
	}
	if lead := byID["row.lead"]; lead.Style != "hero" || len(lead.Metadata) != 1 || lead.Metadata[0].RatingKey != "2" || lead.Size != 1 {
		t.Fatalf("Hide watched lead = %+v", lead)
	}
	if h := byID["row.hero"]; h.Style != "hero" {
		t.Fatalf("owner Hero row style = %q", h.Style)
	}
	if h := byID["row.cold"]; h.Style != "shelf" || len(h.Metadata) != 1 {
		t.Fatalf("artless owner Hero row should fall back to a shelf: %+v", h)
	}
	if h := byID["row.wide"]; h.Style != "shelf" || h.Metadata[0].Thumb != "/a/6" {
		t.Fatalf("Wide row should be a landscape shelf: %+v", h)
	}
}
