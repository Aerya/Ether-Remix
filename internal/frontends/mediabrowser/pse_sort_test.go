package mediabrowser

import (
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func TestPSELibrarySort(t *testing.T) {
	defer pseenv.Replace(map[string]string{})()
	q := frontend.ItemQuery{Types: []frontend.MediaType{"movie"}}
	pseLibrarySort(httptest.NewRequest("GET", "/Items?ParentId=x", nil), &q)
	if q.Sort != frontend.SortReleased || !q.Desc {
		t.Fatalf("sans SortBy : %v desc=%v", q.Sort, q.Desc)
	}
	// Le tri demandé par l'application reste prioritaire.
	q = frontend.ItemQuery{Sort: frontend.SortTitle}
	pseLibrarySort(httptest.NewRequest("GET", "/Items?SortBy=SortName", nil), &q)
	if q.Sort != frontend.SortTitle || q.Desc {
		t.Fatalf("SortBy ignoré : %v desc=%v", q.Sort, q.Desc)
	}
	// Une liste d'épisodes garde l'ordre d'Ether.
	q = frontend.ItemQuery{Types: []frontend.MediaType{"episode"}}
	pseLibrarySort(httptest.NewRequest("GET", "/Items", nil), &q)
	if q.Sort != "" {
		t.Fatalf("épisodes : %v", q.Sort)
	}

	view, _ := ids.GUID(ids.View, 1)
	prefs := map[string]any{"SortBy": "SortName", "SortOrder": "Ascending"}
	pseDisplaySort(view, prefs)
	if prefs["SortBy"] != "PremiereDate" || prefs["SortOrder"] != "Descending" {
		t.Fatalf("préférences par défaut : %v", prefs)
	}
	other := map[string]any{"SortBy": "SortName"}
	pseDisplaySort("usersettings", other)
	if other["SortBy"] != "SortName" {
		t.Fatalf("préférences générales modifiées : %v", other)
	}

	defer pseenv.Replace(map[string]string{"PSE_LIBRARY_SORT": "title"})()
	q = frontend.ItemQuery{Types: []frontend.MediaType{"movie"}}
	pseLibrarySort(httptest.NewRequest("GET", "/Items", nil), &q)
	if q.Sort != "" {
		t.Fatalf("réglage « title » : %v", q.Sort)
	}
}
