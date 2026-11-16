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
