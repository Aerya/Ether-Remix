package releasedb

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

func TestSeriesIdentityCatalogueEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, title, original, alias string
		year                         int
		allowed                      bool
	}{
		{name: "unique", title: "Other", year: 1980, allowed: true},
		{name: "same title", title: "Shogun", year: 1980},
		{name: "same year", title: "Shogun", year: 2024},
		{name: "unknown year", title: "Shogun"},
		{name: "original title", title: "Localized", original: "Shogun", year: 1980},
		{name: "alias", title: "Localized", alias: "Shogun", year: 1980},
		{name: "article", title: "The Shogun", year: 1980},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := store.OpenTest(t)
			_, err := db.Exec(`CREATE TABLE catalog_titles(key INTEGER PRIMARY KEY,type TEXT,title TEXT,original_title TEXT,year INTEGER); CREATE TABLE catalog_alt_titles(key INTEGER,title TEXT)`)
			if err != nil {
				t.Fatal(err)
			}
			key := core.ShowKey(1)
			if _, err = db.Exec(`INSERT INTO catalog_titles VALUES(?,'show','Shogun','Shogun',2024),(?,'show',?,?,?)`, key, core.ShowKey(2), tc.title, tc.original, tc.year); err != nil {
				t.Fatal(err)
			}
			if tc.alias != "" {
				if _, err = db.Exec(`INSERT INTO catalog_alt_titles VALUES(?,?)`, core.ShowKey(2), tc.alias); err != nil {
					t.Fatal(err)
				}
			}
			q := core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 1}, Title: "Shogun", Year: 2024, Season: 1, Episode: 1, SeriesIdentityChecked: true}
			q = CheckSeriesIdentity(t.Context(), db, q)
			r := core.Release{Name: "Shogun.S01E01.1080p"}
			if got := releaseparse.AssociationReason(q, r); (got == "") != tc.allowed {
				t.Fatalf("allowed=%v reason=%s", tc.allowed, got)
			}
			// A fresh check observes a new namesake; evidence is never persisted.
			if _, err = db.Exec(`INSERT INTO catalog_titles VALUES(?,'show','Shogun','',1980)`, core.ShowKey(3)); err != nil {
				t.Fatal(err)
			}
			if CheckSeriesIdentity(t.Context(), db, q).SeriesIdentityChecked {
				t.Fatal("stale evidence reused")
			}
			if CheckSeriesIdentity(t.Context(), nil, q).SeriesIdentityChecked {
				t.Fatal("missing catalogue accepted")
			}
			if _, err = db.Exec(`DROP TABLE catalog_alt_titles`); err != nil {
				t.Fatal(err)
			}
			if CheckSeriesIdentity(t.Context(), db, q).SeriesIdentityChecked {
				t.Fatal("failed lookup accepted")
			}
		})
	}
}

func TestAssociationRequestsCarryYearlessEvidenceToEpisodes(t *testing.T) {
	db := store.OpenTest(t)
	_, err := db.Exec(`CREATE TABLE catalog_titles(key INTEGER PRIMARY KEY,type TEXT,tmdb_id INTEGER,imdb TEXT,tvdb INTEGER,title TEXT,original_title TEXT,year INTEGER,language TEXT);CREATE TABLE catalog_alt_titles(key INTEGER,title TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	key := core.ShowKey(84773)
	_, err = db.Exec(`INSERT INTO catalog_titles VALUES(?,'show',84773,'tt7631058',0,'Les Anneaux de pouvoir','The Rings of Power',2022,'en')`, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, scoped := range []bool{false, true} {
		var keys []core.RatingKey
		if scoped {
			keys = []core.RatingKey{key}
		}
		requests, err := AssociationRequestsFor(t.Context(), db, keys)
		if err != nil {
			t.Fatal(err)
		}
		q, ok := AssociationRequest(requests, core.EpisodeKey(84773, 1, 1), 0, 0)
		if !ok || !q.SeriesIdentityChecked {
			t.Fatalf("missing evidence: %+v", q)
		}
		if reason := releaseparse.AssociationReason(q, core.Release{Name: "The.Rings.of.Power.S01E01.1080p"}); reason != "" {
			t.Fatal(reason)
		}
	}
}
