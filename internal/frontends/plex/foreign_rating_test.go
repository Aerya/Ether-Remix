package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/safety"
	_ "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestForeignAndUncertifiedAdultAcrossPlexRoutes(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	// Plex Home / shared viewer capped at PG-13 + None (unrated permitted) via plex.tv share.
	policy.SetPlexShare(func(u *core.User) safety.PlexShare {
		if u != nil && u.ID == friendUser.ID {
			return safety.PlexShare{
				FilterMovies:     "contentRating=G,PG,PG-13,None",
				FilterTelevision: "contentRating!=TV-MA",
			}
		}
		return safety.PlexShare{}
	})

	rel := fixedNow.Add(-24 * time.Hour)
	titles := []core.Title{
		{
			Key:               core.MovieKey(7101),
			Type:              core.TypeMovie,
			Title:             "Manila Midnight",
			ContentRating:     "ph/R-18",
			EnforcementRating: "NC-17",
			Released:          rel,
		},
		{
			Key:               core.MovieKey(7102),
			Type:              core.TypeMovie,
			Title:             "Banned Cut",
			ContentRating:     "PH X",
			EnforcementRating: "NC-17",
			Released:          rel.Add(-time.Hour),
		},
		{
			Key:               core.MovieKey(7103),
			Type:              core.TypeMovie,
			Title:             "Uncertified Softcore",
			ContentRating:     "",
			EnforcementRating: contentrating.UncertifiedAdult,
			Keywords:          []string{"softcore", "erotic"},
			Released:          rel.Add(-2 * time.Hour),
		},
		{
			Key:               core.MovieKey(7104),
			Type:              core.TypeMovie,
			Title:             "London Teen Adventure",
			ContentRating:     "gb/12A",
			EnforcementRating: "PG-13",
			Released:          rel.Add(-3 * time.Hour),
		},
		{
			Key:               core.MovieKey(7105),
			Type:              core.TypeMovie,
			Title:             "Home Festival Short",
			ContentRating:     "",
			EnforcementRating: "",
			Keywords:          []string{"festival"},
			Released:          rel.Add(-4 * time.Hour),
		},
		{
			Key:               core.ShowKey(7201),
			Type:              core.TypeShow,
			Title:             "Seoul After Hours",
			ContentRating:     "kr/청소년관람불가",
			EnforcementRating: "NC-17",
			Released:          rel,
		},
		{
			Key:      core.SeasonKey(7201, 1),
			Type:     core.TypeSeason,
			ShowKey:  core.ShowKey(7201),
			Season:   1,
			Title:    "Season 1",
			Released: rel,
		},
		{
			Key:      core.EpisodeKey(7201, 1, 1),
			Type:     core.TypeEpisode,
			ShowKey:  core.ShowKey(7201),
			Season:   1,
			Episode:  1,
			Title:    "Pilot",
			Released: rel,
		},
	}
	for _, it := range titles {
		if err := c.UpsertTitle(ctx, it, it.Type == core.TypeMovie || it.Type == core.TypeShow); err != nil {
			t.Fatal(err)
		}
	}
	for i, k := range []core.RatingKey{
		core.MovieKey(7101),
		core.MovieKey(7102),
		core.MovieKey(7103),
		core.MovieKey(7104),
		core.MovieKey(7105),
		core.EpisodeKey(7201, 1, 1),
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO state_watch(user_id,rating_key,view_offset_ns,view_count,last_viewed_at,updated_at) VALUES(?,?,?,?,?,?)`,
			friendUser.ID, int64(k), time.Minute.Nanoseconds(), 0, fixedNow.Add(-time.Duration(i)*time.Minute).UnixNano(), fixedNow.UnixNano(),
		); err != nil {
			t.Fatal(err)
		}
	}

	f := newFixture(func(d *testServerDeps) { d.Catalog = c })
	defer f.srv.Close()
	f.srv.setTestPolicy(policy)

	getKeys := func(path, token string) ([]string, model.MediaContainer) {
		t.Helper()
		rec := f.get(t, path, "Accept", "application/json", "X-Plex-Token", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var body struct {
			MediaContainer model.MediaContainer
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, m := range body.MediaContainer.Metadata {
			keys = append(keys, m.RatingKey)
		}
		return keys, body.MediaContainer
	}

	wantFriend := []string{core.MovieKey(7104).String(), core.MovieKey(7105).String()}

	// Section browse keeps gb/12A (mapped PG-13) and benign unrated, hiding ph/R-18, PH X, and uncertified adult.
	secKeys, secMC := getKeys("/library/sections/1/all", friendToken)
	if !slices.Equal(secKeys, []string{core.MovieKey(7105).String(), core.MovieKey(7104).String()}) {
		t.Fatalf("section browse keys = %v", secKeys)
	}
	// Displayed contentRating stays the region's own label ("gb/12A"), not "PG-13".
	for _, m := range secMC.Metadata {
		if m.RatingKey == core.MovieKey(7104).String() && m.ContentRating != "gb/12A" {
			t.Fatalf("gb/12A displayed rating = %q, want gb/12A", m.ContentRating)
		}
	}

	// Recently Added enforces PlexShare in SQL (including totalSize) across movies and episodes.
	recentKeys, recentMC := getKeys("/library/recentlyAdded", friendToken)
	if !slices.Equal(recentKeys, wantFriend) {
		t.Fatalf("recentlyAdded keys = %v, want %v", recentKeys, wantFriend)
	}
	if recentMC.TotalSize == nil || *recentMC.TotalSize != 2 {
		t.Fatalf("recentlyAdded totalSize = %v, want 2", recentMC.TotalSize)
	}

	// On Deck enforces PlexShare in SQL across movies and episodes.
	deckKeys, deckMC := getKeys("/library/onDeck", friendToken)
	if !slices.Equal(deckKeys, wantFriend) {
		t.Fatalf("onDeck keys = %v, want %v", deckKeys, wantFriend)
	}
	if deckMC.TotalSize == nil || *deckMC.TotalSize != 2 {
		t.Fatalf("onDeck totalSize = %v, want 2", deckMC.TotalSize)
	}

	// Direct metadata lookup rejects foreign adult and uncertified adult for the capped viewer,
	// while owner still sees the foreign display rating unchanged ("ph/R-18").
	for _, blocked := range []core.RatingKey{core.MovieKey(7101), core.MovieKey(7102), core.MovieKey(7103), core.ShowKey(7201)} {
		rec := f.get(t, "/library/metadata/"+blocked.String(), "Accept", "application/json", "X-Plex-Token", friendToken)
		if rec.Code == http.StatusOK {
			var body struct{ MediaContainer model.MediaContainer }
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if len(body.MediaContainer.Metadata) > 0 {
				t.Fatalf("blocked key %s leaked on metadata detail for PG-13 viewer", blocked)
			}
		}
	}
	_, ownerMC := getKeys("/library/metadata/"+core.MovieKey(7101).String(), ownerToken)
	if len(ownerMC.Metadata) != 1 || ownerMC.Metadata[0].ContentRating != "ph/R-18" {
		t.Fatalf("owner metadata contentRating = %+v, want ph/R-18", ownerMC.Metadata)
	}
}
