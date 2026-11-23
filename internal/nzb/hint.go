package nzb

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MetaEpisode is the <meta> type that tells the stream engine which episode
// of a multi-episode archive to play ("season:episode:absolute[,s:e...]").
const MetaEpisode = "ether-episode"

// EpisodeHint is the wanted episode of a season held inside an archive.
type EpisodeHint struct {
	Season, Episode, Absolute int
	Aliases                   [][2]int
}

// String is the <meta> value for h.
func (h EpisodeHint) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d:%d:%d", h.Season, h.Episode, h.Absolute)
	for _, a := range h.Aliases {
		if a[0] > 0 && a[1] > 0 {
			fmt.Fprintf(&b, ",%d:%d", a[0], a[1])
		}
	}
	return b.String()
}

// EpisodeHint returns the document's episode hint, if it has a valid one.
func (n *NZB) EpisodeHint() (EpisodeHint, bool) {
	var h EpisodeHint
	v := strings.TrimSpace(n.MetaValue(MetaEpisode))
	if v == "" {
		return h, false
	}
	parts := strings.Split(v, ",")
	if c, err := fmt.Sscanf(parts[0], "%d:%d:%d", &h.Season, &h.Episode, &h.Absolute); err != nil || c != 3 || h.Episode <= 0 {
		return EpisodeHint{}, false
	}
	for _, part := range parts[1:] {
		var s, e int
		if c, err := fmt.Sscanf(part, "%d:%d", &s, &e); err == nil && c == 2 && s > 0 && e > 0 {
			h.Aliases = append(h.Aliases, [2]int{s, e})
		}
	}
	return h, true
}

// MetaMovie carries the requested movie identity and audio language preferences
// into multi-file NZB and archive member selection.
const MetaMovie = "ether-movie"

type MovieHint struct {
	Titles           []string `json:"Titles,omitempty"`
	Year             int      `json:"Year,omitempty"`
	WantedLanguages  []string `json:"WantedLanguages,omitempty"`
	OriginalLanguage string   `json:"OriginalLanguage,omitempty"`
}

func (h MovieHint) String() string { b, _ := json.Marshal(h); return string(b) }
func (n *NZB) MovieHint() (MovieHint, bool) {
	var h MovieHint
	err := json.Unmarshal([]byte(n.MetaValue(MetaMovie)), &h)
	return h, err == nil && (len(h.Titles) > 0 || len(h.WantedLanguages) > 0)
}

// MetaNonVideo carries non-video release selection intent (explicit posted
// file, archive member, or media MIME) from a plugin release target into the
// stream engine.
const MetaNonVideo = "ether-nonvideo"

// MetaSubjectNames preserves original subject-derived file names (keyed by
// 0-based file index in the emitted NZB) when re-encoding rewrites a file's
// subject to carry a PAR2/yEnc-learned name.
const MetaSubjectNames = "ether-subject-names"

// TrackHint identifies a track inside a multi-track audio release or archive.
type TrackHint struct {
	Disc   int    `json:"disc,omitempty"`
	Number int    `json:"number,omitempty"`
	Title  string `json:"title,omitempty"`
}

// NonEmpty reports whether the hint specifies a track number or title.
func (t *TrackHint) NonEmpty() bool {
	return t != nil && (t.Number > 0 || strings.TrimSpace(t.Title) != "")
}

// NonVideoHint tells the stream engine to serve a non-video file directly
// (loose file or archive member) instead of running video-only heuristics.
type NonVideoHint struct {
	ExplicitFile bool       `json:"explicitFile,omitempty"`
	ExactFile    string     `json:"exactFile,omitempty"`
	RawFile      bool       `json:"rawFile,omitempty"`
	Member       string     `json:"member,omitempty"`
	MemberIndex  *int       `json:"memberIndex,omitempty"`
	Track        *TrackHint `json:"track,omitempty"`
	MIME         string     `json:"mime,omitempty"`
}

func (h NonVideoHint) String() string { b, _ := json.Marshal(h); return string(b) }

// NonVideoHint returns the document's non-video selection hint, if present.
func (n *NZB) NonVideoHint() (NonVideoHint, bool) {
	raw := strings.TrimSpace(n.MetaValue(MetaNonVideo))
	if raw == "" {
		return NonVideoHint{}, false
	}
	var h NonVideoHint
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		return NonVideoHint{}, false
	}
	if !h.ExplicitFile && !h.RawFile && h.Member == "" && h.MemberIndex == nil && !h.Track.NonEmpty() && !IsNonVideoMIME(h.MIME) {
		return NonVideoHint{}, false
	}
	return h, true
}
