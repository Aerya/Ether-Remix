package plex

import "strings"

// libraryToolProducts are X-Plex-Product values sent by monitoring and
// library-management tools. They read metadata in bulk to mirror or
// reconcile a library and never play anything, so their reads must not be
// taken as a viewer opening a title: no version discovery, no warm-ups, no
// source probes and no session intent. PlexAPI is the python-plexapi
// default, used by scripts and by tools that do not set their own product.
var libraryToolProducts = []string{"tautulli", "tracearr", "kometa", "plex meta manager", "plexapi"}

// libraryToolClient reports whether pr comes from a monitoring or
// library-management tool rather than a player.
func libraryToolClient(pr *Request) bool {
	if pr == nil {
		return false
	}
	product := strings.ToLower(strings.TrimSpace(pr.Product))
	for _, p := range libraryToolProducts {
		if product == p {
			return true
		}
	}
	return false
}
