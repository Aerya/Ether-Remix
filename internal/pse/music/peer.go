package music

import (
	"context"
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/peerclient"
)

// PeerResolver uses catalogue metadata for discovery and local completed files for playback.
type PeerResolver struct{ Service func() *peerclient.Service }

func (r PeerResolver) Resolve(ctx context.Context, t Track) (Stream, error) {
	s := r.Service()
	if s == nil {
		return Stream{}, ErrNotFound
	}
	if !core.OnDemandStreamsAllowed(ctx) || core.PSEAvailabilityCheck(ctx) {
		return Stream{}, core.ErrForegroundOnly
	}
	// Resume persisted work before discovery: search ordering and peer availability
	// must not cause another copy to be queued on each playback attempt.
	items, err := s.Items(ctx)
	if err != nil {
		return Stream{}, err
	}
	for _, item := range items {
		if item.State == "failed" || !peerMatches(item.Hit, t) {
			continue
		}
		// Keep the chosen copy even on transient failures.
		return peerStream(ctx, s, item.Hit)
	}
	hits, err := s.Search(ctx, t.Artist.Name+" "+t.Title)
	if err != nil {
		return Stream{}, err
	}
	for _, h := range hits {
		if !peerMatches(h, t) {
			continue
		}
		return peerStream(ctx, s, h)
	}
	return Stream{}, ErrNotFound
}
func peerStream(ctx context.Context, s *peerclient.Service, h peerclient.Hit) (Stream, error) {
	open, err := s.MusicOpen(ctx, h.ID)
	if err != nil {
		return Stream{}, err
	}
	return Stream{URL: "peer:" + s.Client.Settings.Kind + ":" + h.ID, Provider: s.Client.Settings.Kind, Open: open, Mime: audioExts[strings.ToLower(path.Ext(h.Name))], Quality: debridQuality(h.Name)}, nil
}

func peerMatches(h peerclient.Hit, t Track) bool {
	if !IsAudio(h.Name) || t.Artist.Name == "" || t.Album.Title == "" || t.Title == "" {
		return false
	}
	full := strings.ReplaceAll(h.Filename, `\`, "/")
	if full == "" {
		full = h.Name
	}
	base := strings.TrimSuffix(path.Base(full), path.Ext(full))
	normalized := foldWords(base)
	// Require exact artist/album path components, or an exact flat filename.
	if !sameVersion(base, t.Title) {
		return false
	}
	stripped := normalized
	if number, rest, ok := strings.Cut(normalized, " "); ok && strings.Trim(number, "0123456789") == "" {
		stripped = rest
	}
	artist, album, want := foldWords(t.Artist.Name), foldWords(t.Album.Title), foldWords(t.Title)
	if normalized == artist+" "+album+" "+want || stripped == artist+" "+album+" "+want {
		return true
	}
	artistFound, albumFound := false, false
	for _, component := range strings.Split(path.Dir(full), "/") {
		artistFound = artistFound || foldWords(component) == artist
		albumFound = albumFound || foldWords(component) == album
	}
	return albumFound && (artistFound && (normalized == want || stripped == want) || normalized == artist+" "+want || stripped == artist+" "+want)
}
