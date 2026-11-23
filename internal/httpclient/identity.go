// Package httpclient holds shared outbound HTTP identity defaults.
package httpclient

import (
	"strings"
	"sync"
)

// RepoURL is the project URL included in Ether's honest outbound User-Agent.
const RepoURL = "https://github.com/braedonsaunders/ether"

// IndexerOperatorsURL documents Ether's indexer traffic for operators.
const IndexerOperatorsURL = RepoURL + "/blob/main/docs/indexer-operators.md"

var (
	versionMu sync.RWMutex
	versionFn = func() string { return "dev" }
)

// SetVersionProvider registers the build-version lookup used by UserAgent.
// internal/updater wires its linker-injected Version variable at init time so
// packages that cannot import updater still report the build version without
// import cycles.
func SetVersionProvider(fn func() string) {
	if fn == nil {
		return
	}
	versionMu.Lock()
	versionFn = fn
	versionMu.Unlock()
}

// FormatUserAgent formats Ether's outbound User-Agent for version ("dev" when
// blank).
func FormatUserAgent(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "dev"
	}
	return "Ether/" + v + " (+" + RepoURL + ")"
}

// UserAgent returns Ether's outbound User-Agent header value:
// "Ether/<version> (+https://github.com/braedonsaunders/ether)".
func UserAgent() string {
	versionMu.RLock()
	fn := versionFn
	versionMu.RUnlock()
	return FormatUserAgent(fn())
}

// FormatIndexerUserAgent formats Ether's honest outbound User-Agent for
// indexer searches and NZB/torrent downloads ("dev" when version is blank).
func FormatIndexerUserAgent(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "dev"
	}
	return "Ether/" + v + " (+" + IndexerOperatorsURL + ")"
}

// IndexerUserAgent returns Ether's honest default User-Agent for indexers:
// "Ether/<version> (+https://github.com/braedonsaunders/ether/blob/main/docs/indexer-operators.md)".
func IndexerUserAgent() string {
	versionMu.RLock()
	fn := versionFn
	versionMu.RUnlock()
	return FormatIndexerUserAgent(fn())
}
