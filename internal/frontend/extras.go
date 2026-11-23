package frontend

import (
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// MaxExtras caps how many extras one title advertises.
const MaxExtras = 10

// ExtraSourceCommand marks an ExtraClip whose video is resolved by the owner's command.
const ExtraSourceCommand = "command"

// ExtraSourcePlexDiscover marks an ExtraClip whose direct MP4 comes from Plex Discover.
const ExtraSourcePlexDiscover = "plex_discover"

// ExtraClip is one playable clip attached to a catalogue title.
type ExtraClip struct {
	Key         core.RatingKey
	ParentKey   core.RatingKey
	ParentTitle string
	Title       string
	Subtype     string
	ExtraType   int
	Site        string
	SiteKey     string
	DiscoverKey string
	Language    string
	Official    bool
	PublishedAt time.Time
	Size        int
	Duration    time.Duration
	Source      string
}

// IsTrailer reports whether the extra can serve as a Cinema Trailer.
func (e ExtraClip) IsTrailer() bool { return e.Subtype == "trailer" || e.Subtype == "teaser" }

// ResolvedExtra is a playable location for an extra clip.
type ResolvedExtra struct {
	URL       string
	LocalPath string // complete local MP4 prepared by the extras service
	Headers   map[string]string
	Expires   time.Time
	Container string
}

// Input returns the ffmpeg input.
func (r ResolvedExtra) Input() string {
	if r.LocalPath != "" {
		return r.LocalPath
	}
	return r.URL
}
