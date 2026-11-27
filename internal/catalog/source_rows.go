package catalog

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
)

// sourceSearches reports the non-TMDB sources that can answer this query.
// TMDB keeps its existing multi-page, language-aware search path.
func (c *Catalog) sourceSearches(wantMovie, wantTV bool) []core.MetadataSource {
	var sources []core.MetadataSource
	for _, source := range c.MetadataSources().Configured() {
		caps := source.Capabilities()
		if source.ID() != core.MetadataTMDB && caps.Search && (wantMovie && caps.Movies || wantTV && caps.Shows) {
			sources = append(sources, source)
		}
	}
	return sources
}

func (c *Catalog) searchSourceRows(ctx context.Context, sources []core.MetadataSource, q core.MetadataSearch, wantMovie, wantTV bool, limit int) []core.Title {
	var items []core.Title
	for _, source := range sources {
		caps := source.Capabilities()
		for _, typ := range []core.MediaType{core.TypeMovie, core.TypeShow} {
			if typ == core.TypeMovie && !(wantMovie && caps.Movies) || typ == core.TypeShow && !(wantTV && caps.Shows) {
				continue
			}
			q.Type = typ
			hits, err := source.Search(ctx, q)
			if err == nil {
				items = append(items, c.sourceRows(ctx, source.ID(), typ, hits, limit)...)
			}
		}
	}
	return items
}

func (c *Catalog) sourceRows(ctx context.Context, source core.MetadataSourceID, typ core.MediaType, hits []core.MetadataHit, limit int) []core.Title {
	items := make([]core.Title, 0, min(len(hits), limit))
	for _, hit := range hits[:min(len(hits), limit)] {
		if hit.Ref.Source != source || hit.Ref.Type != typ || hit.Ref.ID == "" || hit.Adult {
			continue
		}
		t, err := c.SourceSummary(ctx, hit)
		if err == nil && t.Key != 0 {
			items = append(items, t)
		}
	}
	return items
}

func metadataListKind(name string) core.ListKind {
	switch name {
	case "trending_day", "trending_week":
		return core.ListTrending
	case "popular":
		return core.ListPopular
	case "now_playing":
		return core.ListNew
	}
	return ""
}

func (c *Catalog) metadataListSources(name string, typ core.MediaType) []core.MetadataSource {
	if metadataListKind(name) == "" {
		return nil
	}
	var sources []core.MetadataSource
	for _, source := range c.MetadataSources().Configured() {
		caps := source.Capabilities()
		if source.ID() != core.MetadataTMDB && caps.Lists &&
			((typ == "" || typ == core.TypeMovie) && caps.Movies || (typ == "" || typ == core.TypeShow) && caps.Shows) {
			sources = append(sources, source)
		}
	}
	return sources
}

// SupportsMetadataList reports whether a configured source can serve a chart
// for the requested media type. It never fetches a list.
func (c *Catalog) SupportsMetadataList(name string, typ core.MediaType) bool {
	return c.TMDBConfigured() || len(c.metadataListSources(name, typ)) > 0
}

func (c *Catalog) sourceListRows(ctx context.Context, sources []core.MetadataSource, name string, typ core.MediaType, limit int) ([]core.Title, error) {
	kind := metadataListKind(name)
	var items []core.Title
	seen := map[core.RatingKey]bool{}
	for _, source := range sources {
		caps := source.Capabilities()
		for _, media := range []core.MediaType{core.TypeMovie, core.TypeShow} {
			if (typ != "" && typ != media) || (media == core.TypeMovie && !caps.Movies) || (media == core.TypeShow && !caps.Shows) {
				continue
			}
			for page := 1; page <= 5 && len(items) < limit; page++ {
				hits, err := source.Lists(ctx, kind, media, page)
				if err != nil || len(hits) == 0 {
					break
				}
				start := len(items)
				for _, title := range c.sourceRows(ctx, source.ID(), media, hits, limit) {
					if !seen[title.Key] {
						seen[title.Key] = true
						items = append(items, title)
					}
				}
				if len(items) == start {
					break
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Catalog) findSourceExternal(ctx context.Context, ids core.IDs, typ core.MediaType, allowUnfiltered bool) (*core.Title, error) {
	if typ == core.TypeEpisode {
		return nil, core.ErrNotFound
	}
	kinds := []core.MediaType{typ}
	if typ == "" {
		kinds = []core.MediaType{core.TypeMovie, core.TypeShow}
		if ids.TVDB > 0 {
			slices.Reverse(kinds)
		}
	}
	for _, kind := range kinds {
		for _, source := range c.MetadataSources().Configured() {
			caps := source.Capabilities()
			if source.ID() == core.MetadataTMDB || kind == core.TypeMovie && !caps.Movies || kind == core.TypeShow && !caps.Shows {
				continue
			}
			refs, err := source.FindByExternal(ctx, ids, kind)
			if err != nil || len(refs) != 1 || refs[0].Source != source.ID() || refs[0].Type != kind || refs[0].ID == "" {
				continue
			}
			t, err := c.MaterializeSourceTitle(ctx, refs[0])
			if err == nil && t != nil {
				return c.getOrStored(ctx, t.Key, allowUnfiltered)
			}
			if err != nil && !errors.Is(err, core.ErrNotFound) && !errors.Is(err, core.ErrUnsupported) {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, core.ErrNotFound
}

func externalMetadataIDs(source, id string) core.IDs {
	if source == "tvdb" || source == "tvdb_id" {
		n, _ := strconv.ParseInt(id, 10, 64)
		return core.IDs{TVDB: n}
	}
	return core.IDs{IMDB: id}
}
