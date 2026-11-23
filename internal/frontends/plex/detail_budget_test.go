package plex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontends/mediabrowser"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

// A large series must never turn a detail-family GET into per-season or
// per-episode database work. Count the real catalogue and watch-state SQL.
func TestDetailFamilyQueryBudget(t *testing.T) {
	db := store.OpenTest(t)
	seed, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	show := breakingBad
	show.Released = fixedNow.Add(-48 * time.Hour)
	show.ChildCount, show.LeafCount = 30, 1200
	if err := seed.UpsertDetailedTitle(context.Background(), show); err != nil {
		t.Fatal(err)
	}
	for sn := 1; sn <= 30; sn++ {
		season := bbS1
		season.Released = show.Released
		season.Key, season.Season, season.ChildCount = core.SeasonKey(show.Key.TMDB(), sn), sn, 40
		if err := seed.UpsertDetailedTitle(context.Background(), season); err != nil {
			t.Fatal(err)
		}
		for en := 1; en <= 40; en++ {
			ep := bbE1
			ep.Released = show.Released
			ep.Key, ep.Season, ep.Episode = core.EpisodeKey(show.Key.TMDB(), sn, en), sn, en
			ep.SeasonTitle = fmt.Sprint("Season ", sn)
			if err := seed.UpsertDetailedTitle(context.Background(), ep); err != nil {
				t.Fatal(err)
			}
		}
	}
	watch := state.New(db, state.Options{})
	for _, sn := range []int{1, 15, 30} {
		if err := watch.Scrobble(context.Background(), ownerUser.ID, core.EpisodeKey(show.Key.TMDB(), sn, 1)); err != nil {
			t.Fatal(err)
		}
	}
	const budget = int64(20)
	detailBudgetMediaBrowser(t, db, state.New(db, state.Options{}), show.Key, bbE1.Key, budget)

	for _, route := range []string{
		"/library/metadata/" + bbE1.Key.String(),
		"/library/metadata/" + bbS1.Key.String() + "?includeOnDeck=1",
		"/library/metadata/" + show.Key.String() + "?includeChildren=1&includeOnDeck=1&includeRelated=1&includeExtras=1",
		"/library/metadata/" + show.Key.String() + "/children",
		"/library/metadata/" + bbS1.Key.String() + "/children",
		"/library/metadata/" + show.Key.String() + "/grandchildren",
		"/hubs/metadata/" + show.Key.String() + "/related",
		"/hubs/metadata/" + bbE1.Key.String() + "/postplay",
	} {
		t.Run(route, func(t *testing.T) {
			c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
			if err != nil {
				t.Fatal(err)
			}
			f := newFixture(func(d *testServerDeps) {
				d.Catalog = c
				d.State = state.New(db, state.Options{})
				d.DB = db
				d.DisableEnrichment = true
				d.Recommender = nil
				d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
					return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
				}}
			})
			defer f.srv.Close()
			ctx, stats := store.WithQueryStats(reqmemo.With(context.Background()))
			r := httptest.NewRequest("GET", route, nil).WithContext(ctx)
			r.Header.Set("X-Plex-Token", ownerToken)
			r.Header.Set("Accept", "application/json")
			w := httptest.NewRecorder()
			f.srv.ServeHTTP(w, r)
			t.Logf("status=%d queries=%d writes=%d rows=%d", w.Code, stats.Queries(), stats.Writes(), stats.Rows())
			if w.Code != 200 {
				t.Errorf("status %d: %s", w.Code, w.Body)
			}
			if strings.Contains(route, "?includeChildren=") {
				var response struct {
					MediaContainer struct {
						Metadata []struct{ LeafCount, ViewedLeafCount int }
					}
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.MediaContainer.Metadata) != 1 || response.MediaContainer.Metadata[0].LeafCount != 1200 || response.MediaContainer.Metadata[0].ViewedLeafCount != 3 {
					t.Fatalf("batched badges changed: %+v", response)
				}
			}
			if stats.Queries() > budget || stats.Writes() != 0 {
				for _, s := range stats.Statements() {
					t.Logf("x%d %s", s.Count, s.SQL)
				}
				t.Errorf("budget=%d queries=%d writes=%d", budget, stats.Queries(), stats.Writes())
			}
		})
	}
}

// detailBudgetMediaBrowser reuses the same large fixture through the neutral
// experience service and the real Jellyfin/Emby adapters. Provider enrichment
// is absent; this isolates local catalogue, visibility and watch-state costs.
func detailBudgetMediaBrowser(t *testing.T, db *sql.DB, watch *state.State, show, episode core.RatingKey, budget int64) {
	t.Helper()
	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		for _, key := range []core.RatingKey{show, episode} {
			t.Run(string(protocol)+"/"+key.String(), func(t *testing.T) {
				c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
				if err != nil {
					t.Fatal(err)
				}
				exp, err := experience.NewService(experience.ServiceDeps{
					Protocol: protocol, State: watch, Rows: &experience.RowsService{},
					Active:    func(context.Context, int64) (*core.User, error) { return &ownerUser, nil },
					Libraries: []experience.Library{{ID: 2, Type: core.TypeShow, Name: "TV Shows"}},
					Get:       func(ctx context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) { return c.Get(ctx, k) },
					CachedGet: c.CachedGet,
					Children: func(ctx context.Context, _ *core.User, k core.RatingKey) ([]core.Title, error) {
						return c.Children(ctx, k)
					},
					Visibility: experience.Visibility{GetTitle: c.Get, SectionFor: func(core.MediaType) (int, bool) { return 2, true }},
					Search:     experience.Search{Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) { return nil, nil }},
					NoProviderWait: func(ctx context.Context) context.Context {
						return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				host := frontend.Host{Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*core.User, error) { return &ownerUser, nil }}, Experience: exp}
				var inst frontend.Instance
				if protocol == "emby" {
					inst, err = (mediabrowser.Emby{MachineID: "budget"}).Mount(context.Background(), host)
				} else {
					inst, err = (mediabrowser.Jellyfin{MachineID: "budget"}).Mount(context.Background(), host)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer inst.Close(context.Background())
				ctx, stats := store.WithQueryStats(reqmemo.With(context.Background()))
				r := httptest.NewRequest("GET", "/Items/"+ids.RatingKeyGUID(key), nil).WithContext(ctx)
				r.Header.Set("X-Emby-Token", "grant")
				w := httptest.NewRecorder()
				inst.Handler().ServeHTTP(w, r)
				t.Logf("status=%d queries=%d writes=%d rows=%d", w.Code, stats.Queries(), stats.Writes(), stats.Rows())
				if w.Code != 200 || stats.Queries() > budget || stats.Writes() != 0 {
					t.Fatalf("status=%d queries=%d writes=%d body=%s", w.Code, stats.Queries(), stats.Writes(), w.Body)
				}
			})
		}
	}
}
