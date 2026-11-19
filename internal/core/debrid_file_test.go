package core

import "testing"

func TestIsVerifiedFile(t *testing.T) {
	ok := Release{ID: "sfr-alko:1", GUID: "sfr-alko:1", Protocol: ProtocolHTTP, StreamDebrid: "alldebrid", StreamURL: "https://alldebrid.com/f/abc_1", Size: 1 << 30}
	with := func(f func(*Release)) Release { r := ok; f(&r); return r }
	tests := []struct {
		name string
		r    Release
		want bool
	}{
		{"Alkodique", ok, true},
		{"GUID seul", with(func(r *Release) { r.ID = "x" }), true},
		{"ID rebasé par le client", with(func(r *Release) { r.ID, r.GUID = "idx:abc", "sfr-alko:1" }), true},
		{"sans préfixe", with(func(r *Release) { r.ID, r.GUID = "x", "x" }), false},
		{"lien invalide", with(func(r *Release) { r.StreamURL = "https://example.com/f/abc" }), false},
		{"autre débrideur", with(func(r *Release) { r.StreamDebrid = "realdebrid" }), false},
		{"taille inconnue", with(func(r *Release) { r.Size = 0 }), false},
		{"torrent", with(func(r *Release) { r.Protocol = ProtocolTorrent }), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsVerifiedFile(tc.r); got != tc.want {
				t.Fatalf("IsVerifiedFile = %v, attendu %v", got, tc.want)
			}
		})
	}
}
