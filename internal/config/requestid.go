package config

import (
	"fmt"
	"net/textproto"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Server-wide request identity rules: how Ether identifies itself to indexers
// and download hosts. Ether identifies honestly as Ether by default; owners
// may add rules for indexers that only accept specific apps.

// HeaderKV is one header in an ordered set.
type HeaderKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// KVOverride adds, changes or removes one header or query parameter.
type KVOverride struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Remove bool   `json:"remove,omitempty"`
}

// IdentityTargetKind classifies what requests an IdentityRule applies to.
type IdentityTargetKind string

const (
	TargetAllSearches      IdentityTargetKind = "all_searches"
	TargetIndexer          IdentityTargetKind = "indexer"
	TargetHost             IdentityTargetKind = "host"
	TargetNZBDownloads     IdentityTargetKind = "nzb_downloads"
	TargetTorrentDownloads IdentityTargetKind = "torrent_downloads"
)

// RequestKind identifies the outbound operation being resolved.
type RequestKind string

const (
	RequestSearch          RequestKind = "search"
	RequestNZBDownload     RequestKind = "nzb_download"
	RequestTorrentDownload RequestKind = "torrent_download"
)

// IdentityMediaType optionally scopes an IdentityRule to one media type.
// Empty ("") matches any media type, including requests whose media type is
// unknown (such as capabilities checks or unfiltered feeds).
type IdentityMediaType string

const (
	IdentityMediaAny   IdentityMediaType = ""
	IdentityMediaMovie IdentityMediaType = "movie"
	IdentityMediaTV    IdentityMediaType = "tv"
	IdentityMediaMusic IdentityMediaType = "music"
	IdentityMediaBooks IdentityMediaType = "books"
)

// NormalizeIdentityMediaType canonicalises an IdentityMediaType value.
func NormalizeIdentityMediaType(mt IdentityMediaType) IdentityMediaType {
	return IdentityMediaType(strings.ToLower(strings.TrimSpace(string(mt))))
}

// ValidIdentityMediaType reports whether mt is a supported media-type condition.
func ValidIdentityMediaType(mt IdentityMediaType) bool {
	switch NormalizeIdentityMediaType(mt) {
	case IdentityMediaAny, IdentityMediaMovie, IdentityMediaTV, IdentityMediaMusic, IdentityMediaBooks:
		return true
	default:
		return false
	}
}

// IdentityMediaTypeForCategory maps a standard Newznab/Torznab category ID to
// an IdentityMediaType. Returns IdentityMediaAny when unknown or outside the
// Movies/TV/Music/Books families.
func IdentityMediaTypeForCategory(cat int) IdentityMediaType {
	switch {
	case cat >= 2000 && cat < 3000:
		return IdentityMediaMovie
	case cat >= 5000 && cat < 6000:
		return IdentityMediaTV
	case cat == 3030 || (cat >= 7000 && cat < 8000):
		return IdentityMediaBooks
	case cat >= 3000 && cat < 4000:
		return IdentityMediaMusic
	default:
		return IdentityMediaAny
	}
}

// IdentityMediaTypeForCategories returns the common IdentityMediaType when
// every category in cats maps to the same non-empty media type. Mixed or
// unknown sets return IdentityMediaAny.
func IdentityMediaTypeForCategories(cats []int) IdentityMediaType {
	var common IdentityMediaType
	for _, cat := range cats {
		mt := IdentityMediaTypeForCategory(cat)
		if mt == IdentityMediaAny {
			return IdentityMediaAny
		}
		if common == IdentityMediaAny {
			common = mt
		} else if common != mt {
			return IdentityMediaAny
		}
	}
	return common
}

// DefaultCategoryForIdentityMedia returns the standard top-level Newznab
// category for mt, or 0 for IdentityMediaAny.
func DefaultCategoryForIdentityMedia(mt IdentityMediaType) int {
	switch NormalizeIdentityMediaType(mt) {
	case IdentityMediaMovie:
		return 2000
	case IdentityMediaTV:
		return 5000
	case IdentityMediaMusic:
		return 3000
	case IdentityMediaBooks:
		return 7000
	default:
		return 0
	}
}

// IdentityMediaTypeForSearch extracts the media type known by a search request.
func IdentityMediaTypeForSearch(req core.SearchRequest) IdentityMediaType {
	if strings.TrimSpace(req.Category) != "" {
		cats, err := core.SearchCategories(req.Category)
		if err != nil || len(cats) == 0 {
			return IdentityMediaAny
		}
		return IdentityMediaTypeForCategories(cats)
	}
	if req.Type == core.TypeEpisode || req.Type == core.TypeShow || req.Type == core.TypeSeason || req.Season > 0 {
		return IdentityMediaTV
	}
	if req.Type == core.TypeMovie {
		return IdentityMediaMovie
	}
	return IdentityMediaAny
}

// IdentityMediaTypeForRelease extracts the media type known by a release for
// NZB/torrent download identity resolution. movieCats and tvCats may hold an
// indexer's custom site-specific category IDs.
func IdentityMediaTypeForRelease(r core.Release, movieCats, tvCats []int) IdentityMediaType {
	if mt := IdentityMediaTypeForCategory(r.Category); mt != IdentityMediaAny {
		return mt
	}
	if r.Category != 0 {
		if slices.Contains(movieCats, r.Category) {
			return IdentityMediaMovie
		}
		if slices.Contains(tvCats, r.Category) {
			return IdentityMediaTV
		}
	}
	if len(r.Info.Seasons) > 0 || len(r.Info.Episodes) > 0 || r.Info.FullSeason {
		return IdentityMediaTV
	}
	if r.Target != nil && strings.TrimSpace(r.Target.MIME) != "" {
		mime := strings.ToLower(strings.TrimSpace(r.Target.MIME))
		switch {
		case mime == "application/epub+zip",
			mime == "application/pdf",
			mime == "application/x-mobipocket-ebook",
			strings.HasPrefix(mime, "application/vnd.comicbook"):
			return IdentityMediaBooks
		case strings.HasPrefix(mime, "audio/"):
			return IdentityMediaMusic
		}
	}
	return IdentityMediaAny
}

// IdentityRule sets the outbound HTTP identity for matching requests.
type IdentityRule struct {
	ID        string             `json:"id"`
	Target    IdentityTargetKind `json:"target"`
	IndexerID string             `json:"indexerId,omitempty"`
	Host      string             `json:"host,omitempty"`
	// Scope optionally narrows an indexer or host rule to "search" or
	// "download" (preserved when migrating split search/grab configs).
	Scope string `json:"scope,omitempty"`
	// MediaType optionally narrows this rule to one media type (movie, tv,
	// music, books). Empty matches any media type.
	MediaType IdentityMediaType `json:"mediaType,omitempty"`
	Profile   string            `json:"profile"`
	Headers   []KVOverride      `json:"headers,omitempty"`
	Params    []KVOverride      `json:"params,omitempty"`
}

// IdentityRequest describes one outbound indexer search or NZB/torrent
// download to resolve against ClientIdentity.Rules.
type IdentityRequest struct {
	Kind      RequestKind
	IndexerID string
	URL       string
	MediaType IdentityMediaType
}

// ClientProfile is one selectable compatibility header set.
type ClientProfile struct {
	ID      string
	Headers []HeaderKV
}

// clientVersions is the one table of client version strings.
// Bump a client here and every profile using it follows.
var clientVersions = map[string]string{
	"prowlarr":  "Prowlarr/2.7.0.5723 (alpine 3.24.4)",
	"sonarr":    "Sonarr/4.0.20.3014 (alpine 3.24.4)",
	"radarr":    "Radarr/7.0.3.10918 (alpine 3.24.4)",
	"nzbhydra2": "NZBHydra2 9.3.1",
	"sabnzbd":   "SABnzbd/5.1.3",
	"nzbget":    "nzbget/26.3",
	"browser":   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/158.0.0.0 Safari/537.36",
}

// ProfileUserAgent returns a profile's user agent ("" if unknown).
func ProfileUserAgent(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "ether", "custom":
		return DefaultUserAgent()
	default:
		return clientVersions[strings.ToLower(strings.TrimSpace(id))]
	}
}

const arrAccept = "application/rss+xml, text/rss+xml, application/xml, text/xml"

// ClientProfiles lists every selectable identity profile, in picker order.
// Compatibility profiles set only that client's header set (User-Agent,
// Accept, Accept-Encoding, Connection); Ether never reshapes query strings.
func ClientProfiles() []ClientProfile {
	arr := func(id string) ClientProfile {
		return ClientProfile{ID: id, Headers: []HeaderKV{
			{"User-Agent", clientVersions[id]},
			{"Accept", arrAccept},
			{"Accept-Encoding", "gzip"},
		}}
	}
	return []ClientProfile{
		{ID: "ether", Headers: []HeaderKV{{"User-Agent", DefaultUserAgent()}}},
		arr("sonarr"),
		arr("radarr"),
		arr("prowlarr"),
		{ID: "nzbhydra2", Headers: []HeaderKV{
			{"User-Agent", clientVersions["nzbhydra2"]},
			{"Accept", "application/xml"},
			{"Accept-Encoding", "gzip"},
		}},
		{ID: "sabnzbd", Headers: []HeaderKV{
			{"User-Agent", clientVersions["sabnzbd"]},
			{"Accept-Encoding", "gzip"},
			{"Connection", "close"},
		}},
		{ID: "nzbget", Headers: []HeaderKV{
			{"User-Agent", clientVersions["nzbget"]},
			{"Accept", "*/*"},
			{"Accept-Encoding", "gzip"},
			{"Connection", "close"},
		}},
		{ID: "browser", Headers: []HeaderKV{
			{"User-Agent", clientVersions["browser"]},
			{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"},
			{"Accept-Encoding", "gzip, deflate"},
		}},
		{ID: "custom", Headers: []HeaderKV{{"User-Agent", DefaultUserAgent()}}},
	}
}

// LookupProfile returns the named profile.
func LookupProfile(id string) (ClientProfile, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range ClientProfiles() {
		if p.ID == id {
			return p, true
		}
	}
	return ClientProfile{}, false
}

func applyHeaderOverrides(base []HeaderKV, ovs []KVOverride) ([]HeaderKV, map[string]bool) {
	out := append([]HeaderKV(nil), base...)
	custom := map[string]bool{}
	for _, ov := range ovs {
		name := strings.TrimSpace(ov.Name)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		idx := -1
		for i, h := range out {
			if strings.EqualFold(h.Name, name) {
				idx = i
				break
			}
		}
		switch {
		case ov.Remove:
			if idx >= 0 {
				out = append(out[:idx], out[idx+1:]...)
			}
			delete(custom, lower)
		case idx >= 0:
			out[idx] = HeaderKV{textproto.CanonicalMIMEHeaderKey(name), ov.Value}
			custom[lower] = true
		default:
			out = append(out, HeaderKV{textproto.CanonicalMIMEHeaderKey(name), ov.Value})
			custom[lower] = true
		}
	}
	return out, custom
}

// ResolveRule computes the wire Identity produced by rule.
func ResolveRule(rule IdentityRule, ruleIndex int) Identity {
	profID := strings.ToLower(strings.TrimSpace(rule.Profile))
	prof, ok := LookupProfile(profID)
	if !ok {
		prof = ClientProfile{ID: "ether", Headers: []HeaderKV{{"User-Agent", DefaultUserAgent()}}}
	}
	ordered, custom := applyHeaderOverrides(prof.Headers, rule.Headers)
	id := Identity{
		Ordered:       ordered,
		Params:        append([]KVOverride(nil), rule.Params...),
		Profile:       prof.ID,
		Headers:       map[string]string{},
		RuleID:        rule.ID,
		RuleIndex:     ruleIndex,
		RuleTarget:    rule.Target,
		RuleHost:      rule.Host,
		RuleScope:     rule.Scope,
		RuleMediaType: NormalizeIdentityMediaType(rule.MediaType),
		CustomHeaders: custom,
	}
	for _, h := range ordered {
		if strings.EqualFold(h.Name, "User-Agent") {
			id.UserAgent = h.Value
		} else {
			id.Headers[h.Name] = h.Value
		}
	}
	if id.UserAgent == "" {
		id.UserAgent = DefaultUserAgent()
	}
	return id
}

// DefaultIdentity returns the implicit last rule: Ether's honest default.
func DefaultIdentity() Identity {
	ua := DefaultUserAgent()
	return Identity{
		UserAgent: ua,
		Headers:   map[string]string{},
		Ordered:   []HeaderKV{{"User-Agent", ua}},
		Profile:   "ether",
		RuleIndex: -1,
	}
}

// ResolveRequest resolves the winning rule for req.
// Precedence (most specific wins; within a target tier a specific mediaType
// beats Any; list order breaks ties within the same tier):
//  1. specific indexer (TargetIndexer)
//  2. exact host (TargetHost without "*.")
//  3. wildcard host (TargetHost with "*.host")
//  4. download type (TargetNZBDownloads / TargetTorrentDownloads) or all searches (TargetAllSearches)
//  5. implicit last rule: Ether default
func (p ClientIdentity) ResolveRequest(req IdentityRequest) Identity {
	reqHost := extractHost(req.URL)
	bestRank := 0
	bestIdx := -1
	for i, rule := range p.Rules {
		rank := ruleSpecificity(rule, req, reqHost)
		if rank > bestRank {
			bestRank = rank
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return DefaultIdentity()
	}
	return ResolveRule(p.Rules[bestIdx], bestIdx)
}

// ResolveIndexer resolves the search or download identity for ix without a
// specific media type (e.g. capabilities checks or unfiltered feeds).
func (p ClientIdentity) ResolveIndexer(ix Indexer, kind RequestKind, rawURL string) Identity {
	return p.ResolveIndexerMedia(ix, kind, rawURL, IdentityMediaAny)
}

// ResolveIndexerMedia resolves the search or download identity for ix and a
// known media type.
func (p ClientIdentity) ResolveIndexerMedia(ix Indexer, kind RequestKind, rawURL string, media IdentityMediaType) Identity {
	if strings.TrimSpace(rawURL) == "" {
		rawURL = ix.URL
	}
	return p.ResolveRequest(IdentityRequest{
		Kind:      kind,
		IndexerID: ix.ID,
		URL:       rawURL,
		MediaType: media,
	})
}

func ruleSpecificity(rule IdentityRule, req IdentityRequest, reqHost string) int {
	ruleMedia := NormalizeIdentityMediaType(rule.MediaType)
	reqMedia := NormalizeIdentityMediaType(req.MediaType)
	if ruleMedia != IdentityMediaAny && ruleMedia != reqMedia {
		return 0
	}
	targetRank := 0
	switch rule.Target {
	case TargetIndexer:
		if rule.IndexerID == "" || rule.IndexerID != req.IndexerID {
			return 0
		}
		if !scopeMatches(rule.Scope, req.Kind) {
			return 0
		}
		targetRank = 4
	case TargetHost:
		if reqHost == "" || !scopeMatches(rule.Scope, req.Kind) {
			return 0
		}
		pat := normHostPattern(rule.Host)
		if pat == "" {
			return 0
		}
		if suffix, wildcard := strings.CutPrefix(pat, "*."); wildcard {
			if suffix != "" && strings.HasSuffix(reqHost, "."+suffix) {
				targetRank = 2
			}
		} else if reqHost == pat {
			targetRank = 3
		}
	case TargetNZBDownloads:
		if req.Kind == RequestNZBDownload {
			targetRank = 1
		}
	case TargetTorrentDownloads:
		if req.Kind == RequestTorrentDownload {
			targetRank = 1
		}
	case TargetAllSearches:
		if req.Kind == RequestSearch {
			targetRank = 1
		}
	}
	if targetRank == 0 {
		return 0
	}
	mediaBonus := 0
	if ruleMedia != IdentityMediaAny {
		mediaBonus = 1
	}
	return targetRank*2 + mediaBonus
}

func scopeMatches(scope string, kind RequestKind) bool {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "":
		return true
	case "search":
		return kind == RequestSearch
	case "download":
		return kind == RequestNZBDownload || kind == RequestTorrentDownload
	default:
		return false
	}
}

func extractHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	}
	u, err := url.Parse("https://" + raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

func normHostPattern(raw string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
}

var hostLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidHostPattern reports whether raw is a valid exact host ("api.example.com")
// or wildcard host ("*.example.com") pattern.
func ValidHostPattern(raw string) bool {
	pat := normHostPattern(raw)
	if pat == "" || len(pat) > 253 {
		return false
	}
	if strings.ContainsAny(pat, " /:\\?#\t\r\n") {
		return false
	}
	host := pat
	if suffix, wildcard := strings.CutPrefix(pat, "*."); wildcard {
		if suffix == "" || strings.Contains(suffix, "*") {
			return false
		}
		host = suffix
	} else if strings.Contains(pat, "*") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !hostLabelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// ApplyParams applies the identity's query parameter overrides onto params and
// encodes them with standard url.Values.Encode() ordering (no query mimicry).
// apikey and t can be changed by an override but never removed.
func (id Identity) ApplyParams(params url.Values) string {
	v := url.Values{}
	for k, vs := range params {
		v[k] = append([]string(nil), vs...)
	}
	for _, ov := range id.Params {
		name := strings.TrimSpace(ov.Name)
		if name == "" {
			continue
		}
		if ov.Remove {
			if strings.EqualFold(name, "apikey") || strings.EqualFold(name, "t") {
				continue
			}
			v.Del(name)
		} else {
			v.Set(name, ov.Value)
		}
	}
	return v.Encode()
}

// Validate checks every rule in ClientIdentity.
func (p ClientIdentity) Validate() []IdentityFieldError {
	var errs []IdentityFieldError
	if len(p.Rules) > 64 {
		return append(errs, IdentityFieldError{"rules", "at most 64 rules"})
	}
	seenIDs := map[string]bool{}
	for i, r := range p.Rules {
		prefix := fmt.Sprintf("rules[%d]", i)
		add := func(field, format string, args ...any) {
			errs = append(errs, IdentityFieldError{prefix + "." + field, fmt.Sprintf(format, args...)})
		}
		id := strings.TrimSpace(r.ID)
		if id == "" {
			add("id", "is required")
		} else if len(id) > 64 || strings.ContainsAny(id, "\r\n\x00") {
			add("id", "is not valid")
		} else if seenIDs[id] {
			add("id", "duplicate rule id %q", id)
		} else {
			seenIDs[id] = true
		}

		switch r.Target {
		case TargetAllSearches, TargetNZBDownloads, TargetTorrentDownloads:
			if strings.TrimSpace(r.Scope) != "" {
				add("scope", "is only valid on indexer or host rules")
			}
		case TargetIndexer:
			if strings.TrimSpace(r.IndexerID) == "" {
				add("indexerId", "choose an indexer")
			}
		case TargetHost:
			if !ValidHostPattern(r.Host) {
				add("host", "must be a hostname or *.domain pattern")
			}
		default:
			add("target", "unknown rule target %q", r.Target)
		}

		if s := strings.ToLower(strings.TrimSpace(r.Scope)); s != "" && s != "search" && s != "download" {
			add("scope", "must be empty, search, or download")
		}

		if !ValidIdentityMediaType(r.MediaType) {
			add("mediaType", "must be empty, movie, tv, music, or books")
		}

		if _, ok := LookupProfile(r.Profile); !ok {
			add("profile", "unknown identity profile %q", r.Profile)
		}

		if len(r.Headers) > 32 {
			add("headers", "at most 32 header edits")
		} else {
			seenH := map[string]bool{}
			for _, ov := range r.Headers {
				name := strings.TrimSpace(ov.Name)
				if !headerNameRE.MatchString(name) || len(name) > 64 {
					add("headers", "%q is not a valid header name", ov.Name)
					continue
				}
				lower := strings.ToLower(name)
				if seenH[lower] {
					add("headers", "%q is listed twice", name)
					continue
				}
				seenH[lower] = true
				switch lower {
				case "host", "content-length", "transfer-encoding":
					add("headers", "%q is controlled by the HTTP transport and cannot be edited", name)
					continue
				}
				if !ov.Remove {
					maxLen := 1024
					if lower == "user-agent" {
						maxLen = 512
					}
					if msg := invalidHeaderValue(ov.Value, maxLen); msg != "" {
						add("headers", "%q %s", name, msg)
					}
				}
			}
		}

		if len(r.Params) > 32 {
			add("params", "at most 32 parameter edits")
		} else {
			seenP := map[string]bool{}
			for _, ov := range r.Params {
				name := strings.TrimSpace(ov.Name)
				if name == "" || len(name) > 64 || strings.ContainsAny(name, " \t\r\n\x00&=") {
					add("params", "%q is not a valid parameter name", ov.Name)
					continue
				}
				lower := strings.ToLower(name)
				if seenP[lower] {
					add("params", "%q is listed twice", name)
					continue
				}
				seenP[lower] = true
				if ov.Remove && (lower == "apikey" || lower == "t") {
					add("params", "%q cannot be removed", name)
				}
				if len(ov.Value) > 1024 || strings.ContainsAny(ov.Value, "\r\n\x00") {
					add("params", "%q has an invalid value", name)
				}
			}
		}
	}
	return errs
}

// SensitiveHeader reports whether a header's value may carry a credential
// that must be encrypted at rest, redacted in API responses, and masked in previews.
func SensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key", "apikey":
		return true
	}
	for _, kw := range []string{"token", "secret", "password", "passkey", "apikey", "api-key", "auth"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// SensitiveParam reports whether a query parameter's value may carry a secret
// that must be encrypted at rest, redacted in API responses, and masked in previews.
func SensitiveParam(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "apikey", "api_key", "key", "token", "passkey", "password", "pass", "secret", "auth", "rsskey":
		return true
	}
	for _, kw := range []string{"apikey", "api_key", "token", "secret", "password", "passkey"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ProfileForUserAgent maps a saved user agent to the profile that sends it,
// used for one-time migration from legacy userAgent configs.
func ProfileForUserAgent(ua string) (string, bool) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "", false
	}
	for id, v := range clientVersions {
		if ua == v {
			return id, true
		}
	}
	for id, v := range legacyPresetUserAgents {
		if ua == v {
			return id, true
		}
	}
	return "", false
}
