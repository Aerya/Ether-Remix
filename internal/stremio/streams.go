package stremio

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/animemap"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

const (
	nzbTimeout = 60 * time.Second
	maxNZB     = 64 << 20
	// maxStreamHeaders bounds the request headers an addon may ask for.
	maxStreamHeaders = 16
)

// idLookup is the stream request for a search: the addon's content kind
// ("movie" or "series") and id, whether whole-season packs are wanted, and
// for an anime database id the episode's number inside that entry.
type idLookup struct {
	kind, id       string
	season         int
	packs          bool
	entryEpisode   int
	specialEpisode int
}

// animeDatabases are the anime id prefixes Ether can map TMDB titles to,
// in the order they are tried.
var animeDatabases = []string{animemap.Kitsu, animemap.MAL, animemap.AniList, animemap.AniDB}

func (b *Backend) lookupManifest(ctx context.Context) *Manifest {
	m := b.currentManifest(ctx)
	if m == nil {
		m = &Manifest{Resources: Resources{{Name: ResourceStream, Types: []string{TypeMovie, TypeSeries}, IDPrefixes: []string{"tt"}}}}
	}
	return m
}

func (b *Backend) lookupIDs(ctx context.Context, req core.SearchRequest) []idLookup {
	m := b.lookupManifest(ctx)
	var anime AnimeIDs
	if req.Anime {
		anime = b.svc.animeIDs()
	}
	looks := LookupStreamList(ctx, *m, req, anime, b.svc.specials)
	out := make([]idLookup, len(looks))
	for i, look := range looks {
		out[i] = idLookup{kind: look.Type, id: look.ID, season: look.Season, packs: look.Packs, entryEpisode: look.EntryEpisode, specialEpisode: look.SpecialEpisode}
	}
	return out
}

// origin is the addon's scheme://host[:port], without credentials.
func (b *Backend) origin() string {
	return (&url.URL{Scheme: b.base.Scheme, Host: b.base.Host}).String()
}

// httpLink returns raw when it is an absolute http(s) URL, else "".
func httpLink(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}

// requestHeaders are the headers the addon says its stream URL needs
// (behaviorHints.proxyHeaders.request), bounded and stripped of headers
// Ether controls itself.
func (s *stream) requestHeaders() map[string]string {
	var out map[string]string
	for k, v := range s.BehaviorHints.ProxyHeaders.Request {
		k = http.CanonicalHeaderKey(strings.TrimSpace(k))
		switch k {
		case "", "Range", "Host", "Content-Length", "Connection", "Transfer-Encoding", "Te", "Upgrade", "Proxy-Authorization", "Proxy-Connection":
			continue
		}
		if strings.ContainsAny(k+v, "\r\n\x00") || len(out) == maxStreamHeaders {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// linkIdentity identifies an HTTP or NZB stream within one configured
// addon without its URL, which can carry credentials and often changes
// between answers: the file's name and size, the debrid service the addon
// labels it with, its file index or requested episode, and the link's host.
func linkIdentity(name string, size int64, link string, s *stream, req core.SearchRequest) string {
	host := ""
	if u, err := url.Parse(link); err == nil {
		host = strings.ToLower(u.Host)
	}
	cached, uncached := debridHints(s.Name)
	h := sha1.Sum([]byte(strings.ToLower(name) + "|" + strconv.FormatInt(size, 10) + "|" + host + "|" +
		strings.Join(cached, ",") + "|" + strings.Join(uncached, ",") + "|" + guidSuffix(s, req, false)))
	return hex.EncodeToString(h[:10])
}

// streamLanguages adds the audio languages a stream's text declares
// outside its release name: flag emoji (as Torrentio prints them) and
// "Multi Audio" / "Dual Audio".
func streamLanguages(langs []string, text string) []string {
	add := func(l string) {
		for _, x := range langs {
			if x == l {
				return
			}
		}
		langs = append(langs, l)
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "multi audio") || strings.Contains(lower, "dual audio") {
		add(releaseparse.LanguageMulti)
	}
	for _, l := range releaseparse.FlagLanguages(text) {
		add(l)
	}
	return langs
}

// entryEpisodeIdentity reads an anime release numbered inside the
// database entry the addon was asked for ("Show Final Season Part 2 - 04"
// for that entry's fourth episode) as the requested episode. Only a
// season-less release whose own number is that entry episode is read so;
// every other name keeps its parsed identity.
func entryEpisodeIdentity(info core.ReleaseInfo, req core.SearchRequest, entryEpisode int) core.ReleaseInfo {
	if entryEpisode <= 0 || req.Type != core.TypeEpisode || len(info.Seasons) > 0 || info.FullSeason {
		return info
	}
	if !releaseparse.EpisodeMatchesAbsolute(info, entryEpisode) || entryEpisode == req.AbsoluteEpisode {
		return info
	}
	info.Seasons, info.Episodes = []int{req.Season}, []int{req.Episode}
	// The parsed absolute number was the entry-local one just consumed;
	// keeping it would name a different series-absolute episode.
	info.AbsoluteEpisodes = nil
	return info
}

// FetchNZB implements newznab.Backend for an addon's Usenet streams: it
// downloads the stream's NZB. The addon's own origin is trusted (a
// self-hosted addon on the LAN); any other host, and every redirect hop,
// must be public. Errors name the host only. Whatever the server answers
// is transient (core.ErrSourceUnavailable): a login page or an oversized
// body today says nothing permanent about the release. Only a release
// without a usable NZB link is rejected (core.ErrReleaseRejected).
func (b *Backend) FetchNZB(ctx context.Context, r core.Release, reserve func(context.Context, string) error) ([]byte, error) {
	return FetchNZBWithIdentity(egress.WithSourceProxy(ctx, b.requestProxy), b.svc.nzb, b.origin(), b.instance, b.svc.clientIdentity(), r, reserve)
}

// FetchNZB downloads an add-on Usenet stream's NZB with client using Ether's
// default identity. Callers with server-wide request identity rules use
// FetchNZBWithIdentity.
func FetchNZB(ctx context.Context, client *http.Client, origin string, r core.Release, reserve func(context.Context, string) error) ([]byte, error) {
	return FetchNZBWithIdentity(ctx, client, origin, "", config.ClientIdentity{}, r, reserve)
}

// FetchNZBWithIdentity downloads an add-on Usenet stream's NZB with client,
// resolving policy for RequestNZBDownload against indexerID and r.NZBURL, and
// trusting only the add-on's own origin (see Backend.FetchNZB). Installed
// add-ons (internal/plugins/adapters) fetch their NZBs through it too.
func FetchNZBWithIdentity(ctx context.Context, client *http.Client, origin, indexerID string, policy config.ClientIdentity, r core.Release, reserve func(context.Context, string) error) ([]byte, error) {
	if r.Protocol != core.ProtocolUsenet || r.NZBURL == "" {
		return nil, fmt.Errorf("stremio: %q has no NZB to fetch: %w", r.Name, core.ErrReleaseRejected)
	}
	link, err := url.Parse(r.NZBURL)
	if err != nil || (link.Scheme != "http" && link.Scheme != "https") || link.Host == "" {
		return nil, fmt.Errorf("stremio: %q has an invalid NZB link: %w", r.Name, core.ErrReleaseRejected)
	}
	if err := reserve(ctx, "grab"); err != nil {
		return nil, err
	}
	id := policy.ResolveRequest(config.IdentityRequest{
		Kind:      config.RequestNZBDownload,
		IndexerID: indexerID,
		URL:       r.NZBURL,
		MediaType: config.IdentityMediaTypeForRelease(r, nil, nil),
	})
	targetURL := id.ApplyToURL(r.NZBURL)
	req, err := http.NewRequestWithContext(safefetch.WithTrustedOriginFor(ctx, origin, targetURL), http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("stremio: NZB from %s: invalid request", link.Host)
	}
	applyNZBIdentity(req, id)
	hc := client
	if len(id.Headers) > 0 || len(id.Ordered) > 1 {
		copyClient := *client
		initialURL := req.URL.String()
		priorRedirect := copyClient.CheckRedirect
		copyClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if !safefetch.SameOrigin(initialURL, next.URL.String()) {
				for k := range id.Headers {
					next.Header.Del(k)
				}
				for _, h := range id.Ordered {
					if !strings.EqualFold(h.Name, "User-Agent") {
						next.Header.Del(h.Name)
					}
				}
			}
			if priorRedirect != nil {
				return priorRedirect(next, via)
			}
			if len(via) >= 10 {
				return errors.New("stremio: too many redirects")
			}
			return nil
		}
		hc = &copyClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("stremio: NZB from %s: %w: %v", link.Host, core.ErrSourceUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("stremio: NZB from %s: HTTP %d: %w", link.Host, resp.StatusCode, core.ErrSourceUnavailable)
	}
	reader, derr := decodeNZBBody(resp)
	if derr != nil {
		return nil, fmt.Errorf("stremio: NZB from %s: %w: %v", link.Host, core.ErrSourceUnavailable, derr)
	}
	if reader != resp.Body {
		defer reader.Close()
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxNZB+1))
	if err != nil {
		return nil, fmt.Errorf("stremio: NZB from %s: %w: %v", link.Host, core.ErrSourceUnavailable, err)
	}
	if len(body) > maxNZB {
		return nil, fmt.Errorf("stremio: NZB from %s is larger than %d bytes: %w", link.Host, maxNZB, core.ErrSourceUnavailable)
	}
	if !bytes.Contains(bytes.ToLower(body[:min(len(body), 64<<10)]), []byte("<nzb")) {
		return nil, fmt.Errorf("stremio: %s did not answer with an NZB: %w", link.Host, core.ErrSourceUnavailable)
	}
	return body, nil
}

func applyNZBIdentity(req *http.Request, id config.Identity) {
	req.Header.Set("User-Agent", id.UserAgent)
	for _, h := range id.Ordered {
		switch {
		case strings.EqualFold(h.Name, "User-Agent"):
			req.Header.Set("User-Agent", h.Value)
		case strings.EqualFold(h.Name, "Connection"):
			if strings.EqualFold(h.Value, "close") {
				req.Close = true
			}
		default:
			req.Header.Set(h.Name, h.Value)
		}
	}
}

func decodeNZBBody(resp *http.Response) (io.ReadCloser, error) {
	if resp.Uncompressed {
		return resp.Body, nil
	}
	switch strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))) {
	case "", "identity":
		return resp.Body, nil
	case "gzip":
		return gzip.NewReader(resp.Body)
	case "deflate":
		return flate.NewReader(resp.Body), nil
	default:
		return nil, fmt.Errorf("unsupported Content-Encoding %q", resp.Header.Get("Content-Encoding"))
	}
}

// DefaultNZBClient is the guarded client add-on NZBs are downloaded with.
func DefaultNZBClient() *http.Client {
	return (&safefetch.Guard{}).Client(egress.Client(egress.CapStremio, nzbTimeout))
}

// AnimeIDs is the service's anime id mapping, loaded the first time an
// add-on needs it; installed add-ons share it (internal/plugins/adapters).
func (s *Service) AnimeIDs() AnimeIDs { return lazyAnime{s} }

// LookupCour maps an offset TMDB anime season episode to its cour and TVDB
// season/episode numbering when present.
func (s *Service) LookupCour(ctx context.Context, req core.SearchRequest) (core.CourMapping, bool, error) {
	if cl, ok := s.animeIDs().(interface {
		LookupCour(context.Context, core.SearchRequest) (core.CourMapping, bool, error)
	}); ok {
		return cl.LookupCour(ctx, req)
	}
	return core.CourMapping{}, false, nil
}

type lazyAnime struct{ s *Service }

func (l lazyAnime) Lookup(ctx context.Context, req core.SearchRequest, db string) (int64, int, bool, error) {
	return l.s.animeIDs().Lookup(ctx, req, db)
}
