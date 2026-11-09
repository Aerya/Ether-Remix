package themes

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPriorityLevels(t *testing.T) {
	ctx := context.Background()
	cols := &fakeCols{
		specs: []core.CollectionSpec{{ID: "a"}, {ID: "b"}, {ID: "privee", OwnerUserID: 7}},
		keys:  map[string][]core.RatingKey{},
	}
	s, err := New(ctx, store.OpenTest(t), cols)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPriority(ctx, PriorityCollection, "absente", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("collection absente : %v", err)
	}
	if err := s.SetPriority(ctx, PriorityCollection, "privee", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("collection privée : %v", err)
	}
	if err := s.SetPriority(ctx, PriorityCollection, "a", 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("niveau hors limites : %v", err)
	}
	if err := s.SetPriority(ctx, PriorityLibrary, "12", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bibliothèque absente : %v", err)
	}
	if err := s.SetPriority(ctx, PriorityCollection, "a", 1); err != nil {
		t.Fatal(err)
	}
	cols.keys["a"] = []core.RatingKey{core.MovieKey(1), core.ShowKey(1)}
	cols.keys["b"] = []core.RatingKey{core.MovieKey(1)}
	if err := s.SetPriority(ctx, PriorityCollection, "b", 2); err != nil {
		t.Fatal(err)
	}
	ps, total, err := s.Priorities(ctx)
	if err != nil || len(ps) != 2 || ps[0].ID != "b" || total != 2 {
		t.Fatalf("priorités = %+v, %d, %v", ps, total, err)
	}
	// Le niveau le plus haut l'emporte, et chaque type garde ses titres.
	movies := s.PriorityLevels(ctx, core.TypeMovie)
	if len(movies) != 1 || movies[core.MovieKey(1)] != 2 {
		t.Fatalf("films = %v", movies)
	}
	if shows := s.PriorityLevels(ctx, core.TypeShow); len(shows) != 1 || shows[core.ShowKey(1)] != 1 {
		t.Fatalf("séries = %v", shows)
	}
	if err := s.SetPriority(ctx, PriorityCollection, "b", 0); err != nil {
		t.Fatal(err)
	}
	if ps, _, _ = s.Priorities(ctx); len(ps) != 1 || ps[0].ID != "a" {
		t.Fatalf("niveau 0 doit effacer : %+v", ps)
	}
}

func TestPriorityAPI(t *testing.T) {
	ctx := context.Background()
	cols := &fakeCols{specs: []core.CollectionSpec{{ID: "a"}}, keys: map[string][]core.RatingKey{"a": {core.MovieKey(1)}}}
	s, err := New(ctx, store.OpenTest(t), cols)
	if err != nil {
		t.Fatal(err)
	}
	var searched []core.RatingKey
	changed := 0
	s.SetPriorityHooks(PriorityHooks{
		Max:     2000,
		Changed: func() { changed++ },
		Search: func(_ context.Context, keys []core.RatingKey) (int, error) {
			searched = keys
			return len(keys), nil
		},
	})
	h := s.Handler(cols, nil)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := call("PUT", "/priority", `{"kind":"collection","id":"a","level":2}`); w.Code != 200 || changed != 1 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("PUT : %d %s (changed=%d)", w.Code, w.Body, changed)
	}
	if w := call("PUT", "/priority", `{"kind":"collection","id":"z","level":2}`); w.Code != 400 {
		t.Fatalf("cible inconnue : %d", w.Code)
	}
	if w := call("POST", "/priority/search", `{"kind":"collection","id":"a"}`); w.Code != 200 || len(searched) != 1 || !strings.Contains(w.Body.String(), `"queued":1`) {
		t.Fatalf("recherche : %d %s", w.Code, w.Body)
	}
}
