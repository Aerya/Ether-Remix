package livetv

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// ownedTimeline is the live playlist a client actually reads on the Direct
// paths (HLS passthrough and relay). Ether, not the provider or ffmpeg, owns
// its media sequence, discontinuity sequence and program date-time, so any
// recovery (refetch, re-resolution, a backup source, a producer switch, a
// slate) is spliced into one continuous playlist and the client never has to
// switch streams.
//
// The client contract it enforces: a new segment appears within about one
// target duration, always. Producers ingest segments; the timeline publishes
// them at real-time pace from an optional reserve (a server-side buffer of
// already-fetched provider segments) and, when nothing is ready about a
// quarter target duration after a segment is due, publishes pre-encoded
// slate instead.
type ownedTimeline struct {
	label   string
	log     *slog.Logger
	now     func() time.Time
	reserve time.Duration // media held back behind the source's edge
	slates  func(slateProfile) *slateClip
	// stalled, when set, reports that the producer's input has stopped
	// (provider health): the next segment cannot arrive, so slate goes out
	// when that segment is due instead of after the deadline's grace.
	// stalled and producing are called with mu held: they must only read
	// state (atomics), never block on the producer or its input.
	stalled func() bool
	// producing, when set, reports a producer whose input is flowing and
	// that is cutting on schedule: a late segment from it is jitter, so it
	// gets the in-flight allowance before slate.
	producing func() bool

	mu          sync.Mutex
	target      time.Duration // fixed at the first publication
	windowSpan  time.Duration
	startBehind time.Duration
	targetFloor time.Duration
	startSegs   int // segments the first playlist waits for
	// startMedia, when set, also starts the first playlist once that much
	// media is in hand (a real-time producer's long first segment).
	startMedia time.Duration
	// startMinMedia, when set, requires the first playlist to hold both
	// startSegs segments and at least max(startMinMedia, ~2*target) of
	// media (within startWait) so a player does not stall on a single short
	// opening segment.
	startMinMedia time.Duration
	// startWait, when set, bounds how long first publication waits after
	// firstAt for the startup threshold before publishing what exists.
	// Zero keeps the short passthrough grace (<= ownedTick).
	startWait time.Duration
	firstAt   time.Time // first ingest, bounds the startup wait
	window    []*ownedSegment
	history   []*ownedSegment // slid out of the playlist, still served briefly
	pending   []*ownedSegment // ingested, not yet published: the reserve
	resources map[string]ownedResource
	nextSeq   int64
	discSeq   int64
	lastAt    time.Time     // scheduled publication time of the newest segment
	lastDur   time.Duration // its duration
	elapsed   time.Duration // end of the newest published segment
	lastPDT   time.Time
	format    mediaFormat // newest published media format
	mapRes    string      // newest published media init section
	hint      slateProfile
	needDisc  bool // the next media segment starts a new span
	slating   int  // consecutive slate segments in the current run
	slateCC   map[uint16]byte
	notify    chan struct{}
	inflight  bool // a producer is fetching a segment it has seen listed
	closed    bool
	err       error

	// Evidence, reported in logs and diagnostics.
	slateSegments int
	slateRuns     int // slate episodes (each starts with a discontinuity)
	slateTime     time.Duration
	codecSplices  int
	spans         int

	// Immutable DASH timeline anchors for fMP4 owned sessions.
	dashOrigin time.Time
	dashSpans  map[int]*ownedDashSpan
}

type ownedDashSpan struct {
	id        int
	start     time.Duration
	mapRes    string
	tracks    []transcode.DASHTrack
	originSec float64
}

type ownedDashPeriod struct {
	id        int
	start     time.Duration
	mapRes    string
	tracks    []transcode.DASHTrack
	originSec float64
	segs      []*ownedSegment
}

type ownedSegment struct {
	seq    int64
	span   int
	start  time.Duration // exact cumulative session time, independent of PDT
	dur    time.Duration
	disc   bool
	pdt    time.Time
	data   []byte
	ext    string // "ts" or "m4s"
	mapRes string // owned resource holding the init section; "" none
	key    ownedKey
	format mediaFormat
	slate  bool
	origin string // producer label, for logs
}

// ownedKey is an AES-128/SAMPLE-AES key proxied for one span. The IV is
// always explicit: Ether renumbers segments, so the provider's implicit
// sequence-number IV would no longer match.
type ownedKey struct {
	method, res, iv, format, versions string
}

type ownedResource struct {
	data  []byte
	ctype string
}

const (
	ownedWindowSpan = 24 * time.Second
	ownedMinWindow  = 6
	ownedHistory    = 4
	ownedTick       = 100 * time.Millisecond
)

func newOwnedTimeline(label string, log *slog.Logger, now func() time.Time, reserve time.Duration, slates func(slateProfile) *slateClip) *ownedTimeline {
	if now == nil {
		now = time.Now
	}
	return &ownedTimeline{label: label, log: log, now: now, reserve: max(reserve, 0), slates: slates, startSegs: 1,
		resources: map[string]ownedResource{}, notify: make(chan struct{})}
}

// run publishes on schedule until ctx ends.
func (t *ownedTimeline) run(ctx context.Context) {
	tick := time.NewTicker(ownedTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.mu.Lock()
			t.closed = true
			if t.err == nil {
				t.err = core.ErrNotFound
			}
			t.wakeLocked()
			slate, codec, spans, slateTime := t.slateSegments, t.codecSplices, t.spans, t.slateTime
			t.mu.Unlock()
			t.log.Info("owned live timeline closed", "slate_segments", slate, "slate_seconds", slateTime.Seconds(), "codec_splices", codec, "spans", spans)
			return
		case <-tick.C:
			started := time.Now()
			t.mu.Lock()
			t.tickLocked(t.now())
			t.mu.Unlock()
			if took := time.Since(started); took > 250*time.Millisecond {
				t.log.Warn("owned live timeline tick was slow", "took", took)
			}
		}
	}
}

func (t *ownedTimeline) wakeLocked() {
	close(t.notify)
	t.notify = make(chan struct{})
}

// expectStart sets how many segments the first playlist should carry
// (segs > 0) and raises the floor of the target duration (the source's own
// target). Both are fixed once anything is published: the target duration
// never changes mid-session.
func (t *ownedTimeline) expectStart(segs int, floor time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.window) > 0 {
		return
	}
	if segs > 0 {
		t.startSegs = segs
	}
	t.targetFloor = max(t.targetFloor, floor)
	t.tickLocked(t.now())
}

func (t *ownedTimeline) setHint(p slateProfile) {
	t.mu.Lock()
	t.hint = p
	t.mu.Unlock()
}

func (t *ownedTimeline) addResource(name string, data []byte, ctype string) {
	t.mu.Lock()
	if _, ok := t.resources[name]; !ok {
		t.resources[name] = ownedResource{data: data, ctype: ctype}
	}
	t.mu.Unlock()
}

func (t *ownedTimeline) hasResource(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.resources[name]
	return ok
}

// ingest queues a fetched segment. A producer sets disc for source
// discontinuities, gaps and source changes; codec changes are detected here.
func (t *ownedTimeline) ingest(seg *ownedSegment) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	now := t.now()
	if t.firstAt.IsZero() {
		t.firstAt = now
	}
	t.pending = append(t.pending, seg)
	t.tickLocked(now)
}

// splice marks the next ingested media as a new span (producer switch,
// source change). It also drops queued media from the previous producer
// when discard is set: content from a failed source must not air after
// content from its replacement.
func (t *ownedTimeline) splice(discard bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if discard {
		t.pending = nil
		if len(t.window) == 0 {
			t.firstAt = time.Time{}
		}
	}
	t.needDisc = true
}

// setInflight tells the deadline that the next segment is being fetched.
func (t *ownedTimeline) setInflight(v bool) {
	t.mu.Lock()
	t.inflight = v
	t.mu.Unlock()
}

// depth is the media time ingested but not yet published.
func (t *ownedTimeline) depth() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.depthLocked()
}

func (t *ownedTimeline) depthLocked() time.Duration {
	var d time.Duration
	for _, s := range t.pending {
		d += s.dur
	}
	return d
}

// codecSpliceCount is how many unannounced format changes were marked.
func (t *ownedTimeline) codecSpliceCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.codecSplices
}

// tsOnly reports a timeline of transport stream media (no init sections), so
// a TS producer can follow it behind a discontinuity.
func (t *ownedTimeline) tsOnly() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mapRes == "" || strings.HasPrefix(t.mapRes, "slate-")
}

// slateRunCount is how many slate episodes the timeline has published.
func (t *ownedTimeline) slateRunCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.slateRuns
}

// lastSegmentDuration is the duration of the newest published segment.
func (t *ownedTimeline) lastSegmentDuration() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastDur
}

func (t *ownedTimeline) targetDuration() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.target
}

// started reports that the first playlist has been published.
func (t *ownedTimeline) started() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.window) > 0
}

// hasPending reports whether at least one segment has been ingested and is
// still awaiting first publication.
func (t *ownedTimeline) hasPending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.window) == 0 && len(t.pending) > 0
}

// startWaitRemaining returns how much of the timeline's bounded startup wait
// remains after firstAt, or 0 when nothing is pending.
func (t *ownedTimeline) startWaitRemaining() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.window) > 0 || len(t.pending) == 0 || t.firstAt.IsZero() {
		return 0
	}
	if rem := t.startWaitLocked() - t.now().Sub(t.firstAt); rem > 0 {
		return rem
	}
	return 0
}

func (t *ownedTimeline) projectedTargetLocked() time.Duration {
	// The target covers the source's own target and every segment already in
	// hand (Apple rule 7.7: no segment may exceed it). RFC 8216 compares
	// rounded EXTINF durations with the target.
	secs := max(math.Ceil(t.targetFloor.Seconds()-0.001), 1)
	for _, s := range t.pending {
		secs = max(secs, math.Round(s.dur.Seconds()))
	}
	return time.Duration(secs) * time.Second
}

func (t *ownedTimeline) startWaitLocked() time.Duration {
	if t.startWait > 0 {
		return t.startWait
	}
	return min(ownedTick, t.targetFloor/4)
}

func (t *ownedTimeline) fail(err error) {
	t.mu.Lock()
	if t.err == nil {
		t.err = err
	}
	t.wakeLocked()
	t.mu.Unlock()
}

func (t *ownedTimeline) tickLocked(now time.Time) {
	if t.closed {
		return
	}
	changed := false
	if len(t.window) == 0 {
		if len(t.pending) == 0 {
			return
		}
		depth := t.depthLocked()
		projTarget := t.projectedTargetLocked()
		var minMedia time.Duration
		var enough bool
		if t.startMinMedia > 0 {
			// Hold the first client-visible publication until the timeline
			// holds enough media to start and keep playing: at least
			// startSegs segments AND ~2x target duration (with a 5% rounding
			// margin for fractional frame rates), or a single long opening
			// segment that already covers startMedia (e.g. a 4 s GOP stream).
			minMedia = max(t.startMinMedia, 2*projTarget)
			enough = len(t.pending) >= t.startSegs && depth >= minMedia*19/20 ||
				t.startMedia > 0 && depth >= t.startMedia*19/20 && depth >= projTarget*19/20
		} else {
			// Prefer the start segments, or the media they stand for (a long
			// first segment), but release a short backlog after at most one tick.
			// The first playlist has a half-target response deadline: AVPlayer
			// can hang permanently if it waits a whole target. Leave room for
			// ingest and the next publication tick, even with a one-second target.
			enough = len(t.pending) >= t.startSegs || t.startMedia > 0 && depth >= t.startMedia
		}
		if !enough && now.Sub(t.firstAt) < t.startWaitLocked() {
			return
		}
		t.target = projTarget
		// The first playlist carries at least startSegs segments and enough
		// media to cover minMedia; any further backlog stays pending as the
		// reserve.
		n := min(t.startSegs, len(t.pending))
		var burst time.Duration
		for _, s := range t.pending[:n] {
			burst += s.dur
		}
		if minMedia > 0 {
			need := minMedia * 19 / 20
			for n < len(t.pending) && burst < need {
				burst += t.pending[n].dur
				n++
			}
		}
		t.lastPDT = now.Add(-burst)
		for i := 0; i < n; i++ {
			t.publishLocked(t.pending[0], now)
			t.pending = t.pending[1:]
		}
		t.lastAt = now
		changed = true
		t.log.Info("owned live timeline started", "segments", n, "media", burst, "start_wait", now.Sub(t.firstAt), "target", t.target, "reserve_depth", t.depthLocked(), "reserve_target", t.reserve, "format", t.format.String())
	}
	for len(t.pending) > 0 {
		head := t.pending[0]
		// Real-time pacing, but the playlist advances at least once a
		// target: a long segment (a copy cut late across a timestamp jump)
		// must not hold the next one back for its whole length.
		due := t.lastAt.Add(t.nextDueLocked())
		excess := t.depthLocked()-head.dur >= t.reserve+t.target
		if t.reserve > 0 && now.Before(due) && !excess {
			break
		}
		at := due
		if now.Sub(due) > 500*time.Millisecond || now.Before(due) {
			at = now
		}
		t.publishLocked(head, at)
		t.pending = t.pending[1:]
		changed = true
	}
	if len(t.pending) == 0 && t.slates != nil {
		// The deadline. Segment durations vary up to the target, so the
		// next one is late only about a quarter target (at most half a
		// second) past a target after the last publication, a little longer
		// while its fetch is already in flight. Then splice slate, so the
		// playlist never sits unchanged long enough for Apple's 1.5x-target
		// rule to end playback.
		wait := t.target + min(t.target/4, 500*time.Millisecond)
		if t.inflight || t.producing != nil && t.producing() {
			// A player measures 1.5x target from when it saw the last
			// change, which is always after this publication.
			wait = t.target + min(t.target*9/20, time.Second)
		}
		if t.slating > 0 || t.stalled != nil && !now.Before(t.lastAt.Add(t.nextDueLocked())) && t.stalled() {
			wait = t.nextDueLocked()
		}
		for !now.Before(t.lastAt.Add(wait)) {
			if !t.publishSlateLocked() {
				break
			}
			changed = true
			wait = t.nextDueLocked()
		}
	}
	if changed {
		t.wakeLocked()
	}
}

// nextDueLocked is how long after the newest publication the next one is
// due: that segment's length, but never more than the target.
func (t *ownedTimeline) nextDueLocked() time.Duration {
	if t.target > 0 {
		return min(t.lastDur, t.target)
	}
	return t.lastDur
}

func (t *ownedTimeline) publishLocked(seg *ownedSegment, at time.Time) {
	if len(t.window) == 0 && len(t.history) == 0 {
		seg.disc = false
		t.spans = 1
	} else if t.needDisc || t.slating > 0 {
		seg.disc = true
	}
	if !seg.disc && len(t.window) > 0 {
		if formatChanged(t.format, seg.format) {
			seg.disc = true
			t.codecSplices++
			t.log.Warn("owned live timeline marked an unannounced format change", "from", t.format.String(), "to", seg.format.String(), "origin", seg.origin)
		} else if seg.mapRes != "" && t.mapRes != "" && seg.mapRes != t.mapRes {
			seg.disc = true
		}
	}
	if seg.disc {
		t.spans++
	}
	t.needDisc = false
	if t.slating > 0 {
		t.log.Info("owned live timeline rejoined media", "slate_segments", t.slating, "origin", seg.origin)
	}
	t.slating = 0
	t.slateCC = nil
	t.format = mergeFormat(t.format, seg.format)
	seg.format = t.format
	t.mapRes = seg.mapRes
	seg.span = t.spans
	t.appendLocked(seg, at)
}

func (t *ownedTimeline) appendLocked(seg *ownedSegment, at time.Time) {
	seg.start = t.elapsed
	t.elapsed += seg.dur
	seg.seq = t.nextSeq
	t.nextSeq++
	pdt := t.lastPDT
	if len(t.window) > 0 {
		last := t.window[len(t.window)-1]
		pdt = last.pdt.Add(last.dur)
		// Keep program date-time continuous inside a span; a new span
		// may resynchronise to the wall clock when it has drifted.
		if wall := at.Add(-seg.dur); seg.disc && (pdt.Sub(wall) > 10*time.Second || wall.Sub(pdt) > 10*time.Second) {
			pdt = wall
		}
	}
	seg.pdt = pdt
	t.window = append(t.window, seg)
	t.lastAt, t.lastDur = at, seg.dur
	keep := ownedMinWindow
	if t.target > 0 {
		keep = max(keep, int(math.Ceil(float64(t.playlistWindow())/float64(t.target))))
	}
	for len(t.window) > keep {
		gone := t.window[0]
		t.window = t.window[1:]
		if gone.disc {
			t.discSeq++
		}
		t.history = append(t.history, gone)
	}
	if len(t.history) > ownedHistory {
		t.history = t.history[len(t.history)-ownedHistory:]
		t.collectLocked()
	}
}

// collectLocked drops init sections and keys no queued or listed segment
// references.
func (t *ownedTimeline) collectLocked() {
	live := map[string]bool{}
	liveSpans := map[int]bool{}
	for _, list := range [][]*ownedSegment{t.history, t.window, t.pending} {
		for _, s := range list {
			live[s.mapRes] = true
			live[s.key.res] = true
			if s.span > 0 {
				liveSpans[s.span] = true
			}
		}
	}
	for name := range t.resources {
		if !live[name] && !strings.HasPrefix(name, "slate-") {
			delete(t.resources, name)
		}
	}
	for id := range t.dashSpans {
		if !liveSpans[id] {
			delete(t.dashSpans, id)
		}
	}
}

// isFMP4 reports whether this timeline is publishing fMP4 segments with an init
// section that can be exposed directly as DASH without a second transcoder.
func (t *ownedTimeline) isFMP4() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.window) - 1; i >= 0; i-- {
		if t.window[i].ext == "m4s" && t.window[i].mapRes != "" {
			return true
		}
	}
	for i := len(t.pending) - 1; i >= 0; i-- {
		if t.pending[i].ext == "m4s" && t.pending[i].mapRes != "" {
			return true
		}
	}
	return false
}

// dashSnapshot returns an immutable availabilityStartTime and per-span DASH
// Periods for the current fMP4 window. Period start times and media-clock
// presentationTimeOffsets are anchored once per span so sliding old segments
// out of the window never shifts an active Period.
func (t *ownedTimeline) dashSnapshot(now time.Time) (origin time.Time, target time.Duration, periods []ownedDashPeriod, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var fmp4Segs []*ownedSegment
	for _, seg := range t.window {
		if seg.ext == "m4s" && seg.mapRes != "" {
			fmp4Segs = append(fmp4Segs, seg)
		}
	}
	if len(fmp4Segs) == 0 {
		return time.Time{}, 0, nil, false
	}
	if t.dashSpans == nil {
		t.dashSpans = map[int]*ownedDashSpan{}
	}
	for i := 0; i < len(fmp4Segs); {
		spanID := fmp4Segs[i].span
		if spanID <= 0 {
			spanID = 1
		}
		j := i + 1
		for j < len(fmp4Segs) {
			nextID := fmp4Segs[j].span
			if nextID <= 0 {
				nextID = 1
			}
			if nextID != spanID || fmp4Segs[j].mapRes != fmp4Segs[i].mapRes {
				break
			}
			j++
		}
		group := fmp4Segs[i:j]
		sp := t.dashSpans[spanID]
		if sp == nil || sp.mapRes != group[0].mapRes {
			initRes, has := t.resources[group[0].mapRes]
			if !has || len(initRes.data) == 0 {
				i = j
				continue
			}
			tracks, err := transcode.ParseDASHInit(initRes.data)
			if err != nil {
				i = j
				continue
			}
			originSec := 0.0
			haveOrigin := false
			for _, tr := range tracks {
				if tr.Timescale == 0 {
					continue
				}
				if start, _, ok := transcode.FMP4TrackSpan(group[0].data, tr.ID, tr.DefaultDuration, tr.EditShift); ok {
					sec := float64(start) / float64(tr.Timescale)
					if !haveOrigin || sec < originSec {
						originSec = sec
						haveOrigin = true
					}
				}
			}
			sp = &ownedDashSpan{
				id:        spanID,
				start:     group[0].start,
				mapRes:    group[0].mapRes,
				tracks:    tracks,
				originSec: originSec,
			}
			t.dashSpans[spanID] = sp
		}
		segsCopy := make([]*ownedSegment, len(group))
		copy(segsCopy, group)
		periods = append(periods, ownedDashPeriod{
			id:        sp.id,
			start:     sp.start,
			mapRes:    sp.mapRes,
			tracks:    sp.tracks,
			originSec: sp.originSec,
			segs:      segsCopy,
		})
		i = j
	}
	if len(periods) == 0 {
		return time.Time{}, 0, nil, false
	}
	if t.dashOrigin.IsZero() {
		maxEnd := t.elapsed
		lastP := periods[len(periods)-1]
		lastSeg := lastP.segs[len(lastP.segs)-1]
		for _, tr := range lastP.tracks {
			if tr.Timescale == 0 {
				continue
			}
			if start, dur, ok := transcode.FMP4TrackSpan(lastSeg.data, tr.ID, tr.DefaultDuration, tr.EditShift); ok {
				endSec := float64(start+dur)/float64(tr.Timescale) - lastP.originSec
				if end := lastP.start + time.Duration(endSec*float64(time.Second)); end > maxEnd {
					maxEnd = end
				}
			}
		}
		t.dashOrigin = now.Add(-maxEnd)
	}
	return t.dashOrigin, t.target, periods, true
}

func (t *ownedTimeline) dashInit(mapRes string) ([]byte, []transcode.DASHTrack, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if mapRes == "" {
		mapRes = t.mapRes
	}
	res, ok := t.resources[mapRes]
	if !ok || len(res.data) == 0 {
		return nil, nil, false
	}
	for _, sp := range t.dashSpans {
		if sp.mapRes == mapRes && len(sp.tracks) > 0 {
			return res.data, sp.tracks, true
		}
	}
	tracks, err := transcode.ParseDASHInit(res.data)
	if err != nil {
		return nil, nil, false
	}
	return res.data, tracks, true
}

func (t *ownedTimeline) dashSegment(seq int64) ([]byte, []transcode.DASHTrack, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var seg *ownedSegment
	for i := len(t.window) - 1; i >= 0; i-- {
		if t.window[i].seq == seq {
			seg = t.window[i]
			break
		}
	}
	if seg == nil {
		for i := len(t.history) - 1; i >= 0; i-- {
			if t.history[i].seq == seq {
				seg = t.history[i]
				break
			}
		}
	}
	if seg == nil || seg.ext != "m4s" || seg.mapRes == "" {
		return nil, nil, false
	}
	if sp := t.dashSpans[seg.span]; sp != nil && sp.mapRes == seg.mapRes && len(sp.tracks) > 0 {
		return seg.data, sp.tracks, true
	}
	res, ok := t.resources[seg.mapRes]
	if !ok || len(res.data) == 0 {
		return nil, nil, false
	}
	tracks, err := transcode.ParseDASHInit(res.data)
	if err != nil {
		return nil, nil, false
	}
	return seg.data, tracks, true
}

func (t *ownedTimeline) slateProfileLocked() slateProfile {
	p := t.hint
	if t.format.video == "hevc" || t.format.video == "h264" {
		p.video = t.format.video
	}
	if t.format.width > 0 {
		p.width, p.height = t.format.width, t.format.height
	}
	if len(t.format.audio) > 0 {
		a := t.format.audio[0]
		switch a.codec {
		case "aac", "ac3", "eac3":
			p.audio = a.codec
		}
		if a.codec == "aac" && a.rate > 0 {
			p.sampleRate = a.rate
		}
		if a.codec == "aac" && a.channels > 0 {
			p.channels = a.channels
		}
	}
	if t.mapRes != "" && !strings.HasPrefix(t.mapRes, "slate-") {
		p.container = "fmp4"
	} else if p.container == "" {
		p.container = "ts"
	}
	if t.target > 0 {
		p.segment = min(2*time.Second, t.target)
	}
	return p.normalized()
}

// warmSlate asks for the slate of the current profile so it is encoded
// before it is needed.
func (t *ownedTimeline) warmSlate() {
	if t.slates == nil {
		return
	}
	t.mu.Lock()
	p := t.slateProfileLocked()
	t.mu.Unlock()
	t.slates(p)
}

func (t *ownedTimeline) publishSlateLocked() bool {
	clip := t.slates(t.slateProfileLocked())
	if clip == nil || clip.dur <= 0 {
		return false
	}
	seg := &ownedSegment{dur: clip.dur, ext: clip.ext, slate: true, origin: "slate"}
	k := t.slating
	if clip.ext == "m4s" {
		t.resources[clip.initRes] = ownedResource{data: clip.init, ctype: "video/mp4"}
		seg.mapRes = clip.initRes
		seg.data = shiftFMP4(clip.data, clip.scales, float64(k)*clip.dur.Seconds(), uint32(k+1))
	} else {
		if t.slateCC == nil {
			t.slateCC = map[uint16]byte{}
		}
		seg.data = shiftTS(clip.data, int64(k)*int64(clip.dur.Seconds()*90000+0.5), t.slateCC)
	}
	if k == 0 {
		seg.disc = true
		t.spans++
		t.slateRuns++
		t.needDisc = true
		t.log.Warn("owned live timeline publishing slate", "since_last_segment", t.now().Sub(t.lastAt), "target", t.target, "reserve_depth", t.depthLocked(),
			"producing", t.producing != nil && t.producing(), "input_stalled", t.stalled != nil && t.stalled(), "fetch_in_flight", t.inflight)
	}
	t.slating++
	t.slateSegments++
	t.slateTime += clip.dur
	at := t.lastAt.Add(t.nextDueLocked())
	if now := t.now(); at.After(now) || now.Sub(at) > time.Second {
		at = now
	}
	t.appendLocked(seg, at)
	return true
}

// waitStarted blocks until the first playlist exists.
func (t *ownedTimeline) waitStarted(ctx context.Context) error {
	for {
		t.mu.Lock()
		if len(t.window) > 0 {
			t.mu.Unlock()
			return nil
		}
		if t.err != nil {
			err := t.err
			t.mu.Unlock()
			return err
		}
		ch := t.notify
		t.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

// segment returns segment seq, waiting briefly when the client asks for the
// one about to be published.
func (t *ownedTimeline) segment(ctx context.Context, seq int64) (*ownedSegment, error) {
	deadline := time.NewTimer(2 * max(t.targetDuration(), 2*time.Second))
	defer deadline.Stop()
	for {
		t.mu.Lock()
		for _, list := range [][]*ownedSegment{t.window, t.history} {
			for _, s := range list {
				if s.seq == seq {
					t.mu.Unlock()
					return s, nil
				}
			}
		}
		if t.closed || seq < t.nextSeq || seq > t.nextSeq+2 {
			t.mu.Unlock()
			return nil, core.ErrNotFound
		}
		ch := t.notify
		t.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, core.ErrNotFound
		case <-ch:
		}
	}
}

func (t *ownedTimeline) resource(name string) (ownedResource, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.resources[name]
	return r, ok
}

// render writes the media playlist. seg and res build session-scoped URIs.
func (t *ownedTimeline) render(seg func(seq int64, ext string) string, res func(name string) string) []byte {
	body, _ := t.renderSnapshot(seg, res, false)
	return body
}

// renderSnapshot binds the anchor and body under the same publication lock.
func (t *ownedTimeline) renderSnapshot(seg func(int64, string) string, res func(string) string, exact bool) ([]byte, frontend.LiveWindow) {
	t.mu.Lock()
	defer t.mu.Unlock()
	window, _ := t.liveWindowLocked()
	var b bytes.Buffer
	version := 3
	for _, s := range t.window {
		if s.mapRes != "" {
			version = max(version, 7)
		}
		if s.key.format != "" {
			version = max(version, 5)
		}
	}
	b.WriteString("#EXTM3U\n")
	fmt.Fprintf(&b, "#EXT-X-VERSION:%d\n", version)
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(t.target/time.Second))
	if t.startBehind > 0 {
		// RFC 8216: a negative TIME-OFFSET must not exceed the playlist's
		// duration. A fresh window holds less than the desired start depth
		// (one 1.6 s segment against a 12 s offset), and Safari/AVPlayer
		// reject such a playlist, so clamp it to what the window holds.
		var held time.Duration
		for _, s := range t.window {
			held += s.dur
		}
		if offset := min(max(t.startBehind, 3*t.target), held); offset > 0 {
			fmt.Fprintf(&b, "#EXT-X-START:TIME-OFFSET=-%.3f,PRECISE=NO\n", offset.Seconds())
		}
	}
	first := t.nextSeq
	if len(t.window) > 0 {
		first = t.window[0].seq
	}
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", first)
	fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", t.discSeq)
	var key ownedKey
	mapRes := ""
	for i, s := range t.window {
		if s.disc {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		if i == 0 && s.key.method != "" || i > 0 && s.key != key {
			if s.key.method == "" {
				b.WriteString("#EXT-X-KEY:METHOD=NONE\n")
			} else {
				fmt.Fprintf(&b, "#EXT-X-KEY:METHOD=%s,URI=%q", s.key.method, res(s.key.res))
				if s.key.iv != "" {
					fmt.Fprintf(&b, ",IV=%s", s.key.iv)
				}
				if s.key.format != "" {
					fmt.Fprintf(&b, ",KEYFORMAT=%q", s.key.format)
				}
				if s.key.versions != "" {
					fmt.Fprintf(&b, ",KEYFORMATVERSIONS=%q", s.key.versions)
				}
				b.WriteByte('\n')
			}
		}
		key = s.key
		if s.mapRes != "" && (i == 0 || s.disc || s.mapRes != mapRes) {
			fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q\n", res(s.mapRes))
		}
		mapRes = s.mapRes
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n", s.pdt.UTC().Format("2006-01-02T15:04:05.000Z"))
		precision := 3
		if exact {
			precision = 9
		}
		fmt.Fprintf(&b, "#EXTINF:%.*f,\n%s\n", precision, s.dur.Seconds(), seg(s.seq, s.ext))
	}
	return b.Bytes(), window
}

func (t *ownedTimeline) playlistWindow() time.Duration {
	if t.windowSpan > 0 {
		return t.windowSpan
	}
	return ownedWindowSpan
}
