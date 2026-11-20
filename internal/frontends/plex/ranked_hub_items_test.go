package plex

import (
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// A refreshed /hubs/items page of a ranked Home row must keep numbered art
// and the wide coverArt display, or clients draw black wide tiles.
func TestRankedHubItemsFollowPaintedHome(t *testing.T) {
	s := &Server{}
	pr := &Request{User: &frontend.User{ID: 7}}
	s.noteRankedHubs(pr, []model.Hub{{HubIdentifier: "home.top10.today", RankedTop10: true}, {HubIdentifier: "home.trending"}})
	if !s.rankedHub(pr, "home.top10.today") || s.rankedHub(pr, "home.trending") {
		t.Fatal("painted ranking not remembered")
	}
	if s.rankedHub(&Request{User: &frontend.User{ID: 8}}, "home.top10.today") {
		t.Fatal("ranking leaked to another viewer")
	}
	var items []model.Metadata
	for i := 0; i < 12; i++ {
		k := strconv.Itoa(100 + i)
		items = append(items, model.Metadata{Type: "show", RatingKey: k, Thumb: "/library/metadata/" + k + "/thumb/1", Image: []model.Image{{Type: "coverPoster", URL: "/library/metadata/" + k + "/thumb/1"}}})
	}
	items = limitRankedItems(items)
	if len(items) != 10 {
		t.Fatalf("kept %d ranked items", len(items))
	}
	mc := &model.MediaContainer{Metadata: items[2:4]}
	paintRankedHubPage(mc, 2)
	if mc.Meta == nil || mc.Meta.DisplayImage[0].ImageType != "coverArt" || mc.Meta.DisplayImage[0].Type != "show" {
		t.Fatal(mc.Meta)
	}
	if !strings.Contains(mc.Metadata[0].Thumb, "ranked=3") {
		t.Fatal(mc.Metadata[0].Thumb)
	}
	wide := false
	for _, img := range mc.Metadata[0].Image {
		wide = wide || (img.Type == "coverArt" && strings.Contains(img.URL, "rankshape=wide"))
	}
	if !wide {
		t.Fatal("no wide image on the refreshed page")
	}
	s.noteRankedHubs(pr, []model.Hub{{HubIdentifier: "home.top10.today"}})
	if s.rankedHub(pr, "home.top10.today") {
		t.Fatal("an unranked repaint must clear the ranked state")
	}
}
