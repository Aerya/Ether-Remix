package experience

import (
	"context"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

const personRoleSummaryLimit = 64

var canonicalPersonDepartments = []string{"Acting", "Directing", "Writing", "Crew"}

func creditDepartment(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "cast", "actor", "acting":
		return "Acting"
	case "director", "directing":
		return "Directing"
	case "writer", "writing":
		return "Writing"
	case "crew":
		return "Crew"
	case "author":
		return "Author"
	case "narrator":
		return "Narrator"
	case "artist":
		return "Artist"
	case "albumartist", "album_artist":
		return "Album Artist"
	case "composer":
		return "Composer"
	case "illustrator":
		return "Illustrator"
	case "translator":
		return "Translator"
	case "host":
		return "Host"
	case "producer":
		return "Producer"
	case "lyricist":
		return "Lyricist"
	default:
		trimmed := strings.TrimSpace(kind)
		if trimmed == "" {
			return ""
		}
		return strings.ToUpper(trimmed[:1]) + trimmed[1:]
	}
}

func sortPersonRoleCounts(counts map[string]int) []string {
	if len(counts) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(counts))
	roles := make([]string, 0, len(counts))
	for _, dept := range canonicalPersonDepartments {
		if counts[dept] > 0 {
			roles = append(roles, dept)
			seen[dept] = true
		}
	}
	var extra []string
	for k, n := range counts {
		if n > 0 && !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	roles = append(roles, extra...)
	sort.SliceStable(roles, func(a, b int) bool {
		return counts[roles[a]] > counts[roles[b]]
	})
	return roles
}

func (s *Service) enrichPersonDetail(ctx context.Context, viewer *core.Viewer, detail frontend.PersonDetail, prefetch bool) (frontend.PersonDetail, error) {
	if detail.TMDB <= 0 {
		if detail.Roles == nil {
			detail.Roles = []string{}
		}
		return detail, nil
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.PersonDetail{}, err
	}
	counts := map[string]int{}
	if core.IsPluginPersonID(detail.TMDB) {
		pCounts, err := s.pluginPersonRoles(ctx, v, detail.TMDB)
		if err != nil {
			return frontend.PersonDetail{}, err
		}
		counts = pCounts
	} else if s.d.PersonCreditsMany != nil && s.d.peopleVisibleTitles != nil {
		credits, err := s.d.PersonCreditsMany(ctx, []int64{detail.TMDB}, []core.MediaType{core.TypeMovie, core.TypeShow, core.TypeEpisode}, personRoleSummaryLimit)
		if err != nil {
			return frontend.PersonDetail{}, err
		}
		var keys []core.RatingKey
		seen := map[core.RatingKey]bool{}
		for _, cr := range credits[detail.TMDB] {
			if !seen[cr.Key] {
				seen[cr.Key] = true
				keys = append(keys, cr.Key)
			}
		}
		if len(keys) > 0 {
			titles, err := s.d.peopleVisibleTitles(ctx, keys)
			if err != nil {
				return frontend.PersonDetail{}, err
			}
			visibility, err := s.peopleVisibility(ctx, titles)
			if err != nil {
				return frontend.PersonDetail{}, err
			}
			list := make([]core.Title, 0, len(titles))
			for _, k := range keys {
				if t, ok := titles[k]; ok {
					list = append(list, t)
				}
			}
			available := map[core.RatingKey]bool{}
			for _, t := range visibility.Available(ctx, v, list) {
				available[t.Key] = true
			}
			seenDeptPerTitle := map[core.RatingKey]map[string]bool{}
			for _, cr := range credits[detail.TMDB] {
				title, ok := titles[cr.Key]
				if !ok || !available[cr.Key] || strings.TrimSpace(cr.Person.Name) == "" {
					continue
				}
				section, ok := visibility.SectionFor(title.Type)
				if !ok || !SectionAllowed(v, section) || !visibility.Playable(ctx, v, &title) {
					continue
				}
				dept := creditDepartment(cr.Kind)
				if dept == "" {
					continue
				}
				if seenDeptPerTitle[cr.Key] == nil {
					seenDeptPerTitle[cr.Key] = map[string]bool{}
				}
				if !seenDeptPerTitle[cr.Key][dept] {
					seenDeptPerTitle[cr.Key][dept] = true
					counts[dept]++
				}
			}
		}
	}
	if len(counts) == 0 && detail.ArtworkItem != 0 {
		if title, err := s.peopleTitle(ctx, v, detail.ArtworkItem); err == nil && title != nil {
			for _, g := range []struct {
				dept   string
				people []core.Person
			}{
				{"Acting", title.Cast},
				{"Directing", title.Directors},
				{"Writing", title.Writers},
				{"Crew", title.Crew},
			} {
				for _, p := range g.people {
					if p.TMDB == detail.TMDB && strings.TrimSpace(p.Name) != "" {
						counts[g.dept]++
						break
					}
				}
			}
		}
	}
	roles := sortPersonRoleCounts(counts)
	detail.Roles = roles
	if !core.IsPluginPersonID(detail.TMDB) && s.d.PersonMetadata != nil {
		meta := s.d.PersonMetadata(ctx, detail.TMDB, prefetch)
		detail.Biography = meta.Biography
		detail.Birthday = meta.Birthday
		if meta.KnownFor != "" {
			detail.KnownFor = meta.KnownFor
		}
	}
	if detail.KnownFor == "" && len(roles) > 0 {
		detail.KnownFor = roles[0]
	}
	return detail, nil
}
