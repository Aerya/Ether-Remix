package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

type aiSearchCatalog struct {
	*fakeCatalog
	byQuery map[string][]core.Title
}

func (c *aiSearchCatalog) Search(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error) {
	norm := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	if hits, ok := c.byQuery[norm]; ok {
		allow := map[core.MediaType]bool{}
		for _, typ := range types {
			allow[typ] = true
		}
		var out []core.Title
		for _, t := range hits {
			if allow[t.Type] {
				out = append(out, t)
			}
		}
		if limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	}
	return c.fakeCatalog.Search(ctx, query, types, limit)
}

type fakeAIVibe struct {
	mu       sync.Mutex
	started  map[string]int
	canceled map[string]int
	cached   map[string]core.DescriptiveResult
	delay    map[string]time.Duration
	partial  map[string][]core.Title
	items    map[string][]core.Title
	colTitle map[string]string
}

func newFakeAIVibe() *fakeAIVibe {
	return &fakeAIVibe{
		started:  map[string]int{},
		canceled: map[string]int{},
		cached:   map[string]core.DescriptiveResult{},
		delay:    map[string]time.Duration{},
		partial:  map[string][]core.Title{},
		items:    map[string][]core.Title{},
		colTitle: map[string]string{},
	}
}

func (v *fakeAIVibe) norm(q string) string {
	return strings.Join(strings.Fields(strings.ToLower(q)), " ")
}

func (v *fakeAIVibe) DescriptiveSearchAvailable() bool { return true }

func (v *fakeAIVibe) PromoteEphemeral(context.Context, *core.User, core.RatingKey) (*core.Collection, bool, error) {
	return nil, false, nil
}

func (v *fakeAIVibe) DescribeCached(_ *core.User, query string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.cached[v.norm(query)]
	return ok
}

func (v *fakeAIVibe) CancelDescribeSearch(_ *core.User, query string) {
	k := v.norm(query)
	v.mu.Lock()
	v.canceled[k]++
	delete(v.cached, k)
	v.mu.Unlock()
}

func (v *fakeAIVibe) DescribeSearch(ctx context.Context, _ *core.User, query string, wait bool) (core.DescriptiveResult, error) {
	k := v.norm(query)
	v.mu.Lock()
	if res, ok := v.cached[k]; ok {
		v.mu.Unlock()
		return res, nil
	}
	if !wait {
		part := v.partial[k]
		v.mu.Unlock()
		if len(part) > 0 {
			return core.DescriptiveResult{State: "partial", Title: v.colTitle[k], Items: append([]core.Title(nil), part...)}, nil
		}
		return core.DescriptiveResult{State: "pending", RetryAfterSeconds: 2}, nil
	}
	v.started[k]++
	d := v.delay[k]
	items := append([]core.Title(nil), v.items[k]...)
	title := v.colTitle[k]
	v.mu.Unlock()

	ready := core.DescriptiveResult{State: "ready", Title: title, Items: items}
	if d <= 0 {
		v.mu.Lock()
		v.cached[k] = ready
		v.mu.Unlock()
		return ready, nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		// Simulate detached background completion when a request wait bound expires
		// (unless explicitly canceled via CancelDescribeSearch).
		go func() {
			time.Sleep(d)
			v.mu.Lock()
			if v.canceled[k] == 0 {
				v.cached[k] = ready
			}
			v.mu.Unlock()
		}()
		return core.DescriptiveResult{}, ctx.Err()
	case <-timer.C:
		v.mu.Lock()
		v.cached[k] = ready
		v.mu.Unlock()
		return ready, nil
	}
}

func (v *fakeAIVibe) VibeCollection(_ context.Context, _ *core.User, query string) (*core.Collection, []core.Title, bool, error) {
	k := v.norm(query)
	v.mu.Lock()
	res, ok := v.cached[k]
	title := v.colTitle[k]
	v.mu.Unlock()
	if !ok || len(res.Items) == 0 {
		return nil, nil, false, nil
	}
	if title == "" {
		title = query
	}
	col := &core.Collection{
		Key:        core.EphemeralSmartKey(1),
		Title:      title,
		Kind:       "vibe",
		Type:       res.Items[0].Type,
		ChildCount: len(res.Items),
	}
	return col, append([]core.Title(nil), res.Items...), true, nil
}

func (v *fakeAIVibe) startCount(query string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.started[v.norm(query)]
}

func TestPlexSearchAIFirst(t *testing.T) {
	pacificRim := core.Title{
		Key: core.MovieKey(68726), Type: core.TypeMovie, IDs: core.IDs{TMDB: 68726},
		Title: "Pacific Rim", Year: 2013, Released: time.Date(2013, 7, 12, 0, 0, 0, 0, time.UTC),
		Runtime: 131 * time.Minute, Genres: []string{"Action", "Science Fiction"},
		Popularity: 80, PosterPath: "/pacific.jpg", UpdatedAt: fixedNow,
		SearchScore: experience.TitleMatchScore,
	}
	aiMystery := core.Title{
		Key: core.MovieKey(546554), Type: core.TypeMovie, IDs: core.IDs{TMDB: 546554},
		Title: "Knives Out", Year: 2019, Released: time.Date(2019, 11, 27, 0, 0, 0, 0, time.UTC),
		Runtime: 130 * time.Minute, Genres: []string{"Mystery", "Crime"},
		Popularity: 90, PosterPath: "/knives.jpg", UpdatedAt: fixedNow,
	}
	weakKeyword := core.Title{
		Key: core.MovieKey(195589), Type: core.TypeMovie, IDs: core.IDs{TMDB: 195589},
		Title: "British Mystery Road", Year: 2013, Released: time.Date(2013, 8, 15, 0, 0, 0, 0, time.UTC),
		Runtime: 121 * time.Minute, Genres: []string{"Mystery"},
		Popularity: 30, PosterPath: "/road.jpg", UpdatedAt: fixedNow,
		SearchScore: 0.35,
	}

	ai := newFakeAIVibe()
	ai.items["cozy british mysteries"] = []core.Title{aiMystery}
	ai.colTitle["cozy british mysteries"] = "Cozy British Mysteries"
	ai.delay["cozy british mysteries"] = 15 * time.Millisecond

	var customCat *aiSearchCatalog
	f := newFixture(func(d *testServerDeps) {
		baseCat := d.Catalog.(*fakeCatalog)
		baseCat.titles[pacificRim.Key] = pacificRim
		baseCat.titles[aiMystery.Key] = aiMystery
		baseCat.titles[weakKeyword.Key] = weakKeyword
		customCat = &aiSearchCatalog{
			fakeCatalog: baseCat,
			byQuery: map[string][]core.Title{
				"pacific rim":            {pacificRim},
				"cozy british mysteries": {weakKeyword},
				"slow 80s action":        {weakKeyword},
			},
		}
		d.Catalog = customCat
		d.VibeSearch = ai
	})

	decodeMC := func(t *testing.T, recCode int, body []byte) model.MediaContainer {
		t.Helper()
		if recCode != http.StatusOK {
			t.Fatalf("status %d: %s", recCode, body)
		}
		var env struct {
			MediaContainer model.MediaContainer `json:"MediaContainer"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("decode json: %v\n%s", err, body)
		}
		return env.MediaContainer
	}

	// 1. Title query returns without starting an AI job.
	rec := f.get(t, "/hubs/search?query=Pacific+Rim", "Accept", "application/json", "X-Plex-Client-Identifier", "plezy-title")
	mc := decodeMC(t, rec.Code, rec.Body.Bytes())
	if ai.startCount("pacific rim") != 0 {
		t.Fatalf("title query started %d AI jobs, want 0", ai.startCount("pacific rim"))
	}
	if len(mc.Hub) == 0 || mc.Hub[0].HubIdentifier != "movie" || len(mc.Hub[0].Metadata) == 0 || mc.Hub[0].Metadata[0].Title != "Pacific Rim" {
		t.Fatalf("unexpected title /hubs/search leading hub: %+v", mc.Hub)
	}

	// 2. Descriptive query waits for AI and returns AI items FIRST on /hubs/search, /library/search, and section search.
	rec = f.get(t, "/hubs/search?query=cozy+british+mysteries", "Accept", "application/json", "X-Plex-Client-Identifier", "plezy-desc")
	mc = decodeMC(t, rec.Code, rec.Body.Bytes())
	if ai.startCount("cozy british mysteries") != 1 {
		t.Fatalf("descriptive query started %d AI jobs, want 1", ai.startCount("cozy british mysteries"))
	}
	if len(mc.Hub) < 2 || mc.Hub[0].HubIdentifier != "collection" || len(mc.Hub[0].Directory) != 1 || mc.Hub[0].Directory[0].Title != "Cozy British Mysteries" {
		t.Fatalf("/hubs/search did not lead with AI collection hub: %+v", mc.Hub)
	}
	if mc.Hub[1].HubIdentifier != "movie" || len(mc.Hub[1].Metadata) < 2 || mc.Hub[1].Metadata[0].Title != "Knives Out" || mc.Hub[1].Metadata[1].Title != "British Mystery Road" {
		t.Fatalf("/hubs/search movie hub did not lead with AI item ahead of keyword hit: %+v", mc.Hub[1])
	}

	// Repeat query hits warm AI cache without starting a second AI job.
	rec = f.get(t, "/library/search?query=cozy+british+mysteries", "Accept", "application/json", "X-Plex-Client-Identifier", "plezy-desc")
	mc = decodeMC(t, rec.Code, rec.Body.Bytes())
	if ai.startCount("cozy british mysteries") != 1 {
		t.Fatalf("warm /library/search started extra AI job: %d", ai.startCount("cozy british mysteries"))
	}
	if len(mc.SearchResult) < 3 || mc.SearchResult[0].Metadata == nil || mc.SearchResult[0].Metadata.Title != "Knives Out" {
		t.Fatalf("/library/search did not lead with AI item: %+v", mc.SearchResult)
	}
	if mc.SearchResult[1].Directory == nil || mc.SearchResult[1].Directory.Title != "Cozy British Mysteries" {
		t.Fatalf("/library/search second result should be AI collection directory, got %+v", mc.SearchResult[1])
	}
	if mc.SearchResult[2].Metadata == nil || mc.SearchResult[2].Metadata.Title != "British Mystery Road" {
		t.Fatalf("/library/search third result should be keyword fallback, got %+v", mc.SearchResult[2])
	}

	rec = f.get(t, "/library/sections/1/search?query=cozy+british+mysteries", "Accept", "application/json", "X-Plex-Client-Identifier", "plezy-desc")
	mc = decodeMC(t, rec.Code, rec.Body.Bytes())
	if len(mc.Metadata) < 2 || mc.Metadata[0].Title != "Knives Out" || mc.Metadata[1].Title != "British Mystery Road" {
		t.Fatalf("section search did not lead with AI item: %+v", mc.Metadata)
	}
}

func TestSearchResolveBoundFallbackThenCached(t *testing.T) {
	ctx := core.WithSearchClient(context.Background(), "client-bound")
	viewer := &core.User{ID: 1, IsOwner: true}
	aiItem := core.Title{Key: core.MovieKey(101), Type: core.TypeMovie, Title: "Die Hard"}
	partialItem := core.Title{Key: core.MovieKey(102), Type: core.TypeMovie, Title: "Lethal Weapon"}
	kwItem := core.Title{Key: core.MovieKey(103), Type: core.TypeMovie, Title: "Action Jackson", SearchScore: 0.4}

	ai := newFakeAIVibe()
	ai.items["slow 80s action"] = []core.Title{aiItem}
	ai.partial["slow 80s action"] = []core.Title{partialItem}
	ai.colTitle["slow 80s action"] = "80s Action"
	ai.delay["slow 80s action"] = 80 * time.Millisecond

	vis := experience.Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}
	s := experience.Search{
		Visibility: vis,
		Vibe:       experience.VibeSearch{Source: ai, Visibility: vis, Unrestricted: func(*core.User) bool { return true }},
		Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) {
			return []core.Title{kwItem}, nil
		},
		Unrestricted: func(*core.User) bool { return true },
		WaitBound:    20 * time.Millisecond,
		SettleDelay:  -1,
	}

	// First call hits the 20ms bound: returns partial AI item ahead of keyword fallback.
	res, err := s.Resolve(ctx, viewer, "slow 80s action", []core.MediaType{core.TypeMovie}, 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.AIItems != 1 || len(res.Items) != 2 || res.Items[0].Key != partialItem.Key || res.Items[1].Key != kwItem.Key {
		t.Fatalf("bound expiry did not return partial AI first: %+v", res)
	}

	// Once the detached AI job finishes, the next identical query returns the finished cached AI set.
	deadline := time.Now().Add(500 * time.Millisecond)
	for !ai.DescribeCached(viewer, "slow 80s action") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	res2, err := s.Resolve(ctx, viewer, "slow 80s action", []core.MediaType{core.TypeMovie}, 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if res2.AIItems != 1 || res2.Collection == nil || len(res2.Items) != 2 || res2.Items[0].Key != aiItem.Key || res2.Items[1].Key != kwItem.Key {
		t.Fatalf("repeat query did not return cached finished AI set: %+v", res2)
	}
}
