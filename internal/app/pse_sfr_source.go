package app

import (
	"context"
	"errors"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

const sfrSearchID = "remix-sfr-search"

// A runtime backend shares the existing indexer cache, concurrency, breaker,
// ranking and debrid validation instead of adding a second playback pipeline.
func sfrIndexers(rows []config.Indexer) []config.Indexer {
	cfg := releasedb.LoadSFRSettings()
	if !cfg.Enabled {
		return rows
	}
	out := append([]config.Indexer(nil), rows...)
	return append(out, config.Indexer{ID: sfrSearchID, Name: "StreamFusion Reborn (base)", Kind: config.IndexerKindAddon, Protocol: "torrent", Enabled: true, Priority: 1, URL: "https://remix.invalid/" + releasedb.SFRIdentity(cfg)})
}

type sfrBackend struct{}

func (sfrBackend) Search(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	if err := reserve(ctx, "hit"); err != nil {
		return nil, err
	}
	return releasedb.SearchSFR(ctx, releasedb.LoadSFRSettings(), req)
}
func (sfrBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, errors.New("StreamFusion exposes torrent candidates only")
}
func (sfrBackend) ResolveTorrent(_ context.Context, r core.Release, _ func(context.Context, string) error) (core.Release, error) {
	return r, nil
}
