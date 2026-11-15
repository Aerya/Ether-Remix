package subtitles

// Add-on subtitle providers beside OpenSubtitles.
//
// An add-on whose manifest resources include "subtitles" answers
// GET /subtitles/{type}/{id}[/videoHash=…&videoSize=…].json with
// {url, lang} entries. Listing is a search: Ether calls it only from
// the viewer-driven SearchOnDemand, and a file is downloaded only when
// the viewer picks it (DownloadOnDemand), through the same normalise
// and cache pipeline as OpenSubtitles. The moviehash is sent only when
// a caller already has it from cached ranges; nothing here ever fetches
// a release to compute one (see MovieHash).

import (
	"context"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type videoHashKey struct {
	key     core.RatingKey
	release string
}

type videoHashEntry struct {
	movieHash string
	videoSize int64
}

// RememberVideoHash records an already-computed moviehash and byte size
// (for example from local file enrichment) so a later viewer-driven
// subtitle search can pass videoHash and videoSize without fetching the
// release on the search path.
func (s *Service) RememberVideoHash(key core.RatingKey, releaseName, movieHash string, videoSize int64) {
	if key == 0 || len(movieHash) != 16 {
		return
	}
	s.mu.Lock()
	if s.videoHashes == nil {
		s.videoHashes = map[videoHashKey]videoHashEntry{}
	}
	if len(s.videoHashes) >= 4096 {
		clear(s.videoHashes)
	}
	s.videoHashes[videoHashKey{key, strings.ToLower(releaseName)}] = videoHashEntry{
		movieHash: strings.ToLower(movieHash),
		videoSize: videoSize,
	}
	s.mu.Unlock()
}

func (s *Service) lookupVideoHash(title *core.Title, releaseName string) (string, int64) {
	if title == nil || title.Key == 0 {
		return "", 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.videoHashes[videoHashKey{title.Key, strings.ToLower(releaseName)}]
	if !ok {
		return "", 0
	}
	return e.movieHash, e.videoSize
}

// providerList is the effective provider order: the owner's configured
// order, with OpenSubtitles and SubDL included when enabled and not
// explicitly listed in s.providers.
func (s *Service) providerList() []config.SubtitleProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []config.SubtitleProvider
	hasOpenSubtitles := false
	hasSubDL := false
	for _, p := range s.providers {
		switch p.Kind {
		case config.SubtitleProviderOpenSubtitles:
			hasOpenSubtitles = true
		case config.SubtitleProviderSubDL:
			hasSubDL = true
		}
		if p.Enabled {
			out = append(out, p)
		}
	}
	if !hasSubDL && s.subdl.Enabled && s.subdl.APIKey != "" {
		out = append(out, config.SubtitleProvider{ID: "subdl", Kind: config.SubtitleProviderSubDL, Enabled: true})
	}
	if !hasOpenSubtitles && s.cfg.Enabled && s.cfg.APIKey != "" {
		out = append([]config.SubtitleProvider{{ID: "opensubtitles", Kind: config.SubtitleProviderOpenSubtitles, Enabled: true}}, out...)
	}
	return out
}

// searchEnabled reports whether any provider can search.
func (s *Service) searchEnabled() bool {
	if s.dir == "" {
		return false
	}
	s.mu.Lock()
	hasPlugins := len(s.pluginProviders) > 0
	s.mu.Unlock()
	if hasPlugins {
		return true
	}
	for _, p := range s.providerList() {
		switch p.Kind {
		case config.SubtitleProviderOpenSubtitles:
			if s.openSubtitlesEnabled() {
				return true
			}
		case config.SubtitleProviderSubDL:
			if s.subdlEnabled() {
				return true
			}
		}
	}
	return false
}

// UpdateProviders swaps the subtitle provider order (config changes).
func (s *Service) UpdateProviders(ps []config.SubtitleProvider) {
	s.mu.Lock()
	s.providers = append([]config.SubtitleProvider(nil), ps...)
	s.mu.Unlock()
}

// searchProviders runs every enabled provider in owner order and merges
// the candidates into one ranked list (language, hash match and
// release-name match via Rank; provider order breaks score ties). One
// provider failing does not sink the others; the error surfaces only
// when every provider failed.
func (s *Service) searchProviders(ctx context.Context, title *core.Title, lang, releaseName, movieHash string, videoSize int64) ([]Candidate, error) {
	var all []Candidate
	var firstErr error
	for i, p := range s.providerList() {
		var cands []Candidate
		var err error
		switch p.Kind {
		case config.SubtitleProviderOpenSubtitles:
			if !s.openSubtitlesEnabled() {
				continue
			}
			cands, err = s.searchWithHashAndSize(ctx, title, lang, releaseName, movieHash, videoSize)
			for j := range cands {
				if cands[j].Provider == "" {
					cands[j].Provider = "OpenSubtitles"
					cands[j].ProviderID = "opensubtitles"
				}
			}
		case config.SubtitleProviderSubDL:
			if !s.subdlEnabled() {
				continue
			}
			cands, err = s.searchSubDL(ctx, title, lang, releaseName)
		default:
			pluginID := strings.TrimSuffix(strings.TrimPrefix(p.Kind, "plugin:"), "/subtitles")
			s.mu.Lock()
			provider := s.pluginProviders[pluginID]
			s.mu.Unlock()
			if !strings.HasPrefix(p.Kind, "plugin:") || provider == nil {
				continue
			}
			cands, err = searchPluginLanguages(ctx, provider, title, lang, movieHash, videoSize)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			s.log.Debug("subtitle provider search failed", "provider", p.ID, "err", err)
			continue
		}
		for j := range cands {
			cands[j].providerOrder = i
		}
		all = append(all, cands...)
	}
	s.mu.Lock()
	pluginProviders := make([]PluginProvider, 0, len(s.pluginProviders))
	for _, p := range s.pluginProviders {
		pluginProviders = append(pluginProviders, p)
	}
	s.mu.Unlock()
	sort.Slice(pluginProviders, func(i, j int) bool { return pluginProviders[i].ID() < pluginProviders[j].ID() })
	for i, p := range pluginProviders {
		configured := false
		s.mu.Lock()
		entries := append([]config.SubtitleProvider(nil), s.providers...)
		s.mu.Unlock()
		for _, entry := range entries {
			if entry.Kind == "plugin:"+p.ID()+"/subtitles" {
				configured = true
				break
			}
		}
		if configured {
			continue
		}
		cands, err := searchPluginLanguages(ctx, p, title, lang, movieHash, videoSize)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for j := range cands {
			cands[j].providerOrder = len(s.providerList()) + i
		}
		all = append(all, cands...)
	}
	if len(all) == 0 && firstErr != nil {
		return nil, firstErr
	}
	filtered := all[:0]
	seen := map[offerKey]bool{}
	for _, c := range all {
		k := offerKey{title.Key, c.FileID}
		if matchesSearchLanguages(lang, c.Language) && !seen[k] {
			filtered = append(filtered, c)
			seen[k] = true
		}
	}
	all = filtered
	Rank(all, releaseName)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].HashMatch != all[j].HashMatch {
			return all[i].HashMatch
		}
		ri, rj := releaseMatch(releaseName, all[i]), releaseMatch(releaseName, all[j])
		if ri != rj {
			return ri > rj
		}
		li, lj := languagePreference(lang, all[i].Language), languagePreference(lang, all[j].Language)
		if li != lj {
			return li < lj
		}
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		if all[i].providerOrder != all[j].providerOrder {
			return all[i].providerOrder < all[j].providerOrder
		}
		return all[i].FileID < all[j].FileID
	})
	return all, nil
}

// The plugin contract accepts one language; keep that contract and apply the
// same final filter even if a provider ignores the requested language.
func searchPluginLanguages(ctx context.Context, p PluginProvider, title *core.Title, langs, hash string, size int64) ([]Candidate, error) {
	var out []Candidate
	var firstErr error
	seen := map[string]bool{}
	for _, l := range searchLanguages(langs) {
		query := l
		if query == "nb" {
			query = "no"
		}
		if seen[query] {
			continue
		}
		seen[query] = true
		cands, err := p.Search(ctx, title, query, hash, size)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, cands...)
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}

// Exact and strong token matches outrank popularity and language preference.
func releaseMatch(wanted string, c Candidate) int {
	if wanted == "" {
		return 0
	}
	names := append([]string{c.Release, c.FileName}, c.Releases...)
	want := tokens(wanted)
	best := 0
	for _, name := range names {
		clean := func(s string) string {
			s = strings.ToLower(strings.TrimSpace(s))
			for _, ext := range []string{".mkv", ".mp4", ".avi", ".srt", ".zip"} {
				s = strings.TrimSuffix(s, ext)
			}
			return s
		}
		if clean(name) == clean(wanted) {
			return 2
		}
		have := tokens(name)
		hit := 0
		for token := range want {
			if have[token] {
				hit++
			}
		}
		if len(want) >= 3 && len(have) >= 3 && hit*100 >= 70*len(want) && hit*100 >= 70*len(have) {
			best = 1
		}
	}
	return best
}

func languagePreference(langs, actual string) int {
	for i, l := range searchLanguages(langs) {
		if matchesSearchLanguages(l, actual) {
			return i
		}
	}
	return len(searchLanguages(langs))
}

// CachedVideoHash checks the exact title/version cache without reading media.
func (s *Service) CachedVideoHash(title *core.Title, release string) (string, int64) {
	return s.lookupVideoHash(title, release)
}

// SearchNeedsHash avoids reading media when only title/name providers are in use.
func (s *Service) SearchNeedsHash() bool {
	for _, p := range s.providerList() {
		if p.Kind == config.SubtitleProviderOpenSubtitles && s.openSubtitlesEnabled() {
			return true
		}
		if strings.HasPrefix(p.Kind, "plugin:") {
			return true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.pluginProviders {
		configured := false
		for _, p := range s.providers {
			if p.Kind == "plugin:"+id+"/subtitles" {
				configured = true
				break
			}
		}
		if !configured {
			return true
		}
	}
	return false
}
