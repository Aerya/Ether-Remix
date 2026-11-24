package peerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrProtocol = errors.New("réponse inattendue du service P2P")
var ErrMissing = errors.New("fichier absent du service P2P")

type Hit struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	User     string `json:"user,omitempty"`
	Filename string `json:"filename,omitempty"`
}
type Client struct {
	Settings Settings
	HTTP     *http.Client
	mu       sync.Mutex
	token    string
	expires  int64
}

func (c *Client) request(ctx context.Context, method, endpoint string, body, out any, auth string) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return ErrProtocol
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Settings.URL+endpoint, &buf)
	if err != nil {
		return ErrProtocol
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Settings.Kind == "slskd" {
		req.Header.Set("X-API-Key", c.Settings.Secret)
	} else if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	client := http.Client{Timeout: 20 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("service P2P injoignable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return ErrMissing
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("service P2P : HTTP %d", resp.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 {
		return ErrProtocol
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrProtocol
	}
	return nil
}
func (c *Client) call(ctx context.Context, method, endpoint string, body, out any) error {
	if c.Settings.Kind == "slskd" {
		return c.request(ctx, method, "/api/v0"+endpoint, body, out, "")
	}
	c.mu.Lock()
	if c.token == "" || time.Now().Unix()+60 >= c.expires {
		var login struct {
			Token   string `json:"token"`
			Expires int64  `json:"expires_at"`
		}
		err := c.request(ctx, "POST", "/api/v1/auth/login?include_token=true", map[string]string{"password": c.Settings.Secret}, &login, "")
		if err != nil {
			c.mu.Unlock()
			return err
		}
		if login.Token == "" {
			c.mu.Unlock()
			return ErrProtocol
		}
		c.token, c.expires = login.Token, login.Expires
	}
	token := c.token
	c.mu.Unlock()
	return c.request(ctx, method, "/api/v1"+endpoint, body, out, token)
}
func (c *Client) Test(ctx context.Context) error {
	var out map[string]any
	p := "/status"
	if c.Settings.Kind == "slskd" {
		p = "/application"
	}
	return c.call(ctx, "GET", p, nil, &out)
}

var hashRE = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var uuidRE = regexp.MustCompile(`^[a-fA-F0-9-]{36}$`)

func (c *Client) Search(ctx context.Context, q string) ([]Hit, error) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 300 {
		return nil, errors.New("recherche attendue (300 caractères maximum)")
	}
	var id string
	if c.Settings.Kind == "amule" {
		var r struct {
			ID uint64 `json:"search_id"`
		}
		if err := c.call(ctx, "POST", "/search", map[string]string{"query": q, "type": "global"}, &r); err != nil {
			return nil, err
		}
		if r.ID == 0 {
			return nil, ErrProtocol
		}
		id = strconv.FormatUint(r.ID, 10)
	} else {
		var r struct {
			ID string `json:"id"`
		}
		if err := c.call(ctx, "POST", "/searches", map[string]any{"searchText": q, "searchTimeout": 10000, "fileLimit": 200, "responseLimit": 50}, &r); err != nil {
			return nil, err
		}
		if !uuidRE.MatchString(r.ID) {
			return nil, ErrProtocol
		}
		id = r.ID
	}
	// Only this connector's search is released; other clients' searches are untouched.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		endpoint := "/search/" + id
		if c.Settings.Kind == "slskd" {
			endpoint = "/searches/" + id
		}
		_ = c.call(cleanup, "DELETE", endpoint, nil, nil)
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var hits []Hit
	for {
		var done bool
		var err error
		hits, done, err = c.results(ctx, id)
		if err != nil {
			return nil, err
		}
		if done || len(hits) >= 200 {
			return hits, nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return hits, nil
		case <-timer.C:
		}
	}
}
func (c *Client) results(ctx context.Context, id string) ([]Hit, bool, error) {
	hits := []Hit{}
	if c.Settings.Kind == "amule" {
		var r struct {
			Results []struct {
				Hash, Name string
				Size       int64 `json:"size_bytes"`
			}
			Progress struct{ State string }
		}
		if err := c.call(ctx, "GET", "/search/"+id+"/results", nil, &r); err != nil {
			return nil, false, err
		}
		for _, f := range r.Results {
			if hashRE.MatchString(f.Hash) && f.Size > 0 {
				hits = append(hits, Hit{ID: strings.ToLower(f.Hash), Name: f.Name, Size: f.Size})
			}
			if len(hits) == 200 {
				break
			}
		}
		return hits, r.Progress.State == "finished", nil
	}
	var r struct {
		IsComplete bool
		Responses  []struct {
			Username string
			Files    []struct {
				Filename string
				Size     int64
			}
		}
	}
	if err := c.call(ctx, "GET", "/searches/"+id+"?includeResponses=true", nil, &r); err != nil {
		return nil, false, err
	}
	for _, resp := range r.Responses {
		for _, f := range resp.Files {
			if f.Size <= 0 || resp.Username == "" || f.Filename == "" {
				continue
			}
			h := Hit{Name: path.Base(strings.ReplaceAll(f.Filename, `\`, "/")), Size: f.Size, User: resp.Username, Filename: f.Filename}
			h.ID = hitID(h)
			hits = append(hits, h)
			if len(hits) == 200 {
				return hits, r.IsComplete, nil
			}
		}
	}
	return hits, r.IsComplete, nil
}
func hitID(h Hit) string {
	s := Settings{Kind: h.User, URL: h.Filename, Secret: strconv.FormatInt(h.Size, 10)}
	return s.Identity()
}
