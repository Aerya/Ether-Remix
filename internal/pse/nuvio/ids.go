package nuvio

import (
	"fmt"
	"strconv"
	"strings"
)

// imdbOf valide un identifiant IMDb « tt… » ; les chaînes de télévision et
// autres identifiants de Nuvio sont ignorés.
func imdbOf(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if len(id) < 3 || !strings.HasPrefix(id, "tt") {
		return "", false
	}
	for _, r := range id[2:] {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return id, true
}

// Target est ce qu'une entrée Nuvio désigne : un film, ou un épisode d'une série.
type Target struct {
	IMDB            string
	Series          bool
	Season, Episode int
}

// targetOf décode content_id / video_id / saison / épisode d'une entrée.
func targetOf(contentID, contentType, videoID string, season, episode *int) (Target, bool) {
	imdb, ok := imdbOf(contentID)
	if !ok {
		return Target{}, false
	}
	switch contentType {
	case "movie":
		return Target{IMDB: imdb}, true
	case "series":
		s, e := -1, -1
		if season != nil && episode != nil {
			s, e = *season, *episode
		} else if parts := strings.Split(videoID, ":"); len(parts) == 3 {
			s1, err1 := strconv.Atoi(parts[1])
			e1, err2 := strconv.Atoi(parts[2])
			if err1 == nil && err2 == nil {
				s, e = s1, e1
			}
		}
		if s < 0 || e < 0 {
			return Target{}, false
		}
		return Target{IMDB: imdb, Series: true, Season: s, Episode: e}, true
	}
	return Target{}, false
}

// videoID est l'identifiant de lecture Nuvio : « tt » ou « tt:saison:épisode ».
func (t Target) videoID() string {
	if !t.Series {
		return t.IMDB
	}
	return fmt.Sprintf("%s:%d:%d", t.IMDB, t.Season, t.Episode)
}

func (t Target) contentType() string {
	if t.Series {
		return "series"
	}
	return "movie"
}

// progressEntry est l'entrée poussée par sync_push_watch_progress.
func (t Target) progressEntry(positionMs, durationMs, lastWatchedMs int64) map[string]any {
	m := map[string]any{
		"content_id": t.IMDB, "content_type": t.contentType(), "video_id": t.videoID(),
		"position": positionMs, "duration": durationMs, "last_watched": lastWatchedMs,
	}
	if t.Series {
		m["season"], m["episode"] = t.Season, t.Episode
	}
	return m
}

// watchedItem est l'entrée poussée par sync_push_watched_items.
func (t Target) watchedItem(title string, watchedAtMs int64) map[string]any {
	m := map[string]any{"content_id": t.IMDB, "content_type": t.contentType(), "title": title, "watched_at": watchedAtMs}
	if t.Series {
		m["season"], m["episode"] = t.Season, t.Episode
	}
	return m
}
