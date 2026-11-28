package releasedb

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
	"strings"
	"testing"
	"time"
)

func TestLibraryEncryptedReferencesLifecycle(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	l := Library{Store: NewStore(db), Signature: "source-a", Now: func() time.Time { return now }, Seal: func(s string) (string, error) { return cfg.Seal("test-library", []byte(s)) }, Open: func(s string) (string, error) { b, e := cfg.Unseal("test-library", s); return string(b), e }}
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "off"}))
	key := core.EpisodeKey(42, 2, 3)
	r := core.Release{ID: "one", Name: "Film.2020.FRENCH.1080p", Protocol: core.ProtocolHTTP, StreamURL: "https://example.test/secret-file-token", StreamHeaders: map[string]string{"Authorization": "private-header"}, PubDate: now.Add(-24 * time.Hour), Info: core.ReleaseInfo{Height: 1080}}
	if err = l.Save(ctx, key, []core.Release{r}); err != nil {
		t.Fatal(err)
	}
	entries, _ := l.Entries(ctx, key)
	if len(entries) != 1 {
		t.Fatal("legacy off setting must not disable reference collection")
	}
	restore := pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"})
	defer restore()
	if err = l.Save(ctx, key, []core.Release{r}); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = db.QueryRow(`SELECT payload FROM pse_release_library`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "secret-file-token") || strings.Contains(payload, "private-header") {
		t.Fatal("plaintext descriptor stored")
	}
	got := l.Candidates(ctx, key)
	if len(got) != 1 || got[0].StreamHeaders["Authorization"] != "private-header" {
		t.Fatal("descriptor lost", got)
	}
	if len(l.Candidates(ctx, core.EpisodeKey(42, 2, 4))) != 0 {
		t.Fatal("cross-episode leak")
	}
	l.Signature = "changed"
	if len(l.Candidates(ctx, key)) != 0 {
		t.Fatal("old source reused")
	}
	l.Signature = "source-a"
	now = now.Add(2 * time.Hour)
	if len(l.Candidates(ctx, key)) != 0 {
		t.Fatal("expired HTTP link reused")
	}
	r.StreamDebrid = "alldebrid"
	r.StreamURL = "https://alldebrid.com/f/example"
	r.StreamOnDemand = true
	if err = l.Save(ctx, key, []core.Release{r}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(90 * 24 * time.Hour)
	got = l.Candidates(ctx, key)
	if len(got) != 1 || got[0].StreamDebrid != "alldebrid" || !got[0].StreamOnDemand {
		t.Fatal("Alko routing lost")
	}
	l.Invalidate(ctx, r.ID)
	if len(l.Candidates(ctx, key)) != 0 {
		t.Fatal("failed candidate reused")
	}
	entries, _ = l.Entries(ctx, key)
	if len(entries) != 1 || !entries[0].Failed {
		t.Fatal("failure missing from inspector")
	}
}

func TestLibrarySFRImportPreservesVerifiedFile(t *testing.T) {
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	e := newFreshEnv(t)
	key := core.MovieKey(42)
	sfrCatalog(t, e, "movie", key)
	var got []core.Release
	e.s.d.Remember = func(_ context.Context, k core.RatingKey, rs []core.Release) {
		if k != key {
			t.Fatal(k)
		}
		got = append(got, rs...)
	}
	_, err := e.s.sfrSave(e.ctx, []sfrRow{{ID: "file", Alko: true, Name: "Film.2020.FRENCH.1080p", Size: 1000, TMDB: 42, Media: "movie", AlkoURL: "https://alldebrid.com/f/test"}})
	if err != nil || len(got) != 1 || got[0].StreamDebrid != "alldebrid" || got[0].MagnetURL != "" {
		t.Fatal(err, got)
	}
}

func TestLibraryPreservesSameHashAcrossSources(t *testing.T) {
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	identity := func(s string) (string, error) { return s, nil }
	l := Library{Store: NewStore(store.OpenTest(t)), Seal: identity, Open: identity, Signature: "sources"}
	rs := []core.Release{{ID: "same-hash", IndexerID: "a", Protocol: core.ProtocolTorrent, InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {ID: "same-hash", IndexerID: "b", Protocol: core.ProtocolTorrent, InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	if err := l.Save(t.Context(), core.MovieKey(42), rs); err != nil {
		t.Fatal(err)
	}
	if got := l.Candidates(t.Context(), core.MovieKey(42)); len(got) != 2 {
		t.Fatal("source alternative lost", got)
	}
}

func TestIndexReferencesIgnoreLegacySwitch(t *testing.T) {
	for _, value := range []string{"", "off", "on"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PSE_RELEASE_LIBRARY", value)
			t.Cleanup(pseenv.Replace(map[string]string{"PSE_INDEXER": "off", "PSE_RELEASE_LIBRARY": value}))
			if !LibraryEnabled() {
				t.Fatal("reference retention must remain enabled")
			}
			if LoadSettings().Enabled {
				t.Fatal("migration must not start background searches")
			}
		})
	}
}

// La purge des références périmées ne tourne qu'une fois par libraryRetentionEvery
// et par base : un Save par titre ne doit pas balayer toute la table à chaque fois.
func TestLibraryRetentionThrottled(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Now()
	l := Library{Store: NewStore(db), Signature: "s", Now: func() time.Time { return now }, Seal: func(s string) (string, error) { return s, nil }}
	expired := func(id string) {
		t.Helper()
		old := now.Add(-100 * 24 * time.Hour).Unix()
		if _, err := db.Exec(`INSERT INTO pse_release_library(title_key,release_id,source_id,source_signature,payload,first_seen,last_seen,expires) VALUES(7,?,'src','s','p',?,?,?)`, id, old, old, old); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pse_release_library WHERE title_key=7`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	save := func(id string) {
		t.Helper()
		if err := l.Save(ctx, core.MovieKey(1), []core.Release{{ID: id, Name: "Film.2020.FRENCH.1080p", Protocol: core.ProtocolHTTP}}); err != nil {
			t.Fatal(err)
		}
	}
	expired("a")
	save("one") // première purge de cette base : due
	if count() != 0 {
		t.Fatal("référence périmée non purgée au premier Save")
	}
	expired("b")
	save("two") // moins de dix minutes plus tard : pas de balayage
	if count() != 1 {
		t.Fatal("la purge a tourné à chaque Save")
	}
	now = now.Add(libraryRetentionEvery + time.Minute)
	save("three")
	if count() != 0 {
		t.Fatal("référence périmée non purgée après l'intervalle")
	}
}
