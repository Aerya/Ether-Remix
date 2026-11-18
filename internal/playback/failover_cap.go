package playback

import (
	"errors"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
)

// maxFailoverAttempts is the highest resolver.failoverAttempts accepted.
const maxFailoverAttempts = 10

// failoverAttempts is the cap on the releases one open or failover tries:
// resolver.failoverAttempts (1 to 10, validated by the admin API); 0 falls
// back to Options.MaxAttempts (default 4).
func (m *Manager) failoverAttempts() int {
	n := m.config().Resolver.FailoverAttempts
	if n <= 0 {
		n = m.opts.MaxAttempts
	}
	// A value typed by hand in config.json skips the admin validation.
	return min(n, maxFailoverAttempts)
}

// accountError reports a failure of the whole debrid account (quota reached,
// key rejected): no other release that goes through a debrid service can
// play until the owner or the clock fixes it, so trying them is wasted work.
func accountError(err error) bool {
	return errors.Is(err, core.ErrDebridQuota) || errors.Is(err, core.ErrDebridAuth)
}

// dependsOnDebrid reports a release that plays only through a debrid
// account: a torrent, a verified AllDebrid file or an add-on link a debrid
// service unlocked. Usenet releases and plain HTTP links do not.
func dependsOnDebrid(rel core.Release) bool {
	return rel.Protocol == core.ProtocolTorrent || core.IsVerifiedFile(rel) || (rel.Protocol == core.ProtocolHTTP && rel.StreamDebrid != "")
}

// maxDebridSkips bounds how many debrid releases a search steps over after an
// account error before the attempt gives up (each skip costs one resolve).
const maxDebridSkips = 8

// withoutDebrid drops the releases that depend on a debrid account.
func withoutDebrid(rels []core.Release) []core.Release {
	return slices.DeleteFunc(slices.Clone(rels), dependsOnDebrid)
}
