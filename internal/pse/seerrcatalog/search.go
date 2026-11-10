package seerrcatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/pse/submaker"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

const SearchPath = "/api/integrations/seerrcatalog/v1/search"
const SearchSchema = "seerrcatalog.search.v1"

type delegatedSearchKey struct{}

var hashPattern = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)
var imdbPattern = regexp.MustCompile(`^tt[0-9]{5,12}$`)

type SearchInput struct {
	SubtitleLanguage  string `json:"subtitleLanguage"`
	ExternalSubtitles bool   `json:"externalSubtitles"`
	Media             Media  `json:"media"`
	Season            int    `json:"season"`
	Episode           int    `json:"episode"`
}
type SearchStream struct {
	ExternalSubtitle  *submaker.Proof `json:"externalSubtitle,omitempty"`
	Name              string          `json:"name"`
	Title             string          `json:"title"`
	InfoHash          string          `json:"infoHash,omitempty"`
	URL               string          `json:"url,omitempty"`
	BehaviorHints     map[string]any  `json:"behaviorHints,omitempty"`
	Languages         []string        `json:"languages,omitempty"`
	SubtitleLanguages []string        `json:"subtitleLanguages,omitempty"`
	Source            string          `json:"source"`
}

// The caller is authenticated by ServeHTTP. No NZB/torrent download URL is
// presented as an HTTP video. Only reusable hashes and actual HTTP streams leave
// this endpoint, over the explicitly trusted integration connection.
func (s *Service) serveSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema": SearchSchema, "ready": s.Search != nil})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var in SearchInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil ||
		(in.Media.Type != "movie" && in.Media.Type != "series") || !imdbPattern.MatchString(in.Media.IMDB) ||
		in.Media.TMDB <= 0 || len(in.Media.Title) > 500 || strings.TrimSpace(in.Media.Title) == "" ||
		(in.Media.Type == "series" && (in.Season < 0 || in.Season > 1000 || in.Episode < 1 || in.Episode > 10000)) {
		http.Error(w, "Invalid search", 400)
		return
	}
	if s.Search == nil {
		http.Error(w, "Search unavailable", 503)
		return
	}
	select {
	case s.searchSlots <- struct{}{}:
		defer func() { <-s.searchSlots }()
	default:
		w.Header().Set("Retry-After", "30")
		http.Error(w, "Search busy", 429)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, delegatedSearchKey{}, true)
	ctx = core.WithIndexerPurpose(ctx, core.IndexerPurposeAvailability)
	ctx, coverage := core.WithSearchCoverage(ctx)
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{IMDB: in.Media.IMDB, TMDB: in.Media.TMDB}, Title: in.Media.Title, Year: in.Media.Year, Season: in.Season, Episode: in.Episode}
	if in.Media.Type == "series" {
		req.Type = core.TypeEpisode
	}
	if s.Enrich != nil {
		req = s.Enrich(ctx, req)
	}
	releases, err := s.Search(ctx, req)
	if err != nil {
		http.Error(w, "Indexer search failed", 502)
		return
	}
	out := []SearchStream{}
	ignored := 0
	pendingSubtitles := false
	subtitleAttempts := 0
	for _, rel := range releases {
		if len(out) >= 100 {
			break
		}
		// Search APIs may include loosely matched results. Require identity evidence,
		// or a matching parsed title/year, and always scope series to the episode.
		identified := rel.TitleIDs.IMDB == in.Media.IMDB || rel.TitleIDs.TMDB == in.Media.TMDB
		if (rel.TitleIDs.IMDB != "" && rel.TitleIDs.IMDB != in.Media.IMDB) || (rel.TitleIDs.TMDB > 0 && rel.TitleIDs.TMDB != in.Media.TMDB) {
			continue
		}
		if !identified && (!releaseparse.TitleMatchesAny(rel.Info, req.Title, append(req.AlternativeTitles, req.OriginalTitle)) || !releaseparse.YearMatches(rel.Info.Year, req.Year)) {
			continue
		}
		if req.Type == core.TypeEpisode && !releaseparse.EpisodeMatches(rel.Info, in.Season, in.Episode) {
			continue
		}
		st := SearchStream{Name: "Ether Remix", Title: rel.Name, Languages: rel.Info.Languages, SubtitleLanguages: rel.Info.SubtitleLanguages, Source: rel.IndexerID}
		if hashPattern.MatchString(rel.InfoHash) {
			st.InfoHash = strings.ToLower(rel.InfoHash)
		} else if rel.Protocol == core.ProtocolHTTP && (strings.HasPrefix(rel.StreamURL, "https://") || strings.HasPrefix(rel.StreamURL, "http://")) {
			st.URL = rel.StreamURL
			if len(rel.StreamHeaders) > 0 {
				st.BehaviorHints = map[string]any{"proxyHeaders": map[string]any{"request": rel.StreamHeaders}}
			}
		} else {
			ignored++
			continue
		}
		if in.ExternalSubtitles && s.Submaker != nil && subtitleAttempts < 2 && langtag.Normalize(in.SubtitleLanguage) == submaker.Load().Target {
			proof, pending := s.Submaker.Ensure(ctx, req, rel)
			if proof != nil || pending {
				subtitleAttempts++
			}
			st.ExternalSubtitle = proof
			pendingSubtitles = pendingSubtitles || pending
		}
		out = append(out, st)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"schema": SearchSchema, "streams": out, "conclusive": !coverage.Incomplete() && ctx.Err() == nil, "ignored": ignored, "subtitlesPending": pendingSubtitles})
}
