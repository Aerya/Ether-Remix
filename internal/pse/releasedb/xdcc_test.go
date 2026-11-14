package releasedb

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestXDCCReleasesCountOnlyWhenSeenRecentlyAndNeverAsCached(t *testing.T) {
	now := time.Now()
	fresh := Release{SourceID: XDCCSourceID, LastSeen: now.Add(-time.Hour)}
	old := Release{SourceID: XDCCSourceID, LastSeen: now.Add(-72 * time.Hour)}
	other := Release{SourceID: "stremio-x", LastSeen: now.Add(-72 * time.Hour)}
	if !counts(fresh, now) || counts(old, now) || !counts(other, now) {
		t.Fatal("règle de fraîcheur XDCC")
	}
	// Sans la règle propre à la source, un flux HTTP sans marque compterait
	// comme « direct » (en cache) : la source XDCC l'écarte dans l'indexeur.
	if got := cachedHints(core.Release{Protocol: core.ProtocolHTTP}); len(got) != 1 || got[0] != "direct" {
		t.Fatal(got)
	}
}
