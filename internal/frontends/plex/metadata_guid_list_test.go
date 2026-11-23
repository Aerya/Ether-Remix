package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/plexguid"
	"github.com/braedonsaunders/ether/internal/store"
)

// Infuse resolves its Plex Watchlist row against the server with one
// /library/metadata request naming plex.tv identities, slashes and all.
func TestMetadataByPlexGUIDList(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const movie, show = "plex://movie/5d7768265af944001f1f6976", "plex://show/5d9c086c46115600200aa2fe"
	if err = storeVerifiedGUID(db, fightClub.Key, movie, 550, "Fjord Harbor", 1999); err != nil {
		t.Fatal(err)
	}
	if err = storeVerifiedGUID(db, breakingBad.Key, show, 1396, "Boundary Bay", 2008); err != nil {
		t.Fatal(err)
	}
	pg := plexguid.New(db, plexguid.Options{})
	defer pg.Close()
	f := newFixture()
	f.srv.setPlexGUID(pg)
	defer f.srv.Close()
	f.srv.setCatalog(guidCatalog{f.catalog})

	rec := f.get(t, "/library/metadata/"+movie+",plex://movie/000000000000000000000000,"+show, jsonAccept...)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"ratingKey":"`+fightClub.Key.String()+`"`) || !strings.Contains(body, `"ratingKey":"`+breakingBad.Key.String()+`"`) {
		t.Fatalf("guid list: %d %s", rec.Code, body)
	}
	if rec := f.get(t, "/library/metadata/plex://movie/000000000000000000000000", jsonAccept...); rec.Code != 404 {
		t.Fatalf("unknown identity: %d", rec.Code)
	}
}

// Issue #654: Discover / More Ways to Watch queries /library/all?guid=plex://...
// and /library/metadata/plex://... to offer Ether alongside a local PMS. Both
// routes must verify and return matching titles even when a pre-v0.3.10 mapping
// has credits_rule=0 or the title has not been mapped yet.
func TestMetadataAndLibraryGUIDUpgradePreRule2AndCold(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?),(?,?,?,?,?)`,
		int64(fightClub.Key), "movie", 550, "Fjord Harbor", 1999,
		int64(alien.Key), "movie", 348, "Alien", 1979); err != nil {
		t.Fatal(err)
	}
	const (
		staleMovie = "plex://movie/5d7768265af944001f1f6976"
		coldMovie  = "plex://movie/5d7768265af944001f1f0348"
	)
	now := time.Now().Unix()
	if _, err := db.Exec(`INSERT INTO plexguid_mappings(rating_key,guid,checked_at) VALUES(?,?,?)`, int64(fightClub.Key), staleMovie, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO plexguid_identity(rating_key,guid,tmdb,title,year,credits_rule) VALUES(?,?,?,?,?,0)`, int64(fightClub.Key), staleMovie, 550, "Fjord Harbor", 1999); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/" + staleMovie[len(staleMovie)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Fjord Harbor","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`, staleMovie)
		case "/library/metadata/" + coldMovie[len(coldMovie)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Alien","year":1979,"Guid":[{"id":"tmdb://348"}]}]}}`, coldMovie)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	pg := plexguid.New(db, plexguid.Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }})
	defer pg.Close()
	f := newFixture()
	f.srv.setPlexGUID(pg)
	defer f.srv.Close()

	// Pre-v0.3.10 credits_rule=0 row on /library/all?guid=... upgrades and returns the plex:// guid.
	rec := f.get(t, "/library/all?guid="+url.QueryEscape(staleMovie), jsonAccept...)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), fightClub.Key.String()) || !strings.Contains(rec.Body.String(), staleMovie) {
		t.Fatalf("/library/all stale guid: %d %s", rec.Code, rec.Body.String())
	}

	// Cold single-GUID /library/metadata/plex://... verifies live and returns the plex:// guid.
	rec = f.get(t, "/library/metadata/"+coldMovie, jsonAccept...)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), alien.Key.String()) || !strings.Contains(rec.Body.String(), coldMovie) {
		t.Fatalf("/library/metadata cold guid: %d %s", rec.Code, rec.Body.String())
	}
}
