package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// RecapQuery asks for a viewer's listening recap: the week, month or year
// containing At (in Location), or all of it ("all"), optionally one library.
type RecapQuery struct {
	Period   string // week | month | year | all
	At       time.Time
	Location *time.Location
	Library  core.LibraryID
}

// RecapEntry is one ranked title in a recap with its plays and minutes.
type RecapEntry struct {
	Title   Title
	Plays   int
	Minutes float64
}

// RecapDay is one day's listening.
type RecapDay struct {
	Date    string // YYYY-MM-DD in the query's location
	Minutes float64
}

// HistoryRecap aggregates a viewer's completed plays in a period.
type HistoryRecap struct {
	Period          string
	From, To        time.Time
	Minutes         float64
	Plays           int
	DistinctTracks  int
	DistinctArtists int
	TopArtists      []RecapEntry
	TopTracks       []RecapEntry
	TopAlbums       []RecapEntry
	StreakCurrent   int
	StreakLongest   int
	ByDay           []RecapDay
	FirstPlayAt     *time.Time
	GeneratedAt     time.Time
}

// HistoryRecaps answers listening recaps from play history.
type HistoryRecaps interface {
	HistoryRecap(ctx context.Context, viewer *core.Viewer, q RecapQuery) (HistoryRecap, error)
}

// RecapPeriods lists the periods a recap may cover.
var RecapPeriods = []string{"week", "month", "year", "all"}
