package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/safety"
	_ "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

// #800: a signed-in Plex request pages a section under its viewer's own
// catalogue rules. A viewer exempt from the household's browsing rule gets
// the titles it hides, and totalSize counts them, while the household keeps
// its own page.
func TestSectionBrowsePagesUnderViewerRules(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	french := core.LibraryExclusion{Name: "French", MediaType: "movie", Match: "all", Scope: core.VisibilityDiscovery, Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)}}}
	c, err := catalog.New(db, nil, config.Catalog{}, nil,
		catalog.WithNow(func() time.Time { return fixedNow }),
		catalog.WithLibraryPolicies(catalog.LibraryPolicies{MovieRules: []core.LibraryExclusion{french}}))
	if err != nil {
		t.Fatal(err)
	}
	english, frenchFilm := core.MovieKey(9101), core.MovieKey(9102)
	for i, tt := range []core.Title{
		{Key: english, Type: core.TypeMovie, Title: "Harbour Lights", Language: "en", Released: fixedNow.Add(-24 * time.Hour)},
		{Key: frenchFilm, Type: core.TypeMovie, Title: "Les Phares", Language: "fr", Released: fixedNow.Add(-48 * time.Hour)},
	} {
		tt.IDs.TMDB = int64(9101 + i)
		if err := c.UpsertTitle(ctx, tt, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PutViewerRules(ctx, friendUser.ID, catalog.ViewerRules{Mode: "exempt", Exemptions: []string{catalog.RuleIdentity(french)}}); err != nil {
		t.Fatal(err)
	}
	policy, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	policy.SetCatalogPolicy(c.VisibleForViewer, c.DiscoveryVisibleForViewer)
	f := newFixture(func(d *testServerDeps) { d.Catalog = c })
	defer f.srv.Close()
	f.srv.setTestPolicy(policy)

	browse := func(token string) ([]string, int) {
		t.Helper()
		var keys []string
		total := -1
		for start := 0; start < 4; start++ {
			rec := f.get(t, "/library/sections/1/all?X-Plex-Container-Start="+strconv.Itoa(start)+"&X-Plex-Container-Size=1", "Accept", "application/json", "X-Plex-Token", token)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", token, rec.Code, rec.Body.String())
			}
			var body struct{ MediaContainer model.MediaContainer }
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.MediaContainer.TotalSize == nil {
				t.Fatalf("%s: no totalSize", token)
			}
			if total >= 0 && *body.MediaContainer.TotalSize != total {
				t.Fatalf("%s: totalSize moved %d -> %d", token, total, *body.MediaContainer.TotalSize)
			}
			total = *body.MediaContainer.TotalSize
			for _, m := range body.MediaContainer.Metadata {
				keys = append(keys, m.RatingKey)
			}
		}
		slices.Sort(keys)
		return keys, total
	}
	for _, tc := range []struct {
		token string
		want  []string
	}{
		{ownerToken, []string{english.String()}},
		{friendToken, []string{english.String(), frenchFilm.String()}},
	} {
		keys, total := browse(tc.token)
		slices.Sort(tc.want)
		if !slices.Equal(keys, tc.want) || total != len(tc.want) {
			t.Errorf("%s: keys %v totalSize %d, want %v totalSize %d", tc.token, keys, total, tc.want, len(tc.want))
		}
	}
}
