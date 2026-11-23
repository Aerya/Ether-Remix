package debrid

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// TestHTTPSourceDirectURL covers the source-layer half of direct delivery
// (plex.delivery = "redirect"): which route a player may be sent to in
// Ether's place, and what it is handed. A link Ether itself would refuse to
// follow, or one the engine is about to regenerate, must not leave the
// process — a player has no way to ask for a new one.
func TestHTTPSourceDirectURL(t *testing.T) {
	// A provider-signed link: its query is part of the signature, so it
	// carries an escaped character the answer must not re-encode.
	const (
		signedURL  = "https://x-cdn.torbox.to/dl/9182/Movie.2024.1080p.mkv?r=2&token=a%2Bb%3Dc"
		signedEdge = "x-cdn.torbox.to"
		providerID = "torbox-0" // Payload.Provider holds the config id, not the label
	)

	// DirectURL asks the engine's service whether the link is still fresh,
	// so the file needs a live Engine over a real Service (its own clock).
	h := newHarness(t, 1<<20, config.DebridTorBox)
	eng := NewEngine(h.svc, EngineOptions{Inner: &fakeInner{}, Cache: newMemCache(), HedgeDelay: 0, BlockSize: testBlock, Logger: h.svc.log})
	t.Cleanup(func() { eng.Close() })

	fresh := time.Now().Add(time.Hour)

	cases := []struct {
		name string
		pl   Payload
		// closed is the file's released flag, read under f.mu like pl is.
		closed bool
		want   bool
		// edge is the expected Edge host of an accepted link.
		edge string
	}{
		{
			name: "torbox link on the route it was minted onto",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: signedURL, Expires: fresh},
			want: true, edge: signedEdge,
		},
		{
			// Service.fresh treats a payload with no recorded expiry as
			// usable, and DirectURL reports that back rather than inventing one.
			name: "no recorded expiry",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: signedURL},
			want: true, edge: signedEdge,
		},
		{
			name: "expiry beyond the engine's staleness margin",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: signedURL, Expires: time.Now().Add(2 * linkMargin)},
			want: true, edge: signedEdge,
		},
		{
			// The margin is the engine's own: inside it the link is one the
			// engine is about to regenerate, and the player would keep the old one.
			name: "expiry inside the engine's staleness margin",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: signedURL, Expires: time.Now().Add(linkMargin / 2)},
		},
		{
			name: "expired signature",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: signedURL, Expires: time.Now().Add(-time.Hour)},
		},
		{
			// Ether reads a replay edge with redirects refused; a player sent
			// there reaches a host Ether itself would not follow.
			name: "link stood on a replay edge",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: signedURL, Expires: fresh, OnReplayEdge: true},
		},
		{
			name: "other debrid provider",
			pl:   Payload{Kind: config.DebridRealDebrid, Provider: providerID, URL: signedURL, Expires: fresh},
		},
		{
			// A direct HTTP stream is read through a safefetch guard with the
			// addon's own request headers.
			name: "addon HTTP stream",
			pl:   Payload{Kind: KindHTTP, Provider: providerID, URL: signedURL, Expires: fresh},
		},
		{
			name: "plugin provider",
			pl:   Payload{Kind: "plugin:torbox", Provider: providerID, URL: signedURL, Expires: fresh},
		},
		{
			name: "kind unset",
			pl:   Payload{Provider: providerID, URL: signedURL, Expires: fresh},
		},
		{
			name: "provider id unset",
			pl:   Payload{Kind: config.DebridTorBox, URL: signedURL, Expires: fresh},
		},
		{
			name: "plain http link",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: "http://x-cdn.torbox.to/dl/9182/Movie.mkv", Expires: fresh},
		},
		{
			// Credentials in the target would travel to the player's request.
			name: "link carries userinfo",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: "https://user@x-cdn.torbox.to/dl/9182/Movie.mkv", Expires: fresh},
		},
		{
			name: "link does not parse",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: "https://x-cdn torbox.to/dl/9182", Expires: fresh},
		},
		{
			name: "link empty",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, Expires: fresh},
		},
		{
			name: "link has no host",
			pl:   Payload{Kind: config.DebridTorBox, Provider: providerID, URL: "https://", Expires: fresh},
		},
		{
			name:   "file already released",
			pl:     Payload{Kind: config.DebridTorBox, Provider: providerID, URL: signedURL, Expires: fresh},
			closed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &remoteFile{e: eng, pl: tc.pl, closed: tc.closed}
			got, ok := (&httpSource{f: f}).DirectURL()
			if ok != tc.want {
				t.Fatalf("DirectURL() ok = %v, want %v (link %+v)", ok, tc.want, got)
			}
			if !tc.want {
				if got != (core.DirectSourceLink{}) {
					t.Errorf("refused link = %+v, want the zero value", got)
				}
				return
			}
			want := core.DirectSourceLink{URL: tc.pl.URL, Provider: "TorBox", Edge: tc.edge, Expires: tc.pl.Expires}
			if got != want {
				t.Errorf("DirectURL() = %+v, want %+v", got, want)
			}
		})
	}
}
