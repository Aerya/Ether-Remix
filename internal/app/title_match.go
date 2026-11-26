package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/vault"
	"github.com/braedonsaunders/ether/internal/watchlist"
)

var _ frontend.TitleMatcher = (*App)(nil)

func (a *App) SearchTitleMatches(ctx context.Context, typ core.MediaType, query string, year int, refs []core.ProviderRef) ([]frontend.TitleMatchCandidate, error) {
	if a.metadataSources == nil {
		return nil, core.ErrNotAvailable
	}
	search := core.MetadataSearch{Type: typ, Title: query, Year: year}
	out := []frontend.TitleMatchCandidate{}
	var searchErr error
	succeeded := false
	for _, source := range a.metadataSources.Configured() {
		if !supportsMetadataType(source, typ) {
			continue
		}
		var matches []frontend.TitleMatchCandidate
		var err error
		if len(refs) > 0 {
			if !slices.ContainsFunc(refs, func(ref core.ProviderRef) bool {
				return ref.Type == typ && (ref.Source == source.ID() || ref.Source == "imdb")
			}) {
				continue
			}
			matches, err = a.lookupMetadataMatches(ctx, source, typ, refs)
		} else if source.Capabilities().Search {
			matches, err = a.searchMetadataMatches(ctx, source, search)
		} else {
			continue
		}
		if err != nil {
			searchErr = errors.Join(searchErr, err)
			continue
		}
		succeeded = true
		out = append(out, matches...)
	}
	if !succeeded && searchErr != nil {
		return nil, searchErr
	}
	return out, nil
}

// SearchMetadataMatches searches the source selected in the owner console.
func (a *App) SearchMetadataMatches(ctx context.Context, id core.MetadataSourceID, query core.MetadataSearch) ([]frontend.TitleMatchCandidate, error) {
	source, err := a.matchSource(id, query.Type)
	if err != nil {
		return nil, err
	}
	if !source.Capabilities().Search {
		return nil, core.ErrUnsupported
	}
	return a.searchMetadataMatches(ctx, source, query)
}

// Match results attach stored keys without materializing new titles.
func (a *App) searchMetadataMatches(ctx context.Context, source core.MetadataSource, query core.MetadataSearch) ([]frontend.TitleMatchCandidate, error) {
	query.Title = strings.TrimSpace(query.Title)
	if query.Title == "" || len(query.Title) > 300 {
		return nil, nil
	}
	query.Language = a.cfg.Read().Catalog.Language
	hits, err := source.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]frontend.TitleMatchCandidate, 0, len(hits))
	for _, hit := range hits {
		if hit.Adult || hit.Ref.Source != source.ID() || hit.Ref.Type != query.Type || hit.Ref.ID == "" {
			continue
		}
		candidate, err := a.metadataMatchCandidate(ctx, hit.Ref, core.Title{
			Type: query.Type, IDs: hit.IDs, Title: hit.Title, Year: hit.Year, Summary: hit.Summary, PosterPath: hit.PosterPath,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}

// IMDb identifies an item across providers. Each candidate still has an
// actionable provider reference in the accepting source's namespace.
func (a *App) lookupMetadataMatches(ctx context.Context, source core.MetadataSource, typ core.MediaType, refs []core.ProviderRef) ([]frontend.TitleMatchCandidate, error) {
	var sourceRefs []core.ProviderRef
	for _, ref := range refs {
		if ref.Type != typ || ref.ID == "" {
			continue
		}
		switch ref.Source {
		case source.ID():
			sourceRefs = append(sourceRefs, ref)
		case "imdb":
			found, err := source.FindByExternal(ctx, core.IDs{IMDB: ref.ID}, typ)
			if errors.Is(err, core.ErrUnsupported) || errors.Is(err, core.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			sourceRefs = append(sourceRefs, found...)
		}
	}
	out := []frontend.TitleMatchCandidate{}
	seen := make(map[core.ProviderRef]bool, len(sourceRefs))
	for _, ref := range sourceRefs {
		if ref.Source != source.ID() || ref.Type != typ || ref.ID == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		title, err := source.Lookup(ctx, ref)
		if errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrUnsupported) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if title == nil || title.Adult || title.Type != typ {
			continue
		}
		candidate, err := a.metadataMatchCandidate(ctx, ref, *title)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}

func (a *App) metadataMatchCandidate(ctx context.Context, ref core.ProviderRef, title core.Title) (frontend.TitleMatchCandidate, error) {
	keys, err := a.catalog.KeysByProviderID(ctx, string(ref.Source), ref.ID, []core.MediaType{ref.Type})
	if err != nil {
		return frontend.TitleMatchCandidate{}, err
	}
	title.Key = 0
	if len(keys) > 0 {
		title.Key = keys[0]
		if stored, err := a.catalog.StoredTitle(ctx, title.Key); err == nil {
			title.Key = stored.Key
			if title.IDs.TMDB == 0 {
				title.IDs.TMDB = stored.IDs.TMDB
			}
			if title.IDs.TVDB == 0 {
				title.IDs.TVDB = stored.IDs.TVDB
			}
			if title.IDs.IMDB == "" {
				title.IDs.IMDB = stored.IDs.IMDB
			}
		}
	}
	return frontend.TitleMatchCandidate{Title: title, Ref: ref}, nil
}

func (a *App) matchSource(id core.MetadataSourceID, typ core.MediaType) (core.MetadataSource, error) {
	if a.metadataSources == nil {
		return nil, core.ErrNotAvailable
	}
	source := a.metadataSources.Get(id)
	if source == nil || !supportsMetadataType(source, typ) {
		return nil, core.ErrUnsupported
	}
	if !source.Configured() {
		return nil, core.ErrNotAvailable
	}
	return source, nil
}

func (a *App) RefreshCatalogTitle(ctx context.Context, key core.RatingKey) error {
	if a.catalog == nil {
		return core.ErrNotAvailable
	}
	if _, err := a.catalog.StoredTitle(ctx, key); err != nil {
		return err
	}
	return a.appTitleRefresher().run(ctx, key)
}

func (a *App) ApplyTitleMatch(ctx context.Context, from core.RatingKey, ref core.ProviderRef) (core.RatingKey, error) {
	if a.catalog == nil {
		return 0, core.ErrNotAvailable
	}
	if (from.Kind() != core.KindMovie && from.Kind() != core.KindShow) || from.IsPluginKey() {
		return 0, core.ErrNotFound
	}
	if _, err := a.matchSource(ref.Source, ref.Type); err != nil {
		return 0, err
	}
	old, err := a.catalog.StoredTitle(ctx, from)
	if err != nil {
		return 0, err
	}
	if old.Type != ref.Type {
		return 0, core.ErrUnsupported
	}
	from = old.Key
	next, err := a.catalog.MaterializeSourceTitle(ctx, ref)
	if err != nil {
		return 0, err
	}
	if next == nil || next.Key == 0 || next.Type != old.Type || next.Adult {
		return 0, core.ErrNotFound
	}
	if from == next.Key {
		return next.Key, nil
	}
	if old.Type == core.TypeShow {
		layout, _, err := a.catalog.CachedEpisodeLayout(ctx, from)
		if err != nil {
			return 0, err
		}
		if len(layout.Seasons) > 0 {
			if _, err := a.catalog.HydrateEpisodeLayout(ctx, next.Key); err != nil {
				return 0, err
			}
		}
	}
	ranges, err := a.catalog.EpisodeMatchRanges(ctx, from, next.Key)
	if err != nil {
		return 0, err
	}
	if err := a.moveTitleBindings(ctx, old, next, ranges); err != nil {
		return 0, err
	}
	return next.Key, nil
}

func (a *App) moveTitleBindings(ctx context.Context, old, next *core.Title, ranges []core.TitleKeyRange) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	dialect := store.DialectOf(a.db)
	for _, key := range []core.RatingKey{min(old.Key, next.Key), max(old.Key, next.Key)} {
		if err := store.LockKey(ctx, dialect, tx, "title-match:"+key.String()); err != nil {
			return err
		}
	}

	afterCommit, err := a.rekeyTitleBindings(ctx, tx, old, next, ranges)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	afterCommit()
	return nil
}

// rekeyTitleBindings is shared by explicit rematches and catalogue alias
// reconciliation. The caller owns the transaction and post-commit callback.
func (a *App) rekeyTitleBindings(ctx context.Context, tx *sql.Tx, old, next *core.Title, ranges []core.TitleKeyRange) (func(), error) {
	changedCollections, err := collections.RekeyTitle(ctx, tx, old.Key, next.Key, ranges)
	if err != nil {
		return nil, err
	}
	if err := state.RekeyTitle(ctx, tx, ranges); err != nil {
		return nil, err
	}
	if err := favorites.RekeyTitle(ctx, tx, ranges); err != nil {
		return nil, err
	}
	if err := a.availability.RekeyTitle(ctx, tx, ranges, next); err != nil {
		return nil, err
	}
	var changedPins []core.RatingKey
	if a.resolver != nil {
		changedPins, err = a.resolver.RekeyTitle(ctx, tx, ranges, next.Title)
		if err != nil {
			return nil, err
		}
	}
	if err := playback.RekeyTitle(ctx, tx, ranges); err != nil {
		return nil, err
	}
	if err := watchlist.RekeyTitle(ctx, tx, old, next, ranges); err != nil {
		return nil, err
	}
	if err := vault.RekeyTitle(ctx, tx, old, next); err != nil {
		return nil, err
	}
	if err := catalog.RekeyTitle(ctx, tx, ranges, next.Key); err != nil {
		return nil, err
	}
	return func() {
		if a.catalog != nil {
			a.catalog.TitleBindingsChanged(old.Key, next.Key)
		}
		if a.availability != nil {
			a.availability.InvalidateTitleMatches()
		}
		if a.resolver != nil {
			a.resolver.InvalidateDetailVariants()
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			a.resolver.TitlePinsRebound(ctx, changedPins)
		}
		if a.collections != nil {
			a.collections.TitleMatchesChanged(changedCollections)
		}
	}, nil
}
