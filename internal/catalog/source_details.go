package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

type metadataDefaultLoader func(context.Context, *core.Title) (core.MetadataSourceID, error)

func (c *Catalog) SetMetadataSourceDefault(load func(context.Context, *core.Title) (core.MetadataSourceID, error)) {
	if load == nil {
		c.metadataSourceDefault.Store(nil)
		return
	}
	fn := metadataDefaultLoader(load)
	c.metadataSourceDefault.Store(&fn)
}

func (c *Catalog) titleDetailsSource(ctx context.Context, t *core.Title) (core.MetadataSourceID, error) {
	if load := c.metadataSourceDefault.Load(); load != nil && ctx.Value(resolvingEpisodeDefault{}) == nil {
		src, err := (*load)(context.WithValue(ctx, resolvingEpisodeDefault{}, true), t)
		if err != nil || src != "" {
			return src, err
		}
	}
	var source core.MetadataSourceID
	err := c.db.QueryRowContext(ctx, `SELECT provider FROM catalog_title_detail_source WHERE key=?`, t.Key).Scan(&source)
	if err == nil {
		return source, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if t.IDs.TMDB > 0 {
		return core.MetadataTMDB, nil
	}
	if t.IDs.TVDB > 0 {
		return core.MetadataTVDB, nil
	}
	return "", nil
}

func (c *Catalog) titleSourceRef(ctx context.Context, title *core.Title, source core.MetadataSource) (core.ProviderRef, error) {
	if title.Type == core.TypeShow {
		return c.providerShow(ctx, title, source)
	}
	ref := core.ProviderRef{Source: source.ID(), Type: title.Type}
	switch source.ID() {
	case core.MetadataTMDB:
		if title.IDs.TMDB > 0 {
			ref.ID = strconv.FormatInt(title.IDs.TMDB, 10)
		}
	case core.MetadataTVDB:
		if title.IDs.TVDB > 0 {
			ref.ID = strconv.FormatInt(title.IDs.TVDB, 10)
		}
	}
	if ref.ID != "" {
		return ref, nil
	}
	err := c.db.QueryRowContext(ctx, `SELECT provider_id FROM catalog_identity WHERE key=? AND provider=? AND kind=? ORDER BY seq LIMIT 1`, title.Key, source.ID(), title.Key.Kind()).Scan(&ref.ID)
	if err == nil {
		return ref, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ref, err
	}
	refs, err := source.FindByExternal(ctx, title.IDs, title.Type)
	if err != nil {
		return ref, err
	}
	if len(refs) != 1 || refs[0].Source != source.ID() || refs[0].Type != title.Type || refs[0].ID == "" {
		return ref, core.ErrNotFound
	}
	return refs[0], nil
}

// sourceTitleDetails serves the cached title for an unavailable provider. The
// explicit refresh path instead reports that it cannot load fresh details.
func (c *Catalog) sourceTitleDetails(ctx context.Context, t *core.Title, source core.MetadataSourceID, force bool) (*core.Title, error) {
	src := c.metadataSources.Get(source)
	if src == nil || !src.Configured() {
		if force {
			return nil, core.ErrUnsupported
		}
		return t, nil
	}
	var stored core.MetadataSourceID
	var fetched int64
	err := c.db.QueryRowContext(ctx, `SELECT provider,fetched_at FROM catalog_title_detail_source WHERE key=?`, t.Key).Scan(&stored, &fetched)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if !force && stored == source && c.orderFresh(fetched) {
		return t, nil
	}
	ref, err := c.titleSourceRef(ctx, t, src)
	if err != nil {
		if !force && (errors.Is(err, core.ErrUnsupported) || errors.Is(err, core.ErrNotFound)) {
			return t, nil
		}
		return nil, err
	}
	fresh, err := src.Lookup(ctx, ref)
	if err != nil {
		if !force {
			return t, nil
		}
		return nil, err
	}
	if fresh == nil || fresh.Adult || fresh.Type != t.Type {
		return nil, core.ErrNotFound
	}
	fresh.Key, fresh.IDs = t.Key, mergeSourceIDs(fresh.IDs, t.IDs)
	if err := c.reconcileTitleIdentity(ctx, fresh); err != nil {
		return nil, err
	}
	if err := c.storeSourceDetails(ctx, fresh, source, true); err != nil {
		return nil, err
	}
	return fresh, nil
}

func (c *Catalog) refreshSourceTree(ctx context.Context, t *core.Title, source core.MetadataSourceID) (*core.Title, error) {
	refreshCtx := core.WithMetadataRefresh(ctx)
	fresh, err := c.sourceTitleDetails(refreshCtx, t, source, true)
	if err != nil || t.Type != core.TypeShow {
		return fresh, err
	}
	order, err := c.effectiveEpisodeOrder(ctx, fresh)
	if err != nil {
		return nil, err
	}
	provider := c.metadataSources.Get(order.Order.Source)
	if provider == nil || !provider.Configured() {
		return fresh, nil
	}
	ref, err := c.providerShow(ctx, fresh, provider)
	if err != nil {
		return nil, err
	}
	rows, err := provider.Seasons(refreshCtx, ref, order.Order)
	if err != nil {
		return nil, err
	}
	if err := c.storeOrderManifest(ctx, fresh, order.Order, rows); err != nil {
		return nil, err
	}
	seasons, _, err := c.orderManifest(ctx, fresh.Key, order.Order)
	if err != nil {
		return nil, err
	}
	if order.Order.Source == core.MetadataTMDB && strings.HasPrefix(order.Order.Kind, "group:") {
		// Seasons refreshed the shared group payload. Reuse it for all its
		// coordinates instead of re-downloading the same group for each.
		refreshCtx = ctx
	}

	for _, season := range seasons {
		items, err := provider.Episodes(refreshCtx, ref, order.Order, season.Season)
		if err != nil {
			return nil, err
		}
		if err := c.storeOrderSeason(ctx, fresh, order.Order, season, items); err != nil {
			return nil, err
		}
	}
	return fresh, nil
}
