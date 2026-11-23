package plex

import (
	"context"
	"database/sql"
	"github.com/braedonsaunders/ether/internal/core"
	watchstate "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

func TestContinueWatchingRetainsImportedOldSeriesOnHome(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newFixture()
	defer f.srv.Close()
	st := watchstate.New(db, watchstate.Options{Now: func() time.Time { return fixedNow }})
	defer st.Close(ctx)
	f.srv.setState(st)
	f.srv.setDiscovery(nil)
	at := fixedNow.Add(-163 * 24 * time.Hour)
	err = st.ImportTransaction(ctx, func(*sql.Tx) (watchstate.ImportBatch, error) {
		return watchstate.ImportBatch{Records: []watchstate.ImportRecord{{Views: 1, UserID: ownerUser.ID, Key: bbE1.Key, ViewedAt: at}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/hubs/continueWatching/items", "/hubs/home/continueWatching", "/library/onDeck"} {
		mc := decodeJSON(t, f.get(t, path, jsonAccept...))
		rows, _ := mc["Metadata"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["ratingKey"] != bbE2.Key.String() {
			t.Fatalf("%s: %v", path, mc)
		}
		if off, ok := rows[0].(map[string]any)["viewOffset"]; ok && off != float64(0) {
			t.Fatal("invented resume offset", off)
		}
	}
	// A movie must not displace the next episode from Home Continue Watching.
	if err := st.Progress(ctx, ownerUser.ID, fightClub.Key, 10*time.Minute, time.Hour, "playing"); err != nil {
		t.Fatal(err)
	}

	// A different show's in-progress episode shares the Home hub with the
	// in-progress movie and the older show's next-up episode.
	show, season, episode := breakingBad, bbS1, bbE1
	show.Key, show.IDs.TMDB = core.ShowKey(999), 999
	season.Key, season.ShowKey = core.SeasonKey(999, 1), show.Key
	episode.Key, episode.ShowKey = core.EpisodeKey(999, 1, 1), show.Key
	f.catalog.titles[show.Key], f.catalog.titles[season.Key], f.catalog.titles[episode.Key] = show, season, episode
	f.catalog.children[show.Key] = []core.RatingKey{season.Key}
	f.catalog.children[season.Key] = []core.RatingKey{episode.Key}
	if err := st.Progress(ctx, ownerUser.ID, episode.Key, 10*time.Minute, time.Hour, "playing"); err != nil {
		t.Fatal(err)
	}
	mc := decodeJSON(t, f.get(t, "/hubs?identifier=home.continue,home.ondeck", jsonAccept...))

	want := map[string]bool{fightClub.Key.String(): true, episode.Key.String(): true, bbE2.Key.String(): true}
	seen := map[string]bool{}
	found := false
	for _, row := range mc["Hub"].([]any) {
		h := row.(map[string]any)
		id := h["hubIdentifier"]
		if id != "home.continue" && id != "home.ondeck" {
			continue
		}
		if id == "home.continue" {
			found = true
		}
		items, _ := h["Metadata"].([]any)
		if id == "home.continue" && len(items) != len(want) {
			t.Fatalf("Home contents: %v", h)
		}
		for _, raw := range items {
			key := raw.(map[string]any)["ratingKey"].(string)
			if seen[key] {
				t.Fatalf("duplicate between Home and On Deck: %s", key)
			}
			seen[key] = true
			if id == "home.continue" && !want[key] {
				t.Fatalf("unexpected Home item: %s", key)
			}
		}
	}
	if !found {
		t.Fatalf("missing Home Continue Watching: %v", mc)
	}
}

// Pin the mixed Home contract: resume movies and episodes alongside old
// next-up episodes, one item per show, with separately placed On Deck intact.
func TestContinueWatchingMixedHomeMembership(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	f := newFixture()
	defer f.srv.Close()
	st := watchstate.New(db, watchstate.Options{Now: func() time.Time { return fixedNow }})
	defer st.Close(ctx)
	f.srv.setState(st)
	show, season, ep := breakingBad, bbS1, bbE1
	show.Key = core.ShowKey(77)
	season.Key, season.ShowKey = core.SeasonKey(77, 1), show.Key
	ep.Key, ep.ShowKey = core.EpisodeKey(77, 1, 1), show.Key
	for _, title := range []core.Title{show, season, ep} {
		f.catalog.titles[title.Key] = title
	}
	f.catalog.children[show.Key] = []core.RatingKey{season.Key}
	f.catalog.children[season.Key] = []core.RatingKey{ep.Key}
	if err := st.ImportTransaction(ctx, func(*sql.Tx) (watchstate.ImportBatch, error) {
		return watchstate.ImportBatch{Records: []watchstate.ImportRecord{
			{UserID: ownerUser.ID, Key: bbE1.Key, Views: 1, ViewedAt: fixedNow.Add(-163 * 24 * time.Hour)},
			{UserID: ownerUser.ID, Key: alien.Key, Offset: time.Minute, Duration: time.Hour, ViewedAt: fixedNow.Add(-163 * 24 * time.Hour)},
		}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []core.RatingKey{fightClub.Key, ep.Key} {
		if err := st.Progress(ctx, ownerUser.ID, key, 5*time.Minute, time.Hour, "playing"); err != nil {
			t.Fatal(err)
		}
	}
	f.srv.setRecommender(&onDeckLayoutRecommender{always: true})
	mc := decodeJSON(t, f.get(t, "/hubs?identifier=home.continue,home.ondeck", jsonAccept...))
	hubs := mc["Hub"].([]any)
	if len(hubs) != 1 {
		t.Fatalf("Home shelves: %v", mc)
	}
	for i, wantID := range []string{"home.continue"} {
		hub := hubs[i].(map[string]any)
		if hub["hubIdentifier"] != wantID {
			t.Fatalf("custom placement: %v", mc)
		}
		want := map[string]bool{ep.Key.String(): true, bbE2.Key.String(): true}
		if i == 0 {
			want[fightClub.Key.String()] = true
		}
		items := hub["Metadata"].([]any)
		if len(items) != len(want) {
			t.Fatalf("%s membership: %v", wantID, hub)
		}
		for _, raw := range items {
			item := raw.(map[string]any)
			key := item["ratingKey"].(string)
			if !want[key] {
				t.Fatalf("%s duplicated/unexpected item %s: %v", wantID, key, hub)
			}
			delete(want, key)
			if key == bbE2.Key.String() && item["viewOffset"] != nil && item["viewOffset"] != float64(0) {
				t.Fatalf("next-up offset: %v", item)
			}
		}
	}
	// With only episodes, the identical series shelf is suppressed.
	if err := st.Progress(ctx, ownerUser.ID, fightClub.Key, 0, time.Hour, "stopped"); err != nil {
		t.Fatal(err)
	}
	mc = decodeJSON(t, f.get(t, "/hubs?identifier=home.continue,home.ondeck", jsonAccept...))
	hubs = mc["Hub"].([]any)
	if len(hubs) != 1 || hubs[0].(map[string]any)["hubIdentifier"] != "home.continue" {
		t.Fatalf("duplicate series shelf: %v", mc)
	}
}
