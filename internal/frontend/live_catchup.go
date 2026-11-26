package frontend

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// CatchUpRequest selects either a guide programme or an explicit archive range.
type CatchUpRequest struct {
	ChannelID, ProgrammeID string
	Start                  time.Time
	Duration               time.Duration
	StartOver              bool
	ClampStart             bool
	// DASH selects a finite DASH presentation for native Plex playback.
	DASH bool
	// InitialOffset starts a finite playback inside the selected range.
	InitialOffset time.Duration
}

type CatchUpInfo struct {
	ID, ChannelID, ProgrammeID, Title string
	Start, End, ProgrammeEnd          time.Time
	StartOver, DASH                   bool
	Origin                            time.Duration
	MediaGeneration                   string
	Decision                          Decision
}

var ErrInvalidCatchUpRange = errors.New("invalid catch-up range")

type CatchUpError struct {
	Code        string
	ArchiveDays int
	EarliestAt  time.Time
}

func (e *CatchUpError) Error() string {
	switch e.Code {
	case "catchup_not_supported":
		return "This channel has no catch-up archive."
	case "catchup_outside_archive":
		return "The requested range starts outside the channel archive."
	default:
		return "The provider has no playable archive for this range."
	}
}

// LiveCatchUp is the archive transport shared by Live TV frontends.
type LiveCatchUp interface {
	StartCatchUp(context.Context, *User, CatchUpRequest, ClientCaps, string) (CatchUpInfo, error)
	SeekCatchUp(context.Context, *User, string, time.Duration) (CatchUpInfo, error)
	TouchLiveSession(context.Context, *User, string) error
	CatchUpLiveWindow(context.Context, string) (LiveWindow, error)
	ServeCatchUpTS(http.ResponseWriter, *http.Request, string, *User)
}

// LiveCatchUpCapability reads the authorized archive window without building channel wire metadata.
type LiveCatchUpCapability interface {
	LiveCatchUpMaxDays(context.Context, *Viewer) (int, error)
}

func ArchiveFlags(days int, start, end, now time.Time) (catchUp, startOver bool) {
	if days <= 0 || !end.After(start) || start.After(now) || start.Before(now.Add(-time.Duration(min(days, 30))*24*time.Hour)) {
		return false, false
	}
	return !end.After(now), end.After(now)
}
