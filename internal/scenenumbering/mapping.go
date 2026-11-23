package scenenumbering

import "github.com/braedonsaunders/ether/internal/core"

type mappingRow struct {
	TVDB   number  `json:"tvdb"`
	Scene  *number `json:"scene"`
	Scene2 *number `json:"scene_2"`
}
type mapping struct {
	schemes [2]map[int]core.EpisodeNumber // scene, TVDB, indexed by absolute
	owners  map[core.EpisodeNumber]int    // zero means different absolute episodes
}

func valid(n number) bool {
	return n.Season > 0 && n.Season <= 10000 && n.Episode > 0 && n.Episode <= 1000000
}
func pair(n number) core.EpisodeNumber {
	return core.EpisodeNumber{Season: n.Season, Episode: n.Episode}
}
func parseMapping(rows []mappingRow) *mapping {
	m := &mapping{schemes: [2]map[int]core.EpisodeNumber{{}, {}}, owners: map[core.EpisodeNumber]int{}}
	bad := [2]map[int]bool{{}, {}}
	add := func(scheme, absolute int, n number) {
		if !valid(n) {
			return
		}
		p := pair(n)
		if owner, ok := m.owners[p]; ok && owner != absolute {
			m.owners[p] = 0
		} else if !ok {
			m.owners[p] = absolute
		}
		if old, ok := m.schemes[scheme][absolute]; ok && old != p {
			bad[scheme][absolute] = true
		}
		m.schemes[scheme][absolute] = p
	}
	for _, row := range rows {
		abs := row.TVDB.Absolute
		if abs <= 0 || abs > 1000000 || !valid(row.TVDB) {
			continue
		}
		add(1, abs, row.TVDB)
		if row.Scene != nil {
			add(0, abs, *row.Scene)
		}
		if row.Scene2 != nil {
			if row.Scene != nil && pair(*row.Scene2) != pair(*row.Scene) {
				bad[0][abs] = true
			}
			// A secondary scene identity also participates in collision detection.
			p := pair(*row.Scene2)
			if valid(*row.Scene2) {
				if owner, ok := m.owners[p]; ok && owner != abs {
					m.owners[p] = 0
				} else if !ok {
					m.owners[p] = abs
				}
			}
		}
	}
	for scheme := range bad {
		for abs := range bad[scheme] {
			delete(m.schemes[scheme], abs)
		}
	}
	return m
}
func absoluteNumber(req core.SearchRequest) int {
	if req.AbsoluteEpisode > 0 {
		return req.AbsoluteEpisode
	}
	if req.Season == 1 {
		return req.Episode
	}
	return 0
}

func canonicalOwner(n core.EpisodeNumber, req core.SearchRequest, layout core.EpisodeLayout) (int, bool) {
	if absolute, ok := layout.Numbers[n]; ok {
		return absolute, true
	}
	if n.Season == 1 {
		return n.Episode, true
	}
	if n.Season == req.Season {
		return absoluteNumber(req) - req.Episode + n.Episode, true
	}
	if layout.Seasons != nil && (!layout.Seasons[n.Season] || layout.Complete[n.Season]) {
		return 0, true
	}
	return 0, false
}
func sameSeason(a, b map[int]core.EpisodeNumber, season int) bool {
	countA, countB := 0, 0
	for abs, n := range a {
		if n.Season == season {
			countA++
			if b[abs] != n {
				return false
			}
		}
	}
	for _, n := range b {
		if n.Season == season {
			countB++
		}
	}
	return countA == countB
}
func (m *mapping) lookup(req core.SearchRequest, layout core.EpisodeLayout) core.ReleaseNumbering {
	absolute := absoluteNumber(req)
	canonical := core.EpisodeNumber{Season: req.Season, Episode: req.Episode}
	out := core.ReleaseNumbering{}
	for _, scheme := range m.schemes {
		n, ok := scheme[absolute]
		if !ok || n == canonical || m.owners[n] != absolute {
			continue
		}
		if owner, known := canonicalOwner(n, req, layout); !known || (owner != 0 && owner != absolute) {
			continue
		}
		duplicate := false
		for _, prev := range out.Aliases {
			duplicate = duplicate || prev == n
		}
		if !duplicate {
			out.Aliases = append(out.Aliases, n)
		}
	}
	// A pack can use a numbering only when every other fitting numbering
	// agrees on the season's file identities, not just this episode's position.
	canon := map[int]core.EpisodeNumber{}
	for n, abs := range layout.Numbers {
		canon[abs] = n
	}
	canon[absolute] = canonical
	schemes := []map[int]core.EpisodeNumber{canon, m.schemes[0], m.schemes[1]}
	candidates := append([]core.EpisodeNumber{canonical}, out.Aliases...)
	for _, n := range candidates {
		var fitting map[int]core.EpisodeNumber
		ambiguous := false
		for i, scheme := range schemes {
			if wanted, ok := scheme[absolute]; !ok || wanted.Season != n.Season {
				continue
			}
			if i == 0 && !layout.Complete[n.Season] && n.Season == canonical.Season {
				ambiguous = true
				break
			}
			if fitting != nil && !sameSeason(fitting, scheme, n.Season) {
				ambiguous = true
				break
			}
			fitting = scheme
		}
		if !ambiguous {
			out.PackTargets = append(out.PackTargets, n)
		}
	}
	return out
}
