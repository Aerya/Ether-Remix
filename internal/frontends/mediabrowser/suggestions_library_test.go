package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func TestLibraryScopedSuggestionsAndRecommendations(t *testing.T) {
	lib3GUID, err := ids.GUID(ids.View, 3)
	if err != nil {
		t.Fatal(err)
	}
	collectionsGUID, err := ids.GUID(ids.View, collectionsViewNumber)
	if err != nil {
		t.Fatal(err)
	}

	for _, protocol := range []string{"jellyfin", "emby"} {
		t.Run(protocol, func(t *testing.T) {
			viewer := &frontend.Viewer{ID: 1, Username: "owner"}
			var seenRequests []frontend.RowRequest

			exp := frontendtest.Experience{
				RowsFunc: func(_ context.Context, req frontend.RowRequest) ([]frontend.Row, error) {
					seenRequests = append(seenRequests, req)
					if req.Placement == frontend.PlacementLibrary && req.ViewID == "3" {
						libMovie := frontend.Title{Key: 301, Type: "movie", Title: "Kids Movie Pick"}
						return []frontend.Row{
							{
								ID:      "lib3.picks",
								Title:   "Kids Favorites",
								Kind:    "favorites",
								Purpose: frontend.RowRecommendation,
								Handle:  "h-lib3",
								Entries: []frontend.RowEntry{{Kind: frontend.RowEntryTitle, Title: &libMovie}},
								Items:   []frontend.Title{libMovie},
							},
						}, nil
					}
					if req.Placement == frontend.PlacementHome {
						homeMovie := frontend.Title{Key: 101, Type: "movie", Title: "Global Home Movie"}
						return []frontend.Row{
							{
								ID:      "home.picks",
								Title:   "Because you watched Heat",
								Kind:    "because-you-watched",
								Purpose: frontend.RowRecommendation,
								Handle:  "h-home",
								Entries: []frontend.RowEntry{{Kind: frontend.RowEntryTitle, Title: &homeMovie}},
								Items:   []frontend.Title{homeMovie},
							},
						}, nil
					}
					return nil, frontend.ErrNotFound
				},
				UserDataFunc: func(_ context.Context, _ *frontend.Viewer, _ []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
					return map[frontend.RatingKey]frontend.WatchStatus{}, nil
				},
			}

			host := frontend.Host{
				Identity: frontendtest.Identity{
					ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil },
				},
				Experience: exp,
			}
			var inst frontend.Instance
			if protocol == "jellyfin" {
				inst, err = (Jellyfin{MachineID: "test"}).Mount(context.Background(), host)
			} else {
				inst, err = (Emby{MachineID: "test"}).Mount(context.Background(), host)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(context.Background())

			get := func(path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", path, nil)
				r.Header.Set("X-Emby-Token", "token")
				r.Header.Set("X-Emby-Device-Id", "tv-1")
				w := httptest.NewRecorder()
				inst.Handler().ServeHTTP(w, r)
				return w
			}

			// 1. /Items/Suggestions?parentId=<lib3GUID> uses PlacementLibrary + ViewID="3".
			w := get("/Items/Suggestions?parentId=" + lib3GUID + "&type=Movie")
			if w.Code != 200 {
				t.Fatalf("scoped /Items/Suggestions status=%d body=%s", w.Code, w.Body)
			}
			var sugResp struct {
				Items []struct{ Name string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &sugResp); err != nil {
				t.Fatal(err)
			}
			if len(sugResp.Items) != 1 || sugResp.Items[0].Name != "Kids Movie Pick" {
				t.Fatalf("scoped /Items/Suggestions items=%+v, want [Kids Movie Pick]", sugResp.Items)
			}

			// 2. /Movies/Recommendations?parentId=<lib3GUID> uses PlacementLibrary + ViewID="3".
			w = get("/Movies/Recommendations?parentId=" + lib3GUID)
			if w.Code != 200 {
				t.Fatalf("scoped /Movies/Recommendations status=%d body=%s", w.Code, w.Body)
			}
			var recCategories []struct {
				BaselineItemName string
				Items            []struct{ Name string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &recCategories); err != nil {
				t.Fatal(err)
			}
			if len(recCategories) != 1 || len(recCategories[0].Items) != 1 || recCategories[0].Items[0].Name != "Kids Movie Pick" {
				t.Fatalf("scoped /Movies/Recommendations = %+v, want Kids Movie Pick", recCategories)
			}

			// 3. Unscoped /Movies/Recommendations still uses PlacementHome.
			w = get("/Movies/Recommendations")
			if w.Code != 200 {
				t.Fatalf("unscoped /Movies/Recommendations status=%d body=%s", w.Code, w.Body)
			}
			if err := json.Unmarshal(w.Body.Bytes(), &recCategories); err != nil {
				t.Fatal(err)
			}
			if len(recCategories) != 1 || recCategories[0].Items[0].Name != "Global Home Movie" {
				t.Fatalf("unscoped /Movies/Recommendations = %+v, want Global Home Movie", recCategories)
			}

			// 4. Non-library virtual views (e.g. Collections view) return empty 200; duplicate parentId returns 400.
			w = get("/Items/Suggestions?parentId=" + collectionsGUID)
			if w.Code != 200 {
				t.Fatalf("collections parentId status=%d, want 200", w.Code)
			}
			w = get("/Items/Suggestions?parentId=" + lib3GUID + "&parentId=" + lib3GUID)
			if w.Code != 400 {
				t.Fatalf("duplicate parentId status=%d, want 400", w.Code)
			}

			foundLibPlacement := false
			for _, req := range seenRequests {
				if req.Placement == frontend.PlacementLibrary && req.ViewID == "3" {
					foundLibPlacement = true
					break
				}
			}
			if !foundLibPlacement {
				t.Fatalf("expected PlacementLibrary request for ViewID=3, saw %+v", seenRequests)
			}
		})
	}
}
