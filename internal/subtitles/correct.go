package subtitles

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	correctBackgroundBudget = 12 * time.Second
	correctRetryBase        = 30 * time.Second
	correctRetryMax         = 15 * time.Minute
	correctServeWait        = 2 * time.Second
	sourceReadBudget        = 32 << 20
	manualSource            = "manual"
	defaultSource           = "default"
)

// ReleaseAccess is enough of a playing session to measure a sidecar.
// Reader and URL must belong to that session's lifetime; Enrich must
// not pass a prepare-generation handle. Hold, when set, is taken after
// the speech semaphore so a retired source can close during a failover.
type ReleaseAccess struct {
	Reader       io.ReaderAt
	Size         int64
	URL          string
	Duration     time.Duration
	Info         *core.MediaInfo
	Release      string
	Source       string
	FFmpeg       string
	CheapSpeech  bool
	EmbeddedCues func() [][]Cue
	Done         func()
	Hold         func() (io.ReaderAt, int64, func(), bool)
}

func (a ReleaseAccess) close() {
	if a.Done != nil {
		a.Done()
	}
}

type correction struct {
	Offset     time.Duration `json:"offset"`
	Rate       float64       `json:"rate"`
	Confidence float64       `json:"confidence"`
	Inliers    int           `json:"inliers"`
	Applied    bool          `json:"applied"`
	Measured   bool          `json:"measured"`
	Manual     bool          `json:"manual,omitempty"`
	Method     string        `json:"method,omitempty"`
	At         time.Time     `json:"at"`
	Err        string        `json:"err,omitempty"`
	Attempts   int           `json:"attempts,omitempty"`
	RetryAfter time.Time     `json:"retryAfter,omitempty"`
}

// SourceKey is the on-disk correction key for a release/source id.
// URLs and path-like values are hashed so they never become path
// components. Store and load both go through this function.
func SourceKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "/\\") {
		sum := sha1.Sum([]byte(raw))
		return hex.EncodeToString(sum[:8])
	}
	return sanitizeSource(raw)
}

func sourceKey(access ReleaseAccess) string {
	if s := strings.TrimSpace(access.Source); s != "" {
		return SourceKey(s)
	}
	return ""
}

func sanitizeSource(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 80 {
			break
		}
	}
	if b.Len() == 0 {
		sum := sha1.Sum([]byte(s))
		return hex.EncodeToString(sum[:8])
	}
	return b.String()
}

func (s *Service) correctionPath(id, source string) string {
	return s.correctionFilePath(id, source, "json")
}

func (s *Service) correctionFilePath(id, source, ext string) string {
	if s == nil || s.dir == "" || id == "" || source == "" || ext == "" {
		return ""
	}
	return filepath.Join(s.dir, "correct", id, source+"."+ext)
}

// HasCorrection reports whether id already has a measured or manual correction
// for source, without reading or holding any media source.
func (s *Service) HasCorrection(id, source string) bool {
	if s == nil {
		return false
	}
	id = strings.TrimPrefix(id, URLPrefix)
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return false
	}
	if man, ok := s.loadCorrection(id, manualSource); ok && man.Applied {
		return true
	}
	src := SourceKey(source)
	if src == "" {
		return false
	}
	c, ok := s.loadCorrection(id, src)
	return ok && (c.Measured || c.Applied)
}

func (s *Service) loadApplied(id, source string) (correction, string, bool) {
	if c, ok := s.loadCorrection(id, manualSource); ok && c.Applied {
		return c, manualSource, true
	}
	source = SourceKey(source)
	if source != "" && source != manualSource {
		if c, ok := s.loadCorrection(id, source); ok && c.Applied {
			return c, source, true
		}
		return correction{}, "", false
	}
	if c, ok := s.loadCorrection(id, defaultSource); ok && c.Applied {
		return c, defaultSource, true
	}
	return correction{}, "", false
}

func (s *Service) loadCorrection(id, source string) (correction, bool) {
	source = SourceKey(source)
	p := s.correctionPath(id, source)
	if p == "" {
		return correction{}, false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return correction{}, false
	}
	var c correction
	if json.Unmarshal(raw, &c) != nil {
		return correction{}, false
	}
	return c, true
}

func (s *Service) storeCorrection(id, source string, c correction) {
	source = SourceKey(source)
	p := s.correctionPath(id, source)
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = writeFileAtomic(p, raw)
	srtPath := s.correctionFilePath(id, source, "srt")
	vttPath := s.correctionFilePath(id, source, "vtt")
	if c.Applied && c.Rate > 0 {
		if cues, err := s.cuesByCachedID(id); err == nil && len(cues) > 0 {
			shifted := applyCorrection(cues, c)
			_ = writeFileAtomic(srtPath, []byte(RenderSRT(shifted)))
			_ = writeFileAtomic(vttPath, []byte(RenderVTT(shifted)))
		}
	} else {
		if srtPath != "" {
			_ = os.Remove(srtPath)
		}
		if vttPath != "" {
			_ = os.Remove(vttPath)
		}
	}
}

func (s *Service) loadSidecarMeta(id string) sidecarMeta {
	if s == nil || s.dir == "" || id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return sidecarMeta{}
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil || len(raw) > 64<<10 {
		return sidecarMeta{}
	}
	var m sidecarMeta
	if json.Unmarshal(raw, &m) != nil {
		return sidecarMeta{}
	}
	return m
}

type flightGroup struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}

func (g *flightGroup) run(id string, fn func()) bool {
	g.mu.Lock()
	if g.m == nil {
		g.m = map[string]chan struct{}{}
	}
	if _, ok := g.m[id]; ok {
		g.mu.Unlock()
		return false
	}
	done := make(chan struct{})
	g.m[id] = done
	g.mu.Unlock()
	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.m, id)
			g.mu.Unlock()
			close(done)
		}()
		fn()
	}()
	return true
}

// StartCorrection measures the cached sidecar against the live session's
// source. It no-ops without a source handle (no prefetch). The first
// serve after a pick waits briefly via OpenFormatFrom so fast reference
// or heuristic fits are served immediately.
func (s *Service) StartCorrection(ctx context.Context, title *core.Title, id string, access ReleaseAccess) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		access.close()
		return
	}
	id = strings.TrimPrefix(id, URLPrefix)
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		access.close()
		return
	}
	src := sourceKey(access)
	if src == "" {
		access.close()
		return
	}
	if access.Reader == nil && access.URL == "" && access.Hold == nil && access.EmbeddedCues == nil && access.Info == nil && access.Release == "" {
		access.close()
		return
	}
	meta := s.loadSidecarMeta(id)
	if meta.Uploaded {
		access.close()
		return
	}
	if man, ok := s.loadCorrection(id, manualSource); ok && man.Applied {
		access.close()
		return
	}
	if c, ok := s.loadCorrection(id, src); ok {
		if c.Measured || c.Applied {
			access.close()
			return
		}
		if c.Err != "" && !s.now().UTC().After(c.RetryAfter) {
			access.close()
			return
		}
	}
	if !s.correctFlights.run(id+"\x00"+src, func() {
		if s.speechSem != nil {
			select {
			case s.speechSem <- struct{}{}:
				defer func() { <-s.speechSem }()
			case <-ctx.Done():
				access.close()
				return
			}
		}
		holdFailed := false
		if access.Hold != nil {
			if r, size, done, ok := access.Hold(); ok {
				access.Reader, access.Size, access.Done = r, size, done
			} else if access.Reader == nil {
				holdFailed = true
			}
		}
		defer access.close()
		ctx, cancel := context.WithTimeout(ctx, correctBackgroundBudget)
		defer cancel()
		s.measureAndStore(ctx, title, id, src, meta, access, holdFailed)
	}) {
		access.close()
	}
}

func (g *flightGroup) wait(id string, d time.Duration) bool {
	g.mu.Lock()
	ch, ok := g.m[id]
	g.mu.Unlock()
	if !ok {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}

func (g *flightGroup) waitPrefix(prefix string, d time.Duration) bool {
	g.mu.Lock()
	var ch chan struct{}
	for k, c := range g.m {
		if strings.HasPrefix(k, prefix) {
			ch = c
			break
		}
	}
	g.mu.Unlock()
	if ch == nil {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}

func (s *Service) measureAndStore(ctx context.Context, title *core.Title, id, source string, meta sidecarMeta, access ReleaseAccess, holdFailed bool) {
	if access.Reader == nil && access.URL == "" && access.EmbeddedCues == nil && access.Info == nil && access.Release == "" {
		if holdFailed {
			prev, _ := s.loadCorrection(id, source)
			s.storeError(id, source, prev, "hold")
		}
		return
	}
	if man, ok := s.loadCorrection(id, manualSource); ok && man.Applied {
		return
	}
	prev, _ := s.loadCorrection(id, source)
	cues, err := s.cuesByCachedID(id)
	if err != nil || len(cues) < 3 {
		s.storeError(id, source, prev, "cues")
		return
	}
	fit, method, ok := s.measureFit(ctx, title, id, source, meta, cues, access)
	if !ok {
		if ctx.Err() != nil || holdFailed {
			s.storeError(id, source, prev, "measure")
			return
		}
		s.storeCorrection(id, source, correction{
			Rate: 1, Measured: true, Applied: false, Method: "none", At: s.now().UTC(),
		})
		return
	}
	c := correction{
		Offset: fit.Offset, Rate: fit.Rate, Confidence: fit.Confidence,
		Inliers: fit.Inliers, Measured: true, Method: method, At: s.now().UTC(),
	}
	if !fit.Identity() {
		if man, ok := s.loadCorrection(id, manualSource); ok && man.Applied {
			c.Applied = false
		} else {
			c.Applied = true
		}
	}
	s.storeCorrection(id, source, c)
	if source != defaultSource && source != manualSource {
		s.storeCorrection(id, defaultSource, c)
	}
	if c.Applied {
		s.log.Info("subtitle alignment applied",
			"id", id,
			"source", source,
			"method", method,
			"offset", c.Offset.Round(time.Millisecond),
			"offset_ms", c.Offset.Milliseconds(),
			"scale", math.Round(c.Rate*1e6)/1e6,
			"confidence", math.Round(c.Confidence*1e3)/1e3,
			"inliers", c.Inliers,
		)
	} else {
		s.log.Debug("subtitle alignment in sync",
			"id", id,
			"source", source,
			"method", method,
			"offset", c.Offset.Round(time.Millisecond),
			"scale", math.Round(c.Rate*1e6)/1e6,
		)
	}
}

func (s *Service) storeError(id, source string, prev correction, reason string) {
	n := prev.Attempts + 1
	wait := correctRetryBase * time.Duration(1<<min(n-1, 5))
	if wait > correctRetryMax {
		wait = correctRetryMax
	}
	s.storeCorrection(id, source, correction{
		Err: reason, Attempts: n, RetryAfter: s.now().UTC().Add(wait), At: s.now().UTC(),
	})
}

func (s *Service) cuesByCachedID(id string) ([]Cue, error) {
	id = strings.TrimPrefix(id, URLPrefix)
	if s.dir == "" || id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return nil, core.ErrNotFound
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, id+".srt"))
	if err != nil {
		return nil, err
	}
	return ParseCues(string(raw))
}

func (s *Service) SetFFmpeg(path string) {
	if s != nil {
		s.ffmpeg = path
	}
}

func (s *Service) measureFit(ctx context.Context, title *core.Title, id, source string, meta sidecarMeta, cues []Cue, access ReleaseAccess) (TimingFit, string, bool) {
	if access.EmbeddedCues != nil {
		if fit, ok := bestReferenceFit(cues, access.EmbeddedCues()); ok {
			return fit, "embedded_cues", true
		}
	}
	if access.Reader != nil {
		tracks, err := readEmbeddedTextCueTracks(ctx, limitReaderAt(access.Reader, sourceReadBudget), access.Size, access.Size <= sourceReadBudget)
		if err == nil && len(tracks) > 0 {
			if fit, ok := bestReferenceFit(cues, tracks); ok {
				return fit, "embedded_cues", true
			}
		}
	}
	if ctx.Err() != nil {
		return TimingFit{}, "", false
	}
	if refs := s.knownReferenceCues(title, id, source, access); len(refs) > 0 {
		if fit, ok := bestReferenceFit(cues, refs); ok {
			return fit, "reference_subtitle", true
		}
	}
	if ctx.Err() != nil {
		return TimingFit{}, "", false
	}
	if isCheapSpeechSource(access) {
		if fit, ok := s.measureSpeechFit(ctx, cues, access); ok {
			return fit, "speech", true
		}
	}
	if ctx.Err() != nil {
		return TimingFit{}, "", false
	}
	if fit, ok := FitFrameRateHeuristic(meta, cues, access); ok {
		return fit, "fps_heuristic", true
	}
	return TimingFit{}, "", false
}

func bestReferenceFit(cues []Cue, refs [][]Cue) (TimingFit, bool) {
	var best TimingFit
	found := false
	for _, ref := range refs {
		if len(ref) < 3 {
			continue
		}
		fit, ok := FitTiming(cues, ref)
		if !ok {
			continue
		}
		if !found || betterTimingFit(fit, best) {
			best, found = fit, true
		}
	}
	return best, found
}

// knownReferenceCues gathers reference subtitle timelines already cached on disk
// for this title (uploads, already-measured sidecars for the same source, or
// release-matched sidecars). It performs zero network calls and scales only
// with the title's own selections.
func (s *Service) knownReferenceCues(title *core.Title, selfID, source string, access ReleaseAccess) [][]Cue {
	if s == nil || s.dir == "" {
		return nil
	}
	var key core.RatingKey
	if title != nil && title.Key != 0 {
		key = title.Key
	}
	if key == 0 {
		return nil
	}
	all := s.titleSelections(key)
	if len(all) == 0 {
		return nil
	}
	relSig := releaseSig(access.Release)
	seen := map[string]bool{selfID: true}
	var out [][]Cue
	add := func(id string, shift *correction, checkDur bool) {
		if seen[id] || len(out) >= 3 {
			return
		}
		cues, err := s.cuesByCachedID(id)
		if err != nil || len(cues) < 3 {
			return
		}
		if shift != nil {
			cues = applyCorrection(cues, *shift)
		}
		if checkDur && !durationFit(cues, access.Duration) {
			return
		}
		seen[id] = true
		out = append(out, cues)
	}
	for _, e := range all {
		if e.sig == uploadSig && !e.sel.Forced {
			add(e.sel.ID, nil, true)
		}
	}
	if source != "" {
		for _, e := range all {
			if seen[e.sel.ID] || e.sel.Forced {
				continue
			}
			if c, ok := s.loadCorrection(e.sel.ID, source); ok && c.Measured && (c.Confidence >= timingMinConfidence || c.Method == "embedded_cues") {
				add(e.sel.ID, &c, false)
			}
		}
	}
	if relSig != "any" {
		for _, e := range all {
			if seen[e.sel.ID] || e.sel.Forced {
				continue
			}
			if releaseSig(e.sel.Release) == relSig {
				add(e.sel.ID, nil, true)
			}
		}
	}
	return out
}

func isCheapSpeechSource(access ReleaseAccess) bool {
	if access.CheapSpeech {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(access.Source), "local:") {
		return true
	}
	if localInputPath(access) != "" {
		return true
	}
	return false
}

func localInputPath(access ReleaseAccess) string {
	if u := strings.TrimSpace(access.URL); u != "" {
		if !strings.Contains(u, "://") {
			return u
		}
		if parsed, err := url.Parse(u); err == nil && parsed.Scheme == "file" && parsed.Path != "" {
			return parsed.Path
		}
	}
	if r := unwrapReaderAt(access.Reader); r != nil {
		if lf, ok := r.(core.LocalFileSource); ok {
			if f := lf.LocalFile(); f != nil && f.Name() != "" {
				return f.Name()
			}
		}
		if f, ok := r.(*os.File); ok && f != nil && f.Name() != "" {
			return f.Name()
		}
	}
	return ""
}

func unwrapReaderAt(r io.ReaderAt) io.ReaderAt {
	for {
		lr, ok := r.(*limitedReaderAt)
		if !ok || lr == nil {
			return r
		}
		r = lr.r
	}
}

func (s *Service) measureSpeechFit(ctx context.Context, cues []Cue, access ReleaseAccess) (TimingFit, bool) {
	ff := access.FFmpeg
	if ff == "" && s != nil {
		ff = s.ffmpeg
	}
	if ff == "" {
		return TimingFit{}, false
	}
	var windows []SpeechWindow
	var err error
	if path := localInputPath(access); path != "" {
		windows, err = DecodeAudioWindows(ctx, ff, path, access.Duration, 4, 12*time.Second)
	} else if access.URL != "" {
		windows, err = DecodeAudioWindows(ctx, ff, access.URL, access.Duration, 4, 12*time.Second)
	} else if access.Reader != nil {
		windows, err = DecodeAudioWindowsReader(ctx, ff, access.Reader, access.Size, access.Duration, 4, 12*time.Second)
	}
	if err != nil || len(windows) == 0 {
		return TimingFit{}, false
	}
	return FitSpeechEnergy(cues, windows)
}

type limitedReaderAt struct {
	r      io.ReaderAt
	remain *int64
}

func limitReaderAt(r io.ReaderAt, budget int64) io.ReaderAt {
	if r == nil || budget <= 0 {
		return r
	}
	n := budget
	return &limitedReaderAt{r: r, remain: &n}
}

func (l *limitedReaderAt) clip(p []byte) []byte {
	if l == nil || l.remain == nil {
		return nil
	}
	left := *l.remain
	if left <= 0 {
		return nil
	}
	if int64(len(p)) > left {
		return p[:left]
	}
	return p
}

func (l *limitedReaderAt) debit(n int) {
	if n > 0 && l != nil && l.remain != nil {
		*l.remain -= int64(n)
	}
}

func (l *limitedReaderAt) ReadAt(p []byte, off int64) (int, error) {
	p = l.clip(p)
	if p == nil {
		return 0, io.EOF
	}
	if l.r == nil {
		return 0, io.EOF
	}
	n, err := l.r.ReadAt(p, off)
	l.debit(n)
	return n, err
}

func (l *limitedReaderAt) ReadAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	p = l.clip(p)
	if p == nil {
		return 0, io.EOF
	}
	var n int
	var err error
	switch r := l.r.(type) {
	case core.ContextReaderAt:
		n, err = r.ReadAtContext(ctx, p, off)
	default:
		if l.r == nil {
			return 0, io.EOF
		}
		n, err = l.r.ReadAt(p, off)
	}
	l.debit(n)
	return n, err
}

func (l *limitedReaderAt) ReadMetadataAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	p = l.clip(p)
	if p == nil {
		return 0, io.EOF
	}
	var n int
	var err error
	switch r := l.r.(type) {
	case core.MetadataReaderAt:
		n, err = r.ReadMetadataAtContext(ctx, p, off)
	case core.ContextReaderAt:
		n, err = r.ReadAtContext(ctx, p, off)
	default:
		if l.r == nil {
			return 0, io.EOF
		}
		n, err = l.r.ReadAt(p, off)
	}
	l.debit(n)
	return n, err
}

func applyCorrection(cues []Cue, c correction) []Cue {
	if !c.Applied || c.Rate == 0 {
		return cues
	}
	out, err := Shift(cues, c.Offset, c.Rate)
	if err != nil || len(out) == 0 {
		return cues
	}
	return out
}
