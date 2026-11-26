package frontend

import "net/http"

// Themes is a cache-only detail/read port. schedule is true only on detail opens.
// Theme returns the cached container ("mp3" or "m4a"), or "" when there is
// none; a show/season open with schedule may wait briefly for a first fetch.
// ServeTheme serves local audio with Range support and never resolves providers.
type Themes interface {
	ThemeEnabled() bool
	Theme(Title, bool) string
	ServeTheme(http.ResponseWriter, *http.Request, Title)
}
