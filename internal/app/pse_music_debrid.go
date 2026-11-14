package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/music"
)

// Lecture musicale depuis les comptes debrid configurés dans Ether-Remix :
// la clé et les indexeurs sont ceux déjà réglés dans Ether, rien n'est
// stocké sur disque. Voir docs/pse/FORK.fr.md.

// newPSEMusicDebrid prépare les résolveurs dans l’ordre des comptes actifs.
// Plusieurs comptes d'un même service sont essayés dans l'ordre de leurs
// priorités : chacun a son propre résolveur, donc ses propres caches, et
// les identifiants de torrents et de fichiers restent liés au compte qui
// les a donnés. Les changements de compte, de clé ou de priorité
// reconstruisent les résolveurs (et vident leurs caches) via reloadMusic.
func (a *App) newPSEMusicDebrid() music.DebridSources {
	kinds := []string{config.DebridAllDebrid, config.DebridRealDebrid, config.DebridTorBox}
	accounts := musicDebridAccounts(a.cfg.Get().Debrid)
	sort.SliceStable(kinds, func(i, j int) bool {
		return firstPriority(accounts[kinds[i]]) < firstPriority(accounts[kinds[j]])
	})
	var out music.DebridSources
	for _, kind := range kinds {
		if kind == config.DebridRealDebrid {
			out = append(out, music.DebridSource{Kind: music.DMMSource, Name: "DMM · Real-Debrid", Resolver: a.musicDMMResolvers(accounts[kind]), NoAccount: len(accounts[kind]) == 0})
		}
		out = append(out, music.DebridSource{Kind: kind, Name: musicDebridName(kind), Resolver: a.musicDebridResolvers(kind, accounts[kind]), NoAccount: len(accounts[kind]) == 0})
	}
	// Client torrent de Remix, en dernier : il télécharge ce qui manque.
	out = append(out, a.musicTorrentSource())
	return out
}

// musicDebridAccounts liste, par service, les comptes actifs avec une clé,
// triés par priorité.
func musicDebridAccounts(d config.Debrid) map[string][]config.DebridProvider {
	providers := append([]config.DebridProvider(nil), d.Providers...)
	sort.SliceStable(providers, func(i, j int) bool { return providers[i].Priority < providers[j].Priority })
	out := map[string][]config.DebridProvider{}
	for _, p := range providers {
		if p.Enabled && strings.TrimSpace(p.APIKey) != "" {
			out[p.Kind] = append(out[p.Kind], p)
		}
	}
	return out
}

func firstPriority(ps []config.DebridProvider) int {
	if len(ps) == 0 {
		return int(^uint(0) >> 1)
	}
	return ps[0].Priority
}

// musicDebridResolvers renvoie le résolveur du service : un seul compte,
// ou les comptes dans l'ordre, le suivant prenant le relais quand le
// précédent n'a pas le titre ou est en panne.
func (a *App) musicDebridResolvers(kind string, accounts []config.DebridProvider) music.Resolver {
	if len(accounts) <= 1 {
		return a.musicDebridResolver(kind)
	}
	rs := make(music.Resolvers, 0, len(accounts))
	for i, p := range accounts {
		r := a.musicDebridResolver(kind)
		r.Provider = fmt.Sprintf("%s #%d", musicDebridName(kind), i+1)
		r.Account.(*pseDebridAccount).id, r.Account.(*pseDebridAccount).index = p.ID, i
		rs = append(rs, r)
	}
	return rs
}

// DMM est indépendant de Prowlarr et ne reçoit jamais la clé du compte.
func (a *App) musicDMMResolvers(accounts []config.DebridProvider) music.Resolver {
	var rs music.Resolvers
	for i := 0; i < max(1, len(accounts)); i++ {
		r := a.musicDebridResolver(config.DebridRealDebrid)
		r.Provider = "DMM · Real-Debrid"
		r.Finder = &music.DMMFinder{BaseURL: music.LoadSettings().DMMURL, HTTP: egress.Client(egress.CapIndexers, 15*time.Second), Log: a.log}
		account := r.Account.(*pseDebridAccount)
		account.dmm = true
		account.log = a.log
		if len(accounts) > 0 {
			account.id, account.index = accounts[i].ID, i
		}
		if len(accounts) > 1 {
			r.Provider = fmt.Sprintf("%s #%d", r.Provider, i+1)
		}
		rs = append(rs, r)
	}
	if len(rs) == 1 {
		return rs[0]
	}
	return rs
}

func musicDebridName(kind string) string {
	switch kind {
	case config.DebridRealDebrid:
		return "Real-Debrid"
	case config.DebridTorBox:
		return "TorBox"
	default:
		return "AllDebrid"
	}
}

func (a *App) musicDebridResolver(kind string) *music.DebridResolver {
	return &music.DebridResolver{
		Provider: musicDebridName(kind),
		Finder:   &music.IndexerFinder{Config: a.pseMusicIndexers, HTTP: egress.Client(egress.CapIndexers, 20*time.Second)},
		Account:  &pseDebridAccount{kind: kind, cfg: func() config.Debrid { return a.cfg.Get().Debrid }},
	}
}

func (a *App) pseMusicIndexers() music.IndexerConfig {
	cfg := a.cfg.Get()
	out := music.IndexerConfig{
		ProwlarrURL: strings.TrimSpace(cfg.Prowlarr.URL),
		ProwlarrKey: strings.TrimSpace(cfg.Prowlarr.APIKey),
	}
	for _, ix := range cfg.Indexers {
		if ix.Enabled && ix.Protocol == "torrent" && strings.TrimSpace(ix.URL) != "" {
			out.Torznab = append(out.Torznab, music.TorznabIndexer{Name: ix.Name, URL: ix.URL, APIKey: ix.APIKey})
		}
	}
	return out
}

// pseDebridAccount relie le résolveur à un compte du service : celui dont
// l'identifiant est id, ou à défaut le index-ième par priorité (le premier
// quand rien n'est précisé).
type pseDebridAccount struct {
	cfg   func() config.Debrid
	kind  string
	dmm   bool
	log   *slog.Logger
	id    string
	index int
	opts  debrid.ProviderOptions

	mu   sync.Mutex
	key  string
	prov debrid.Provider
}

func (d *pseDebridAccount) account() (config.DebridProvider, bool) {
	accounts := musicDebridAccounts(d.cfg())[d.kind]
	if d.id != "" {
		for _, p := range accounts {
			if p.ID == d.id {
				return p, true
			}
		}
		return config.DebridProvider{}, false
	}
	if d.index < len(accounts) {
		return accounts[d.index], true
	}
	return config.DebridProvider{}, false
}

func (d *pseDebridAccount) provider() (debrid.Provider, error) {
	p, ok := d.account()
	if !ok {
		return nil, fmt.Errorf("musique : aucun compte %s actif dans Ether-Remix (Sources › Debrid)", musicDebridName(d.kind))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.prov != nil && d.key == p.APIKey {
		return d.prov, nil
	}
	prov, err := debrid.NewProvider(p, d.opts)
	if err != nil {
		return nil, err
	}
	d.prov, d.key = prov, p.APIKey
	return prov, nil
}

// OpenCached ouvre le torrent seulement s'il est déjà en cache ; un torrent
// ajouté pour vérification et absent du cache (ou sans audio) est retiré.
func (d *pseDebridAccount) OpenCached(ctx context.Context, hit music.TorrentHit) (files []music.DebridFile, err error) {
	defer func() { d.traceDMM("cache", err, len(files)) }()
	prov, err := d.provider()
	if err != nil {
		return nil, err
	}
	id := ""
	if hit.InfoHash != "" {
		if found, err := prov.Find(ctx, hit.InfoHash); err == nil {
			id = found
		}
	}
	acquired, err := prov.Acquire(ctx, debrid.AcquireRequest{
		InfoHash: hit.InfoHash, Magnet: hit.Magnet, TorrentID: id,
		CachedOnly: true, RemoveUncached: true,
		SelectFiles: func(fs []debrid.File, chosen debrid.File) []string {
			if d.dmm {
				// DMM met en cache la sélection complète FLAC/MP3.
				var ids []string
				for _, f := range fs {
					ext := strings.ToLower(path.Ext(f.Path))
					if ext == ".flac" || ext == ".mp3" {
						ids = append(ids, f.ID)
					}
				}
				if len(ids) > 0 {
					return ids
				}
			}
			return musicAudioSelection(fs, chosen)
		},
		Choose: func(fs []debrid.File) (debrid.File, error) {
			if d.dmm {
				var preferred []debrid.File
				for _, f := range fs {
					ext := strings.ToLower(path.Ext(f.Path))
					if ext == ".flac" || ext == ".mp3" {
						preferred = append(preferred, f)
					}
				}
				if len(preferred) > 0 {
					fs = preferred
				}
			}
			// Prefer an already selected track in a reused Real-Debrid album.
			for _, f := range fs {
				if music.IsAudio(f.Path) && f.Link != "" {
					return f, nil
				}
			}
			for _, f := range fs {
				if music.IsAudio(f.Path) {
					return f, nil
				}
			}
			return debrid.File{}, music.ErrNotCached
		},
	})
	if err != nil {
		if errors.Is(err, core.ErrNotCached) {
			return nil, music.ErrNotCached
		}
		return nil, err
	}
	out := make([]music.DebridFile, 0, len(acquired.Files))
	for _, f := range acquired.Files {
		if !music.IsAudio(f.Path) || (d.kind == config.DebridRealDebrid && f.Link == "") {
			continue
		}
		out = append(out, music.DebridFile{Path: f.Path, Size: f.Size, Link: f.Link, TorrentID: acquired.TorrentID, FileID: f.ID, AccountID: fmt.Sprintf("%p", prov)})
	}
	return out, nil
}

// Unlock renvoie le lien de lecture direct d'un fichier.
func (d *pseDebridAccount) Unlock(ctx context.Context, file music.DebridFile) (stream music.Stream, err error) {
	defer func() { d.traceDMM("link", err, 0) }()
	prov, err := d.provider()
	if err != nil {
		return music.Stream{}, err
	}
	if file.AccountID != fmt.Sprintf("%p", prov) {
		return music.Stream{}, errors.New("musique : compte modifié ; relancez la lecture pour renouveler les liens")
	}
	dl, err := prov.Link(ctx, debrid.Handle{Link: file.Link, TorrentID: file.TorrentID, FileID: file.FileID, Path: file.Path, Size: file.Size})
	if err != nil {
		return music.Stream{}, err
	}
	st := music.Stream{URL: dl.URL}
	if !dl.Expires.IsZero() {
		st.ExpiresAt = dl.Expires.Unix()
	}
	return st, nil
}

func musicAudioSelection(files []debrid.File, chosen debrid.File) []string {
	ids := []string{chosen.ID}
	for _, f := range files {
		if f.ID != chosen.ID && music.IsAudio(f.Path) {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

// testPSEMusicDebrid vérifie le compte puis cherche un lien sur les albums du moment.
// Le diagnostic des sources vérifie séparément les octets audio.
func (a *App) testPSEMusicDebrid(ctx context.Context, kind string) (string, error) {
	if kind == music.DMMSource {
		return a.testPSEMusicDMM(ctx)
	}
	acc := &pseDebridAccount{kind: kind, cfg: func() config.Debrid { return a.cfg.Get().Debrid }}
	prov, err := acc.provider()
	if err != nil {
		return "", err
	}
	if _, err := prov.Account(ctx); err != nil {
		return "", errors.New("musique : compte " + musicDebridName(kind) + " inaccessible ou non autorisé")
	}
	if ix := a.pseMusicIndexers(); ix.ProwlarrURL == "" && len(ix.Torznab) == 0 {
		return "", errors.New("musique : aucun indexeur torrent ni Prowlarr dans Ether (Sources › Indexeurs)")
	}
	dz := music.NewDeezerSource(nil, nil)
	albums, err := dz.ChartAlbums(ctx, 8)
	if err != nil {
		return "", errors.New("musique : catalogue Deezer injoignable")
	}
	res := a.musicDebridResolver(kind)
	res.Tries = 3
	tried := 0
	for _, al := range albums {
		if tried >= 5 || ctx.Err() != nil {
			break
		}
		full, tracks, err := dz.Album(ctx, al.ID)
		if err != nil || len(tracks) == 0 {
			continue
		}
		tried++
		t := tracks[0]
		if t.Album.ID == 0 {
			t.Album = full
		}
		if _, err := res.Resolve(ctx, t); err == nil {
			return "Lien " + res.Provider + " obtenu : « " + full.Title + " » de " + full.Artist.Name + " est disponible. Utilisez le diagnostic des sources pour vérifier les octets audio.", nil
		}
	}
	if ctx.Err() != nil {
		return "", errors.New("musique : délai dépassé pendant la recherche ; lecture non vérifiée")
	}

	return "", errors.New("Compte " + musicDebridName(kind) + " joignable, mais aucun lien audio obtenu sur les albums testés. Vérifiez les indexeurs et la disponibilité en cache.")
}
