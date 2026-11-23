package plex

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

func (s *Server) liveProviderSource() string {
	return "server://" + s.identity().MachineIdentifier + "/" + frontend.LiveTVProvider
}

func (s *Server) liveSearchProgrammeMetadata(r *http.Request, c frontend.LiveTVChannel, p frontend.LiveTVProgramme, now time.Time, source string) (model.Metadata, bool) {
	md := s.liveProgrammeMetadata(c, p)
	s.applyGuideChannelThumb(r, &md, c)
	onAir := !now.Before(p.Start) && !now.After(p.End)
	if onAir && len(md.Media) > 0 {
		md.Media[0].Extra = append(md.Media[0].Extra, model.A("onAir", true))
	}
	if source != "" {
		md.Extra = append(md.Extra, model.A("source", source))
	}
	return md, onAir
}

func (s *Server) canSearchLiveTV(ctx context.Context, pr *Request) (frontend.LiveTVSearch, bool) {
	if !s.liveTVEnabled() || pr == nil {
		return nil, false
	}
	if !s.svc().liveTV.VisibleTo(ctx, pr.User) {
		return nil, false
	}
	if p, ok := s.policy().(frontend.PlaybackAccessPolicy); ok && !p.CanPlay(pr.User, pr.Remote, s.now()) {
		return nil, false
	}
	search, ok := s.svc().liveTV.(frontend.LiveTVSearch)
	return search, ok
}

// Search shares the indexed, policy-filtered Live TV search with other apps.
func (s *Server) handleLiveChannelSearch(w http.ResponseWriter, r *http.Request) {
	limit, ok := searchLimit(w, r, 10, 50)
	if !ok {
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	if utf8.RuneCountInString(query) > 256 {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	mc := &model.MediaContainer{Identifier: frontend.LiveTVProvider}
	if query == "" {
		s.write(w, r, mc)
		return
	}
	search, ok := s.svc().liveTV.(frontend.LiveTVSearch)
	if !ok {
		s.write(w, r, mc)
		return
	}
	results, err := search.SearchLiveTV(r.Context(), FromContext(r.Context()).User, query, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	source := s.liveProviderSource()
	for _, c := range results.Channels {
		mc.SearchResult = append(mc.SearchResult, model.SearchResult{Score: 1, Channel: s.liveSearchChannel(r, c)})
	}
	parents := map[string]frontend.LiveTVChannel{}
	for _, c := range results.ProgrammeChannels {
		parents[c.ID] = c
	}
	now := s.now()
	for _, p := range results.Programmes {
		c := parents[p.ChannelID]
		md, _ := s.liveSearchProgrammeMetadata(r, c, p, now, source)
		mc.SearchResult = append(mc.SearchResult, model.SearchResult{Score: 0.9, Metadata: &md})
	}
	mc.Size = len(mc.SearchResult)
	s.write(w, r, mc)
}

func (s *Server) liveSearchChannel(r *http.Request, c frontend.LiveTVChannel) *model.SearchChannel {
	categories := model.StringList{}
	if c.Group != "" {
		categories = append(categories, c.Group)
	}
	logo := s.liveGuideLogo(r, c)
	return &model.SearchChannel{
		ID:                 c.ID,
		Identifier:         c.ID,
		ChannelIdentifier:  c.ID,
		Slug:               c.ID,
		Key:                liveProviderPath + "/channels/" + url.PathEscape(c.ID),
		Type:               "channel",
		GridKey:            c.ID,
		VCN:                c.Number,
		Title:              c.Name,
		ShortTitle:         c.Name,
		CallSign:           c.Name,
		Thumb:              logo,
		CoverPoster:        logo,
		Language:           "eng",
		ProviderIdentifier: frontend.LiveTVProvider,
		Source:             s.liveProviderSource(),
		Categories:         categories,
	}
}

type liveLibrarySearchResult struct {
	results []model.SearchResult
	scores  []float64
}

func liveCompactFold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func liveChannelSearchScore(c frontend.LiveTVChannel, query string, rank int) float64 {
	q := liveCompactFold(query)
	name := liveCompactFold(c.Name)
	num := liveCompactFold(c.Number)
	base := 0.82
	if q != "" {
		if name == q || num == q {
			base = 0.95
		} else if strings.HasPrefix(name, q) || strings.HasPrefix(num, q) {
			base = 0.88
		}
	}
	return max(0.1, base-float64(rank)*0.001)
}

// searchLiveTVLibrary runs indexed, policy-filtered Live TV search for
// /library/search, returning channels in the EPG SearchResult.Channel shape
// along with matching airings and on-now airings on matched channels.
func (s *Server) searchLiveTVLibrary(r *http.Request, pr *Request, query string, limit int) liveLibrarySearchResult {
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > 256 {
		return liveLibrarySearchResult{}
	}
	ctx := r.Context()
	search, ok := s.canSearchLiveTV(ctx, pr)
	if !ok {
		return liveLibrarySearchResult{}
	}
	results, err := search.SearchLiveTV(ctx, pr.User, query, limit)
	if err != nil {
		s.log.Warn("live search unavailable", "error", err)
		return liveLibrarySearchResult{}
	}
	source := s.liveProviderSource()
	now := s.now()
	var out liveLibrarySearchResult
	matchedByID := make(map[string]frontend.LiveTVChannel, len(results.Channels))
	matchedIDs := make([]string, 0, len(results.Channels))
	for i, c := range results.Channels {
		matchedByID[c.ID] = c
		matchedIDs = append(matchedIDs, c.ID)
		sc := liveChannelSearchScore(c, query, i)
		out.results = append(out.results, model.SearchResult{
			Score:   math.Round(sc*10000) / 10000,
			Channel: s.liveSearchChannel(r, c),
		})
		out.scores = append(out.scores, sc)
	}
	parents := make(map[string]frontend.LiveTVChannel, len(results.ProgrammeChannels)+len(results.Channels))
	for _, c := range results.Channels {
		parents[c.ID] = c
	}
	for _, c := range results.ProgrammeChannels {
		parents[c.ID] = c
	}
	seenProg := make(map[string]bool, len(results.Programmes))
	for i, p := range results.Programmes {
		seenProg[p.ID] = true
		c := parents[p.ChannelID]
		md, onAir := s.liveSearchProgrammeMetadata(r, c, p, now, source)
		sc := 0.72
		if onAir {
			sc = 0.82
		}
		sc = max(0.1, sc-float64(i)*0.001)
		out.results = append(out.results, model.SearchResult{
			Score:    math.Round(sc*10000) / 10000,
			Metadata: &md,
		})
		out.scores = append(out.scores, sc)
	}
	if len(matchedIDs) > 0 {
		onNowCap := min(len(matchedIDs), 20)
		if onNow, err := s.svc().liveTV.GuideForChannels(ctx, pr.User, now, now.Add(time.Second), matchedIDs[:onNowCap]); err == nil {
			for i, p := range onNow {
				if seenProg[p.ID] {
					continue
				}
				c, ok := matchedByID[p.ChannelID]
				if !ok {
					continue
				}
				md, onAir := s.liveSearchProgrammeMetadata(r, c, p, now, source)
				if !onAir {
					continue
				}
				seenProg[p.ID] = true
				sc := max(0.1, 0.68-float64(i)*0.001)
				out.results = append(out.results, model.SearchResult{
					Score:    math.Round(sc*10000) / 10000,
					Metadata: &md,
				})
				out.scores = append(out.scores, sc)
			}
		}
	}
	return out
}

func (s *Server) appendLiveSearchHubs(r *http.Request, mc *model.MediaContainer, query string, limit int) {
	if r.URL.Query().Get("sectionId") != "" {
		return
	}
	pr := FromContext(r.Context())
	search, ok := s.canSearchLiveTV(r.Context(), pr)
	if !ok {
		return
	}
	results, err := search.SearchLiveTV(r.Context(), pr.User, query, limit)
	if err != nil {
		s.log.Warn("live search unavailable", "error", err)
		return
	}
	source := s.liveProviderSource()
	var liveHubs []model.Hub
	if len(results.Channels) > 0 {
		hub := model.Hub{
			Title:         "Live TV",
			Type:          "clip",
			HubIdentifier: "livetv",
			Context:       "hub.livetv",
			Style:         "shelf",
			Source:        source,
			Identifier:    frontend.LiveTVProvider,
		}
		for i, c := range results.Channels {
			md := s.liveChannelMetadata(r, c)
			md.Score = strconv.FormatFloat(reportedScore(searchScore(i, len(results.Channels))), 'f', 5, 64)
			md.Extra = append(md.Extra, model.A("source", source))
			hub.Metadata = append(hub.Metadata, md)
		}
		hub.Size = len(hub.Metadata)
		liveHubs = append(liveHubs, hub)
	}
	parents := map[string]frontend.LiveTVChannel{}
	for _, c := range results.ProgrammeChannels {
		parents[c.ID] = c
	}
	if len(results.Programmes) > 0 {
		now := s.now()
		hub := model.Hub{
			Title:         "On TV",
			Type:          "episode",
			HubIdentifier: "tv.plex.provider.epg",
			Context:       "hub.tv.plex.provider.epg",
			Style:         "shelf",
			Source:        source,
			Identifier:    frontend.LiveTVProvider,
		}
		for i, p := range results.Programmes {
			c := parents[p.ChannelID]
			md, _ := s.liveSearchProgrammeMetadata(r, c, p, now, source)
			md.Score = strconv.FormatFloat(reportedScore(searchScore(i, len(results.Programmes))), 'f', 5, 64)
			hub.Metadata = append(hub.Metadata, md)
		}
		hub.Size = len(hub.Metadata)
		liveHubs = append(liveHubs, hub)
	}
	if len(liveHubs) == 0 {
		return
	}
	insertAt := 0
	for i, h := range mc.Hub {
		if h.Size > 0 {
			insertAt = i + 1
		}
	}
	merged := make([]model.Hub, 0, len(mc.Hub)+len(liveHubs))
	merged = append(merged, mc.Hub[:insertAt]...)
	merged = append(merged, liveHubs...)
	merged = append(merged, mc.Hub[insertAt:]...)
	mc.Hub = merged
}
