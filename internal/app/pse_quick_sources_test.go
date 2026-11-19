package app

import (
	"github.com/braedonsaunders/ether/internal/config"
	"testing"
)

func TestQuickSourceInventory(t *testing.T) {
	sources := []config.Indexer{
		{ID: sfrSearchID, Enabled: true, Kind: config.IndexerKindAddon, Protocol: "torrent"},
		{ID: "news", Enabled: true},
		{ID: "tracker", Enabled: true, Protocol: "torrent"},
		{ID: "seed", Enabled: true, Kind: config.IndexerKindStremio},
		{ID: "debrider", Enabled: true, Kind: config.IndexerKindStremio, PlaybackLastResort: true},
		{ID: "disabled", Kind: config.IndexerKindStremio},
	}
	for source, want := range map[string]string{"alko": sfrSearchID, "usenet": "news", "torrent": "tracker", "seedstrem": "seed", "addons": "debrider", "addons-rescue": "debrider"} {
		var got []string
		for _, ix := range sources {
			if quickIndexerSelected(ix, source, "seed") {
				got = append(got, ix.ID)
			}
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("%s: %v", source, got)
		}
	}
}
