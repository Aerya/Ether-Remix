package plex

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type coldDetailProvider struct {
	catalog.TMDB
	release    chan struct{}
	foreground atomic.Int64
	shows      atomic.Int64
	mu         sync.Mutex
	seasons    map[int]int
}

func (p *coldDetailProvider) TVDetails(ctx context.Context, id int64) (*tmdb.TVDetails, error) {
	if store.QueryStatsFrom(ctx) != nil {
		p.foreground.Add(1)
	}
	p.shows.Add(1)
	d := &tmdb.TVDetails{TVShow: tmdb.TVShow{ID: id, Name: "Cold Show", FirstAirDate: "2000-01-01"}, NumberOfSeasons: 30, NumberOfEpisodes: 1200, Status: "Ended"}
	for n := 1; n <= 30; n++ {
		d.Seasons = append(d.Seasons, tmdb.SeasonSummary{SeasonNumber: n, Name: fmt.Sprint("Season ", n), AirDate: "2000-01-01", EpisodeCount: 40})
	}
	return d, nil
}
func (p *coldDetailProvider) SeasonDetails(ctx context.Context, id int64, n int) (*tmdb.SeasonDetails, error) {
	if store.QueryStatsFrom(ctx) != nil {
		p.foreground.Add(1)
	} else {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	p.seasons[n]++
	p.mu.Unlock()
	d := &tmdb.SeasonDetails{SeasonNumber: n, Name: fmt.Sprint("Season ", n), AirDate: "2000-01-01"}
	for e := 1; e <= 40; e++ {
		d.Episodes = append(d.Episodes, tmdb.Episode{SeasonNumber: n, EpisodeNumber: e, Name: fmt.Sprint("Episode ", e), AirDate: "2000-01-01", ShowID: id})
	}
	return d, nil
}

func TestColdDetailFamilyBudget(t *testing.T) {
	for _, suffix := range []string{"", "/children", "/grandchildren", "/allLeaves"} {
		for _, local := range []bool{false, true} {
			t.Run(fmt.Sprintf("local=%t%s", local, suffix), func(t *testing.T) {
				db := store.OpenTest(t)
				p := &coldDetailProvider{release: make(chan struct{}), seasons: map[int]int{}}
				c, err := catalog.New(db, p, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
				if err != nil {
					t.Fatal(err)
				}
				released := false
				defer func() {
					if !released {
						close(p.release)
					}
					c.WaitRefreshes()
				}()
				show := core.Title{Key: core.ShowKey(7331), IDs: core.IDs{TMDB: 7331}, Type: core.TypeShow, Title: "Cold Show", Released: fixedNow.Add(-48 * time.Hour)}
				f := newFixture(func(d *testServerDeps) {
					d.Catalog = c
					d.State = state.New(db, state.Options{})
					d.DB = db
					d.DisableEnrichment = true
					d.Recommender = nil
					d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
						if local {
							return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
						}
						return ctx
					}}
				})
				defer f.srv.Close()
				call := func(route string) *store.QueryStats {
					ctx, stats := store.WithQueryStats(reqmemo.With(context.Background()))
					r := httptest.NewRequest("GET", route, nil).WithContext(ctx)
					r.Header.Set("X-Plex-Token", ownerToken)
					r.Header.Set("Accept", "application/json")
					w := httptest.NewRecorder()
					f.srv.ServeHTTP(w, r)
					if w.Code != 200 {
						t.Fatalf("%s: status=%d %s", route, w.Code, w.Body)
					}
					return stats
				}
				path := "/library/metadata/" + show.Key.String() + suffix
				_ = call(path)
				if !local {
					return
				}
				if p.foreground.Load() != 1 {
					t.Fatalf("cold show foreground=%d", p.foreground.Load())
				}
				// Concurrent views reuse the same admitted show job/flight.
				var opened sync.WaitGroup
				for range 4 {
					opened.Add(1)
					go func() {
						defer opened.Done()
						stats := call(path)
						if stats.Writes() != 0 {
							t.Errorf("concurrent repeat: writes=%d", stats.Writes())
						}
					}()
				}
				opened.Wait()
				before := p.foreground.Load()
				repeat := call(path)
				if p.foreground.Load() != before || repeat.Writes() != 0 {
					t.Fatalf("repeat view: calls=%d writes=%d", p.foreground.Load()-before, repeat.Writes())
				}
				// Season 30 has priority while background seasons 1..6 are held.
				_ = call("/library/metadata/" + core.SeasonKey(7331, 30).String() + "/children")
				if p.foreground.Load() != before+1 {
					t.Fatalf("cold season foreground=%d", p.foreground.Load()-before)
				}
				before = p.foreground.Load()
				again := call("/library/metadata/" + core.SeasonKey(7331, 30).String() + "/children")
				if p.foreground.Load() != before || again.Writes() != 0 {
					t.Fatalf("stored season calls=%d writes=%d", p.foreground.Load()-before, again.Writes())
				}
				before = p.foreground.Load()
				_ = call("/library/metadata/" + core.EpisodeKey(7331, 29, 1).String())
				if p.foreground.Load() != before+1 {
					t.Fatalf("cold episode calls=%d", p.foreground.Load()-before)
				}
				before = p.foreground.Load()
				episode := call("/library/metadata/" + core.EpisodeKey(7331, 29, 1).String())
				if p.foreground.Load() != before || episode.Writes() != 0 {
					t.Fatalf("repeat episode calls=%d writes=%d", p.foreground.Load()-before, episode.Writes())
				}
				close(p.release)
				released = true
				c.WaitRefreshes()
				before = p.foreground.Load()
				finished := call("/library/metadata/" + core.SeasonKey(7331, 30).String() + "/children")
				if p.foreground.Load() != before || finished.Writes() != 0 {
					t.Fatalf("finished season fetched again: calls=%d writes=%d", p.foreground.Load()-before, finished.Writes())
				}
				if p.shows.Load() != 1 {
					t.Fatalf("show job fetched show %d times", p.shows.Load())
				}
				p.mu.Lock()
				defer p.mu.Unlock()
				for n, calls := range p.seasons {
					if calls != 1 {
						t.Errorf("season %d fetched %d times", n, calls)
					}
				}
				if len(p.seasons) != 30 {
					t.Errorf("background filled %d seasons", len(p.seasons))
				}
			})
		}
	}
}
