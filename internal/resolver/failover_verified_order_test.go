package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// L'ordre de failover garde un fichier vérifié (verifiedFiles "first") devant
// les autres sources encore non essayées.
func TestFailoverAlternatesVerifiedFileFirst(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.MaxCandidates = 5
	svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{})
	winner := remuxTorrent()
	addon := rel("addon", "The.Signal.1999.1080p.WEB-DL.H.264-ADDON", 4000)
	addon.Protocol = core.ProtocolHTTP
	vf := alkoRelease("a1", "The.Signal.1999.720p.WEB-DL.DDP5.1.H.264-ALKO", 3000)
	cands := []candidate{{rel: winner}, {rel: addon}, {rel: vf, vfFirst: true}}
	res := svc.resolutionSkipping(core.MovieKey(1), cands, 0, nil, func(candidate) bool { return false })
	if len(res.Alternates) != 2 || res.Alternates[0].ID != vf.ID || res.Alternates[1].ID != "addon" {
		t.Fatalf("alternates %+v", res.Alternates)
	}
}
