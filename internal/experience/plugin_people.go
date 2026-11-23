package experience

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// pluginPeopleTitleSearchScore is the relevance score assigned to plugin titles
// matched via cast member/people search so exact, prefix, and token title matches
// always rank ahead of cast member-only matches.
const pluginPeopleTitleSearchScore = 0.60

// PluginPersonMatch is one person matched in a plugin library along with their
// role counts and visible credited titles.
type PluginPersonMatch struct {
	PersonID    int64
	Name        string
	CreditCount int
	ArtworkItem core.RatingKey
	HasThumb    bool
	Roles       map[string]int
	Titles      []core.Title
}

// PluginPeopleLibraries is an optional extension implemented by PluginLibraries
// adapters that index declared people fields on plugin items.
type PluginPeopleLibraries interface {
	SearchPeople(ctx context.Context, lib core.Library, query string, q frontend.ItemQuery, personLimit, itemLimit int) ([]PluginPersonMatch, error)
	PersonByID(ctx context.Context, lib core.Library, personID int64, itemLimit int) (PluginPersonMatch, bool, error)
	FindPersonID(ctx context.Context, lib core.Library, match func(personID int64, name string) bool) (int64, bool, error)
}

func (s *Service) allowedPluginPeopleLibraries(ctx context.Context, v *core.User) ([]core.Library, PluginPeopleLibraries, error) {
	if s == nil || s.d.PluginLibraries == nil || v == nil {
		return nil, nil, nil
	}
	ppl, ok := s.d.PluginLibraries.(PluginPeopleLibraries)
	if !ok {
		return nil, nil, nil
	}
	libs, err := s.d.PluginLibraries.Libraries(ctx)
	if err != nil {
		return nil, nil, err
	}
	var allowed []core.Library
	for _, lib := range libs {
		if lib.Source != core.LibraryPlugin || !v.AllowsLibrary(lib.ID) {
			continue
		}
		if _, _, pub := s.publishedLibrary(ctx, lib); !pub {
			continue
		}
		allowed = append(allowed, lib)
	}
	return allowed, ppl, nil
}

func rolesFromCounts(counts map[string]int) []string {
	return sortPersonRoleCounts(counts)
}

func mergeRoleBucketCounts(dst map[string]int, raw map[string]int) {
	for k, n := range raw {
		if n <= 0 {
			continue
		}
		dept := creditDepartment(k)
		if dept != "" {
			dst[dept] += n
		}
	}
}

// SearchPluginPeople searches people across the viewer's allowed, published
// plugin libraries and returns both the matched people cards and all of their
// visible credited plugin titles. Exposed as a standalone people-titles search
// source so unified people+title search can join catalogue and plugin sources.
func (s *Service) SearchPluginPeople(ctx context.Context, viewer *core.Viewer, query string, q frontend.ItemQuery, limit int) ([]frontend.PersonDetail, []core.Title, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, nil, err
	}
	return s.searchPluginPeopleForUser(ctx, v, query, q, limit)
}

func (s *Service) searchPluginPeopleForUser(ctx context.Context, v *core.User, query string, q frontend.ItemQuery, limit int) ([]frontend.PersonDetail, []core.Title, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil, nil
	}
	libs, ppl, err := s.allowedPluginPeopleLibraries(ctx, v)
	if err != nil || len(libs) == 0 || ppl == nil {
		return nil, nil, err
	}
	return s.searchPluginPeopleInLibraries(ctx, v, libs, ppl, query, q, limit)
}

func (s *Service) searchPluginPeopleInLibraries(ctx context.Context, v *core.User, libs []core.Library, ppl PluginPeopleLibraries, query string, q frontend.ItemQuery, limit int) ([]frontend.PersonDetail, []core.Title, error) {
	terms := peopleTerms(query)
	if len(terms) == 0 {
		return nil, nil, nil
	}
	exact := strings.Join(peopleWords(query), " ")
	exactFolded := strings.Join(peopleFoldedWords(query), " ")

	personCap := limit
	if personCap <= 0 {
		personCap = 20
	}
	titleCap := max(q.Offset, 0) + max(q.Limit, 50)
	titleCap = min(max(titleCap*2, 100), 500)

	subQ := q
	subQ.Filter.Query = ""
	subQ.Recursive = true

	type aggPerson struct {
		vis   visiblePerson
		roles map[string]int
	}
	byID := make(map[int64]*aggPerson)
	var titles []core.Title
	seenTitle := make(map[core.RatingKey]bool)

	for _, lib := range libs {
		matches, err := ppl.SearchPeople(ctx, lib, query, subQ, personCap, titleCap)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, nil, err
			}
			continue
		}
		for _, m := range matches {
			if !core.IsPluginPersonID(m.PersonID) || !personMatches(m.Name, terms) {
				continue
			}
			visibleTitles, err := s.filterPluginTitles(ctx, v, m.Titles, subQ)
			if err != nil {
				return nil, nil, err
			}
			if len(visibleTitles) == 0 {
				continue
			}
			ap := byID[m.PersonID]
			if ap == nil {
				ap = &aggPerson{roles: make(map[string]int)}
				byID[m.PersonID] = ap
			}
			mergeRoleBucketCounts(ap.roles, m.Roles)
			for _, t := range visibleTitles {
				for _, group := range [][]core.Person{t.Cast, t.Directors, t.Writers, t.Crew} {
					for _, p := range group {
						if p.TMDB == m.PersonID && strings.TrimSpace(p.Name) != "" {
							if merged, mErr := mergeVisiblePerson(ap.vis, p, t.Key); mErr == nil {
								ap.vis = merged
							}
						}
					}
				}
				if !seenTitle[t.Key] {
					seenTitle[t.Key] = true
					// A full-name match ranks the person's titles like a
					// catalogue person match: right after exact titles.
					floor := pluginPeopleTitleSearchScore
					if strings.Join(peopleFoldedWords(m.Name), " ") == exactFolded {
						floor = PersonMatchScore
					}
					if t.SearchScore < floor {
						t.SearchScore = floor
					}
					titles = append(titles, t)
				}
			}
			if ap.vis.detail.ID == "" && len(visibleTitles) > 0 {
				ap.vis = visiblePerson{
					detail: frontend.PersonDetail{
						ID:          personFacetID(m.PersonID),
						Name:        m.Name,
						TMDB:        m.PersonID,
						ArtworkItem: visibleTitles[0].Key,
					},
					hasPortrait: m.HasThumb,
				}
			}
		}
	}

	if len(byID) == 0 {
		return nil, titles, nil
	}
	people := make([]frontend.PersonDetail, 0, len(byID))
	for _, ap := range byID {
		if ap.vis.detail.ID == "" || !personMatches(ap.vis.detail.Name, terms) {
			continue
		}
		d := ap.vis.detail
		d.Roles = rolesFromCounts(ap.roles)
		if d.KnownFor == "" && len(d.Roles) > 0 {
			d.KnownFor = d.Roles[0]
		}
		people = append(people, d)
	}
	sort.Slice(people, func(a, b int) bool {
		return personBefore(people[a], people[b], exact)
	})
	if limit > 0 && len(people) > limit {
		people = people[:limit]
	}
	return people, titles, nil
}

func (s *Service) pluginPeopleForScope(ctx context.Context, viewer *core.Viewer, q frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	libs, ppl, err := s.allowedPluginPeopleLibraries(ctx, v)
	if err != nil || len(libs) == 0 || ppl == nil {
		return nil, err
	}
	types, err := peopleTypes(q.Scope)
	if err != nil {
		return nil, err
	}
	terms := peopleTerms(q.Query)
	if strings.TrimSpace(q.Query) != "" && len(terms) == 0 {
		return nil, nil
	}
	exact := strings.Join(peopleWords(q.Query), " ")
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	itemQ := frontend.ItemQuery{
		Types:     types,
		Recursive: true,
	}

	byID := make(map[int64]visiblePerson)
	for _, lib := range libs {
		matches, err := ppl.SearchPeople(ctx, lib, q.Query, itemQ, limit, personCreditLimit)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			continue
		}
		for _, m := range matches {
			if !core.IsPluginPersonID(m.PersonID) || !personMatches(m.Name, terms) {
				continue
			}
			visibleTitles, err := s.filterPluginTitles(ctx, v, m.Titles, itemQ)
			if err != nil {
				return nil, err
			}
			for _, t := range visibleTitles {
				for _, group := range [][]core.Person{t.Cast, t.Directors, t.Writers, t.Crew} {
					for _, p := range group {
						if p.TMDB == m.PersonID && strings.TrimSpace(p.Name) != "" {
							merged, mErr := mergeVisiblePerson(byID[m.PersonID], p, t.Key)
							if mErr != nil {
								return nil, mErr
							}
							byID[m.PersonID] = merged
						}
					}
				}
			}
		}
	}
	if len(byID) == 0 {
		return nil, nil
	}
	out := make([]frontend.PersonDetail, 0, len(byID))
	for _, vp := range byID {
		if vp.detail.ID == "" {
			continue
		}
		current, err := s.currentPerson(ctx, viewer, vp.detail)
		if err != nil {
			continue
		}
		if personMatches(current.Name, terms) {
			out = append(out, current)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		return personBefore(out[a], out[b], exact)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func mergePeopleLists(a, b []frontend.PersonDetail, exact string, limit int) []frontend.PersonDetail {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		if limit > 0 && len(b) > limit {
			return b[:limit]
		}
		return b
	}
	seen := make(map[string]int, len(a)+len(b))
	merged := make([]frontend.PersonDetail, 0, len(a)+len(b))
	for _, p := range a {
		seen[p.ID] = len(merged)
		merged = append(merged, p)
	}
	for _, p := range b {
		if _, exists := seen[p.ID]; !exists {
			seen[p.ID] = len(merged)
			merged = append(merged, p)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return personBefore(merged[i], merged[j], exact)
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func (s *Service) pluginPerson(ctx context.Context, viewer *core.Viewer, personID int64) (frontend.PersonDetail, error) {
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.PersonDetail{}, err
	}
	libs, ppl, err := s.allowedPluginPeopleLibraries(ctx, v)
	if err != nil {
		return frontend.PersonDetail{}, err
	}
	if len(libs) == 0 || ppl == nil {
		return frontend.PersonDetail{}, core.ErrNotFound
	}
	itemQ := frontend.ItemQuery{Recursive: true}
	var found visiblePerson
	musicMatches := 0
	totalMatches := 0
	samplePoster := ""
	var samplePosterKey core.RatingKey
	for _, lib := range libs {
		pm, ok, err := ppl.PersonByID(ctx, lib, personID, 25)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return frontend.PersonDetail{}, err
			}
			continue
		}
		if !ok {
			continue
		}
		visibleTitles, err := s.filterPluginTitles(ctx, v, pm.Titles, itemQ)
		if err != nil {
			return frontend.PersonDetail{}, err
		}
		for _, t := range visibleTitles {
			matched := false
			for _, group := range [][]core.Person{t.Cast, t.Directors, t.Writers, t.Crew} {
				for _, p := range group {
					if p.TMDB == personID && strings.TrimSpace(p.Name) != "" {
						found, err = mergeVisiblePerson(found, p, t.Key)
						if err != nil {
							return frontend.PersonDetail{}, err
						}
						matched = true
					}
				}
			}
			if matched {
				totalMatches++
				if t.MBItemType == "MusicAlbum" || t.MBItemType == "Audio" || t.MBItemType == "MusicArtist" {
					musicMatches++
				}
				if samplePoster == "" && t.PosterPath != "" {
					samplePoster = t.PosterPath
					samplePosterKey = t.Key
				}
			}
		}
	}
	if found.detail.ID == "" {
		return frontend.PersonDetail{}, core.ErrNotFound
	}
	detail, err := s.currentPerson(ctx, viewer, found.detail)
	if err != nil {
		return frontend.PersonDetail{}, err
	}
	if totalMatches > 0 && musicMatches == totalMatches {
		detail.KnownFor = "MusicArtist"
		if len(detail.Images) == 0 && samplePoster != "" && samplePosterKey != 0 {
			detail.ArtworkItem = samplePosterKey
			detail.Images = []frontend.Image{{Kind: frontend.ImagePoster, Path: samplePoster}}
		}
	}
	return detail, nil
}

func (s *Service) pluginPersonRoles(ctx context.Context, v *core.User, personID int64) (map[string]int, error) {
	counts := map[string]int{}
	libs, ppl, err := s.allowedPluginPeopleLibraries(ctx, v)
	if err != nil || len(libs) == 0 || ppl == nil {
		return counts, err
	}
	itemQ := frontend.ItemQuery{Recursive: true}
	for _, lib := range libs {
		pm, ok, err := ppl.PersonByID(ctx, lib, personID, personRoleSummaryLimit)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			continue
		}
		if !ok {
			continue
		}
		visibleTitles, err := s.filterPluginTitles(ctx, v, pm.Titles, itemQ)
		if err != nil {
			return nil, err
		}
		if len(visibleTitles) == 0 {
			continue
		}
		seenDeptPerTitle := map[core.RatingKey]map[string]bool{}
		for _, t := range visibleTitles {
			if len(t.MediaCredits) > 0 && t.MediaClass != "" && t.MediaClass != core.MediaClassVideo {
				for _, cr := range t.MediaCredits {
					if cr.TMDB == personID && strings.TrimSpace(cr.Name) != "" {
						dept := creditDepartment(cr.Role)
						if dept == "" {
							continue
						}
						if seenDeptPerTitle[t.Key] == nil {
							seenDeptPerTitle[t.Key] = map[string]bool{}
						}
						if !seenDeptPerTitle[t.Key][dept] {
							seenDeptPerTitle[t.Key][dept] = true
							counts[dept]++
						}
					}
				}
				continue
			}
			for _, g := range []struct {
				dept   string
				people []core.Person
			}{
				{"Acting", t.Cast},
				{"Directing", t.Directors},
				{"Writing", t.Writers},
				{"Crew", t.Crew},
			} {
				for _, p := range g.people {
					if p.TMDB == personID && strings.TrimSpace(p.Name) != "" {
						if seenDeptPerTitle[t.Key] == nil {
							seenDeptPerTitle[t.Key] = map[string]bool{}
						}
						if !seenDeptPerTitle[t.Key][g.dept] {
							seenDeptPerTitle[t.Key][g.dept] = true
							counts[g.dept]++
						}
					}
				}
			}
		}
	}
	return counts, nil
}

func isHexFacetID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (s *Service) pluginPersonByFacetID(ctx context.Context, id string) (int64, bool, error) {
	if s == nil || s.d.PluginLibraries == nil || !isHexFacetID(id) {
		return 0, false, nil
	}
	ppl, ok := s.d.PluginLibraries.(PluginPeopleLibraries)
	if !ok {
		return 0, false, nil
	}
	noViewerCtx := core.WithoutCatalogViewer(ctx)
	libs, err := s.d.PluginLibraries.Libraries(noViewerCtx)
	if err != nil {
		return 0, false, err
	}
	for _, lib := range libs {
		if lib.Source != core.LibraryPlugin {
			continue
		}
		pid, found, err := ppl.FindPersonID(noViewerCtx, lib, func(personID int64, _ string) bool {
			return facetID("person", strconv.FormatInt(personID, 10)) == id
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return 0, false, err
			}
			continue
		}
		if found && core.IsPluginPersonID(pid) {
			personFacetID(pid)
			return pid, true, nil
		}
	}
	return 0, false, nil
}

func (s *Service) resolvePluginPersonFacet(ctx context.Context, viewer *core.Viewer, viewID, id string, personID int64) (core.FacetValue, bool, error) {
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return core.FacetValue{}, true, err
	}
	libs, ppl, err := s.allowedPluginPeopleLibraries(ctx, v)
	if err != nil {
		return core.FacetValue{}, true, err
	}
	if ppl == nil {
		return core.FacetValue{}, false, nil
	}
	for _, lib := range libs {
		if viewID != "" && strconv.FormatInt(int64(lib.ID), 10) != viewID {
			continue
		}
		pm, ok, err := ppl.PersonByID(core.WithoutCatalogViewer(ctx), lib, personID, 1)
		if err != nil {
			return core.FacetValue{}, true, err
		}
		if ok && strings.TrimSpace(pm.Name) != "" {
			return core.FacetValue{
				ID:    id,
				Key:   strconv.FormatInt(personID, 10),
				Title: pm.Name,
			}, true, nil
		}
	}
	return core.FacetValue{}, true, core.ErrNotFound
}
