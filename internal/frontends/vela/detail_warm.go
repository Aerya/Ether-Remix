package vela

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

const negotiatedClientTTL = 24 * time.Hour

// Negotiation precedes sign-in. Bind its untrusted capability declaration to
// the device and observed peer, then apply the authenticated viewer's rules
// only when detail is opened. No source work runs at negotiation or at idle.
type negotiatedClient struct {
	client client
	at     time.Time
}

func (i *instance) negotiationKey(r *http.Request, deviceID string) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	if ip, ok := frontend.ClientIP(i.identityContext(r)); ok {
		peer = ip.String()
	}
	return hashJSON([]string{deviceID, peer, r.UserAgent()})
}

func (i *instance) rememberNegotiatedClient(r *http.Request, c client) {
	if c.Device.ID == "" || len(c.Device.ID) > 1024 || (requestDevice(r).ID != "" && requestDevice(r).ID != c.Device.ID) {
		return
	}
	encoded, err := json.Marshal(c)
	if err != nil || len(encoded) > 16<<10 {
		return
	}
	now := time.Now()
	key := i.negotiationKey(r, c.Device.ID)
	i.warmMu.Lock()
	defer i.warmMu.Unlock()
	if i.warmClients == nil {
		i.warmClients = make(map[string]negotiatedClient)
	}
	if _, exists := i.warmClients[key]; !exists && len(i.warmClients) >= 256 {
		var oldest string
		var at time.Time
		for k, entry := range i.warmClients {
			if at.IsZero() || entry.at.Before(at) {
				oldest, at = k, entry.at
			}
		}
		delete(i.warmClients, oldest)
	}
	i.warmClients[key] = negotiatedClient{client: c, at: now}
}

// Admission is bounded before launching any work. The shared broker rechecks
// access; the playback manager applies the same speculation policy, queue and
// resource limits as Plex. Never wait for capability/metadata I/O in detail.
func (i *instance) warmDetail(r *http.Request, v *frontend.Viewer, d frontend.ItemDetail) {
	warmer, ok := i.host.Sessions.(frontend.DetailWarmer)
	if !ok || i.host.Capabilities == nil || i.ctx == nil || i.ctx.Err() != nil || v == nil || (d.CanPlay != nil && !*d.CanPlay) || (d.Title.Key.Kind() != frontend.KindMovie && d.Title.Key.Kind() != frontend.KindEpisode) {
		return
	}
	dev := requestDevice(r)
	if dev.ID == "" {
		return
	}
	key := i.negotiationKey(r, dev.ID)
	pending := hashJSON([]any{v.ID, dev.ID})
	i.warmMu.Lock()
	if len(i.warmPending) >= 4 || i.warmPending[pending] {
		i.warmMu.Unlock()
		return
	}
	entry, declared := i.warmClients[key]
	declared = declared && time.Since(entry.at) < negotiatedClientTTL
	if i.warmPending == nil {
		i.warmPending = make(map[string]bool)
	}
	i.warmPending[pending] = true
	i.warmMu.Unlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(i.identityContext(r)), 5*time.Second)
	stop := context.AfterFunc(i.ctx, cancel)
	r = r.Clone(ctx)
	viewer := *v
	go func() {
		defer func() {
			stop()
			cancel()
			i.warmMu.Lock()
			delete(i.warmPending, pending)
			i.warmMu.Unlock()
		}()
		c := entry.client
		if !declared {
			c = client{Device: device{ID: dev.ID, Name: dev.DeviceName, Platform: dev.Platform, AppVersion: dev.Version}}
		}
		capability, err := i.clientCapabilities(r, &viewer, c)
		if err != nil || ctx.Err() != nil {
			return
		}
		if warmer.WarmDetail(ctx, frontend.PlanRequest{Protocol: "vela", Viewer: &viewer, Device: dev, Item: d.Title.Key, Caps: capability.Client, Intent: frontend.PlayIntent{DeclaredCaps: declared}}) {
			slog.Debug("vela detail prewarm scheduled", "key", d.Title.Key, "viewer", viewer.ID, "device", dev.ID)
		}
	}()
}
