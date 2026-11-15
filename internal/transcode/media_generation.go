package transcode

import (
	"net/http"
	urlpath "path"
	"strings"
)

// A public session ID survives seek and track restarts, but its bytes do not.
// Scope every advertised HLS media resource to the concrete session instance.
// The prefix lives in the filename so relative URLs and frontend route shapes
// (including Plex's base/rung directories) keep working.
func (s *Session) scopeMediaName(name string) string {
	if s.isDASH() || s.mediaGeneration == "" {
		return name
	}
	dir, file := urlpath.Split(name)
	return dir + "g-" + s.mediaGeneration + "-" + file
}

func splitMediaGeneration(name string) (generation, file string, ok bool) {
	if len(name) < 36 || !strings.HasPrefix(name, "g-") || name[34] != '-' {
		return "", "", false
	}
	generation = name[2:34]
	for _, c := range generation {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", "", false
		}
	}
	file = name[35:]
	return generation, file, file != ""
}

func (s *Session) acceptMediaGeneration(w http.ResponseWriter, r *http.Request) bool {
	if s.isDASH() || s.mediaGeneration == "" {
		return true
	}
	generation, _, ok := splitMediaGeneration(urlpath.Base(r.URL.Path))
	if !ok || generation != s.mediaGeneration {
		// Never let a delayed range request for old bytes reach a new producer.
		http.Error(w, "media generation expired", http.StatusGone)
		return false
	}
	return true
}
