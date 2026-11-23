package adapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/stremio"
)

const (
	// eventAddonRefreshInterval is the short refresh schedule used for
	// event-based add-ons ONLY while the source is actively in use.
	eventAddonRefreshInterval = 5 * time.Minute
	// defaultEventDuration is the fallback programme duration when an event
	// omits runtime and endTime.
	defaultEventDuration = 2 * time.Hour
	minEventDuration     = 15 * time.Minute
	maxEventDuration     = 12 * time.Hour
	// eventLiveAnchorStep aligns untimed/live-now events to a stable bucket so
	// repeated refreshes within the bucket produce identical programme rows.
	eventLiveAnchorStep = 15 * time.Minute
	// maxEventCatalogItems bounds one event catalog read per refresh.
	maxEventCatalogItems = 200
	// maxEventMetaEnrich bounds deduplicated /meta/ lookups during one import.
	maxEventMetaEnrich = 40
	// maxTuneLiveEvents bounds concurrent live events resolved on one tune.
	maxTuneLiveEvents = 6
	// eventOpenDeadline bounds tune-time live event resolution.
	eventOpenDeadline = 5 * time.Second
)

func eventCatalogGuideID(cat stremio.Catalog) string {
	return "event:" + cat.Type + ":" + cat.ID
}

func eventChannelStreamURL(pluginID, catType, catID string) string {
	return "plugin://" + pluginID + "/event/" + url.PathEscape(catType) + "/" + url.PathEscape(catID)
}

func parseEventChannelStreamURL(raw string) (pluginID, catType, catID string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "plugin" || u.Host == "" {
		return "", "", "", false
	}
	rest, found := strings.CutPrefix(u.EscapedPath(), "/event/")
	if !found || rest == "" {
		return "", "", "", false
	}
	rawType, rawID, found := strings.Cut(rest, "/")
	if !found || rawType == "" || rawID == "" || strings.Contains(rawID, "/") {
		return "", "", "", false
	}
	catType, err = url.PathUnescape(rawType)
	if err != nil || catType == "" {
		return "", "", "", false
	}
	catID, err = url.PathUnescape(rawID)
	if err != nil || catID == "" {
		return "", "", "", false
	}
	return u.Host, catType, catID, true
}

func (k *channelSourceKind) fetchEventCatalogs(
	ctx context.Context,
	addon stremio.Addon,
	src *livetv.Source,
	cats []stremio.Catalog,
	now time.Time,
) ([]livetv.Channel, livetv.XMLGuide, int, error) {
	guide := livetv.XMLGuide{Channels: map[string]livetv.Channel{}, Names: map[string][]string{}}
	if len(cats) == 0 {
		return nil, guide, 0, nil
	}
	items := make([][]stremio.MetaPreview, len(cats))
	errs := make([]error, len(cats))
	forEachBounded(ctx, len(cats), addonGuideWorkers, func(i int) {
		items[i], errs[i] = addon.Catalog(ctx, cats[i], nil, maxEventCatalogItems)
	})
	if err := ctx.Err(); err != nil {
		return nil, guide, 0, err
	}

	var failed int
	var firstErr error
	var totalEvents int
	for i := range cats {
		if errs[i] != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("the add-on's %q catalog: %w", firstNonEmpty(cats[i].Name, cats[i].ID), errs[i])
			}
			continue
		}
		totalEvents += len(items[i])
	}
	if failed == len(cats) {
		return nil, guide, 0, fmt.Errorf("the add-on's live event catalogs could not be read: %w", firstErr)
	}

	enriched := enrichEventMetas(ctx, addon, cats, items, maxEventMetaEnrich)
	if err := ctx.Err(); err != nil {
		return nil, guide, 0, err
	}

	channels := make([]livetv.Channel, 0, len(cats))
	for i, cat := range cats {
		rawID := eventCatalogGuideID(cat)
		chID := stableID(src.ID, rawID)
		chName := strings.TrimSpace(firstNonEmpty(cat.Name, cat.ID))
		chLogo := strings.TrimSpace(addon.Manifest.Logo)
		if chLogo == "" {
			for _, m := range items[i] {
				if logo := strings.TrimSpace(firstNonEmpty(m.Logo, m.Poster)); logo != "" {
					chLogo = logo
					break
				}
			}
		}
		eventURL := eventChannelStreamURL(k.pluginID, cat.Type, cat.ID)
		ch := livetv.Channel{
			ID:       chID,
			SourceID: src.ID,
			GuideID:  rawID,
			Name:     chName,
			Logo:     chLogo,
			Group:    strings.TrimSpace(firstNonEmpty(addon.Manifest.Name, cat.Type)),
			Kind:     "iptv",
			Enabled:  true,
			StreamSources: []livetv.StreamSource{{
				ID:       livetv.AddonInputID(chID, 0),
				Name:     chName,
				URL:      eventURL,
				Enabled:  true,
				Priority: 0,
			}},
			StreamURL: eventURL,
		}
		channels = append(channels, ch)
		guide.Channels[rawID] = livetv.Channel{GuideID: rawID, Name: chName, Logo: chLogo}
		guide.Names[rawID] = []string{chName}

		if errs[i] != nil {
			continue
		}
		progs := buildCatalogEventProgrammes(rawID, cat.Type, chName, items[i], enriched, now)
		guide.Programmes = append(guide.Programmes, progs...)
	}
	return channels, guide, totalEvents, nil
}

type eventMetaKey struct {
	typ string
	id  string
}

func enrichEventMetas(ctx context.Context, addon stremio.Addon, cats []stremio.Catalog, items [][]stremio.MetaPreview, limit int) map[eventMetaKey]stremio.Meta {
	if limit <= 0 || !addon.Manifest.HasResource(stremio.ResourceMeta) {
		return nil
	}
	var want []eventMetaKey
	seen := map[eventMetaKey]bool{}
	for i, cat := range cats {
		for _, m := range items[i] {
			if len(want) >= limit {
				break
			}
			if m.ID == "" {
				continue
			}
			typ := firstNonEmpty(strings.TrimSpace(m.Type), cat.Type)
			if !addon.Manifest.TakesID(stremio.ResourceMeta, typ, m.ID) {
				continue
			}
			if !needsEventMetaEnrich(m) {
				continue
			}
			key := eventMetaKey{typ: typ, id: m.ID}
			if seen[key] {
				continue
			}
			seen[key] = true
			want = append(want, key)
		}
		if len(want) >= limit {
			break
		}
	}
	if len(want) == 0 {
		return nil
	}
	fetched := make([]stremio.Meta, len(want))
	ok := make([]bool, len(want))
	forEachBounded(ctx, len(want), addonStreamWorkers, func(i int) {
		meta, err := addon.Meta(ctx, want[i].typ, want[i].id)
		if err == nil && (meta.ID != "" || meta.Name != "" || len(meta.Videos) > 0 || meta.Released != "" || meta.ReleaseInfo != "" || meta.Runtime != "" || meta.Description != "") {
			fetched[i] = meta
			ok[i] = true
		}
	})
	out := make(map[eventMetaKey]stremio.Meta, len(want))
	for i, key := range want {
		if ok[i] {
			out[key] = fetched[i]
		}
	}
	return out
}

func needsEventMetaEnrich(m stremio.MetaPreview) bool {
	if m.BehaviorHints.HasScheduledVideos {
		return true
	}
	if strings.TrimSpace(m.Released) == "" && strings.TrimSpace(m.ReleaseInfo) == "" {
		return true
	}
	if strings.TrimSpace(m.Runtime) == "" || strings.TrimSpace(m.Description) == "" {
		return true
	}
	return false
}

func mergeEventMeta(preview stremio.MetaPreview, detail stremio.Meta, hasDetail bool) stremio.Meta {
	out := stremio.Meta{MetaPreview: preview}
	if !hasDetail {
		return out
	}
	if v := strings.TrimSpace(detail.Name); v != "" {
		out.Name = v
	}
	if v := strings.TrimSpace(detail.Type); v != "" {
		out.Type = v
	}
	if v := strings.TrimSpace(detail.Poster); v != "" {
		out.Poster = v
	}
	if v := strings.TrimSpace(detail.Logo); v != "" {
		out.Logo = v
	}
	if v := strings.TrimSpace(detail.Background); v != "" {
		out.Background = v
	}
	if v := strings.TrimSpace(detail.Description); v != "" {
		out.Description = v
	}
	if len(detail.Genres) > 0 {
		out.Genres = detail.Genres
	}
	if v := strings.TrimSpace(detail.ReleaseInfo); v != "" {
		out.ReleaseInfo = v
	}
	if v := strings.TrimSpace(detail.Released); v != "" {
		out.Released = v
	}
	if v := strings.TrimSpace(detail.Runtime); v != "" {
		out.Runtime = v
	}
	if v := strings.TrimSpace(detail.BehaviorHints.DefaultVideoID); v != "" {
		out.BehaviorHints.DefaultVideoID = v
	}
	if detail.BehaviorHints.HasScheduledVideos {
		out.BehaviorHints.HasScheduledVideos = true
	}
	out.Videos = detail.Videos
	return out
}

func buildCatalogEventProgrammes(
	guideChID, catType, catName string,
	items []stremio.MetaPreview,
	enriched map[eventMetaKey]stremio.Meta,
	now time.Time,
) []livetv.Programme {
	usedStart := map[int64]bool{}
	var out []livetv.Programme
	for _, preview := range items {
		if preview.ID == "" {
			continue
		}
		typ := firstNonEmpty(strings.TrimSpace(preview.Type), catType)
		detail, hasDetail := enriched[eventMetaKey{typ: typ, id: preview.ID}]
		meta := mergeEventMeta(preview, detail, hasDetail)
		progs := eventMetaProgrammes(guideChID, typ, catName, meta, now)
		for _, p := range progs {
			// Simultaneous events on one channel must have distinct start_ms
			// values to survive PRIMARY KEY(source_id, channel_id, start_ms)
			// while preserving catalog order.
			for usedStart[p.Start.UnixMilli()] {
				p.Start = p.Start.Add(time.Millisecond)
				p.End = p.End.Add(time.Millisecond)
			}
			usedStart[p.Start.UnixMilli()] = true
			p.ID = stableID(guideChID, p.Start.UTC().Format(time.RFC3339Nano))
			out = append(out, p)
		}
	}
	return out
}

func eventMetaProgrammes(guideChID, typ, catName string, meta stremio.Meta, now time.Time) []livetv.Programme {
	dur := defaultEventDuration
	if parsed, ok := parseEventDuration(meta.Runtime); ok {
		dur = parsed
	}
	title := strings.TrimSpace(firstNonEmpty(meta.Name, meta.ID))
	summary := strings.TrimSpace(meta.Description)
	if summary == "" {
		if info := strings.TrimSpace(meta.ReleaseInfo); info != "" && !strings.EqualFold(info, title) {
			summary = info
		}
	}
	thumb := strings.TrimSpace(firstNonEmpty(meta.Poster, meta.Logo))
	art := strings.TrimSpace(firstNonEmpty(meta.Background, meta.Poster, meta.Logo))
	genres := meta.Genres
	if len(genres) == 0 && catName != "" {
		genres = []string{catName}
	}

	// 1. Scheduled videos from /meta/ (especially when behaviorHints.hasScheduledVideos is set).
	if len(meta.Videos) > 0 {
		winFrom := now.Add(-12 * time.Hour)
		winUntil := now.Add(7 * 24 * time.Hour)
		var timed []stremio.Video
		for _, v := range meta.Videos {
			if !v.StartTime.IsZero() || !v.Released.IsZero() {
				timed = append(timed, v)
			}
		}
		if len(timed) > 0 && (meta.BehaviorHints.HasScheduledVideos || len(timed) > 1 || strings.TrimSpace(meta.Released) == "") {
			var progs []livetv.Programme
			for _, v := range timed {
				start := v.StartTime.UTC()
				if start.IsZero() {
					start = v.Released.UTC()
				}
				end := v.EndTime.UTC()
				if !end.After(start) {
					end = start.Add(dur)
				}
				if !end.After(winFrom) || !start.Before(winUntil) {
					continue
				}
				vTitle := strings.TrimSpace(firstNonEmpty(v.Title, v.Name))
				pTitle := title
				if vTitle != "" && !strings.EqualFold(vTitle, title) {
					if len(timed) > 1 || meta.BehaviorHints.HasScheduledVideos {
						pTitle = title + " · " + vTitle
					} else if pTitle == "" {
						pTitle = vTitle
					}
				}
				pSummary := firstNonEmpty(strings.TrimSpace(v.Overview), summary)
				pThumb := firstNonEmpty(strings.TrimSpace(v.Thumbnail), thumb)
				live := !now.Before(start) && now.Before(end)
				progs = append(progs, livetv.Programme{
					ChannelID:    guideChID,
					Start:        start,
					End:          end,
					EventStreams: eventStreamRefs(typ, v.ID, meta.BehaviorHints.DefaultVideoID, meta.ID),
					Item: livetv.Item{
						Title:      pTitle,
						Summary:    pSummary,
						Thumb:      pThumb,
						Art:        art,
						Genres:     genres,
						DurationMS: end.Sub(start).Milliseconds(),
						ReleasedMS: start.UnixMilli(),
						Live:       live,
					},
				})
			}
			if len(progs) > 0 {
				return progs
			}
		}
	}

	// 2. Determine timing from released, releaseInfo, or live-now fallback.
	start, end, live := resolveEventWindow(meta.Released, meta.ReleaseInfo, dur, now)
	return []livetv.Programme{{
		ChannelID:    guideChID,
		Start:        start,
		End:          end,
		EventStreams: eventStreamRefs(typ, "", meta.BehaviorHints.DefaultVideoID, meta.ID),
		Item: livetv.Item{
			Title:      title,
			Summary:    summary,
			Thumb:      thumb,
			Art:        art,
			Genres:     genres,
			DurationMS: end.Sub(start).Milliseconds(),
			ReleasedMS: start.UnixMilli(),
			Live:       live,
		},
	}}
}

func eventStreamRefs(typ string, ids ...string) []livetv.EventStreamRef {
	var out []livetv.EventStreamRef
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, livetv.EventStreamRef{Type: typ, ID: id})
	}
	return out
}

func liveAnchor(now time.Time) time.Time {
	return now.UTC().Truncate(eventLiveAnchorStep)
}

func resolveEventWindow(released, releaseInfo string, dur time.Duration, now time.Time) (start, end time.Time, live bool) {
	now = now.UTC()
	liveMarker := hasExplicitLiveMarker(releaseInfo)

	if ts, dateOnly, ok := parseEventTimestamp(released); ok {
		if !dateOnly {
			start = ts.UTC()
			end = start.Add(dur)
			if liveMarker && !end.After(now) {
				end = liveAnchor(now).Add(max(time.Hour, dur/2))
			}
			live = liveMarker || (!now.Before(start) && now.Before(end))
			return start, end, live
		}
		// Date-only released value: prefer time-of-day or live marker in releaseInfo.
		if s, e, l, ok := parseReleaseInfoTiming(releaseInfo, now, &ts, dur); ok {
			return s, e, l
		}
		ny, nm, nd := now.Date()
		ty, tm, td := ts.UTC().Date()
		if ny == ty && nm == tm && nd == td {
			start = liveAnchor(now)
			return start, start.Add(dur), true
		}
		start = ts.UTC()
		return start, start.Add(dur), false
	}

	if s, e, l, ok := parseReleaseInfoTiming(releaseInfo, now, nil, dur); ok {
		return s, e, l
	}

	// Sensible fallback: untimed items currently listed in a live event
	// catalog are treated as live now.
	start = liveAnchor(now)
	return start, start.Add(dur), true
}

var (
	reHoursMins   = regexp.MustCompile(`(?i)(\d+)\s*(?:h|hr|hrs|hour|hours)\s*(?:(\d+)\s*(?:m|min|mins|minute|minutes))?`)
	reMinutes     = regexp.MustCompile(`(?i)^(\d+)\s*(?:m|min|mins|minute|minutes)?$`)
	reClockDur    = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
	reRelative    = regexp.MustCompile(`(?i)\b(?:starts?\s+)?in\s+(?:(\d+)\s*(?:h|hr|hrs|hour|hours)\s*)?(?:(\d+)\s*(?:m|min|mins|minute|minutes))?\b`)
	reTimeRange   = regexp.MustCompile(`(?i)\b(\d{1,2}:\d{2}(?:\s*[ap]m)?)\s*[-–—]\s*(\d{1,2}:\d{2}(?:\s*[ap]m)?)(?:\s*(utc|gmt|z))?\b`)
	reTimeOfDay   = regexp.MustCompile(`(?i)\b(\d{1,2}:\d{2}(?:\s*[ap]m)?)(?:\s*(utc|gmt|z))?\b`)
	reLiveKeyword = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(live(?:\s+now)?|on\s+air|in\s+progress|now\s+playing)(?:$|[^a-z0-9])`)
)

func hasExplicitLiveMarker(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	return reLiveKeyword.MatchString(raw)
}

func parseEventDuration(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	var d time.Duration
	switch {
	case reClockDur.MatchString(raw):
		m := reClockDur.FindStringSubmatch(raw)
		h, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		secs, _ := strconv.Atoi(m[3])
		d = time.Duration(h)*time.Hour + time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second
	case reHoursMins.MatchString(raw):
		m := reHoursMins.FindStringSubmatch(raw)
		h, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		d = time.Duration(h)*time.Hour + time.Duration(mins)*time.Minute
	case reMinutes.MatchString(raw):
		m := reMinutes.FindStringSubmatch(raw)
		mins, _ := strconv.Atoi(m[1])
		d = time.Duration(mins) * time.Minute
	default:
		return 0, false
	}
	if d <= 0 {
		return 0, false
	}
	if d < minEventDuration {
		d = minEventDuration
	}
	if d > maxEventDuration {
		d = maxEventDuration
	}
	return d, true
}

func parseEventTimestamp(raw string) (time.Time, bool, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, false
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n >= 1_000_000_000_000 && n < 10_000_000_000_000 {
			return time.UnixMilli(n).UTC(), false, true
		}
		if n >= 1_000_000_000 && n < 10_000_000_000 {
			return time.Unix(n, 0).UTC(), false, true
		}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05Z",
		"2006-01-02 15:04Z",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04 MST",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"Jan 2, 2006 15:04 MST",
		"Jan 2, 2006 3:04 PM MST",
		"2 Jan 2006 15:04 MST",
		"Jan 2, 2006 15:04",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), false, true
		}
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), true, true
	}
	return time.Time{}, false, false
}

func parseReleaseInfoTiming(raw string, now time.Time, fallbackDate *time.Time, dur time.Duration) (start, end time.Time, live, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, time.Time{}, false, false
	}
	liveMarker := hasExplicitLiveMarker(raw)

	// Try full timestamp in whole string or separated segments ("Premier League · 2026-11-21 15:00 UTC").
	segments := []string{raw}
	for _, sep := range []string{"·", "|", "•"} {
		if strings.Contains(raw, sep) {
			for _, part := range strings.Split(raw, sep) {
				if p := strings.TrimSpace(part); p != "" {
					segments = append(segments, p)
				}
			}
		}
	}
	for _, seg := range segments {
		if ts, dateOnly, parsed := parseEventTimestamp(seg); parsed && !dateOnly {
			start = ts.UTC()
			end = start.Add(dur)
			if liveMarker && !end.After(now) {
				end = liveAnchor(now).Add(max(time.Hour, dur/2))
			}
			return start, end, liveMarker || (!now.Before(start) && now.Before(end)), true
		}
	}

	// Try relative start ("in 30m", "starts in 1h 15m").
	if m := reRelative.FindStringSubmatch(raw); len(m) == 3 && (m[1] != "" || m[2] != "") {
		h, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		if offset := time.Duration(h)*time.Hour + time.Duration(mins)*time.Minute; offset > 0 {
			start = now.UTC().Truncate(time.Minute).Add(offset)
			return start, start.Add(dur), false, true
		}
	}

	baseDate := now.UTC()
	if fallbackDate != nil {
		baseDate = fallbackDate.UTC()
	}

	// Try time range ("15:00 - 17:00 UTC", "3:00 PM - 5:00 PM UTC").
	if m := reTimeRange.FindStringSubmatch(raw); len(m) >= 3 {
		s, ok1 := parseClockOnDate(m[1], baseDate)
		e, ok2 := parseClockOnDate(m[2], baseDate)
		if ok1 && ok2 {
			if !e.After(s) {
				e = e.Add(24 * time.Hour)
			}
			if fallbackDate == nil && !liveMarker && e.Before(now) && s.Add(24*time.Hour).Sub(now) <= 18*time.Hour {
				s = s.Add(24 * time.Hour)
				e = e.Add(24 * time.Hour)
			}
			if liveMarker && !e.After(now) {
				e = liveAnchor(now).Add(max(time.Hour, dur/2))
			}
			return s, e, liveMarker || (!now.Before(s) && now.Before(e)), true
		}
	}

	// Try single time-of-day ("15:00 UTC", "8:30 PM").
	if m := reTimeOfDay.FindStringSubmatch(raw); len(m) >= 2 {
		if s, parsed := parseClockOnDate(m[1], baseDate); parsed {
			if fallbackDate == nil && !liveMarker && s.Add(dur).Before(now) && s.Add(24*time.Hour).Sub(now) <= 18*time.Hour {
				s = s.Add(24 * time.Hour)
			}
			e := s.Add(dur)
			if liveMarker && !e.After(now) {
				e = liveAnchor(now).Add(max(time.Hour, dur/2))
			}
			return s, e, liveMarker || (!now.Before(s) && now.Before(e)), true
		}
	}

	if liveMarker {
		start = liveAnchor(now)
		return start, start.Add(dur), true, true
	}
	return time.Time{}, time.Time{}, false, false
}

func parseClockOnDate(raw string, baseDate time.Time) (time.Time, bool) {
	clean := strings.ToUpper(strings.TrimSpace(raw))
	clean = strings.Join(strings.Fields(clean), " ")
	for _, layout := range []string{"15:04", "3:04 PM", "3:04PM"} {
		if t, err := time.Parse(layout, clean); err == nil {
			y, mo, d := baseDate.UTC().Date()
			return time.Date(y, mo, d, t.Hour(), t.Minute(), 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}

// expandPluginStream resolves an event-based channel's lazy plugin://.../event/
// input at tune time into concrete ordered HTTP(S) StreamSources: the currently
// live event's streams in add-on order, followed by the next live event's
// streams, so Ether's normal tune/failover pipeline tries them in order.
func (b *Bridge) expandPluginStream(
	ctx context.Context,
	s *livetv.Service,
	c livetv.Channel,
	src livetv.StreamSource,
) ([]livetv.StreamSource, map[string]livetv.Programme, *livetv.Programme, bool, error) {
	pluginID, catType, catID, ok := parseEventChannelStreamURL(src.URL)
	if !ok {
		return nil, nil, nil, false, nil
	}
	if b.mgr == nil || s == nil {
		return nil, nil, nil, true, core.ErrNotAvailable
	}
	rec, err := b.mgr.Get(ctx, pluginID)
	if err != nil {
		return nil, nil, nil, true, fmt.Errorf("event add-on unavailable: %w", core.ErrNotAvailable)
	}
	addon := b.mgr.Addon(rec)
	openCtx, cancel := context.WithTimeout(ctx, eventOpenDeadline)
	defer cancel()

	now := s.Now()
	liveProgs, err := s.LiveProgrammes(openCtx, c, now)
	if err != nil {
		return nil, nil, nil, true, err
	}

	var sources []livetv.StreamSource
	var byInput map[string]livetv.Programme
	var primary *livetv.Programme
	if len(liveProgs) > 0 {
		sources, byInput, primary = resolveEventProgrammesStreams(openCtx, addon, c.ID, liveProgs)
	}

	// If the local guide had no live event (e.g., source was idle while no one
	// watched and a match just started) or cached live events returned no
	// playable streams, check this single catalog live once.
	if len(sources) == 0 {
		if freshProgs := fetchLiveCatalogEvents(openCtx, addon, c.GuideID, catType, catID, c.Name, now); len(freshProgs) > 0 {
			// Skip events we already tried above.
			tried := map[string]bool{}
			for _, p := range liveProgs {
				for _, ref := range p.EventStreams {
					tried[ref.Type+"\x00"+ref.ID] = true
				}
			}
			var unseen []livetv.Programme
			for _, p := range freshProgs {
				already := len(p.EventStreams) > 0
				for _, ref := range p.EventStreams {
					if !tried[ref.Type+"\x00"+ref.ID] {
						already = false
						break
					}
				}
				if !already {
					unseen = append(unseen, p)
				}
			}
			if len(unseen) > 0 {
				sources, byInput, primary = resolveEventProgrammesStreams(openCtx, addon, c.ID, unseen)
				if len(liveProgs) == 0 {
					liveProgs = unseen
				}
			}
		}
	}

	if len(liveProgs) == 0 {
		return nil, nil, nil, true, fmt.Errorf("no live event is currently airing on %s: %w", c.Name, core.ErrNotAvailable)
	}
	if len(sources) == 0 {
		return nil, nil, nil, true, fmt.Errorf("no live event on %s has a playable HTTP stream: %w", c.Name, core.ErrNotAvailable)
	}
	return sources, byInput, primary, true, nil
}

func fetchLiveCatalogEvents(ctx context.Context, addon stremio.Addon, guideChID, catType, catID, catName string, now time.Time) []livetv.Programme {
	cat, ok := addon.Manifest.CatalogNamed(catType, catID)
	if !ok {
		cat = stremio.Catalog{Type: catType, ID: catID, Name: catName}
	}
	metas, err := addon.Catalog(ctx, cat, nil, maxEventCatalogItems)
	if err != nil || len(metas) == 0 {
		return nil
	}
	enriched := enrichEventMetas(ctx, addon, []stremio.Catalog{cat}, [][]stremio.MetaPreview{metas}, maxTuneLiveEvents)
	all := buildCatalogEventProgrammes(guideChID, catType, catName, metas, enriched, now)
	var live []livetv.Programme
	for _, p := range all {
		if p.End.After(now) && !p.Start.After(now.Add(999*time.Millisecond)) {
			live = append(live, p)
		}
	}
	return live
}

func resolveEventProgrammesStreams(
	ctx context.Context,
	addon stremio.Addon,
	chID string,
	liveProgs []livetv.Programme,
) ([]livetv.StreamSource, map[string]livetv.Programme, *livetv.Programme) {
	if len(liveProgs) > maxTuneLiveEvents {
		liveProgs = liveProgs[:maxTuneLiveEvents]
	}
	perEvent := make([][]stremio.Stream, len(liveProgs))
	forEachBounded(ctx, len(liveProgs), 4, func(i int) {
		for _, ref := range liveProgs[i].EventStreams {
			if ctx.Err() != nil {
				return
			}
			if !addon.Manifest.TakesID(stremio.ResourceStream, ref.Type, ref.ID) {
				continue
			}
			sts, err := addon.StreamsFor(ctx, ref.Type, ref.ID)
			if err != nil {
				continue
			}
			var playable []stremio.Stream
			for _, st := range sts {
				if httpURL(strings.TrimSpace(st.URL)) {
					playable = append(playable, st)
				}
			}
			if len(playable) > 0 {
				perEvent[i] = playable
				return
			}
		}
	})

	eventsWithStreams := 0
	for _, sts := range perEvent {
		if len(sts) > 0 {
			eventsWithStreams++
		}
	}
	if eventsWithStreams == 0 {
		return nil, nil, nil
	}

	// Preserve strict order: all streams of live event 0 in order, then live
	// event 1, etc. When multiple live events have streams, bound each event's
	// share so a single event with many links cannot crowd out the next live
	// event within MaxChannelInputs.
	perEventCap := livetv.MaxChannelInputs
	if eventsWithStreams > 1 {
		perEventCap = max(4, livetv.MaxChannelInputs/eventsWithStreams)
	}

	var out []livetv.StreamSource
	byInput := make(map[string]livetv.Programme)
	seenURL := map[string]bool{}
	var primary *livetv.Programme

	for i, sts := range perEvent {
		if len(sts) == 0 {
			continue
		}
		if primary == nil {
			p := liveProgs[i]
			primary = &p
		}
		addedForEvent := 0
		for _, st := range sts {
			if len(out) >= livetv.MaxChannelInputs || addedForEvent >= perEventCap {
				break
			}
			u := strings.TrimSpace(st.URL)
			if !httpURL(u) || seenURL[u] {
				continue
			}
			seenURL[u] = true
			id := livetv.AddonInputPrefix + stableID(chID, liveProgs[i].ID, u)
			name := eventStreamLabel(liveProgs[i].Item.Title, inputName(st))
			out = append(out, livetv.StreamSource{
				ID:       id,
				Name:     name,
				URL:      u,
				Enabled:  true,
				Priority: len(out),
				Headers:  livetv.CleanInputHeaders(st.BehaviorHints.ProxyHeaders.Request),
			})
			byInput[id] = liveProgs[i]
			addedForEvent++
		}
	}
	return out, byInput, primary
}

func eventStreamLabel(eventTitle, streamName string) string {
	eventTitle = strings.TrimSpace(eventTitle)
	streamName = strings.TrimSpace(streamName)
	label := streamName
	switch {
	case eventTitle != "" && streamName != "":
		label = eventTitle + " · " + streamName
	case eventTitle != "":
		label = eventTitle
	}
	for len(label) > addonInputNameMax {
		_, size := utf8.DecodeLastRuneInString(label)
		label = label[:len(label)-size]
	}
	return label
}

func headersFromMap(m map[string]string) http.Header {
	if len(m) == 0 {
		return nil
	}
	h := make(http.Header, len(m))
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}

var errNoLiveAddOnPlayable = errors.New("the add-on lists no live TV channels or event catalogs")
