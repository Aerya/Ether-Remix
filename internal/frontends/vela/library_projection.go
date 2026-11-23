package vela

import "github.com/braedonsaunders/ether/internal/frontend"

func NativeLibraryType(mt frontend.MediaType) string {
	if mt == frontend.TypeShow {
		return "shows"
	}
	return "movies"
}

// ProjectLibrary interprets this adapter's presentation declaration.
// Console-only and adapter-only sources remain unpublished; visibility is
// still applied by Experience.Views.
func ProjectLibrary(p frontend.Presentations, fallback frontend.MediaType) (frontend.MediaType, string, bool) {
	if p.AdapterOnly || p.Vela == nil {
		return "", "", false
	}
	switch p.Vela.Type {
	case "movies":
		return frontend.TypeMovie, "movies", true
	case "shows":
		return frontend.TypeShow, "shows", true
	}
	return "", "", false
}
