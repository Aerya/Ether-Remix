package veladist

import (
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/updater"
)

const (
	// CacheControlImmutable is sent for content-hashed and version-addressed
	// static Vela assets whose bytes never change at the same URL.
	CacheControlImmutable = "public, max-age=31536000, immutable"

	// CacheControlRevalidate is sent for stable shell documents and unhashed
	// files whose bytes can change at the same URL across Vela releases.
	CacheControlRevalidate = "no-cache"
)

// StaticCacheControl returns the HTTP Cache-Control header value for a static
// file served under /vela/ at path rel (relative to the installed Vela root).
func StaticCacheControl(rel string) string {
	if IsImmutableStaticAsset(rel) {
		return CacheControlImmutable
	}
	return CacheControlRevalidate
}

// IsImmutableStaticAsset reports whether rel identifies a static Vela asset
// whose URL is content-hashed or version-addressed so browsers can cache it
// immutably for a year. HTML documents, web manifests, service workers, and
// stable shell files are never immutable.
func IsImmutableStaticAsset(rel string) bool {
	cleaned, ok := cleanStaticRelPath(rel)
	if !ok {
		return false
	}
	base := path.Base(cleaned)
	if strings.HasPrefix(base, ".") {
		return false
	}
	baseLower := strings.ToLower(base)
	ext := strings.ToLower(path.Ext(base))

	// HTML documents and web manifests are never immutable, even if fingerprinted.
	if ext == ".html" || ext == ".htm" || ext == ".webmanifest" {
		return false
	}
	if baseLower == "manifest.json" || strings.HasSuffix(baseLower, ".manifest.json") {
		return false
	}
	if ext == ".json" && (strings.HasPrefix(baseLower, "manifest-") || strings.HasPrefix(baseLower, "manifest.")) {
		return false
	}
	// Service workers are always revalidated.
	if baseLower == "sw.js" || baseLower == "service-worker.js" {
		return false
	}

	parts := strings.Split(cleaned, "/")
	// Stable root shell scripts and release metadata files change in place.
	if len(parts) == 1 {
		switch baseLower {
		case "pwa.js", "boot-handoff.js", "routes.json", "app.version.json", "engines.json", "player-assets-manifest.json":
			return false
		}
	}

	// Digest- and semver-addressed subtrees shipped by Vela releases.
	if len(parts) >= 3 && parts[0] == "player" && hexID(parts[1], 64) {
		return true
	}
	if len(parts) >= 4 && parts[0] == "skins" {
		if _, err := updater.ParseVersion(parts[2]); err == nil {
			return true
		}
	}

	if ext == "" {
		return false
	}
	stem := strings.TrimSuffix(base, path.Ext(base))
	if ext == ".map" {
		if innerExt := path.Ext(stem); innerExt != "" {
			stem = strings.TrimSuffix(stem, innerExt)
		}
	}
	for _, token := range strings.FieldsFunc(stem, func(r rune) bool {
		return r == '-' || r == '.' || r == '_' || r == '@'
	}) {
		if isContentDigestToken(token) {
			return true
		}
	}
	return false
}

func cleanStaticRelPath(rel string) (string, bool) {
	if rel == "" || strings.Contains(rel, "\\") {
		return "", false
	}
	if strings.HasPrefix(rel, "/vela/") {
		rel = strings.TrimPrefix(rel, "/vela/")
	} else if rel == "/vela" {
		return "", false
	}
	trimmed := strings.TrimPrefix(rel, "/")
	if trimmed == "" {
		return "", false
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == ".." {
			return "", false
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+trimmed), "/")
	if cleaned == "" || cleaned == "." {
		return "", false
	}
	return cleaned, true
}

func isContentDigestToken(token string) bool {
	if hexID(token, 16) {
		return true
	}
	// Short bundler hashes (8..15 hex chars) require at least one digit and one
	// hex letter so dates (20261120), dimensions, and English words (deadbeef)
	// are not misclassified as content digests.
	if len(token) < 8 || len(token) > 15 {
		return false
	}
	hasDigit, hasHexLetter := false, false
	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
			hasHexLetter = true
		default:
			return false
		}
	}
	return hasDigit && hasHexLetter
}
