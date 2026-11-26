// Package tvdb implements the fixed-destination TheTVDB v4 API.
package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/httpclient"
	"github.com/braedonsaunders/ether/internal/retryafter"
	"golang.org/x/sync/singleflight"
)

const endpoint = "https://api4.thetvdb.com/v4"
const maxResponse = 8 << 20

// Provider bodies and transport errors can contain credentials and are never
// returned. The console translates credential errors and groups other errors.
var (
	ErrKeyRequired     = errors.New("Enter a TheTVDB API key.")
	ErrUnauthorized    = errors.New("TheTVDB rejected the API key.")
	ErrUnavailable     = errors.New("Could not connect to TheTVDB.")
	ErrInvalidResponse = errors.New("TheTVDB returned an invalid response.")
	ErrRateLimited     = errors.New("TheTVDB rate limit was reached. Try again later.")
)

type transport struct {
	http     *http.Client
	inFlight chan struct{}
	dispatch chan struct{}
	next     time.Time // guarded by dispatch
	cooldown atomic.Int64
}

var upstream = newTransport()

func newTransport() *transport {
	c := egress.Client(egress.CapMetadata, 15*time.Second)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &transport{http: c, inFlight: make(chan struct{}, 4), dispatch: make(chan struct{}, 1)}
}

func (t *transport) pace(ctx context.Context) error {
	select {
	case t.dispatch <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-t.dispatch }()
	if t.cooldown.Load() > time.Now().Unix() {
		return ErrRateLimited
	}
	if delay := time.Until(t.next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if t.cooldown.Load() > time.Now().Unix() {
		return ErrRateLimited
	}
	t.next = time.Now().Add(125 * time.Millisecond)
	return nil
}

func (t *transport) request(ctx context.Context, method, path, token string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", httpclient.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	select {
	case t.inFlight <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-t.inFlight }()
	if err := t.pace(ctx); err != nil {
		return err
	}
	res, err := t.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return core.ErrNotFound
	case http.StatusTooManyRequests:
		now := time.Now()
		delay, ok := retryafter.Parse(res.Header.Get("Retry-After"), now, 0)
		if !ok {
			delay = time.Minute
		}
		until := now.Add(max(time.Second, delay)).Unix() + 1
		for old := t.cooldown.Load(); until > old && !t.cooldown.CompareAndSwap(old, until); old = t.cooldown.Load() {
		}
		return ErrRateLimited
	default:
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse || json.Unmarshal(raw, out) != nil {
		return ErrInvalidResponse
	}
	return nil
}

func (t *transport) login(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", ErrKeyRequired
	}
	body, _ := json.Marshal(map[string]string{"apikey": key})
	var res struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
		Status string `json:"status"`
	}
	if err := t.request(ctx, http.MethodPost, "/login", "", body, &res); err != nil {
		return "", err
	}
	if res.Status != "success" || strings.TrimSpace(res.Data.Token) == "" {
		return "", ErrInvalidResponse
	}
	return res.Data.Token, nil
}

// Test verifies a key without writing it or changing catalogue membership.
func Test(ctx context.Context, key string) error {
	_, err := upstream.login(ctx, strings.TrimSpace(key))
	return err
}

type bearer struct {
	key, token string
	expires    time.Time
}

// Client implements the TheTVDB metadata source. Construction performs no I/O.
type Client struct {
	settings  func() (key, language string)
	cacheDir  string
	transport *transport

	authMu sync.Mutex
	auth   bearer
	login  singleflight.Group
	fetch  singleflight.Group

	changesMu sync.Mutex
	snapshots map[string]changeSnapshot
}

// NewSource reads the current owner's settings on use; rotating a key takes
// effect on the next call without rebuilding the source.
func NewSource(cacheDir string, settings func() (key, language string)) *Client {
	return &Client{settings: settings, cacheDir: cacheDir, transport: upstream, snapshots: make(map[string]changeSnapshot)}
}

func (c *Client) key() string {
	key, _ := c.settings()
	return strings.TrimSpace(key)
}

func (c *Client) token(ctx context.Context, key string) (bearer, error) {
	read := func() (bearer, bool) {
		c.authMu.Lock()
		defer c.authMu.Unlock()
		return c.auth, c.auth.key == key && time.Now().Before(c.auth.expires)
	}
	if token, ok := read(); ok {
		return token, nil
	}
	result, err := waitFlight(ctx, &c.login, key, func() (any, error) {
		if token, ok := read(); ok {
			return token, nil
		}
		token, err := c.transport.login(ctx, key)
		if err != nil {
			return nil, err
		}
		// v4 tokens are valid for one month. Use a shorter fixed lifetime.
		auth := bearer{key: key, token: token, expires: time.Now().Add(28 * 24 * time.Hour)}
		c.authMu.Lock()
		c.auth = auth
		c.authMu.Unlock()
		return auth, nil
	})
	if err != nil {
		return bearer{}, err
	}
	return result.(bearer), nil
}

type pageLinks struct {
	Next  string `json:"next"`
	Total *int   `json:"total_items"`
}

type response struct {
	Data   json.RawMessage `json:"data"`
	Status string          `json:"status"`
	Links  pageLinks       `json:"links"`
}

func (c *Client) fetchResponse(ctx context.Context, path string) (response, error) {
	var res response
	key := c.key()
	auth, err := c.token(ctx, key)
	if err != nil {
		return res, err
	}
	err = c.transport.request(ctx, http.MethodGet, path, auth.token, nil, &res)
	if errors.Is(err, ErrUnauthorized) {
		c.authMu.Lock()
		if c.auth == auth {
			c.auth = bearer{}
		}
		c.authMu.Unlock()
		if auth, err = c.token(ctx, key); err == nil {
			res = response{}
			err = c.transport.request(ctx, http.MethodGet, path, auth.token, nil, &res)
		}
	}
	if err == nil && (res.Status != "success" || len(res.Data) == 0) {
		err = ErrInvalidResponse
	}
	return res, err
}

func waitFlight(ctx context.Context, group *singleflight.Group, key string, fn func() (any, error)) (any, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-group.DoChan(key, fn):
			// A canceled leader must not fail callers that still need the result.
			if result.Shared && (errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded)) && ctx.Err() == nil {
				continue
			}
			return result.Val, result.Err
		}
	}
}
