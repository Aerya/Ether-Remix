package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// IsImmutableURL reports whether rawURL addresses an immutable TMDB image path.
func (im *Images) IsImmutableURL(rawURL string) bool {
	if im == nil {
		return false
	}
	_, ok := im.tmdbKeyFromURL(rawURL)
	return ok
}

// cachePolicy bounds HTTP freshness derived from Cache-Control, Expires and Age.
type cachePolicy struct {
	Floor         time.Duration
	Default       time.Duration
	Ceiling       time.Duration
	HonourNoStore bool
}

var (
	providerCachePolicy = cachePolicy{
		Floor:         0,
		Default:       6 * time.Hour,
		Ceiling:       6 * time.Hour,
		HonourNoStore: true,
	}
	thirdPartyURLPolicy = cachePolicy{
		Floor:         24 * time.Hour,
		Default:       24 * time.Hour,
		Ceiling:       30 * 24 * time.Hour,
		HonourNoStore: false,
	}
)

func setConditionalHeaders(req *http.Request, etag, lastModified string) {
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
}

func parseCacheHeaders(h http.Header, status int, prevETag, prevLastMod, prevCC string, now time.Time, p cachePolicy) (etag, lastMod, cc string, expires time.Time, noStore bool) {
	etag, lastMod, cc = prevETag, prevLastMod, prevCC
	if v := h.Get("ETag"); v != "" || status == http.StatusOK {
		etag = v
	}
	if v := h.Get("Last-Modified"); v != "" || status == http.StatusOK {
		lastMod = v
	}
	if v := h.Get("Cache-Control"); v != "" || status == http.StatusOK {
		cc = v
	}
	const maxSeconds = 365 * 24 * 3600
	ttl := p.Default
	hasExplicit := false
	noCache := false
	for _, directive := range strings.Split(cc, ",") {
		name, value, hasVal := strings.Cut(strings.TrimSpace(directive), "=")
		switch strings.ToLower(name) {
		case "no-store":
			if p.HonourNoStore {
				noStore = true
			}
		case "no-cache":
			noCache = true
			hasExplicit = true
		case "max-age":
			if hasVal {
				if n, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), `"`), 10, 64); err == nil && n >= 0 {
					ttl = time.Duration(min(n, maxSeconds)) * time.Second
					hasExplicit = true
				}
			}
		}
	}
	usedExpires := false
	if noCache {
		ttl = 0
	} else if !hasExplicit {
		if expStr := strings.TrimSpace(h.Get("Expires")); expStr != "" {
			if exp, err := http.ParseTime(expStr); err == nil {
				base := now
				if dateStr := strings.TrimSpace(h.Get("Date")); dateStr != "" {
					if d, err := http.ParseTime(dateStr); err == nil {
						base = d
					}
				}
				ttl = exp.Sub(base)
			} else {
				ttl = 0
			}
			usedExpires = true
		}
	}
	if !usedExpires {
		if age, err := strconv.ParseInt(strings.TrimSpace(h.Get("Age")), 10, 64); err == nil && age > 0 {
			ttl -= time.Duration(min(age, maxSeconds)) * time.Second
		}
	}
	ttl = max(p.Floor, min(p.Ceiling, ttl))
	expires = now.Add(ttl)
	return etag, lastMod, cc, expires, noStore
}

func parseRetryAfter(h http.Header, now time.Time, ceiling time.Duration) (time.Time, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return time.Time{}, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs <= 0 {
			return time.Time{}, false
		}
		maxSecs := int64(ceiling / time.Second)
		return now.Add(time.Duration(min(secs, maxSecs)) * time.Second), true
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		if t.Sub(now) > ceiling {
			t = now.Add(ceiling)
		}
		return t, true
	}
	return time.Time{}, false
}

// Originals have a stable key across client widths and clock buckets. Provider
// scores/styles are revalidated at least every six hours; owner URL/profile/key
// edits select a new key immediately. Viewer ratings do not alter provider art.
type providerImage struct {
	Data               []byte
	ETag, LastModified string
	CacheControl       string
	Expires            time.Time
	NoStore            bool
}

func (im *Images) providerOriginal(ctx context.Context, raw string) (*providerImage, error) {
	key := providerCacheKey(raw)
	read := func() *providerImage {
		im.mu.Lock()
		cached, ok := im.items.Get(key)
		im.mu.Unlock()
		var data []byte
		if ok {
			data = cached.data
		} else {
			data, _, _ = im.readDisk(key)
		}
		var img providerImage
		if json.Unmarshal(data, &img) != nil || len(img.Data) == 0 {
			return nil
		}
		if !ok {
			im.mu.Lock()
			im.addLocked(&memImage{key: key, data: data})
			im.mu.Unlock()
		}
		return &img
	}
	if img := read(); img != nil && im.now().Before(img.Expires) {
		return img, nil
	}
	// Different local sizes share this flight. A cancelled leader finishes
	// its work before returning; live waiters retry on their own context.
	result, err := im.inflight.Do(ctx, key, func(ctx context.Context) (*memImage, error) {
		old := read()
		if old != nil && im.now().Before(old.Expires) {
			data, _ := json.Marshal(old)
			return &memImage{key: key, data: data}, nil
		}
		img, err := im.fetchProviderOriginal(ctx, raw, old)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := json.Marshal(img)
		if err != nil {
			return nil, err
		}
		result := &memImage{key: key, data: data}
		if img.NoStore {
			im.mu.Lock()
			im.items.Remove(key)
			im.mu.Unlock()
			if im.o.Dir != "" {
				im.diskMu.Lock()
				if info, err := os.Stat(im.diskPath(key)); err == nil && os.Remove(im.diskPath(key)) == nil && im.diskKnown {
					im.diskUsed -= info.Size()
				}
				im.diskMu.Unlock()
			}
		} else {
			im.writeDisk(key, data)
			im.mu.Lock()
			im.addLocked(result)
			im.mu.Unlock()
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	var img providerImage
	if json.Unmarshal(result.data, &img) != nil {
		return nil, errProvider
	}
	return &img, nil
}

func (im *Images) fetchProviderOriginal(ctx context.Context, raw string, old *providerImage) (*providerImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case im.posterSlots <- struct{}{}:
		defer func() { <-im.posterSlots }()
	case <-ctx.Done():
		return nil, errProvider
	}
	client := *im.o.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errProvider
	}
	if old != nil {
		setConditionalHeaders(req, old.ETag, old.LastModified)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errProvider
	}
	defer resp.Body.Close()
	img := &providerImage{}
	if resp.StatusCode == http.StatusNotModified && old != nil {
		*img = *old
	} else if resp.StatusCode == http.StatusOK {
		img.Data, err = io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		if err != nil || len(img.Data) > 8<<20 {
			return nil, errProvider
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
			return nil, errProvider
		}
		// Validate the full raster before persisting an original.
		if _, _, err := image.Decode(bytes.NewReader(img.Data)); err != nil {
			return nil, errProvider
		}
	} else {
		return nil, errProvider
	}
	img.ETag, img.LastModified, img.CacheControl, img.Expires, img.NoStore = parseCacheHeaders(
		resp.Header, resp.StatusCode, img.ETag, img.LastModified, img.CacheControl, im.now(), providerCachePolicy,
	)
	return img, nil
}

func providerCacheKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "provider-original-v2/" + hex.EncodeToString(sum[:])
}
