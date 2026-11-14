package themes

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSyncSeerrRoutesFallbackToVostfrLibrary(t *testing.T) {
	ctx := context.Background()
	s, cols := newTest(t)
	vfMovie, vostMovie, vostShow := core.MovieKey(1), core.MovieKey(2), core.ShowKey(3)
	if err := s.SyncSeerr(ctx, cols, []core.RatingKey{vfMovie}, []core.RatingKey{vostMovie, vostShow}); err != nil {
		t.Fatal(err)
	}
	byName := map[string]core.CollectionSpec{}
	for _, sp := range cols.specs {
		byName[sp.Name+"/"+sp.MediaType] = sp
	}
	all, ok := byName[SeerrName+"/movie"]
	if !ok || len(all.Sources[0].Keys) != 2 {
		t.Fatalf("SeerrCatalog films : %+v", all)
	}
	vost, ok := byName[SeerrVostfrName+"/movie"]
	if !ok || len(vost.Sources[0].Keys) != 1 || vost.Sources[0].Keys[0] != vostMovie.String() {
		t.Fatalf("SeerrCatalog · VOSTFR films : %+v", vost)
	}
	if _, ok := byName[SeerrVostfrName+"/show"]; !ok {
		t.Fatal("SeerrCatalog · VOSTFR séries absente")
	}
	var lib *Library
	for _, l := range s.Libraries() {
		if l.Name == VostfrName {
			l := l
			lib = &l
		}
	}
	if lib == nil || !lib.AllLanguages {
		t.Fatalf("bibliothèque VOSTFR : %+v", lib)
	}
	for _, c := range lib.Collections {
		if c == all.ID {
			t.Fatal("la collection SeerrCatalog (VF comprise) ne doit pas être dans la bibliothèque VOSTFR")
		}
	}
	if !s.LangFreeCollection(vost.ID) || !s.LangFreeCollection(all.ID) {
		t.Fatal("les collections SeerrCatalog doivent garder leurs titres sans VF")
	}
	if s.LangFreeCollection("1") {
		t.Fatal("une collection ordinaire ne doit pas échapper au filtre VF")
	}

	// Le titre est ensuite trouvé en VF : il quitte la collection VOSTFR,
	// qui est désactivée une fois vide.
	created := len(cols.created)
	if err := s.SyncSeerr(ctx, cols, []core.RatingKey{vfMovie, vostMovie, vostShow}, nil); err != nil {
		t.Fatal(err)
	}
	if len(cols.created) != created {
		t.Fatalf("collections recréées : %d → %d", created, len(cols.created))
	}
	for _, sp := range cols.specs {
		if sp.Name == SeerrVostfrName && sp.Enabled {
			t.Fatalf("collection VOSTFR vide encore active : %+v", sp)
		}
	}
}
