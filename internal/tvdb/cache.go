package tvdb

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/atomicfile"
	"github.com/braedonsaunders/ether/internal/core"
)

// Per-URL compressed snapshots use fixed slots so the cache has a hard
// disk bound and lookups never enumerate cached titles. A slot collision
// simply evicts the previous response. Writes are atomic, as for TMDB exports.
const (
	cacheSlots    = 4096
	cacheMaxEntry = 512 << 10
	entityFresh   = 2 * time.Hour
	entityMaxAge  = 24 * time.Hour
	updateWindow  = 8 * time.Hour
)

type cacheRecord struct {
	Digest   [32]byte
	Fetched  time.Time
	Checked  time.Time
	Response response
}

type cachePolicy struct {
	fresh   time.Duration
	changes changeRef
}

type changeRef struct {
	kind     string
	id       int64
	seriesID bool
}

func entityPolicy(kind string, id int64) cachePolicy {
	return cachePolicy{fresh: entityFresh, changes: changeRef{kind: kind, id: id}}
}

func (c *Client) cachePath(digest [32]byte) string {
	slot := binary.BigEndian.Uint16(digest[:2]) % cacheSlots
	return filepath.Join(c.cacheDir, fmt.Sprintf("%02x", slot/256), fmt.Sprintf("%02x.json.gz", slot%256))
}

func (c *Client) readCache(digest [32]byte) (cacheRecord, bool) {
	var record cacheRecord
	if c.cacheDir == "" {
		return record, false
	}
	file, err := os.Open(c.cachePath(digest))
	if err != nil {
		return record, false
	}
	defer file.Close()
	z, err := gzip.NewReader(io.LimitReader(file, cacheMaxEntry+1))
	if err != nil {
		return record, false
	}
	defer z.Close()
	data, err := io.ReadAll(io.LimitReader(z, maxResponse+4097))
	if err != nil || len(data) > maxResponse+4096 || json.Unmarshal(data, &record) != nil {
		return cacheRecord{}, false
	}
	return record, record.Digest == digest && record.Response.Status == "success"
}

func (c *Client) writeCache(record cacheRecord) {
	if c.cacheDir == "" {
		return
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	var buffer bytes.Buffer
	z, _ := gzip.NewWriterLevel(&buffer, gzip.BestSpeed)
	_, _ = z.Write(data)
	_ = z.Close()
	if buffer.Len() > cacheMaxEntry {
		return
	}
	path := c.cachePath(record.Digest)
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = atomicfile.Write(path, buffer.Bytes(), 0o600)
	}
}

func get[T any](ctx context.Context, c *Client, path string, policy cachePolicy) (T, pageLinks, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, pageLinks{}, err
	}
	if !c.Configured() {
		return zero, pageLinks{}, ErrKeyRequired
	}
	digest := sha256.Sum256([]byte(path))
	started := core.MetadataRefreshStarted(ctx)
	force := !started.IsZero()
	useCache := func(cached cacheRecord, now time.Time) bool {
		return now.Sub(cached.Checked) < policy.fresh && (!force || !cached.Fetched.Before(started))
	}

	if cached, ok := c.readCache(digest); ok && useCache(cached, time.Now()) {
		var data T
		if json.Unmarshal(cached.Response.Data, &data) == nil {
			return data, cached.Response.Links, nil
		}
	}
	flightKey := path
	if force {
		flightKey = "refresh:" + path
	}
	result, err := waitFlight(ctx, &c.fetch, flightKey, func() (any, error) {
		now := time.Now()
		if cached, ok := c.readCache(digest); ok {
			if useCache(cached, time.Now()) {
				var data T
				if json.Unmarshal(cached.Response.Data, &data) == nil {
					return cached.Response, nil
				}
			} else if !force && policy.changes.id > 0 && now.Sub(cached.Fetched) < entityMaxAge {
				if checked := c.unchangedThrough(ctx, policy.changes, cached.Checked, now); !checked.IsZero() {
					cached.Checked = checked
					c.writeCache(cached)
					return cached.Response, nil
				}
			}
		}
		res, err := c.fetchResponse(ctx, path)
		if err != nil {
			return nil, err
		}
		var data T
		if json.Unmarshal(res.Data, &data) != nil {
			return nil, ErrInvalidResponse
		}
		c.writeCache(cacheRecord{Digest: digest, Fetched: now, Checked: now, Response: res})
		return res, nil
	})
	if err != nil {
		return zero, pageLinks{}, err
	}
	res := result.(response)
	var data T
	if json.Unmarshal(res.Data, &data) != nil {
		return zero, pageLinks{}, ErrInvalidResponse
	}
	return data, res.Links, nil
}

type entityUpdate struct {
	RecordID int64 `json:"recordId"`
	SeriesID int64 `json:"seriesId"`
	Stamp    int64 `json:"timeStamp"`
}

type changeSnapshot struct {
	through              time.Time
	complete             bool
	records              map[int64]int64
	series               map[int64]int64
	unknownEpisodeSeries int64
}

func (c *Client) unchangedThrough(ctx context.Context, ref changeRef, checked, now time.Time) time.Time {
	// The snapshot is shared across titles. A fixed window and page bound
	// avoid crawling the global feed on a request. If coverage is uncertain,
	// the one record the caller asked for is fetched directly.
	since := now.Truncate(time.Hour).Add(-updateWindow)
	if checked.Before(since) {
		return time.Time{}
	}
	snap, err := c.changes(ctx, ref.kind)
	if err != nil || !snap.complete || !snap.through.After(checked) {
		return time.Time{}
	}
	stamp := snap.records[ref.id]
	if ref.seriesID {
		// Some episode updates omit seriesId. They cannot prove a listing
		// unchanged, but their record IDs still invalidate individual episodes.
		stamp = max(snap.series[ref.id], snap.unknownEpisodeSeries)
	}
	if stamp >= checked.Unix() {
		return time.Time{}
	}
	return snap.through
}

func (c *Client) changes(ctx context.Context, kind string) (changeSnapshot, error) {
	current := func() (changeSnapshot, bool) {
		c.changesMu.Lock()
		defer c.changesMu.Unlock()
		snap, ok := c.snapshots[kind]
		return snap, ok && time.Since(snap.through) < 15*time.Minute
	}
	if snap, ok := current(); ok {
		return snap, nil
	}
	result, err := waitFlight(ctx, &c.fetch, "updates:"+kind, func() (any, error) {
		if snap, ok := current(); ok {
			return snap, nil
		}
		snap := changeSnapshot{
			through: time.Now(), records: make(map[int64]int64), series: make(map[int64]int64),
		}
		since := snap.through.Truncate(time.Hour).Add(-updateWindow)
		prefix := "/updates?since=" + strconv.FormatInt(since.Unix(), 10) + "&type=" + kind + "&page="
	pages:
		for page := 0; page < 4; page++ {
			// A snapshot's watermark is the moment this fetch began. Its
			// pages must all be obtained afresh within that same window.
			res, err := c.fetchResponse(ctx, prefix+strconv.Itoa(page))
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				break
			}
			var updates []entityUpdate
			if json.Unmarshal(res.Data, &updates) != nil || len(snap.records)+len(updates) > 20_000 {
				break
			}
			for _, update := range updates {
				if update.RecordID <= 0 || update.Stamp <= 0 {
					break pages
				}
				snap.records[update.RecordID] = max(snap.records[update.RecordID], update.Stamp)
				if update.SeriesID > 0 {
					snap.series[update.SeriesID] = max(snap.series[update.SeriesID], update.Stamp)
				} else if kind == "episodes" {
					snap.unknownEpisodeSeries = max(snap.unknownEpisodeSeries, update.Stamp)
				}
			}
			if res.Links.Next == "" {
				snap.complete = true
				break
			}
		}
		c.changesMu.Lock()
		c.snapshots[kind] = snap
		c.changesMu.Unlock()
		return snap, nil
	})
	if err != nil {
		return changeSnapshot{}, err
	}
	return result.(changeSnapshot), nil
}
