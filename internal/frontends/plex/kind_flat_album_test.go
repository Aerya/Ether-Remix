package plex

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// TestKindTypeForBaseFlatAlbum proves a single-level artist-base kind lists
// its flat playable items as albums (never artists), and the item's own
// playable incarnation as a track.
func TestKindTypeForBaseFlatAlbum(t *testing.T) {
	flat := &frontend.Title{Type: frontend.TypeMovie, KindLevel: "album"}
	if got := kindTypeForBase(plexBaseArtist, string(flat.Type), flat); got != plexBaseAlbum {
		t.Fatalf("named album level = %q, want album", got)
	}
	unnamed := &frontend.Title{Type: frontend.TypeMovie, KindLevel: "release"}
	if got := kindTypeForBase(plexBaseArtist, string(unnamed.Type), unnamed); got != plexBaseAlbum {
		t.Fatalf("flat leaf = %q, want album", got)
	}
	self := &frontend.Title{Type: frontend.TypeMovie, KindLevel: "album", SelfChild: true}
	if got := kindTypeForBase(plexBaseArtist, string(self.Type), self); got != plexBaseTrack {
		t.Fatalf("self child = %q, want track", got)
	}
	// The three-level hierarchy is unchanged.
	root := &frontend.Title{Type: frontend.TypeShow, KindLevel: "artist"}
	if got := kindTypeForBase(plexBaseArtist, string(root.Type), root); got != plexBaseArtist {
		t.Fatalf("artist root = %q, want artist", got)
	}
}
