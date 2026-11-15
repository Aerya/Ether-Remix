package frontend

import "net/http"

// Themes is a cache-only detail/read port. schedule is true only on detail opens.
// ServeTheme serves local audio with Range support and never resolves providers.
type Themes interface {
	ThemeEnabled() bool
	Theme(Title, bool) bool
	ServeTheme(http.ResponseWriter, *http.Request, Title)
}
