package themes

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func (f *fakeCols) UpdateSpec(_ context.Context, id string, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	for i := range f.specs {
		if f.specs[i].ID == id {
			sp.ID = id
			f.specs[i] = sp
			return &sp, nil
		}
	}
	return nil, core.ErrNotFound
}

func TestVostfrSelection(t *testing.T) {
	ctx := context.Background()
	s, cols := newTest(t)
	if err := s.SetVostfr(ctx, cols, "movie:1", "movie", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVostfr(ctx, cols, "show:2", "show", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVostfr(ctx, cols, "movie:3", "movie", true); err != nil {
		t.Fatal(err)
	}
	_ = s.SetVostfr(ctx, cols, "movie:3", "movie", true) // déjà présent
	got, _ := s.Vostfr(ctx)
	if len(got) != 3 || got[0] != "movie:1" || got[1] != "movie:3" || got[2] != "show:2" {
		t.Fatalf("sélection : %v", got)
	}
	if len(cols.created) != 2 {
		t.Fatalf("deux collections attendues (films, séries) : %d", len(cols.created))
	}
	if ex := s.Exceptions(); len(ex) != 2 {
		t.Fatalf("exceptions : %v", ex)
	}
	var lib *Library
	for _, l := range s.Libraries() {
		if l.Name == VostfrName {
			l := l
			lib = &l
		}
	}
	if lib == nil || lib.Type != TypeMixed || len(lib.Collections) != 2 || !lib.AllLanguages {
		t.Fatalf("bibliothèque VOSTFR : %+v", lib)
	}
	if err := s.SetVostfr(ctx, cols, "movie:1", "", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Vostfr(ctx); len(got) != 2 || got[0] != "movie:3" {
		t.Fatalf("après retrait : %v", got)
	}
	if err := s.SetVostfr(ctx, cols, "x", "episode", true); err == nil {
		t.Fatal("type invalide accepté")
	}
}

func TestVostfrAPIQueuesAddedTitleAndRetainsSelectionWhenIndexerIsOff(t *testing.T) {
	for _, off := range []bool{false, true} {
		t.Run(fmt.Sprint(off), func(t *testing.T) {
			s, cols := newTest(t)
			calls := 0
			key := core.MovieKey(42)
			s.SetPriorityHooks(PriorityHooks{Search: func(_ context.Context, keys []core.RatingKey) (int, error) {
				calls++
				if len(keys) != 1 || keys[0] != key {
					t.Fatal(keys)
				}
				if off {
					return 0, errors.New("private upstream failure")
				}
				return 1, nil
			}})
			h := s.Handler(cols, nil)
			body := fmt.Sprintf(`{"key":"%d","type":"movie","add":true}`, key)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("PUT", "/vostfr", strings.NewReader(body)))
			if w.Code != 200 || calls != 1 {
				t.Fatal(w.Code, w.Body.String(), calls)
			}
			if off && (!strings.Contains(w.Body.String(), "searchWarning") || strings.Contains(w.Body.String(), "private upstream")) {
				t.Fatal(w.Body.String())
			}
			if !off && !strings.Contains(w.Body.String(), `"queued":1`) {
				t.Fatal(w.Body.String())
			}
			keys, _ := s.Vostfr(t.Context())
			if len(keys) != 1 {
				t.Fatal("selection lost after queue failure")
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("PUT", "/vostfr", strings.NewReader(fmt.Sprintf(`{"key":"%d","type":"movie","add":false}`, key))))
			if w.Code != 200 || calls != 1 {
				t.Fatal("removal queued a search")
			}
		})
	}
}
