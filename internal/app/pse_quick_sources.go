package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/stremio"
)

// Use the existing manager so source caches, budgets, admission, egress and
// circuit breakers are shared with advanced playback and indexing.
func (a *App) searchQuickSource(ctx context.Context, req core.SearchRequest, source, seedstremID string) ([]core.Release, error) {
	if source == "torrent" && a.pseTorrentProvider() == nil {
		return nil, nil
	}
	if source == "usenet" && (a.fetcher == nil || !a.fetcher.HasUsableProvider()) {
		return nil, nil
	}
	allowed := map[string]bool{}
	for _, client := range a.indexers.Clients() {
		ix := client.Config()
		if !ix.Enabled {
			continue
		}
		if quickIndexerSelected(ix, source, seedstremID) {
			allowed[ix.ID] = true
		}
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	if source == "addons-rescue" {
		return a.sources.SearchRescue(core.WithQuickSource(ctx, "addons", allowed), req)
	}
	results, err := a.sources.Search(core.WithQuickSource(ctx, source, allowed), req)
	if source == "seedstrem" {
		filtered := results[:0]
		for _, r := range results {
			if r.Protocol == core.ProtocolHTTP && stremio.IsSeedstremStream(r.StreamURL) {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	return results, err
}

func quickIndexerSelected(ix config.Indexer, source, seedstremID string) bool {
	if !ix.Enabled {
		return false
	}
	switch source {
	case "alko":
		return ix.ID == sfrSearchID
	case "usenet":
		return !ix.IsStremio() && !ix.IsAddon() && !ix.IsTorrent()
	case "torrent":
		return !ix.IsStremio() && !ix.IsAddon() && ix.IsTorrent()
	case "seedstrem":
		return ix.IsStremio() && ix.ID == seedstremID
	case "addons", "addons-rescue":
		return ix.IsStremio() && ix.ID != seedstremID
	}
	return false
}
