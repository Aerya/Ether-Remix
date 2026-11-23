package plex

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

type plexCreditsCtxKey struct{}

type plexCreditsBatch struct {
	rows  map[frontend.RatingKey]frontend.LibraryTitleCredits
	guids map[frontend.RatingKey]string
}

func (s *Server) libraryCredits() frontend.LibraryCredits {
	if s == nil {
		return nil
	}
	c, _ := s.libraryGUIDs().(frontend.LibraryCredits)
	return c
}

// withPlexCredits attaches a read-only batch snapshot of cached provider
// credits for this render. Non-detail list/section/hub renders always attach
// an empty snapshot without touching the database so metadata() never falls
// back to per-item GUID or credit lookups on list paths.
func (s *Server) withPlexCredits(ctx context.Context, keys []frontend.RatingKey, guids map[frontend.RatingKey]string, detail bool) context.Context {
	batch := plexCreditsBatch{guids: guids}
	if detail && len(keys) > 0 && len(guids) > 0 {
		if credSrc := s.libraryCredits(); credSrc != nil {
			batch.rows = credSrc.CachedCreditsMany(ctx, keys, guids)
		}
	}
	return context.WithValue(ctx, plexCreditsCtxKey{}, batch)
}

func (s *Server) plexCreditsCached(ctx context.Context, k frontend.RatingKey) (frontend.LibraryTitleCredits, bool) {
	if s == nil || k == 0 {
		return frontend.LibraryTitleCredits{}, false
	}
	if batch, ok := ctx.Value(plexCreditsCtxKey{}).(plexCreditsBatch); ok {
		row, hit := batch.rows[k]
		return row, hit
	}
	credSrc := s.libraryCredits()
	guidSrc := s.libraryGUIDs()
	if credSrc == nil || guidSrc == nil {
		return frontend.LibraryTitleCredits{}, false
	}
	guid := guidSrc.Cached(ctx, k)
	if guid == "" {
		return frontend.LibraryTitleCredits{}, false
	}
	return credSrc.CachedCredits(ctx, k, guid)
}

func (s *Server) recordLibraryPersonMappings(ctx context.Context, mappings []frontend.LibraryPersonMapping) {
	if len(mappings) == 0 {
		return
	}
	if credSrc := s.libraryCredits(); credSrc != nil {
		credSrc.RecordPersonMappings(ctx, mappings)
	}
}

func (s *Server) plexPersonByTagKey(ctx context.Context, tagKey string) (frontend.LibraryPersonMapping, bool) {
	credSrc := s.libraryCredits()
	if credSrc == nil {
		return frontend.LibraryPersonMapping{}, false
	}
	return credSrc.PersonByTagKey(ctx, tagKey)
}

func (s *Server) plexPersonByTMDB(ctx context.Context, tmdb int64) (frontend.LibraryPersonMapping, bool) {
	credSrc := s.libraryCredits()
	if credSrc == nil || tmdb <= 0 {
		return frontend.LibraryPersonMapping{}, false
	}
	return credSrc.PersonByTMDB(ctx, tmdb)
}

func (s *Server) plexPeopleByTMDB(ctx context.Context, ids []int64) map[int64]frontend.LibraryPersonMapping {
	credSrc := s.libraryCredits()
	if credSrc == nil || len(ids) == 0 {
		return nil
	}
	return credSrc.PeopleByTMDB(ctx, ids)
}

func producersOf(t *frontend.Title) []frontend.Person {
	if t == nil || len(t.Crew) == 0 {
		return nil
	}
	var out []frontend.Person
	for _, p := range t.Crew {
		if strings.EqualFold(p.Role, "Producer") {
			out = append(out, p)
		}
	}
	return out
}

func detailProducers(t *frontend.Title, credits *frontend.LibraryTitleCredits) []frontend.Person {
	if ps := producersOf(t); len(ps) > 0 {
		return ps
	}
	if credits == nil || len(credits.Producer) == 0 {
		return nil
	}
	out := make([]frontend.Person, 0, len(credits.Producer))
	for _, c := range credits.Producer {
		if c.Name == "" {
			continue
		}
		out = append(out, frontend.Person{
			TMDB:      c.TMDB,
			Name:      c.Name,
			Role:      "Producer",
			ThumbPath: c.Thumb,
		})
	}
	return out
}

func contentRatingAge(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	switch strings.ToUpper(raw) {
	case "TV-Y7", "TV-Y7-FV":
		return 7
	case "PG", "TV-PG":
		return 10
	case "PG-13":
		return 13
	case "TV-14":
		return 14
	case "R", "TV-MA":
		return 17
	case "NC-17":
		return 18
	case "G", "TV-G", "TV-Y":
		return 0
	}
	r := contentrating.Parse(raw)
	if r.Level <= contentrating.AllAges {
		return 0
	}
	if n := labelAgeNumber(r.Label); n > 0 && n <= 21 {
		return n
	}
	if age := r.Level.Age(); age > 0 {
		return age
	}
	return 0
}

func labelAgeNumber(label string) int {
	start := -1
	for i := 0; i < len(label); i++ {
		if label[i] >= '0' && label[i] <= '9' {
			if start == -1 {
				start = i
			}
		} else if start != -1 {
			n, _ := strconv.Atoi(label[start:i])
			return n
		}
	}
	if start != -1 {
		n, _ := strconv.Atoi(label[start:])
		return n
	}
	return 0
}

func (s *Server) applyDetailCredits(ctx context.Context, t *frontend.Title, md *model.Metadata, credits frontend.LibraryTitleCredits) {
	if t == nil || md == nil {
		return
	}
	t = s.enrich(t)
	if ov, ok := s.plexMetaOverlay(ctx, t.Key); ok {
		t = applyPlexMetaTitle(t, ov.Overlay)
	}
	if (t.Type == frontend.TypeMovie || t.Type == frontend.TypeShow) && md.Slug == "" && credits.Slug != "" {
		md.Slug = credits.Slug
	}
	if (t.Type == frontend.TypeMovie || t.Type == frontend.TypeShow) && credits.ContentRatingAge > 0 {
		md.ContentRatingAge = credits.ContentRatingAge
	}
	var mapped []frontend.LibraryPersonMapping
	md.Director = s.personTags(t.Key, t.Directors, "director", math.MaxInt, true, false, &credits, &mapped)
	md.Writer = s.personTags(t.Key, t.Writers, "writer", math.MaxInt, true, false, &credits, &mapped)
	md.Role = s.personTags(t.Key, t.Cast, "actor", math.MaxInt, true, true, &credits, &mapped)
	if t.Type != frontend.TypeSeason {
		md.Producer = s.personTags(t.Key, detailProducers(t, &credits), "producer", math.MaxInt, true, false, &credits, &mapped)
	}
	s.recordLibraryPersonMappings(ctx, mapped)
}
