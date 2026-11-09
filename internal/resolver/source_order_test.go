package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSourceOrderRanksEquivalentReleases(t *testing.T) {
	movie := &core.Title{Type: core.TypeMovie}
	rc := rankContext{
		title: movie, preferSource: PreferDebrid,
		priority:     map[string]int{"tracker-a": 0, "tracker-b": 1, "nzb": 2, "addon": 3},
		familyOf:     map[string]string{"tracker-a": "torrent", "tracker-b": "torrent", "nzb": "usenet", "addon": "stremio"},
		families:     []string{"stremio", "usenet", "torrent"},
		contentOrder: map[string][]string{"movie": {"tracker-b"}},
	}
	keys := func(id, protocol string) rankKeys {
		return keysFor(core.Release{IndexerID: id, Protocol: protocol}, false, rc)
	}
	addon, nzb, a, b := keys("addon", core.ProtocolTorrent), keys("nzb", core.ProtocolUsenet), keys("tracker-a", core.ProtocolTorrent), keys("tracker-b", core.ProtocolTorrent)
	if addon.family != 0 || nzb.family != 1 || a.family != 2 {
		t.Fatalf("familles : stremio=%d usenet=%d torrent=%d", addon.family, nzb.family, a.family)
	}
	if addon.protocol || a.protocol {
		t.Fatal("l'ordre des familles doit remplacer la préférence de protocole")
	}
	if b.priority >= a.priority {
		t.Fatalf("l'ordre par contenu doit passer avant la priorité globale : b=%d a=%d", b.priority, a.priority)
	}

	rc.title = &core.Title{Type: core.TypeShow}
	if got := keys("tracker-b", core.ProtocolTorrent).priority; got != 1 {
		t.Fatalf("sans ordre pour les séries, la priorité globale s'applique : %d", got)
	}
	rc.title = &core.Title{Type: core.TypeShow, Keywords: []string{"anime"}}
	if got := contentKind(rc.title); got != "anime" {
		t.Fatalf("contenu anime attendu : %q", got)
	}
}

func TestSourceOrderQualityStaysFirst(t *testing.T) {
	better := candidate{keys: rankKeys{position: 2, family: 2}}
	preferred := candidate{keys: rankKeys{position: 1, family: 0}}
	if !compareCandidates(better, preferred) {
		t.Fatal("la qualité doit rester devant la famille préférée")
	}
	if !compareCandidates(candidate{keys: rankKeys{position: 1, family: 0}}, candidate{keys: rankKeys{position: 1, family: 1}}) {
		t.Fatal("à qualité égale, la famille préférée doit passer devant")
	}
}
