package app

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func TestSFRRuntimeSourceIdentityAndDisable(t *testing.T) {
	cfg := map[string]string{"PSE_SFR": "on", "PSE_SFR_URL": "postgres://reader:test-secret@db/library"}
	restore := pseenv.Replace(cfg)
	defer restore()
	base := []config.Indexer{{ID: "existing"}}
	rows := sfrIndexers(base)
	if len(rows) != 2 || rows[1].ID != sfrSearchID || !rows[1].Enabled || strings.Contains(rows[1].URL, "test-secret") {
		t.Fatal(rows)
	}
	first := rows[1].URL
	restore2 := pseenv.Replace(map[string]string{"PSE_SFR": "on", "PSE_SFR_URL": "postgres://reader:different-secret@db/library"})
	if sfrIndexers(base)[1].URL == first {
		t.Fatal("credentials must invalidate search cache")
	}
	restore2()
	restore3 := pseenv.Replace(map[string]string{"PSE_SFR": "off"})
	defer restore3()
	if len(sfrIndexers(base)) != 1 {
		t.Fatal("disabled source remains registered")
	}
	if len(base) != 1 {
		t.Fatal("mutated original configuration")
	}
}
