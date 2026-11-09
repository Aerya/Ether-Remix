package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Audius resolves only public, ungated tracks through the official API.
// The catalogue identity stays in Deezer, so switching sources preserves libraries.
type Audius struct {
	base    string
	http    *http.Client
	mu      sync.Mutex
	retryAt time.Time
}

func NewAudius(hc *http.Client) *Audius {
	if hc == nil {
		hc = &http.Client{Timeout: 12 * time.Second}
	}
	return &Audius{base: "https://api.audius.co/v1", http: hc}
}

type audiusTrack struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	ISRC       string `json:"isrc"`
	Duration   int64  `json:"duration"`
	Streamable bool   `json:"is_streamable"`
	Available  bool   `json:"is_available"`
	Gated      *bool  `json:"is_stream_gated"`
	Deleted    bool   `json:"is_delete"`
	User       struct {
		Name string `json:"name"`
	} `json:"user"`
}

var audiusID = regexp.MustCompile(`^[a-zA-Z0-9]+$`)

func (a *Audius) search(ctx context.Context, query string) ([]audiusTrack, error) {
	a.mu.Lock()
	cooling := time.Now().Before(a.retryAt)
	a.mu.Unlock()
	if cooling {
		return nil, errors.New("music: Audius temporairement limité")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	u := a.base + "/tracks/search?" + url.Values{"query": {query}, "limit": {"20"}, "app_name": {"Accent"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("music: requête Audius invalide")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Ether-Remix")
	res, err := a.http.Do(req)
	if err != nil {
		return nil, redactErr(err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		delay := time.Minute
		if at, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil && time.Until(at) > delay {
			delay = time.Until(at)
		}
		if seconds, err := time.ParseDuration(res.Header.Get("Retry-After") + "s"); err == nil && seconds > delay {
			delay = seconds
		}
		a.mu.Lock()
		a.retryAt = time.Now().Add(delay)
		a.mu.Unlock()
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("music: Audius a répondu %d", res.StatusCode)
	}
	var out struct {
		Data []audiusTrack `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
		return nil, errors.New("music: réponse Audius illisible")
	}
	return out.Data, nil
}

// Check tests public search without credentials; playback is tested by Diagnose.
func (a *Audius) Check(ctx context.Context) error {
	_, err := a.search(ctx, "Disclosure Waterfall")
	return err
}

func (a *Audius) Resolve(ctx context.Context, t Track) (Stream, error) {
	hits, err := a.search(ctx, strings.TrimSpace(t.Artist.Name+" "+t.Title))
	if err != nil {
		return Stream{}, err
	}
	bestScore := -1
	var best audiusTrack
	for _, h := range hits {
		if !h.Streamable || !h.Available || h.Gated == nil || *h.Gated || h.Deleted || !audiusID.MatchString(h.ID) {
			continue
		}
		// Without an ISRC, require both artist and duration rather than a title alone.
		if h.ISRC == "" || t.ISRC == "" {
			if fold(h.User.Name) != fold(t.Artist.Name) || h.User.Name == "" || h.Duration <= 0 || t.Duration <= 0 {
				continue
			}
			delta := h.Duration - int64(t.Duration)
			if delta < -5 || delta > 5 {
				continue
			}
		}
		score, ok := matchScore(candidate{title: h.Title, artist: h.User.Name, isrc: h.ISRC, duration: h.Duration}, t)
		link := a.base + "/tracks/" + h.ID + "/stream?app_name=Accent"
		if ok && score > bestScore && !skipped(ctx, link) {
			bestScore, best = score, h
		}
	}
	if bestScore < 0 {
		return Stream{}, ErrNotFound
	}
	// Resolve afresh on every playback: the API enforces access and redirects to
	// a current signed URL. Never persist or log those ephemeral URLs.
	return Stream{URL: a.base + "/tracks/" + best.ID + "/stream?app_name=Accent", Mime: "audio/mpeg", Quality: "MP3", Provider: "Audius"}, nil
}
