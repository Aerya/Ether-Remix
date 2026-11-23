package debrid

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// atomicHashCacheProvider is implemented by providers whose cached-only,
// id-less Acquire resolves only an already-cached torrent by bare info hash
// without adding an uncached torrent to the account.
type atomicHashCacheProvider interface {
	supportsAtomicHashCache(infoHash string) bool
}

// normalizeBareHash returns a lower-case 40-character hex BitTorrent v1 info
// hash, converting a 32-character base32 hash; "" when s is neither.
func normalizeBareHash(s string) string {
	s = strings.TrimSpace(s)
	switch len(s) {
	case 40:
		if _, err := hex.DecodeString(s); err == nil {
			return strings.ToLower(s)
		}
	case 32:
		if b, err := base32.StdEncoding.DecodeString(strings.ToUpper(s)); err == nil && len(b) == 20 {
			return hex.EncodeToString(b)
		}
	}
	return ""
}

// bareMagnet builds a trackerless magnet URI containing only the normalized
// v1 info hash (no tracker URLs, passkeys or display name).
func bareMagnet(infoHash string) string {
	h := normalizeBareHash(infoHash)
	if h == "" {
		return ""
	}
	return "magnet:?xt=urn:btih:" + h
}

// prepareAcquire is the single gate for torrent acquisition requests sent to
// any debrid provider. For private-tracker torrents it enforces:
//  1. bare info hash only (any tracker URLs or magnet query parameters are stripped);
//  2. existing debrid cache only (CachedOnly and RemoveUncached forced on);
//  3. never adding or downloading an uncached release.
func prepareAcquire(ctx context.Context, p Provider, req AcquireRequest, knownCached bool) (AcquireRequest, error) {
	if !req.Private {
		return req, nil
	}
	hash := normalizeBareHash(req.InfoHash)
	if hash == "" {
		return AcquireRequest{}, fmt.Errorf("debrid: private torrent has no valid info hash: %w", core.ErrReleaseRejected)
	}
	req.InfoHash = hash
	req.Magnet = "magnet:?xt=urn:btih:" + hash
	req.CachedOnly = true
	req.RemoveUncached = true
	req.privateChecked = true
	if req.TorrentID != "" {
		return req, nil
	}
	if !knownCached {
		cached, err := p.CheckCached(ctx, []string{hash})
		if err == nil {
			if !cached[hash] {
				return AcquireRequest{}, core.ErrPrivateUncached
			}
			knownCached = true
		} else if !errors.Is(err, ErrUnsupported) {
			return AcquireRequest{}, err
		}
	}
	if !knownCached {
		if id, ferr := p.Find(ctx, hash); ferr == nil && id != "" {
			req.TorrentID = id
			return req, nil
		}
		atomic, ok := p.(atomicHashCacheProvider)
		if !ok || !atomic.supportsAtomicHashCache(hash) {
			return AcquireRequest{}, core.ErrPrivateUncached
		}
	}
	return req, nil
}

// prepareHandle strips any tracker parameters from a private payload handle
// before a provider regenerates a direct link from its magnet.
func prepareHandle(h Handle) (Handle, error) {
	if !h.Private {
		return h, nil
	}
	hash := normalizeBareHash(h.InfoHash)
	if hash == "" {
		return Handle{}, fmt.Errorf("debrid: private torrent handle has no valid info hash: %w", core.ErrReleaseRejected)
	}
	h.InfoHash = hash
	h.Magnet = "magnet:?xt=urn:btih:" + hash
	return h, nil
}

func wrapPrivateNotCached(private bool, err error) error {
	if private && err != nil && errors.Is(err, core.ErrNotCached) && !errors.Is(err, core.ErrDownloadLimit) && !errors.Is(err, core.ErrPrivateUncached) {
		return core.ErrPrivateUncached
	}
	return err
}
