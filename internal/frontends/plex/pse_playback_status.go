package plex

import (
	"errors"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// psePartTerminal reports a failure that no retry can fix for now: the
// indexer budget or pause is spent, or the debrid account reached its quota.
// Players such as Plezy retry a 502 on the part; a 404 ends the attempt and
// shows "file unavailable" (PSE-HOOK dans playback.go).
func psePartTerminal(err error) bool {
	return errors.Is(err, frontend.ErrBudgetExhausted) || errors.Is(err, frontend.ErrDebridQuota)
}
