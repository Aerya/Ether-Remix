package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/frenchcat"
	"github.com/braedonsaunders/ether/internal/pse/quality"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Reference selection is independent of the source's link lifetime. An unknown
// language cannot satisfy a required language. Keep raw search diagnostics in
// pse_releases, but never retain a reusable locator outside the owner's rules.
func (a *App) acceptReference(ctx context.Context, key core.RatingKey, r core.Release) (bool, error) {
	typ := core.TypeMovie
	if key.Kind() == core.KindEpisode || key.Kind() == core.KindShow {
		key = core.ShowKey(key.TMDB())
		typ = core.TypeShow
	}
	parsed := releaseparse.Parse(r.Name)
	if len(r.Info.Languages) == 0 {
		r.Info.Languages = parsed.Languages
	}
	if len(r.Info.SubtitleLanguages) == 0 {
		r.Info.SubtitleLanguages = parsed.SubtitleLanguages
	}
	if r.Info.Height == 0 {
		r.Info.Height = parsed.Height
	}
	if r.Info.VideoCodec == "" {
		r.Info.VideoCodec = parsed.VideoCodec
	}
	normalized := releasedb.FromCore(core.Title{Key: key, Type: typ}, 0, 0, r.IndexerID, r, r.DebridCached)
	r.Info.Languages = normalized.Languages
	if quality.Active(a.db) {
		var original string
		if a.catalog != nil {
			labels, err := a.catalog.ReleaseLabels(ctx, []core.RatingKey{key})
			if err != nil {
				return false, err
			}
			original = labels[key].Language
		}
		return quality.Allow(a.db, key, typ, r, original), nil
	}
	var exempt bool
	err := a.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pse_lang_exempt WHERE title_key=?)`, int64(key)).Scan(&exempt)
	if err != nil {
		return false, err
	}
	p := releasedb.Playable()
	if p.Enabled {
		filter := p.Filter
		if exempt {
			filter.Languages = nil
		}
		if !filter.Match(normalized) {
			return false, nil
		}
	}
	f := frenchcat.Current()
	if f.Enabled && f.Types[typ] && !exempt && len(f.Releases) > 0 {
		if !(releasedb.Filter{Languages: f.Releases}).Match(normalized) {
			return false, nil
		}
	}
	return true, nil
}
