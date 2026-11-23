package extras

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

var (
	youtubeKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	vimeoKeyRe   = regexp.MustCompile(`^[0-9]{6,12}$`)
)

// ValidateSiteKey reports whether a TMDB site/key pair is a well-formed
// video id Ether may hand to the resolver. Anything else is rejected
// before it can reach a command line.
func ValidateSiteKey(site, key string) bool {
	switch strings.ToLower(strings.TrimSpace(site)) {
	case "youtube":
		return youtubeKeyRe.MatchString(key)
	case "vimeo":
		return vimeoKeyRe.MatchString(key)
	}
	return false
}

// PageURL builds the public watch-page URL for a validated site key.
func PageURL(site, key string) (string, bool) {
	if !ValidateSiteKey(site, key) {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(site)) {
	case "youtube":
		return "https://www.youtube.com/watch?v=" + key, true
	case "vimeo":
		return "https://vimeo.com/" + key, true
	}
	return "", false
}

// ThumbURL returns a public thumbnail URL for an extra when the site
// offers one without an API call (YouTube only).
func (e Extra) ThumbURL() string {
	if strings.EqualFold(e.Site, "youtube") && youtubeKeyRe.MatchString(e.SiteKey) {
		return "https://i.ytimg.com/vi/" + e.SiteKey + "/hqdefault.jpg"
	}
	return ""
}

const defaultTrailerFormat = "bv*[vcodec^=avc1][height<=1080]+ba[acodec^=mp4a]/b[ext=mp4]/b"

// DefaultResolverArgs is the yt-dlp-compatible template used when the
// owner configured a command but no arguments: print video and audio URLs
// (or one muxed URL) for the watch page.
var DefaultResolverArgs = []string{"-g", "-f", defaultTrailerFormat, "--no-playlist", "{url}"}

const (
	maxResolverOutput = 64 << 10
	maxResolverStderr = 8 << 10
	defaultURLTTL     = time.Hour
	maxURLTTL         = 6 * time.Hour
)

type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len() >= c.max {
		c.overflow = true
		return len(p), nil
	}
	if room := c.max - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:room])
		c.overflow = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

// execRunner runs the resolver without a shell, bounding both streams.
func execRunner(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := mediaproc.CommandContext(ctx, 90*time.Second, name, args...)
	rc, hasProxy := runnerProxyFrom(ctx)
	if hasProxy {
		if env := subprocessEnv(nil, rc.proxy, rc.policyEnabled, rc.bypass, true); env != nil {
			cmd.Env = env
		}
	}
	out := &cappedBuffer{max: maxResolverOutput}
	serr := &cappedBuffer{max: maxResolverStderr}
	cmd.Stdout = out
	cmd.Stderr = serr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(serr.buf.String())
		if rc.proxy != nil {
			msg = strings.TrimSpace(scrubProxyText(msg, rc.proxy))
		}
		if len(msg) > 256 {
			msg = msg[:256]
		}
		if msg != "" {
			return nil, scrubProxyErr(fmt.Errorf("extras resolver: %w: %s", err, msg), rc.proxy)
		}
		return nil, scrubProxyErr(fmt.Errorf("extras resolver: %w", err), rc.proxy)
	}
	if out.overflow {
		return nil, fmt.Errorf("extras resolver: output exceeds %d bytes", maxResolverOutput)
	}
	return out.buf.Bytes(), nil
}

// runResolver invokes the owner's command for one extra and parses its
// output: either a JSON object {"url":..., "headers":{...},
// "audioURL":..., "expires":...} or one/two plain http(s) lines
// (the yt-dlp -g shape).
func runResolver(ctx context.Context, cfg config.Extras, e Extra, runner CommandRunner, now func() time.Time) (Resolved, error) {
	page, ok := PageURL(e.Site, e.SiteKey)
	if !ok {
		return Resolved{}, fmt.Errorf("extras: invalid %s key %q", e.Site, e.SiteKey)
	}
	proxy, policyEnabled, bypass, err := extrasProxyFor(page)
	if err != nil {
		return Resolved{}, err
	}
	args := cfg.ResolverArgs
	customArgs := len(args) > 0
	if !customArgs {
		args = DefaultResolverArgs
	}
	sub := strings.NewReplacer(
		"{url}", page, "{site}", strings.ToLower(e.Site),
		"{key}", e.SiteKey, "{id}", e.SiteKey)
	out := make([]string, 0, len(args)+2)
	if proxy != nil && !customArgs {
		out = append(out, "--proxy", proxy.String())
	}
	for _, a := range args {
		out = append(out, sub.Replace(a))
	}
	timeout := cfg.ResolverTimeoutOr(20 * time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = withRunnerProxy(ctx, runnerProxyConfig{proxy: proxy, policyEnabled: policyEnabled, bypass: bypass})
	raw, err := runner(ctx, cfg.ResolverCommand, out)
	if err != nil {
		return Resolved{}, scrubProxyErr(err, proxy)
	}
	if len(raw) > maxResolverOutput {
		return Resolved{}, fmt.Errorf("extras resolver: output exceeds %d bytes", maxResolverOutput)
	}
	return parseResolverOutput(raw, now())
}

func parseResolverOutput(raw []byte, at time.Time) (Resolved, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return Resolved{}, fmt.Errorf("extras resolver: empty output")
	}
	if strings.HasPrefix(text, "{") {
		var j struct {
			URL       string            `json:"url"`
			AudioURL  string            `json:"audioURL"`
			Headers   map[string]string `json:"headers"`
			Expires   json.RawMessage   `json:"expires"`
			Container string            `json:"container"`
		}
		if err := json.Unmarshal([]byte(text), &j); err != nil {
			return Resolved{}, fmt.Errorf("extras resolver: bad JSON: %w", err)
		}
		r := Resolved{URL: strings.TrimSpace(j.URL), AudioURL: strings.TrimSpace(j.AudioURL), Headers: j.Headers, Container: j.Container}
		r.Expires = parseExpiry(j.Expires, at)
		return finishResolved(r, at)
	}
	var urls []string
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "http://") && !strings.HasPrefix(line, "https://") {
			continue
		}
		urls = append(urls, line)
	}
	if len(urls) == 0 {
		return Resolved{}, fmt.Errorf("extras resolver: no media URL in output")
	}
	if len(urls) > 2 {
		return Resolved{}, fmt.Errorf("extras resolver: %d media URLs; expected video and optional audio", len(urls))
	}
	r := Resolved{URL: urls[0]}
	if len(urls) == 2 {
		r.AudioURL = urls[1]
	}
	return finishResolved(r, at)
}

func finishResolved(r Resolved, at time.Time) (Resolved, error) {
	var video *url.URL
	for i, raw := range []string{r.URL, r.AudioURL} {
		if i == 1 && raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Resolved{}, fmt.Errorf("extras resolver: invalid media URL %q", raw)
		}
		if i == 0 {
			video = u
		}
		// A pair is one cache entry: expire both at the earliest URL expiry,
		// including when an owner supplied an explicit JSON expiry.
		if exp, err := strconv.ParseInt(u.Query().Get("expire"), 10, 64); err == nil && exp > 0 {
			expires := time.Unix(exp, 0)
			if r.Expires.IsZero() || expires.Before(r.Expires) {
				r.Expires = expires
			}
		}
	}
	if r.Expires.IsZero() || !r.Expires.After(at) {
		if !r.Expires.IsZero() && !r.Expires.After(at) {
			return Resolved{}, fmt.Errorf("extras resolver: media URL already expired")
		}
		r.Expires = at.Add(defaultURLTTL)
	}
	if latest := at.Add(maxURLTTL); r.Expires.After(latest) {
		r.Expires = latest
	}
	if r.Container == "" {
		r.Container = containerFromURL(video)
	}
	return r, nil
}

func parseExpiry(raw json.RawMessage, at time.Time) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var unix int64
	if err := json.Unmarshal(raw, &unix); err == nil && unix > 0 {
		return time.Unix(unix, 0)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			return time.Unix(n, 0)
		}
	}
	_ = at
	return time.Time{}
}

func containerFromURL(u *url.URL) string {
	if ct := u.Query().Get("mime"); strings.Contains(ct, "webm") {
		return "webm"
	} else if strings.Contains(ct, "mp4") {
		return "mp4"
	}
	path := strings.ToLower(u.Path)
	for _, ext := range []string{".mp4", ".webm", ".mkv", ".mov", ".m4v"} {
		if strings.HasSuffix(path, ext) {
			return strings.TrimPrefix(ext, ".")
		}
	}
	return "mp4"
}
