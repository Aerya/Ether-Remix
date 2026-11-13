package music

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_music_spotify", 1, `CREATE TABLE pse_music_spotify(viewer_id INTEGER PRIMARY KEY, credentials TEXT NOT NULL);`)
	store.RegisterViewerTable("pse_music_spotify", "pse_music_spotify", "viewer_id", store.ViewerMergeMove)
}

type spotifyCredentials struct {
	ClientID  string `json:"clientId"`
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int    `json:"expires_in"`
	ExpiresAt int64  `json:"expiresAt"`
}
type spotifyPending struct {
	Viewer                     int64
	Client, Redirect, Verifier string
	Until                      time.Time
}

// SpotifyPlaylists keeps tokens sealed and never returns them to the console.
// Network endpoints are injectable only in Go tests, never through the API.
type SpotifyPlaylists struct {
	Store         *Playlists
	Sealer        Sealer
	HTTP          *http.Client
	API, TokenURL string
	mu            sync.Mutex
	pending       map[string]spotifyPending
}

func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

var spotifyID = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)
var spotifyClientID = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

func (s *SpotifyPlaylists) begin(viewer int64, client, redirect string) (string, error) {
	u, err := url.Parse(redirect)
	if err != nil || u.User != nil || u.RawQuery != "content=musique" || u.Fragment != "" || (u.Path != "/ether/remix/contenus" && u.Path != "/ether/accent/contenus") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "[::1]" || u.Hostname() == "::1"))) || u.Host == "" || !spotifyClientID.MatchString(client) {
		return "", errors.New("Client ID ou URI de retour Spotify invalide (HTTPS requis hors boucle locale)")
	}
	if s.Sealer == nil {
		return "", errors.New("chiffrement Spotify indisponible")
	}
	state, verifier := randomToken(), randomToken()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]spotifyPending{}
	}
	for k, p := range s.pending {
		if time.Now().After(p.Until) || p.Viewer == viewer {
			delete(s.pending, k)
		}
	}
	if len(s.pending) >= 100 {
		return "", errors.New("trop de connexions Spotify en attente")
	}
	s.pending[state] = spotifyPending{viewer, client, redirect, verifier, time.Now().Add(10 * time.Minute)}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {client}, "response_type": {"code"}, "redirect_uri": {redirect}, "state": {state}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "scope": {"playlist-read-private playlist-read-collaborative"}}
	return "https://accounts.spotify.com/authorize?" + q.Encode(), nil
}
func (s *SpotifyPlaylists) client() *http.Client {
	c := s.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	copy := *c
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}
func (s *SpotifyPlaylists) token(ctx context.Context, q url.Values) (spotifyCredentials, error) {
	endpoint := s.TokenURL
	if endpoint == "" {
		endpoint = "https://accounts.spotify.com/api/token"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(q.Encode()))
	if err != nil {
		return spotifyCredentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.client().Do(req)
	if err != nil {
		return spotifyCredentials{}, errors.New("Spotify injoignable")
	}
	defer res.Body.Close()
	var c spotifyCredentials
	if res.StatusCode != 200 {
		return c, fmt.Errorf("connexion Spotify refusée (%d), reconnectez le compte", res.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&c)
	if err != nil || c.Access == "" {
		return c, errors.New("réponse Spotify invalide")
	}
	c.ExpiresAt = time.Now().Add(time.Duration(c.ExpiresIn) * time.Second).Unix()
	return c, nil
}
func (s *SpotifyPlaylists) save(ctx context.Context, viewer int64, c spotifyCredentials) error {
	if s.Sealer == nil {
		return errors.New("chiffrement Spotify indisponible")
	}
	raw, _ := json.Marshal(c)
	sealed, err := s.Sealer.SealField(string(raw), "pse-spotify", strconv.FormatInt(viewer, 10))
	if err != nil {
		return err
	}
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO pse_music_spotify(viewer_id,credentials) VALUES(?,?) ON CONFLICT(viewer_id) DO UPDATE SET credentials=excluded.credentials`, viewer, sealed)
	return err
}
func (s *SpotifyPlaylists) finish(ctx context.Context, viewer int64, state, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[state]
	if !ok || p.Viewer != viewer || time.Now().After(p.Until) {
		return errors.New("connexion Spotify expirée ou état invalide ; recommencez")
	}
	delete(s.pending, state)
	c, err := s.token(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {p.Client}, "redirect_uri": {p.Redirect}, "code_verifier": {p.Verifier}, "code": {code}})
	if err != nil {
		return err
	}
	c.ClientID = p.Client
	return s.save(ctx, viewer, c)
}
func (s *SpotifyPlaylists) access(ctx context.Context, viewer int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Sealer == nil {
		return "", errors.New("chiffrement Spotify indisponible")
	}
	var raw string
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT credentials FROM pse_music_spotify WHERE viewer_id=?`, viewer).Scan(&raw); err != nil {
		return "", errors.New("connectez le compte Spotify de ce spectateur")
	}
	plain, _, err := s.Sealer.OpenField(raw, "pse-spotify", strconv.FormatInt(viewer, 10))
	if err != nil {
		return "", errors.New("connexion Spotify illisible ; reconnectez le compte")
	}
	var c spotifyCredentials
	if json.Unmarshal([]byte(plain), &c) != nil {
		return "", errors.New("connexion Spotify invalide")
	}
	if c.ExpiresAt <= time.Now().Add(time.Minute).Unix() {
		next, err := s.token(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {c.Refresh}})
		if err != nil {
			return "", err
		}
		next.ClientID = c.ClientID
		if next.Refresh == "" {
			next.Refresh = c.Refresh
		}
		c = next
		if err = s.save(ctx, viewer, c); err != nil {
			return "", err
		}
	}
	return c.Access, nil
}
func (s *SpotifyPlaylists) get(ctx context.Context, viewer int64, path string, into any) error {
	token, err := s.access(ctx, viewer)
	if err != nil {
		return err
	}
	base := s.API
	if base == "" {
		base = "https://api.spotify.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := s.client().Do(req)
	if err != nil {
		return errors.New("Spotify injoignable")
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case 200:
	case 403:
		return errors.New("Spotify refuse cette playlist : en mode développement, seuls les titres de vos playlists ou des playlists collaboratives sont accessibles")
	case 429:
		return errors.New("quota Spotify atteint ; réessayez plus tard")
	case 401:
		return errors.New("session Spotify expirée ; reconnectez le compte")
	default:
		return fmt.Errorf("Spotify : réponse %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(into)
}
func (s *SpotifyPlaylists) lists(ctx context.Context, viewer int64, offset int) ([]playlistChoice, bool, error) {
	var r struct {
		Items []struct{ ID, Name string }
		Next  string
	}
	err := s.get(ctx, viewer, fmt.Sprintf("/me/playlists?limit=20&offset=%d", offset), &r)
	out := []playlistChoice{}
	for _, p := range r.Items {
		if spotifyID.MatchString(p.ID) {
			out = append(out, playlistChoice{Name: p.Name, URL: "https://open.spotify.com/playlist/" + p.ID})
		}
	}
	return out, r.Next != "", err
}
func (s *SpotifyPlaylists) preview(ctx context.Context, viewer int64, id string) (playlistPreview, error) {
	p := playlistPreview{Source: "spotify", URL: "https://open.spotify.com/playlist/" + id, Tracks: []playlistEntry{}}
	var meta struct{ Name string }
	if err := s.get(ctx, viewer, "/playlists/"+id, &meta); err != nil {
		return p, err
	}
	p.Name = meta.Name
	type track struct {
		Name, Type string
		Artists    []struct{ Name string }
	}
	for offset := 0; offset < MaxUserTracks; offset += 50 {
		var r struct {
			Items []struct {
				Item    *track
				Track   *track
				IsLocal bool `json:"is_local"`
			}
			Next  string
			Total int
		}
		if err := s.get(ctx, viewer, fmt.Sprintf("/playlists/%s/items?limit=50&offset=%d", id, offset), &r); err != nil {
			return p, err
		}
		p.Total = r.Total
		for _, item := range r.Items {
			t := item.Item
			if t == nil {
				t = item.Track
			}
			if t == nil || item.IsLocal || t.Type != "track" || len(t.Artists) == 0 {
				p.Skipped++
				continue
			}
			p.Tracks = append(p.Tracks, playlistEntry{Title: t.Name, Artist: t.Artists[0].Name})
		}
		if r.Next == "" || len(r.Items) == 0 {
			return p, nil
		}
	}
	p.Truncated = true
	return p, nil
}
