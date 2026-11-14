package plex

import (
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Les tests d'Ether vérifient son ordre alphabétique d'origine : Remix
// trie par date de sortie par défaut (PSE_LIBRARY_SORT), sauf ici.
func TestMain(m *testing.M) {
	restore := pseenv.Replace(map[string]string{"PSE_LIBRARY_SORT": "title"})
	code := m.Run()
	restore()
	os.Exit(code)
}

func TestPSELibrarySortDefaultsToNewestRelease(t *testing.T) {
	restore := pseenv.Replace(map[string]string{})
	defer restore()
	s := &Server{}
	for _, typ := range []frontend.MediaType{frontend.TypeMovie, frontend.TypeShow} {
		mt := s.metaType(Section{ID: 1}, typ, "")
		var def, active string
		for _, so := range mt.Sort {
			if so.Default != "" {
				def = so.Key + ":" + so.Default
			}
			if so.Active != nil && *so.Active {
				active = so.Key + ":" + so.ActiveDirection
			}
		}
		if def != "originallyAvailableAt:desc" || active != "originallyAvailableAt:desc" {
			t.Fatalf("%s : défaut %q, actif %q", typ, def, active)
		}
		var br browseRequest
		br.applySortParam("", typ)
		if br.q.Sort != frontend.SortReleased || !br.q.Desc {
			t.Fatalf("%s : tri sans paramètre = %v desc=%v", typ, br.q.Sort, br.q.Desc)
		}
		// Un tri choisi dans l'application reste prioritaire.
		br = browseRequest{}
		br.applySortParam("titleSort", typ)
		if br.q.Sort != frontend.SortTitle || br.q.Desc {
			t.Fatalf("%s : tri demandé ignoré : %v desc=%v", typ, br.q.Sort, br.q.Desc)
		}
	}
	// Épisodes : ordre d'Ether inchangé.
	var br browseRequest
	br.applySortParam("", frontend.TypeEpisode)
	if br.q.Sort != "" {
		t.Fatalf("épisodes : %v", br.q.Sort)
	}
}

func TestPSELibrarySortTitleOption(t *testing.T) {
	restore := pseenv.Replace(map[string]string{"PSE_LIBRARY_SORT": "title"})
	defer restore()
	if pseDefaultSortIndex(frontend.TypeMovie) != 0 {
		t.Fatal("le réglage « title » doit garder le tri alphabétique")
	}
	var br browseRequest
	br.applySortParam("", frontend.TypeMovie)
	if br.q.Sort != frontend.SortTitle || br.q.Desc {
		t.Fatalf("tri = %v desc=%v", br.q.Sort, br.q.Desc)
	}
}
