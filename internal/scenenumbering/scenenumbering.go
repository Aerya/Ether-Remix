// Package scenenumbering lazily reads untrusted XEM episode mappings.
// Only a TVDB absolute-number match establishes a TMDB episode's identity.
package scenenumbering

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/httpclient"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

const DefaultURL = "https://thexem.info/map/all"

type Options struct {
	URL    string
	Client *http.Client
	Now    func() time.Time
}
type number struct {
	Season   int `json:"season"`
	Episode  int `json:"episode"`
	Absolute int `json:"absolute"`
}
type entry struct {
	numbers *mapping
	expires time.Time
	loading chan struct{}
}
type Service struct {
	opts  Options
	mu    sync.Mutex
	shows map[int64]*entry
}

func New(opts Options) *Service {
	if opts.URL == "" {
		opts.URL = DefaultURL
	}
	if opts.Client == nil {
		opts.Client = (&safefetch.Guard{}).Client(&http.Client{Transport: egress.Transport(egress.CapMetadata)})
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, shows: map[int64]*entry{}}
}

// Lookup reads XEM only on demand; canonical facts are read only after an
// entry exists. Canonical numbering remains authoritative.
func (s *Service) Lookup(ctx context.Context, req core.SearchRequest, canonical func() (core.EpisodeLayout, error)) (core.ReleaseNumbering, error) {
	if req.Type != core.TypeEpisode || req.IDs.TVDB <= 0 || req.Season <= 0 || req.Episode <= 0 {
		return core.ReleaseNumbering{}, nil
	}
	if absoluteNumber(req) <= 0 {
		return core.ReleaseNumbering{}, nil
	}
	numbers, err := s.load(ctx, req.IDs.TVDB)
	if err != nil || numbers == nil {
		return core.ReleaseNumbering{}, err
	}
	layout, err := canonical()
	if err != nil {
		return core.ReleaseNumbering{}, err
	}
	return numbers.lookup(req, layout), nil
}

func (s *Service) load(ctx context.Context, id int64) (*mapping, error) {
	for {
		s.mu.Lock()
		e := s.shows[id]
		if e != nil && s.opts.Now().Before(e.expires) {
			n := e.numbers
			s.mu.Unlock()
			return n, nil
		}
		if e != nil && e.loading != nil {
			wait := e.loading
			s.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		// Bound memory even when many different shows are resolved. No background sweeper.
		if len(s.shows) >= 512 {
			for k, v := range s.shows {
				if v.loading == nil {
					delete(s.shows, k)
					break
				}
			}
		}
		e = &entry{loading: make(chan struct{})}
		s.shows[id] = e
		s.mu.Unlock()
		numbers, err := s.fetch(id)
		ttl := 7 * 24 * time.Hour
		if err != nil {
			ttl = time.Hour
		}
		s.mu.Lock()
		e.numbers, e.expires = numbers, s.opts.Now().Add(ttl)
		close(e.loading)
		e.loading = nil
		s.mu.Unlock()
		return numbers, err
	}
}

func (s *Service) fetch(id int64) (*mapping, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.opts.URL+"?origin=tvdb&id="+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", httpclient.UserAgent())
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	const limit = 4 << 20
	if resp.StatusCode != http.StatusOK || resp.ContentLength > limit {
		return nil, fmt.Errorf("scenenumbering: invalid HTTP response %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("scenenumbering: oversized mapping")
	}
	var result struct {
		Result string          `json:"result"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.Result == "failure" {
		// Missing shows and upstream failures both back off without guessing.
		return nil, fmt.Errorf("scenenumbering: no mapping available")
	}
	if result.Result != "success" {
		return nil, fmt.Errorf("scenenumbering: invalid result")
	}
	var rows []mappingRow
	if err := json.Unmarshal(result.Data, &rows); err != nil {
		return nil, err
	}
	return parseMapping(rows), nil
}

// LookupCached never performs network work.
func (s *Service) LookupCached(req core.SearchRequest, canonical func() (core.EpisodeLayout, error)) (core.ReleaseNumbering, error) {
	s.mu.Lock()
	var numbers *mapping
	if e := s.shows[req.IDs.TVDB]; e != nil {
		numbers = e.numbers
	}
	s.mu.Unlock()
	if numbers == nil {
		return core.ReleaseNumbering{}, nil
	}
	layout, err := canonical()
	if err != nil {
		return core.ReleaseNumbering{}, err
	}
	return numbers.lookup(req, layout), nil
}
