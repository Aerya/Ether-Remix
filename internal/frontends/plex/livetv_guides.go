package plex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

const (
	liveGuideMinCategoryChannels = 3
	liveGuideMinGenreChannels    = 3
	liveGuideGenreCap            = 8
	liveGuideChannelGenreKeysMax = 32
)

type liveGuideGenre struct {
	id    string
	title string
	count int
}

type liveGuideDay struct {
	categoryCounts map[string]int
	genres         []liveGuideGenre
	channelGenres  map[string]model.StringList
}

type liveGuideDayEntry struct {
	key      string
	built    time.Time
	expires  time.Time
	snapshot *liveGuideDay
}

type liveGuideDayCache struct {
	mu               sync.Mutex
	closed, building bool
	entries          map[int64]liveGuideDayEntry
	ready            chan struct{}
	idleTimer        *time.Timer
}

func (c *liveGuideDayCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.idleTimer != nil {
		c.idleTimer.Stop()
	}
	c.entries = nil
}

// Provider categories are identities, not English classification heuristics.
func liveGenreID(genre string) string {
	return "genre-" + base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(genre))))
}

// foldLiveGuideName normalizes case, diacritics, and whitespace runs so tab
// names collide only on an exact match after folding, never by containment.
func foldLiveGuideName(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		r = unicode.ToLower(r)
		switch r {
		case 'ß':
			b.WriteString("ss")
		case 'æ':
			b.WriteString("ae")
		case 'œ':
			b.WriteString("oe")
		case 'ø':
			b.WriteRune('o')
		case 'ł':
			b.WriteRune('l')
		case 'đ':
			b.WriteRune('d')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(norm.NFC.String(b.String())), " ")
}

func liveGuideGenreThreshold(totalChannels int) int {
	return max(liveGuideMinGenreChannels, (totalChannels+19)/20)
}

func liveGuideDayWindow(now time.Time) (dayStart, dayEnd, queryFrom time.Time) {
	dayStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayEnd = dayStart.AddDate(0, 0, 1)
	queryFrom = dayStart
	if minFrom := now.Add(-23*time.Hour - 59*time.Minute); queryFrom.Before(minFrom) {
		queryFrom = minFrom
	}
	return dayStart, dayEnd, queryFrom
}

func normalizeLiveCategory(raw string) (string, bool) {
	switch raw {
	case "movies":
		raw = "movie"
	case "tv-shows":
		raw = "series"
	}
	switch raw {
	case "movie", "series", "sports", "news", "kids":
		return raw, true
	}
	if strings.HasPrefix(raw, "genre-") {
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, "genre-"))
		return raw, err == nil && len(b) > 0 && len(b) <= 256
	}
	return "", false
}

func matchesLiveCategory(genres []string, category, mediaType string) bool {
	if strings.HasPrefix(category, "genre-") {
		for _, genre := range genres {
			if liveGenreID(genre) == category {
				return true
			}
		}
		return false
	}
	return frontend.LiveCategory(genres, category, mediaType)
}

func liveCategoryGuideFilters() []model.GridChannelFilter {
	out := []model.GridChannelFilter{}
	for _, c := range []struct{ id, title string }{{"movie", "Movies"}, {"series", "TV Shows"}, {"sports", "Sports"}, {"news", "News"}, {"kids", "Kids"}} {
		out = append(out, liveCategoryFilter(c.id, c.title))
	}
	return out
}

func liveCategoryFilter(id, title string) model.GridChannelFilter {
	return model.GridChannelFilter{Identifier: id, Key: liveProviderPath + "/lineups/dvr/channels?genre=" + url.QueryEscape(id), GenreRatingKey: "ether.category." + id, Title: title}
}

// liveGuideDaySnapshot derives category and genre channel memberships from the
// full calendar day's guide window so guide tabs stay stable across the day.
func (s *Server) liveGuideDaySnapshot(ctx context.Context, viewer *frontend.User) *liveGuideDay {
	if !s.liveTVEnabled() {
		return nil
	}
	id := int64(0)
	if viewer != nil {
		id = viewer.ID
	}
	raw, _ := json.Marshal(viewer)
	now := s.now()
	dayStart, dayEnd, queryFrom := liveGuideDayWindow(now)
	key := string(raw) + "|" + s.svc().liveTV.LineupChangedFor(viewer).String() + "|" + dayStart.Format("2006-01-02") + "|" + now.Location().String()

	c := &s.liveGuideDay
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	if c.idleTimer == nil {
		c.idleTimer = time.AfterFunc(15*time.Minute, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.entries = nil
		})
	} else {
		c.idleTimer.Reset(15 * time.Minute)
	}
	entry := c.entries[id]
	if entry.key == key && (entry.snapshot != nil || (!now.Before(entry.built) && now.Before(entry.expires))) {
		c.mu.Unlock()
		return entry.snapshot
	}

	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	for {
		if c.closed {
			c.mu.Unlock()
			return nil
		}
		entry = c.entries[id]
		if entry.key == key && (entry.snapshot != nil || (!now.Before(entry.built) && now.Before(entry.expires))) {
			c.mu.Unlock()
			return entry.snapshot
		}
		if !c.building {
			c.building = true
			c.ready = make(chan struct{})
			var user *frontend.User
			if viewer != nil {
				copyViewer := *viewer
				user = &copyViewer
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				workerCtx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
				defer cancel()
				snapshot, err := s.buildLiveGuideDay(workerCtx, user, dayStart, dayEnd, queryFrom)
				c.mu.Lock()
				defer c.mu.Unlock()
				c.building = false
				if !c.closed {
					if c.entries == nil {
						c.entries = map[int64]liveGuideDayEntry{}
					}
					if _, exists := c.entries[id]; !exists && len(c.entries) >= 8 {
						var oldestID int64
						var oldest time.Time
						for uid, e := range c.entries {
							if oldest.IsZero() || e.built.Before(oldest) {
								oldestID, oldest = uid, e.built
							}
						}
						delete(c.entries, oldestID)
					}
					expires := dayEnd
					if err != nil {
						expires = now.Add(5 * time.Second)
					}
					c.entries[id] = liveGuideDayEntry{key: key, built: now, expires: expires, snapshot: snapshot}
				}
				close(c.ready)
			}()
		}
		ready := c.ready
		c.mu.Unlock()
		select {
		case <-ready:
			c.mu.Lock()
		case <-timer.C:
			c.mu.Lock()
			entry = c.entries[id]
			c.mu.Unlock()
			if entry.key == key {
				return entry.snapshot
			}
			return nil
		case <-ctx.Done():
			c.mu.Lock()
			entry = c.entries[id]
			c.mu.Unlock()
			if entry.key == key {
				return entry.snapshot
			}
			return nil
		}
	}
}

func (s *Server) buildLiveGuideDay(ctx context.Context, viewer *frontend.User, dayStart, dayEnd, queryFrom time.Time) (*liveGuideDay, error) {
	live := s.svc().liveTV
	if live == nil || !live.VisibleTo(ctx, viewer) {
		return nil, frontend.ErrNotAvailable
	}
	cs, err := live.AllowedChannels(ctx, viewer)
	if err != nil {
		return nil, err
	}
	builtin := liveCategoryGuideFilters()
	catChannels := make(map[string]map[string]struct{}, len(builtin))
	type genreAcc struct {
		id            string
		channels      map[string]struct{}
		titleChannels map[string]map[string]struct{}
	}
	genresByID := map[string]*genreAcc{}

	for start := 0; start < len(cs); start += 32 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk := cs[start:min(start+32, len(cs))]
		ids := make([]string, len(chunk))
		allowed := make(map[string]struct{}, len(chunk))
		for i, c := range chunk {
			ids[i] = c.ID
			allowed[c.ID] = struct{}{}
		}
		ps, err := live.GuideForChannels(ctx, viewer, queryFrom, dayEnd, ids)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if _, ok := allowed[p.ChannelID]; !ok || !p.End.After(dayStart) || !p.Start.Before(dayEnd) {
				continue
			}
			for _, f := range builtin {
				if matchesLiveCategory(p.Item.Genres, f.Identifier, p.Item.MediaType) {
					if catChannels[f.Identifier] == nil {
						catChannels[f.Identifier] = map[string]struct{}{}
					}
					catChannels[f.Identifier][p.ChannelID] = struct{}{}
				}
			}
			for _, genre := range p.Item.Genres {
				genre = strings.TrimSpace(genre)
				if genre == "" || len(genre) > 256 {
					continue
				}
				canonical := false
				for _, f := range builtin {
					if frontend.LiveCategory([]string{genre}, f.Identifier, "") {
						canonical = true
						break
					}
				}
				if canonical {
					continue
				}
				id := liveGenreID(genre)
				acc := genresByID[id]
				if acc == nil {
					acc = &genreAcc{
						id:            id,
						channels:      map[string]struct{}{},
						titleChannels: map[string]map[string]struct{}{},
					}
					genresByID[id] = acc
				}
				acc.channels[p.ChannelID] = struct{}{}
				if acc.titleChannels[genre] == nil {
					acc.titleChannels[genre] = map[string]struct{}{}
				}
				acc.titleChannels[genre][p.ChannelID] = struct{}{}
			}
		}
	}

	categoryCounts := make(map[string]int, len(builtin))
	for _, f := range builtin {
		categoryCounts[f.Identifier] = len(catChannels[f.Identifier])
	}

	threshold := liveGuideGenreThreshold(len(cs))
	genres := make([]liveGuideGenre, 0)
	var otherGenreIDs []string
	for id, acc := range genresByID {
		count := len(acc.channels)
		if count < threshold {
			otherGenreIDs = append(otherGenreIDs, id)
			continue
		}
		var bestTitle string
		bestCount := -1
		for title, chSet := range acc.titleChannels {
			n := len(chSet)
			if n > bestCount || (n == bestCount && title < bestTitle) {
				bestTitle = title
				bestCount = n
			}
		}
		genres = append(genres, liveGuideGenre{id: id, title: bestTitle, count: count})
	}
	sort.Slice(genres, func(i, j int) bool {
		if genres[i].count != genres[j].count {
			return genres[i].count > genres[j].count
		}
		fi, fj := foldLiveGuideName(genres[i].title), foldLiveGuideName(genres[j].title)
		if fi != fj {
			return fi < fj
		}
		if genres[i].title != genres[j].title {
			return genres[i].title < genres[j].title
		}
		return genres[i].id < genres[j].id
	})
	slices.Sort(otherGenreIDs)

	channelGenres := make(map[string]model.StringList)
	for _, f := range builtin {
		key := "ether.category." + f.Identifier
		for cid := range catChannels[f.Identifier] {
			channelGenres[cid] = append(channelGenres[cid], key)
		}
	}
	for _, g := range genres {
		key := "ether.category." + g.id
		for cid := range genresByID[g.id].channels {
			if len(channelGenres[cid]) < liveGuideChannelGenreKeysMax {
				channelGenres[cid] = append(channelGenres[cid], key)
			}
		}
	}
	for _, id := range otherGenreIDs {
		key := "ether.category." + id
		for cid := range genresByID[id].channels {
			if len(channelGenres[cid]) < liveGuideChannelGenreKeysMax {
				channelGenres[cid] = append(channelGenres[cid], key)
			}
		}
	}

	return &liveGuideDay{
		categoryCounts: categoryCounts,
		genres:         genres,
		channelGenres:  channelGenres,
	}, nil
}

// Owner groups are published as guide tabs on the single Live TV provider in
// owner order, ahead of data-driven category and genre filters.
func (s *Server) liveGuideFilters(ctx context.Context, u *frontend.User) []model.GridChannelFilter {
	out := []model.GridChannelFilter{}
	claimed := map[string]bool{}
	if g, ok := s.svc().liveTV.(frontend.LiveTVGroups); ok {
		groups, err := g.Groups(ctx, u)
		if err != nil {
			s.log.Warn("live groups unavailable", "error", err)
		}
		for _, grp := range groups {
			out = append(out, model.GridChannelFilter{Identifier: "guide-" + grp.ID, Key: liveProviderPath + "/guides/" + url.PathEscape(grp.ID) + "/channels", GenreRatingKey: "ether.guide." + grp.ID, Title: grp.Name})
			if key := foldLiveGuideName(grp.Name); key != "" {
				claimed[key] = true
			}
		}
	}
	day := s.liveGuideDaySnapshot(ctx, u)
	if day == nil {
		return out
	}
	for _, f := range liveCategoryGuideFilters() {
		key := foldLiveGuideName(f.Title)
		if claimed[key] || day.categoryCounts[f.Identifier] < liveGuideMinCategoryChannels {
			continue
		}
		out = append(out, f)
		if key != "" {
			claimed[key] = true
		}
	}
	genresAdded := 0
	for _, g := range day.genres {
		if genresAdded >= liveGuideGenreCap {
			break
		}
		key := foldLiveGuideName(g.title)
		if claimed[key] {
			continue
		}
		out = append(out, liveCategoryFilter(g.id, g.title))
		if key != "" {
			claimed[key] = true
		}
		genresAdded++
	}
	return out
}
