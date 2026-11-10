package torrentclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CrossSeed prévient cross-seed (v6) qu'un torrent est complet : il cherche
// alors le même contenu sur les autres trackers (POST /api/webhook).
type CrossSeed struct {
	endpoint string
	key      string
	hc       *http.Client
}

// NewCrossSeed prépare l'appel ; raw est l'adresse de cross-seed
// (http://cross-seed:2468), key sa clé d'API.
func NewCrossSeed(raw, key string, hc *http.Client) (*CrossSeed, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("torrentclient: adresse cross-seed http(s)://… attendue")
	}
	u.RawQuery, u.Fragment = "", ""
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api/webhook") {
		u.Path = strings.TrimRight(u.Path, "/") + "/api/webhook"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &CrossSeed{endpoint: u.String(), key: strings.TrimSpace(key), hc: hc}, nil
}

// Notify demande la recherche du torrent hash.
func (c *CrossSeed) Notify(ctx context.Context, hash string) error {
	form := url.Values{"infoHash": {normHash(hash)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.key != "" {
		req.Header.Set("X-Api-Key", c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("torrentclient: cross-seed injoignable")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("torrentclient: clé cross-seed refusée")
	case resp.StatusCode >= 300:
		return fmt.Errorf("torrentclient: cross-seed a répondu HTTP %d", resp.StatusCode)
	}
	return nil
}
