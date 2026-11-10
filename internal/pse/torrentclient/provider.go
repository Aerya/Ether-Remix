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
	"github.com/braedonsaunders/ether/internal/debrid"
)

// ProviderID identifie le client torrent parmi les comptes debrid d'Ether.
const ProviderID = "remix-torrent"

// Provider présente le client BitTorrent à Ether comme un compte debrid :
// un torrent complet sur le client est « en cache », un torrent absent est
// ajouté seulement pour une lecture demandée (jamais par une vérification en
// arrière-plan), et rien n'est jamais retiré du client.
type Provider struct {
	Client   Client
	Settings Settings
	Files    *FileServer
	Log      *slog.Logger
	Now      func() time.Time

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
	found, err := p.Client.Lookup(ctx, hashes)
	if err != nil {
		return nil, p.wrap(err)
	}
	out := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		t, ok := found[normHash(h)]
		out[strings.ToLower(h)] = ok && t.Done
	}
	return out, nil
}

func (p *Provider) Find(ctx context.Context, hash string) (string, error) {
	t, err := p.Client.Get(ctx, hash)
	if err != nil {
		return "", p.wrap(err)
	}
	if t == nil {
		return "", nil
	}
	return t.Hash, nil
}

// errDailyLimit : le plafond d'ajouts du jour protège les comptes trackers.
var errDailyLimit = fmt.Errorf("client torrent : plafond d'ajouts du jour atteint : %w", core.ErrDownloadLimit)

// reserve compte un ajout ; faux une fois le plafond du jour atteint ou
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
		return debrid.Pending(hash, core.ErrNotCached)
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

// Acquire : voir Provider.
func (p *Provider) Acquire(ctx context.Context, req debrid.AcquireRequest) (*debrid.Acquired, error) {
	return p.acquire(ctx, req, false)
}

// AcquireMusic est Acquire pour un album (un seul ajout à la fois).
func (p *Provider) AcquireMusic(ctx context.Context, req debrid.AcquireRequest, download bool) (*debrid.Acquired, error) {
	req.CachedOnly = !download
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
	t, err := p.Client.Get(ctx, hash)
	if err != nil {
		return nil, p.wrap(err)
	}
	if t == nil {
		if req.CachedOnly || !p.Settings.Download || (music && !p.Settings.Music) {
			return nil, core.ErrNotCached
		}
		if req.Magnet == "" {
			return nil, core.ErrNotCached
		}
		if err := p.reserve(hash, music); err != nil {
			return nil, err
		}
		err := p.Client.Add(ctx, AddOptions{Magnet: req.Magnet, Category: p.Settings.Category, SavePath: p.Settings.SavePath,
			Ratio: p.Settings.Ratio, SeedTime: p.Settings.SeedTime})
		if err != nil {
			p.mu.Lock()
			delete(p.addedSet, hash)
			p.added--
			p.mu.Unlock()
			return nil, p.wrap(err)
		}
		p.log().Info("torrent envoyé au client", "client", p.Client.Kind(), "hash", hash)
		return nil, debrid.Pending(hash, core.ErrNotCached)
	}
	if !t.Done {
		return nil, debrid.Pending(hash, core.ErrNotCached)
	}
	files, err := p.files(ctx, *t)
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

// files liste les fichiers du torrent, chemins relatifs pour le choix et
// chemin du client dans Link.
func (p *Provider) files(ctx context.Context, t Torrent) ([]debrid.File, error) {
	raw, err := p.Client.Files(ctx, t)
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
	t, err := p.Client.Get(ctx, hash)
	if err != nil {
		return "", 0, p.wrap(err)
	}
	if t == nil || !t.Done {
		return "", 0, debrid.ErrGone
	}
	files, err := p.files(ctx, *t)
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
	local, ok := p.Settings.Local(chosen.Link)
	if !ok {
		return "", 0, fmt.Errorf("client torrent : %s hors des dossiers partagés (PSE_TORRENT_PATH_MAP)", chosen.Path)
	}
	if st, err := os.Stat(local); err != nil || !st.Mode().IsRegular() || st.Size() != chosen.Size {
		return "", 0, fmt.Errorf("client torrent : fichier absent du dossier partagé de Remix (%s) ; vérifiez le montage ou PSE_TORRENT_PATH_MAP", chosen.Path)
	}
	return local, chosen.Size, nil
}

// Remove ne retire jamais rien : le torrent continue son partage.
func (p *Provider) Remove(context.Context, string) error { return nil }

func (p *Provider) Account(ctx context.Context) (debrid.Account, error) {
	v, err := p.Client.Version(ctx)
	if err != nil {
		return debrid.Account{}, p.wrap(err)
	}
	return debrid.Account{User: p.Settings.Label() + " " + v, Premium: true}, nil
}

func (p *Provider) wrap(err error) error {
	if errors.Is(err, ErrAuth) {
		return fmt.Errorf("%w: %w", debrid.ErrAuth, err)
	}
	return err
}
