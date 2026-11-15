package nzb

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MetaEpisode is the <meta> type that tells the stream engine which episode
// of a multi-episode archive to play ("season:episode:absolute").
const MetaEpisode = "ether-episode"

// EpisodeHint is the wanted episode of a season held inside an archive.
type EpisodeHint struct {
	Season, Episode, Absolute int
}

// String is the <meta> value for h.
func (h EpisodeHint) String() string {
	return fmt.Sprintf("%d:%d:%d", h.Season, h.Episode, h.Absolute)
}

// EpisodeHint returns the document's episode hint, if it has a valid one.
func (n *NZB) EpisodeHint() (EpisodeHint, bool) {
	var h EpisodeHint
	v := strings.TrimSpace(n.MetaValue(MetaEpisode))
	if v == "" {
		return h, false
	}
	if c, err := fmt.Sscanf(v, "%d:%d:%d", &h.Season, &h.Episode, &h.Absolute); err != nil || c != 3 || h.Episode <= 0 {
		return EpisodeHint{}, false
	}
	return h, true
}

// MetaMovie carries the requested movie identity into an archive's member selector.
const MetaMovie = "ether-movie"

type MovieHint struct {
	Titles []string
	Year   int
}

func (h MovieHint) String() string { b, _ := json.Marshal(h); return string(b) }
func (n *NZB) MovieHint() (MovieHint, bool) {
	var h MovieHint
	err := json.Unmarshal([]byte(n.MetaValue(MetaMovie)), &h)
	return h, err == nil && len(h.Titles) > 0
}
