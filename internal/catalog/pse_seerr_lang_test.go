package catalog_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// Un titre que SeerrCatalog n'a trouvé qu'en VOSTFR (version de secours)
// n'entre pas dans Films : il reste réservé à la bibliothèque VOSTFR et aux
// collections SeerrCatalog. Trouvé en VF, il apparaît normalement.
func TestPSESeerrFallbackStaysOutOfMovies(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := tmdb.New("testkey", slog.Default(), tmdb.WithBaseURL(fakeTMDB(t).URL), tmdb.WithCacheTTL(0))
	t.Cleanup(client.Close)
	cfg := config.Catalog{Language: "en-US", Region: "US", MinPopularity: 5, MinVotes: 100, RefreshInterval: config.Duration(24 * time.Hour)}
	c, err := catalog.New(db, client, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	fjord, family := core.MovieKey(550), core.MovieKey(551)
	for _, k := range []core.RatingKey{fjord, family} {
		if _, err := c.Get(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(pseenv.Replace(map[string]string{
		"PSE_SEERRCATALOG": "on", "PSE_SEERRCATALOG_URL": "https://seerr.example", "PSE_SEERRCATALOG_TOKEN": strings.Repeat("t", 40),
	}))
	t.Cleanup(releasedb.OverridePlayable(releasedb.NewPlayable(2, "lang=fr,multi", false)))
	sig := seerrSignature("https://seerr.example", strings.Repeat("t", 40))
	// Fjord : VOSTFR seulement ; Family : VF.
	if _, err := db.ExecContext(ctx, `INSERT INTO pse_seerrcatalog(media_id,title_key,available,expires,signature,confirmed,fallback) VALUES(1,?,1,?,?,1,1),(2,?,1,?,?,1,0)`,
		int64(fjord), time.Now().Add(time.Hour).Unix(), sig, int64(family), time.Now().Add(time.Hour).Unix(), sig); err != nil {
		t.Fatal(err)
	}
	movies := func(f core.Filter) []core.RatingKey {
		t.Helper()
		c, err := catalog.New(db, client, cfg, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		f.Type = core.TypeMovie
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Sort: core.SortPopularity, Desc: true})
		if err != nil {
			t.Fatal(err)
		}
		var keys []core.RatingKey
		for _, it := range page.Items {
			keys = append(keys, it.Key)
		}
		return keys
	}
	if got := movies(core.Filter{}); len(got) != 1 || got[0] != family {
		t.Fatalf("Films : %v (seul le titre en VF attendu)", got)
	}
	both := []core.RatingKey{fjord, family}
	if got := movies(core.Filter{IncludeKeys: both, PSEThemed: true, PSELangFree: true}); len(got) != 2 {
		t.Fatalf("bibliothèque VOSTFR : %v", got)
	}
	vf, fallback, err := seerrcatalog.Keys(ctx, db)
	if err != nil || len(vf) != 1 || vf[0] != family || len(fallback) != 1 || fallback[0] != fjord {
		t.Fatalf("Keys = %v %v %v", vf, fallback, err)
	}
}

// seerrSignature reproduit l'empreinte des réglages SeerrCatalog actifs.
func seerrSignature(url, token string) string {
	h := sha256.Sum256([]byte(url + "\x00" + token))
	return hex.EncodeToString(h[:])
}
