package app

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// enrichPlaybackEventContext fills app-level playback context fields using
// lock-free / in-memory lookups so emitting never blocks the play path.
func (a *App) enrichPlaybackEventContext(ps *core.PlaySession, title *core.Title, base events.PlaybackSessionContext) events.PlaybackSessionContext {
	if a == nil {
		return base
	}
	if base.LibraryName == "" {
		switch base.MediaType {
		case "movie":
			base.LibraryName = "Movies"
			if base.LibraryID == "" {
				base.LibraryID = "movies"
			}
		case "episode":
			base.LibraryName = "TV Shows"
			if base.LibraryID == "" {
				base.LibraryID = "shows"
			}
		case "live":
			base.LibraryName = "Live TV"
			if base.LibraryID == "" {
				base.LibraryID = "livetv"
			}
		}
	}
	if ps != nil && ps.ID != "" {
		if tc := a.activeTranscoder(); tc != nil {
			if hs, ok := tc.Session(ps.ID); ok && hs != nil {
				if ts, ok := hs.(*transcode.Session); ok {
					if ts.HWAccelerated() {
						base.Stream.HWEncode = true
						base.Stream.HWDecode = true
					}
					if st := ts.EncoderStatus(); st != nil {
						if st.Speed > 0 {
							base.Stream.TranscodeSpeed = st.Speed
						}
						if strings.EqualFold(st.Phase, "throttled") {
							base.Stream.Throttled = true
						}
					}
				}
			}
		}
	}
	return base
}

// cachedPlaybackMarkers reads only locally cached marker spans (no remote I/O)
// for playback.marker.entered / playback.marker.exited transitions.
func (a *App) cachedPlaybackMarkers(key core.RatingKey, info core.MediaInfo) []playback.MarkerSpan {
	if a == nil || a.markers == nil || key == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	list, err := a.markers.CachedMarkers(ctx, key, info)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make([]playback.MarkerSpan, 0, len(list))
	for _, mk := range list {
		if mk.Type == "" || mk.EndMS <= mk.StartMS {
			continue
		}
		out = append(out, playback.MarkerSpan{
			Kind:    strings.ToLower(mk.Type),
			StartMS: mk.StartMS,
			EndMS:   mk.EndMS,
		})
	}
	return out
}

// initMediaEventBridges connects watch-state completion notifications to the
// domain event bus so playback.session.completed fires with full context.
func (a *App) initMediaEventBridges() {
	if a == nil || a.state == nil || a.eventBus == nil {
		return
	}
	unsub := a.state.Subscribe(func(ev state.Event) {
		if ev.Kind != state.EventCompleted || ev.Imported {
			return
		}
		a.emitPlaybackCompleted(ev)
	})
	a.closers = append(a.closers, unsub)
}

func (a *App) emitPlaybackCompleted(ev state.Event) {
	if a == nil || a.eventBus == nil {
		return
	}
	var ps *core.PlaySession
	var all []*core.PlaySession
	if a.playback != nil {
		all = a.playback.Sessions()
		if ev.Origin.SessionID != "" {
			if found, ok := a.playback.Session(ev.Origin.SessionID); ok {
				ps = found
			}
		}
		if ps == nil && ev.Origin.ClientID != "" {
			if found, ok := a.playback.SessionFor(ev.UserID, ev.Origin.ClientID, ev.Key); ok {
				ps = found
			}
		}
	}
	var title *core.Title
	if a.catalog != nil && ev.Key != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		if t, err := a.catalog.CachedTitle(ctx, ev.Key); err == nil {
			title = t
		}
		cancel()
	}
	var user *core.User
	if a.users != nil && ev.UserID > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		if u, err := a.users.ActiveUser(ctx, ev.UserID); err == nil && u != nil {
			user = u
		}
		cancel()
	}
	if ps == nil {
		ps = &core.PlaySession{
			ID:       ev.Origin.SessionID,
			UserID:   ev.UserID,
			ClientID: ev.Origin.ClientID,
			Key:      ev.Key,
			Client: core.ClientCaps{
				ClientID: ev.Origin.ClientID,
				Protocol: core.Protocol(ev.Origin.Surface),
			},
			Info: core.MediaInfo{Duration: ev.Duration},
		}
		if user != nil {
			ps.Username = user.Username
		}
	}
	sessCtx := playback.BaseSessionContext(ps, title, user, all)
	sessCtx = a.enrichPlaybackEventContext(ps, title, sessCtx)

	sessionID := ps.ID
	if sessionID == "" {
		sessionID = ev.Origin.SessionID
	}
	ratio := ev.Fraction
	if ratio <= 0 && ev.Duration > 0 && ev.Offset > 0 {
		ratio = float64(ev.Offset) / float64(ev.Duration)
	}
	if ratio > 1 {
		ratio = 1
	}
	a.publishEvent(
		events.EventPlaybackSessionCompleted,
		events.PlaybackSessionCompletedPayload{
			SessionID:              sessionID,
			PlayID:                 sessionID,
			WatchedMS:              ev.Offset.Milliseconds(),
			WatchedRatio:           ratio,
			ThresholdPercent:       int(math.Round(state.WatchedThreshold * 100)),
			PlaybackSessionContext: sessCtx,
		},
		events.WithCorrelation(events.Correlation{
			SessionID: sessionID,
			PlayID:    sessionID,
			ViewerID:  ev.UserID,
			TitleID:   ev.Key.String(),
			DeviceID:  ev.Origin.ClientID,
		}),
	)
}
