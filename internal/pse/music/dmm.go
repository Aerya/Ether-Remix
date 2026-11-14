package music

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DMMSource = "dmm"

// DMMFinder ne consulte que le catalogue public : aucun identifiant Real-Debrid
// n'est envoyé à DMM. La résolution des magnets passe directement par le compte.
type DMMFinder struct {
	BaseURL string
	HTTP    *http.Client
	Log     *slog.Logger
}

func (f *DMMFinder) FindAlbum(ctx context.Context, query string) ([]TorrentHit, error) {
	return f.albums(ctx, "search", url.Values{"q": {query}, "limit": {"50"}})
}

// SampleAlbums échantillonne le catalogue DMM lui-même : les nouveautés
// Deezer ne sont pas un test représentatif des albums disponibles sur DMM.
func (f *DMMFinder) SampleAlbums(ctx context.Context) ([]TorrentHit, error) {
	return f.albums(ctx, "library", url.Values{"limit": {"5"}, "summary": {"1"}})
}

func (f *DMMFinder) albums(ctx context.Context, endpoint string, query url.Values) (hits []TorrentHit, err error) {
	started := time.Now()
	status := 0
	defer func() {
		if f.Log != nil {
			attrs := []any{"component", "pse-music", "source", "dmm", "stage", endpoint, "http_status", status, "albums", len(hits), "duration", time.Since(started).Round(time.Millisecond)}
			if err != nil {
				f.Log.Warn("music catalogue request failed", attrs...)
			} else {
				f.Log.Info("music catalogue request completed", attrs...)
			}
		}
	}()
	client := f.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	base := strings.TrimRight(strings.TrimSpace(f.BaseURL), "/")
	if base == "" {
		base = "https://debridmediamanager.com"
	}
	u, parseErr := url.Parse(base)
	if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("musique : adresse de l'instance DMM invalide")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/music/"+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("musique : catalogue DMM injoignable")
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("musique : catalogue DMM indisponible (HTTP %d)", resp.StatusCode)
	}
	const limit = 2 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(body) > limit {
		return nil, errors.New("musique : réponse DMM illisible")
	}
	var data struct {
		Albums *[]struct {
			Hash, Artist, Album string
			TotalBytes          int64
		}
	}
	if json.Unmarshal(body, &data) != nil || data.Albums == nil {
		return nil, errors.New("musique : réponse DMM invalide")
	}
	hits = make([]TorrentHit, 0, len(*data.Albums))
	seen := map[string]bool{}
	for _, a := range *data.Albums {
		hash := strings.ToLower(strings.TrimSpace(a.Hash))
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 20 || seen[hash] || strings.TrimSpace(a.Artist) == "" || strings.TrimSpace(a.Album) == "" {
			continue
		}
		seen[hash] = true
		hits = append(hits, TorrentHit{Title: a.Artist + " - " + a.Album, InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash, Size: max(0, a.TotalBytes)})
	}
	return hits, nil
}

func (f *DMMFinder) Magnet(_ context.Context, h TorrentHit) (TorrentHit, error) { return h, nil }
