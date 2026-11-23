package releaseparse

import (
	"path"
	"slices"
	"sort"
	"strings"
)

const (
	packEpisodeMinMedianRatio = 0.15
	packEpisodeMaxMedianRatio = 4.00
)

// PackSizedFile describes one candidate episode file or archive unit in a pack.
type PackSizedFile struct {
	Path string
	Size int64
}

// PackEpisodeSizeGate rejects season-pack episode files whose per-episode byte
// size is an implausible outlier (< 15% or > 400% of the pack's median episode
// size), preventing samples, teasers, or full-season recap/extras files from
// standing in for an episode.
type PackEpisodeSizeGate struct {
	season    int
	cour      int
	peerSizes []int64
	median    float64
}

// NewPackEpisodeSizeGate builds a size gate from the playable candidates of a
// season pack for targetSeason (and optional targetCour). Specials (season <= 0)
// and single-episode releases are unconstrained.
func NewPackEpisodeSizeGate(entries []PackSizedFile, targetSeason, targetCour int) PackEpisodeSizeGate {
	g := PackEpisodeSizeGate{season: targetSeason, cour: targetCour}
	if targetSeason <= 0 || len(entries) < 2 {
		return g
	}
	byEp := map[int][]int64{}
	for _, e := range entries {
		if e.Size <= 0 {
			continue
		}
		epKey, epSpan, ok := packEpisodeKey(e.Path, targetSeason, targetCour)
		if !ok {
			continue
		}
		perEp := max(e.Size/int64(max(epSpan, 1)), 1)
		byEp[epKey] = append(byEp[epKey], perEp)
	}
	if len(byEp) < 2 {
		return g
	}
	peers := make([]int64, 0, len(byEp))
	for _, sizes := range byEp {
		sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
		peers = append(peers, sizes[len(sizes)/2])
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })
	g.peerSizes = peers
	n := len(peers)
	if n%2 == 1 {
		g.median = float64(peers[n/2])
	} else {
		g.median = float64(peers[n/2-1]+peers[n/2]) / 2.0
	}
	return g
}

// Accept reports whether a candidate episode file's size falls within the
// season pack's plausible episode size bounds ([15%, 400%] of median).
func (g PackEpisodeSizeGate) Accept(filePath string, size int64) bool {
	if g.season <= 0 || size <= 0 || len(g.peerSizes) < 2 {
		return true
	}
	epSpan := 1
	if _, span, ok := packEpisodeKey(filePath, g.season, g.cour); ok && span > 1 {
		epSpan = span
	}
	perEp := float64(size) / float64(epSpan)
	if len(g.peerSizes) == 2 {
		// With only two episodes, reject tiny sample/teaser clips (< 15% of the
		// full-sized peer) without rejecting the larger real episode (> 400% of
		// the sample).
		larger := float64(g.peerSizes[1])
		return perEp >= packEpisodeMinMedianRatio*larger
	}
	return perEp >= packEpisodeMinMedianRatio*g.median && perEp <= packEpisodeMaxMedianRatio*g.median
}

func packEpisodeKey(filePath string, targetSeason, targetCour int) (key int, span int, ok bool) {
	normPath := strings.ReplaceAll(filePath, "\\", "/")
	base := path.Base(normPath)
	baseInfo := Parse(base)
	fullInfo := Parse(strings.ReplaceAll(normPath, "/", " "))

	seasons := baseInfo.Seasons
	if len(seasons) == 0 {
		seasons = fullInfo.Seasons
	}
	if len(seasons) > 0 && !slices.Contains(seasons, targetSeason) {
		return 0, 0, false
	}

	cour := baseInfo.Cour
	if cour == 0 {
		cour = fullInfo.Cour
	}
	if targetCour > 0 && cour > 0 && cour != targetCour {
		return 0, 0, false
	}
	if targetCour == 0 && cour > 1 {
		return 0, 0, false
	}

	switch {
	case len(baseInfo.Episodes) > 0:
		return baseInfo.Episodes[0], len(baseInfo.Episodes), true
	case len(baseInfo.AbsoluteEpisodes) > 0:
		return 100000 + baseInfo.AbsoluteEpisodes[0], len(baseInfo.AbsoluteEpisodes), true
	case len(fullInfo.Episodes) == 1 && len(seasons) > 0:
		return fullInfo.Episodes[0], 1, true
	}
	if ep, classicOK := ClassicPackEpisode(normPath, targetSeason); classicOK {
		return 200000 + ep, 1, true
	}
	return 0, 0, false
}
