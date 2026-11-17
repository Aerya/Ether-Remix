package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// Audit old associations once, before restarting the indexer. The rest of the
// server remains available; browsing and retained answers wait for completion.
func (a *App) startAssociationAudit() {
	rt := pseRuntimeOf(a)
	var version int
	if a.db.QueryRow(`SELECT version FROM pse_association_audit WHERE version=1`).Scan(&version) == nil {
		rt.restartIndexer(a)
		return
	}
	rt.associationPending.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.closers = append(a.closers, func() { cancel(); <-done; rt.stopIndexer() })
	go func() {
		defer close(done)
		a.log.Info("release association audit started")
		lib := a.releaseLibrary()
		report, err := releasedb.AuditAssociations(ctx, a.db, lib.Open, true)
		if err != nil {
			rt.associationFailed.Store(true)
			a.log.Error("release association audit failed; retained library remains unavailable", "error", err)
			return
		}
		a.log.Info("release association audit completed", "checked", report.Checked, "quarantined", report.Quarantined)
		rt.associationPending.Store(false)
		if ctx.Err() == nil {
			rt.restartIndexer(a)
		}
	}()
}
