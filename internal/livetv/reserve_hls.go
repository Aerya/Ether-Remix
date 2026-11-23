package livetv

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// An HLS provider keeps a backlog of segments in its live playlist,
// typically 12–30 s. Steady starts its decoder in that backlog
// (live_start_index 0 on the window the relay serves), so the reserve costs
// no tune time: the decoder reads at real time behind the live edge, and a
// provider stall plays out of segments the provider still lists.
//
// For a Steady grant the relay trims each media playlist it serves so its
// first segment sits at the reserve depth: the preset's target, or the
// provider's backlog less its oldest segment (which may expire before the
// decoder reaches it), whichever is smaller. After the decoder starts, the
// window follows one segment behind the last segment it fetched, so a
// decoder restarted on the same grant resumes where the old one stopped.
//
// hlsInput also measures the provider's health: whether the playlist keeps
// advancing on schedule. For a Steady grant it polls the playlist itself
// when the decoder is busy in the backlog and not reloading, so a stall is
// seen as soon as it happens, while the reserve still plays.

// hlsSegment is one media segment of a provider playlist.
type hlsSegment struct {
	seq       int64
	dur       time.Duration
	url       string    // absolute upstream URL
	pdt       time.Time // EXT-X-PROGRAM-DATE-TIME (explicit or propagated within span)
	hasPDT    bool
	mapURI    string // active EXT-X-MAP URI, resolved against playlist base
	encrypted bool   // active EXT-X-KEY with METHOD != NONE
	disc      bool   // EXT-X-DISCONTINUITY immediately before this segment
}

// hlsMedia is a parsed live media playlist, kept as blocks of lines so a
// window can be cut from it without disturbing anything else.
type hlsMedia struct {
	header []string   // lines before the first segment's tags
	blocks [][]string // each segment's tag lines followed by its URI line
	segs   []hlsSegment
	target time.Duration
	seq    int64 // EXT-X-MEDIA-SEQUENCE
	disc   int64 // EXT-X-DISCONTINUITY-SEQUENCE
	ended  bool
	master bool
	tail   []string // lines after the last URI
}

// hlsSegmentTag reports tags that belong to the segment that follows them.
func hlsSegmentTag(line string) bool {
	for _, p := range []string{"#EXTINF", "#EXT-X-BYTERANGE", "#EXT-X-DISCONTINUITY", "#EXT-X-KEY", "#EXT-X-MAP",
		"#EXT-X-PROGRAM-DATE-TIME", "#EXT-X-GAP", "#EXT-X-BITRATE", "#EXT-X-DATERANGE", "#EXT-X-CUE", "#EXT-OATCLS", "#EXT-X-SCTE35", "#EXT-X-PART:"} {
		if strings.HasPrefix(line, p) && !strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE") {
			return true
		}
	}
	return false
}

func parseHLSPDT(raw string) (time.Time, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return time.Time{}, false
	}
	for _, candidate := range []string{v, strings.ToUpper(v)} {
		if t, err := time.Parse(time.RFC3339Nano, candidate); err == nil {
			return t, true
		}
		if t, err := time.Parse("2006-01-02T15:04:05.999999999Z0700", candidate); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseHLSMedia(b []byte, base *url.URL) *hlsMedia {
	m := &hlsMedia{}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var cur []string
	inSegs := false
	var dur time.Duration
	var pendingPDT time.Time
	var hasPendingPDT, pendingDisc, encrypted bool
	var activeMap string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			if inSegs {
				cur = append(cur, raw)
			} else {
				m.header = append(m.header, raw)
			}
			continue
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF"):
			m.master = true
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			if n, err := strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64); err == nil && n > 0 {
				m.target = time.Duration(n * float64(time.Second))
			}
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			m.seq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			m.disc, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			pendingDisc = true
		case line == "#EXT-X-ENDLIST":
			m.ended = true
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			if t, ok := parseHLSPDT(strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:")); ok {
				pendingPDT, hasPendingPDT = t, true
			}
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			a := hlsAttrs(strings.TrimPrefix(line, "#EXT-X-MAP:"))
			if u, ok := resolveHLS(base, a["URI"]); ok {
				activeMap = u
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			a := hlsAttrs(strings.TrimPrefix(line, "#EXT-X-KEY:"))
			method := strings.ToUpper(a["METHOD"])
			encrypted = method != "" && method != "NONE"
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.SplitN(strings.TrimPrefix(line, "#EXTINF:"), ",", 2)[0]
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > 0 && f < 600 {
				dur = time.Duration(f * float64(time.Second))
			}
		}
		if !strings.HasPrefix(line, "#") {
			cur = append(cur, raw)
			u, err := url.Parse(line)
			abs := line
			if err == nil && base != nil {
				abs = base.ResolveReference(u).String()
			}
			m.segs = append(m.segs, hlsSegment{
				seq:       m.seq + int64(len(m.blocks)),
				dur:       dur,
				url:       abs,
				pdt:       pendingPDT,
				hasPDT:    hasPendingPDT,
				mapURI:    activeMap,
				encrypted: encrypted,
				disc:      pendingDisc,
			})
			m.blocks = append(m.blocks, cur)
			cur, dur = nil, 0
			pendingPDT, hasPendingPDT, pendingDisc = time.Time{}, false, false
			inSegs = true
			continue
		}
		if !inSegs && !hlsSegmentTag(line) {
			m.header = append(m.header, raw)
			continue
		}
		inSegs = true
		cur = append(cur, raw)
	}
	m.tail = cur
	// Propagate explicit program date-time across contiguous segments within
	// the same discontinuity span (RFC 8216 section 4.3.2.6).
	for i := 1; i < len(m.segs); i++ {
		if !m.segs[i].hasPDT && !m.segs[i].disc && m.segs[i-1].hasPDT && m.segs[i-1].dur > 0 {
			m.segs[i].pdt = m.segs[i-1].pdt.Add(m.segs[i-1].dur)
			m.segs[i].hasPDT = true
		}
	}
	for i := len(m.segs) - 2; i >= 0; i-- {
		if !m.segs[i].hasPDT && !m.segs[i+1].disc && m.segs[i+1].hasPDT && m.segs[i].dur > 0 {
			m.segs[i].pdt = m.segs[i+1].pdt.Add(-m.segs[i].dur)
			m.segs[i].hasPDT = true
		}
	}
	return m
}

// live reports a live media playlist the window applies to.
func (m *hlsMedia) live() bool { return !m.master && !m.ended && len(m.segs) > 0 }

// startFor picks the first segment for a reader that should play d behind
// the live edge.
func (m *hlsMedia) startFor(d time.Duration) int {
	n := len(m.segs)
	if n < 2 {
		return 0
	}
	// Never start on the provider's oldest segment when it can be avoided:
	// it is the next to leave the playlist and may 404 before it is read.
	lowest := 0
	if n >= 3 {
		lowest = 1
	}
	var sum time.Duration
	for i := n - 1; i > lowest; i-- {
		sum += m.segs[i].dur
		if sum >= d {
			return i
		}
	}
	return lowest
}

// window renders the playlist from the first segment with seq >= floor.
// Dropped segments' discontinuities move into the discontinuity sequence,
// and the latest key and map tags they carried stay in force.
func (m *hlsMedia) window(floor int64) []byte {
	return m.windowLimit(floor, 0)
}

// windowLimit is window with an optional cap on the number of segments
// emitted. When the tail is truncated, trailing partial-segment / preload
// hints belonging to omitted segments are dropped too.
func (m *hlsMedia) windowLimit(floor int64, maxSegs int) []byte {
	drop := 0
	for drop < len(m.segs) && m.segs[drop].seq < floor {
		drop++
	}
	if drop >= len(m.segs) {
		return nil
	}
	end := len(m.segs)
	truncatedTail := false
	if maxSegs > 0 && end-drop > maxSegs {
		end = drop + maxSegs
		truncatedTail = true
	}
	if drop == 0 && !truncatedTail {
		return nil
	}
	var key, mapTag string
	disc := m.disc
	for _, block := range m.blocks[:drop] {
		for _, raw := range block {
			line := strings.TrimSpace(raw)
			switch {
			case line == "#EXT-X-DISCONTINUITY":
				disc++
			case strings.HasPrefix(line, "#EXT-X-KEY"):
				key = raw
			case strings.HasPrefix(line, "#EXT-X-MAP"):
				mapTag = raw
			}
		}
	}
	first := m.blocks[drop]
	for _, raw := range first {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#EXT-X-KEY") {
			key = ""
		}
		if strings.HasPrefix(line, "#EXT-X-MAP") {
			mapTag = ""
		}
	}
	var out []string
	sawDisc := false
	for _, raw := range m.header {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			raw = "#EXT-X-MEDIA-SEQUENCE:" + strconv.FormatInt(m.segs[drop].seq, 10)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			raw = "#EXT-X-DISCONTINUITY-SEQUENCE:" + strconv.FormatInt(disc, 10)
			sawDisc = true
		}
		out = append(out, raw)
	}
	if !sawDisc && disc != 0 {
		out = append(out, "#EXT-X-DISCONTINUITY-SEQUENCE:"+strconv.FormatInt(disc, 10))
	}
	if !hasMediaSequence(m.header) {
		out = append(out, "#EXT-X-MEDIA-SEQUENCE:"+strconv.FormatInt(m.segs[drop].seq, 10))
	}
	if key != "" {
		out = append(out, key)
	}
	if mapTag != "" {
		out = append(out, mapTag)
	}
	for _, block := range m.blocks[drop:end] {
		out = append(out, block...)
	}
	if !truncatedTail {
		out = append(out, m.tail...)
	}
	return []byte(strings.Join(out, "\n"))
}

func hasMediaSequence(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#EXT-X-MEDIA-SEQUENCE:") {
			return true
		}
	}
	return false
}

// hlsDemuxAlign coordinates starting-window alignment for separate video and
// audio playlists declared by a demuxed HLS master.
type hlsDemuxAlign struct {
	videoURL  string
	audioURLs []string
	gen       uint64
	pending   bool
	inFlight  *hlsAlignCall
	snap      map[string]hlsAlignedSnap
	anchorPDT time.Time
	hasAnchor bool
}

type hlsAlignCall struct {
	done chan struct{}
}

type hlsAlignedSnap struct {
	body []byte
	base *url.URL
	at   time.Time
}

type hlsCachedResource struct {
	body     []byte
	ctype    string
	reusable bool // init maps may be read repeatedly; media segments are one-shot
	at       time.Time
}

// hlsInput is one grant's view of its provider playlists.
type hlsInput struct {
	g       *inputGrant
	mu      sync.Mutex
	lists   map[string]*hlsList // by upstream playlist URL as requested
	segs    map[string]int64    // upstream segment URL -> sequence
	polling map[string]bool
	demux   hlsDemuxAlign
	// Startup segment/init cache populated by segment-timestamp alignment so
	// ffmpeg does not download probed init maps or segments twice.
	startupCache map[string]hlsCachedResource
	// Steady prefetch (reserve_hls_prefetch.go).
	fetches           map[string]*hlsFetch
	queue             []*hlsFetch // waiting, in playlist order
	fetching, helping bool        // the own fetcher and the extra-slot helper are running
	held              int
	// Media served but not yet reported to the source history.
	pendMedia time.Duration
}

// hlsList is the state of one provider media playlist.
type hlsList struct {
	raw         string
	target      time.Duration
	last        []hlsSegment
	newest      int64 // highest sequence seen
	advancedAt  time.Time
	fetchedAt   time.Time
	failures    int // consecutive failed fetches
	floor       int64
	firstServed int64 // first segment sequence the reader fetched (-1 none)
	served      int64 // highest sequence the reader fetched
	stalled     bool
	failed      bool
	initialized bool
}

func (g *inputGrant) hlsIfAny() *hlsInput {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hls
}

func (g *inputGrant) hlsState() *hlsInput {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hls == nil {
		g.hls = &hlsInput{g: g, lists: map[string]*hlsList{}, segs: map[string]int64{}, polling: map[string]bool{}}
	}
	return g.hls
}

// noteDemuxedMaster records a demuxed video/audio playlist pair from a served
// master playlist and arms paired window alignment for the reader's upcoming
// variant fetches.
func (h *hlsInput) noteDemuxedMaster(videoURL string, audioURLs []string) {
	if videoURL == "" || len(audioURLs) == 0 {
		return
	}
	h.mu.Lock()
	h.demux.gen++
	h.demux.videoURL = videoURL
	h.demux.audioURLs = append([]string(nil), audioURLs...)
	h.demux.pending = true
	h.demux.snap = nil
	h.demux.hasAnchor = false
	h.mu.Unlock()
}

func (h *hlsInput) isDemuxedAudioLocked(raw string) bool {
	for _, u := range h.demux.audioURLs {
		if u == raw {
			return true
		}
	}
	return false
}

func (h *hlsInput) isDemuxedMemberLocked(raw string) bool {
	if raw == "" || h.demux.videoURL == "" || len(h.demux.audioURLs) == 0 {
		return false
	}
	return raw == h.demux.videoURL || h.isDemuxedAudioLocked(raw)
}

// putStartupCache stores a small init map or segment fetched during startup
// timestamp alignment for reuse by the reader that immediately follows.
func (h *hlsInput) putStartupCache(raw string, body []byte, ctype string, reusable bool) {
	if raw == "" || len(body) == 0 || len(body) > hlsSegmentLimit {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.startupCache == nil {
		h.startupCache = map[string]hlsCachedResource{}
	}
	if len(h.startupCache) >= 8 {
		// Evict expired or non-reusable entries first.
		now := time.Now()
		for k, v := range h.startupCache {
			if now.Sub(v.at) > 30*time.Second || !v.reusable {
				delete(h.startupCache, k)
			}
		}
		if len(h.startupCache) >= 8 {
			h.startupCache = map[string]hlsCachedResource{}
		}
	}
	h.startupCache[raw] = hlsCachedResource{
		body:     body,
		ctype:    ctype,
		reusable: reusable,
		at:       time.Now(),
	}
}

func (h *hlsInput) getStartupCache(raw string, consume bool) (hlsCachedResource, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	res, ok := h.startupCache[raw]
	if !ok {
		return hlsCachedResource{}, false
	}
	if time.Since(res.at) > 30*time.Second {
		delete(h.startupCache, raw)
		return hlsCachedResource{}, false
	}
	if consume && !res.reusable {
		delete(h.startupCache, raw)
	}
	return res, true
}

func (h *hlsInput) ensureListLocked(raw string) *hlsList {
	l := h.lists[raw]
	if l == nil {
		l = &hlsList{raw: raw, served: -1, firstServed: -1, floor: -1}
		h.lists[raw] = l
	}
	return l
}

func (h *hlsInput) recordListLocked(l *hlsList, m *hlsMedia, now time.Time) {
	l.target = m.target
	if l.target <= 0 {
		l.target = 6 * time.Second
	}
	l.fetchedAt = now
	l.failures = 0
	newest := m.segs[len(m.segs)-1].seq
	if !l.initialized || newest > l.newest {
		l.newest, l.advancedAt = newest, now
	}
	l.last = m.segs
	for _, sg := range m.segs {
		h.segs[sg.url] = sg.seq
	}
	if len(h.segs) > 4*relayResourceLimit {
		h.segs = map[string]int64{}
		for _, list := range h.lists {
			for _, sg := range list.last {
				h.segs[sg.url] = sg.seq
			}
		}
	}
}

// media handles one fetched media playlist for the grant: it records health
// and returns the playlist to serve (trimmed to the reserve window for a
// Steady grant, or to an aligned demuxed floor).
func (h *hlsInput) media(raw string, body []byte, base *url.URL) ([]byte, []UpstreamEvent) {
	m := parseHLSMedia(body, base)
	if !m.live() {
		return body, nil
	}
	now := time.Now()
	h.mu.Lock()
	l := h.ensureListLocked(raw)
	h.recordListLocked(l, m, now)
	reserve := h.g.reserve
	if !l.initialized {
		if h.demux.hasAnchor && h.isDemuxedAudioLocked(raw) {
			if idx, ok := closestSegmentToPDT(m, h.demux.anchorPDT); ok {
				l.floor = m.segs[idx].seq
			}
		}
		if l.floor < 0 && reserve > 0 {
			l.floor = m.segs[m.startFor(h.g.joinDepth())].seq
		}
	}
	if l.floor >= 0 && l.served >= 0 && l.served-1 > l.floor {
		l.floor = l.served - 1
	}
	l.initialized = true
	events := h.transitionsLocked(l, now)
	h.prefetchLocked(l)
	h.publishLocked()
	floor := l.floor
	startPoll := reserve > 0 && !h.polling[raw]
	if startPoll {
		h.polling[raw] = true
	}
	h.mu.Unlock()
	if startPoll {
		go h.poll(raw)
	}
	if floor >= 0 {
		if out := m.window(floor); out != nil {
			return out, events
		}
	}
	return body, events
}

const (
	hlsAlignGoodSkew = 50 * time.Millisecond
	hlsAlignTieBand  = 25 * time.Millisecond
)

// hlsMediaPDTTimes extracts relative segment start times for a video/audio
// playlist pair from EXT-X-PROGRAM-DATE-TIME tags. ok is false if either
// playlist lacks usable timestamps or the two timelines do not overlap.
func hlsMediaPDTTimes(vm, am *hlsMedia) (vTimes []time.Duration, vHas []bool, aTimes []time.Duration, aHas []bool, ok bool) {
	var epoch time.Time
	haveEpoch := false
	for _, sg := range vm.segs {
		if sg.hasPDT && (!haveEpoch || sg.pdt.Before(epoch)) {
			epoch, haveEpoch = sg.pdt, true
		}
	}
	for _, sg := range am.segs {
		if sg.hasPDT && (!haveEpoch || sg.pdt.Before(epoch)) {
			epoch, haveEpoch = sg.pdt, true
		}
	}
	if !haveEpoch {
		return nil, nil, nil, nil, false
	}
	vTimes = make([]time.Duration, len(vm.segs))
	vHas = make([]bool, len(vm.segs))
	aTimes = make([]time.Duration, len(am.segs))
	aHas = make([]bool, len(am.segs))
	for i, sg := range vm.segs {
		if sg.hasPDT {
			vTimes[i], vHas[i] = sg.pdt.Sub(epoch), true
		}
	}
	for i, sg := range am.segs {
		if sg.hasPDT {
			aTimes[i], aHas[i] = sg.pdt.Sub(epoch), true
		}
	}
	if !timelinesOverlap(vm, vTimes, vHas, am, aTimes, aHas) {
		return nil, nil, nil, nil, false
	}
	return vTimes, vHas, aTimes, aHas, true
}

// buildAnchorTimeline extrapolates segment start times around one probed
// anchor segment within its continuity span using EXTINF durations.
func buildAnchorTimeline(m *hlsMedia, anchor int, anchorTime time.Duration) ([]time.Duration, []bool) {
	times := make([]time.Duration, len(m.segs))
	has := make([]bool, len(m.segs))
	if anchor < 0 || anchor >= len(m.segs) {
		return times, has
	}
	times[anchor], has[anchor] = anchorTime, true
	for i := anchor + 1; i < len(m.segs); i++ {
		if m.segs[i].disc || m.segs[i-1].dur <= 0 {
			break
		}
		times[i] = times[i-1] + m.segs[i-1].dur
		has[i] = true
	}
	for i := anchor - 1; i >= 0; i-- {
		if m.segs[i+1].disc || m.segs[i].dur <= 0 {
			break
		}
		times[i] = times[i+1] - m.segs[i].dur
		has[i] = true
	}
	return times, has
}

func timelineSpan(m *hlsMedia, times []time.Duration, has []bool) (minStart, maxEnd time.Duration, ok bool) {
	for i, sg := range m.segs {
		if !has[i] {
			continue
		}
		dur := sg.dur
		if dur <= 0 {
			dur = m.target
		}
		if dur <= 0 {
			dur = time.Second
		}
		end := times[i] + dur
		if !ok {
			minStart, maxEnd, ok = times[i], end, true
		} else {
			if times[i] < minStart {
				minStart = times[i]
			}
			if end > maxEnd {
				maxEnd = end
			}
		}
	}
	return minStart, maxEnd, ok
}

func timelinesOverlap(vm *hlsMedia, vTimes []time.Duration, vHas []bool, am *hlsMedia, aTimes []time.Duration, aHas []bool) bool {
	vMin, vMax, vOK := timelineSpan(vm, vTimes, vHas)
	aMin, aMax, aOK := timelineSpan(am, aTimes, aHas)
	if !vOK || !aOK {
		return false
	}
	return min(vMax, aMax) > max(vMin, aMin)
}

func sumLastSegments(segs []hlsSegment, n int, fallback time.Duration) time.Duration {
	if len(segs) == 0 || n <= 0 {
		return fallback
	}
	start := max(0, len(segs)-n)
	var sum time.Duration
	for i := start; i < len(segs); i++ {
		d := segs[i].dur
		if d <= 0 {
			d = fallback
		}
		sum += d
	}
	return sum
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// chooseAlignedStarts selects starting segment indexes (vIdx, aIdx) in vm and
// am so both playlists begin at as close to the same media time as possible,
// near desiredT, preferring starts that skip the expiring oldest segment and
// leave at least two segments of runway on both playlists.
func chooseAlignedStarts(vm, am *hlsMedia, vTimes []time.Duration, vHas []bool, aTimes []time.Duration, aHas []bool, desiredT, maxEarlier time.Duration) (vIdx, aIdx int, skew time.Duration, ok bool) {
	type cand struct {
		vi, ai     int
		skew       time.Duration
		dist       time.Duration
		runway     bool
		audioFirst bool
	}
	better := func(a, b cand) bool {
		if a.skew > hlsAlignGoodSkew || b.skew > hlsAlignGoodSkew {
			if absDuration(a.skew-b.skew) > hlsAlignTieBand {
				return a.skew < b.skew
			}
		}
		if a.runway != b.runway {
			return a.runway
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		if a.skew != b.skew {
			return a.skew < b.skew
		}
		return a.audioFirst && !b.audioFirst
	}
	search := func(vMin, aMin int, earliest time.Duration, limitEarliest bool) (cand, bool) {
		var best cand
		found := false
		for i := vMin; i < len(vm.segs); i++ {
			if !vHas[i] || (limitEarliest && vTimes[i] < earliest) {
				continue
			}
			for j := aMin; j < len(am.segs); j++ {
				if !aHas[j] || (limitEarliest && aTimes[j] < earliest) {
					continue
				}
				c := cand{
					vi:         i,
					ai:         j,
					skew:       absDuration(vTimes[i] - aTimes[j]),
					dist:       absDuration(vTimes[i] - desiredT),
					runway:     (len(vm.segs)-i >= 2) && (len(am.segs)-j >= 2),
					audioFirst: aTimes[j] <= vTimes[i],
				}
				if !found || better(c, best) {
					best, found = c, true
				}
			}
		}
		return best, found
	}
	vLow, aLow := 0, 0
	if len(vm.segs) >= 3 {
		vLow = 1
	}
	if len(am.segs) >= 3 {
		aLow = 1
	}
	var best cand
	found := false
	if maxEarlier > 0 {
		best, found = search(vLow, aLow, desiredT-maxEarlier, true)
	}
	if !found || best.skew > hlsAlignGoodSkew {
		if wider, ok := search(vLow, aLow, 0, false); ok {
			if !found || (wider.skew <= hlsAlignGoodSkew && best.skew > hlsAlignGoodSkew) || best.skew-wider.skew > hlsAlignTieBand {
				best, found = wider, true
			}
		}
	}
	if !found || best.skew > hlsAlignGoodSkew {
		if full, ok := search(0, 0, 0, false); ok {
			if !found || (full.skew <= hlsAlignGoodSkew && best.skew > hlsAlignGoodSkew) || best.skew-full.skew > 250*time.Millisecond {
				best, found = full, true
			}
		}
	}
	if !found {
		return 0, 0, 0, false
	}
	maxTarget := max(vm.target, am.target)
	if maxTarget <= 0 {
		maxTarget = 6 * time.Second
	}
	if best.skew > maxTarget+2*time.Second {
		return 0, 0, 0, false
	}
	return best.vi, best.ai, best.skew, true
}

func closestSegmentToPDT(m *hlsMedia, anchor time.Time) (int, bool) {
	bestIdx := -1
	var bestDiff time.Duration
	for i, sg := range m.segs {
		if !sg.hasPDT {
			continue
		}
		d := absDuration(sg.pdt.Sub(anchor))
		if bestIdx < 0 || d < bestDiff {
			bestIdx, bestDiff = i, d
		}
	}
	if bestIdx < 0 {
		return 0, false
	}
	maxTarget := m.target
	if maxTarget <= 0 {
		maxTarget = 6 * time.Second
	}
	if bestDiff > maxTarget+2*time.Second {
		return 0, false
	}
	return bestIdx, true
}

func segmentTimeForSeq(m *hlsMedia, times []time.Duration, has []bool, seq int64) (time.Duration, bool) {
	for i, sg := range m.segs {
		if sg.seq == seq && has[i] {
			return times[i], true
		}
	}
	return 0, false
}

// applyDemuxedPair updates both video and audio playlist state atomically,
// aligning their starting floors when comparable timestamps are available.
func (h *hlsInput) applyDemuxedPair(
	vRaw string, vBody []byte, vm *hlsMedia,
	aRaw string, aBody []byte, am *hlsMedia,
	vTimes []time.Duration, vHas []bool,
	aTimes []time.Duration, aHas []bool,
	haveTimes bool,
	source string,
) (vOut, aOut []byte, events []UpstreamEvent) {
	now := time.Now()
	h.mu.Lock()
	vl := h.ensureListLocked(vRaw)
	al := h.ensureListLocked(aRaw)
	h.recordListLocked(vl, vm, now)
	h.recordListLocked(al, am, now)

	reserve := h.g.reserve
	priorStream := func(l *hlsList) bool {
		if !l.initialized || l.served < 0 || l.firstServed < 0 {
			return false
		}
		// A backlog decoder (-live_start_index 0) reads from floor; ffprobe
		// (-live_start_index -3) skips straight to the live edge.
		if l.floor >= 0 && l.firstServed > l.floor+1 {
			return false
		}
		return true
	}
	resuming := reserve > 0 && priorStream(vl) && priorStream(al)

	vMaxSegs, aMaxSegs := 0, 0
	aligned := false
	var alignedSkew time.Duration
	if haveTimes {
		vMin, vMax, vOK := timelineSpan(vm, vTimes, vHas)
		aMin, aMax, aOK := timelineSpan(am, aTimes, aHas)
		if vOK && aOK {
			vLow, aLow := 0, 0
			if len(vm.segs) >= 3 {
				vLow = 1
			}
			if len(am.segs) >= 3 {
				aLow = 1
			}
			commonMin := max(vMin, aMin)
			if vHas[vLow] && aHas[aLow] {
				commonMin = max(vTimes[vLow], aTimes[aLow])
			}
			commonLiveEdge := min(vMax, aMax)

			var desiredT time.Duration
			if resuming {
				vResume, vHave := segmentTimeForSeq(vm, vTimes, vHas, max(vm.segs[0].seq, vl.served-1))
				aResume, aHave := segmentTimeForSeq(am, aTimes, aHas, max(am.segs[0].seq, al.served-1))
				switch {
				case vHave && aHave:
					desiredT = min(vResume, aResume)
				case vHave:
					desiredT = vResume
				case aHave:
					desiredT = aResume
				default:
					desiredT = commonLiveEdge - h.g.joinDepth()
				}
			} else {
				vl.served, vl.firstServed = -1, -1
				al.served, al.firstServed = -1, -1
				var targetDepth time.Duration
				if reserve > 0 {
					targetDepth = h.g.joinDepth()
				} else {
					fallback := max(vm.target, am.target)
					if fallback <= 0 {
						fallback = 2 * time.Second
					}
					targetDepth = sumLastSegments(vm.segs, 3, fallback)
				}
				desiredT = commonLiveEdge - targetDepth
			}
			if desiredT < commonMin {
				desiredT = commonMin
			}
			if desiredT > commonLiveEdge {
				desiredT = commonLiveEdge
			}
			maxTarget := max(vm.target, am.target)
			if maxTarget <= 0 {
				maxTarget = 2 * time.Second
			}
			maxEarlier := max(2*maxTarget, 4*time.Second)
			if vIdx, aIdx, skew, ok := chooseAlignedStarts(vm, am, vTimes, vHas, aTimes, aHas, desiredT, maxEarlier); ok {
				vl.floor = vm.segs[vIdx].seq
				al.floor = am.segs[aIdx].seq
				aligned = true
				alignedSkew = skew
				if vm.segs[vIdx].hasPDT {
					h.demux.anchorPDT = vm.segs[vIdx].pdt
					h.demux.hasAnchor = true
				}
				if reserve == 0 {
					// Without a reserve, ffmpeg opens live HLS at default
					// -live_start_index -3. Capping the initial response to at
					// most 3 segments ensures both playlists begin at index 0;
					// subsequent reloads serve the full advancing window.
					vMaxSegs, aMaxSegs = 3, 3
				}
			}
		}
	}
	if !aligned {
		if reserve > 0 {
			if !vl.initialized {
				vl.floor = vm.segs[vm.startFor(h.g.joinDepth())].seq
			}
			if !al.initialized {
				al.floor = am.segs[am.startFor(h.g.joinDepth())].seq
			}
			if vl.served >= 0 && vl.served-1 > vl.floor {
				vl.floor = vl.served - 1
			}
			if al.served >= 0 && al.served-1 > al.floor {
				al.floor = al.served - 1
			}
		}
	}
	vl.initialized = true
	al.initialized = true
	events = append(events, h.transitionsLocked(vl, now)...)
	events = append(events, h.transitionsLocked(al, now)...)
	h.prefetchLocked(vl)
	h.prefetchLocked(al)
	h.publishLocked()
	vFloor, aFloor := vl.floor, al.floor
	var startPolls []string
	if reserve > 0 {
		if !h.polling[vRaw] {
			h.polling[vRaw] = true
			startPolls = append(startPolls, vRaw)
		}
		if !h.polling[aRaw] {
			h.polling[aRaw] = true
			startPolls = append(startPolls, aRaw)
		}
	}
	log := h.g.log
	h.mu.Unlock()

	for _, u := range startPolls {
		go h.poll(u)
	}
	if aligned && log != nil {
		log.Info("aligned demuxed HLS playlists", "video_seq", vFloor, "audio_seq", aFloor, "skew", alignedSkew.Round(time.Millisecond), "method", source)
	}
	vOut, aOut = vBody, aBody
	if vFloor >= 0 {
		if out := vm.windowLimit(vFloor, vMaxSegs); out != nil {
			vOut = out
		}
	}
	if aFloor >= 0 {
		if out := am.windowLimit(aFloor, aMaxSegs); out != nil {
			aOut = out
		}
	}
	return vOut, aOut, events
}

// failedFetch records a playlist fetch that failed.
func (h *hlsInput) failedFetch(raw string) []UpstreamEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.lists[raw]
	if l == nil {
		return nil
	}
	l.failures++
	events := h.transitionsLocked(l, time.Now())
	h.publishLocked()
	return events
}

// isSegment reports a media segment named by one of the grant's live
// playlists.
func (h *hlsInput) isSegment(raw string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.segs[raw]
	return ok
}

// servedSegment records a segment fetch by the reader.
func (h *hlsInput) servedSegment(raw string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	seq, ok := h.segs[raw]
	if !ok {
		return
	}
	for _, l := range h.lists {
		for _, sg := range l.last {
			if sg.url == raw {
				if l.firstServed < 0 {
					l.firstServed = seq
				}
				if seq > l.served {
					h.pendMedia += sg.dur
					if h.pendMedia >= historyReportEvery {
						h.g.noteEvidence(SourceEvidence{Media: h.pendMedia})
						h.pendMedia = 0
					}
					l.served = seq
					h.evictLocked(l.raw, seq)
					h.prefetchLocked(l)
				}
			}
		}
	}
	h.publishLocked()
}

// lateness is how far past schedule the playlist is: zero while a new
// segment arrives within a target duration of the last.
func (l *hlsList) lateness(now time.Time) time.Duration {
	if l.advancedAt.IsZero() {
		return 0
	}
	return max(0, now.Sub(l.advancedAt)-l.target)
}

func (h *hlsInput) transitionsLocked(l *hlsList, now time.Time) []UpstreamEvent {
	var out []UpstreamEvent
	stalled := l.lateness(now) > l.target/2 || l.failures > 0
	if stalled != l.stalled {
		l.stalled = stalled
		kind := InputRecovered
		if stalled {
			kind = InputStalled
			h.g.noteEvidence(SourceEvidence{Stalls: 1, Media: h.pendMedia})
			h.pendMedia = 0
		}
		out = append(out, UpstreamEvent{Kind: kind, At: now})
	}
	failed := l.failures >= tapFailAfter
	if failed && !l.failed {
		out = append(out, UpstreamEvent{Kind: InputFailed, At: now})
	}
	l.failed = failed
	return out
}

// health aggregates the grant's playlists, from the reader's point of view.
func (h *hlsInput) health() UpstreamHealth {
	h.mu.Lock()
	defer h.mu.Unlock()
	out, _ := h.healthLocked(time.Now())
	return out
}

// publishLocked refreshes the grant's lock-free health snapshot.
func (h *hlsInput) publishLocked() {
	out, due := h.healthLocked(time.Now())
	h.g.snap.Store(&healthSnap{h: out, due: due})
}

// healthLocked also returns when the most overdue playlist's next segment
// was due, from which DataAge (lateness) is derived at read time.
func (h *hlsInput) healthLocked(now time.Time) (UpstreamHealth, time.Time) {
	out := UpstreamHealth{Kind: "hls", Connected: true}
	var due time.Time
	first := true
	for _, l := range h.lists {
		if now.Sub(l.fetchedAt) > 4*l.target && !first {
			continue // a variant the reader no longer loads
		}
		late := l.lateness(now)
		var depth time.Duration
		for _, sg := range l.last {
			if sg.seq > l.served {
				depth += sg.dur
			}
		}
		if first || late > out.DataAge {
			out.DataAge = late
		}
		if !l.advancedAt.IsZero() {
			if at := l.advancedAt.Add(l.target); due.IsZero() || at.Before(due) {
				due = at
			}
		}
		if first || depth < out.ReserveDepth {
			out.ReserveDepth = depth
		}
		out.Stalled = out.Stalled || l.stalled
		out.Failed = out.Failed || l.failed
		out.Connected = out.Connected && l.failures == 0 && !l.stalled
		if h.g.reserve > 0 {
			var backlog time.Duration
			for _, sg := range l.last[min(1, len(l.last)):] {
				backlog += sg.dur
			}
			out.Target = min(h.g.reserve, backlog)
		}
		first = false
	}
	return out, due
}

// poll fetches the playlist on schedule while the reader is busy in the
// backlog and not reloading it, so a provider stall is noticed at once.
// It never fetches segments, and skips a turn whenever the reader itself
// reloaded recently.
func (h *hlsInput) poll(raw string) {
	s := h.g.service
	for {
		h.mu.Lock()
		l := h.lists[raw]
		wait := 6 * time.Second
		if l != nil {
			wait = max(time.Second, l.target/2)
			if since := time.Since(l.fetchedAt); since < wait {
				wait -= since
			}
		}
		h.mu.Unlock()
		select {
		case <-h.g.ctx.Done():
			return
		case <-time.After(wait):
		}
		h.mu.Lock()
		due := l == nil || time.Since(l.fetchedAt) >= max(time.Second, l.target/2)
		h.mu.Unlock()
		if !due || s == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(h.g.ctx, max(2*time.Second, l.target))
		res, err := s.relayFetch(ctx, cancel, h.g, raw, &http.Request{Header: http.Header{}})
		var events []UpstreamEvent
		if err == nil && res.StatusCode == http.StatusOK {
			b, rerr := io.ReadAll(io.LimitReader(res.Body, relayManifestLimit+1))
			base := res.Request.URL
			res.Body.Close()
			if rerr == nil && len(b) <= relayManifestLimit {
				_, events = h.media(raw, b, base)
			} else {
				events = h.failedFetch(raw)
			}
		} else {
			if res != nil {
				if isExpiredStatus(res.StatusCode) {
					h.g.noteExpiredManifest(res.StatusCode)
				}
				res.Body.Close()
			}
			if h.g.ctx.Err() != nil {
				cancel()
				return
			}
			events = h.failedFetch(raw)
		}
		cancel()
		h.deliver(events)
	}
}

func (h *hlsInput) deliver(events []UpstreamEvent) {
	if len(events) == 0 {
		return
	}
	health, _ := h.g.snapHealth()
	for _, ev := range events {
		ev.Health = health
		h.g.notify(ev)
	}
}

// segmentFailed records a segment the reader could not have (an error, or
// skipped as stuck) as damage in the source history.
func (h *hlsInput) segmentFailed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.g.noteEvidence(SourceEvidence{DamageEvents: 1, Media: h.pendMedia})
	h.pendMedia = 0
}

// flushEvidence reports media still pending (the grant is ending).
func (h *hlsInput) flushEvidence() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pendMedia > 0 {
		h.g.noteEvidence(SourceEvidence{Media: h.pendMedia})
		h.pendMedia = 0
	}
}
