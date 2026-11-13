package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/notify"
	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/pse/notifyfr"
	"github.com/braedonsaunders/ether/internal/pse/torrentclient"
)

// Client BitTorrent du propriétaire, en option (PSE_TORRENT, désactivé par
// défaut) : il rejoint les comptes debrid d'Ether sous le nom « Client
// torrent ». Voir internal/pse/torrentclient.

type pseTorrentState struct {
	mu    sync.Mutex
	key   [32]byte
	prov  *torrentclient.Provider
	mon   *torrentclient.Monitor
	files torrentclient.FileServer
}

// reloadTorrent applique les réglages PSE_TORRENT_* : (dés)inscription du
// client auprès d'Ether et surveillance cross-seed / limites de partage.
func (rt *pseRuntime) reloadTorrent(a *App) {
	if a.debrid == nil {
		return
	}
	st := &rt.torrent
	s := torrentclient.LoadSettings()
	raw, _ := json.Marshal(s)
	key := sha256.Sum256(raw)
	st.mu.Lock()
	defer st.mu.Unlock()
	if key == st.key && (st.prov != nil) == s.Enabled {
		return
	}
	st.key = key
	if st.mon != nil {
		st.mon.Close()
		st.mon = nil
	}
	if !s.Enabled {
		if st.prov != nil {
			a.log.Info("client torrent Ether-Remix désactivé")
		}
		st.prov = nil
		a.debrid.RegisterPluginProvider(torrentclient.ProviderID, "", nil)
		return
	}
	log := a.log.With("component", "pse-torrent")
	prov := &torrentclient.Provider{Settings: s, Files: &st.files, Log: log, Lang: "en"}
	if notifyfr.Enabled() {
		prov.Lang = "fr"
	}
	core.LocalMessageLang.Store(prov.Lang)
	for _, c := range s.Clients {
		client, err := c.NewClient(egress.Client(egress.CapDebrid, 20*time.Second))
		if err != nil {
			log.Warn("client torrent non configuré", "client", c.Label(), "err", err)
			continue
		}
		prov.Slots = append(prov.Slots, &torrentclient.Slot{Conf: c, Client: client})
	}
	if len(prov.Slots) == 0 {
		st.prov = nil
		a.debrid.RegisterPluginProvider(torrentclient.ProviderID, "", nil)
		return
	}
	st.prov = prov
	a.debrid.RegisterPluginProvider(torrentclient.ProviderID, "Client torrent ("+s.Label()+")", prov)
	var xseed *torrentclient.CrossSeed
	if s.XSeedURL != "" {
		var err error
		if xseed, err = torrentclient.NewCrossSeed(s.XSeedURL, s.XSeedKey, egress.Client(egress.CapDebrid, 30*time.Second)); err != nil {
			log.Warn("cross-seed non configuré", "err", err)
		}
	}
	st.mon = &torrentclient.Monitor{Provider: prov, XSeed: xseed, Log: log, Alert: a.pseTorrentAlert}
	st.mon.Start()
	log.Info("client torrent Ether-Remix actif", "clients", s.Label(), "downloads", s.Download, "cross-seed", xseed != nil)
}

// pseTorrentAlert passe par les notifications d'Ether : « fournisseur
// indisponible » pour une alerte, « fournisseur rétabli » au retour.
func (a *App) pseTorrentAlert(ctx context.Context, critical bool, title, message string) {
	if a.notifications == nil {
		return
	}
	kind := "provider.recovered"
	if critical {
		kind = "provider.outage"
	}
	if err := a.notifications.Emit(ctx, notify.Event{Kind: kind, Title: title, Message: message}); err != nil {
		a.log.Warn("alerte du client torrent non envoyée", "err", err)
	}
}

// pseTorrentStatus est l'état affiché dans la console.
func (a *App) pseTorrentStatus(ctx context.Context) map[string]any {
	out := map[string]any{"active": false}
	prov := a.pseTorrentProvider()
	if prov == nil {
		return out
	}
	out["active"] = true
	out["state"] = prov.Status(ctx)
	return out
}

func (rt *pseRuntime) stopTorrent() {
	st := &rt.torrent
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.mon != nil {
		st.mon.Close()
		st.mon = nil
	}
	st.files.Close()
}

func (a *App) startPSETorrent() {
	rt := pseRuntimeOf(a)
	rt.reloadTorrent(a)
	a.closers = append(a.closers, rt.stopTorrent)
}

// pseTorrentProvider renvoie le client actif, ou nil.
func (a *App) pseTorrentProvider() *torrentclient.Provider {
	st := &pseRuntimeOf(a).torrent
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.prov
}

// --- musique ---

func (a *App) musicTorrentSource() music.DebridSource {
	return music.DebridSource{Kind: music.TorrentSource, Name: "Client torrent",
		Resolver: &music.DebridResolver{
			Provider: "Client torrent",
			Finder:   &music.IndexerFinder{Config: a.pseMusicIndexers, HTTP: egress.Client(egress.CapIndexers, 20*time.Second)},
			Account:  &pseTorrentMusicAccount{a: a},
		},
		NoAccount: a.pseTorrentProvider() == nil}
}

// pseTorrentMusicAccount lit les albums complets sur le client ; un album
// absent est envoyé au client (un à la fois) et sera lisible une fois
// téléchargé.
type pseTorrentMusicAccount struct{ a *App }

func (m *pseTorrentMusicAccount) OpenCached(ctx context.Context, hit music.TorrentHit) ([]music.DebridFile, error) {
	prov := m.a.pseTorrentProvider()
	if prov == nil {
		return nil, errors.New("musique : client torrent désactivé (Ether-Remix › Client torrent)")
	}
	acq, err := prov.AcquireMusic(ctx, debrid.AcquireRequest{InfoHash: hit.InfoHash, Magnet: hit.Magnet,
		Choose: func(fs []debrid.File) (debrid.File, error) {
			for _, f := range fs {
				if music.IsAudio(f.Path) {
					return f, nil
				}
			}
			return debrid.File{}, music.ErrNotCached
		}}, hit.Seeders > 0 && !music.CachedOnly(ctx))
	if err != nil {
		if errors.As(err, new(*core.LocalDownloadError)) {
			return nil, fmt.Errorf("%w: %w", music.ErrDownloadPending, err)
		}
		if errors.Is(err, core.ErrNotCached) || errors.Is(err, core.ErrDownloadRefused) || errors.Is(err, music.ErrNotCached) {
			return nil, music.ErrNotCached
		}
		return nil, err
	}
	out := make([]music.DebridFile, 0, len(acq.Files))
	for _, f := range acq.Files {
		if music.IsAudio(f.Path) {
			out = append(out, music.DebridFile{Path: f.Path, Size: f.Size, Link: f.Link, TorrentID: acq.TorrentID, FileID: f.ID, AccountID: "torrent"})
		}
	}
	return out, nil
}

func (m *pseTorrentMusicAccount) Unlock(ctx context.Context, file music.DebridFile) (music.Stream, error) {
	prov := m.a.pseTorrentProvider()
	if prov == nil {
		return music.Stream{}, errors.New("musique : client torrent désactivé")
	}
	local, err := prov.LocalFile(ctx, debrid.Handle{TorrentID: file.TorrentID, FileID: file.FileID, Path: file.Path, Link: file.Link, Size: file.Size})
	if err != nil {
		return music.Stream{}, err
	}
	return music.Stream{URL: fmt.Sprintf("torrent://%s/%s", strings.ToLower(file.TorrentID), file.FileID), File: local,
		ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
}
