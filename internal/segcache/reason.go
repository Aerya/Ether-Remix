package segcache

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// ReleaseReason is the owner-facing explanation for one cached release.
type ReleaseReason struct {
	Causes    []core.CacheCause    `json:"causes"`
	Retention RetentionExplanation `json:"retention"`
	File      string               `json:"file"`
	Source    string               `json:"source"`
	Indexer   string               `json:"indexer,omitempty"`
	Origin    string               `json:"origin,omitempty"`
	RatingKey core.RatingKey       `json:"ratingKey,omitempty"`
	CachedAt  time.Time            `json:"cachedAt,omitzero"`
	Info      *core.ReleaseInfo    `json:"info,omitempty"`
}

// RetentionExplanation says why bytes are still retained, or how they leave.
type RetentionExplanation struct {
	Kind  string    `json:"kind"` // pinned | expiry | replay | lru
	Pins  []string  `json:"pins,omitempty"`
	Days  int       `json:"days,omitempty"`
	Hours float64   `json:"hours,omitempty"`
	Until time.Time `json:"until,omitzero"`
}

// SegmentSpan is an inclusive contiguous range of cached segment numbers.
type SegmentSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ReleaseDetail summarizes which parts/segments of a single release are cached.
type ReleaseDetail struct {
	ReleaseID      string        `json:"releaseId"`
	Segments       int           `json:"segments"`
	EstimatedTotal int           `json:"estimatedTotal,omitempty"`
	SpanCount      int           `json:"spanCount"`
	Spans          []SegmentSpan `json:"spans"`
	Coverage       []float64     `json:"coverage,omitempty"`
	FirstCached    time.Time     `json:"firstCached,omitzero"`
}

const (
	maxReportedSpans = 32
	coverageSlots    = 48
)

// buildReleaseDetail summarizes sorted segment numbers into contiguous spans
// and a fixed-width coverage strip over the estimated file length.
func buildReleaseDetail(id string, sorted []int, bytes, size int64, firstCached time.Time) ReleaseDetail {
	d := ReleaseDetail{
		ReleaseID:   id,
		Segments:    len(sorted),
		Spans:       []SegmentSpan{},
		FirstCached: firstCached,
	}
	if len(sorted) == 0 {
		return d
	}
	cur := SegmentSpan{Start: sorted[0], End: sorted[0]}
	for _, seg := range sorted[1:] {
		if seg <= cur.End+1 {
			if seg > cur.End {
				cur.End = seg
			}
			continue
		}
		d.SpanCount++
		if len(d.Spans) < maxReportedSpans {
			d.Spans = append(d.Spans, cur)
		}
		cur = SegmentSpan{Start: seg, End: seg}
	}
	d.SpanCount++
	if len(d.Spans) < maxReportedSpans {
		d.Spans = append(d.Spans, cur)
	}

	total := sorted[len(sorted)-1] + 1
	if size > 0 && bytes > 0 {
		est := int((size*int64(len(sorted)) + bytes/2) / bytes)
		if est > total {
			total = est
		}
	}
	d.EstimatedTotal = total
	if total > 0 {
		slots := min(coverageSlots, total)
		counts := make([]int, slots)
		for _, seg := range sorted {
			idx := int(int64(seg) * int64(slots) / int64(total))
			if idx >= slots {
				idx = slots - 1
			}
			if idx >= 0 {
				counts[idx]++
			}
		}
		d.Coverage = make([]float64, slots)
		for i, n := range counts {
			slotStart := int(int64(i) * int64(total) / int64(slots))
			slotEnd := int(int64(i+1) * int64(total) / int64(slots))
			capSegs := max(1, slotEnd-slotStart)
			d.Coverage[i] = min(1, float64(n)/float64(capSegs))
		}
	}
	return d
}

// AddCause merges a response-time cause such as a live session state.
func (r *ReleaseReason) AddCause(add core.CacheCause) {
	if r == nil || add.Code == "" || add.Code == core.CacheCauseUnrecorded {
		return
	}
	if len(r.Causes) == 1 && r.Causes[0].Code == core.CacheCauseUnrecorded {
		r.Causes = r.Causes[:0]
	}
	for i := range r.Causes {
		if r.Causes[i].Code != add.Code {
			continue
		}
		if r.Causes[i].FirstSeen.IsZero() || (!add.FirstSeen.IsZero() && add.FirstSeen.Before(r.Causes[i].FirstSeen)) {
			r.Causes[i].FirstSeen = add.FirstSeen
		}
		return
	}
	r.Causes = append(r.Causes, add)
	sort.SliceStable(r.Causes, func(i, j int) bool { return causeRank[r.Causes[i].Code] < causeRank[r.Causes[j].Code] })
}

var causeRank = map[string]int{
	core.CacheCausePlaying:      0,
	core.CacheCausePaused:       1,
	core.CacheCauseUnfinished:   2,
	core.CacheCauseOwnerPin:     3,
	core.CacheCauseShelf:        4,
	core.CacheCauseNextEpisode:  5,
	core.CacheCausePostPlay:     6,
	core.CacheCauseResumeWarm:   7,
	core.CacheCauseDetailWarm:   8,
	core.CacheCauseTrailerWarm:  9,
	core.CacheCauseBrowseWarm:   10,
	core.CacheCauseWatchlist:    11,
	core.CacheCauseQualityCheck: 12,
	core.CacheCauseFailover:     13,
	core.CacheCauseRepair:       14,
}

func looksOpaqueHash(s string) bool {
	stem := strings.TrimSpace(s)
	if ext := filepath.Ext(stem); len(ext) >= 2 && len(ext) <= 6 {
		stem = strings.TrimSuffix(stem, ext)
	}
	if len(stem) < 24 {
		return false
	}
	for i := 0; i < len(stem); i++ {
		c := stem[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func parseMeaningfulInfo(candidates ...string) *core.ReleaseInfo {
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if raw == "" || looksOpaqueHash(raw) {
			continue
		}
		info := releaseparse.Parse(raw)
		if info.Height > 0 || info.Source != "" || info.VideoCodec != "" || info.AudioCodec != "" || info.HDR10 || info.DolbyVision || info.Atmos || info.Group != "" {
			return &info
		}
	}
	return nil
}

func earliestTime(base time.Time, causes []core.CacheCause) time.Time {
	best := base
	for _, c := range causes {
		if !c.FirstSeen.IsZero() && (best.IsZero() || c.FirstSeen.Before(best)) {
			best = c.FirstSeen
		}
	}
	return best
}

// reasonFor renders stored causes and current policy. Unknown codes are
// ignored; with nothing known the only reported cause is unrecorded.
func reasonFor(u *ReleaseUsage, policy Policy, now time.Time) *ReleaseReason {
	causes := make([]core.CacheCause, 0, len(u.causes))
	for _, c := range u.causes {
		if _, ok := causeRank[c.Code]; ok {
			causes = append(causes, c)
		}
	}
	sort.SliceStable(causes, func(i, j int) bool { return causeRank[causes[i].Code] < causeRank[causes[j].Code] })
	if len(causes) == 0 {
		causes = []core.CacheCause{{Code: core.CacheCauseUnrecorded}}
	}
	ret := RetentionExplanation{Kind: "lru"}
	switch {
	case len(u.Pins) > 0:
		ret = RetentionExplanation{Kind: "pinned", Pins: append([]string(nil), u.Pins...)}
	case policy.Retention > 0:
		ret = RetentionExplanation{Kind: "expiry", Days: int(policy.Retention / (24 * time.Hour)), Until: u.LastUsed.Add(policy.Retention)}
	case policy.Mode == "adaptive" && policy.ReplayBoost > 0 && u.Plays > 1 && !u.LastPlayed.IsZero() && now.Sub(u.LastPlayed) < 30*24*time.Hour:
		if hours := float64(min(max(u.Plays-1, 0), 8)) * policy.ReplayBoost.Hours(); hours > 0 {
			ret = RetentionExplanation{Kind: "replay", Hours: hours}
		}
	}
	return &ReleaseReason{
		Causes:    causes,
		Retention: ret,
		File:      u.file,
		Source:    u.source,
		Indexer:   u.indexer,
		Origin:    u.origin,
		RatingKey: u.ratingKey,
		CachedAt:  earliestTime(u.cachedAt, causes),
		Info:      parseMeaningfulInfo(u.source, u.file),
	}
}
