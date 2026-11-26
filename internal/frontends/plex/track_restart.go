package plex

import (
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// A Plex client changes the audio or subtitle track of a playing item by
// restarting it: PUT /library/parts/{part}?subtitleStreamID= (or
// audioStreamID=), stop the transcode, then a new decision or part read for
// the same item at the current offset. That restart must play the release the
// viewer was watching. The track ids it names belong to that release, and
// another release has different streams and timing.
//
// Without a pin the restart is an ordinary Auto (or Play Version) request.
// A burned-in subtitle turns direct play into a transcode carrying the
// client's quality bitrate, so source selection drops both the live session
// and the remembered release when either exceeds that bitrate, and the
// resolver picks another release. After a stop, an expired resolver answer
// can pick another release too.
//
// The track change pins the release. Every play of the same version on this
// viewer, client and title then opens that exact release while its session is
// live, or within trackRestartWindow of the change or of the last stop. A
// pinned release that can no longer open fails with the usual error instead
// of switching. Picking another version, or playing again after the window,
// ends the pin.
const trackRestartWindow = 2 * time.Minute

type trackRestart struct {
	// selection is the version the playback asked for ("" = Auto) and
	// release the release it plays.
	selection string
	release   string
	pinned    bool
	at        time.Time
}

type trackRestarts struct {
	mu sync.Mutex
	m  map[playIdentityKey]*trackRestart
}

func newTrackRestarts() *trackRestarts {
	return &trackRestarts{m: map[playIdentityKey]*trackRestart{}}
}

// entryLocked returns k's entry, creating it when there is room after
// dropping idle entries.
func (t *trackRestarts) entryLocked(k playIdentityKey, now time.Time) *trackRestart {
	if e := t.m[k]; e != nil {
		return e
	}
	if len(t.m) >= maxPlayIdentities {
		for key, e := range t.m {
			if now.Sub(e.at) > playIdentityTTL {
				delete(t.m, key)
			}
		}
		if len(t.m) >= maxPlayIdentities {
			return nil
		}
	}
	e := &trackRestart{}
	t.m[k] = e
	return e
}

// admitted records the release a play of selection opened on. Another
// version's release ends a pin.
func (t *trackRestarts) admitted(k playIdentityKey, selection, release string, now time.Time) {
	if t == nil || !k.valid() || release == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entryLocked(k, now)
	if e == nil {
		return
	}
	if e.pinned && release == e.release {
		e.at = now
		return
	}
	*e = trackRestart{selection: selection, release: release, at: now}
}

// pin is called on a track change. live is the release the session for k
// reads now ("" when it has already stopped). Without a live session, only a
// play stopped within trackRestartWindow is pinned: a track picked on a
// detail page long after playback ended leaves the next play free.
func (t *trackRestarts) pin(k playIdentityKey, live string, now time.Time) {
	if t == nil || !k.valid() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[k]
	if live == "" {
		if e == nil || e.release == "" || now.Sub(e.at) > trackRestartWindow {
			return
		}
	} else {
		// With no admission seen, a restart may ask for Auto or for this
		// release by name.
		if e = t.entryLocked(k, now); e == nil {
			return
		}
		e.release = live
	}
	e.pinned, e.at = true, now
}

// stopped records the release a stopping session read (after any failover)
// and restarts the window: a client may stop before it changes the track.
func (t *trackRestarts) stopped(k playIdentityKey, release string, now time.Time) {
	if t == nil || !k.valid() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[k]
	if e == nil && release != "" {
		e = t.entryLocked(k, now)
	}
	if e == nil {
		return
	}
	if release != "" && !e.pinned {
		e.release = release
	}
	e.at = now
}

// release is the pinned release for a play of selection, if any. live is
// the release the session for k reads now ("" when there is none).
func (t *trackRestarts) release(k playIdentityKey, selection, live string, now time.Time) (string, bool) {
	if t == nil || !k.valid() {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[k]
	if e == nil || !e.pinned {
		return "", false
	}
	if selection != e.selection && selection != e.release {
		e.pinned = false // the viewer picked another version
		return "", false
	}
	if live != e.release && now.Sub(e.at) > trackRestartWindow {
		e.pinned = false // the playback ended; a new play chooses again
		return "", false
	}
	e.at = now
	return e.release, true
}

// liveRelease is the release the playback session for this viewer, client
// and title reads now (after any failover), or "".
func (s *Server) liveRelease(userID int64, clientID string, key frontend.RatingKey) string {
	pb := s.playback()
	if pb == nil {
		return ""
	}
	ps, ok := pb.SessionFor(userID, clientID, key)
	if !ok || ps == nil || ps.PreparedOnly || ps.Resolution == nil {
		return ""
	}
	return ps.Resolution.Release.ID
}

// restartRelease is the release a play of selection must open: the pinned
// release (exact) after a track change, else selection itself.
func (s *Server) restartRelease(ik playIdentityKey, selection string) (string, bool) {
	if ik.key.IsPluginKey() {
		return selection, false
	}
	if rel, ok := s.trackRestarts.release(ik, selection, s.liveRelease(ik.userID, ik.clientID, ik.key), s.now()); ok {
		return rel, true
	}
	return selection, false
}

// expire drops entries idle for playIdentityTTL.
func (t *trackRestarts) expire(now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, e := range t.m {
		if now.Sub(e.at) > playIdentityTTL {
			delete(t.m, k)
		}
	}
}
