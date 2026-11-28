package alko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Erreurs typées : aucun message ne contient l'adresse, la clé ni un lien.
var (
	ErrKey         = errors.New("clé de lecture refusée par StreamFusion")
	ErrDisabled    = errors.New("API Alkodiques absente : StreamFusion trop ancien ou lecture désactivée")
	ErrFullReread  = errors.New("relecture complète nécessaire")
	ErrViews       = errors.New("vues Alkodiques indisponibles côté StreamFusion")
	ErrParams      = errors.New("paramètres refusés par StreamFusion")
	ErrProtocol    = errors.New("réponse inattendue de StreamFusion")
	ErrUnreachable = errors.New("StreamFusion injoignable")
)

// Limites : la lecture complète reste bornée en mémoire, page par page.
const (
	// PageSize est la taille des pages du catalogue (le maximum de l'API est 2000).
	PageSize = 1000
	// ModificationsPage est la taille des pages du journal.
	ModificationsPage = 5000
	// FilesBatch est le maximum d'identifiants par relecture (limite de l'API).
	FilesBatch = 1000

	maxCatalogBody = 48 << 20
	maxSmallBody   = 16 << 20
)

// État est le cliché que l'API joint à chaque réponse.
type State struct {
	Epoch      int64 `json:"epoque"`
	PurgeSeq   int64 `json:"purge_seq"`
	JournalSeq int64 `json:"journal_seq"`
	LastSeq    int64 `json:"last_seq"`
	Journal    bool  `json:"journal_actif"`
}

// JournalValid : le journal ne vaut que s'il est actif et à jour (même cliché).
func (s State) JournalValid() bool { return s.Journal && s.JournalSeq == s.LastSeq }

// Element est un fichier vérifié. SourceURL est privé : jamais journalisé.
type Element struct {
	PlaybackID  string  `json:"playback_id"`
	Kind        string  `json:"kind"`
	TMDB        int64   `json:"tmdb"`
	Season      *int    `json:"season"`
	Episode     *int    `json:"episode"`
	Filename    string  `json:"filename"`
	Filesize    int64   `json:"filesize"`
	MediaInfo   *string `json:"media_info"`
	Fingerprint string  `json:"fingerprint"`
	SyncedAt    string  `json:"synced_at"`
	SourceURL   string  `json:"source_url"`
}

// Modification est une ligne du journal.
type Modification struct {
	Seq        int64  `json:"seq"`
	PlaybackID string `json:"playback_id"`
	Operation  string `json:"operation"`
	Kind       string `json:"kind"`
	TMDB       int64  `json:"tmdb"`
}

// CataloguePage est une page du catalogue ; Next vide à la fin.
type CataloguePage struct {
	State    State     `json:"etat"`
	Elements []Element `json:"elements"`
	Next     *string   `json:"suivant"`
}

// ModificationsPage est une page du journal ; Next nil à la fin.
type ModificationsResult struct {
	State         State          `json:"etat"`
	Modifications []Modification `json:"modifications"`
	Next          *int64         `json:"suivant"`
}

type filesResult struct {
	State    State     `json:"etat"`
	Elements []Element `json:"elements"`
}

// Client lit l'API de lecture d'SFR.
type Client struct {
	Settings Settings
	// HTTP remplace le client par défaut (tests).
	HTTP *http.Client

	once sync.Once
	hc   *http.Client
}

func defaultHTTP() *http.Client {
	return &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			// Adresse interne (réseau Docker) : jamais de proxy d'environnement.
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConns:          2,
			IdleConnTimeout:       60 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// http construit le client et son transport une seule fois : les connexions
// sont réutilisées d'une requête à l'autre.
func (c *Client) http() *http.Client {
	c.once.Do(func() {
		if c.HTTP != nil {
			cp := *c.HTTP
			cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			c.hc = &cp
			return
		}
		c.hc = defaultHTTP()
	})
	return c.hc
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, limit int64) error {
	target := c.Settings.URL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return ErrProtocol
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return ErrProtocol
	}
	req.Header.Set("alko-key", c.Settings.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnreachable // l'erreur de transport contient l'adresse : jamais relayée
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return ErrKey
	case resp.StatusCode == http.StatusNotFound:
		return ErrDisabled
	case resp.StatusCode == http.StatusGone:
		return ErrFullReread
	case resp.StatusCode == http.StatusServiceUnavailable:
		return ErrViews
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return ErrParams
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return ErrProtocol
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, limit+1))
	if err := dec.Decode(out); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrProtocol
	}
	return nil
}

// State lit GET /api/alko/v1/etat.
func (c *Client) State(ctx context.Context) (State, error) {
	var st State
	err := c.do(ctx, http.MethodGet, "/api/alko/v1/etat", nil, nil, &st, maxSmallBody)
	return st, err
}

// Catalogue lit une page de GET /api/alko/v1/catalogue.
func (c *Client) Catalogue(ctx context.Context, after string, limit int) (CataloguePage, error) {
	q := url.Values{"limite": {strconv.Itoa(limit)}}
	if after != "" {
		q.Set("apres", after)
	}
	var out CataloguePage
	err := c.do(ctx, http.MethodGet, "/api/alko/v1/catalogue", q, nil, &out, maxCatalogBody)
	return out, err
}

// Modifications lit une page de GET /api/alko/v1/modifications (seq > since).
func (c *Client) Modifications(ctx context.Context, since int64, limit int) (ModificationsResult, error) {
	q := url.Values{"depuis": {strconv.FormatInt(since, 10)}, "limite": {strconv.Itoa(limit)}}
	var out ModificationsResult
	err := c.do(ctx, http.MethodGet, "/api/alko/v1/modifications", q, nil, &out, maxSmallBody)
	return out, err
}

// Files relit des fichiers par identifiant (POST /api/alko/v1/fichiers). Un
// identifiant absent de la réponse est un fichier supprimé.
func (c *Client) Files(ctx context.Context, ids []string) (State, []Element, error) {
	if len(ids) == 0 {
		return State{}, nil, nil
	}
	if len(ids) > FilesBatch {
		return State{}, nil, ErrParams
	}
	var out filesResult
	err := c.do(ctx, http.MethodPost, "/api/alko/v1/fichiers", nil, map[string]any{"playback_ids": ids}, &out, maxCatalogBody)
	return out.State, out.Elements, err
}
