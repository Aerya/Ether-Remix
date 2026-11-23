package experience

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
)

// Exercise the real HTTP -> experience -> catalogue/index path, including
// final visibility checks. Browsing-only rules must never swallow title hits;
// mandatory content policy must still apply in a local-response search.
func TestVelaLocalCatalogueSearch(t *testing.T) {
	ctx := t.Context()
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []core.Title{
		{Key: core.MovieKey(949), Type: core.TypeMovie, Title: "Heat", Year: 1995, Released: time.Date(1995, 12, 15, 0, 0, 0, 0, time.UTC)},
		{Key: core.MovieKey(42023), Type: core.TypeMovie, Title: "Heat", Year: 1986, Released: time.Date(1986, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Key: core.MovieKey(999), Type: core.TypeMovie, Title: "Heat", Adult: true, Released: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if err := cat.UpsertDetailedTitle(ctx, title); err != nil {
			t.Fatal(err)
		}
	}
	// A normal read builds the tiny title index before the no-wait HTTP read.
	if _, err := cat.Search(ctx, "heat", []core.MediaType{core.TypeMovie}, 50); err != nil {
		t.Fatal(err)
	}
	policy, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	policy.SetCatalogPolicy(cat.VisibleForViewer, cat.DiscoveryVisibleForViewer)
	viewer := &core.User{ID: 231}
	vis := Visibility{Policy: policy, GetTitle: cat.CachedGet, SectionFor: func(core.MediaType) (int, bool) { return 1, true }}
	service, err := NewService(ServiceDeps{
		Active:    func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries: []Library{{ID: 1, Type: core.TypeMovie}}, Visibility: vis,
		Search: SearchFor(SearchSources{Catalog: cat.Search, Details: cat.SearchDetails}, vis, func(*core.User) bool { return true }),
		Get: func(ctx context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
			return cat.CachedGet(ctx, k)
		},
		Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { return nil, nil },
		State:    serviceState{}, Rows: &RowsService{}, Protocol: "vela",
		NoProviderWait: func(ctx context.Context) context.Context {
			return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := (vela.Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(ctx, frontend.Host{Experience: service, Sessions: frontendtest.Sessions{}, Identity: searchIdentity{Identity: frontendtest.Identity{
		ResolveDeviceFunc: func(context.Context, string, core.Device) (*core.User, time.Time, error) {
			return viewer, time.Time{}, nil
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())
	for _, scope := range []string{"", "discovery", "everywhere"} {
		rules := []core.LibraryExclusion(nil)
		if scope != "" {
			rules = []core.LibraryExclusion{{Scope: scope, MediaType: "movie", Match: "all", Rules: []core.CollectionRule{{Field: "year", Op: "gte", Value: json.RawMessage(`1900`)}}}}
		}
		if err := cat.SetLibraryPolicies(catalog.LibraryPolicies{MovieRules: rules}); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/vela/v0/search", strings.NewReader(`{"query":"heat","mode":"fast","generation":2,"idempotencyKey":"heat-`+scope+`"}`))
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", scope, w.Code, w.Body.String())
		}
		var page struct {
			Items []struct {
				Name string
				Year int
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		want := 2
		if scope == "everywhere" {
			want = 0
		}
		if len(page.Items) != want {
			t.Fatalf("%s: %s", scope, w.Body.String())
		}
	}
}

type searchIdentity struct{ frontendtest.Identity }

func (searchIdentity) GrantScope(context.Context, string, core.Device) (string, error) {
	return "family", nil
}

func (s searchIdentity) GrantScopeCurrent(ctx context.Context, _ string, d core.Device) (*core.User, error) {
	v, _, err := s.ResolveDevice(ctx, "token", d)
	return v, err
}
