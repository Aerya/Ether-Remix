package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/conform/openapi/spectest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type batchPersonGrantsFake struct {
	imageGrantsFake
	batchCalls int
}

func (b *batchPersonGrantsFake) IssuePersonImages(_ context.Context, _ string, _ frontend.Device, targets []frontend.PersonImageTarget) ([]string, error) {
	b.batchCalls++
	out := make([]string, len(targets))
	for i, t := range targets {
		if t.Image.Path != "" {
			out[i] = "batch-person" + t.Image.Path
		}
	}
	return out, nil
}

func (b *batchPersonGrantsFake) Serve(w http.ResponseWriter, _ *http.Request, secret string) {
	if strings.HasPrefix(secret, "batch-person/") || strings.HasPrefix(secret, "scoped-image/") {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("JPEG"))
		return
	}
	http.Error(w, "Not Found", http.StatusNotFound)
}

func TestJellyfinAndEmbyPeopleSearchTitlesHintsAndHeadshots(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	personID, _ := ids.GUID(ids.User, 1640)
	movieKey := core.MovieKey(101)
	portrait := frontend.Image{
		Kind: frontend.ImagePerson, Subject: "tmdb:1640", Path: "/stellan.jpg",
		Width: 400, Height: 600, BlurHash: "LGF5]+Yk^6",
	}
	personRec := frontend.PersonDetail{
		ID: personID, Name: "Stellan Skarsgård", TMDB: 1640, IMDB: "nm0001745",
		ArtworkItem: movieKey, Images: []frontend.Image{portrait},
		Biography: "Swedish actor.", Birthday: "1951-06-13",
	}

	exp := peopleExperience{
		Experience: frontendtest.Experience{
			SearchFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, term string, q frontend.ItemQuery) (frontend.SearchPage, error) {
				if strings.Contains(strings.ToLower(term), "stellan") {
					m := core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Dune", Year: 2021, SearchScore: 0.950}
					return frontend.SearchPage{Page: frontend.Page{Items: []core.Title{m}, Total: 1}}, nil
				}
				return frontend.SearchPage{}, nil
			},
		},
		people: func(q frontend.PeopleQuery) []frontend.PersonDetail {
			if strings.Contains(strings.ToLower(q.Query), "stellan") {
				return []frontend.PersonDetail{personRec}
			}
			return nil
		},
	}
	// Extend person lookup on peopleExperience via wrapper so /Items/{personId} resolves too.
	fullExp := personDetailExperience{peopleExperience: exp, detail: personRec}

	for _, proto := range []string{"jellyfin", "emby"} {
		t.Run(proto, func(t *testing.T) {
			grants := &batchPersonGrantsFake{}
			ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
			host := frontend.Host{Identity: ident, Experience: fullExp, ImageGrants: grants}
			var (
				inst frontend.Instance
				err  error
			)
			if proto == "jellyfin" {
				inst, err = (Jellyfin{MachineID: "test"}).Mount(context.Background(), host)
			} else {
				inst, err = (Emby{MachineID: "test"}).Mount(context.Background(), host)
			}
			if err != nil {
				t.Fatal(err)
			}

			doGet := func(path string) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.Header.Set("X-Emby-Token", "grant")
				w := httptest.NewRecorder()
				inst.Handler().ServeHTTP(w, r)
				return w
			}

			// 1. /Persons?searchTerm=... returns Person with batched ImageTags.Primary + ImageBlurHashes and serves 200 OK headshot.
			w := doGet("/Persons?searchTerm=Stellan+Skarsgard&limit=24")
			if w.Code != http.StatusOK {
				t.Fatalf("/Persons status = %d: %s", w.Code, w.Body.String())
			}
			if grants.batchCalls != 1 {
				t.Fatalf("expected 1 batched person image grant call, got %d", grants.batchCalls)
			}
			var persons struct {
				Items []struct {
					Id        string
					Name      string
					Type      string
					ImageTags map[string]string
				}
				TotalRecordCount int
			}
			if err := json.Unmarshal(w.Body.Bytes(), &persons); err != nil {
				t.Fatal(err)
			}
			if persons.TotalRecordCount != 1 || len(persons.Items) != 1 || persons.Items[0].Id != personID || persons.Items[0].Type != "Person" {
				t.Fatalf("unexpected /Persons body: %s", w.Body.String())
			}
			tag := persons.Items[0].ImageTags["Primary"]
			if tag != "batch-person/stellan.jpg" {
				t.Fatalf("Primary image tag = %q, want batch-person/stellan.jpg (body=%s)", tag, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "PrimaryImageAspectRatio") {
				t.Fatalf("unexpected PrimaryImageAspectRatio on Person: %s", w.Body.String())
			}
			if proto == "jellyfin" && !strings.Contains(w.Body.String(), "LGF5]+Yk^6") {
				t.Fatalf("expected ImageBlurHashes on Jellyfin Person: %s", w.Body.String())
			}
			imgRec := doGet("/Items/" + personID + "/Images/Primary?tag=" + tag)
			if imgRec.Code != http.StatusOK {
				t.Fatalf("headshot /Items/%s/Images/Primary?tag=%s returned %d", personID, tag, imgRec.Code)
			}

			// 2. /Items?Recursive=true&SearchTerm=...&IncludeItemTypes=Person returns Person DTO with headshot.
			w = doGet("/Items?Recursive=true&SearchTerm=Stellan+Skarsgard&IncludeItemTypes=Person")
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Type":"Person"`) || !strings.Contains(w.Body.String(), `"batch-person/stellan.jpg"`) {
				t.Fatalf("/Items IncludeItemTypes=Person failed: %d %s", w.Code, w.Body.String())
			}

			// 3. /Items?Recursive=true&SearchTerm=...&IncludeItemTypes=Movie,Series returns person's titles.
			w = doGet("/Items?Recursive=true&SearchTerm=Stellan+Skarsgard&IncludeItemTypes=Movie,Series")
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Name":"Dune"`) {
				t.Fatalf("/Items title search by person failed: %d %s", w.Code, w.Body.String())
			}

			// 4. /Search/Hints unrestricted returns both Person hint (with PrimaryImageTag) and Movie hint, valid against Jellyfin OpenAPI.
			hintsPath := "/Search/Hints?searchTerm=Stellan+Skarsgard&Limit=10"
			w = doGet(hintsPath)
			if w.Code != http.StatusOK {
				t.Fatalf("/Search/Hints status = %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"Type":"Person"`) || !strings.Contains(w.Body.String(), `"PrimaryImageTag":"batch-person/stellan.jpg"`) || !strings.Contains(w.Body.String(), `"Name":"Dune"`) {
				t.Fatalf("/Search/Hints missing person or title hint: %s", w.Body.String())
			}
			if proto == "jellyfin" {
				if doc := spectest.Document(t, "jellyfin"); doc != nil {
					if err := doc.Validate("GET", hintsPath, w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
						t.Fatalf("Jellyfin OpenAPI validation failed on /Search/Hints: %v\nbody: %s", err, w.Body.String())
					}
				}
			}

			// 5. /Items/{personId} returns Person detail DTO with Overview, PremiereDate, ProviderIds, and ImageTags.Primary.
			w = doGet("/Items/" + personID)
			if w.Code != http.StatusOK {
				t.Fatalf("/Items/{personId} status = %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"Type":"Person"`) || !strings.Contains(w.Body.String(), `"Overview":"Swedish actor."`) || !strings.Contains(w.Body.String(), `"Tmdb":"1640"`) || !strings.Contains(w.Body.String(), `"Imdb":"nm0001745"`) || !strings.Contains(w.Body.String(), `"batch-person/stellan.jpg"`) {
				t.Fatalf("unexpected /Items/{personId} body: %s", w.Body.String())
			}
		})
	}
}

type personDetailExperience struct {
	peopleExperience
	detail frontend.PersonDetail
}

func (p personDetailExperience) Person(_ context.Context, _ *frontend.Viewer, _ frontend.Device, id string, _ frontend.PersonReadOptions) (frontend.PersonDetail, error) {
	if id == p.detail.ID {
		return p.detail, nil
	}
	return frontend.PersonDetail{}, frontend.ErrNotFound
}
