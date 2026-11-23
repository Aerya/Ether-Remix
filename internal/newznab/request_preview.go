package newznab

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
)

const previewMaskSentinel = "__ETHER_MASKED_SECRET__"
const previewMaskDisplay = "••••"

// PreviewInput specifies what PreviewRequestIdentity renders.
// When Rule is set, it previews that (possibly unsaved) rule with sample
// search and download requests appropriate to the rule's target.
// Otherwise it previews the effective search and download requests for Indexer.
type PreviewInput struct {
	Rule          *config.IdentityRule
	Rules         []config.IdentityRule
	HasRules      bool
	Indexer       *config.Indexer
	SavedIndexers []config.Indexer
	SavedPolicy   config.ClientIdentity
}

// RequestPreview is what the console renders as "What the indexer sees":
// the exact request line and ordered headers for a sample search and a sample
// download. API keys and sensitive header/parameter values are always masked.
type RequestPreview struct {
	Profile       string           `json:"profile"`
	MatchedRuleID string           `json:"matchedRuleId,omitempty"`
	Search        PreviewCall      `json:"search"`
	Download      PreviewCall      `json:"download"`
	DownloadKind  string           `json:"downloadKind"` // "nzb" or "torrent"
	Profiles      []PreviewProfile `json:"profiles"`
}

// PreviewCall is one rendered sample HTTP request.
type PreviewCall struct {
	RequestLine   string            `json:"requestLine"`
	Headers       []config.HeaderKV `json:"headers"`
	Profile       string            `json:"profile"`
	MatchedRuleID string            `json:"matchedRuleId,omitempty"`
}

// PreviewProfile describes one selectable identity profile for the picker.
type PreviewProfile struct {
	ID        string `json:"id"`
	UserAgent string `json:"userAgent"`
}

// PreviewRequestIdentity builds the server-side preview for a rule or indexer.
// It performs no network I/O and never emits plaintext API keys or secrets.
func PreviewRequestIdentity(in PreviewInput) (*RequestPreview, error) {
	policy := in.SavedPolicy
	if in.HasRules {
		policy = config.ClientIdentity{Rules: in.Rules}
	}

	var secrets []string
	addSecret := func(s string) {
		if s = strings.TrimSpace(s); s != "" && s != previewMaskDisplay {
			secrets = append(secrets, s)
		}
	}
	for _, ix := range in.SavedIndexers {
		addSecret(ix.APIKey)
	}

	var draft config.Indexer
	downloadKind := "nzb"
	dlReqKind := config.RequestNZBDownload
	previewMedia := config.IdentityMediaAny
	if in.Rule != nil {
		previewMedia = config.NormalizeIdentityMediaType(in.Rule.MediaType)
	}

	pickSaved := func(wantTorrent bool, preferUnmatched bool) (config.Indexer, bool) {
		isShadowed := func(ix config.Indexer) bool {
			for _, r := range policy.Rules {
				if r.Target == config.TargetIndexer && r.IndexerID == ix.ID {
					rm := config.NormalizeIdentityMediaType(r.MediaType)
					if rm == config.IdentityMediaAny || rm == previewMedia {
						return true
					}
				}
			}
			return false
		}
		if preferUnmatched {
			for _, ix := range in.SavedIndexers {
				if !ix.IsBuiltin() && !ix.IsStremio() && ix.IsTorrent() == wantTorrent && strings.TrimSpace(ix.URL) != "" && !isShadowed(ix) {
					return ix, true
				}
			}
			return config.Indexer{}, false
		}
		for _, ix := range in.SavedIndexers {
			if !ix.IsBuiltin() && !ix.IsStremio() && ix.IsTorrent() == wantTorrent && strings.TrimSpace(ix.URL) != "" {
				return ix, true
			}
		}
		for _, ix := range in.SavedIndexers {
			if strings.TrimSpace(ix.URL) != "" && !ix.IsStremio() {
				return ix, true
			}
		}
		return config.Indexer{}, false
	}

	if in.Indexer != nil {
		draft = *in.Indexer
		addSecret(draft.APIKey)
		if draft.IsTorrent() {
			downloadKind = "torrent"
			dlReqKind = config.RequestTorrentDownload
		}
	} else if in.Rule != nil {
		switch in.Rule.Target {
		case config.TargetIndexer:
			for _, ix := range in.SavedIndexers {
				if ix.ID == in.Rule.IndexerID {
					draft = ix
					break
				}
			}
			if draft.ID == "" {
				draft.ID = in.Rule.IndexerID
			}
			if draft.IsTorrent() {
				downloadKind = "torrent"
				dlReqKind = config.RequestTorrentDownload
			}
		case config.TargetHost:
			sampleHost := strings.TrimSpace(in.Rule.Host)
			if suffix, ok := strings.CutPrefix(strings.ToLower(sampleHost), "*."); ok && suffix != "" {
				sampleHost = "api." + suffix
			}
			if sampleHost != "" {
				draft.URL = "https://" + sampleHost
			}
		case config.TargetTorrentDownloads:
			downloadKind = "torrent"
			dlReqKind = config.RequestTorrentDownload
			if found, ok := pickSaved(true, true); ok {
				draft = found
			} else {
				draft.URL = "https://tracker.example.org"
				draft.Protocol = "torrent"
			}
		case config.TargetNZBDownloads:
			if found, ok := pickSaved(false, true); ok {
				draft = found
			}
		default:
			if found, ok := pickSaved(false, true); ok {
				draft = found
			}
		}
	}

	if strings.TrimSpace(draft.URL) == "" {
		if downloadKind == "torrent" {
			draft.URL = "https://tracker.example.org"
		} else {
			draft.URL = "https://indexer.example.com"
		}
	}
	draft.APIKey = previewMaskSentinel

	c := New(draft, nil, Options{ClientIdentity: policy, SearchCacheTTL: -1})

	// Build sample download URL before resolving download identity so host rules match accurately.
	sampleBase := strings.TrimRight(strings.TrimSpace(draft.URL), "/")
	sampleTorrentURL := sampleBase + "/download/example.torrent"

	searchID := policy.ResolveIndexerMedia(draft, config.RequestSearch, draft.URL, previewMedia)
	downloadURLForMatch := draft.URL
	if downloadKind == "torrent" {
		downloadURLForMatch = sampleTorrentURL
	}
	downloadID := policy.ResolveIndexerMedia(draft, dlReqKind, downloadURLForMatch, previewMedia)

	// When previewing a specific (possibly unsaved) rule, apply that rule directly
	// to whichever operation(s) it targets so edits preview live before saving.
	if in.Rule != nil {
		ruleIdx := -1
		for i, r := range policy.Rules {
			if r.ID != "" && r.ID == in.Rule.ID {
				ruleIdx = i
				break
			}
		}
		resolved := config.ResolveRule(*in.Rule, ruleIdx)
		switch in.Rule.Target {
		case config.TargetAllSearches:
			searchID = resolved
		case config.TargetNZBDownloads, config.TargetTorrentDownloads:
			downloadID = resolved
		case config.TargetIndexer, config.TargetHost:
			scope := strings.ToLower(strings.TrimSpace(in.Rule.Scope))
			if scope != "download" {
				searchID = resolved
			}
			if scope != "search" {
				downloadID = resolved
			}
		}
	}

	sanitizeParams := func(id config.Identity) config.Identity {
		if len(id.Params) == 0 {
			return id
		}
		cp := id
		cp.Params = make([]config.KVOverride, len(id.Params))
		for i, ov := range id.Params {
			if !ov.Remove && config.SensitiveParam(ov.Name) && strings.TrimSpace(ov.Value) != "" {
				addSecret(ov.Value)
				ov.Value = previewMaskSentinel
			}
			cp.Params[i] = ov
		}
		return cp
	}

	safeSearchID := sanitizeParams(searchID)
	safeDownloadID := sanitizeParams(downloadID)

	searchURL, err := c.apiURLFor(sampleSearchParams(previewMedia), safeSearchID)
	if err != nil {
		return nil, err
	}

	var downloadURL string
	if downloadKind == "torrent" {
		downloadURL = safeDownloadID.ApplyToURL(sampleTorrentURL)
	} else {
		downloadURL, err = c.apiURLFor(url.Values{"t": {"get"}, "id": {"example-guid"}}, safeDownloadID)
		if err != nil {
			return nil, err
		}
	}

	maskURI := func(s string) string {
		s = strings.ReplaceAll(s, url.QueryEscape(previewMaskSentinel), previewMaskDisplay)
		s = strings.ReplaceAll(s, previewMaskSentinel, previewMaskDisplay)
		for _, sec := range secrets {
			s = strings.ReplaceAll(s, url.QueryEscape(sec), previewMaskDisplay)
			s = strings.ReplaceAll(s, sec, previewMaskDisplay)
		}
		return s
	}

	buildCall := func(raw string, id config.Identity) (PreviewCall, error) {
		u, err := url.Parse(raw)
		if err != nil {
			return PreviewCall{}, err
		}
		headers := make([]config.HeaderKV, 0, len(id.Ordered))
		for _, h := range id.Ordered {
			v := h.Value
			if config.SensitiveHeader(h.Name) && strings.TrimSpace(v) != "" {
				v = previewMaskDisplay
			}
			headers = append(headers, config.HeaderKV{Name: h.Name, Value: v})
		}
		return PreviewCall{
			RequestLine:   fmt.Sprintf("GET %s HTTP/1.1\nHost: %s", maskURI(u.RequestURI()), u.Host),
			Headers:       headers,
			Profile:       id.Profile,
			MatchedRuleID: id.RuleID,
		}, nil
	}

	sc, err := buildCall(searchURL, searchID)
	if err != nil {
		return nil, err
	}
	dc, err := buildCall(downloadURL, downloadID)
	if err != nil {
		return nil, err
	}

	primary := searchID
	if in.Rule != nil && (in.Rule.Target == config.TargetNZBDownloads || in.Rule.Target == config.TargetTorrentDownloads || strings.EqualFold(in.Rule.Scope, "download")) {
		primary = downloadID
	}

	out := &RequestPreview{
		Profile:       primary.Profile,
		MatchedRuleID: primary.RuleID,
		Search:        sc,
		Download:      dc,
		DownloadKind:  downloadKind,
	}
	for _, p := range config.ClientProfiles() {
		out.Profiles = append(out.Profiles, PreviewProfile{ID: p.ID, UserAgent: config.ProfileUserAgent(p.ID)})
	}
	return out, nil
}

func sampleSearchParams(media config.IdentityMediaType) url.Values {
	switch config.NormalizeIdentityMediaType(media) {
	case config.IdentityMediaMovie:
		return url.Values{
			"t": {"movie"}, "imdbid": {"0133093"},
			"cat": {"2000"}, "extended": {"1"}, "limit": {"100"},
		}
	case config.IdentityMediaMusic:
		return url.Values{
			"t": {"search"}, "q": {"Example Album"},
			"cat": {"3000"}, "extended": {"1"}, "limit": {"100"},
		}
	case config.IdentityMediaBooks:
		return url.Values{
			"t": {"search"}, "q": {"Example Book"},
			"cat": {"7000"}, "extended": {"1"}, "limit": {"100"},
		}
	case config.IdentityMediaTV:
		return url.Values{
			"t": {"tvsearch"}, "q": {"Example Show"}, "season": {"1"}, "ep": {"1"},
			"cat": {"5000"}, "extended": {"1"}, "limit": {"100"},
		}
	default:
		// Any media type (the default identity and Any rules): an
		// uncategorised search, which no media-type rule matches, so the
		// default card never shows a TV search a TV rule would claim.
		return url.Values{
			"t": {"search"}, "q": {"Example"}, "extended": {"1"}, "limit": {"100"},
		}
	}
}
