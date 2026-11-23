package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// Keep the replay scope fixture small while using real credential checks.
type bootstrapBudgetIdentity struct {
	frontendtest.Identity
	grants *identity.Grants
	token  string
}

func (f bootstrapBudgetIdentity) GrantScope(ctx context.Context, token string, device core.Device) (string, error) {
	_, err := f.grants.Resolve(ctx, "vela", token)
	return "budget-" + device.ID, err
}
func (f bootstrapBudgetIdentity) GrantScopeCurrent(ctx context.Context, scope string, device core.Device) (*core.User, error) {
	return f.grants.Resolve(ctx, "vela", f.token)
}

func TestVelaBootstrapBoundedQueries(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := t.Context()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 99, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	viewer, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := *a.artwork
	source.Metadata = nil
	source.Variants = func(*core.Title, string) (*tmdb.Images, bool) { return &tmdb.Images{}, true }
	source.Images = artworkservice.NewRemoteCache(1024, &http.Client{Transport: disabledArtworkTransport{}})
	a.catalog.FreezeCacheMinute()
	for _, count := range []int{40, 240, 400, 1600} {
		for n := 1; n <= count; n++ {
			title := core.Title{Key: core.MovieKey(int64(n)), Type: core.TypeMovie, IDs: core.IDs{TMDB: int64(n)}, Title: fmt.Sprintf("Film %03d", n), Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Runtime: 90 * time.Minute, Genres: []string{"Drama", "Comedy"}, Rating: 8, VoteCount: 1000, Popularity: 50, PosterPath: fmt.Sprintf("/poster%d.jpg", n), BackdropPath: fmt.Sprintf("/backdrop%d.jpg", n)}
			if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
				t.Fatal(err)
			}
		}
		name := fmt.Sprintf("titles=%d", count)
		t.Run(name, func(t *testing.T) {
			raw, err := a.newSurfaceExperience("vela", source)
			if err != nil {
				t.Fatal(err)
			}
			exp := raw.(*surfaceExperience)
			// Hold ranking fixed for the 20x20 admission gate: persistence, current
			// policy, catalogue hydration, receipts, artwork and HTTP remain real.
			if count >= 400 {
				libs := a.configuredLibraries()
				vis := experienceVisibility(a.safety, a.catalog.CachedGet, nil, nil, libs)
				exp.rowViews, err = experience.NewRuntimeRowViews(experience.RuntimeRowViews{DB: a.db, Protocol: "vela", Receipts: exp.receipts, Active: a.users.PolicyUser, CachedGet: a.catalog.CachedGet, CachedGetMany: a.catalog.CachedGetMany, Visibility: vis, Libraries: libs, Situation: situationFor, Snapshot: func(ctx context.Context, req frontend.RowRequest, _ core.HomeSituation) ([]frontend.Row, error) {
					keys := make([]core.RatingKey, count)
					for n := range keys {
						keys[n] = core.MovieKey(int64(n + 1))
					}
					titles, e := a.catalog.CachedGetMany(ctx, keys)
					if e != nil {
						return nil, e
					}
					rows := make([]frontend.Row, count/20)
					for n := range rows {
						row := frontend.Row{ID: fmt.Sprintf("fixture-%d", n), Kind: "trending", Title: "Trending", Purpose: frontend.RowRecommendation, Rank: n}
						for j := 0; j < 20; j++ {
							title := titles[keys[n*20+j]]
							row.Entries = append(row.Entries, frontend.RowEntry{Kind: frontend.RowEntryTitle, Title: &title})
						}
						rows[n] = row
					}
					return rows, nil
				}})
				if err != nil {
					t.Fatal(err)
				}
			}
			grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
			if err != nil {
				t.Fatal(err)
			}
			device := core.Device{ID: name}
			credential, err := grants.Issue(ctx, viewer.ID, nil, "vela", device)
			if err != nil {
				t.Fatal(err)
			}
			images, err := identity.NewArtworkImageGrantService(a.db, "vela", grants, func() frontend.Experience { return exp }, source, time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			inst, err := (vela.Frontend{DB: a.db, ReplayKey: make([]byte, 32)}).Mount(ctx, frontend.Host{Experience: exp, Identity: bootstrapBudgetIdentity{grants: grants, token: credential, Identity: frontendtest.Identity{ResolveDeviceFunc: func(ctx context.Context, token string, _ core.Device) (*core.User, time.Time, error) {
				v, e := grants.Resolve(ctx, "vela", token)
				return v, time.Time{}, e
			}}}, ImageGrants: images, Sessions: frontendtest.Sessions{}})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(context.Background())
			// Keep each large fixture at the same cold activity state.
			if count >= 400 {
				if _, err := a.db.ExecContext(ctx, `DELETE FROM identity_viewer_times WHERE viewer_id=?`, viewer.ID); err != nil {
					t.Fatal(err)
				}
			}
			measured, stats := store.WithQueryStats(ctx)
			req := httptest.NewRequest("GET", "/vela/v0/bootstrap?limit=100", nil).WithContext(measured)
			req.Header.Set("Authorization", "Bearer "+credential)
			req.Header.Set("X-Vela-Device-Id", device.ID)
			w := httptest.NewRecorder()
			inst.Handler().ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("bootstrap: %d %s", w.Code, w.Body.String())
			}
			var body struct {
				Rows   []struct{ Items []json.RawMessage }
				Heroes []json.RawMessage
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			cards := len(body.Heroes)
			for _, row := range body.Rows {
				cards += len(row.Items)
			}
			if cards < 6 {
				t.Fatalf("fixture returned only %d cards: %s", cards, w.Body.String())
			}
			if count >= 400 && (len(body.Rows) != count/20 || cards != count) {
				t.Fatalf("bootstrap shape: rows=%d cards=%d", len(body.Rows), cards)
			}
			if stats.Queries() > 200 {
				t.Fatalf("unbounded bootstrap at %d titles: queries=%d", count, stats.Queries())
			}
		})
	}
}
