package transcode

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// dashState is what a DASH session adds to Session: its representations
// and their current init segments. Guarded by Session.mu.
type dashState struct {
	pms      bool // Plex TV manifest shape and buffer requirements
	reps     []dashRep
	initURL  string
	mediaURL string
	inits    []string // per track: current init segment file in the session dir
	codecs   []string // per track: codecs read from that init segment
	mpd      []byte   // cached once every track's codecs are known
}

func newDASHState(reps []dashRep, urls DASHURLs, sessionID string) *dashState {
	if urls == nil {
		urls = PlexDASHURLs
	}
	initURL, mediaURL := urls(sessionID)
	return &dashState{
		reps:     reps,
		initURL:  initURL,
		mediaURL: mediaURL,
		inits:    make([]string, len(reps)),
		codecs:   make([]string, len(reps)),
	}
}

// dashRun is one ffmpeg run's chunk bookkeeping, owned by its watch
// goroutine.
type dashRun struct {
	dir    string
	tracks []*dashTrack
	failed bool
	// sessionTrack maps a muxer track (0=video, 1=audio in the run) to
	// the session track its segments belong to (a ladder video rung may
	// be a session track > 1).
	sessionTrack []int
}

// dashTrack follows one representation's chunks in a run directory.
type dashTrack struct {
	rep         int
	next        int // next chunk number ffmpeg will write (from 1)
	init        []byte
	info        trackInit
	published   bool // init copied into the session
	sl          *slotter
	mediaEnd    time.Duration // end timestamp of last consumed chunk
	hasMedia    bool
	drained     bool // last pass hit ENOENT at next
	chunkDurSum time.Duration
	chunkCount  int
}

func newDASHRun(dir string, sessionTracks []int, base, segDur time.Duration, total, startSeg int) *dashRun {
	dr := &dashRun{dir: dir, sessionTrack: sessionTracks}
	for t := range sessionTracks {
		dr.tracks = append(dr.tracks, &dashTrack{rep: t, next: 1, sl: newSlotter(base, segDur, total, startSeg)})
	}
	return dr
}

// dashDecision makes a decision streamable as DASH: fMP4 segments with
// codecs browsers decode through Media Source Extensions.
func dashDecision(info core.MediaInfo, d core.Decision) core.Decision {
	if d.Container != "" {
		d.Reason = strings.Replace(d.Reason, "remux to HLS ("+d.Container+")", "remux to DASH (fmp4)", 1)
	}
	d.Container = ContainerFMP4
	if v := info.Video(); v != nil && d.VideoCopy && !dashVideoOK(v.Codec) {
		d.VideoCopy = false
		d.VideoCodec = "h264"
		if d.MaxHeight <= 0 || d.MaxHeight > 1080 {
			d.MaxHeight = min(max(v.Height, 0), 1080)
		}
		d.ToneMap = encodedRange(v) != rangeSDR
		d.BitrateKbps = 0
		d.Reason += fmt.Sprintf("; DASH: video %s re-encoded to h264", v.Codec)
	}
	if a := findStream(info, d.AudioStream, "audio"); a != nil {
		if d.AudioCopy && !dashAudioOK(a.Codec) {
			d.AudioCopy = false
			d.AudioCodec = ""
			d.Reason += fmt.Sprintf("; DASH: audio %s re-encoded to aac", a.Codec)
		}
		if !d.AudioCopy && !dashAudioOK(d.AudioCodec) {
			d.AudioCodec = "aac"
		}
	}
	if d.EncodesVideo(info) {
		d.Kind = core.DecisionTranscode
	}
	return d
}

// ServeManifest implements core.DASHSession: the static, full-length MPD.
// The first request waits (bounded) for ffmpeg's init segments so the MPD
// carries exact codecs strings; until then they are estimated.
func (s *Session) ServeManifest(w http.ResponseWriter, r *http.Request) {
	if !s.isDASH() {
		http.NotFound(w, r)
		return
	}
	delay := s.m.opts.manifestWait
	s.mu.Lock() // SwapInput replaces s.info
	if s.info.Duration <= 0 {
		delay = s.m.opts.segmentTimeout
	}
	s.mu.Unlock()
	wait := time.NewTimer(delay)
	defer wait.Stop()
	timedOut, restarted := false, false
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			http.Error(w, "session closed", http.StatusGone)
			return
		}
		body := s.dash.mpd
		if body == nil {
			// Only the primary video and the audio track must have exact
			// codecs; ladder rungs that never ran keep their estimates.
			complete := true
			for i, rep := range s.dash.reps {
				needed := rep.Kind == "audio" || i == 0
				if needed && s.dash.codecs[i] == "" {
					complete = false
				}
			}
			idle := (s.cur == nil || s.cur.finished) && !s.pendingStart && !s.headerRecovering && !s.retryRecovering && !s.skipProbing
			if idle && !restarted && !complete && (len(s.rungs) <= 1 || s.info.Duration <= 0 || s.liveInput) {
				restarted = true
				if err := s.startRunForLocked(s.lastReq, s.failures > 0, "dash manifest"); err == nil {
					idle = false
				}
			}
			if s.info.Duration <= 0 {
				complete = complete && s.commonDoneLocked() >= 0
			}
			if complete || timedOut || (idle && s.info.Duration > 0) {
				body = s.renderMPDLocked()
				if complete && s.info.Duration > 0 {
					s.dash.mpd = body
				}
			}
		}
		if body != nil {
			s.mu.Unlock()
			h := w.Header()
			h.Set("Content-Type", "application/dash+xml")
			h.Set("Cache-Control", "no-cache")
			h.Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
			return
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		case <-wait.C:
			timedOut = true
		}
	}
}

// commonDoneLocked is the highest segment finished on a video rendition and
// every audio track. Only one video rung is produced at a time: pinning the
// live edge to the primary would freeze it after a client selects a lower rung.
// Other video renditions remain lazy and are produced when requested.
func (s *Session) commonDoneLocked() int {
	hasVideo := false
	for _, rep := range s.dash.reps {
		hasVideo = hasVideo || rep.Kind == "video"
	}
	common := -1
	for i, rep := range s.dash.reps {
		if i >= len(s.done) || (hasVideo && rep.Kind != "video") {
			continue
		}
		for n, done := range s.done[i] {
			if !done || n <= common {
				continue
			}
			complete := true
			for a, track := range s.dash.reps {
				if track.Kind == "audio" && (a >= len(s.done) || !s.done[a][n]) {
					complete = false
					break
				}
			}
			if complete {
				common = n
			}
		}
	}
	return common
}

// renderMPDLocked renders the MPD with the best codecs known.
func (s *Session) renderMPDLocked() []byte {
	reps := append([]dashRep(nil), s.dash.reps...)
	for i := range reps {
		if c := s.dash.codecs[i]; c != "" {
			reps[i].Codecs = c
		}
	}
	dur := s.info.Duration
	if s.plan != nil && s.plan.span > 0 {
		dur = s.plan.span
	}
	if dur <= 0 {
		s.log.Warn("using growing DASH manifest", "reason", "source duration unavailable")
		return renderLiveMPD(s.created, time.Now(), s.segDur, s.commonDoneLocked(), s.dash.initURL, s.dash.mediaURL, reps)
	}
	minBuffer := s.segDur
	if s.dash.pms {
		minBuffer = 2 * s.segDur
	}
	return renderMPDWithPlan(dur, s.segDur, minBuffer, s.plan, s.dash.initURL, s.dash.mediaURL, reps)
}

// ServeInit implements core.DASHSession: representation rep's
// initialization segment.
func (s *Session) ServeInit(w http.ResponseWriter, r *http.Request, rep int) {
	s.serveInit(w, r, rep)
}

// ServeMedia implements core.DASHSession: media segment n of
// representation rep. Each segment starts with its run's init segment, so
// segments from different ffmpeg runs (seeks, failovers) play back to back.
func (s *Session) ServeMedia(w http.ResponseWriter, r *http.Request, rep, n int) {
	s.serveSegment(w, r, rep, n)
}

// dashCollectBatch is the nominal 1-second chunk count for one bounded
// collectDASH pass; dashCollectMediaWindow balances tracks by media time
// instead of raw chunk count so a 2 s copied-video GOP track cannot outrun a
// 1 s AAC audio track. dashCollectMaxChunks caps chunks per track per pass as
// a safety ceiling against unusually short chunks. The final pass after
// ffmpeg exits is unbounded.
const (
	dashCollectBatch       = 32
	dashCollectMediaWindow = dashCollectBatch * time.Second
	dashCollectMaxChunks   = 4 * dashCollectBatch
)

// collectDASH picks up the chunks a DASH run has finished, regroups them
// into numbered segments and publishes those. exited is true for the final
// pass after ffmpeg exited.
func (s *Session) collectDASH(r *run, exited bool) {
	s.mu.Lock()
	skip := s.closed || (r.stopped && s.cur != r)
	stopped := r.stopped
	eof := !skip && exited && !stopped && r.proc.Err() == nil
	publishCap := -1
	if !exited && s.cur == r && s.m != nil && (s.m.opts.maxAhead > 0 || s.speculative) && s.info.Duration > 0 && s.segDur > 0 {
		publishCap = s.lastReq + s.aheadLimit() + 1
		if publishCap < r.startSeg {
			publishCap = r.startSeg
		}
	}
	runStart := s.base + r.spec.Start
	chunkDur := r.spec.ChunkDur
	if chunkDur <= 0 {
		chunkDur = dashChunkDuration
	}
	s.mu.Unlock()
	if skip {
		return
	}
	dr := r.dash
	if dr.failed {
		return
	}
	var targetEnd time.Duration
	if !exited {
		var minEnd time.Duration
		for i, tr := range dr.tracks {
			pos := runStart
			if tr.hasMedia {
				pos = tr.mediaEnd
			}
			if i == 0 || pos < minEnd {
				minEnd = pos
			}
		}
		targetEnd = minEnd + dashCollectMediaWindow
	}
	type initPub struct {
		t    int
		file string
		info trackInit
	}
	type segPub struct {
		t    int // session track
		mt   int // muxer track (index into dr.tracks)
		out  slotOut
		file string
	}
	var inits []initPub
	var segs []segPub
	for t, tr := range dr.tracks {
		st := t
		if t < len(dr.sessionTrack) {
			st = dr.sessionTrack[t]
		}
		tr.drained = false
		for consumed := 0; ; consumed++ {
			if !exited {
				if consumed >= dashCollectMaxChunks {
					break
				}
				if tr.hasMedia && tr.mediaEnd >= targetEnd {
					break
				}
				if publishCap >= 0 && tr.sl.count > 0 && tr.sl.last >= publishCap {
					break
				}
			}
			chunk := filepath.Join(dr.dir, dashChunkFile(tr.rep, tr.next))
			if _, err := os.Stat(chunk); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					tr.drained = true
				}
				break
			}
			if tr.init == nil {
				// The muxer writes the init segment before the first chunk.
				b, err := readMuxOutput(filepath.Join(dr.dir, dashInitFile(tr.rep)))
				if err != nil {
					break
				}
				info, err := parseInit(b)
				if err != nil {
					s.log.Warn("unreadable DASH init segment", "run", r.seq, "rep", tr.rep, "err", err)
					break
				}
				tr.init, tr.info = b, info
			}
			if !tr.published {
				tr.published = true
				name := fmt.Sprintf("init-%d-%d.mp4", st, r.seq)
				if err := os.WriteFile(filepath.Join(s.dir, name), tr.init, 0o644); err == nil {
					inits = append(inits, initPub{t: st, file: name, info: tr.info})
				}
			}
			c, err := scanChunk(chunk, tr.info)
			if err != nil {
				// Never skip unreadable media: Windows sharing violations
				// can be transient, and advancing next would create a hole.
				// Retry on the next poll; a final failure uses normal run
				// retry/failover rather than publishing a broken timeline.
				if exited {
					s.failDASHRun(r, fmt.Errorf("read DASH rep %d chunk %d: %w", tr.rep, tr.next, err))
					return
				}
				break
			}
			tr.next++
			if !tr.hasMedia || c.end > tr.mediaEnd {
				tr.mediaEnd = c.end
				tr.hasMedia = true
			}
			if d := c.end - c.start; d > 0 {
				tr.chunkDurSum += d
				tr.chunkCount++
			}
			if tr.sl.index != nil && r.startSeg > 0 && !tr.sl.started &&
				c.start < r.spec.Start-tr.sl.tol && c.end <= r.spec.Start+tr.sl.tol {
				// Pure seek preroll ending at or before this run's planned
				// segment start; drop it so sought segments match the MPD.
				s.removals.remove(c.path)
				continue
			}
			for _, o := range tr.sl.add(c) {
				segs = append(segs, segPub{t: st, mt: t, out: o})
			}
		}
		if exited {
			for _, o := range tr.sl.flush(eof) {
				segs = append(segs, segPub{t: st, mt: t, out: o})
			}
		}
	}
	var backlogBytes int64
	diskSeg, hasDiskSeg := -1, false
	if !exited {
		backlogBytes, diskSeg, hasDiskSeg = scanDASHBacklog(dr, chunkDur, runStart)
	}
	// Assemble segment files outside the lock.
	for i := range segs {
		sp := &segs[i]
		if sp.out.alias >= 0 {
			continue
		}
		file := filepath.Join(dr.dir, fmt.Sprintf("seg-%d-%05d.m4s", sp.t, sp.out.n))
		if err := writeSegmentFile(file, dr.tracks[sp.mt].init, sp.out.chunks); err != nil {
			s.log.Warn("assemble DASH segment", "segment", sp.out.n, "rep", sp.t, "err", err)
			continue
		}
		sp.file = file
		for _, c := range sp.out.chunks {
			s.removals.remove(c.path)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (r.stopped && s.cur != r) {
		for _, in := range inits {
			_ = os.Remove(filepath.Join(s.dir, in.file))
		}
		return
	}
	changed := false
	for _, in := range inits {
		if s.cur != r && in.t == s.audioTrackLocked() {
			continue
		}
		old := s.dash.inits[in.t]
		s.dash.inits[in.t] = in.file
		s.dash.codecs[in.t] = in.info.Codecs
		if old != "" && old != in.file {
			// A request may still hold it open (Windows refuses the
			// delete then): retry later rather than leak it.
			s.removals.remove(filepath.Join(s.dir, old))
		}
		changed = true
	}
	for _, sp := range segs {
		if s.cur != r && sp.t == s.audioTrackLocked() {
			if sp.file != "" {
				s.removals.remove(sp.file)
			}
			continue
		}
		done := s.done[sp.t]
		n := sp.out.n
		if done[n] || (r.planEnd > 0 && n >= r.planEnd) {
			// An earlier run's copy is as good (or past this run's forced-key
			// boundary); don't keep this one on disk until the run exits.
			if sp.file != "" {
				s.removals.remove(sp.file)
			}
			continue
		}
		dst := s.trackSegPath(sp.t, n)
		if sp.out.alias >= 0 {
			if !done[sp.out.alias] || linkOrCopy(s.trackSegPath(sp.t, sp.out.alias), dst) != nil {
				continue
			}
		} else if sp.file == "" || os.Rename(sp.file, dst) != nil {
			if sp.file != "" {
				s.removals.remove(sp.file)
			}
			continue
		}
		done[n] = true
		changed = true
	}
	produced, maxDone := -1, -1
	leadDone, hasLead := -1, false
	for _, tr := range dr.tracks {
		slLast := tr.sl.last
		slCount := tr.sl.count
		if r.planEnd > 0 && slLast >= r.planEnd {
			slLast = r.planEnd - 1
			if limit := r.planEnd - r.startSeg; limit >= 0 && slCount > limit {
				slCount = limit
			}
		}
		if slCount > 0 && (!hasLead || slLast > leadDone) {
			leadDone, hasLead = slLast, true
		}
		if produced < 0 || slCount < produced {
			produced, maxDone = slCount, slLast
		} else if slCount == produced && slLast < maxDone {
			maxDone = slLast
		}
	}
	if hasLead && (!r.hasLead || leadDone > r.leadDone) {
		r.leadDone, r.hasLead = leadDone, true
	}
	r.dashDiskSeg, r.hasDashDiskSeg = diskSeg, hasDiskSeg
	r.dashBacklogBytes = backlogBytes
	r.hasMinDrained = false
	for _, tr := range dr.tracks {
		if tr.drained {
			if !r.hasMinDrained || tr.sl.last < r.minDrainedSeg {
				r.minDrainedSeg = tr.sl.last
				r.hasMinDrained = true
			}
		}
	}
	if produced > r.produced {
		if r.produced == 0 {
			s.log.Info("first segment ready", "run", r.seq, "segment", r.startSeg, "after", time.Since(r.started))
		}
		r.produced, r.maxDone = produced, maxDone
		changed = true
	}
	if changed {
		s.advanceHandoffLocked(r)
		if r.produced > 0 && s.cur == r {
			if r.rtx && r.rtxConfirmed.Load() {
				s.decision.Enhancement.State = "running"
				s.decision.Enhancement.Reason = "NVEncC NGX VSR produced enhanced segments"
			}
			s.failures = 0
			s.failErr = nil
		}
		s.throttleLocked()
		s.chainOrHoldLocked(r)
		s.broadcastLocked()
	} else if s.cur == r {
		s.throttleLocked()
	}
}

func parseDASHChunkName(name string) (rep, num int, ok bool) {
	if !strings.HasPrefix(name, "chunk-") || !strings.HasSuffix(name, ".m4s") {
		return 0, 0, false
	}
	body := name[len("chunk-") : len(name)-len(".m4s")]
	repStr, numStr, found := strings.Cut(body, "-")
	if !found {
		return 0, 0, false
	}
	rep, err1 := strconv.Atoi(repStr)
	num, err2 := strconv.Atoi(numStr)
	if err1 != nil || err2 != nil || rep < 0 || num <= 0 {
		return 0, 0, false
	}
	return rep, num, true
}

// scanDASHBacklog measures uncollected chunk bytes and estimates the furthest
// segment slot already written to dr.dir when any track stopped early (hitting
// the media-time window, publish cap, or chunk ceiling). When all tracks
// drained to ENOENT it skips the directory scan.
func scanDASHBacklog(dr *dashRun, chunkDur, runStart time.Duration) (backlogBytes int64, diskSeg int, hasDiskSeg bool) {
	allDrained := true
	for _, tr := range dr.tracks {
		if !tr.drained {
			allDrained = false
			break
		}
	}
	if allDrained {
		return 0, -1, false
	}
	entries, err := os.ReadDir(dr.dir)
	if err != nil {
		return 0, -1, false
	}
	maxChunk := make([]int, len(dr.tracks))
	for _, e := range entries {
		rep, num, ok := parseDASHChunkName(e.Name())
		if !ok {
			continue
		}
		idx := -1
		for i, tr := range dr.tracks {
			if tr.rep == rep {
				idx = i
				break
			}
		}
		if idx < 0 || num < dr.tracks[idx].next {
			continue
		}
		if info, err := e.Info(); err == nil {
			backlogBytes += info.Size()
		}
		if num > maxChunk[idx] {
			maxChunk[idx] = num
		}
	}
	diskSeg = -1
	for i, tr := range dr.tracks {
		if maxChunk[i] < tr.next {
			continue
		}
		extra := maxChunk[i] - tr.next + 1
		avgDur := chunkDur
		if tr.chunkCount > 0 && tr.chunkDurSum > 0 {
			avgDur = tr.chunkDurSum / time.Duration(tr.chunkCount)
		}
		base := runStart
		if tr.hasMedia {
			base = tr.mediaEnd
		}
		lastStart := base + time.Duration(extra-1)*avgDur
		seg := tr.sl.slotOf(lastStart)
		if !hasDiskSeg || seg > diskSeg {
			diskSeg, hasDiskSeg = seg, true
		}
	}
	return backlogBytes, diskSeg, hasDiskSeg
}

func (s *Session) failDASHRun(r *run, err error) {
	r.dash.failed = true
	s.log.Warn("DASH run output unusable", "run", r.seq, "err", err)
	r.stderr.add(err.Error())
	s.mu.Lock()
	r.proc = failedProc{Process: r.proc, err: err}
	p := r.proc
	s.mu.Unlock()
	p.Stop()
}

// writeSegmentFile writes a self-initializing segment to path.
func writeSegmentFile(path string, init []byte, chunks []fmp4Chunk) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := writeSegment(f, init, chunks); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// linkOrCopy makes dst a copy of src (a hard link where possible).
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
