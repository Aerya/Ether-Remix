package mediabrowser

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pse/latestfilter"
)

const pseLatestTestRules = `{"movie":{"exclude":[{"field":"genre","op":"in","value":["animation","Documentaire"]},` +
	`{"field":"country","op":"in","value":["IN"]},{"field":"language","op":"in","value":["hi"]}]},` +
	`"show":{"exclude":[{"field":"genre","op":"in","value":["news"]}]}}`

func pseLatestRow(key core.RatingKey, typ core.MediaType, genres ...string) frontend.LatestItem {
	var it frontend.LatestItem
	it.Title.Key, it.Title.Type, it.Title.Genres = key, typ, genres
	return it
}

func TestPSEFilterLatestDropsExcluded(t *testing.T) {
	t.Setenv(latestfilter.Key, pseLatestTestRules)
	m := core.TypeMovie
	rows := []frontend.LatestItem{pseLatestRow(1, m, "Animation", "Comedy"), pseLatestRow(2, m, "Drama"),
		pseLatestRow(3, m, "documentaire"), pseLatestRow(4, m), pseLatestRow(5, m, "Crime"), pseLatestRow(6, m, "Documentaire")}
	got := pseFilterLatest("1", rows, 2)
	if len(got) != 2 || got[0].Title.Key != 2 || got[1].Title.Key != 4 {
		t.Fatalf("got %+v", got)
	}
	if n := len(pseFilterLatest("7", []frontend.LatestItem{pseLatestRow(1, m, "Animation")}, 10)); n != 1 {
		t.Fatalf("autre bibliothèque filtrée : %d", n)
	}
	if pseLatestFetch("1", 20) != 200 || pseLatestFetch("7", 20) != 20 {
		t.Fatal("taille demandée")
	}
	// Les séries n'utilisent que leurs propres règles.
	s := core.TypeShow
	got = pseFilterLatest("2", []frontend.LatestItem{pseLatestRow(1, s, "Animation"), pseLatestRow(2, s, "News")}, 10)
	if len(got) != 1 || got[0].Title.Key != 1 {
		t.Fatalf("séries : %+v", got)
	}
}

func TestPSEFilterLatestCountriesAndLanguages(t *testing.T) {
	t.Setenv(latestfilter.Key, pseLatestTestRules)
	var a, b, c frontend.LatestItem
	a.Title.Key, a.Title.Type, a.Title.Countries = 1, core.TypeMovie, []string{"India"}
	b.Title.Key, b.Title.Type, b.Title.Language = 2, core.TypeMovie, "hi"
	c.Title.Key, c.Title.Type, c.Title.Countries, c.Title.Language = 3, core.TypeMovie, []string{"US"}, "en"
	got := pseFilterLatest("1", []frontend.LatestItem{a, b, c}, 10)
	if len(got) != 1 || got[0].Title.Key != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestPSEFilterLatestOffByDefault(t *testing.T) {
	t.Setenv(latestfilter.Key, "")
	rows := []frontend.LatestItem{{}}
	if len(pseFilterLatest("1", rows, 10)) != 1 || pseLatestFetch("1", 20) != 20 {
		t.Fatal("doit rester inactif")
	}
	t.Setenv(latestfilter.Key, "{invalide")
	if len(pseFilterLatest("1", rows, 10)) != 1 || pseLatestFetch("1", 20) != 20 {
		t.Fatal("valeur invalide : ne doit rien exclure")
	}
}
