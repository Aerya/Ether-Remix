package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func init() {
	store.Register("catalog", 81, `
CREATE TABLE catalog_episode_order (
 show_key INTEGER PRIMARY KEY,
 provider TEXT NOT NULL,
 order_kind TEXT NOT NULL
);
CREATE TABLE catalog_order_manifest (
 show_key INTEGER NOT NULL,
 provider TEXT NOT NULL,
 order_kind TEXT NOT NULL,
 fetched_at INTEGER NOT NULL,
 PRIMARY KEY(show_key,provider,order_kind)
);
CREATE TABLE catalog_order_seasons (
 show_key INTEGER NOT NULL,
 provider TEXT NOT NULL,
 order_kind TEXT NOT NULL,
 season INTEGER NOT NULL,
 season_key INTEGER NOT NULL,
 title TEXT NOT NULL,
 summary TEXT NOT NULL,
 poster TEXT NOT NULL,
 air_date TEXT NOT NULL,
 episode_count INTEGER NOT NULL,
 fetched_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(show_key,provider,order_kind,season)
);
CREATE INDEX catalog_order_seasons_key ON catalog_order_seasons(season_key,provider,order_kind);
CREATE TABLE catalog_order_episodes (
 show_key INTEGER NOT NULL,
 provider TEXT NOT NULL,
 order_kind TEXT NOT NULL,
 season INTEGER NOT NULL,
 episode INTEGER NOT NULL,
 episode_key INTEGER NOT NULL,
 absolute INTEGER NOT NULL,
 air_date TEXT NOT NULL,
 PRIMARY KEY(show_key,provider,order_kind,season,episode),
 UNIQUE(show_key,provider,order_kind,episode_key)
);
CREATE INDEX catalog_order_episodes_key ON catalog_order_episodes(episode_key,provider,order_kind);
CREATE INDEX catalog_order_episodes_date ON catalog_order_episodes(show_key,provider,order_kind,air_date);
`)
}

func init() {
	store.Register("catalog", 83, `
ALTER TABLE catalog_order_episodes ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_order_episodes ADD COLUMN summary TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_order_episodes ADD COLUMN still TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_order_episodes ADD COLUMN runtime_min INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_order_seasons ADD COLUMN provider_id TEXT NOT NULL DEFAULT '';
`)
}

func init() {
	store.Register("catalog", 84, `
CREATE TABLE catalog_episode_provider_air (
 key INTEGER PRIMARY KEY,
 air_ts INTEGER NOT NULL
);`)
}

var tmdbAired = core.EpisodeOrder{Source: core.MetadataTMDB, Kind: core.OrderAired}

type EffectiveEpisodeOrder struct {
	Order          core.EpisodeOrder
	Override       core.EpisodeOrder
	LibraryDefault core.EpisodeOrder
	LibraryID      core.LibraryID
	LibraryName    string
}

type orderDefaultLoader func(context.Context, *core.Title) (EffectiveEpisodeOrder, error)

func (c *Catalog) SetEpisodeOrderDefault(load func(context.Context, *core.Title) (EffectiveEpisodeOrder, error)) {
	if load == nil {
		c.episodeOrderDefault.Store(nil)
		return
	}
	fn := orderDefaultLoader(load)
	c.episodeOrderDefault.Store(&fn)
}

type orderBatchDefaultLoader func(context.Context, []core.Title) (map[core.RatingKey]EffectiveEpisodeOrder, error)

func (c *Catalog) SetEpisodeOrderBatchDefault(load func(context.Context, []core.Title) (map[core.RatingKey]EffectiveEpisodeOrder, error)) {
	if load == nil {
		c.episodeOrderBatchDefault.Store(nil)
		return
	}
	fn := orderBatchDefaultLoader(load)
	c.episodeOrderBatchDefault.Store(&fn)
}

func (c *Catalog) canProjectAlternateOrders() bool {
	if c.hasEpisodeLayouts.Load() || store.DialectOf(c.db) == store.Postgres {
		return true
	}
	for _, source := range c.metadataSources.Configured() {
		if source.ID() != core.MetadataTMDB && source.Capabilities().Shows {
			return true
		}
	}
	return false
}

type metadataGroupLoader func(context.Context, int64, string) (*tmdb.EpisodeGroup, error)

func (c *Catalog) SetMetadataEpisodeGroup(load func(context.Context, int64, string) (*tmdb.EpisodeGroup, error)) {
	if load == nil {
		c.episodeGroupLoader.Store(nil)
		return
	}
	fn := metadataGroupLoader(load)
	c.episodeGroupLoader.Store(&fn)
}

func (c *Catalog) metadataEpisodeGroup(ctx context.Context, id int64, group string) (*tmdb.EpisodeGroup, error) {
	if load := c.episodeGroupLoader.Load(); load != nil {
		return (*load)(ctx, id, group)
	}
	return nil, core.ErrUnsupported
}

func (c *Catalog) EffectiveEpisodeOrder(ctx context.Context, show core.RatingKey) (EffectiveEpisodeOrder, error) {
	if show.Kind() != core.KindShow {
		return EffectiveEpisodeOrder{}, core.ErrNotFound
	}
	t, err := c.storedTitle(ctx, show)
	if err != nil {
		return EffectiveEpisodeOrder{}, err
	}
	return c.effectiveEpisodeOrder(ctx, t)
}

func (c *Catalog) effectiveEpisodeOrder(ctx context.Context, show *core.Title) (EffectiveEpisodeOrder, error) {
	out, err := c.episodeLibraryDefault(ctx, show)
	if err != nil {
		return out, err
	}
	out.Order = out.LibraryDefault
	err = c.db.QueryRowContext(ctx, `SELECT provider,order_kind FROM catalog_episode_order WHERE show_key=?`, show.Key).Scan(&out.Override.Source, &out.Override.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Order = out.Override
	return out, nil
}

func (c *Catalog) episodeLibraryDefault(ctx context.Context, show *core.Title) (EffectiveEpisodeOrder, error) {
	var selected EffectiveEpisodeOrder
	if load := c.episodeOrderDefault.Load(); load != nil && ctx.Value(resolvingEpisodeDefault{}) == nil {
		var err error
		selected, err = (*load)(context.WithValue(ctx, resolvingEpisodeDefault{}, true), show)
		if err != nil {
			return selected, err
		}
	}
	return normalizeLibraryEpisodeOrder(show, selected), nil
}

func normalizeLibraryEpisodeOrder(show *core.Title, selected EffectiveEpisodeOrder) EffectiveEpisodeOrder {
	if selected.LibraryDefault.Source == "" {
		selected.LibraryDefault = selected.Order
	}
	if selected.LibraryDefault.Source == "" {
		selected.LibraryDefault = tmdbAired
	}
	if show.IDs.TMDB == 0 && show.IDs.TVDB > 0 && selected.LibraryDefault == tmdbAired && (show.Key.Allocated() || selected.LibraryID == 0) {
		selected.LibraryDefault.Source = core.MetadataTVDB
	}
	return selected
}

func (c *Catalog) SetEpisodeOrder(ctx context.Context, show core.RatingKey, order core.EpisodeOrder) error {
	if show.Kind() != core.KindShow {
		return core.ErrNotFound
	}
	if order == (core.EpisodeOrder{}) {
		_, err := c.db.ExecContext(ctx, `DELETE FROM catalog_episode_order WHERE show_key=?`, show)
		if err == nil {
			c.writeGen.Add(1)
		}
		return err
	}
	t, err := c.storedTitle(ctx, show)
	if err != nil {
		return err
	}
	source := c.metadataSources.Get(order.Source)
	if source == nil || !source.Configured() || !validSourceOrder(source, order.Kind) {
		return core.ErrUnsupported
	}
	if _, err := c.ensureOrderManifest(ctx, t, order); err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, `INSERT INTO catalog_episode_order(show_key,provider,order_kind) VALUES (?,?,?)
 ON CONFLICT(show_key) DO UPDATE SET provider=excluded.provider,order_kind=excluded.order_kind`, show, order.Source, order.Kind)
	if err == nil {
		c.hasEpisodeLayouts.Store(true)
		c.writeGen.Add(1)
	}
	return err
}

func validSourceOrder(source core.MetadataSource, kind string) bool {
	for _, supported := range source.Capabilities().EpisodeOrders {
		if kind != "" && (supported == kind || supported == "group:*" && len(kind) > len("group:") && len(kind) <= 128 && strings.HasPrefix(kind, "group:")) {
			return true
		}
	}
	return false
}
