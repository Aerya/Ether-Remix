package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/identity"
)

// A warm visit and taste model must both advance before a deferred playback
// notification has had a chance to request a new model.
func TestVelaHomeAdvancesWarmContinuationAfterTimelineCompletion(t *testing.T) {
	a := setupTestAppOwner(t)
	ctx := t.Context()
	viewer, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	show := core.ShowKey(100)
	release := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	titles := []core.Title{{Key: show, Type: core.TypeShow, IDs: core.IDs{TMDB: 100}, Title: "Series", Released: release}}
	for n := 1; n <= 3; n++ {
		titles = append(titles, core.Title{Key: core.EpisodeKey(100, 1, n), ShowKey: show, Type: core.TypeEpisode,
			Title: "Episode", Season: 1, Episode: n, Released: release, Runtime: 45 * time.Minute})
	}
	for _, title := range titles {
		if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	page := core.RowPage{Key: core.PageHome, Mode: core.PageCurated, Rows: []core.Row{
		{Source: core.RowSource{Kind: "continue-watching"}, Placement: core.RowPlacement{Mode: core.PlacementShow}},
		{Source: core.RowSource{Kind: "up-next"}, Placement: core.RowPlacement{Mode: core.PlacementShow}},
	}}
	if _, err := a.personalize.SavePage(ctx, page); err != nil {
		t.Fatal(err)
	}
	if err := a.state.ScrobbleMany(ctx, viewer.ID, []core.RatingKey{titles[1].Key}); err != nil {
		t.Fatal(err)
	}
	if err := a.personalize.Rebuild(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	exp, err := a.newSurfaceExperience("vela")
	if err != nil {
		t.Fatal(err)
	}

	grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	device := core.Device{ID: "warm-continuation"}
	credential, err := grants.Issue(ctx, viewer.ID, nil, "vela", device)
	if err != nil {
		t.Fatal(err)
	}
	identity := bootstrapBudgetIdentity{grants: grants, token: credential, Identity: frontendtest.Identity{
		ResolveDeviceFunc: func(ctx context.Context, token string, _ core.Device) (*core.User, time.Time, error) {
			user, err := grants.Resolve(ctx, "vela", token)
			return user, time.Time{}, err
		},
	}}
	inst, err := (vela.Frontend{DB: a.db, ReplayKey: make([]byte, 32)}).Mount(ctx, frontend.Host{
		Experience: exp, Identity: identity, Sessions: frontendtest.Sessions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })

	read := func(want core.RatingKey) {
		t.Helper()
		for _, endpoint := range []string{"rows", "bootstrap"} {
			req := httptest.NewRequest(http.MethodGet, "/vela/v0/"+endpoint, nil).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+credential)
			req.Header.Set("X-Vela-Device-Id", device.ID)
			w := httptest.NewRecorder()
			inst.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s returned %d", endpoint, w.Code)
			}
			type row struct {
				Purpose core.RowPurpose
				Items   []struct{ ID string }
			}
			var body struct{ Items, Rows []row }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			got := map[core.RowPurpose][]string{}
			for _, row := range append(body.Items, body.Rows...) {
				for _, item := range row.Items {
					got[row.Purpose] = append(got[row.Purpose], item.ID)
				}
			}
			for _, purpose := range []core.RowPurpose{frontend.RowContinue, frontend.RowNextUp} {
				if items := got[purpose]; len(items) != 1 || items[0] != ids.RatingKeyGUID(want) {
					t.Errorf("%s %s = %v, want episode %s", endpoint, purpose, items, want)
				}
			}
		}
	}
	read(titles[2].Key)
	notify, err := a.state.AcceptProgress(ctx, viewer.ID, titles[2].Key, 45*time.Minute, 45*time.Minute, "stopped")
	if err != nil {
		t.Fatal(err)
	}
	defer notify()
	read(titles[3].Key)
}
