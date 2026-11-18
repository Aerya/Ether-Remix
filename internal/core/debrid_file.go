package core

import (
	"net/url"
	"strings"
)

// ValidAllDebridFileURL accepts only the provider's standalone file links.
// They are sent to the configured API, never fetched as arbitrary HTTP URLs.
func ValidAllDebridFileURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "alldebrid.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	id := strings.TrimPrefix(u.Path, "/f/")
	if id == u.Path || id == "" {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// VerifiedFileIDPrefix préfixe l'ID et le GUID des fichiers AllDebrid déjà
// vérifiés (« Alkodiques ») importés depuis la base StreamFusion Reborn.
const VerifiedFileIDPrefix = "sfr-alko:"

// IsVerifiedFile reports whether r is a verified AllDebrid file (Alkodiques):
// a standalone provider file link whose playback costs one link unlock and no
// magnet. The importer stamps the ID and GUID prefix, which survives the
// indexer rewrite (only the display name and trust binding change); the file
// link itself must still be valid and the size known (as acquireFile requires).
func IsVerifiedFile(r Release) bool {
	return (strings.HasPrefix(r.ID, VerifiedFileIDPrefix) || strings.HasPrefix(r.GUID, VerifiedFileIDPrefix)) &&
		r.Size > 0 && r.Protocol == ProtocolHTTP && r.StreamDebrid == "alldebrid" && ValidAllDebridFileURL(r.StreamURL)
}
