package core

import "time"

// Loudness is how loud an audio item plays and where it starts, ends and
// fades, so a client can level it (ReplayGain 2.0, reference -18 LUFS) and
// time a crossfade. Peaks are linear true peak (1.0 = 0 dBTP).
type Loudness struct {
	TrackGainDB float64
	TrackPeak   float64
	LUFS        float64
	// AlbumGainDB and AlbumPeak are set once every track of the item's album
	// is measured.
	AlbumGainDB *float64
	AlbumPeak   *float64
	Source      string // LoudnessMeasured or LoudnessProvider
	Trim        *LoudnessTrim
}

// LoudnessTrim marks silence and fades in milliseconds from the start.
type LoudnessTrim struct {
	StartMs        int64
	EndMs          int64
	FadeInEndMs    int64
	FadeOutStartMs int64
}

const (
	LoudnessMeasured = "measured" // Ether measured the file
	LoudnessProvider = "provider" // the catalogue's estimate, gain only
)

// HistoryPlay is one completed play in a viewer's history.
type HistoryPlay struct {
	Key RatingKey
	At  time.Time
}
