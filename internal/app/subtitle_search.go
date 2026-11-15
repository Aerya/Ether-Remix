package app

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/subtitles"
)

type subtitleHashReader struct {
	ctx  context.Context
	read func(context.Context, []byte, int64) (int, error)
}

func (r subtitleHashReader) ReadAt(p []byte, off int64) (int, error) { return r.read(r.ctx, p, off) }

// subtitleSearchSource uses only this viewer/device's already-playing file.
// Hashing has a 200 ms total budget, two 64 KiB reads, and no read-ahead.
func (s subtitleSurface) subtitleSearchSource(ctx context.Context, t *core.Title, u *core.User, device, fallback string) (string, string, int64) {
	if t == nil || u == nil || s.app.playback == nil || !s.Enabled() {
		return fallback, "", 0
	}
	ps := s.app.playSessionOn(s.surface, u.ID, device, t.Key)
	if ps == nil {
		return fallback, "", 0
	}
	if ps.Resolution != nil {
		fallback = ps.Resolution.Release.Name
	}
	bounded, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	revision := core.SourceRevision(ps)
	lease, err := s.app.playback.LeaseSubtitleSource(bounded, ps.ID, revision)
	if err != nil {
		return fallback, "", 0
	}
	defer lease.Close()
	stop := context.AfterFunc(lease.Context, cancel)
	defer stop()
	name, size := lease.SearchFile()
	if name == "" {
		return fallback, "", 0
	}
	if !s.app.subtitles.SearchNeedsHash() {
		return name, "", 0
	}
	hash, cachedSize := s.app.subtitles.CachedVideoHash(t, revision)
	if hash != "" {
		return name, hash, cachedSize
	}
	hash, err = subtitles.MovieHash(subtitleHashReader{ctx: bounded, read: lease.ReadSearchAt}, size)
	if err != nil || bounded.Err() != nil {
		return name, "", 0
	}
	s.app.subtitles.RememberVideoHash(t.Key, revision, hash, size)
	return name, hash, size
}
