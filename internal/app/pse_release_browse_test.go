package app

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/store"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReleaseBrowse(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(db, nil, config.Catalog{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, cfg: cfg, catalog: cat, log: slog.Default()}
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	for _, title := range []core.Title{
		{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "Le film", OriginalTitle: "Original movie"},
		{Key: core.ShowKey(2), Type: core.TypeShow, Title: "Une série"},
		{Key: core.ShowKey(3), Type: core.TypeShow, Title: "Un anime", Language: "ja", Genres: []string{"Animation"}},
	} {
		if err = cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []core.RatingKey{core.MovieKey(1), core.EpisodeKey(2, 1, 1), core.EpisodeKey(2, 2, 5), core.EpisodeKey(3, 1, 1)} {
		a.rememberReleases(ctx, &core.Title{Key: key}, []core.Release{{ID: "secret-id", Name: "Unique.1080p", Indexer: "Source", StreamURL: "https://private.test/secret", StreamHeaders: map[string]string{"Authorization": "secret-header"}}})
	}
	// Exercise bounded backfill for pre-existing encrypted entries.
	if _, err = db.Exec(`UPDATE pse_release_library SET browse=''`); err != nil {
		t.Fatal(err)
	}
	// Matching metadata must not duplicate the episode reference.
	meta := releasedb.FromCore(core.Title{Key: core.ShowKey(2), Type: core.TypeShow}, 2, 5, "source", core.Release{ID: "meta", Name: "Unique.1080p", Indexer: "Source"}, nil)
	if err = releasedb.NewStore(db).Save(ctx, []releasedb.Release{meta}, time.Now()); err != nil {
		t.Fatal(err)
	}
	request := func(query string) struct {
		Items []browseItem
		Total int
		Page  int
	} {
		t.Helper()
		w := httptest.NewRecorder()
		a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", "/release-library?browse=1&"+query, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, secret := range []string{"secret-id", "private.test", "secret-header", "payload", "source_signature"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("leak", secret)
			}
		}
		var out struct {
			Items []browseItem
			Total int
			Page  int
		}
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := request("mode=media"); got.Total != 3 {
		t.Fatalf("media %+v", got)
	}
	if got := request("mode=media&q=original"); got.Total != 1 || got.Items[0].Title != "Le film" {
		t.Fatalf("title search %+v", got)
	}
	for _, category := range []string{"movie", "show", "anime"} {
		if got := request("category=" + category); got.Total != 1 {
			t.Fatalf("%s %+v", category, got)
		}
	}
	if got := request("mode=releases&q=unique"); got.Total != 4 {
		t.Fatalf("dedup %+v", got)
	}
	if got := request("mode=releases&media=" + core.ShowKey(2).String()); got.Total != 2 {
		t.Fatalf("all episodes %+v", got)
	}
	if got := request("mode=releases&q=absent"); got.Total != 0 {
		t.Fatal(got)
	}
	if _, err = db.Exec(`UPDATE pse_release_library SET first_seen=100,last_seen=200 WHERE title_key=?`, int64(core.MovieKey(1))); err != nil {
		t.Fatal(err)
	}
	if got := request("category=movie&from=100&to=101"); got.Total != 1 {
		t.Fatal(got)
	}
	if got := request("category=movie&from=101"); got.Total != 0 {
		t.Fatal(got)
	}
	if got := request("category=movie&date=lastSeen&from=200&to=201"); got.Total != 1 {
		t.Fatal(got)
	}
	if got := request("category=movie&to=100"); got.Total != 0 {
		t.Fatal(got)
	}
	// Metadata-only rows keep their display fields without encrypted descriptors.
	if _, err = db.Exec(`UPDATE pse_releases SET source_name='Metadata',languages='fr,en',debrid_cached='alldebrid' WHERE id=?`, meta.ID); err != nil {
		t.Fatal(err)
	}
	if got := request("mode=releases&q=metadata"); got.Total != 1 || got.Items[0].Release.State != "metadata" || len(got.Items[0].Release.Languages) != 2 {
		t.Fatal(got)
	}
	if _, err = db.Exec(`UPDATE pse_releases SET source_name='Source' WHERE id=?`, meta.ID); err != nil {
		t.Fatal(err)
	}
	// SQL metacharacters are literal search text, not query syntax.
	if got := request("q=%25%27"); got.Total != 0 {
		t.Fatal(got)
	}
	for _, query := range []string{"category=bad", "mode=bad", "from=oops", "from=200&to=100", "date=bad"} {
		w := httptest.NewRecorder()
		a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", "/release-library?browse=1&"+query, nil))
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	if _, err = db.Exec(`UPDATE pse_release_library SET last_seen=? WHERE title_key=?`, time.Now().Unix(), int64(core.MovieKey(1))); err != nil {
		t.Fatal(err)
	}
	// Pagination is across the whole index, with stable order for equal timestamps.
	for i := int64(10); i < 65; i++ {
		a.rememberReleases(ctx, &core.Title{Key: core.MovieKey(i)}, []core.Release{{ID: "secret-id", Name: "Page", Indexer: "Source"}})
	}
	first, second := request("mode=releases"), request("mode=releases&page=1")
	if len(first.Items) != 50 || len(second.Items) != 9 {
		t.Fatal(len(first.Items), len(second.Items))
	}
	seen := map[string]bool{}
	for _, item := range first.Items {
		seen[item.Key] = true
	}
	for _, item := range second.Items {
		if seen[item.Key] {
			t.Fatal("page overlap", item.Key)
		}
	}
	if got := request("mode=releases&page=999"); got.Page != 1 {
		t.Fatal(got.Page)
	}
}

// Representative metadata volume for the index browser; no source requests.
func BenchmarkReleaseBrowseLargeIndex(b *testing.B) {
	db := store.OpenTest(b)
	cfg, err := config.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	a := &App{db: db, cfg: cfg, log: slog.Default()}
	b.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	_, err = db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1500000)
 INSERT INTO pse_releases(id,title_key,media_type,source_id,source_name,name,first_seen,last_seen)
 SELECT CAST(x AS TEXT),1000000000000000+(x%43000),'movie','source','Source','Film.'||x,1794800000+x,1794800000+x FROM n`)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", "/release-library?browse=1&mode=releases", nil))
		if w.Code != 200 {
			b.Fatal(w.Code, w.Body.String())
		}
	}
}
