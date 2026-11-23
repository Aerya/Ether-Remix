package plextv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CommunityClient reads only data visible to the owner's Plex account.
// A single client serializes requests, including concurrent explicit syncs,
// and applies Pulsarr-style 429 cooldowns and retries.
type CommunityClient struct {
	Client                *http.Client
	URL                   string
	MinInterval           time.Duration
	MaxRetries            int // default 3 when 0; negative disables retries
	mu                    sync.Mutex
	last                  time.Time
	retryAfter            time.Time
	consecutiveRateLimits int
	lastRateLimit         time.Time
}
type CommunityFriend struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
}
type CommunityItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}
type CommunityPage struct {
	Nodes    []CommunityItem `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

func (c *CommunityClient) reserveSlot(ctx context.Context) error {
	var slotJitter time.Duration
	if c.MinInterval <= 0 {
		slotJitter = time.Duration(rand.Int64N(int64(500 * time.Millisecond)))
	}
	for {
		c.mu.Lock()
		minInterval := c.MinInterval
		if minInterval <= 0 {
			minInterval = 2*time.Second + slotJitter
		}
		until := c.last.Add(minInterval)
		if c.retryAfter.After(until) {
			until = c.retryAfter
		}
		delay := time.Until(until)
		if delay <= 0 {
			if err := ctx.Err(); err != nil {
				c.mu.Unlock()
				return err
			}
			c.last = time.Now()
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *CommunityClient) recordRateLimit(retryAfterHeader string) {
	const (
		baseMultiplier = 2.0
		maxBackoff     = 30 * time.Second
		minCooldown    = 100 * time.Millisecond
		maxRetryAfter  = 300 * time.Second
	)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.lastRateLimit) < 45*time.Second {
		c.consecutiveRateLimits++
	} else {
		c.consecutiveRateLimits = 1
	}
	c.lastRateLimit = now

	var cooldown time.Duration
	if secs, ok := parseCommunityRetryAfterSeconds(retryAfterHeader, now); ok {
		cooldown = time.Duration(secs * float64(time.Second))
		// Never fire earlier than an explicit Retry-After: add +0..10% positive jitter.
		jitter := float64(cooldown) * 0.1
		cooldown += time.Duration(rand.Float64() * jitter)
		if cooldown > maxRetryAfter {
			cooldown = maxRetryAfter
		}
	} else {
		backoff := baseMultiplier * math.Pow(1.5, float64(c.consecutiveRateLimits-1))
		if backoff > maxBackoff.Seconds() {
			backoff = maxBackoff.Seconds()
		}
		cooldown = time.Duration(backoff * float64(time.Second))
		jitter := float64(cooldown) * 0.1
		cooldown += time.Duration(rand.Float64()*jitter*2 - jitter)
		if cooldown > maxBackoff {
			cooldown = maxBackoff
		}
	}
	if cooldown < minCooldown {
		cooldown = minCooldown
	}
	end := now.Add(cooldown)
	if end.After(c.retryAfter) {
		c.retryAfter = end
	}
}

func parseCommunityRetryAfterSeconds(raw string, now time.Time) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs <= 0 {
			return 0.1, true
		}
		if secs > 300 {
			return 300, true
		}
		return float64(secs), true
	}
	if t, err := http.ParseTime(raw); err == nil {
		d := t.Sub(now).Seconds()
		if d <= 0 {
			return 0.1, true
		}
		if d > 300 {
			return 300, true
		}
		return d, true
	}
	return 0, false
}

// Wait reserves a request slot for GUID enrichment during a community sync.
func (c *CommunityClient) Wait(ctx context.Context) error {
	return c.reserveSlot(ctx)
}

func (c *CommunityClient) maxRetries() int {
	if c.MaxRetries < 0 {
		return 0
	}
	if c.MaxRetries == 0 {
		return 3
	}
	return c.MaxRetries
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *CommunityClient) query(ctx context.Context, token, query string, variables any, out any) error {
	if token == "" {
		return errors.New("Plex community requires owner authorization")
	}
	raw, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	endpoint := c.URL
	if endpoint == "" {
		endpoint = "https://community.plex.tv/api"
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}

	maxRetries := c.maxRetries()
	for attempt := 0; ; attempt++ {
		if err := c.reserveSlot(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			return errors.New("Plex community URL invalid")
		}
		req.Header.Set("X-Plex-Token", token)
		req.Header.Set("Content-Type", "application/json")

		c.mu.Lock()
		c.last = time.Now()
		c.mu.Unlock()

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < maxRetries {
				delay := min(time.Second<<attempt, 10*time.Second)
				if c.MinInterval > 0 && c.MinInterval < delay {
					delay = c.MinInterval
				}
				if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return errors.New("Plex community request failed")
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			ra := resp.Header.Get("Retry-After")
			resp.Body.Close()
			c.recordRateLimit(ra)
			if attempt < maxRetries {
				continue
			}
			return fmt.Errorf("Plex community: HTTP 429: %w", ErrRateLimited)
		}
		if (resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout) && attempt < maxRetries {
			resp.Body.Close()
			delay := min(time.Second<<attempt, 10*time.Second)
			if c.MinInterval > 0 && c.MinInterval < delay {
				delay = c.MinInterval
			}
			if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("Plex community: HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		resp.Body.Close()
		if err != nil || len(data) > 4<<20 {
			return errors.New("Plex community response unreadable or too large")
		}
		var envelope struct {
			Data   json.RawMessage   `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		if json.Unmarshal(data, &envelope) != nil || len(envelope.Errors) > 0 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return errors.New("Plex community query refused or response invalid")
		}
		c.mu.Lock()
		c.consecutiveRateLimits = 0
		c.mu.Unlock()
		return json.Unmarshal(envelope.Data, out)
	}
}
func (c *CommunityClient) Friends(ctx context.Context, token string) ([]CommunityFriend, error) {
	var data struct {
		Friends *[]struct {
			User CommunityFriend `json:"user"`
		} `json:"allFriendsV2"`
	}
	err := c.query(ctx, token, `query { allFriendsV2 { user { id username displayName avatar } } }`, nil, &data)
	if err != nil {
		return nil, err
	}
	if data.Friends == nil {
		return nil, errors.New("Plex community friends response missing")
	}
	out := make([]CommunityFriend, 0, len(*data.Friends))
	for _, f := range *data.Friends {
		if f.User.ID == "" {
			return nil, errors.New("Plex community friend identity missing")
		}
		out = append(out, f.User)
	}
	return out, nil
}

// WatchlistPage reads the user's watchlist with UserInput and cursor pagination.
func (c *CommunityClient) WatchlistPage(ctx context.Context, token, id, after string, first int) (*CommunityPage, error) {
	if first < 1 || first > 100 {
		first = 100
	}
	var cursor any
	if after != "" {
		cursor = after
	}
	var data struct {
		User *struct {
			Watchlist *CommunityPage `json:"watchlist"`
		} `json:"userV2"`
	}
	err := c.query(ctx, token, `query ($user: UserInput!, $first: PaginationInt!, $after: String) { userV2(user: $user) { ... on User { watchlist(first: $first, after: $after) { nodes { id title type } pageInfo { hasNextPage endCursor } } } } }`, map[string]any{"user": map[string]string{"id": id}, "first": first, "after": cursor}, &data)
	if err != nil {
		return nil, err
	}
	if data.User == nil || data.User.Watchlist == nil || data.User.Watchlist.Nodes == nil {
		return nil, errors.New("Plex community watchlist unavailable; check friend visibility")
	}
	return data.User.Watchlist, nil
}
