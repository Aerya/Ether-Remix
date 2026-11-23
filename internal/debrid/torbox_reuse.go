package debrid

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Positive hints are validated by the normal per-id readiness request.
// Separate from the short-lived account snapshot; normal per-id reads validate hints.
func (p *torBox) findKnown(hash string) string {
	p.knownMu.Lock()
	defer p.knownMu.Unlock()
	return p.known[strings.ToLower(hash)]
}

func (p *torBox) rememberKnown(hash, id string) {
	p.knownMu.Lock()
	defer p.knownMu.Unlock()
	if p.known == nil {
		p.known = make(map[string]string)
	}
	hash = strings.ToLower(hash)
	if _, ok := p.known[hash]; !ok && len(p.known) >= 4096 {
		for k := range p.known {
			delete(p.known, k)
			break
		}
	}
	p.known[hash] = id
}

// findFresh is only the recovery path for a documented DUPLICATE_ITEM.
// Matching by hash preserves episode/series identity and avoids duplicate
// account entries without an unconditional account-wide request on Play.
func (p *torBox) findFresh(ctx context.Context, hash string) (string, error) {
	id, _, err := p.findDuplicate(ctx, hash)
	return id, err
}

func (p *torBox) findDuplicate(ctx context.Context, hash string) (string, int, error) {
	// D: a positive fresh/in-flight background snapshot already identifies
	// this duplicate. A miss still needs the provider's fresh recovery read.
	if id, err := p.list.reuse(ctx, p.opts.Now(), hash); id != "" || err != nil {
		return id, 0, err
	}
	return p.findInMyList(ctx, hash)
}

func (p *torBox) findInMyList(ctx context.Context, hashes ...string) (string, int, error) {
	data, err := p.do(ctx, apiRequest{method: http.MethodGet, path: "/torrents/mylist", query: url.Values{"bypass_cache": {"true"}, "files": {"false"}}})
	if err != nil {
		return "", 0, err
	}
	items, err := decodeTorBoxTorrents(data)
	if err != nil {
		return "", 0, err
	}
	var foundID string
	active := 0
	for _, it := range items {
		if it.Active {
			active++
		}
		id := tbID(it.ID)
		if id == "" || !matchesQueuedHash(it.Hash, hashes...) {
			continue
		}
		if it.DownloadPresent || it.DownloadFinished {
			return id, active, nil
		}
		if foundID == "" {
			foundID = id
		}
	}
	return foundID, active, nil
}
