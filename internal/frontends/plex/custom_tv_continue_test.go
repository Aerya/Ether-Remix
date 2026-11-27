package plex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/personalize"
	watchstate "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

type customTVContinueExperience struct {
	frontend.Experience
}

func (customTVContinueExperience) PublishedViews(context.Context) ([]frontend.View, error) {
	return []frontend.View{
		{ID: "2", LibraryID: core.LibraryShows, Name: "Built-in TV", Type: core.TypeShow, Native: plexBaseShow},
		{ID: "4", LibraryID: 4, Name: "Custom TV", Type: core.TypeShow, Native: plexBaseShow},
	}, nil
}

func (customTVContinueExperience) LibraryBrowseFilter(_ context.Context, _ *core.User, _ string, f core.Filter) (core.Filter, bool, error) {
	return f, false, nil
}

func (customTVContinueExperience) LibraryMembers(_ context.Context, id core.LibraryID, keys []core.RatingKey) (map[core.RatingKey]bool, bool, error) {
	members := map[core.RatingKey]bool{}
	for _, key := range keys {
		if key == breakingBad.Key {
			members[key] = true
		}
	}
	return members, id == 4, nil
}

func TestCustomTVRecommendedContinueWatching(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
		want   core.RatingKey
	}{
		{"partial playback", 10 * time.Minute, bbE1.Key},
		{"completed playback", 58 * time.Minute, bbE2.Key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			viewer := friendUser
			viewer.AllowedLibraries = []core.LibraryID{4}
			f := newFixture(func(d *testServerDeps) {
				setDepsAuth(d, &fakeAuth{users: map[string]core.User{friendToken: viewer}})
			})
			defer f.srv.Close()
			db := store.OpenTest(t)
			now := fixedNow
			st := watchstate.New(db, watchstate.Options{Now: func() time.Time { return now }})
			defer st.Close(ctx)
			f.srv.setState(st)

			// A newer resume from another library must not use up count=1.
			otherShow, otherEpisode := breakingBad, bbE1
			otherShow.Key = core.ShowKey(77)
			otherEpisode.Key, otherEpisode.ShowKey = core.EpisodeKey(77, 1, 1), otherShow.Key
			f.catalog.titles[otherShow.Key], f.catalog.titles[otherEpisode.Key] = otherShow, otherEpisode
			if err := st.Progress(ctx, viewer.ID, bbE1.Key, tc.offset, time.Hour, "stopped"); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			if err := st.Progress(ctx, viewer.ID, otherEpisode.Key, 10*time.Minute, time.Hour, "stopped"); err != nil {
				t.Fatal(err)
			}

			e, err := personalize.New(personalize.Options{
				DB: db, Catalog: f.catalog, State: st, Now: func() time.Time { return fixedNow },
				RefreshEvery: -1, LibrarySections: func(context.Context) map[int]core.MediaType {
					return map[int]core.MediaType{2: core.TypeShow, 4: core.TypeShow}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			f.srv.setRecommender(e)
			f.srv.bindHost(frontend.Host{Experience: customTVContinueExperience{f.srv.exp()}})
			saveCuratedPage(t, e, core.LibraryPage(4, "recommended"), 1, core.Row{Source: core.RowSource{Kind: personalize.KindContinue}})

			read := func(t *testing.T, path string) model.MediaContainer {
				t.Helper()
				rec := f.get(t, path, "Accept", "application/json", "X-Plex-Token", friendToken)
				var body struct{ MediaContainer model.MediaContainer }
				if err := json.Unmarshal(rec.Body.Bytes(), &body); rec.Code != 200 || err != nil {
					t.Fatalf("%s: HTTP %d, decode error %v", path, rec.Code, err)
				}
				return body.MediaContainer
			}
			checkEpisode := func(t *testing.T, items []model.Metadata) {
				t.Helper()
				if len(items) != 1 {
					t.Fatalf("want exactly one episode from the member show, got %d", len(items))
				}
				item := items[0]
				if item.RatingKey != tc.want.String() || item.LibrarySectionID != 4 {
					t.Errorf("got episode %s in section %d, want %s in section 4", item.RatingKey, item.LibrarySectionID, tc.want)
				}
				var wantOffset int64
				if tc.want == bbE1.Key {
					wantOffset = tc.offset.Milliseconds()
				}
				if item.ViewOffset != wantOffset {
					t.Errorf("got view offset %d, want %d", item.ViewOffset, wantOffset)
				}
			}
			const id = "tv.inprogress.4"
			const section = "/hubs/sections/4"
			t.Run("Recommended", func(t *testing.T) {
				mc := read(t, section+"?count=1&includeLibraryPlaylists=1")
				if len(mc.Hub) == 0 || mc.Hub[0].HubIdentifier != id {
					t.Fatalf("first hub should be %s", id)
				}
				checkEpisode(t, mc.Hub[0].Metadata)
				if mc.Hub[0].More {
					t.Error("an out-of-library title was counted as overflow")
				}
			})
			t.Run("items", func(t *testing.T) {
				checkEpisode(t, read(t, section+"/continueWatching/items").Metadata)
			})
			for _, path := range []string{section + "/promoted", section + "?excludeContinueWatching=1"} {
				for _, hub := range read(t, path).Hub {
					if hub.HubIdentifier == id {
						t.Errorf("%s unexpectedly returned %s", path, id)
					}
				}
			}
		})
	}
}
