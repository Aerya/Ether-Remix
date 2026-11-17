package releasedb

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
	"strings"
	"testing"
	"time"
)

func TestAssociationAuditPreservesQuarantineAndVerifiedRows(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	_, err := db.Exec(`CREATE TABLE catalog_titles(key INTEGER PRIMARY KEY,type TEXT,tmdb_id INTEGER,imdb TEXT,tvdb INTEGER,title TEXT,original_title TEXT,year INTEGER,language TEXT);CREATE TABLE catalog_alt_titles(key INTEGER,title TEXT);INSERT INTO catalog_titles VALUES(1000000000048463,'movie',48463,'tt0374639',0,'Incident au Loch Ness','Incident at Loch Ness',2004,'en')`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	l := Library{Store: NewStore(db), Seal: func(s string) (string, error) { return "private:" + s, nil }, Open: func(s string) (string, error) { return strings.TrimPrefix(s, "private:"), nil }}
	key := core.MovieKey(48463)
	good := core.Release{ID: "good", Name: "Incident.at.Loch.Ness.2004.1080p", Indexer: "valid", StreamURL: "https://private.invalid/token"}
	bad := core.Release{ID: "bad", Name: "Superman.2025.1080p", Indexer: "wrong", TitleIDs: core.IDs{TMDB: 48463}, TitleIDsEchoed: true}
	if err = l.Save(ctx, key, []core.Release{good, bad}); err != nil {
		t.Fatal(err)
	}
	if err = l.Store.Save(ctx, []Release{FromCore(core.Title{Key: key, Type: core.TypeMovie}, 0, 0, "valid", good, nil), FromCore(core.Title{Key: key, Type: core.TypeMovie}, 0, 0, "wrong", bad, nil)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	dry, err := AuditAssociations(ctx, db, l.Open, false)
	if err != nil || dry.Checked != 4 || dry.Quarantined != 2 {
		t.Fatalf("%+v %v", dry, err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM pse_release_library`).Scan(&n)
	if n != 2 {
		t.Fatal("dry run mutated library")
	}
	applied, err := AuditAssociations(ctx, db, l.Open, true)
	if err != nil || applied.Quarantined != 2 {
		t.Fatalf("%+v %v", applied, err)
	}
	for _, table := range []string{"pse_release_library", "pse_releases", "pse_release_library_quarantine", "pse_releases_quarantine"} {
		if err = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s %d %v", table, n, err)
		}
	}
	// Simulate a restart after quarantine transactions but before the final marker.
	if _, err = db.Exec(`DELETE FROM pse_association_audit`); err != nil {
		t.Fatal(err)
	}
	again, err := AuditAssociations(ctx, db, l.Open, true)
	if err != nil || again.Quarantined != 0 {
		t.Fatalf("%+v %v", again, err)
	}
	if err = db.QueryRow(`SELECT quarantined FROM pse_association_audit WHERE version=1`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("resumed quarantine counter: %d %v", n, err)
	}
	raw, _ := json.Marshal(applied)
	if strings.Contains(string(raw), "token") || strings.Contains(string(raw), "private.invalid") {
		t.Fatal("descriptor leaked in report")
	}
}
