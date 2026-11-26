package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
)

func supportsMetadataType(source core.MetadataSource, media core.MediaType) bool {
	caps := source.Capabilities()
	return (media == core.TypeMovie && caps.Movies) || (media == core.TypeShow && caps.Shows)
}

func (a *App) defaultMetadataSource(media core.MediaType) core.MetadataSourceID {
	if a.metadataSources != nil {
		for _, source := range a.metadataSources.Configured() {
			if supportsMetadataType(source, media) {
				return source.ID()
			}
		}
	}
	return ""
}

func (a *App) validLibraryMetadataSource(media core.MediaType, id string) bool {
	if id == "" {
		return true
	}
	if a.metadataSources == nil {
		return false
	}
	source := a.metadataSources.Get(core.MetadataSourceID(id))
	return source != nil && supportsMetadataType(source, media)
}

// metadataLibrary finds the first owner-ordered library containing the title.
func (a *App) metadataLibrary(ctx context.Context, title *core.Title) (core.Library, error) {
	libs, err := a.metadataLibraries(ctx, []core.Title{*title})
	return libs[title.Key], err
}

func (a *App) metadataLibraries(ctx context.Context, titles []core.Title) (map[core.RatingKey]core.Library, error) {
	found := make(map[core.RatingKey]core.Library, len(titles))
	if a.libraries == nil || len(titles) == 0 {
		return found, nil
	}
	libs, err := a.libraries.List(ctx)
	if err != nil {
		return nil, err
	}
	// Library order comes from List. Remove owners from later probes once
	// a higher priority library claims them.
	unassigned := map[core.MediaType][]core.RatingKey{}
	for _, title := range titles {
		unassigned[title.Type] = append(unassigned[title.Type], title.Key)
	}
	for _, lib := range libs {
		keys := unassigned[lib.MediaType]
		if lib.Source == core.LibraryPlugin || len(keys) == 0 {
			continue
		}
		members, err := a.catalog.TitlesInFilter(ctx, libraryScopeFilter(lib, core.Filter{}), keys)
		if err != nil {
			return nil, err
		}
		remaining := keys[:0]
		for _, key := range keys {
			if members[key] {
				found[key] = lib
			} else {
				remaining = append(remaining, key)
			}
		}
		unassigned[lib.MediaType] = remaining
	}
	return found, nil
}

func (a *App) metadataSourceForLibrary(lib core.Library) core.MetadataSourceID {
	if lib.MetadataSource != "" {
		return core.MetadataSourceID(lib.MetadataSource)
	}
	return a.defaultMetadataSource(lib.MediaType)
}

func (a *App) libraryMetadataSource(ctx context.Context, title *core.Title) (core.MetadataSourceID, error) {
	lib, err := a.metadataLibrary(ctx, title)
	if err != nil || lib.ID == 0 {
		return "", err
	}
	return a.metadataSourceForLibrary(lib), nil
}

func (a *App) libraryEpisodeOrder(ctx context.Context, show *core.Title) (catalog.EffectiveEpisodeOrder, error) {
	lib, err := a.metadataLibrary(ctx, show)
	if err != nil || lib.ID == 0 {
		return catalog.EffectiveEpisodeOrder{}, err
	}
	return a.episodeOrderForLibrary(lib), nil
}

func (a *App) libraryEpisodeOrders(ctx context.Context, titles []core.Title) (map[core.RatingKey]catalog.EffectiveEpisodeOrder, error) {
	libs, err := a.metadataLibraries(ctx, titles)
	if err != nil {
		return nil, err
	}
	out := make(map[core.RatingKey]catalog.EffectiveEpisodeOrder, len(libs))
	for key, lib := range libs {
		out[key] = a.episodeOrderForLibrary(lib)
	}
	return out, nil
}

func (a *App) episodeOrderForLibrary(lib core.Library) catalog.EffectiveEpisodeOrder {
	out := catalog.EffectiveEpisodeOrder{LibraryID: lib.ID, LibraryName: lib.Name}
	if source := a.metadataSourceForLibrary(lib); source != "" {
		out.LibraryDefault = core.EpisodeOrder{Source: source, Kind: core.OrderAired}
	}
	return out
}

// StoredTitleEpisodeOrder adds provenance without hydrating the show, loading
// episode group options or comparing coverage.
func (a *App) StoredTitleEpisodeOrder(ctx context.Context, key core.RatingKey) (admin.EpisodeOrderProvenance, error) {
	effective, err := a.catalog.EffectiveEpisodeOrder(ctx, key)
	if err != nil {
		return admin.EpisodeOrderProvenance{}, err
	}
	out := admin.EpisodeOrderProvenance{
		Effective: effective.Order, LibraryDefault: effective.LibraryDefault, LibraryName: effective.LibraryName,
	}
	if effective.Override != (core.EpisodeOrder{}) {
		out.Override = &effective.Override
	}
	return out, nil
}

// TitleEpisodeOrder describes the order for the owner detail drawer. Only this
// entry point compares cached provider coverage.
func (a *App) TitleEpisodeOrder(ctx context.Context, show *core.Title) (*admin.TitleEpisodeOrder, error) {
	provenance, err := a.StoredTitleEpisodeOrder(ctx, show.Key)
	if err != nil {
		return nil, err
	}
	out := &admin.TitleEpisodeOrder{EpisodeOrderProvenance: provenance, Options: a.episodeOrderOptions(ctx, show)}
	if gaps, err := a.catalog.EpisodeGap(ctx, show.Key); err == nil {
		for _, gap := range gaps {
			if gap.Order.Source != provenance.Effective.Source && gap.Difference > 0 && (out.Gap == nil || gap.Difference > out.Gap.Count) {
				out.Gap = &admin.EpisodeOrderGap{Order: gap.Order, Count: gap.Difference}
			}
		}
	}
	return out, nil
}

func (a *App) episodeOrderOptions(ctx context.Context, show *core.Title) []admin.EpisodeOrderOption {
	out := []admin.EpisodeOrderOption{}
	for _, source := range a.metadataSources.Configured() {
		caps := source.Capabilities()
		if !caps.Shows {
			continue
		}
		for _, kind := range caps.EpisodeOrders {
			if kind == "" || kind == "group:*" {
				continue
			}
			if source.ID() == core.MetadataTVDB && kind != core.OrderAired && kind != core.OrderDVD && kind != core.OrderAbsolute {
				continue
			}
			out = append(out, admin.EpisodeOrderOption{EpisodeOrder: core.EpisodeOrder{Source: source.ID(), Kind: kind}})
		}
		if source.ID() == core.MetadataTMDB && a.tmdb != nil && show.IDs.TMDB > 0 {
			groups, err := a.tmdb.EpisodeGroups(ctx, show.IDs.TMDB)
			if err != nil {
				continue
			}
			for _, group := range groups {
				if group.ID != "" {
					out = append(out, admin.EpisodeOrderOption{
						EpisodeOrder: core.EpisodeOrder{Source: source.ID(), Kind: "group:" + group.ID},
						Label:        group.Name,
					})
				}
			}
		}
	}
	return out
}

func (a *App) SetTitleEpisodeOrder(ctx context.Context, key core.RatingKey, order core.EpisodeOrder) error {
	show, err := a.catalog.StoredTitle(ctx, key)
	if err != nil {
		return err
	}
	if show.Type != core.TypeShow || show.Adult {
		return core.ErrNotFound
	}
	return a.catalog.SetEpisodeOrder(ctx, key, order)
}
