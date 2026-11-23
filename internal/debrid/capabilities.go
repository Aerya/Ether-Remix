package debrid

import "context"

// knownTorrentFinder is a package-specific optional capability.
type knownTorrentFinder interface{ findKnown(string) string }

// backgroundTorrentFinder never waits for a network listing. A cold list
// starts one shared read-only refresh, retaining the background budget.
type backgroundTorrentFinder interface {
	findBackground(context.Context, string) string
}

// backgroundCacheOnlyProvider is a provider whose Acquire, with
// CachedOnly set and no torrent id, serves already-cached content
// without adding anything to the account (Premiumize's directdl).
// Only such providers may proceed in a background check after a
// positive CheckCached: every other provider's id-less Acquire adds
// a torrent, which background work must never do.
type backgroundCacheOnlyProvider interface{ supportsBackgroundCacheOnly() bool }

// edgePortableLinks is a provider whose CDN serves any of its links from
// every edge: a link's path and token stand on hosts other than the one
// the API pinned (measured on TorBox during its 2026-11-09 maintenance
// window). Only such providers may replay a link whose pinned edge died on
// an edge the engine remembers (resumeOnAnotherEdge).
type edgePortableLinks interface{ edgePortableLinks() bool }

// cdnEdgeApprover reports whether candidate (a host:port remembered from a
// completed block) stands in the same CDN as the host serving raw. It is
// the engine's last check before a probe hands the link's signed token to
// that edge: a provider without an approver never replays, whatever its
// other capabilities.
type cdnEdgeApprover interface {
	approvesCDNEdge(raw, candidate string) bool
}

// zipOnlyCheckBlindProvider is a provider whose bulk CheckCached cannot see
// torrents cached only as a single server-side ZIP archive (TorBox zips
// torrents of 100+ files and reports them uncached in /torrents/checkcached
// even when cached-only /torrents/createtorrent returns the cached zip).
// Returning a positive file-count threshold tells Service not to trust a
// CheckCached miss for public torrents at or above that file count.
type zipOnlyCheckBlindProvider interface{ zipOnlyCheckThreshold() int }

// queuedTorrentStatus is the result of checking a provider-queued download
// (accepted with a queue id instead of a torrent id).
type queuedTorrentStatus struct {
	// TorrentID is populated once the provider promotes the queued item into
	// an active or finished torrent on the account.
	TorrentID string
	// StillQueued reports that the item is still waiting in the provider's
	// queue and has not been promoted yet.
	StillQueued bool
	// QueuedID is the current queue id when StillQueued is true.
	QueuedID string
	// ActiveDownloads is the number of active downloads currently holding
	// slots ahead of the queued item, when the lookup obtained it cheaply.
	ActiveDownloads int
}

// queuedTorrentFollower is a provider that can queue an add behind active
// downloads without assigning a torrent id yet (TorBox queued_id), and check
// whether the queued item has started or is still queued without re-adding it.
type queuedTorrentFollower interface {
	followQueuedTorrent(ctx context.Context, hash, queuedHash, queuedID string) (queuedTorrentStatus, error)
}
