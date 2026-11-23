package app

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/plexguid"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/transcode"
	"github.com/braedonsaunders/ether/internal/uploadbudget"
)

func (a *App) buildUploadBudget() {
	if a == nil || a.uploadBudget != nil {
		return
	}
	clientID := ""
	if a.cfg != nil {
		cfg := a.cfg.Read()
		clientID = plexguid.ClientIdentifier(cfg.Plex.ClientIdentifier, cfg.Server.MachineIdentifier)
	}
	poller := uploadbudget.NewPMSPoller(uploadbudget.PMSPollerOptions{
		HTTP:     egress.Client(egress.CapDirect, 4*time.Second),
		ClientID: clientID,
		Now:      parityClock(),
		LinkedPMS: func(ctx context.Context) ([]uploadbudget.ServerTarget, error) {
			if a.plexServers == nil {
				return nil, nil
			}
			servers, err := a.plexServers.LinkedServers(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]uploadbudget.ServerTarget, 0, len(servers))
			for _, srv := range servers {
				out = append(out, uploadbudget.ServerTarget{
					MachineID: srv.MachineID,
					Name:      srv.Name,
					BaseURLs:  []string{srv.BaseURL},
					Token:     srv.Token,
					Source:    "linked_pms",
				})
			}
			return out, nil
		},
		AccountResources: func(ctx context.Context) ([]plextv.Resource, string, string, error) {
			if a.cfg == nil || a.plextv == nil {
				return nil, "", "", nil
			}
			cfg := a.cfg.Read()
			if !cfg.Plex.Claimed || cfg.Plex.Token == "" {
				return nil, "", "", nil
			}
			resources, err := a.plextv.Resources(ctx, cfg.Plex.Token)
			if err != nil {
				return nil, "", "", err
			}
			pubIP := ""
			if a.registrar != nil {
				pubIP = a.registrar.Status().PublicAddress
			}
			return resources, cfg.Server.MachineIdentifier, pubIP, nil
		},
	})

	a.uploadBudget = uploadbudget.NewManager(uploadbudget.Options{
		Logger: a.log.With("component", "upload-budget"),
		Now:    parityClock(),
		OverrideKbps: func() int {
			if a.cfg == nil {
				return 0
			}
			return a.cfg.Read().Transcode.RemoteUploadBudgetKbps
		},
		VideoTranscodingOff: func() bool {
			if a.cfg == nil {
				return false
			}
			return a.cfg.Read().Transcode.VideoTranscodingOff()
		},
		ActiveSessions: func() []*core.PlaySession {
			return (ownerPlayback{Playback: a.playback, app: a}).activeLocalSessions()
		},
		AdaptiveInfo: func(sessionID string) ([]int, int, bool) {
			if txID := a.transcodeSessionIDForPlay(sessionID); txID != "" && a.transcoder != nil {
				return a.transcoder.AdaptiveBudgetInfo(txID)
			}
			return nil, 0, false
		},
		ApplySessionCap: func(sessionID string, capKbps int) (bool, string) {
			if txID := a.transcodeSessionIDForPlay(sessionID); txID != "" && a.transcoder != nil {
				applied, rungID := a.transcoder.SetSessionBudgetCap(txID, capKbps)
				if applied {
					a.notifySessionsChanged()
				}
				return applied, rungID
			}
			return false, ""
		},
		Poller: poller,
	})

	if a.transcoder != nil {
		a.transcoder.SetRemoteDeliveryObserver(a.observeTranscodeRemoteDelivery)
	}
	if a.playback != nil {
		a.playback.SetRemoteDeliveryObserver(a.observeDirectRemoteDelivery)
	}
}

func (a *App) transcodeSessionIDForPlay(playID string) string {
	if a == nil || playID == "" {
		return ""
	}
	if a.frontends != nil && a.frontends.broker != nil {
		if hs, ok := a.frontends.broker.TranscodeFor(context.Background(), playID); ok && hs != nil && hs.ID() != "" {
			return hs.ID()
		}
	}
	if a.transcoder != nil {
		if _, ok := a.transcoder.Session(playID); ok {
			return playID
		}
	}
	return ""
}

func (a *App) observeTranscodeRemoteDelivery(ev transcode.RemoteDeliveryEvent) {
	if a == nil || a.uploadBudget == nil {
		return
	}
	// Resolve the owning PlaySession to honour trusted-proxy / WAN locality
	// and strictly exclude LAN streams from the WAN upload capacity estimator.
	var matched *core.PlaySession
	for _, ps := range (ownerPlayback{Playback: a.playback, app: a}).localSessions() {
		if ps == nil {
			continue
		}
		if ps.ID == ev.SessionID || a.transcodeSessionIDForPlay(ps.ID) == ev.SessionID {
			matched = ps
			break
		}
	}
	sessionID := ev.SessionID
	starved := false
	if matched != nil {
		if !matched.Client.Remote {
			return // LAN stream: never feed WAN upload capacity estimator.
		}
		sessionID = matched.ID
		if matched.Experience != nil && matched.Experience.BufferEvents > 0 {
			starved = a.uploadBudget.NoteBufferEvents(sessionID, matched.Experience.BufferEvents)
		}
	} else {
		host, _, err := net.SplitHostPort(ev.RemoteAddr)
		if err != nil {
			return
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || clientnet.LAN(ip) {
			return
		}
	}
	a.uploadBudget.ObserveDelivery(sessionID, uploadbudget.Observation{
		Bytes:            ev.Bytes,
		Duration:         ev.Duration,
		SegDuration:      ev.SegDuration,
		MediaBitrateKbps: ev.MediaBitrateKbps,
		TCPBacklogBytes:  ev.TCPBacklogBytes,
		TCPStall:         ev.TCPStall,
		BufferFilling:    ev.BufferFilling,
		Starved:          starved,
	})
}

func (a *App) observeDirectRemoteDelivery(ev playback.RemoteDeliveryEvent) {
	if a == nil || a.uploadBudget == nil {
		return
	}
	a.uploadBudget.ObserveDelivery(ev.SessionID, uploadbudget.Observation{
		Bytes:            ev.Bytes,
		Duration:         ev.Duration,
		MediaBitrateKbps: ev.MediaBitrateKbps,
		TCPBacklogBytes:  ev.TCPBacklogBytes,
		TCPStall:         ev.TCPStall,
		BufferFilling:    ev.BufferFilling,
		Starved:          ev.Starved,
	})
}

func (a *App) remoteUploadCap(ctx context.Context, sessionID string, remote bool, allowTranscoding bool, existingCapKbps int, demandKbps int) (int, bool) {
	if a == nil || a.uploadBudget == nil {
		return existingCapKbps, false
	}
	return a.uploadBudget.RemoteCapForNewStream(ctx, sessionID, remote, allowTranscoding, existingCapKbps, demandKbps)
}

func (a *App) recordUploadCap(sessionID string, capKbps int, fromBudget bool) {
	if a == nil || a.uploadBudget == nil {
		return
	}
	a.uploadBudget.RecordSessionCap(sessionID, capKbps, fromBudget)
}

func (a *App) uploadBudgetStatus(context.Context) any {
	if a == nil || a.uploadBudget == nil {
		return uploadbudget.StatusSnapshot{Source: "learning"}
	}
	return a.uploadBudget.Status()
}

func (a *App) sessionBudgetCapInfo(p *core.PlaySession) (capKbps int, source string) {
	if a == nil || p == nil || !p.Client.Remote {
		return 0, ""
	}
	// Check live dynamic step-down on the active transcode session first.
	if txID := a.transcodeSessionIDForPlay(p.ID); txID != "" {
		if a.transcoder != nil {
			if capVal, active, _ := a.transcoder.SessionBudgetStatus(txID); active && capVal > 0 {
				return capVal, uploadbudget.CapSourceUploadBudget
			}
		}
		if a.uploadBudget != nil {
			if capVal, ok := a.uploadBudget.SessionBudgetCap(txID); ok && capVal > 0 {
				if isBudgetCapEffective(p, capVal) {
					return capVal, uploadbudget.CapSourceUploadBudget
				}
			}
		}
	}
	if a.uploadBudget != nil {
		if capVal, ok := a.uploadBudget.SessionBudgetCap(p.ID); ok && capVal > 0 {
			if isBudgetCapEffective(p, capVal) {
				return capVal, uploadbudget.CapSourceUploadBudget
			}
		}
	}
	return 0, ""
}

func isBudgetCapEffective(p *core.PlaySession, capKbps int) bool {
	if p == nil || capKbps <= 0 {
		return false
	}
	if p.Decision == core.DecisionTranscode {
		return true
	}
	if p.LiveChannel != nil && p.Client.MaxBitrateKbps > 0 && p.Client.MaxBitrateKbps <= capKbps {
		return true
	}
	src := sourceBitrateKbps(p.Info)
	return src > 0 && int(src) > capKbps
}
