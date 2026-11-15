package plex

import "net/http"

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
