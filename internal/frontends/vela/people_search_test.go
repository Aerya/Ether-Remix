package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestVelaFastSearchReturnsPersonTitlesInLeadOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []core.Title{
		{
			Key: core.MovieKey(101), IDs: core.IDs{TMDB: 101}, Type: core.TypeMovie,
			Title: "Dune", Year: 2021, Released: now.AddDate(-3, 0, 0), Popularity: 120, VoteCount: 15000,
			Cast: []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Baron Harkonnen"}},
		},
		{
			Key: core.MovieKey(102), IDs: core.IDs{TMDB: 102}, Type: core.TypeMovie,
			Title: "Talk Show Spot", Year: 2022, Released: now.AddDate(-2, 0, 0), Popularity: 250, VoteCount: 25000,
			Cast: []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Self - Guest"}},
		},
	} {
		if err := cat.UpsertTitle(ctx, item, true); err != nil {
			t.Fatal(err)
		}
	}

	srch := experience.SearchFor(frontend.SearchSources{
		Catalog:      cat.Search,
		PeopleTitles: cat.PeopleTitles,
		Details:      cat.SearchDetails,
	}, experience.Visibility{}, func(*core.User) bool { return true })

	exp := frontendtest.Experience{
		SearchFunc: func(ctx context.Context, _ *frontend.Viewer, _ frontend.Device, term string, q frontend.ItemQuery) (frontend.SearchPage, error) {
			items, err := srch.Keyword(ctx, &core.User{ID: 1, IsOwner: true}, term, q.Types, q.Limit)
			if err != nil {
				return frontend.SearchPage{}, err
			}
			return frontend.SearchPage{Page: frontend.Page{Items: items, Total: len(items)}, Continuation: frontend.ContinuationExhausted}, nil
		},
	}

	inst := &instance{ctx: ctx, host: frontend.Host{Identity: identityFake{}, Experience: exp}}
	req := httptest.NewRequest(http.MethodPost, "/vela/v0/search", nil)
	req = req.WithContext(context.WithValue(req.Context(), bodyKey{}, json.RawMessage(`{"query":"Stellan Skarsgard","mode":"fast","generation":1}`)))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Vela-Device-Id", "vela-web")
	w := httptest.NewRecorder()
	inst.search(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Items []struct {
			Title string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0].Title != "Dune" || got.Items[1].Title != "Talk Show Spot" {
		t.Fatalf("Vela fast search items = %+v, want [Dune, Talk Show Spot]", got.Items)
	}
}
