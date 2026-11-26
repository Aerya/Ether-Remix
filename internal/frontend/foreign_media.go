package frontend

import "time"

// ForeignMediaRatio bounds how far a reported media duration may fall below
// the title's catalogue runtime and still be the title's own media. Real
// files land within a few percent of the listed runtime; a trailer, theme or
// preview is a few percent of it.
const ForeignMediaRatio = 10

// ForeignMediaDuration reports whether a playback report's duration cannot
// belong to the title's own media: a trailer, theme song or preview a client
// attributed to its parent title. Progress judged against such a duration
// reads a finished two-minute trailer as a finished film, so these reports
// never write the title's watch state or taste evidence. Unknown runtimes and
// durations are never foreign.
func ForeignMediaDuration(runtime, reported time.Duration) bool {
	return runtime > 0 && reported > 0 && reported*ForeignMediaRatio < runtime
}
