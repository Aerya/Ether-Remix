package core

import "errors"

// ImportTrack describes one track of a playlist being imported, as the link
// or file gave it.
type ImportTrack struct {
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Album       string  `json:"album,omitempty"`
	ISRC        string  `json:"isrc,omitempty"`
	DurationSec float64 `json:"durationSec,omitempty"`
}

// KindImportRequest asks a kind's plugin to read a playlist link.
type KindImportRequest struct {
	URL string `json:"url"`
}

// KindImportResponse is a link's playlist: its name and tracks in order.
type KindImportResponse struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Art         string        `json:"art,omitempty"`
	Tracks      []ImportTrack `json:"tracks"`
	// Total is how many tracks the source playlist has, when known.
	Total int `json:"total,omitempty"`
	// Truncated: the source shared fewer tracks than the playlist has.
	Truncated bool `json:"truncated,omitempty"`
}

// KindIdentifyRequest asks a kind's plugin to find its items for tracks.
type KindIdentifyRequest struct {
	Tracks []ImportTrack `json:"tracks"`
}

// KindIdentifyMatch is the plugin's answer for one requested track (by its
// index in the request): the matched item's external id and how it matched,
// or the candidates when several releases fit equally.
type KindIdentifyMatch struct {
	Index      int      `json:"index"`
	ID         string   `json:"id,omitempty"`
	By         string   `json:"by,omitempty"` // isrc | title
	Candidates []string `json:"candidates,omitempty"`
}

// KindIdentifyResponse carries every item the matches need (parents before
// children; Ether stores them like live search results) and the matches.
type KindIdentifyResponse struct {
	Items   []KindItem          `json:"items"`
	Matches []KindIdentifyMatch `json:"matches"`
}

// ImportMatch is one identified track as stored items: the matched key, or
// the candidate keys when ambiguous.
type ImportMatch struct {
	Index      int
	Key        RatingKey
	By         string
	Candidates []RatingKey
}

// ErrUnsupportedImport means a playlist link or file cannot be imported.
var ErrUnsupportedImport = errors.New("unsupported playlist import")

// ErrInvalidImportChoice means an import choice is not one of a track's candidates.
var ErrInvalidImportChoice = errors.New("invalid import choice")
