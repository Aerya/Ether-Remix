package frontend

// DefaultPort returns the default listener port for protocols that support
// a dedicated listener. Silo uses its Compose default; Vela's dedicated
// listener is optional (off until configured) and defaults to 8098.
func DefaultPort(id Protocol) (int, bool) {
	switch id {
	case "plex": // Listener default wiring.
		return 32400, true
	case "jellyfin": // Listener default wiring.
		return 8096, true
	case "emby": // Listener default wiring.
		return 8097, true
	case "silo":
		return 8090, true
	case "vela":
		return 8098, true
	default:
		return 0, false
	}
}
