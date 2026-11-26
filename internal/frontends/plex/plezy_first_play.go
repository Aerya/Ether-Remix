package plex

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// Plezy (a Flutter/mpv Plex client) builds its track selection from the
// /library/metadata row it already holds: getVideoPlaybackData reuses a
// fresh (5 minute) cached row whenever every Part carries Stream, exists
// and accessible, and its subtitle selection trusts only the server's
// selected stream. A cold episode's detail row is estimated — invented
// audio, no subtitles — so first play started with subtitles off, and the
// real tracks the playback probe found seconds later were never re-read
// (#618). Two changes, both scoped to Plezy:
//
//   - A pre-probe (estimated) Part does not claim completeness: file
//     availability leaves exists/accessible unset for it, exactly like
//     PMS without a file check, so Plezy rejects its cached row at Play
//     and re-fetches. Missing flags still read as playable in Plezy
//     (isPlayable is accessible != false && exists != false).
//   - That re-fetch is play intent. The detail-screen fetch and the
//     play-time fetch are identical on the wire (same endpoint, same
//     includeChapters/includeMarkers/checkFiles/includeStreams), so the
//     only reliable signal is state: this client was already served an
//     incomplete row for this title and is asking again. Then the
//     request waits, bounded, on the shared resolve+probe flight — the
//     same warm entry the following file request opens — and answers
//     with the real streams and the viewer's rule-selected subtitle.
//
// The first (browsing) fetch never waits and starts nothing beyond the
// detail warm-up scheduleDetailWarm already runs for every client.

const (
	// plezyPlayProbeWait bounds the play-time re-fetch's wait for the
	// shared resolve+probe flight. The playback manager stops waiting
	// early while the source is still being searched; the following
	// file.mkv joins the same flight.
	plezyPlayProbeWait = playIntentProbeWait
	// plezyVersionProbeWait bounds parallel header probes on free-source
	// non-Auto versions during a Plezy play-time metadata read (#678).
	plezyVersionProbeWait = 4 * time.Second
	// plezyReaskMinAge separates Plezy's detail-open burst (three
	// /library/metadata/<id> fetches within ~1 s while browsing) from
	// the later play-time re-ask. Burst fetches inside this window are
	// browsing and never wait, and the arm survives the burst.
	plezyReaskMinAge = 2500 * time.Millisecond
	// plezyReaskTTL is how long a served incomplete row arms the
	// play-intent re-fetch. It comfortably covers Plezy's 5-minute
	// fresh-cache window.
	plezyReaskTTL     = 15 * time.Minute
	plezyReaskMaxRows = 512
)

// plezyClient identifies Plezy by its X-Plex-Product. The change is
// scoped to it: other clients re-read metadata around playback, or are
// already served a probed detail page (Bedrock), and keep today's
// wire shape untouched.
func plezyClient(pr *Request) bool {
	return pr != nil && strings.EqualFold(strings.TrimSpace(pr.Product), "Plezy")
}

type plezyReaskKey struct {
	userID   int64
	clientID string
	key      frontend.RatingKey
}

// plezyReaskStore remembers which Plezy clients were served an
// incomplete (pre-probe) detail row, so their later identical fetch at
// Play can be recognised as the play-time re-ask without catching the
// detail screen's initial burst.
type plezyReaskStore struct {
	mu   sync.Mutex
	rows map[plezyReaskKey]time.Time
}

// checkOrArm records an arming fetch for k when none is active, or
// classifies a follow-up fetch against the existing arm. It returns
// true only when k was armed at least plezyReaskMinAge ago (and within
// plezyReaskTTL): that follow-up is the play-time re-ask. Burst fetches
// inside plezyReaskMinAge return false without consuming or resetting
// the arm, so the arm survives the detail-open burst. An armed entry
// stays armed until it expires or forget is called, so a re-ask whose
// probe misses the wait budget still arms the next retry.
func (st *plezyReaskStore) checkOrArm(k plezyReaskKey, now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.rows == nil {
		st.rows = map[plezyReaskKey]time.Time{}
	}
	if at, ok := st.rows[k]; ok {
		age := now.Sub(at)
		if age >= 0 && age <= plezyReaskTTL {
			return age >= plezyReaskMinAge
		}
		delete(st.rows, k)
	}
	if len(st.rows) >= plezyReaskMaxRows {
		for row, at := range st.rows {
			if age := now.Sub(at); age < 0 || age > plezyReaskTTL {
				delete(st.rows, row)
			}
		}
		if len(st.rows) >= plezyReaskMaxRows {
			return false // full of fresh rows: skip arming rather than grow
		}
	}
	st.rows[k] = now
	return false
}

func (st *plezyReaskStore) isArmedReady(k plezyReaskKey, now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	at, ok := st.rows[k]
	if !ok {
		return false
	}
	age := now.Sub(at)
	if age < 0 || age > plezyReaskTTL {
		delete(st.rows, k)
		return false
	}
	return age >= plezyReaskMinAge
}

func (st *plezyReaskStore) armIfIdle(k plezyReaskKey, now time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.rows == nil {
		st.rows = map[plezyReaskKey]time.Time{}
	}
	if at, ok := st.rows[k]; ok {
		age := now.Sub(at)
		if age >= 0 && age <= plezyReaskTTL {
			return
		}
		delete(st.rows, k)
	}
	if len(st.rows) >= plezyReaskMaxRows {
		for row, at := range st.rows {
			if age := now.Sub(at); age < 0 || age > plezyReaskTTL {
				delete(st.rows, row)
			}
		}
		if len(st.rows) >= plezyReaskMaxRows {
			return
		}
	}
	st.rows[k] = now
}

func (st *plezyReaskStore) forget(k plezyReaskKey) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.rows, k)
}

// plezyFirstPlay runs on a single-item metadata read for a playable
// title. Nothing happens unless the client is Plezy asking with file
// checks (its detail and playback fetches both do). On browsing reads it
// never waits; on a play-intent fetch (Plezy's getVideoPlaybackData query
// shape or an armed re-ask after plezyReaskMinAge) it waits, bounded, for
// Auto's resolve+probe flight when needed and for parallel header probes
// on unprobed free-source non-Auto versions (#618, #678).
func (s *Server) plezyFirstPlay(ctx, reqCtx context.Context, pr *Request, t *frontend.Title, q url.Values, rawQuery string) {
	if pr == nil || pr.User == nil || t == nil || !plezyClient(pr) || libraryToolClient(pr) {
		return
	}
	if !metadataOptionOn(q.Get("checkFiles")) && !metadataOptionOn(q.Get("asyncCheckFiles")) && !metadataOptionOn(q.Get("checkFileAvailability")) {
		return
	}
	k := plezyReaskKey{userID: pr.User.ID, clientID: pr.ClientID, key: t.Key}
	_, _, exact := s.mediaInfoWithSource(ctx, pr, t)
	// Plezy's playback fetch (getVideoPlaybackData) is the only Plezy read
	// that puts includeMarkers before includeChapters; every browsing read
	// (detail screen, season-row focus header, shared row, extras) puts
	// includeChapters first. Plezy on Android and Android TV has no episode
	// detail page: a season row plays its episode directly, and its single
	// play-time fetch is that title's first. That shape is play intent on
	// its own, with no arming fetch before it.
	play := plezyPlaybackFetch(rawQuery)
	if exact && !play {
		if !s.plezyReasks.isArmedReady(k, s.now()) {
			return
		}
		play = true
	}
	if !play && !s.plezyReasks.checkOrArm(k, s.now()) {
		// Initial detail open or follow-up burst fetch inside
		// plezyReaskMinAge: never wait; this response goes out
		// incomplete (see projectFileAvailability) while keeping
		// the arm intact for the play-time re-ask.
		return
	}
	if reqCtx == nil {
		reqCtx = ctx
	}
	var autoProbe func(context.Context) error
	if !exact {
		autoProbe = func(c context.Context) error { return s.probePlezyPlay(c, pr, t) }
	}
	s.runPlayIntentProbes(reqCtx, pr, t, exact, plezyPlayProbeWait, plezyVersionProbeWait, autoProbe)
	if _, _, exactNow := s.mediaInfoWithSource(ctx, pr, t); exactNow {
		s.plezyReasks.forget(k)
	}
}

// runPlayIntentProbes joins (or starts) the foreground Auto resolve+probe
// flight (when !exact) and runs bounded header probes on free-source non-Auto
// offered versions CONCURRENTLY under the single wait budget (#618, #670,
// #678, #715). Once Auto finishes, any remaining wait for non-Auto version
// probes is capped at versionTail (still bounded by wait), so first-tap
// latency never exceeds the serial Auto + versionTail budget while giving
// non-Auto version probes Auto's full runtime as head start.
func (s *Server) runPlayIntentProbes(ctx context.Context, pr *Request, t *frontend.Title, exact bool, wait, versionTail time.Duration, autoProbe func(context.Context) error) {
	if pr == nil || pr.User == nil || t == nil || s.playback() == nil {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	vhp, hasVersionProber := s.playback().(frontend.VersionHeaderProber)
	caps := s.clientCapsForKey(pr, t.Key)

	// playIntentVersionKey lets this title's version menu read beyond the
	// local caches. It is set only once the source is known, so rendering
	// never waits on a cold search either.
	if exact || autoProbe == nil {
		pr.playIntentVersionKey = t.Key
		if hasVersionProber {
			vCtx, vCancel := context.WithTimeout(probeCtx, versionTail)
			_ = vhp.ProbeVersionHeaders(vCtx, pr.User, t.Key, caps)
			vCancel()
		}
		return
	}
	if !hasVersionProber {
		if autoProbe(probeCtx) == nil {
			pr.playIntentVersionKey = t.Key
		}
		return
	}

	autoDone := make(chan struct{})
	versionsDone := make(chan struct{})
	vCtx, vCancel := context.WithCancel(frontend.WithVersionProbeSearchWait(probeCtx, autoDone))
	// Cancelling ends the version probes; the response never waits for
	// them to wind down.
	defer vCancel()

	go func() {
		defer close(versionsDone)
		_ = vhp.ProbeVersionHeaders(vCtx, pr.User, t.Key, caps)
	}()

	var autoErr error
	func() {
		defer close(autoDone)
		autoErr = autoProbe(probeCtx)
	}()
	if autoErr != nil {
		// Auto's source is still being searched (or failed): the version
		// probes would wait on the same search. Answer now; the playback
		// decision waits for the flight instead.
		return
	}
	pr.playIntentVersionKey = t.Key

	select {
	case <-versionsDone:
		return
	case <-probeCtx.Done():
		return
	default:
	}
	timer := time.NewTimer(versionTail)
	defer timer.Stop()
	select {
	case <-versionsDone:
	case <-probeCtx.Done():
	case <-timer.C:
	}
}

func (s *Server) probePlezyPlay(ctx context.Context, pr *Request, t *frontend.Title) error {
	if pr == nil || pr.User == nil || s.playback() == nil {
		return nil
	}
	caps := s.clientCapsForKey(pr, t.Key)
	if prober, ok := s.playback().(frontend.PlayMetadataProber); ok {
		return prober.ProbePlayMetadata(ctx, pr.User, t.Key, caps)
	}
	if prober, ok := s.playback().(frontend.MetadataProber); ok {
		return prober.ProbeMetadata(ctx, pr.User, t.Key, caps)
	}
	return nil
}

// plezyPartProbed reports whether a rendered Part's streams come from a
// real probe rather than the pre-probe estimate. Stream bindings carry
// the estimate flag of the info they were rendered from; when no
// binding resolves (a render without issued streams), the title's
// session/resumed/warm probe decides.
func (s *Server) plezyPartProbed(ctx context.Context, pr *Request, part *model.Part) bool {
	resolved := false
	for _, st := range part.Stream {
		if st.ID == 0 {
			continue
		}
		binding, err := s.resolveIssuedStream(ctx, pr.User, pr.ClientID, st.ID)
		if err != nil {
			continue
		}
		if binding.Estimated {
			return false
		}
		resolved = true
	}
	if resolved {
		return true
	}
	key, _, ok := s.partTitle(part.ID)
	if !ok || pr.User == nil || s.playback() == nil {
		return false
	}
	if ps, ok := s.playback().SessionFor(pr.User.ID, pr.ClientID, key); ok && ps != nil && !ps.Info.Estimated && len(ps.Info.Streams) > 0 {
		return true
	}
	caps := s.clientCapsForKey(pr, key)
	if src, ok := s.playback().(resumedTrackSource); ok {
		if info, releaseID, ok := src.ResumedTracks(ctx, pr.User, key, caps); ok && releaseID != "" && !info.Estimated && len(info.Streams) > 0 {
			return true
		}
	}
	if wp, ok := s.playback().(frontend.WarmProber); ok {
		if _, warm := wp.WarmProbeInfo(pr.User, caps, key); warm {
			return true
		}
	}
	return false
}

// plezyPlaybackFetch reports whether a /library/metadata/<id> query has the
// shape of Plezy's playback-metadata read (Plezy 2.24-2.26 getVideoPlaybackData:
// includeMarkers, includeChapters, checkFiles, includeStreams, in that order).
// Plezy serialises query parameters in insertion order, and no browsing read
// it makes lists includeMarkers first. Season-row focus fetches use the
// browsing shape, so focus never waits.
func plezyPlaybackFetch(rawQuery string) bool {
	markers := strings.Index(rawQuery, "includeMarkers=1")
	chapters := strings.Index(rawQuery, "includeChapters=1")
	return markers >= 0 && chapters > markers && strings.Contains(rawQuery, "checkFiles=1") && strings.Contains(rawQuery, "includeStreams=1")
}
