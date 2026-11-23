package livetv

import (
	"context"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// awaitBroadcastCushion prefers three target durations for virtual
// channels (and live handovers such as multiview tile replans) and one target
// duration for cold providers. Only tune waits, with a bounded budget; the
// first playlist never waits for this cushion. Returns an error if no segment
// is published before the budget expires or the broadcast stops, so callers
// never report ready before a manifest can be served.
func awaitBroadcastCushion(ctx context.Context, lease *broadcastLease, provider bool) error {
	index, err := lease.output()
	if err != nil {
		return err
	}
	segment := broadcastSegment(lease)
	targets := 1
	if !provider || core.LiveHandover(ctx) {
		// Prefer AVPlayer's three-target startup buffer at tune time, and
		// gather a full cushion during a live handover while the previous
		// rendition keeps playing on screen.
		targets = 3
	}
	firstLimit := max(12*time.Second, segment*4)
	cushionLimit := max(3*time.Second, segment*time.Duration(targets)+2*time.Second)
	if deadline, ok := ctx.Deadline(); ok {
		avail := time.Until(deadline) - 500*time.Millisecond
		firstLimit = min(firstLimit, avail)
		cushionLimit = min(cushionLimit, avail)
	}
	latest, ok := index.Latest()
	if !ok {
		if firstLimit <= 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return core.ErrNotAvailable
		}
		firstWait, cancel := context.WithTimeout(ctx, firstLimit)
		latest, err = index.Wait(firstWait, -1)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return core.ErrNotAvailable
		}
	}
	if cushionLimit > 0 {
		wait, cancel := context.WithTimeout(ctx, cushionLimit)
		// Return the first playable cold provider segment immediately. The
		// frame clock continues building the cushion while the client fetches
		// its playlist.
		first := index.Since(-1, 1)
		for len(first) > 0 && latest.Start.Add(latest.Duration).Sub(first[0].Start) < segment*time.Duration(targets) {
			if latest, err = index.Wait(wait, latest.Seq); err != nil {
				break
			}
		}
		cancel()
	}
	originWait, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	lease.broadcast.awaitRenditionOrigin(originWait, lease.rendition)
	cancel()
	if lease.broadcast.ctx.Err() != nil {
		return core.ErrNotAvailable
	}
	return nil
}

// broadcastSegment is the target duration of the output a lease reads.
func broadcastSegment(l *broadcastLease) time.Duration {
	if l == nil || l.broadcast == nil || l.broadcast.segment <= 0 {
		return segmentDuration
	}
	return l.broadcast.segment
}

func (s *Service) broadcastSegments(ctx context.Context, ss *session) (transcode.SegmentIndex, error) {
	ss.mu.Lock()
	lease := ss.broadcast
	ss.mu.Unlock()
	if lease == nil {
		return nil, core.ErrNotAvailable
	}
	index, err := lease.output()
	if err == nil {
		return index, nil
	}
	// A retained session URL can outlive its output (for example after a
	// lineup invalidation). Run rejoin under the session's lifetime so a
	// short playlist request timeout or abort never cancels a cold start.
	waitBudget := broadcastSegment(lease) / 2
	done := s.ensureBroadcastRejoin(ss, lease)
	if done == nil {
		ss.mu.Lock()
		closed := ss.closed
		lease = ss.broadcast
		ss.mu.Unlock()
		if closed {
			return nil, core.ErrNotFound
		}
		if lease == nil {
			return nil, core.ErrNotAvailable
		}
		return lease.output()
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitBudget)
	defer cancel()
	select {
	case <-done:
		ss.mu.Lock()
		closed := ss.closed
		lease = ss.broadcast
		rejoinErr := ss.rejoinErr
		ss.mu.Unlock()
		if closed {
			return nil, core.ErrNotFound
		}
		if lease != nil {
			if index, err := lease.output(); err == nil {
				return index, nil
			}
		}
		if rejoinErr != nil {
			return nil, rejoinErr
		}
		return nil, core.ErrNotAvailable
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, core.ErrNotAvailable
	}
}

func (s *Service) ensureBroadcastRejoin(ss *session, stale *broadcastLease) <-chan struct{} {
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return nil
	}
	if ss.broadcast != nil && ss.broadcast != stale {
		current := ss.broadcast
		ss.mu.Unlock()
		if _, err := current.output(); err == nil {
			return nil
		}
		ss.mu.Lock()
		if ss.closed {
			ss.mu.Unlock()
			return nil
		}
	}
	if ss.rejoinDone != nil {
		done := ss.rejoinDone
		ss.mu.Unlock()
		return done
	}
	done := make(chan struct{})
	ss.rejoinDone = done
	ss.rejoinErr = nil
	base := ss.ctx
	if base == nil {
		base = s.serviceContext()
	}
	if base == nil {
		base = context.Background()
	}
	ss.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			ss.mu.Lock()
			if ss.rejoinDone == done {
				ss.rejoinDone = nil
			}
			ss.mu.Unlock()
		}()
		ss.prepareMu.Lock()
		defer ss.prepareMu.Unlock()
		ss.mu.Lock()
		if ss.closed {
			ss.rejoinErr = core.ErrNotFound
			ss.mu.Unlock()
			return
		}
		lease := ss.broadcast
		ss.mu.Unlock()
		if lease == nil {
			ss.mu.Lock()
			ss.rejoinErr = core.ErrNotAvailable
			ss.mu.Unlock()
			return
		}
		if _, err := lease.output(); err == nil {
			return
		}
		lease.close()
		rejoinCtx, cancel := context.WithTimeout(base, 15*time.Second)
		defer cancel()
		err := s.startBroadcast(rejoinCtx, ss)
		ss.mu.Lock()
		ss.rejoinErr = err
		ss.mu.Unlock()
		if err == nil {
			s.sessionLog(ss).Info("live retained session rejoined broadcast")
		}
	}()
	return done
}
func (s *Service) broadcastWindow(ctx context.Context, ss *session, r *http.Request) (transcode.SegmentIndex, []transcode.Segment, error) {
	index, err := s.broadcastSegments(ctx, ss)
	if err != nil {
		return nil, nil, err
	}
	// AVPlayer never starts an item whose first playlist response took a
	// whole target duration, and fails it on a 503: never hold a playlist
	// for more than half a target duration. The tune already waited for the
	// output's first segment (awaitBroadcastCushion).
	ss.mu.Lock()
	provider := ss.info.Channel.Kind == "iptv"
	segment := broadcastSegment(ss.broadcast)
	ss.mu.Unlock()
	wait, cancel := context.WithTimeout(ctx, segment/2)
	defer cancel()
	latest, ok := index.Latest()
	if !ok {
		latest, err = index.Wait(wait, -1)
		if err != nil {
			// No playlist exists yet, including when a cold encoder exits before
			// publishing. Tell clients to retry rather than report a bad gateway.
			return nil, nil, core.ErrNotAvailable
		}
	}
	if provider {
		if first := index.Since(-1, 1); len(first) > 0 {
			for latest.Start.Add(latest.Duration).Sub(first[0].Start) < segment {
				next, err := index.Wait(wait, latest.Seq)
				if err != nil {
					break
				}
				latest = next
			}
		}
	}
	end := latest.Start.Add(latest.Duration)
	from := end.Add(-s.latencyPolicy().PlaylistWindow())
	if raw := r.URL.Query().Get("offset"); raw != "" {
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > broadcastDVR.Seconds() {
			return nil, nil, fmt.Errorf("invalid DVR offset")
		}
		end = end.Add(-time.Duration(seconds * float64(time.Second)))
		from = end.Add(-s.latencyPolicy().PlaylistWindow())
	}
	ss.mu.Lock()
	user := ss.user
	ss.mu.Unlock()
	ctx, err = s.broadcastPolicyContext(ctx, ss, &user)
	if err != nil {
		return nil, nil, err
	}
	segments := index.Window(from, end)
	if len(segments) == 0 {
		return nil, nil, core.ErrNotAvailable
	}

	for _, seg := range segments {
		if !s.broadcastSegmentAllowed(ctx, ss, seg) {
			return nil, nil, core.ErrUnauthorized
		}
	}
	return index, segments, nil
}

// broadcastUsesFMP4 reports whether a client requests fMP4 HLS segments and
// does not declare MPEG-TS support. Continuous broadcast encoders produce both
// TS and fMP4 from the same encode, so both viewers share one encoder.
func broadcastUsesFMP4(caps core.ClientCaps) bool {
	if len(caps.HLSContainers) == 0 {
		return false
	}
	wantsFMP4 := false
	for _, c := range caps.HLSContainers {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "ts", "mpegts":
			return false
		case "fmp4", "mp4", "m4s", "m4a":
			wantsFMP4 = true
		}
	}
	return wantsFMP4
}

func (s *Service) serveBroadcastPlaylist(w http.ResponseWriter, r *http.Request, ss *session) {
	index, segments, err := s.broadcastWindow(r.Context(), ss, r)
	if err != nil {
		if errors.Is(err, core.ErrNotAvailable) {
			w.Header().Set("Retry-After", "1")
		}
		writeStreamError(w, err)
		return
	}
	ss.mu.Lock()
	lease, caps := ss.broadcast, ss.caps
	vela := caps.Protocol == "vela"
	ss.mu.Unlock()
	if vela {
		window, err := broadcastLiveWindow(r.Context(), lease, index, segments)
		if err == nil {
			writeLiveAnchor(w, window)
		}
	}
	// TARGETDURATION is a property of the rendition, including its short
	// startup segments, and must not change as the window advances. Adding 1 s
	// of RFC 8216 ceiling headroom (3 s for 2 s segments) gives Apple HLS
	// clients 2.5 s of stale-playlist margin instead of 1.0 s.
	ss.mu.Lock()
	target := int(math.Ceil(broadcastSegment(ss.broadcast).Seconds())) + 1
	ss.mu.Unlock()
	for _, seg := range segments {
		target = max(target, int(math.Ceil(seg.Duration.Seconds())))
	}
	fmp4 := broadcastUsesFMP4(caps)
	version := 3
	ext := "ts"
	if fmp4 {
		version = 7
		ext = "m4s"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:%d\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", version, target, segments[0].Seq)
	fmt.Fprintf(&b, "#EXT-X-START:TIME-OFFSET=-%.3f,PRECISE=NO\n", s.latencyPolicy().TargetLatencySeconds)
	if fmp4 {
		initURI := "resource/" + transcode.InitSegmentName
		if !strings.Contains(r.URL.Path, "/livetv/sessions/") {
			initURI = fmt.Sprintf("/livetv/sessions/%s/0/resource/%s", ss.info.ID, transcode.InitSegmentName)
		}
		fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q\n", withAuth(initURI, ss.streamGrant))
	}
	for _, seg := range segments {
		uri := fmt.Sprintf("relay/%d.%s", seg.Seq, ext)
		if !strings.Contains(r.URL.Path, "/livetv/sessions/") {
			uri = fmt.Sprintf("/livetv/sessions/%s/0/relay/%d.%s", ss.info.ID, seg.Seq, ext)
		}
		uri = withAuth(uri, ss.streamGrant)
		precision := 6
		if vela {
			precision = 9
		}
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.*f,\n%s\n", seg.Start.UTC().Format(time.RFC3339Nano), precision, seg.Duration.Seconds(), uri)
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		fmt.Fprint(w, b.String())
	}
}
func (s *Service) serveBroadcastInit(w http.ResponseWriter, r *http.Request, ss *session) {
	index, err := s.broadcastSegments(r.Context(), ss)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	index.ServeInit(w, r)
}
func (s *Service) serveBroadcastFMP4(w http.ResponseWriter, r *http.Request, ss *session, seq int64) {
	index, err := s.broadcastSegments(r.Context(), ss)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	segments := index.Since(seq-1, 1)
	if len(segments) == 0 || segments[0].Seq != seq {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	if !s.broadcastSegmentAllowed(r.Context(), ss, segments[0]) {
		writeStreamError(w, core.ErrUnauthorized)
		return
	}
	s.touchMedia(ss)
	observed := s.observeMedia(w, ss)
	defer observed.done()
	index.ServeFMP4(observed, r, seq)
}
func (s *Service) serveBroadcastTS(w http.ResponseWriter, r *http.Request, ss *session, seq int64) {
	index, err := s.broadcastSegments(r.Context(), ss)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	segments := index.Since(seq-1, 1)
	if len(segments) == 0 || segments[0].Seq != seq {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	if !s.broadcastSegmentAllowed(r.Context(), ss, segments[0]) {
		writeStreamError(w, core.ErrUnauthorized)
		return
	}
	s.touchMedia(ss)
	observed := s.observeMedia(w, ss)
	defer observed.done()
	index.ServeTS(observed, r, seq)
}
func (s *Service) serveBroadcastDASH(w http.ResponseWriter, r *http.Request, ss *session) error {
	index, segments, err := s.broadcastWindow(r.Context(), ss, r)
	if err != nil {
		return err
	}
	codecs, width, height, bandwidth := index.Format()
	first := segments[0]
	if tracks, ok := index.(transcode.DASHSegmentIndex); ok {
		return s.serveBroadcastTrackDASH(w, r, ss, tracks, segments)
	}
	ss.mu.Lock()
	lease := ss.broadcast
	ss.mu.Unlock()
	if lease == nil {
		return core.ErrNotAvailable
	}
	origin, err := lease.timelineOrigin(r.Context())
	if err != nil {
		return err
	}
	init := withAuth(fmt.Sprintf("/livetv/sessions/%s/0/dash/0/header", ss.info.ID), ss.streamGrant)
	media := withAuth(fmt.Sprintf("/livetv/sessions/%s/0/dash/0/$Number$.m4s", ss.info.ID), ss.streamGrant)
	minBufSec, windowSec, delaySec := s.broadcastDASHWindow(ss, lease)
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="utf-8"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="dynamic" profiles="urn:mpeg:dash:profile:isoff-live:2011" availabilityStartTime="%s" publishTime="%s" minimumUpdatePeriod="PT2S" minBufferTime="PT%dS" timeShiftBufferDepth="PT%dS" suggestedPresentationDelay="PT%dS"><Period id="0" start="PT0S"><AdaptationSet mimeType="video/mp4" segmentAlignment="true"><Representation id="0" codecs="%s" width="%d" height="%d" bandwidth="%d"><SegmentTemplate timescale="1000000000" startNumber="%d" initialization="%s" media="%s"><SegmentTimeline>`, origin.UTC().Format(time.RFC3339Nano), s.o.Now().UTC().Format(time.RFC3339Nano), minBufSec, windowSec, delaySec, html.EscapeString(codecs), width, height, bandwidth, first.Seq, html.EscapeString(init), html.EscapeString(media))
	for _, seg := range segments {
		fmt.Fprintf(&b, `<S t="%d" d="%d"/>`, seg.Start.Sub(origin).Nanoseconds(), seg.Duration.Nanoseconds())
	}
	b.WriteString(`</SegmentTimeline></SegmentTemplate></Representation></AdaptationSet></Period></MPD>`)
	w.Header().Set("Content-Type", "application/dash+xml")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		fmt.Fprint(w, b.String())
	}
	return nil
}
func (s *Service) serveBroadcastDASHPart(w http.ResponseWriter, r *http.Request, ss *session, rep int, seq int64, init bool) {
	index, err := s.broadcastSegments(r.Context(), ss)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	tracks, separate := index.(transcode.DASHSegmentIndex)
	if !separate && rep != 0 {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	if init {
		if separate {
			tracks.ServeDASHTrack(w, r, rep, seq, true)
		} else {
			index.ServeInit(w, r)
		}
		return
	}
	segments := index.Since(seq-1, 1)
	if len(segments) == 0 || segments[0].Seq != seq {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	if !s.broadcastSegmentAllowed(r.Context(), ss, segments[0]) {
		writeStreamError(w, core.ErrUnauthorized)
		return
	}
	s.touchMedia(ss)
	observed := s.observeMedia(w, ss)
	defer observed.done()
	if separate {
		tracks.ServeDASHTrack(observed, r, rep, seq, false)
	} else {
		index.ServeFMP4(observed, r, seq)
	}
}

// broadcastDASHWindow returns MPD timing attributes (minBufferTime,
// timeShiftBufferDepth, suggestedPresentationDelay) in seconds matching the
// session's actual segment length and live playlist window.
func (s *Service) broadcastDASHWindow(ss *session, lease *broadcastLease) (minBufSec, windowSec, delaySec int) {
	policy := s.latencyPolicy()
	minBufSec = max(2, int(math.Ceil(broadcastSegment(lease).Seconds())))
	delaySec = max(minBufSec, int(math.Ceil(policy.TargetLatency().Seconds())))
	if ss.info.Channel.Kind == "virtual" {
		windowSec = 1800
	} else {
		windowSec = max(minBufSec*3, int(math.Ceil(policy.PlaylistWindow().Seconds())))
	}
	return minBufSec, windowSec, delaySec
}

// Separate AdaptationSets give browser capability checks real audio parameters.
// Older clients otherwise guess a 1 Hz sample rate for multiplexed video/audio.
func (s *Service) serveBroadcastTrackDASH(w http.ResponseWriter, r *http.Request, ss *session, index transcode.DASHSegmentIndex, segments []transcode.Segment) error {
	tracks, err := index.DASHTracks()
	if err != nil {
		return err
	}
	ss.mu.Lock()
	lease := ss.broadcast
	ss.mu.Unlock()
	if lease == nil {
		return core.ErrNotAvailable
	}
	origin, err := lease.timelineOrigin(r.Context())
	if err != nil {
		return err
	}
	minBufSec, windowSec, delaySec := s.broadcastDASHWindow(ss, lease)
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="utf-8"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="dynamic" profiles="urn:mpeg:dash:profile:isoff-live:2011" availabilityStartTime="%s" publishTime="%s" minimumUpdatePeriod="PT2S" minBufferTime="PT%dS" timeShiftBufferDepth="PT%dS" suggestedPresentationDelay="PT%dS"><Period id="0" start="PT0S">`, origin.UTC().Format(time.RFC3339Nano), s.o.Now().UTC().Format(time.RFC3339Nano), minBufSec, windowSec, delaySec)
	for rep, t := range tracks {
		fmt.Fprintf(&b, `<AdaptationSet contentType="%s" mimeType="%s/mp4" segmentAlignment="true"><Representation id="%d" codecs="%s" bandwidth="%d"`, t.Kind, t.Kind, rep, html.EscapeString(t.Codec), t.Bandwidth)
		if t.Kind == "audio" {
			fmt.Fprintf(&b, ` audioSamplingRate="%d">`, t.SampleRate)
			fmt.Fprintf(&b, `<AudioChannelConfiguration schemeIdUri="urn:mpeg:dash:23003:3:audio_channel_configuration:2011" value="%d"/>`, t.Channels)
		} else {
			fmt.Fprintf(&b, ` width="%d" height="%d">`, t.Width, t.Height)
		}
		init := withAuth(fmt.Sprintf("/livetv/sessions/%s/0/dash/%d/header", ss.info.ID, rep), ss.streamGrant)
		media := withAuth(fmt.Sprintf("/livetv/sessions/%s/0/dash/%d/$Number$.m4s", ss.info.ID, rep), ss.streamGrant)
		fmt.Fprintf(&b, `<SegmentTemplate timescale="1000000000" startNumber="%d" initialization="%s" media="%s"><SegmentTimeline>`, segments[0].Seq, html.EscapeString(init), html.EscapeString(media))
		for _, seg := range segments {
			fmt.Fprintf(&b, `<S t="%d" d="%d"/>`, seg.Start.Sub(origin).Nanoseconds(), seg.Duration.Nanoseconds())
		}
		b.WriteString(`</SegmentTimeline></SegmentTemplate></Representation></AdaptationSet>`)
	}
	b.WriteString(`</Period></MPD>`)
	w.Header().Set("Content-Type", "application/dash+xml")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		fmt.Fprint(w, b.String())
	}
	return nil
}
