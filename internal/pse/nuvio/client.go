// Package nuvio relie un spectateur d'Ether-Remix à un profil Nuvio : reprise
// et historique synchronisés dans les deux sens, et installation du lien
// Stremio du spectateur dans le profil. Ajout PSE : voir docs/pse/FORK.fr.md.
package nuvio

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

const (
	defaultBase     = "https://api.nuvio.tv"
	defaultFallback = "https://api-two.nuvioapp.space"
	tvLoginBase     = "https://nuvio.tv/tv-login"
	deviceName      = "Ether Remix"
	// OriginSurface marque les écritures venues de Nuvio : le push ne les
	// renvoie pas.
	OriginSurface = "nuvio"
	// originClientID identifie Ether auprès de Nuvio (p_origin_client_id).
	originClientID = "ether-remix"
)

// Settings sont les réglages PSE de la fonction.
type Settings struct {
	Interval time.Duration
	AnonKey  string
}

// LoadSettings lit PSE_NUVIO_INTERVAL (5 min par défaut, 1 min au minimum) et
// PSE_NUVIO_ANON_KEY.
func LoadSettings() Settings {
	s := Settings{Interval: 5 * time.Minute, AnonKey: defaultAnonKey}
	if d, err := time.ParseDuration(strings.TrimSpace(pseenv.Get("PSE_NUVIO_INTERVAL"))); err == nil && d > 0 {
		s.Interval = max(d, time.Minute)
	}
	if k := strings.TrimSpace(pseenv.Get("PSE_NUVIO_ANON_KEY")); k != "" {
		s.AnonKey = k
	}
	return s
}

// ErrUnauthorized : jeton refusé (401).
var ErrUnauthorized = errors.New("nuvio: jeton refusé")

// StatusError est une réponse HTTP d'erreur de Nuvio.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("nuvio: HTTP %d %s", e.Code, e.Body) }

// Client appelle l'API Nuvio (Supabase) avec un hôte de repli.
type Client struct {
	HTTP     *http.Client
	Bases    []string
	Settings func() Settings
}

// NewClient crée un client vers l'hôte public de Nuvio et son repli.
func NewClient(h *http.Client) *Client {
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{HTTP: h, Bases: []string{defaultBase, defaultFallback}, Settings: LoadSettings}
}

func (c *Client) anon() string {
	if c.Settings != nil {
		return c.Settings().AnonKey
	}
	return defaultAnonKey
}

func retryable(code int) bool { return code == 502 || code == 503 || code == 504 }

// do envoie une requête JSON ; bearer vide = clé anon. Sur erreur réseau ou
// 502/503/504, l'hôte de repli est essayé.
func (c *Client) do(ctx context.Context, method, path, bearer string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	if bearer == "" {
		bearer = c.anon()
	}
	var lastErr error
	for _, base := range c.Bases {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("apikey", c.anon())
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if retryable(resp.StatusCode) {
			lastErr = &StatusError{Code: resp.StatusCode}
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return ErrUnauthorized
		}
		if resp.StatusCode/100 != 2 {
			msg := string(raw)
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return &StatusError{Code: resp.StatusCode, Body: msg}
		}
		if out == nil || len(bytes.TrimSpace(raw)) == 0 {
			return nil
		}
		return json.Unmarshal(raw, out)
	}
	return lastErr
}

// rpc appelle /rest/v1/rpc/<name>.
func (c *Client) rpc(ctx context.Context, token, name string, args, out any) error {
	if args == nil {
		args = map[string]any{}
	}
	return c.do(ctx, http.MethodPost, "/rest/v1/rpc/"+name, token, args, out)
}

// Tokens est un couple de jetons Nuvio.
type Tokens struct {
	Access    string
	Refresh   string
	ExpiresAt time.Time
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (r tokenResp) tokens(now time.Time) (Tokens, error) {
	if r.AccessToken == "" || r.RefreshToken == "" {
		return Tokens{}, errors.New("nuvio: réponse de jetons incomplète")
	}
	ttl := time.Duration(r.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	return Tokens{Access: r.AccessToken, Refresh: r.RefreshToken, ExpiresAt: now.Add(ttl)}, nil
}

// Refresh échange un jeton de rafraîchissement (à usage unique).
func (c *Client) Refresh(ctx context.Context, refresh string, now time.Time) (Tokens, error) {
	var r tokenResp
	if err := c.do(ctx, http.MethodPost, "/auth/v1/token?grant_type=refresh_token", "", map[string]string{"refresh_token": refresh}, &r); err != nil {
		return Tokens{}, err
	}
	return r.tokens(now)
}

// TVLogin est une session de connexion TV en cours.
type TVLogin struct {
	Code         string    `json:"code"`
	WebURL       string    `json:"webUrl"`
	ExpiresAt    time.Time `json:"expiresAt"`
	PollInterval int       `json:"pollInterval"`
	nonce        string
}

type tvStart struct {
	Code         string `json:"code"`
	WebURL       string `json:"web_url"`
	ExpiresAt    string `json:"expires_at"`
	PollInterval int    `json:"poll_interval_seconds"`
}

// StartTVLogin ouvre une session de connexion TV.
func (c *Client) StartTVLogin(ctx context.Context, now time.Time) (TVLogin, error) {
	var nb [32]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return TVLogin{}, err
	}
	nonce := hex.EncodeToString(nb[:])
	raw := json.RawMessage{}
	err := c.rpc(ctx, "", "start_tv_login_session", map[string]string{
		"p_device_nonce": nonce, "p_redirect_base_url": tvLoginBase, "p_device_name": deviceName,
	}, &raw)
	if err != nil {
		return TVLogin{}, err
	}
	var s tvStart
	if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '[' {
		var arr []tvStart
		if err := json.Unmarshal(t, &arr); err != nil || len(arr) == 0 {
			return TVLogin{}, errors.New("nuvio: session de connexion illisible")
		}
		s = arr[0]
	} else if err := json.Unmarshal(t, &s); err != nil {
		return TVLogin{}, err
	}
	if s.Code == "" {
		return TVLogin{}, errors.New("nuvio: code de connexion absent")
	}
	exp, err := time.Parse(time.RFC3339, s.ExpiresAt)
	if err != nil {
		exp = now.Add(10 * time.Minute)
	}
	return TVLogin{Code: s.Code, WebURL: s.WebURL, ExpiresAt: exp, PollInterval: max(s.PollInterval, 2), nonce: nonce}, nil
}

// PollTVLogin renvoie l'état (pending, approved, expired, used, cancelled).
func (c *Client) PollTVLogin(ctx context.Context, l TVLogin) (string, error) {
	var raw json.RawMessage
	if err := c.rpc(ctx, "", "poll_tv_login_session", map[string]string{"p_code": l.Code, "p_device_nonce": l.nonce}, &raw); err != nil {
		return "", err
	}
	var s struct {
		Status string `json:"status"`
	}
	if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '[' {
		var arr []struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(t, &arr) == nil && len(arr) > 0 {
			s.Status = arr[0].Status
		}
	} else {
		_ = json.Unmarshal(t, &s)
	}
	return s.Status, nil
}

// ExchangeTVLogin échange une session approuvée contre des jetons.
func (c *Client) ExchangeTVLogin(ctx context.Context, l TVLogin, now time.Time) (Tokens, error) {
	var r tokenResp
	if err := c.do(ctx, http.MethodPost, "/functions/v1/tv-logins-exchange", "", map[string]string{"code": l.Code, "device_nonce": l.nonce}, &r); err != nil {
		return Tokens{}, err
	}
	return r.tokens(now)
}

// Profile est un profil Nuvio.
type Profile struct {
	ID    string `json:"id"`
	Index int    `json:"profileIndex"`
	Name  string `json:"name"`
}

// Profiles liste les profils du compte.
func (c *Client) Profiles(ctx context.Context, token string) ([]Profile, error) {
	var rows []struct {
		ID    string `json:"id"`
		Index int    `json:"profile_index"`
		Name  string `json:"name"`
	}
	if err := c.rpc(ctx, token, "sync_pull_profiles", nil, &rows); err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(rows))
	for _, r := range rows {
		out = append(out, Profile{ID: r.ID, Index: r.Index, Name: r.Name})
	}
	return out, nil
}

// ProgressEntry est une progression Nuvio (millisecondes).
type ProgressEntry struct {
	ContentID   string `json:"content_id"`
	ContentType string `json:"content_type"`
	VideoID     string `json:"video_id"`
	Season      *int   `json:"season"`
	Episode     *int   `json:"episode"`
	Position    int64  `json:"position"`
	Duration    int64  `json:"duration"`
	LastWatched int64  `json:"last_watched"`
	ProgressKey string `json:"progress_key"`
}

// WatchedEntry est un élément vu de Nuvio.
type WatchedEntry struct {
	ContentID   string `json:"content_id"`
	ContentType string `json:"content_type"`
	Title       string `json:"title"`
	Season      *int   `json:"season"`
	Episode     *int   `json:"episode"`
	WatchedAt   int64  `json:"watched_at"`
}

// PullProgress lit les progressions modifiées après since (0 : toutes).
func (c *Client) PullProgress(ctx context.Context, token string, profile int, since int64) ([]ProgressEntry, error) {
	args := map[string]any{"p_profile_id": profile}
	if since > 0 {
		args["p_since_last_watched"] = since
	}
	var out []ProgressEntry
	return out, c.rpc(ctx, token, "sync_pull_watch_progress", args, &out)
}

const watchedPageSize = 900

// PullWatched lit tous les éléments vus, page par page.
func (c *Client) PullWatched(ctx context.Context, token string, profile int) ([]WatchedEntry, error) {
	var all []WatchedEntry
	for page := 1; page <= 200; page++ {
		var rows []WatchedEntry
		err := c.rpc(ctx, token, "sync_pull_watched_items", map[string]any{"p_profile_id": profile, "p_page": page, "p_page_size": watchedPageSize}, &rows)
		if err != nil {
			return all, err
		}
		all = append(all, rows...)
		if len(rows) < watchedPageSize {
			break
		}
	}
	return all, nil
}

// PushProgress envoie des progressions (entrées au format de sync_push_watch_progress).
func (c *Client) PushProgress(ctx context.Context, token string, profile int, entries []map[string]any) error {
	return c.rpc(ctx, token, "sync_push_watch_progress", map[string]any{"p_entries": entries, "p_profile_id": profile, "p_origin_client_id": originClientID}, nil)
}

// PushWatched envoie des éléments vus.
func (c *Client) PushWatched(ctx context.Context, token string, profile int, items []map[string]any) error {
	return c.rpc(ctx, token, "sync_push_watched_items", map[string]any{"p_items": items, "p_profile_id": profile, "p_origin_client_id": originClientID}, nil)
}

// Addons lit les addons d'un profil, champs inconnus compris : les entrées
// des autres applications sont renvoyées telles quelles.
func (c *Client) Addons(ctx context.Context, token string, profile int) ([]map[string]any, error) {
	var out []map[string]any
	return out, c.do(ctx, http.MethodGet, fmt.Sprintf("/rest/v1/addons?select=*&profile_id=eq.%d&order=sort_order.asc", profile), token, nil, &out)
}

// PushAddons remplace la liste d'addons d'un profil.
func (c *Client) PushAddons(ctx context.Context, token string, profile int, addons []map[string]any) error {
	return c.rpc(ctx, token, "sync_push_addons", map[string]any{"p_addons": addons, "p_profile_id": profile, "p_origin_client_id": originClientID}, nil)
}
