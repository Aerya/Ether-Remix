package plugins

import (
	"context"
	"encoding/json"
	"fmt"
)

// Screen pushes (docs/plugin-screens.md): a plugin with document screens may
// tell Ether that its data changed. Pushes reach only screens someone has
// open; with nobody watching, they cost nothing and are not kept.

// ScreenPush receives a plugin's pushes; the app installs it.
type ScreenPush interface {
	Invalidate(pluginID string) int
	PutRecords(ctx context.Context, pluginID string, records json.RawMessage) (int, error)
}

// SetScreenPush installs the receiver of screens.invalidate and
// screens.putRecords.
func (m *Manager) SetScreenPush(p ScreenPush) {
	m.screenPushMu.Lock()
	m.screenPush = p
	m.screenPushMu.Unlock()
}

func (h *host) screenPush(ctx context.Context) (ScreenPush, error) {
	r, g, err := h.record(ctx)
	if err != nil {
		return nil, err
	}
	man := r.Manifest
	man.Ether = man.EffectiveEther()
	if g.revoked(string(CapScreens)) || man.Ether == nil || !man.HasCapability(CapScreens) {
		h.noteDenied(string(CapScreens))
		return nil, fmt.Errorf("%w: %s", ErrPermissionDenied, CapScreens)
	}
	h.m.screenPushMu.RLock()
	p := h.m.screenPush
	h.m.screenPushMu.RUnlock()
	if p == nil {
		return nil, ErrUnavailable
	}
	return p, nil
}

// ScreensInvalidate implements Host.
func (h *host) ScreensInvalidate(ctx context.Context) (int, error) {
	p, err := h.screenPush(ctx)
	if err != nil {
		return 0, err
	}
	return p.Invalidate(h.plugin), nil
}

// ScreensPutRecords implements Host.
func (h *host) ScreensPutRecords(ctx context.Context, records json.RawMessage) (int, error) {
	p, err := h.screenPush(ctx)
	if err != nil {
		return 0, err
	}
	if len(records) > 256<<10 {
		return 0, fmt.Errorf("%w: screens.putRecords is limited to 256 KB", ErrQuotaExceeded)
	}
	return p.PutRecords(ctx, h.plugin, records)
}
