package mediabrowser

import (
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/pse/listonly"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func TestPSEListItemsMarksOnlyLists(t *testing.T) {
	view, _ := ids.GUID(ids.View, 1)
	show := ids.RatingKeyGUID(core.ShowKey(100))
	season := ids.RatingKeyGUID(core.SeasonKey(100, 1))
	for _, c := range []struct {
		name   string
		parent []string
		query  string
		want   bool
	}{
		{"sans ParentId", nil, "", true},
		{"vue", []string{view}, "", true},
		{"série récursive", []string{show}, "?Recursive=true", true},
		{"série non récursive", []string{show}, "", false},
		{"saison", []string{season}, "?Recursive=true", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := pseListItems(httptest.NewRequest("GET", "/Items"+c.query, nil), c.parent)
			if got := frontend.PSEListOnly(r.Context()); got != c.want {
				t.Fatalf("marqueur = %v, want %v", got, c.want)
			}
		})
	}
	t.Run("réglage off", func(t *testing.T) {
		defer pseenv.Replace(map[string]string{listonly.Key: "off"})()
		if r := pseListItems(httptest.NewRequest("GET", "/Items", nil), nil); frontend.PSEListOnly(r.Context()) {
			t.Fatal("marqueur posé avec le réglage off")
		}
	})
}
