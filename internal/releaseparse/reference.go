package releaseparse

import (
	"encoding/hex"
	"github.com/braedonsaunders/ether/internal/core"
	"net/url"
	"strings"
)

// ReferenceReason requires a reusable source locator, not a search result ID.
func ReferenceReason(r core.Release) string {
	if r.ID == "" {
		return "missing_release_id"
	}
	if r.Protocol == core.ProtocolHTTP {
		if referenceURL(r.StreamURL) {
			return ""
		}
	} else if r.Protocol == core.ProtocolTorrent {
		if validInfoHash(r.InfoHash) || referenceURL(r.NZBURL) {
			return ""
		}
		if u, err := url.Parse(r.MagnetURL); err == nil && u.Scheme == "magnet" {
			for _, xt := range u.Query()["xt"] {
				if strings.HasPrefix(xt, "urn:btih:") && validInfoHash(strings.TrimPrefix(xt, "urn:btih:")) {
					return ""
				}
			}
		}
	} else if r.Protocol == core.ProtocolUsenet && referenceURL(r.NZBURL) {
		return ""
	}
	return "missing_source_reference"
}
func referenceURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}
func validInfoHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}

// StableReference survives age-based refresh. Playback still checks identity,
// source configuration and actual availability; failed references are excluded.
func StableReference(r core.Release) bool {
	return ReferenceReason(r) == "" && (r.Protocol == core.ProtocolTorrent && (validInfoHash(r.InfoHash) || strings.HasPrefix(r.MagnetURL, "magnet:")) || r.Protocol == core.ProtocolHTTP && r.StreamDebrid != "" && core.ValidAllDebridFileURL(r.StreamURL))
}
