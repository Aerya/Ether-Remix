package app

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/veladist"
)

// velaManager is this node's one Vela web client installer, shared by the
// console's Apps page and the automatic first install.
func (a *App) velaManager() *veladist.Manager {
	a.velaOnce.Do(func() {
		a.velaDist = veladist.New(a.frontendClientsDir(), a.opts.Version, func() string { return a.cfg.Get().Vela.ChannelURL })
	})
	return a.velaDist
}

// ensureVelaInstalled installs the signed stable Vela web client once, so
// Watch opens Vela on a new install without anyone visiting Apps. Updates
// and rollbacks stay the owner's choice on the Apps page. An owner who
// turned Vela off gets nothing installed.
func (a *App) ensureVelaInstalled(ctx context.Context) {
	wait := time.Minute // let startup settle before a ~100 MB download
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		m := a.velaManager()
		if st := m.Status(); st.Installed || st.Busy {
			return
		}
		if !frontendDesired(a.cfg.Get(), "vela") {
			wait = time.Hour
			continue
		}
		err := m.Check(ctx)
		if err == nil {
			err = m.Install(ctx)
		}
		if err == nil {
			a.log.Info("vela: installed the web client", "version", m.Status().InstalledVersion)
			return
		}
		if ctx.Err() != nil {
			return
		}
		a.log.Warn("vela: web client install failed; retrying later", "err", err, "retry", wait*5)
		wait = min(wait*5, 24*time.Hour)
	}
}
