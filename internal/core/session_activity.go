package core

import "time"

// Shared session liveness thresholds across playback, broker, live TV,
// Activity/Overview, restart/update gating, upload budget, and stream limits.
const (
	// PausedSessionRetention bounds a silent pause without treating it as an
	// abandoned stream. Explicit stop still closes immediately.
	PausedSessionRetention = 30 * time.Minute

	// AbandonedSessionTimeout is how long a session that never received a
	// playback position and never served media bytes is kept before expiring.
	AbandonedSessionTimeout = 2 * time.Minute

	// StartupGraceWindow is how long a freshly prepared/opened session holds a
	// stream-limit reservation while the client connects and starts fetching.
	StartupGraceWindow = 12 * time.Second

	// PlayingEvidenceWindowDirect is how recently a direct-play / range session
	// must have served media bytes or received a playing progress/timeline
	// report to count as playing when no range read is currently in flight.
	PlayingEvidenceWindowDirect = 45 * time.Second

	// PlayingEvidenceWindowHLS is how recently an HLS/DASH/transcode session
	// must have served a segment or received a playing progress/timeline report
	// to count as playing. HLS clients fetch segments in bursts between buffer
	// refills.
	PlayingEvidenceWindowHLS = 60 * time.Second

	// PlayingEvidenceWindowLive is how recently a Live TV viewer must have
	// consumed live media bytes/segments to count as playing.
	PlayingEvidenceWindowLive = 30 * time.Second
)

// SessionActivity is an optional output-session extension. Only authenticated,
// ownership-checked player timelines may renew it; playlist polls may not.
type SessionActivity interface{ PlaybackState(state string) }

// MediaAccessTracker is an optional output-session extension reporting when
// client-facing media bytes (segments, init segments, or HTTP stream bytes —
// never session creation, playlists, or paused timelines) were last served.
type MediaAccessTracker interface{ LastMediaAccess() time.Time }

// SessionLivenessState classifies a playback session under the shared liveness rule.
type SessionLivenessState string

const (
	// SessionLivenessPlaying means the session has recent playing evidence:
	// an in-flight media read, or media bytes served or a playing
	// progress/timeline report within the transport's playing window.
	SessionLivenessPlaying SessionLivenessState = "playing"

	// SessionLivenessPaused means the client explicitly reported "paused" and
	// is within PausedSessionRetention. Shown as Paused in Activity/Overview;
	// NOT counted as an active stream for restart gating, upload budget, or
	// stream limits.
	SessionLivenessPaused SessionLivenessState = "paused"

	// SessionLivenessStarting means the session was just opened/prepared within
	// StartupGraceWindow and has not yet served media bytes or reported a
	// playback position. Holds a brief stream-limit slot during client startup;
	// hidden from Activity/Overview and excluded from restart/upload-budget.
	SessionLivenessStarting SessionLivenessState = "starting"

	// SessionLivenessIdle means the session has no recent playing evidence and
	// was not explicitly paused. Hidden from Activity/Overview and excluded
	// from restart gating, upload budget, and stream limits.
	SessionLivenessIdle SessionLivenessState = "idle"

	// SessionLivenessStopped means the session was closed, explicitly stopped,
	// or returned to prepared-only after stop.
	SessionLivenessStopped SessionLivenessState = "stopped"
)

// SessionLiveness is the evaluated liveness classification at a point in time.
type SessionLiveness struct {
	State         SessionLivenessState
	Expired       bool
	PlayingWindow time.Duration
}

// Active reports whether the session counts as an active stream for the
// restart/update gate and upload budget.
func (l SessionLiveness) Active() bool {
	return !l.Expired && l.State == SessionLivenessPlaying
}

// CountsForLimit reports whether the session occupies a stream-limit slot.
// Playing streams and freshly opened sessions within StartupGraceWindow count;
// paused, idle, abandoned, and stopped sessions do not.
func (l SessionLiveness) CountsForLimit() bool {
	return !l.Expired && (l.State == SessionLivenessPlaying || l.State == SessionLivenessStarting)
}

// Visible reports whether the session should appear in Console Overview and
// Activity. Playing sessions appear as playing; explicitly paused sessions
// appear as Paused. Starting/abandoned, idle/stale, and stopped sessions are hidden.
func (l SessionLiveness) Visible() bool {
	return !l.Expired && (l.State == SessionLivenessPlaying || l.State == SessionLivenessPaused)
}

// SessionLivenessInput holds the session timestamps and counters needed to
// evaluate the shared liveness rule deterministically at a given clock instant.
type SessionLivenessInput struct {
	Closed           bool
	PreparedOnly     bool
	StoppedBySurface bool
	BackgroundHeld   bool
	StopAt           time.Time
	StartedAt        time.Time
	OpenedAt         time.Time

	IsLive bool
	IsHLS  bool

	ViewerState    string
	PausedAt       time.Time
	LastProgressAt time.Time
	HasPosition    bool

	ActiveReads int32
	MediaBytes  int64
	LastMediaAt time.Time
	LastActive  time.Time

	PlayingWindow  time.Duration
	StartupGrace   time.Duration
	IdleTimeout    time.Duration
	AbandonedAfter time.Duration
	PauseRetention time.Duration
}

// EvaluateSessionLiveness evaluates the single shared liveness rule for all
// protocols (Plex, Jellyfin/Emby, Vela, Silo, Stremio, Xtream, Live TV).
func EvaluateSessionLiveness(in SessionLivenessInput, now time.Time) SessionLiveness {
	playingWindow := in.PlayingWindow
	if playingWindow <= 0 {
		switch {
		case in.IsLive:
			playingWindow = PlayingEvidenceWindowLive
		case in.IsHLS:
			playingWindow = PlayingEvidenceWindowHLS
		default:
			playingWindow = PlayingEvidenceWindowDirect
		}
	}
	startupGrace := in.StartupGrace
	if startupGrace <= 0 {
		startupGrace = StartupGraceWindow
	}
	abandonedAfter := in.AbandonedAfter
	if abandonedAfter <= 0 {
		abandonedAfter = AbandonedSessionTimeout
	}
	idleTimeout := in.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = AbandonedSessionTimeout
	}
	if idleTimeout < abandonedAfter {
		abandonedAfter = idleTimeout
	}
	pauseRetention := in.PauseRetention
	if pauseRetention <= 0 {
		pauseRetention = PausedSessionRetention
	}
	if idleTimeout > pauseRetention {
		pauseRetention = idleTimeout
	}

	if in.Closed {
		return SessionLiveness{State: SessionLivenessStopped, Expired: true, PlayingWindow: playingWindow}
	}
	if !in.StopAt.IsZero() {
		return SessionLiveness{
			State:         SessionLivenessStopped,
			Expired:       !now.Before(in.StopAt),
			PlayingWindow: playingWindow,
		}
	}
	if in.ViewerState == "stopped" {
		return SessionLiveness{State: SessionLivenessStopped, PlayingWindow: playingWindow}
	}
	if in.StoppedBySurface {
		expired := false
		if !in.BackgroundHeld && in.ActiveReads <= 0 {
			ref := latestTime(in.LastActive, in.LastMediaAt, in.LastProgressAt, in.StartedAt)
			expired = !ref.IsZero() && now.Sub(ref) > abandonedAfter
		}
		return SessionLiveness{State: SessionLivenessStopped, Expired: expired, PlayingWindow: playingWindow}
	}

	// Explicit client pause is retained up to pauseRetention and shown as
	// Paused, but never counts as an active stream for restart/budget/limits.
	if in.ViewerState == "paused" {
		ref := latestTime(in.PausedAt, in.LastProgressAt, in.StartedAt)
		expired := !in.BackgroundHeld && in.ActiveReads <= 0 && !ref.IsZero() && now.Sub(ref) > pauseRetention
		return SessionLiveness{
			State:         SessionLivenessPaused,
			Expired:       expired,
			PlayingWindow: playingWindow,
		}
	}

	// Prepared-only sources (detail plans before Open) stay hidden from
	// Activity/Overview and restart/budget, hold a brief startup limit slot,
	// and expire after abandonedAfter unless background-held.
	if in.PreparedOnly {
		ref := latestTime(in.LastMediaAt, in.LastProgressAt, in.OpenedAt, in.StartedAt)
		expired := !in.BackgroundHeld && in.ActiveReads <= 0 && !ref.IsZero() && now.Sub(ref) > abandonedAfter
		if !expired && !ref.IsZero() && now.Sub(ref) <= startupGrace {
			return SessionLiveness{State: SessionLivenessStarting, PlayingWindow: playingWindow}
		}
		return SessionLiveness{State: SessionLivenessIdle, Expired: expired, PlayingWindow: playingWindow}
	}

	hasMedia := in.ActiveReads > 0 || in.MediaBytes > 0 || !in.LastMediaAt.IsZero()
	hasProgress := in.HasPosition || !in.LastProgressAt.IsZero() || in.ViewerState == "playing"
	hasStarted := hasMedia || hasProgress

	// A long in-flight media read (e.g. direct-play range or live TS response)
	// is always live and never dropped.
	if in.ActiveReads > 0 {
		return SessionLiveness{State: SessionLivenessPlaying, PlayingWindow: playingWindow}
	}
	lastPlaying := in.LastMediaAt
	if in.LastProgressAt.After(lastPlaying) {
		lastPlaying = in.LastProgressAt
	}
	if !lastPlaying.IsZero() && now.Sub(lastPlaying) <= playingWindow {
		return SessionLiveness{State: SessionLivenessPlaying, PlayingWindow: playingWindow}
	}

	// Prepared/opened without ever serving media bytes or receiving a position:
	// never shown as playing, expires after abandonedAfter (~2m).
	if !hasStarted {
		ref := in.StartedAt
		expired := !in.BackgroundHeld && !ref.IsZero() && now.Sub(ref) > abandonedAfter
		if !expired {
			startRef := latestTime(in.OpenedAt, in.StartedAt)
			if !startRef.IsZero() && now.Sub(startRef) <= startupGrace {
				return SessionLiveness{State: SessionLivenessStarting, PlayingWindow: playingWindow}
			}
		}
		return SessionLiveness{State: SessionLivenessIdle, Expired: expired, PlayingWindow: playingWindow}
	}

	// Started previously, now silent past playingWindow without reporting paused:
	// no longer playing (hidden and excluded from active counts), expires after idleTimeout.
	ref := latestTime(lastPlaying, in.StartedAt)
	expired := !in.BackgroundHeld && !ref.IsZero() && now.Sub(ref) > idleTimeout
	return SessionLiveness{State: SessionLivenessIdle, Expired: expired, PlayingWindow: playingWindow}
}

func latestTime(ts ...time.Time) time.Time {
	var best time.Time
	for _, t := range ts {
		if t.After(best) {
			best = t
		}
	}
	return best
}

// PlaySessionLiveness returns the evaluated liveness state of a PlaySession
// snapshot. Real session managers populate Liveness explicitly; minimal test
// fixtures with an unset Liveness fall back to PreparedOnly/ViewerState.
func PlaySessionLiveness(ps *PlaySession) SessionLivenessState {
	if ps == nil {
		return SessionLivenessStopped
	}
	if ps.Liveness != "" {
		return ps.Liveness
	}
	if ps.PreparedOnly || ps.ViewerState == "stopped" {
		return SessionLivenessStopped
	}
	if ps.ViewerState == "paused" {
		return SessionLivenessPaused
	}
	return SessionLivenessPlaying
}

// SessionActiveStream reports whether ps is an actively playing stream for the
// restart/update gate and upload budget. Paused, idle, abandoned, prepared-only,
// and stopped sessions return false.
func SessionActiveStream(ps *PlaySession) bool {
	return PlaySessionLiveness(ps) == SessionLivenessPlaying
}

// SessionVisible reports whether ps should be shown in Console Overview and
// Activity (playing or explicitly paused).
func SessionVisible(ps *PlaySession) bool {
	st := PlaySessionLiveness(ps)
	return st == SessionLivenessPlaying || st == SessionLivenessPaused
}

// SessionCountsForLimit reports whether ps counts toward stream limits.
// Playing streams and freshly starting sessions count; paused, idle, abandoned,
// and stopped sessions do not.
func SessionCountsForLimit(ps *PlaySession) bool {
	st := PlaySessionLiveness(ps)
	return st == SessionLivenessPlaying || st == SessionLivenessStarting
}

// SessionPaused reports whether ps is explicitly paused.
func SessionPaused(ps *PlaySession) bool {
	return PlaySessionLiveness(ps) == SessionLivenessPaused
}
