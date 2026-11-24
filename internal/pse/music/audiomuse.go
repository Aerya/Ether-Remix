package music

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// AudioMuseConfig names an explicitly configured, trusted self-hosted service.
// AudioMuse must have indexed this Remix server using its OpenSubsonic IDs.
type AudioMuseConfig struct{ URL, Token, Server string }

func audioMuseEnv() AudioMuseConfig {
	return AudioMuseConfig{pseenv.Get("PSE_AUDIOMUSE_URL"), pseenv.Get("PSE_AUDIOMUSE_TOKEN"), pseenv.Get("PSE_AUDIOMUSE_SERVER")}
}
func (a *PlaylistAPI) audioMuseConfig() AudioMuseConfig {
	if a.AudioMuse != nil {
		return a.AudioMuse()
	}
	return audioMuseEnv()
}
func (a *PlaylistAPI) audioMuseSeed(ctx context.Context, viewer, id int64) (Track, error) {
	var raw string
	var t Track
	err := a.Sync.Store.DB.QueryRowContext(ctx, `SELECT data FROM pse_music_owned WHERE kind='track' AND item_id=? AND provider LIKE 'local:%' AND `+mediaaccess.Predicate("provider", viewer), id).Scan(&raw)
	if err != nil || json.Unmarshal([]byte(raw), &t) != nil || t.ID != id {
		return Track{}, errors.New("titre local inaccessible")
	}
	return t, nil
}
func (a *PlaylistAPI) audioMuseTracks(ctx context.Context, f MusicFlow) ([]Track, int, error) {
	seed, err := a.audioMuseSeed(ctx, f.Viewer, f.Seed)
	if err != nil {
		return nil, 0, err
	}
	cfg := a.audioMuseConfig()
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(cfg.Server) == "" {
		return nil, 0, errors.New("configuration AudioMuse invalide")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/similar_tracks"
	q := url.Values{"item_id": {trackID(seed.ID)}, "n": {strconv.Itoa(f.Size + 1)}, "server": {cfg.Server}, "eliminate_duplicates": {"true"}}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, errors.New("requête AudioMuse invalide")
	}
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("AudioMuse indisponible")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, errors.New("AudioMuse n'a pas fourni de recommandations")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, 0, errors.New("réponse AudioMuse trop grande")
	}
	var neighbors []struct {
		ID     string `json:"item_id"`
		Title  string `json:"title"`
		Artist string `json:"author"`
	}
	if json.Unmarshal(raw, &neighbors) != nil {
		return nil, 0, errors.New("réponse AudioMuse invalide")
	}
	out := []Track{}
	seen := map[int64]bool{seed.ID: true}
	missing := 0
	for _, n := range neighbors {
		if !strings.HasPrefix(n.ID, "tr-") {
			missing++
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(n.ID, "tr-"), 10, 64)
		if err != nil || id <= 0 {
			missing++
			continue
		}
		if seen[id] {
			continue
		}
		t, err := a.audioMuseSeed(ctx, f.Viewer, id)
		if err != nil || flowName(t.Title) != flowName(n.Title) || flowName(t.Artist.Name) != flowName(n.Artist) {
			missing++
			continue
		}
		seen[id] = true
		if len(out) < f.Size {
			out = append(out, t)
		}
	}
	return out, missing, nil
}
