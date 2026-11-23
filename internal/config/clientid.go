package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/httpclient"
)

// Identity is the resolved client identity Ether writes on the wire.
type Identity struct {
	UserAgent string
	Headers   map[string]string
	// Ordered is the full header set in profile order (User-Agent included).
	Ordered []HeaderKV
	// Params are query-parameter overrides, applied by ApplyParams / ApplyToURL.
	Params  []KVOverride
	Profile string
	// Matched rule metadata (RuleID == "" and RuleIndex == -1 when the implicit
	// Ether default matched).
	RuleID        string
	RuleIndex     int
	RuleTarget    IdentityTargetKind
	RuleHost      string
	RuleScope     string
	RuleMediaType IdentityMediaType
	// CustomHeaders records lowercase header names explicitly set by owner
	// overrides so origin confinement can separate them from standard headers.
	CustomHeaders map[string]bool
}

// Fingerprint returns a deterministic digest of the wire identity (profile,
// ordered headers, and parameter overrides) for search cache separation.
func (id Identity) Fingerprint() string {
	h := sha256.New()
	fmt.Fprintf(h, "p:%s|ua:%s|", id.Profile, id.UserAgent)
	for _, kv := range id.Ordered {
		fmt.Fprintf(h, "h:%s=%s|", kv.Name, kv.Value)
	}
	for _, ov := range id.Params {
		fmt.Fprintf(h, "q:%s=%s:%t|", ov.Name, ov.Value, ov.Remove)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ClientIdentity is the server-wide ordered list of request-identity rules.
// An empty Rules slice uses Ether's honest default identity everywhere.
type ClientIdentity struct {
	Rules []IdentityRule `json:"rules,omitempty"`
}

// IdentityFieldError is one rejected client-identity setting, keyed like the
// config field it belongs to (e.g. "rules[0].host").
type IdentityFieldError struct {
	Field   string
	Message string
}

// DefaultUserAgent returns Ether's honest outbound User-Agent for indexers and
// NZB/torrent downloads:
// "Ether/<version> (+https://github.com/braedonsaunders/ether/blob/main/docs/indexer-operators.md)".
func DefaultUserAgent() string {
	return httpclient.IndexerUserAgent()
}

var headerNameRE = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")

// invalidHeaderValue returns a message when value cannot go on the wire.
func invalidHeaderValue(value string, max int) string {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "must not contain line breaks"
	}
	for i := 0; i < len(value); i++ {
		if (value[i] < 0x20 && value[i] != '\t') || value[i] == 0x7f {
			return "must contain only printable characters"
		}
	}
	if len(value) > max {
		return "is too long"
	}
	return ""
}

// legacyPresetUserAgents exists only to convert saved preset selections from
// older configs into compatibility profiles on load.
var legacyPresetUserAgents = map[string]string{
	"sonarr":    "Sonarr/4.0.20.3014 (ubuntu 26.04)",
	"radarr":    "Radarr/6.4.4.10685 (ubuntu 26.04)",
	"prowlarr":  "Prowlarr/2.7.0.5723 (ubuntu 26.04)",
	"nzbhydra2": "NZBHydra2 9.2.0",
	"sabnzbd":   "SABnzbd/5.1.3",
	"nzbget":    "nzbget/26.3",
}

// upgradeClientIdentity migrates legacy global clientIdentity fields (pre-W175
// slot presets, userAgent, headers) and per-indexer identities (W175
// requestIdentity, legacy userAgent, ancient clientIdentity) into
// clientIdentity.rules without loss, dropping the superseded keys.
func upgradeClientIdentity(doc map[string]any) bool {
	changed := false

	ci, _ := doc["clientIdentity"].(map[string]any)
	var existingRules []any
	existingIDs := map[string]bool{}
	if ci != nil {
		if rawRules, ok := ci["rules"].([]any); ok {
			existingRules = append(existingRules, rawRules...)
			for _, item := range rawRules {
				if m, ok := item.(map[string]any); ok {
					if id, _ := m["id"].(string); id != "" {
						existingIDs[id] = true
					}
				}
			}
		}
	}

	nextID := func(base string) string {
		id := base
		for n := 2; existingIDs[id]; n++ {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		existingIDs[id] = true
		return id
	}

	var migratedIxRules []any
	var migratedGlobalRules []any

	// 1. Migrate legacy global clientIdentity (ancient slots + userAgent + headers).
	if ci != nil {
		ua, _ := ci["userAgent"].(string)
		ua = strings.TrimSpace(ua)
		slotProfs := map[string]string{}
		hasLegacySlots := false
		for _, slot := range []string{"tvSearch", "movieSearch", "tvGrab", "movieGrab"} {
			raw, exists := ci[slot]
			if !exists {
				continue
			}
			hasLegacySlots = true
			key := strings.ToLower(strings.TrimSpace(asString(raw)))
			if key != "" && key != "ether" && key != "custom" {
				if _, ok := legacyPresetUserAgents[key]; ok {
					slotProfs[slot] = key
				}
			}
			delete(ci, slot)
			changed = true
		}

		var legacyHeaders []any
		if rawHdrs, exists := ci["headers"]; exists {
			if hm, ok := rawHdrs.(map[string]any); ok && len(hm) > 0 {
				keys := make([]string, 0, len(hm))
				for k := range hm {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if v, ok := hm[k].(string); ok {
						legacyHeaders = append(legacyHeaders, map[string]any{"name": k, "value": v})
					}
				}
			}
			delete(ci, "headers")
			changed = true
		}
		if _, exists := ci["userAgent"]; exists {
			delete(ci, "userAgent")
			changed = true
		}

		addGlobalRule := func(suffix string, target IdentityTargetKind, media IdentityMediaType, prof string, hdrs []any) {
			if prof == "" {
				return
			}
			rule := map[string]any{
				"id":      nextID("migrated-global-" + suffix),
				"target":  string(target),
				"profile": prof,
			}
			if media != IdentityMediaAny {
				rule["mediaType"] = string(media)
			}
			if len(hdrs) > 0 {
				rule["headers"] = cloneAnySlice(hdrs)
			}
			migratedGlobalRules = append(migratedGlobalRules, rule)
			changed = true
		}

		if ua == "" && hasLegacySlots && len(slotProfs) > 0 {
			tvS, movS := slotProfs["tvSearch"], slotProfs["movieSearch"]
			tvG, movG := slotProfs["tvGrab"], slotProfs["movieGrab"]
			if tvS != "" && tvS == movS {
				addGlobalRule("searches", TargetAllSearches, IdentityMediaAny, tvS, legacyHeaders)
			} else {
				addGlobalRule("searches-tv", TargetAllSearches, IdentityMediaTV, tvS, legacyHeaders)
				addGlobalRule("searches-movie", TargetAllSearches, IdentityMediaMovie, movS, legacyHeaders)
			}
			if tvG != "" && tvG == movG {
				addGlobalRule("nzb", TargetNZBDownloads, IdentityMediaAny, tvG, legacyHeaders)
				addGlobalRule("torrent", TargetTorrentDownloads, IdentityMediaAny, tvG, legacyHeaders)
			} else {
				addGlobalRule("nzb-tv", TargetNZBDownloads, IdentityMediaTV, tvG, legacyHeaders)
				addGlobalRule("torrent-tv", TargetTorrentDownloads, IdentityMediaTV, tvG, legacyHeaders)
				addGlobalRule("nzb-movie", TargetNZBDownloads, IdentityMediaMovie, movG, legacyHeaders)
				addGlobalRule("torrent-movie", TargetTorrentDownloads, IdentityMediaMovie, movG, legacyHeaders)
			}
		} else if ua != "" || len(legacyHeaders) > 0 {
			prof, hdrs := profileAndHeadersFromUA(ua, legacyHeaders)
			if !(prof == "ether" && len(hdrs) == 0) {
				targets := []struct {
					target IdentityTargetKind
					suffix string
				}{
					{TargetAllSearches, "searches"},
					{TargetNZBDownloads, "nzb"},
					{TargetTorrentDownloads, "torrent"},
				}
				for _, t := range targets {
					addGlobalRule(t.suffix, t.target, IdentityMediaAny, prof, hdrs)
				}
			}
		}
	}

	// 2. Migrate W175 indexer.requestIdentity and legacy indexer.userAgent / clientIdentity.
	if ixs, ok := doc["indexers"].([]any); ok {
		for idx, item := range ixs {
			ix, ok := item.(map[string]any)
			if !ok {
				continue
			}
			ixID := strings.TrimSpace(asString(ix["id"]))
			legacyUA := strings.TrimSpace(asString(ix["userAgent"]))
			legacyPreset := strings.ToLower(strings.TrimSpace(asString(ix["clientIdentity"])))
			rawRI, hasRI := ix["requestIdentity"]
			_, hasUA := ix["userAgent"]
			_, hasPreset := ix["clientIdentity"]

			if !hasRI && !hasUA && !hasPreset {
				continue
			}

			ensureIxID := func() string {
				if ixID == "" {
					ixID = "ix-" + strconv.Itoa(idx+1)
					ix["id"] = ixID
				}
				return ixID
			}

			if ri, ok := rawRI.(map[string]any); ok && len(ri) > 0 {
				prof := strings.ToLower(strings.TrimSpace(asString(ri["profile"])))
				hdrs := normalizeRawOverrides(ri["headers"])
				params := normalizeRawOverrides(ri["params"])
				grabProf := strings.ToLower(strings.TrimSpace(asString(ri["grabProfile"])))
				grabHdrs := normalizeRawOverrides(ri["grabHeaders"])
				grabParams := normalizeRawOverrides(ri["grabParams"])

				hasSearch := prof != "" || len(hdrs) > 0 || len(params) > 0
				hasGrab := grabProf != "" || len(grabHdrs) > 0 || len(grabParams) > 0

				if hasSearch && hasGrab {
					id := ensureIxID()
					sRule := buildMigratedRule(nextID("migrated-ix-"+id+"-search"), TargetIndexer, id, "search", defaultProfile(prof), hdrs, params)
					gRule := buildMigratedRule(nextID("migrated-ix-"+id+"-grab"), TargetIndexer, id, "download", defaultProfile(grabProf), grabHdrs, grabParams)
					migratedIxRules = append(migratedIxRules, sRule, gRule)
				} else if hasSearch {
					id := ensureIxID()
					sRule := buildMigratedRule(nextID("migrated-ix-"+id), TargetIndexer, id, "", defaultProfile(prof), hdrs, params)
					migratedIxRules = append(migratedIxRules, sRule)
				} else if hasGrab {
					id := ensureIxID()
					gRule := buildMigratedRule(nextID("migrated-ix-"+id+"-grab"), TargetIndexer, id, "download", defaultProfile(grabProf), grabHdrs, grabParams)
					migratedIxRules = append(migratedIxRules, gRule)
				}
			} else {
				ua := legacyUA
				if ua == "" && legacyPreset != "" && legacyPreset != "ether" && legacyPreset != "inherit" {
					if mapped, ok := legacyPresetUserAgents[legacyPreset]; ok {
						ua = mapped
					}
				}
				if ua != "" {
					id := ensureIxID()
					prof, hdrs := profileAndHeadersFromUA(ua, nil)
					migratedIxRules = append(migratedIxRules, buildMigratedRule(nextID("migrated-ix-"+id), TargetIndexer, id, "", prof, hdrs, nil))
				}
			}

			if hasRI {
				delete(ix, "requestIdentity")
				changed = true
			}
			if hasUA {
				delete(ix, "userAgent")
				changed = true
			}
			if hasPreset {
				delete(ix, "clientIdentity")
				changed = true
			}
		}
	}

	if len(migratedIxRules) > 0 || len(migratedGlobalRules) > 0 {
		if ci == nil {
			ci = map[string]any{}
			doc["clientIdentity"] = ci
		}
		combined := make([]any, 0, len(existingRules)+len(migratedIxRules)+len(migratedGlobalRules))
		combined = append(combined, existingRules...)
		combined = append(combined, migratedIxRules...)
		combined = append(combined, migratedGlobalRules...)
		ci["rules"] = combined
		changed = true
	}

	return changed
}

func defaultProfile(p string) string {
	if _, ok := LookupProfile(p); ok {
		return strings.ToLower(strings.TrimSpace(p))
	}
	return "ether"
}

func profileAndHeadersFromUA(ua string, extra []any) (string, []any) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "ether", cloneAnySlice(extra)
	}
	if prof, ok := ProfileForUserAgent(ua); ok {
		return prof, cloneAnySlice(extra)
	}
	if ua == DefaultUserAgent() || strings.HasPrefix(ua, "Ether/") {
		return "ether", cloneAnySlice(extra)
	}
	hdrs := make([]any, 0, 1+len(extra))
	hdrs = append(hdrs, map[string]any{"name": "User-Agent", "value": ua})
	for _, item := range extra {
		if m, ok := item.(map[string]any); ok {
			if strings.EqualFold(asString(m["name"]), "User-Agent") {
				continue
			}
			hdrs = append(hdrs, cloneAnyMap(m))
		}
	}
	return "custom", hdrs
}

func normalizeRawOverrides(raw any) []any {
	slice, ok := raw.([]any)
	if !ok || len(slice) == 0 {
		return nil
	}
	out := make([]any, 0, len(slice))
	for _, item := range slice {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(asString(m["name"]))
		if name == "" {
			continue
		}
		entry := map[string]any{"name": name}
		if rm, _ := m["remove"].(bool); rm {
			entry["remove"] = true
		} else if v, ok := m["value"].(string); ok {
			entry["value"] = v
		}
		out = append(out, entry)
	}
	return out
}

func buildMigratedRule(id string, target IdentityTargetKind, indexerID, scope, profile string, headers, params []any) map[string]any {
	rule := map[string]any{
		"id":      id,
		"target":  string(target),
		"profile": profile,
	}
	if indexerID != "" {
		rule["indexerId"] = indexerID
	}
	if scope != "" {
		rule["scope"] = scope
	}
	if len(headers) > 0 {
		rule["headers"] = headers
	}
	if len(params) > 0 {
		rule["params"] = params
	}
	return rule
}

func cloneAnySlice(in []any) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, 0, len(in))
	for _, item := range in {
		if m, ok := item.(map[string]any); ok {
			out = append(out, cloneAnyMap(m))
		} else {
			out = append(out, item)
		}
	}
	return out
}

func cloneAnyMap(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// ApplyToURL applies the identity's query parameter overrides to rawURL.
// apikey and t can be changed by an override but never removed.
func (id Identity) ApplyToURL(rawURL string) string {
	if len(id.Params) == 0 {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = id.ApplyParams(u.Query())
	return u.String()
}
