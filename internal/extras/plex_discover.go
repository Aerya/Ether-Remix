package extras

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
)

// DefaultDiscoverBaseURL is Plex's Discover metadata provider.
const DefaultDiscoverBaseURL = "https://metadata.provider.plex.tv"

// DiscoverProvider lists and resolves direct-MP4 extras from Plex Discover.
type DiscoverProvider interface {
	Available() bool
	List(ctx context.Context, parent *core.Title) ([]Extra, error)
	Resolve(ctx context.Context, e Extra) (Resolved, error)
}

// PlexDiscoverOptions configures the Plex Discover extras provider.
type PlexDiscoverOptions struct {
	BaseURL     string
	HTTP        *http.Client
	GUID        func(ctx context.Context, key core.RatingKey) string
	ResolveGUID func(ctx context.Context, key core.RatingKey) (string, error)
	Token       func() string
	ClientID    func() string
	Now         func() time.Time
	Logger      *slog.Logger
}

// PlexDiscover fetches direct-MP4 trailers and extras from Plex Discover using
// the title's verified plex:// GUID and the server's existing Plex account
// token handling. Tokens are never exposed to clients or forwarded on redirect.
type PlexDiscover struct {
	baseURL     string
	http        *http.Client
	guid        func(ctx context.Context, key core.RatingKey) string
	resolveGUID func(ctx context.Context, key core.RatingKey) (string, error)
	token       func() string
	clientID    func() string
	now         func() time.Time
	log         *slog.Logger
}

// NewPlexDiscover constructs a Plex Discover extras provider.
func NewPlexDiscover(o PlexDiscoverOptions) *PlexDiscover {
	base := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if base == "" {
		base = DefaultDiscoverBaseURL
	}
	var hc http.Client
	if o.HTTP != nil {
		hc = *o.HTTP
	} else {
		hc = *egress.Client(egress.CapDirect, 8*time.Second)
	}
	// Never forward X-Plex-Token across redirects, and capture Part.key 302s
	// server-side without following them with auth headers.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &PlexDiscover{
		baseURL:     base,
		http:        &hc,
		guid:        o.GUID,
		resolveGUID: o.ResolveGUID,
		token:       o.Token,
		clientID:    o.ClientID,
		now:         o.Now,
		log:         o.Logger.With("source", SourcePlexDiscover),
	}
}

// Available reports whether Plex Discover can be queried under Ether's Plex
// account privacy rules (Plex enabled and claimed with an account token).
func (p *PlexDiscover) Available() bool {
	if p == nil || (p.guid == nil && p.resolveGUID == nil) || p.token == nil {
		return false
	}
	return strings.TrimSpace(p.token()) != ""
}

// List returns direct-MP4 extras for parent from Plex Discover.
func (p *PlexDiscover) List(ctx context.Context, parent *core.Title) ([]Extra, error) {
	if !p.Available() || parent == nil || parent.Key == 0 {
		return nil, nil
	}
	var rawGUID string
	if p.resolveGUID != nil {
		g, err := p.resolveGUID(ctx, parent.Key)
		if err != nil {
			return nil, err
		}
		rawGUID = g
	} else if p.guid != nil {
		rawGUID = p.guid(ctx, parent.Key)
	}
	parentHex := extractDiscoverHex(rawGUID)
	if parentHex == "" {
		return nil, nil
	}
	items, err := p.fetchExtras(ctx, parentHex)
	if err != nil {
		return nil, err
	}
	out := make([]Extra, 0, len(items))
	for _, item := range items {
		extraID := discoverExtraID(item)
		if extraID == "" {
			continue
		}
		media, ok := pickDiscoverMedia(item.Media)
		if !ok {
			continue
		}
		sub, extraType := mapDiscoverSubtype(item.Subtype, int(item.ExtraType))
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = strings.TrimSpace(parent.Title + " " + sub)
		}
		durMs := media.durationMs
		if durMs <= 0 {
			durMs = int64(item.Duration)
		}
		discoverKey := parentHex + "/" + extraID
		out = append(out, Extra{
			ParentKey:   parent.Key,
			ParentTitle: parent.Title,
			Title:       title,
			Subtype:     sub,
			ExtraType:   extraType,
			DiscoverKey: discoverKey,
			Language:    ISO639(media.language),
			Official:    true,
			PublishedAt: parseDiscoverDate(item.OriginallyAvailableAt, int64(item.AddedAt)),
			Size:        media.height,
			Duration:    time.Duration(durMs) * time.Millisecond,
			Source:      SourcePlexDiscover,
			identity:    fmt.Sprintf("%d|d|%s", int64(parent.Key), discoverKey),
		})
	}
	return out, nil
}

// Resolve fetches a fresh direct media URL for a Plex Discover extra at play
// time without caching the signed URL across plays.
func (p *PlexDiscover) Resolve(ctx context.Context, e Extra) (Resolved, error) {
	variants, err := p.resolveVariants(ctx, e)
	if err != nil {
		return Resolved{}, err
	}
	var lastErr error
	for _, v := range variants {
		r, err := v.resolve(ctx)
		if err == nil {
			return r, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return Resolved{}, ctx.Err()
		}
	}
	if lastErr != nil {
		return Resolved{}, lastErr
	}
	return Resolved{}, errors.New("extras: plex discover media unavailable")
}

type discoverVariant struct {
	container string
	height    int
	bitrate   int
	resolve   func(ctx context.Context) (Resolved, error)
}

type discoverVariantResolver interface {
	resolveVariants(ctx context.Context, e Extra) ([]discoverVariant, error)
}

// resolveVariants returns ranked, lazily resolved media variants (best first)
// so callers such as the trailer cache can fall back to a smaller variant when
// the highest bitrate exceeds the cache entry limit.
func (p *PlexDiscover) resolveVariants(ctx context.Context, e Extra) ([]discoverVariant, error) {
	if !p.Available() {
		return nil, errors.New("extras: plex discover unavailable")
	}
	parentHex, extraID, ok := parseDiscoverKey(e.DiscoverKey)
	if !ok {
		return nil, errors.New("extras: invalid plex discover key")
	}
	items, err := p.fetchExtras(ctx, parentHex)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if discoverExtraID(item) != extraID {
			continue
		}
		ranked := rankDiscoverMedia(item.Media)
		if len(ranked) == 0 {
			return nil, errors.New("extras: plex discover media unavailable")
		}
		// Discover often copies the highest-bitrate Media.url onto every Media
		// variant while differentiating them via Part.key. Detect shared URLs
		// so each variant resolves its own stream.
		urlCounts := make(map[string]int, len(ranked))
		for _, c := range ranked {
			if c.directURL != "" {
				urlCounts[c.directURL]++
			}
		}
		variants := make([]discoverVariant, 0, len(ranked))
		for _, c := range ranked {
			cand := c
			sharedURL := cand.directURL != "" && urlCounts[cand.directURL] > 1
			container := cand.container
			if container == "" {
				container = "mp4"
			}
			variants = append(variants, discoverVariant{
				container: container,
				height:    cand.height,
				bitrate:   cand.bitrate,
				resolve: func(rctx context.Context) (Resolved, error) {
					return p.resolveCandidate(rctx, cand, sharedURL)
				},
			})
		}
		return variants, nil
	}
	return nil, errors.New("extras: plex discover item not found")
}

func (p *PlexDiscover) resolveCandidate(ctx context.Context, cand discoverCandidate, sharedURL bool) (Resolved, error) {
	urlBitrate, hasURLBitrate := discoverURLBitrate(cand.directURL)
	bitrateMismatch := hasURLBitrate && cand.bitrate > 0 && urlBitrate != cand.bitrate

	var target string
	var err error
	usePartKey := cand.partKey != "" && (cand.directURL == "" || sharedURL || bitrateMismatch)
	if usePartKey {
		target, err = p.resolvePartRedirect(ctx, cand.partKey)
		if err != nil {
			// Fall back to directURL only if it actually matches this variant's bitrate.
			if cand.directURL != "" && !bitrateMismatch && (!sharedURL || (hasURLBitrate && cand.bitrate > 0 && urlBitrate == cand.bitrate)) {
				target = cand.directURL
			} else {
				return Resolved{}, err
			}
		}
	} else if cand.directURL != "" {
		if bitrateMismatch {
			if rewritten, ok := rewriteDiscoverURLBitrate(cand.directURL, cand.bitrate); ok {
				target = rewritten
			} else {
				return Resolved{}, errors.New("extras: plex discover variant bitrate mismatch")
			}
		} else {
			target = cand.directURL
		}
	} else {
		return Resolved{}, errors.New("extras: plex discover media unavailable")
	}
	if !validPublicMediaURL(target) {
		return Resolved{}, errors.New("extras: invalid plex discover media URL")
	}
	container := cand.container
	if container == "" {
		container = "mp4"
	}
	return Resolved{
		URL:       target,
		Container: container,
		Expires:   p.now().Add(5 * time.Minute),
	}, nil
}

func (p *PlexDiscover) fetchExtras(ctx context.Context, parentHex string) ([]discoverExtraItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/library/metadata/"+parentHex+"/extras", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	p.applyPlexHeaders(req)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plex discover extras status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var env struct {
		MediaContainer struct {
			Metadata []discoverExtraItem `json:"Metadata"`
			Video    []discoverExtraItem `json:"Video"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if len(env.MediaContainer.Metadata) > 0 {
		return env.MediaContainer.Metadata, nil
	}
	return env.MediaContainer.Video, nil
}

func (p *PlexDiscover) resolvePartRedirect(ctx context.Context, partKey string) (string, error) {
	if !validDiscoverPartKey(partKey) {
		return "", errors.New("extras: invalid plex discover part key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+partKey, nil)
	if err != nil {
		return "", err
	}
	p.applyPlexHeaders(req)
	resp, err := p.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("plex discover part redirect status %d", resp.StatusCode)
	}
	loc := strings.TrimSpace(resp.Header.Get("Location"))
	if !validPublicMediaURL(loc) {
		return "", errors.New("extras: invalid plex discover redirect target")
	}
	return loc, nil
}

func (p *PlexDiscover) applyPlexHeaders(req *http.Request) {
	if p.token != nil {
		if tok := strings.TrimSpace(p.token()); tok != "" {
			req.Header.Set("X-Plex-Token", tok)
		}
	}
	if p.clientID != nil {
		if cid := strings.TrimSpace(p.clientID()); cid != "" {
			req.Header.Set("X-Plex-Client-Identifier", cid)
		}
	}
}

type discoverExtraItem struct {
	RatingKey             flexString      `json:"ratingKey"`
	Key                   string          `json:"key"`
	GUID                  string          `json:"guid"`
	Title                 string          `json:"title"`
	Subtype               string          `json:"subtype"`
	ExtraType             flexInt         `json:"extraType"`
	Duration              flexInt64       `json:"duration"`
	OriginallyAvailableAt string          `json:"originallyAvailableAt"`
	AddedAt               flexInt64       `json:"addedAt"`
	Media                 []discoverMedia `json:"Media"`
}

type discoverMedia struct {
	Container       string         `json:"container"`
	VideoCodec      string         `json:"videoCodec"`
	AudioCodec      string         `json:"audioCodec"`
	VideoResolution flexString     `json:"videoResolution"`
	Height          flexInt        `json:"height"`
	Bitrate         flexInt        `json:"bitrate"`
	Duration        flexInt64      `json:"duration"`
	URL             string         `json:"url"`
	Part            []discoverPart `json:"Part"`
}

type discoverPart struct {
	Key       string           `json:"key"`
	URL       string           `json:"url"`
	Container string           `json:"container"`
	Duration  flexInt64        `json:"duration"`
	Stream    []discoverStream `json:"Stream"`
}

type discoverStream struct {
	StreamType   flexInt `json:"streamType"`
	LanguageCode string  `json:"languageCode"`
}

type discoverCandidate struct {
	directURL  string
	partKey    string
	container  string
	videoCodec string
	audioCodec string
	height     int
	bitrate    int
	durationMs int64
	language   string
}

func rankDiscoverMedia(media []discoverMedia) []discoverCandidate {
	out := make([]discoverCandidate, 0, len(media))
	for _, m := range media {
		cand := discoverCandidate{
			container:  strings.ToLower(strings.TrimSpace(m.Container)),
			videoCodec: strings.ToLower(strings.TrimSpace(m.VideoCodec)),
			audioCodec: strings.ToLower(strings.TrimSpace(m.AudioCodec)),
			height:     discoverHeight(int(m.Height), string(m.VideoResolution)),
			bitrate:    int(m.Bitrate),
			durationMs: int64(m.Duration),
		}
		if u := strings.TrimSpace(m.URL); validPublicMediaURL(u) {
			cand.directURL = u
		}
		for _, part := range m.Part {
			if cand.directURL == "" {
				if u := strings.TrimSpace(part.URL); validPublicMediaURL(u) {
					cand.directURL = u
				}
			}
			if cand.partKey == "" && validDiscoverPartKey(part.Key) {
				cand.partKey = strings.TrimSpace(part.Key)
			}
			if cand.container == "" {
				cand.container = strings.ToLower(strings.TrimSpace(part.Container))
			}
			if cand.durationMs <= 0 {
				cand.durationMs = int64(part.Duration)
			}
			if cand.language == "" {
				for _, st := range part.Stream {
					if int(st.StreamType) == 2 && strings.TrimSpace(st.LanguageCode) != "" {
						cand.language = st.LanguageCode
						break
					}
				}
			}
		}
		if cand.directURL == "" && cand.partKey == "" {
			continue
		}
		if cand.bitrate <= 0 && cand.partKey != "" {
			cand.bitrate = discoverPartKeyBitrate(cand.partKey)
		}
		if cand.container == "" {
			if strings.Contains(strings.ToLower(cand.directURL), ".mp4") || strings.Contains(strings.ToLower(cand.partKey), "mp4") {
				cand.container = "mp4"
			}
		}
		out = append(out, cand)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return betterDiscoverMedia(out[i], out[j])
	})
	return out
}

func pickDiscoverMedia(media []discoverMedia) (discoverCandidate, bool) {
	ranked := rankDiscoverMedia(media)
	if len(ranked) == 0 {
		return discoverCandidate{}, false
	}
	return ranked[0], true
}

func betterDiscoverMedia(a, b discoverCandidate) bool {
	if (a.container == "mp4") != (b.container == "mp4") {
		return a.container == "mp4"
	}
	aCompat := (a.videoCodec == "" || a.videoCodec == "h264") && (a.audioCodec == "" || a.audioCodec == "aac")
	bCompat := (b.videoCodec == "" || b.videoCodec == "h264") && (b.audioCodec == "" || b.audioCodec == "aac")
	if aCompat != bCompat {
		return aCompat
	}
	aCapped := a.height > 0 && a.height <= 1080
	bCapped := b.height > 0 && b.height <= 1080
	if aCapped != bCapped {
		return aCapped
	}
	if a.height != b.height {
		if aCapped {
			return a.height > b.height
		}
		return a.height < b.height
	}
	if a.bitrate != b.bitrate {
		return a.bitrate > b.bitrate
	}
	return a.directURL != "" && b.directURL == ""
}

func discoverPartKeyBitrate(partKey string) int {
	base := strings.ToLower(path.Base(strings.TrimSpace(partKey)))
	rest, ok := strings.CutPrefix(base, "mp4-")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func discoverURLBitrate(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return 0, false
	}
	for k, vals := range u.Query() {
		if !strings.EqualFold(k, "videokbrate") || len(vals) == 0 {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(vals[0])); err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func rewriteDiscoverURLBitrate(raw string, bitrate int) (string, bool) {
	if raw == "" || bitrate <= 0 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	q := u.Query()
	found := false
	for k := range q {
		if strings.EqualFold(k, "videokbrate") {
			q.Set(k, strconv.Itoa(bitrate))
			found = true
		}
	}
	if !found {
		return "", false
	}
	u.RawQuery = q.Encode()
	rewritten := u.String()
	if !validPublicMediaURL(rewritten) {
		return "", false
	}
	return rewritten, true
}

func discoverHeight(height int, res string) int {
	if height > 0 {
		return height
	}
	s := strings.ToLower(strings.TrimSpace(res))
	switch s {
	case "4k", "2160", "2160p":
		return 2160
	case "sd":
		return 480
	}
	s = strings.TrimSuffix(s, "p")
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return 0
}

func discoverExtraID(item discoverExtraItem) string {
	for _, raw := range []string{string(item.RatingKey), item.Key, item.GUID} {
		s := strings.TrimSpace(raw)
		if i := strings.IndexByte(s, '?'); i >= 0 {
			s = s[:i]
		}
		if s == "" || strings.ContainsAny(s, "\r\n\t ") {
			continue
		}
		return s
	}
	return ""
}

func extractDiscoverHex(guid string) string {
	guid = strings.TrimSpace(guid)
	if !strings.HasPrefix(guid, "plex://") {
		return ""
	}
	rest := strings.TrimPrefix(guid, "plex://")
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return ""
	}
	hexID := strings.ToLower(rest[slash+1:])
	if !isDiscoverHex(hexID) {
		return ""
	}
	return hexID
}

func isDiscoverHex(s string) bool {
	if len(s) != 24 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func parseDiscoverKey(k string) (parentHex, extraID string, ok bool) {
	parentHex, extraID, ok = strings.Cut(strings.TrimSpace(k), "/")
	if !ok || !isDiscoverHex(parentHex) || extraID == "" || strings.ContainsAny(extraID, "\r\n\t ") {
		return "", "", false
	}
	return parentHex, extraID, true
}

func validDiscoverPartKey(k string) bool {
	k = strings.TrimSpace(k)
	return strings.HasPrefix(k, "/library/metadata/") &&
		!strings.Contains(k, "..") &&
		!strings.Contains(k, "://") &&
		!strings.ContainsAny(k, "\r\n\t ")
}

func validPublicMediaURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	for k := range u.Query() {
		if strings.EqualFold(k, "X-Plex-Token") {
			return false
		}
	}
	return true
}

func mapDiscoverSubtype(subtype string, extraType int) (string, int) {
	sub := strings.TrimSpace(subtype)
	switch strings.ToLower(sub) {
	case "trailer":
		return "trailer", 1
	case "teaser":
		return "teaser", 1
	case "deletedscene":
		return "deletedScene", 2
	case "interview":
		return "interview", 3
	case "musicvideo":
		return "musicVideo", 4
	case "behindthescenes":
		return "behindTheScenes", 5
	case "sceneorsample":
		return "sceneOrSample", 6
	case "livemusicvideo":
		return "liveMusicVideo", 7
	case "lyricmusicvideo":
		return "lyricMusicVideo", 8
	case "concert":
		return "concert", 9
	case "featurette":
		return "featurette", 10
	case "short":
		return "short", 11
	case "clip":
		return "clip", 4
	case "other":
		return "other", 12
	}
	switch extraType {
	case 1:
		return "trailer", 1
	case 2:
		return "deletedScene", 2
	case 3:
		return "interview", 3
	case 4:
		return "musicVideo", 4
	case 5:
		return "behindTheScenes", 5
	case 6:
		return "sceneOrSample", 6
	case 7:
		return "liveMusicVideo", 7
	case 8:
		return "lyricMusicVideo", 8
	case 9:
		return "concert", 9
	case 10:
		return "featurette", 10
	case 11:
		return "short", 11
	case 12:
		return "other", 12
	}
	return "clip", 4
}

func parseDiscoverDate(ymd string, addedAt int64) time.Time {
	if ymd = strings.TrimSpace(ymd); ymd != "" {
		if t, err := time.Parse("2006-01-02", ymd); err == nil {
			return t
		}
	}
	if addedAt > 0 {
		return time.Unix(addedAt, 0).UTC()
	}
	return time.Time{}
}

type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexString(n.String())
		return nil
	}
	return nil
}

type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = 0
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			*f = flexInt(v)
		}
	}
	return nil
}

type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = 0
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexInt64(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			*f = flexInt64(v)
		}
	}
	return nil
}
