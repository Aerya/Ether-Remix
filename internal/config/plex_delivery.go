package config

// Plex media delivery modes (plex.delivery).
const (
	// DeliveryRelay streams media bytes through Ether.
	DeliveryRelay = "relay"
	// DeliveryRedirect answers an authorised direct-play part read with a 307
	// to the provider's own URL, when the session has one.
	DeliveryRedirect = "redirect"
)

// Redirects reports whether Plex media is handed to the provider's own URL instead
// of being streamed through Ether. Only an explicit "redirect" does: an unset or
// unrecognised value relays, which is the behaviour every other frontend and every
// older config expects. A method rather than a constant comparison so a protocol
// adapter can ask the question without importing config (see the frontend import
// guard).
func (p Plex) Redirects() bool { return p.Delivery == DeliveryRedirect }
