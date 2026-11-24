package music

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/peerclient"
	"testing"
)

func TestPeerMusicIdentity(t *testing.T) {
	track := Track{Title: "A Song", Artist: Artist{Name: "Example Artist"}, Album: Album{Title: "An Album"}}
	for _, tc := range []struct {
		file string
		want bool
	}{
		{`Example Artist\An Album\01 - A Song.flac`, true},
		{`Example Artist\An Album\01 - A Song Live.flac`, false},
		{`Other Artist\An Album\01 - A Song.flac`, false},
		{`Example Artist\Other Album\01 - A Song.flac`, false},
		{`Example Artist\An Album\01 - Another Song.flac`, false},
		{`Example Artist\An Album\01 - A Song.zip`, false},
		{`A Song.flac`, false},
	} {
		h := peerclient.Hit{Name: tc.file, Filename: tc.file}
		if got := peerMatches(h, track); got != tc.want {
			t.Errorf("%s: %v", tc.file, got)
		}
	}
}

type peerQueueResolver struct {
	called *int
	queued bool
}

func (r peerQueueResolver) Resolve(context.Context, Track) (Stream, error) {
	*r.called++
	if r.queued {
		return Stream{}, core.ErrDownloadQueued
	}
	return Stream{}, ErrNotFound
}
func TestPeerQueuedMusicStopsFallback(t *testing.T) {
	first, next := 0, 0
	rs := Resolvers{peerQueueResolver{&first, true}, peerQueueResolver{&next, false}}
	_, err := rs.Resolve(t.Context(), Track{})
	if !errors.Is(err, core.ErrDownloadQueued) || first != 1 || next != 0 {
		t.Fatalf("%v, calls %d/%d", err, first, next)
	}
}
