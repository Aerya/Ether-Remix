package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
	"time"
)

func (a *App) startSeerrCatalog() {
	rt := pseRuntimeOf(a)
	s := seerrcatalog.New(a.db, egress.Client(egress.CapMetadata, 15*time.Second))
	s.Ensure = func(ctx context.Context, k core.RatingKey) error { _, err := a.catalog.Get(ctx, k); return err }
	s.Changed = a.catalog.RefreshExternalEvidence
	rt.seerr = s
	if a.indexers != nil {
		a.indexers.SetRetainedSearch(s.Lookup)
	}
	s.Start()
	a.closers = append(a.closers, s.Close)
}
