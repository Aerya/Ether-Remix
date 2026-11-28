package app

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

var alkoTestKey = strings.Repeat("k", 32) // factice, jamais une vraie clé

func TestAlkoRuntimeSourceRowAndDisable(t *testing.T) {
	defer pseenv.Replace(map[string]string{"PSE_ALKO": "on", "PSE_ALKO_KEY": alkoTestKey, "PSE_ALKO_URL": "http://stream-fusion:8080"})()
	base := []config.Indexer{{ID: "existing"}}
	rows := alkoIndexers(base)
	if len(rows) != 2 || rows[1].ID != alkoSourceID || !rows[1].Enabled || rows[1].Priority != 0 {
		t.Fatal(rows)
	}
	if strings.Contains(rows[1].URL, alkoTestKey) || strings.Contains(rows[1].URL, "stream-fusion") {
		t.Fatal("ni clé ni adresse dans la ligne d'indexeur")
	}
	first := rows[1].URL
	restore := pseenv.Replace(map[string]string{"PSE_ALKO": "on", "PSE_ALKO_KEY": alkoTestKey, "PSE_ALKO_URL": "http://autre:8080"})
	if alkoIndexers(base)[1].URL == first {
		t.Fatal("une autre adresse doit invalider le cache de recherche")
	}
	restore()
	pseenv.Replace(map[string]string{"PSE_ALKO": "on"}) // clé absente : source inactive
	if len(alkoIndexers(base)) != 1 {
		t.Fatal("source sans clé enregistrée")
	}
	pseenv.Replace(map[string]string{"PSE_ALKO": "off", "PSE_ALKO_KEY": alkoTestKey})
	if len(alkoIndexers(base)) != 1 {
		t.Fatal("source désactivée enregistrée")
	}
	if len(base) != 1 {
		t.Fatal("configuration d'origine modifiée")
	}
}

func TestAlkoBackendWithoutServiceFindsNothing(t *testing.T) {
	rs, err := alkoBackend{&App{}}.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 1}}, nil)
	if err != nil || len(rs) != 0 {
		t.Fatal(rs, err)
	}
}

// Quand la source API est active, le chemin PostgreSQL des Alkodiques est ignoré
// et le mode rapide « Alkodiques » lit le miroir local.
func TestAlkoAPIReplacesPostgresPath(t *testing.T) {
	ctx := core.WithQuickSource(context.Background(), "alko", map[string]bool{sfrSearchID: true})
	t.Cleanup(func() { releasedb.SetAlkoMirrorReady(false) })
	releasedb.SetAlkoMirrorReady(true) // au moins une lecture complète réussie
	defer pseenv.Replace(map[string]string{"PSE_ALKO": "on", "PSE_ALKO_KEY": alkoTestKey, "PSE_SFR": "on", "PSE_SFR_URL": "postgres://reader:secret@127.0.0.1:1/x"})()
	rs, err := sfrBackend{}.Search(ctx, core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 1}}, func(context.Context, string) error { return nil })
	if err != nil || len(rs) != 0 {
		t.Fatalf("le chemin PostgreSQL doit être ignoré : %v %v", rs, err)
	}
	api, pg := config.Indexer{ID: alkoSourceID, Enabled: true}, config.Indexer{ID: sfrSearchID, Enabled: true}
	if !quickIndexerSelected(api, "alko", "") || quickIndexerSelected(pg, "alko", "") {
		t.Fatal("mode rapide : la source API remplace la ligne PostgreSQL")
	}
	pseenv.Replace(map[string]string{"PSE_ALKO": "off"})
	if quickIndexerSelected(api, "alko", "") || !quickIndexerSelected(pg, "alko", "") {
		t.Fatal("sans la source API, le mode rapide reste inchangé")
	}
	if releasedb.AlkoAPIEnabled() {
		t.Fatal("source API désactivée")
	}
}
