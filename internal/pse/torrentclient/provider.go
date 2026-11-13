package torrentclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/datafs"
	"github.com/braedonsaunders/ether/internal/debrid"
)

// ProviderID identifie le client torrent parmi les comptes debrid d'Ether.
const ProviderID = "remix-torrent"

// Slot est un client BitTorrent et ses réglages.
type Slot struct {
	Conf   ClientConf
	Client Client

	mu       sync.Mutex
	dir      string // dossier par défaut du client, retenu
	dirAt    time.Time
	free     int64 // place libre mesurée, -1 : inconnue
	freeAt   time.Time
	freeErr  string
	lowSpace bool // sous le seuil lors de la dernière mesure
}

// Provider présente les clients BitTorrent à Ether comme un compte debrid :
// un torrent complet sur l'un d'eux est « en cache », un torrent absent est
// ajouté seulement pour une lecture demandée (jamais par une vérification en
// arrière-plan), et rien n'est jamais retiré d'un client.
type Provider struct {
	Slots    []*Slot
	Settings Settings
	Files    *FileServer
	Log      *slog.Logger
	Now      func() time.Time
	// Lang : langue des messages aux spectateurs (fr, en).
	Lang string
	// Usage mesure la place d'un dossier (datafs.Usage par défaut).
	Usage func(path string) (total, free int64, err error)

	mu       sync.Mutex
	day      string
	added    int
	musicAt  time.Time
	addedSet map[string]time.Time
}

var _ debrid.LocalDownloader = (*Provider)(nil)

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Provider) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

func (p *Provider) Kind() string { return ProviderID }

// LocalDownloads : les releases absentes peuvent être envoyées au client.
func (p *Provider) LocalDownloads() bool { return p.Settings.Download }

func (p *Provider) CheckCached(ctx context.Context, hashes []string) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	var firstErr error
	answered := false
	for _, s := range p.Slots {
		found, err := s.Client.Lookup(ctx, hashes)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		answered = true
		for _, h := range hashes {
			if t, ok := found[normHash(h)]; ok && t.Done {
				out[strings.ToLower(h)] = true
			}
		}
	}
	if !answered && firstErr != nil {
		return nil, p.wrap(firstErr)
	}
	for _, h := range hashes {
		if _, ok := out[strings.ToLower(h)]; !ok {
			out[strings.ToLower(h)] = false
		}
	}
	return out, nil
}

// find cherche le torrent sur les clients, dans l'ordre. Une panne d'un
// client ne cache pas un torrent présent sur un autre.
func (p *Provider) find(ctx context.Context, hash string) (*Slot, *Torrent, error) {
	var firstErr error
	var pendingSlot *Slot
	var pendingTorrent *Torrent
	answered := false
	for _, s := range p.Slots {
		t, err := s.Client.Get(ctx, hash)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		answered = true
		if t != nil && t.Done {
			return s, t, nil
		}
		if t != nil && pendingTorrent == nil {
			pendingSlot, pendingTorrent = s, t
		}
	}
	if pendingTorrent != nil {
		return pendingSlot, pendingTorrent, nil
	}
	// Un client indisponible ne bloque pas les ajouts sur les clients sains.
	if answered {
		return nil, nil, nil
	}
	return nil, nil, firstErr
}

func (p *Provider) Find(ctx context.Context, hash string) (string, error) {
	_, t, err := p.find(ctx, normHash(hash))
	if t != nil {
		return t.Hash, nil
	}
	if err != nil {
		return "", p.wrap(err)
	}
	return "", nil
}

// errDailyLimit : le plafond d'ajouts du jour protège les comptes trackers.
var errDailyLimit = fmt.Errorf("client torrent : plafond d'ajouts du jour atteint : %w", core.ErrDownloadLimit)

// reserve compte un ajout ; refuse une fois le plafond du jour atteint ou
// quand le torrent vient déjà d'être envoyé.
func (p *Provider) reserve(hash string, music bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	day := now.Format("2006-01-02")
	if day != p.day {
		p.day, p.added, p.addedSet = day, 0, map[string]time.Time{}
	}
	if at, ok := p.addedSet[hash]; ok && now.Sub(at) < time.Hour {
		return debrid.Pending(hash, p.downloading(nil, nil))
	}
	if p.added >= p.Settings.MaxPerDay {
		return errDailyLimit
	}
	if music {
		// Musique : un album à la fois, pour qu'une liste de lecture
		// n'envoie pas des dizaines d'albums d'un coup.
		if !p.musicAt.IsZero() && now.Sub(p.musicAt) < 2*time.Minute {
			return core.ErrNotCached
		}
		p.musicAt = now
	}
	p.added++
	p.addedSet[hash] = now
	return nil
}

func (p *Provider) unreserve(hash string) {
	p.mu.Lock()
	delete(p.addedSet, hash)
	p.added--
	p.mu.Unlock()
}

// downloading décrit un téléchargement en cours (t nil : envoyé à l'instant).
func (p *Provider) downloading(s *Slot, t *Torrent) error {
	e := &core.LocalDownloadError{Lang: p.Lang, Started: t == nil}
	if s != nil {
		e.Client = s.Conf.Label()
	}
	if t != nil {
		e.Progress, e.ETA = t.Progress, t.ETA
	}
	return e
}

// Acquire : voir Provider.
func (p *Provider) Acquire(ctx context.Context, req debrid.AcquireRequest) (*debrid.Acquired, error) {
	return p.acquire(ctx, req, false)
}

// AcquireMusic est Acquire pour un album (un seul ajout à la fois).
func (p *Provider) AcquireMusic(ctx context.Context, req debrid.AcquireRequest, download bool) (*debrid.Acquired, error) {
	req.CachedOnly = req.CachedOnly || !download
	return p.acquire(ctx, req, true)
}

func (p *Provider) acquire(ctx context.Context, req debrid.AcquireRequest, music bool) (*debrid.Acquired, error) {
	hash := normHash(req.InfoHash)
	if hash == "" {
		hash = normHash(req.TorrentID)
	}
	if !validHash(hash) {
		return nil, fmt.Errorf("client torrent : empreinte invalide : %w", core.ErrReleaseRejected)
	}
	slot, t, err := p.find(ctx, hash)
	if t == nil && err != nil {
		return nil, p.wrap(err)
	}
	if t == nil {
		if req.CachedOnly || core.IsBackgroundCheck(ctx) || !p.Settings.Download || (music && !p.Settings.Music) || req.Magnet == "" {
			return nil, core.ErrNotCached
		}
		return nil, p.add(ctx, hash, req.Magnet, music)
	}
	if !t.Done {
		return nil, debrid.Pending(hash, p.downloading(slot, t))
	}
	files, err := p.files(ctx, slot, *t)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, debrid.ErrGone
	}
	if req.Choose == nil {
		return nil, errors.New("client torrent : aucun choix de fichier")
	}
	file, err := req.Choose(files)
	if err != nil {
		return nil, err
	}
	return &debrid.Acquired{TorrentID: t.Hash, File: file, Files: files}, nil
}

// add envoie le torrent au premier client qui a la place et l'accepte.
func (p *Provider) add(ctx context.Context, hash, magnet string, music bool) error {
	if err := p.reserve(hash, music); err != nil {
		return err
	}
	var lastErr error
	full := 0
	for _, s := range p.Slots {
		if free, known := p.FreeSpace(ctx, s); known && p.Settings.MinFree > 0 && free < p.Settings.MinFree {
			full++
			continue
		}
		ratio, seed := p.Settings.Limits(s.Conf, magnet)
		err := s.Client.Add(ctx, AddOptions{Magnet: magnet, Category: p.Settings.Category, SavePath: s.Conf.SavePath,
			Ratio: ratio, SeedTime: seed})
		if err != nil {
			lastErr = err
			p.log().Warn("client torrent : ajout refusé", "client", s.Conf.Label(), "err", err)
			continue
		}
		p.log().Info("torrent envoyé au client", "client", s.Conf.Label(), "hash", hash, "ratio", ratio, "seed", seed)
		return debrid.Pending(hash, p.downloading(s, nil))
	}
	p.unreserve(hash)
	if lastErr == nil && full > 0 {
		return core.ErrLocalNoSpace
	}
	if lastErr == nil {
		return core.ErrDownloadRefused
	}
	if errors.Is(lastErr, ErrAuth) {
		return p.wrap(lastErr)
	}
	return fmt.Errorf("%w: %w", core.ErrDownloadRefused, lastErr)
}

// files liste les fichiers du torrent, chemins relatifs pour le choix et
// chemin du client dans Link.
func (p *Provider) files(ctx context.Context, s *Slot, t Torrent) ([]debrid.File, error) {
	raw, err := s.Client.Files(ctx, t)
	if err != nil {
		return nil, p.wrap(err)
	}
	root := strings.TrimRight(t.Root, "/") + "/"
	out := make([]debrid.File, 0, len(raw))
	for i, f := range raw {
		if f.Size <= 0 {
			continue
		}
		rel := strings.TrimPrefix(f.Path, root)
		out = append(out, debrid.File{ID: strconv.Itoa(i), Path: rel, Size: f.Size, Link: f.Path})
	}
	return out, nil
}

// Link publie le fichier sur le serveur local après avoir vérifié qu'il
// appartient toujours au torrent du client.
func (p *Provider) Link(ctx context.Context, h debrid.Handle) (debrid.DirectLink, error) {
	local, size, err := p.localFile(ctx, h)
	if err != nil {
		return debrid.DirectLink{}, err
	}
	u, until, err := p.Files.URL(local)
	if err != nil {
		return debrid.DirectLink{}, err
	}
	return debrid.DirectLink{URL: u, Size: size, Expires: until}, nil
}

// LocalFile renvoie le chemin, vu par Remix, d'un fichier d'un torrent complet.
func (p *Provider) LocalFile(ctx context.Context, h debrid.Handle) (string, error) {
	local, _, err := p.localFile(ctx, h)
	return local, err
}

func (p *Provider) localFile(ctx context.Context, h debrid.Handle) (string, int64, error) {
	hash := normHash(h.TorrentID)
	if hash == "" {
		hash = normHash(h.InfoHash)
	}
	slot, t, err := p.find(ctx, hash)
	if t == nil && err != nil {
		return "", 0, p.wrap(err)
	}
	if t == nil || !t.Done {
		return "", 0, debrid.ErrGone
	}
	files, err := p.files(ctx, slot, *t)
	if err != nil {
		return "", 0, err
	}
	var chosen *debrid.File
	for i := range files {
		if (h.Link != "" && files[i].Link == h.Link) || (h.Link == "" && files[i].Path == h.Path) {
			chosen = &files[i]
			break
		}
	}
	if chosen == nil {
		return "", 0, debrid.ErrGone
	}
	local, ok := slot.Conf.Local(chosen.Link)
	if !ok {
		return "", 0, fmt.Errorf("client torrent : %s hors des dossiers partagés (correspondance des dossiers de %s)", chosen.Path, slot.Conf.Label())
	}
	if st, err := os.Stat(local); err != nil || !st.Mode().IsRegular() || st.Size() != chosen.Size {
		return "", 0, fmt.Errorf("client torrent : fichier absent du dossier partagé de Remix (%s) ; vérifiez le montage ou la correspondance des dossiers", chosen.Path)
	}
	return local, chosen.Size, nil
}

// Remove ne retire jamais rien : le torrent continue son partage.
func (p *Provider) Remove(context.Context, string) error { return nil }

func (p *Provider) Account(ctx context.Context) (debrid.Account, error) {
	var names []string
	var firstErr error
	for _, s := range p.Slots {
		v, err := s.Client.Version(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		names = append(names, s.Conf.Label()+" "+v)
	}
	if len(names) == 0 && firstErr != nil {
		return debrid.Account{}, p.wrap(firstErr)
	}
	return debrid.Account{User: strings.Join(names, ", "), Premium: true}, nil
}

func (p *Provider) wrap(err error) error {
	if errors.Is(err, ErrAuth) {
		return fmt.Errorf("%w: %w", debrid.ErrAuth, err)
	}
	return err
}

// --- place libre ---

const (
	freeTTL = time.Minute
	dirTTL  = 10 * time.Minute
)

// FreeSpace mesure la place libre du dossier de téléchargement de s, vu
// par Remix : le dossier réglé, sinon celui du client. known est faux quand
// Remix ne voit pas ce dossier.
func (p *Provider) FreeSpace(ctx context.Context, s *Slot) (int64, bool) {
	s.mu.Lock()
	if !s.freeAt.IsZero() && p.now().Sub(s.freeAt) < freeTTL {
		free := s.free
		s.mu.Unlock()
		return free, free >= 0
	}
	s.mu.Unlock()
	free, msg := p.measure(ctx, s)
	s.mu.Lock()
	s.free, s.freeErr, s.freeAt = free, msg, p.now()
	s.mu.Unlock()
	return free, free >= 0
}

func (p *Provider) measure(ctx context.Context, s *Slot) (int64, string) {
	dir := s.Conf.SavePath
	if dir == "" {
		s.mu.Lock()
		dir = s.dir
		fresh := !s.dirAt.IsZero() && p.now().Sub(s.dirAt) < dirTTL
		s.mu.Unlock()
		if !fresh {
			d, err := s.Client.DefaultDir(ctx)
			if err != nil {
				return -1, "dossier du client inconnu"
			}
			dir = d
			s.mu.Lock()
			s.dir, s.dirAt = d, p.now()
			s.mu.Unlock()
		}
	}
	local, ok := s.Conf.Local(dir)
	if !ok {
		return -1, "dossier " + dir + " hors de la correspondance des dossiers"
	}
	usage := p.Usage
	if usage == nil {
		usage = datafs.Usage
	}
	_, free, err := usage(local)
	if err != nil {
		return -1, "dossier " + local + " invisible pour Remix"
	}
	return free, ""
}
