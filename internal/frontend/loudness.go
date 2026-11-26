package frontend

import (
	"context"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// LoudnessRef names a title and its album (parent) for a loudness lookup.
type LoudnessRef struct {
	Key    core.RatingKey
	Parent core.RatingKey
}

// LoudnessReader answers measured loudness for a page of titles in a bounded
// number of indexed queries, and for the exact source a playback opened.
type LoudnessReader interface {
	PageLoudness(ctx context.Context, refs []LoudnessRef) map[core.RatingKey]core.Loudness
	ForSource(ctx context.Context, key core.RatingKey, source string) (core.Loudness, bool)
}

// Loudness re-exports core.Loudness for frontends.
type Loudness = core.Loudness

// LoudnessMeasured and LoudnessProvider name where a loudness came from.
const (
	LoudnessMeasured = core.LoudnessMeasured
	LoudnessProvider = core.LoudnessProvider
)

// QueuedHandover is how long an item a queued successor took over may keep
// playing out (a crossfade) before the server retires its session.
const QueuedHandover = 15 * time.Second

// LoudnessSource keys a loudness measurement to one file of an item: its byte
// size, known to both the opened media and the playback plan.
func LoudnessSource(size int64) string {
	if size <= 0 {
		return ""
	}
	return "size:" + strconv.FormatInt(size, 10)
}
