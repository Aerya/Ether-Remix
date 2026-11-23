package livetv

import (
	"errors"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
)

// ServeContinuous writes a live session as one continuous MPEG-TS response,
// starting at the live edge, for IPTV apps that request a ".ts" stream rather
// than an HLS playlist. Each segment is the same one HLS clients receive, so
// sharing, policy and source failover are unchanged. It reports false, having
// written nothing, when the session's media is not MPEG-TS (fMP4 timelines);
// the caller then serves the HLS playlist instead.
func (s *Service) ServeContinuous(w http.ResponseWriter, r *http.Request, id string, u *core.User) bool {
	ss, err := s.getSession(r.Context(), id, u)
	if err != nil {
		writeStreamError(w, err)
		return true
	}
	ss.mu.Lock()
	shared := ss.broadcast != nil
	lease := ss.owned
	ss.mu.Unlock()
	if shared {
		s.serveBroadcastContinuous(w, r, ss)
		return true
	}
	if lease == nil || lease.tl == nil {
		writeStreamError(w, core.ErrNotFound)
		return true
	}
	tl := lease.tl
	if err := tl.waitStarted(r.Context()); err != nil {
		writeStreamError(w, err)
		return true
	}
	if !tl.tsOnly() {
		return false
	}
	tl.mu.Lock()
	seq := tl.nextSeq - 1
	if n := len(tl.window); n > 0 {
		seq = tl.window[n-1].seq
	}
	tl.mu.Unlock()
	if seq < 0 {
		seq = 0
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	observed := s.observeContinuousMedia(w, ss)
	defer observed.done()
	observed.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return true
	}
	ss.activeReads.Add(1)
	defer ss.activeReads.Add(-1)
	for {
		seg, err := tl.segment(r.Context(), seq)
		if errors.Is(err, core.ErrNotFound) && r.Context().Err() == nil && !ss.isClosed() {
			// The producer is between segments or restarting; keep the edge.
			tl.mu.Lock()
			behind := seq < tl.nextSeq-int64(len(tl.window))
			if behind && len(tl.window) > 0 {
				seq = tl.window[0].seq
			}
			closed := tl.closed
			tl.mu.Unlock()
			if closed {
				return true
			}
			continue
		}
		if err != nil || seg.ext != "ts" {
			return true
		}
		s.touchMedia(ss)
		armWriteDeadline(w)
		if _, err := observed.Write(seg.data); err != nil {
			return true
		}
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		seq = seg.seq + 1
	}
}

func (s *Service) serveBroadcastContinuous(w http.ResponseWriter, r *http.Request, ss *session) {
	index, err := s.broadcastSegments(r.Context(), ss)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	after := int64(-1)
	if latest, ok := index.Latest(); ok {
		after = latest.Seq - 1
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	observed := s.observeContinuousMedia(w, ss)
	defer observed.done()
	observed.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	ss.activeReads.Add(1)
	defer ss.activeReads.Add(-1)
	for {
		seg, err := index.Wait(r.Context(), after)
		if err != nil || !s.broadcastSegmentAllowed(r.Context(), ss, seg) || ss.isClosed() {
			return
		}
		s.touchMedia(ss)
		armWriteDeadline(w)
		part := r.Clone(r.Context())
		part.Header = r.Header.Clone()
		part.Header.Del("Range")
		part.Method = http.MethodGet
		index.ServeTS(&broadcastTSWriter{ResponseWriter: observed, header: make(http.Header)}, part, seg.Seq)
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		after = seg.Seq
	}
}

func (ss *session) isClosed() bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.closed || ss.ctx.Err() != nil
}
