package frontend

import "github.com/braedonsaunders/ether/internal/core"

// DetailBurst reports a client reading detail pages faster than a person
// could (a library sync). Such clients get no speculative detail work.
func DetailBurst(userID int64, clientID string, key RatingKey) bool {
	return core.DetailBurst(userID, clientID, key)
}
