package silo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

type siloPeopleExp struct {
	frontendtest.Experience
	people func(frontend.PeopleQuery) ([]frontend.PersonDetail, error)
}

func (s siloPeopleExp) People(_ context.Context, _ *frontend.Viewer, _ frontend.Device, q frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
	return s.people(q)
}
func (siloPeopleExp) Person(context.Context, *frontend.Viewer, frontend.Device, string, frontend.PersonReadOptions) (frontend.PersonDetail, error) {
	return frontend.PersonDetail{}, frontend.ErrNotFound
}
func (siloPeopleExp) PersonCredit(context.Context, *frontend.Viewer, frontend.Device, frontend.RatingKey, int64) (frontend.PersonDetail, error) {
	return frontend.PersonDetail{}, frontend.ErrNotFound
}

type siloBatchGrants struct{}

func (siloBatchGrants) Issue(context.Context, frontend.ImageGrantRequest) (string, error) {
	return "single-grant", nil
}
func (siloBatchGrants) Revoke(context.Context, string, string) error { return nil }
func (siloBatchGrants) IssuePersonImages(_ context.Context, _ string, _ frontend.Device, targets []frontend.PersonImageTarget) ([]string, error) {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = "silo-headshot" + t.Image.Path
	}
	return out, nil
}
func (siloBatchGrants) Serve(w http.ResponseWriter, _ *http.Request, _ string) {
	w.WriteHeader(http.StatusOK)
}

func TestSiloPeopleSearchReturnsTitlesAndPersonHeadshot(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	movieKey := core.MovieKey(101)
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: movieKey, IDs: core.IDs{TMDB: 101}, Type: core.TypeMovie,
		Title: "Dune", Year: 2021, Released: now.AddDate(-3, 0, 0), Popularity: 120, VoteCount: 15000,
		Cast: []core.Person{{TMDB: 1640, Name: "Stellan Skarsgård", Role: "Baron Harkonnen", ThumbPath: "/stellan.jpg"}},
	}, true); err != nil {
		t.Fatal(err)
	}

	srch := experience.SearchFor(frontend.SearchSources{
		Catalog:      cat.Search,
		PeopleTitles: cat.PeopleTitles,
		Details:      cat.SearchDetails,
	}, experience.Visibility{}, func(*core.User) bool { return true })

	viewer := &frontend.Viewer{ID: 1}
	exp := siloPeopleExp{
		Experience: frontendtest.Experience{
			SearchFunc: func(ctx context.Context, _ *frontend.Viewer, _ frontend.Device, term string, q frontend.ItemQuery) (frontend.SearchPage, error) {
				items, err := srch.Keyword(ctx, &core.User{ID: 1, IsOwner: true}, term, q.Types, q.Limit)
				if err != nil {
					return frontend.SearchPage{}, err
				}
				return frontend.SearchPage{Page: frontend.Page{Items: items, Total: len(items)}, TotalExact: true}, nil
			},
		},
		people: func(q frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
			if strings.Contains(strings.ToLower(q.Query), "stellan") {
				return []frontend.PersonDetail{{
					ID: "person-1640", Name: "Stellan Skarsgård", TMDB: 1640,
					ArtworkItem: movieKey,
					Images:      []frontend.Image{{Kind: frontend.ImagePerson, Subject: "tmdb:1640", Path: "/stellan.jpg"}},
				}}, nil
			}
			return nil, nil
		},
	}

	inst, err := (Frontend{MachineID: "test"}).Mount(ctx, frontend.Host{
		Identity:    frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
		Experience:  exp,
		ImageGrants: siloBatchGrants{},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. /api/v2/catalog?q=Stellan+Skarsgard returns Stellan Skarsgård's titles.
	r := httptest.NewRequest(http.MethodGet, "/api/v2/catalog?q=Stellan+Skarsgard&limit=20", nil)
	r.Header.Set("Authorization", "Bearer grant")
	r.Header.Set("X-Silo-Device-Id", "tv")
	r.Header.Set("X-Profile-Id", "1")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"title":"Dune"`) {
		t.Fatalf("Silo catalog search failed: %d %s", w.Code, w.Body.String())
	}

	// 2. /api/v2/people?q=Stellan+Skarsgard returns person with signed image_url headshot.
	r = httptest.NewRequest(http.MethodGet, "/api/v2/catalog/people?q=Stellan+Skarsgard&limit=20", nil)
	r.Header.Set("Authorization", "Bearer grant")
	r.Header.Set("X-Silo-Device-Id", "tv")
	r.Header.Set("X-Profile-Id", "1")
	w = httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Silo people search failed: %d %s", w.Code, w.Body.String())
	}
	var pResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pResp); err != nil || len(pResp.Items) != 1 {
		t.Fatalf("unexpected Silo people response: %s", w.Body.String())
	}
	imgURL, _ := pResp.Items[0]["photo_url"].(string)
	if !strings.Contains(imgURL, "silo-headshot/stellan.jpg") {
		t.Fatalf("Silo person missing headshot image_url: %#v", pResp.Items[0])
	}
}
