package app

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func TestXDCCRuntimeSourceRowAndDisable(t *testing.T) {
	const key = "test-xdcc-secret"
	restore := pseenv.Replace(map[string]string{"PSE_XDCC": "on", "PSE_XDCC_URL": "http://xdcc-grabscher:5556", "PSE_XDCC_KEY": key, "PSE_XDCC_SEARCH_DAILY": "500"})
	defer restore()
	base := []config.Indexer{{ID: "existing"}}
	rows := xdccIndexers(base)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	r := rows[1]
	if r.ID != xdccSourceID || !r.Enabled || r.Kind != config.IndexerKindAddon || r.DailyLimit != 500 || strings.Contains(r.URL, key) || strings.Contains(r.URL, "xdcc-grabscher") {
		t.Fatalf("ligne XDCC : %+v", r)
	}
	first := r.URL
	restore2 := pseenv.Replace(map[string]string{"PSE_XDCC": "on", "PSE_XDCC_URL": "http://xdcc-grabscher:5556", "PSE_XDCC_KEY": "other"})
	if xdccIndexers(base)[1].URL == first {
		t.Fatal("une autre clé doit séparer le cache des recherches")
	}
	restore2()
	for _, cfg := range []map[string]string{
		{"PSE_XDCC": "off", "PSE_XDCC_URL": "http://x:5556", "PSE_XDCC_KEY": key},    // désactivé
		{"PSE_XDCC": "on", "PSE_XDCC_URL": "http://x:5556"},                          // sans clé
		{"PSE_XDCC": "on", "PSE_XDCC_URL": "http://u:p@x:5556", "PSE_XDCC_KEY": key}, // identifiants dans l'adresse
		{}, // par défaut
	} {
		r := pseenv.Replace(cfg)
		if len(xdccIndexers(base)) != 1 {
			t.Fatalf("source inscrite avec %v", cfg)
		}
		r()
	}
	if len(base) != 1 {
		t.Fatal("configuration d'origine modifiée")
	}
}

func TestXDCCBackendWithoutServiceFindsNothing(t *testing.T) {
	a := &App{}
	rels, err := xdccBackend{a}.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "X"}, nil)
	if err != nil || len(rels) != 0 {
		t.Fatal(rels, err)
	}
}

func TestValidPacket(t *testing.T) {
	if !validPacket("00000000-0000-4000-8000-000000000001") || validPacket("../../etc") || validPacket("") {
		t.Fatal("contrôle du paquet")
	}
}
