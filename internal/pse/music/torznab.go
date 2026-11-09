package music

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/newznab"
)

// Recherche d'albums sur les indexeurs torrent d'Ether : Prowlarr (un seul
// appel pour tous ses indexeurs) quand il est configuré, sinon chaque
// indexeur Torznab, en catégorie audio (3000).

// TorznabIndexer est un indexeur torrent d'Ether.
type TorznabIndexer struct {
	Name, URL, APIKey string
}

// IndexerConfig décrit les indexeurs utilisables.
type IndexerConfig struct {
	ProwlarrURL, ProwlarrKey string
	Torznab                  []TorznabIndexer
}

// IndexerFinder implémente TorrentFinder.
type IndexerFinder struct {
	Config func() IndexerConfig
	HTTP   *http.Client
}

const audioCategory = "3000"

func (f *IndexerFinder) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// FindAlbum renvoie les torrents trouvés pour la requête.
func (f *IndexerFinder) FindAlbum(ctx context.Context, query string) ([]TorrentHit, error) {
	cfg := f.Config()
	if cfg.ProwlarrURL != "" && cfg.ProwlarrKey != "" {
		hits, err := f.prowlarr(ctx, cfg, query)
		if err == nil || len(cfg.Torznab) == 0 {
			return hits, err
		}
	}
	if len(cfg.Torznab) == 0 {
		return nil, errors.New("music: aucun indexeur torrent configuré dans Ether")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var (
		mu   sync.Mutex
		all  []TorrentHit
		errs int
		wg   sync.WaitGroup
	)
	sem := make(chan struct{}, 8)
	for _, ix := range cfg.Torznab {
		wg.Add(1)
		go func(ix TorznabIndexer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			hits, err := f.torznab(ctx, ix, query)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
				return
			}
			all = append(all, hits...)
		}(ix)
	}
	wg.Wait()
	if len(all) == 0 && errs == len(cfg.Torznab) {
		return nil, errors.New("music: aucun indexeur torrent n'a répondu")
	}
	return all, nil
}

func (f *IndexerFinder) get(ctx context.Context, u string, header http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("music: indexeur HTTP " + strconv.Itoa(resp.StatusCode))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func (f *IndexerFinder) prowlarr(ctx context.Context, cfg IndexerConfig, query string) ([]TorrentHit, error) {
	u := strings.TrimRight(cfg.ProwlarrURL, "/") + "/api/v1/search?" + url.Values{
		"query": {query}, "categories": {audioCategory}, "type": {"search"}, "limit": {"100"},
	}.Encode()
	body, err := f.get(ctx, u, http.Header{"X-Api-Key": {cfg.ProwlarrKey}, "Accept": {"application/json"}})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Title       string `json:"title"`
		Protocol    string `json:"protocol"`
		InfoHash    string `json:"infoHash"`
		MagnetURL   string `json:"magnetUrl"`
		DownloadURL string `json:"downloadUrl"`
		GUID        string `json:"guid"`
		Seeders     int    `json:"seeders"`
		Size        int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, errors.New("music: réponse Prowlarr illisible")
	}
	var out []TorrentHit
	for _, r := range rows {
		if r.Protocol != "" && r.Protocol != "torrent" {
			continue
		}
		magnet := r.MagnetURL
		if magnet == "" && strings.HasPrefix(strings.ToLower(r.GUID), "magnet:") {
			magnet = r.GUID
		}
		out = append(out, newHit(r.Title, r.InfoHash, magnet, r.DownloadURL, r.Seeders, r.Size))
	}
	return out, nil
}

func (f *IndexerFinder) torznab(ctx context.Context, ix TorznabIndexer, query string) ([]TorrentHit, error) {
	base := strings.TrimRight(ix.URL, "/")
	if !strings.HasSuffix(base, "/api") {
		base += "/api"
	}
	q := url.Values{"t": {"search"}, "q": {query}, "cat": {audioCategory}, "limit": {"100"}}
	if ix.APIKey != "" {
		q.Set("apikey", ix.APIKey)
	}
	body, err := f.get(ctx, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			Size      int64  `xml:"size"`
			Enclosure struct {
				URL string `xml:"url,attr"`
			} `xml:"enclosure"`
			Attrs []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"attr"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("music: réponse Torznab illisible")
	}
	var out []TorrentHit
	for _, it := range doc.Items {
		var hash, magnet string
		seeders := 0
		size := it.Size
		for _, a := range it.Attrs {
			switch strings.ToLower(a.Name) {
			case "infohash":
				hash = a.Value
			case "magneturl":
				magnet = a.Value
			case "seeders":
				seeders, _ = strconv.Atoi(a.Value)
			case "size":
				if n, err := strconv.ParseInt(a.Value, 10, 64); err == nil {
					size = n
				}
			}
		}
		dl := it.Enclosure.URL
		if dl == "" {
			dl = it.Link
		}
		if magnet == "" && strings.HasPrefix(strings.ToLower(dl), "magnet:") {
			magnet, dl = dl, ""
		}
		out = append(out, newHit(it.Title, hash, magnet, dl, seeders, size))
	}
	return out, nil
}

func newHit(title, hash, magnet, download string, seeders int, size int64) TorrentHit {
	h := newznab.NormalizeInfoHash(hash)
	if h == "" && magnet != "" {
		h = newznab.InfoHashFromMagnet(magnet)
	}
	if magnet == "" && h != "" {
		magnet = newznab.MagnetFor(h, title, nil)
	}
	return TorrentHit{Title: strings.TrimSpace(title), InfoHash: h, Magnet: magnet, Download: download, Seeders: seeders, Size: size}
}

// Magnet complète un résultat sans empreinte en lisant son lien de
// téléchargement : redirection vers un magnet, ou fichier .torrent. Rien
// n'est écrit sur disque.
func (f *IndexerFinder) Magnet(ctx context.Context, h TorrentHit) (TorrentHit, error) {
	if h.Magnet != "" || h.Download == "" {
		return h, nil
	}
	cl := *f.client()
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.Download, nil)
	if err != nil {
		return h, err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); strings.HasPrefix(strings.ToLower(loc), "magnet:") {
		return newHit(h.Title, "", loc, "", h.Seeders, h.Size), nil
	}
	if resp.StatusCode != http.StatusOK {
		return h, errors.New("music: torrent indisponible")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return h, err
	}
	meta, err := newznab.ParseTorrent(data)
	if err != nil {
		return h, err
	}
	return TorrentHit{Title: h.Title, InfoHash: meta.InfoHash, Magnet: newznab.MagnetFor(meta.InfoHash, meta.Name, meta.Trackers), Seeders: h.Seeders, Size: h.Size}, nil
}
