package actions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
)

const defaultStopSessionMessage = "The server owner has ended this stream."

// NewPlaybackStopSession constructs the built-in playback.stop_session action.
// It supports explicit session targeting as well as dynamic selectors
// (lowest_priority_transcode, oldest_paused, viewer_oldest) and routes
// termination through the same path Plex /status/sessions/terminate uses.
func NewPlaybackStopSession(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "playback.stop_session",
		Owner:       events.OwnerBuiltin,
		Group:       "playback",
		Label:       "Stop playback session",
		Description: "Terminate an active playback session with an owner-supplied message shown on the viewer's player, targeting an explicit session ID or a dynamic selector.",
		Icon:        "square-stop",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "sessionId", Type: events.FieldString, Label: "Session ID", Description: "Explicit active session ID to terminate (used when selector is explicit).", Template: true},
			{
				Key:         "selector",
				Type:        events.FieldEnum,
				Label:       "Target selector",
				Description: "How to choose the playback session to stop.",
				Default:     "explicit",
				Enum: []events.EnumOption{
					{Value: "explicit", Label: "Explicit session ID"},
					{Value: "lowest_priority_transcode", Label: "Lowest-priority transcode"},
					{Value: "oldest_paused", Label: "Oldest paused stream"},
					{Value: "viewer_oldest", Label: "Viewer's oldest stream"},
				},
			},
			{Key: "viewerId", Type: events.FieldString, Label: "Viewer", Description: "Viewer ID for viewer_oldest selector (defaults to trigger event's viewer).", Template: true, Picker: flows.PickerViewer},
			{Key: "message", Type: events.FieldString, Label: "Viewer message", Description: "Reason message displayed on the viewer's client.", Default: defaultStopSessionMessage, Template: true},
			{Key: "excludeSessionId", Type: events.FieldString, Label: "Exclude session ID", Description: "Never terminate this session ID when using a dynamic selector.", Template: true},
			{Key: "excludeViewerTags", Type: events.FieldStringList, Label: "Protected viewer tags", Description: "Skip sessions belonging to viewers carrying any of these tags (for example vip or owner).", Template: true},
			{Key: "minPausedMs", Type: events.FieldDurationMS, Label: "Minimum paused duration", Description: "Only consider streams paused for at least this many milliseconds (oldest_paused selector).", Unit: "ms"},
		},
		Outputs: []events.FieldSchema{
			{Name: "stopped", Type: events.FieldBool, Label: "Session stopped"},
			{Name: "sessionId", Type: events.FieldString, Label: "Stopped session ID"},
			{Name: "viewerId", Type: events.FieldString, Label: "Viewer ID"},
			{Name: "titleName", Type: events.FieldString, Label: "Title name"},
			{Name: "reason", Type: events.FieldString, Label: "Outcome or viewer message"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		selector := strings.TrimSpace(paramString(params, "selector", "explicit"))
		sessionID := strings.TrimSpace(paramString(params, "sessionId", ""))
		viewerID := strings.TrimSpace(paramString(params, "viewerId", correlationViewerID(actx)))
		message := strings.TrimSpace(paramString(params, "message", defaultStopSessionMessage))
		if len(message) > 500 {
			message = string([]rune(message)[:500])
		}
		excludeSessionID := strings.TrimSpace(paramString(params, "excludeSessionId", ""))
		excludeTags := paramStringList(params, "excludeViewerTags")
		minPausedMs, _, err := paramInt64(params, "minPausedMs", 0)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("playback.stop_session: %w", err)
		}

		if selector == "explicit" && sessionID == "" {
			if actx.Event.Correlation.SessionID != "" {
				sessionID = actx.Event.Correlation.SessionID
			} else {
				return flows.ActionResult{}, errors.New("playback.stop_session: sessionId is required when selector is \"explicit\"")
			}
		}

		if deps.Playback == nil {
			if actx.DryRun {
				return flows.ActionResult{
					Output: map[string]any{
						"dryRun":    true,
						"stopped":   false,
						"sessionId": sessionID,
						"selector":  selector,
						"reason":    message,
					},
				}, nil
			}
			return flows.ActionResult{}, errors.New("playback.stop_session: playback controller unavailable")
		}

		sessions, err := deps.Playback.ListSessions(ctx)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("playback.stop_session: list sessions: %w", err)
		}

		target, found := selectSessionToStop(sessions, sessionSelection{
			Selector:          selector,
			SessionID:         sessionID,
			ViewerID:          viewerID,
			ExcludeSessionID:  excludeSessionID,
			ExcludeViewerTags: excludeTags,
			MinPaused:         time.Duration(minPausedMs) * time.Millisecond,
			Now:               deps.now(),
		})
		if !found {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":    actx.DryRun,
					"stopped":   false,
					"sessionId": "",
					"selector":  selector,
					"reason":    "no_matching_session",
				},
			}, nil
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":    true,
					"stopped":   false,
					"sessionId": target.ID,
					"viewerId":  target.ViewerID,
					"titleName": target.TitleName,
					"selector":  selector,
					"reason":    message,
				},
			}, nil
		}

		if err := deps.Playback.StopSession(ctx, target.ID, message); err != nil {
			return flows.ActionResult{}, fmt.Errorf("playback.stop_session: %w", err)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":    false,
				"stopped":   true,
				"sessionId": target.ID,
				"viewerId":  target.ViewerID,
				"titleName": target.TitleName,
				"selector":  selector,
				"reason":    message,
			},
		}, nil
	}}
}

type sessionSelection struct {
	Selector          string
	SessionID         string
	ViewerID          string
	ExcludeSessionID  string
	ExcludeViewerTags []string
	MinPaused         time.Duration
	Now               time.Time
}

func selectSessionToStop(sessions []ActiveSession, sel sessionSelection) (ActiveSession, bool) {
	excludedTagSet := make(map[string]bool, len(sel.ExcludeViewerTags))
	for _, tag := range sel.ExcludeViewerTags {
		if t := strings.ToLower(strings.TrimSpace(tag)); t != "" {
			excludedTagSet[t] = true
		}
	}
	isExcluded := func(s ActiveSession) bool {
		if sel.ExcludeSessionID != "" && (s.ID == sel.ExcludeSessionID || s.ClientSession == sel.ExcludeSessionID) {
			return true
		}
		if len(excludedTagSet) > 0 {
			if s.ViewerRole != "" && excludedTagSet[strings.ToLower(s.ViewerRole)] {
				return true
			}
			for _, tag := range s.ViewerTags {
				if excludedTagSet[strings.ToLower(strings.TrimSpace(tag))] {
					return true
				}
			}
		}
		return false
	}

	switch sel.Selector {
	case "", "explicit":
		for _, s := range sessions {
			if s.ID == sel.SessionID || (s.ClientSession != "" && s.ClientSession == sel.SessionID) {
				if isExcluded(s) {
					return ActiveSession{}, false
				}
				return s, true
			}
		}
		return ActiveSession{}, false

	case "lowest_priority_transcode":
		var candidates []ActiveSession
		for _, s := range sessions {
			if isExcluded(s) {
				continue
			}
			if !s.IsTranscode && !strings.EqualFold(s.VideoDecision, "transcode") {
				continue
			}
			candidates = append(candidates, s)
		}
		if len(candidates) == 0 {
			return ActiveSession{}, false
		}
		// Sort so candidates[0] is the lowest-priority transcode to stop first:
		// 1. Non-owner before owner
		// 2. Remote (WAN) before local (LAN)
		// 3. Paused before playing
		// 4. Higher video height / bitrate first (frees the most transcode capacity)
		// 5. Most recently started first (least invested viewing progress)
		// 6. Lexicographical session ID tie-breaker
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i], candidates[j]
			aOwner := strings.EqualFold(a.ViewerRole, "owner")
			bOwner := strings.EqualFold(b.ViewerRole, "owner")
			if aOwner != bOwner {
				return !aOwner
			}
			if a.IsRemote != b.IsRemote {
				return a.IsRemote
			}
			aPaused := strings.EqualFold(a.State, "paused")
			bPaused := strings.EqualFold(b.State, "paused")
			if aPaused != bPaused {
				return aPaused
			}
			if a.VideoHeight != b.VideoHeight {
				return a.VideoHeight > b.VideoHeight
			}
			if a.BitrateKbps != b.BitrateKbps {
				return a.BitrateKbps > b.BitrateKbps
			}
			if !a.StartedAt.Equal(b.StartedAt) {
				return a.StartedAt.After(b.StartedAt)
			}
			return a.ID < b.ID
		})
		return candidates[0], true

	case "oldest_paused":
		var candidates []ActiveSession
		for _, s := range sessions {
			if isExcluded(s) || !strings.EqualFold(s.State, "paused") {
				continue
			}
			pausedAt := sessionPausedAt(s)
			if sel.MinPaused > 0 && !pausedAt.IsZero() && sel.Now.Sub(pausedAt) < sel.MinPaused {
				continue
			}
			candidates = append(candidates, s)
		}
		if len(candidates) == 0 {
			return ActiveSession{}, false
		}
		sort.Slice(candidates, func(i, j int) bool {
			ai, aj := sessionPausedAt(candidates[i]), sessionPausedAt(candidates[j])
			if !ai.Equal(aj) {
				return ai.Before(aj)
			}
			return candidates[i].ID < candidates[j].ID
		})
		return candidates[0], true

	case "viewer_oldest":
		if sel.ViewerID == "" {
			return ActiveSession{}, false
		}
		var candidates []ActiveSession
		for _, s := range sessions {
			if isExcluded(s) || s.ViewerID != sel.ViewerID {
				continue
			}
			candidates = append(candidates, s)
		}
		if len(candidates) == 0 {
			return ActiveSession{}, false
		}
		sort.Slice(candidates, func(i, j int) bool {
			if !candidates[i].StartedAt.Equal(candidates[j].StartedAt) {
				return candidates[i].StartedAt.Before(candidates[j].StartedAt)
			}
			return candidates[i].ID < candidates[j].ID
		})
		return candidates[0], true

	default:
		return ActiveSession{}, false
	}
}

func sessionPausedAt(s ActiveSession) time.Time {
	if !s.PausedSince.IsZero() {
		return s.PausedSince
	}
	if !s.LastActive.IsZero() {
		return s.LastActive
	}
	return s.StartedAt
}
