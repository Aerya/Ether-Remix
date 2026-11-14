package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
	"github.com/braedonsaunders/ether/internal/pse/themes"
	"time"
)

func (a *App) startSeerrCatalog() {
	rt := pseRuntimeOf(a)
	s := seerrcatalog.New(a.db, egress.Client(egress.CapMetadata, 15*time.Second))
	s.Submaker = a.pseSubmaker()
	s.Ensure = a.ensureSeerrCatalogMetadata
	s.Changed = func() {
		a.catalog.RefreshExternalEvidence()
		a.pseSeerrChanged() // PSE : collections SeerrCatalog et bibliothèque VOSTFR
	}
	s.Enrich = func(ctx context.Context, req core.SearchRequest) core.SearchRequest {
		key := core.MovieKey(req.IDs.TMDB)
		if req.Type == core.TypeEpisode {
			key = core.ShowKey(req.IDs.TMDB)
		}
		if title, err := a.catalog.Get(ctx, key); err == nil && title != nil {
			req.AlternativeTitles = append(append([]string{}, title.AlternativeTitles...), req.Title)
			req.Title, req.OriginalTitle = title.Title, title.OriginalTitle
			req.IDs.TVDB = title.IDs.TVDB
			if title.Year > 0 {
				req.Year = title.Year
			}
		}
		return req
	}
	rt.seerr = s
	if a.indexers != nil {
		a.indexers.SetRetainedSearch(s.Lookup)
		s.Search = a.indexers.Search
	}
	s.Start()
	a.closers = append(a.closers, s.Close)
}

func (a *App) ensureSeerrCatalogMetadata(ctx context.Context, key core.RatingKey) error {
	_, err := a.catalog.Get(ctx, key)
	// Get materializes metadata but also enforces catalogue visibility. A hidden
	// title must not reject the source snapshot; visibility remains enforced by
	// the normal catalogue and playback paths. Require an actual stored record.
	if errors.Is(err, core.ErrNotFound) {
		_, err = a.catalog.StoredTitle(ctx, key)
	}
	return err
}

// pseSeerrSignature résume les titres SeerrCatalog (VF puis secours).
func pseSeerrSignature(vf, fallback []core.RatingKey) string {
	h := sha256.New()
	for _, k := range vf {
		fmt.Fprintf(h, "v%d,", k)
	}
	for _, k := range fallback {
		fmt.Fprintf(h, "f%d,", k)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pseSeerrChanged relance le rangement des titres SeerrCatalog quand la
// liste a changé depuis le dernier passage.
func (a *App) pseSeerrChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	vf, fallback, err := seerrcatalog.Keys(ctx, a.db)
	if err != nil {
		return
	}
	if prev, _ := pseRuntimeOf(a).seerrSig.Load().(string); prev == pseSeerrSignature(vf, fallback) {
		return
	}
	a.pseLangExemptChanged()
}

// syncPSESeerrCollections range les titres SeerrCatalog : collection
// « SeerrCatalog » (tous) et « SeerrCatalog · VOSTFR » (versions de
// secours, dans la bibliothèque VOSTFR). Sans VF, ils n'apparaissent jamais
// dans Films ni Séries.
func (a *App) syncPSESeerrCollections(ctx context.Context, svc *themes.Service) error {
	if a.collections == nil || svc == nil {
		return nil
	}
	ed, ok := any(a.collections).(themes.Editor)
	if !ok {
		return nil
	}
	vf, fallback, err := seerrcatalog.Keys(ctx, a.db)
	if err != nil {
		return err
	}
	sig := pseSeerrSignature(vf, fallback)
	if prev, _ := pseRuntimeOf(a).seerrSig.Load().(string); prev == sig {
		return nil
	}
	if err := svc.SyncSeerr(ctx, ed, vf, fallback); err != nil {
		return err
	}
	pseRuntimeOf(a).seerrSig.Store(sig)
	return nil
}
