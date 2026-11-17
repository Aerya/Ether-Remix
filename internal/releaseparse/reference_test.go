package releaseparse

import (
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
	"testing"
)

func TestReferenceNeedsActualSourceLocator(t *testing.T) {
	for _, tc := range []struct {
		name          string
		r             core.Release
		valid, stable bool
	}{
		{"identifier only", core.Release{ID: "source-search-result", Protocol: core.ProtocolTorrent}, false, false},
		{"hash", core.Release{ID: "r", Protocol: core.ProtocolTorrent, InfoHash: strings.Repeat("a", 40)}, true, true},
		{"contradictory hashes", core.Release{ID: "r", Protocol: core.ProtocolTorrent, InfoHash: strings.Repeat("a", 40), MagnetURL: "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)}, false, false},
		{"invalid hash", core.Release{ID: "r", Protocol: core.ProtocolTorrent, InfoHash: "abc"}, false, false},
		{"magnet", core.Release{ID: "r", Protocol: core.ProtocolTorrent, MagnetURL: "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)}, true, true},
		{"signed HTTP", core.Release{ID: "r", Protocol: core.ProtocolHTTP, StreamURL: "https://source.invalid/file?expires=100"}, true, false},
		{"AllDebrid file", core.Release{ID: "r", Protocol: core.ProtocolHTTP, StreamURL: "https://alldebrid.com/f/test", StreamDebrid: "alldebrid"}, true, true},
		{"NZB", core.Release{ID: "r", Protocol: core.ProtocolUsenet, NZBURL: "https://source.invalid/get/123"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (ReferenceReason(tc.r) == "") != tc.valid || StableReference(tc.r) != tc.stable {
				t.Fatalf("reason=%s stable=%v", ReferenceReason(tc.r), StableReference(tc.r))
			}
		})
	}
}
