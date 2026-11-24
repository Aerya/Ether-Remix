package app

import (
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"strings"
	"testing"
)

func TestPeerIndexersConfiguration(t *testing.T) {
	restore := pseenv.Replace(map[string]string{"PSE_AMULE": "on", "PSE_AMULE_URL": "http://amule:4713", "PSE_AMULE_KEY": "fixture-password", "PSE_AMULE_ROOT": "/incoming", "PSE_SLSKD": "on", "PSE_SLSKD_URL": "http://slskd:5030", "PSE_SLSKD_KEY": "fixture-key", "PSE_SLSKD_ROOT": "/downloads"})
	defer restore()
	rows := peerIndexers([]config.Indexer{{ID: "existing"}})
	if len(rows) != 3 || rows[0].ID != "existing" {
		t.Fatalf("%+v", rows)
	}
	for _, r := range rows[1:] {
		if !r.Enabled || !strings.HasPrefix(r.ID, "remix-") || strings.Contains(r.URL, "fixture") {
			t.Fatalf("invalid indexer %+v", r)
		}
	}
}
