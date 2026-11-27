package plex

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	pr := FromContext(r.Context())
	key, err := keyParam(r)
	if err != nil || s.host.Themes == nil || s.host.ThemeTitle == nil {
		http.NotFound(w, r)
		return
	}
	t, err := s.host.ThemeTitle(r.Context(), pr.User, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.host.Themes.ServeTheme(w, r, *t)
}

// themeURL is the show's theme route, as PMS writes it for the show and its
// seasons and episodes (one URL per show keeps client caches warm). schedule
// is true only on a detail open.
func (s *Server) themeURL(t frontend.Title, schedule bool) string {
	if s.host.Themes == nil {
		return ""
	}
	show := t.Key
	if t.Key.Kind() != frontend.KindShow {
		show = t.ShowKey
	}
	if show == 0 || s.host.Themes.Theme(t, schedule) == "" {
		return ""
	}
	return "/library/metadata/" + show.String() + "/theme/1"
}
