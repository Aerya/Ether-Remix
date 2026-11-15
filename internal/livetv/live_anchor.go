package livetv

import (
	"context"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// LiveWindow reads only an already published output. Authorization belongs to
// the session broker; this is not a public service endpoint.
func (s *Service) LiveWindow(ctx context.Context, id string) (frontend.LiveWindow, error) {
	s.mu.Lock()
	ss := s.sessions[id]
	s.mu.Unlock()
	if ss == nil {
		return frontend.LiveWindow{}, core.ErrNotFound
	}
	ss.mu.Lock()
	lease, owned, closed := ss.broadcast, ss.owned, ss.closed
	ss.mu.Unlock()
	if closed {
		return frontend.LiveWindow{}, core.ErrNotFound
	}
	if lease == nil {
		if owned == nil {
			return frontend.LiveWindow{}, core.ErrNotAvailable
		}
		owned.tl.mu.Lock()
		defer owned.tl.mu.Unlock()
		return owned.tl.liveWindowLocked()
	}
	index, err := lease.output()
	if err != nil {
		return frontend.LiveWindow{}, err
	}
	latest, ok := index.Latest()
	if !ok {
		return frontend.LiveWindow{}, core.ErrNotAvailable
	}
	end := latest.Start.Add(latest.Duration)
	segments := index.Window(end.Add(-s.latencyPolicy().PlaylistWindow()), end)
	return broadcastLiveWindow(ctx, lease, index, segments)
}

func broadcastLiveWindow(ctx context.Context, lease *broadcastLease, index transcode.SegmentIndex, segments []transcode.Segment) (frontend.LiveWindow, error) {
	if len(segments) == 0 {
		return frontend.LiveWindow{}, core.ErrNotAvailable
	}
	if err := ctx.Err(); err != nil {
		return frontend.LiveWindow{}, err
	}
	// Unlike DASH startup, plan/playlist reads never wait for the origin watcher.
	// If it has not published yet, exact mapping is temporarily unavailable.
	if lease == nil {
		return frontend.LiveWindow{}, core.ErrNotAvailable
	}
	b := lease.broadcast
	b.mu.Lock()
	defer b.mu.Unlock()
	// Bind to the index that supplied these segments, rather than the lease's
	// current rendition: concurrent recovery can change that rendition.
	var r *broadcastRendition
	for _, candidate := range b.renditions {
		if candidate.encoder.Segments() == index {
			r = candidate
			break
		}
	}
	if r == nil || r.originDone == nil {
		return frontend.LiveWindow{}, core.ErrNotAvailable
	}
	select {
	case <-r.originDone:
	default:
		return frontend.LiveWindow{}, core.ErrNotAvailable
	}
	if r.originErr != nil {
		return frontend.LiveWindow{}, r.originErr
	}
	origin := r.origin
	first, last := segments[0], segments[len(segments)-1]
	return frontend.LiveWindow{Anchor: frontend.LiveAnchor{MediaSequence: first.Seq, WindowStartSeconds: first.Start.Sub(origin).Seconds()}, LiveEdgeSeconds: last.Start.Add(last.Duration).Sub(origin).Seconds()}, nil
}

func (t *ownedTimeline) liveWindowLocked() (frontend.LiveWindow, error) {
	if len(t.window) == 0 {
		return frontend.LiveWindow{}, core.ErrNotAvailable
	}
	first, last := t.window[0], t.window[len(t.window)-1]
	return frontend.LiveWindow{Anchor: frontend.LiveAnchor{MediaSequence: first.seq, WindowStartSeconds: first.start.Seconds()}, LiveEdgeSeconds: (last.start + last.dur).Seconds()}, nil
}

func writeLiveAnchor(w http.ResponseWriter, window frontend.LiveWindow) {
	a := window.Anchor
	// JavaScript clients require an exactly representable sequence number.
	if a.MediaSequence < 0 || a.MediaSequence > 1<<53-1 {
		return
	}
	w.Header().Set("X-Ether-Live-Media-Sequence", strconv.FormatInt(a.MediaSequence, 10))
	w.Header().Set("X-Ether-Live-Window-Start", strconv.FormatFloat(a.WindowStartSeconds, 'f', 9, 64))
	w.Header().Add("Access-Control-Expose-Headers", "X-Ether-Live-Media-Sequence, X-Ether-Live-Window-Start")
}
