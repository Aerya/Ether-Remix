package playback

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/logredact"
)

const maxOwnerErrorRunes = 512

// TimelineEvent is one structured playback lifecycle milestone emitted for
// durable per-play timeline storage.
type TimelineEvent struct {
	SessionID string
	At        time.Time
	Kind      string
	Level     string
	Message   string
	Detail    string
	Failing   bool
}

// OwnerError formats err as an owner-facing failure cause for stats, console
// drawers, and notifications, scrubbed through logredact.String. Viewer-facing
// protocol responses continue to classify errors via sentinel wrapping.
func OwnerError(err error) string {
	if err == nil {
		return ""
	}
	s := strings.TrimSpace(err.Error())
	s = stripInternalErrorPrefixes(s)
	if formatted, ok := formatReleasesFailedError(s); ok {
		s = formatted
	} else {
		s = stripRedundantTrailingSentinel(s)
	}
	s = strings.TrimSpace(logredact.String(s))
	if s == "" {
		return "playback failed"
	}
	r := []rune(s)
	if len(r) > maxOwnerErrorRunes {
		return string(r[:maxOwnerErrorRunes-1]) + "…"
	}
	return s
}

func stripInternalErrorPrefixes(s string) string {
	prefixes := []string{
		"playback: failover: ",
		"playback: ",
		core.ErrNotAvailable.Error() + ": ",
		"media not available: ",
	}
	for {
		trimmed := s
		for _, prefix := range prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			}
		}
		if trimmed == s {
			return s
		}
		s = trimmed
	}
}

var redundantSentinels = []string{
	": " + core.ErrSourceUnavailable.Error(),
	": " + core.ErrDebridAuth.Error(),
	": " + context.DeadlineExceeded.Error(),
	": " + core.ErrNotAvailable.Error(),
}

func stripRedundantTrailingSentinel(s string) string {
	if !strings.Contains(s, "(last failure: ") && !strings.Contains(s, "(last: ") {
		return s
	}
	for _, suf := range redundantSentinels {
		if strings.HasSuffix(s, suf) {
			return strings.TrimSpace(strings.TrimSuffix(s, suf))
		}
	}
	return s
}

func formatReleasesFailedError(s string) (string, bool) {
	i := 0
	for i < len(s) && unicode.IsDigit(rune(s[i])) {
		i++
	}
	if i == 0 {
		return "", false
	}
	const marker = " releases failed"
	if !strings.HasPrefix(s[i:], marker) {
		return "", false
	}
	count := s[:i]
	rest := strings.TrimSpace(s[i+len(marker):])
	var detail string
	switch {
	case strings.HasPrefix(rest, "(last: "):
		rest = strings.TrimPrefix(rest, "(last: ")
		for _, suf := range redundantSentinels {
			if strings.HasSuffix(rest, ")"+suf) {
				rest = strings.TrimSuffix(rest, ")"+suf)
				detail = rest
				break
			}
		}
		if detail == "" {
			detail = strings.TrimSuffix(rest, ")")
		}
	case strings.HasPrefix(rest, ": "):
		detail = strings.TrimPrefix(rest, ": ")
	default:
		detail = rest
	}
	detail = stripInternalErrorPrefixes(strings.TrimSpace(detail))
	if detail == "" {
		return fmt.Sprintf("every release failed (%s tried)", count), true
	}
	return fmt.Sprintf("every release failed (%s tried): %s", count, detail), true
}

func (m *Manager) recordTimeline(s *session, ev TimelineEvent) {
	if m == nil || m.deps.Timeline == nil || s == nil || !s.timelineEnabled || s.id == "" {
		return
	}
	ev.SessionID = s.id
	if ev.At.IsZero() {
		ev.At = m.now()
	}
	m.deps.Timeline(ev)
}

func (m *Manager) recordTimelineByID(sessionID string, ev TimelineEvent) {
	if m == nil || m.deps.Timeline == nil || sessionID == "" {
		return
	}
	ev.SessionID = sessionID
	if ev.At.IsZero() {
		ev.At = m.now()
	}
	m.deps.Timeline(ev)
}

func timelineStartDetail(caps core.ClientCaps, prefer string) string {
	var parts []string
	dev := strings.TrimSpace(caps.DeviceName)
	if dev == "" {
		dev = strings.TrimSpace(caps.Device)
	}
	prod := strings.TrimSpace(caps.Product)
	plat := strings.TrimSpace(caps.Platform)
	switch {
	case prod != "" && dev != "" && !strings.EqualFold(prod, dev):
		parts = append(parts, prod+" on "+dev)
	case prod != "":
		parts = append(parts, prod)
	case dev != "":
		parts = append(parts, dev)
	}
	if plat != "" && !strings.EqualFold(plat, dev) && !strings.EqualFold(plat, prod) {
		parts = append(parts, plat)
	}
	if caps.Protocol != "" {
		parts = append(parts, string(caps.Protocol))
	}
	if caps.MaxBitrateKbps > 0 && !caps.DefaultRemoteBitrate {
		parts = append(parts, fmt.Sprintf("cap %.1f Mbps", float64(caps.MaxBitrateKbps)/1000))
	}
	if prefer != "" {
		parts = append(parts, "picked version")
	}
	return strings.Join(parts, " · ")
}

func timelineCandidateDetail(r core.Release, alternates int) string {
	var parts []string
	if r.Indexer != "" {
		parts = append(parts, r.Indexer)
	}
	if r.Protocol != "" {
		parts = append(parts, r.Protocol)
	}
	if r.Size > 0 {
		parts = append(parts, formatTimelineBytes(r.Size))
	}
	if alternates > 0 {
		parts = append(parts, fmt.Sprintf("%d alternates", alternates))
	}
	return strings.Join(parts, " · ")
}

func timelineOpenedDetail(info core.MediaInfo, r core.Release, size int64) string {
	var parts []string
	if v := info.Video(); v != nil {
		res := ""
		if v.Height > 0 {
			res = fmt.Sprintf("%dp", v.Height)
		}
		codec := strings.ToUpper(strings.TrimSpace(v.Codec))
		switch {
		case res != "" && codec != "":
			parts = append(parts, res+" "+codec)
		case res != "":
			parts = append(parts, res)
		case codec != "":
			parts = append(parts, codec)
		}
	}
	if info.Container != "" {
		parts = append(parts, info.Container)
	}
	if size <= 0 {
		size = r.Size
	}
	if size > 0 {
		parts = append(parts, formatTimelineBytes(size))
	}
	if r.Indexer != "" {
		parts = append(parts, r.Indexer)
	}
	return strings.Join(parts, " · ")
}

func timelineDecisionSummary(d core.Decision) string {
	switch d.Kind {
	case core.DecisionDirectPlay:
		return "Delivery: Direct play"
	case core.DecisionRemux:
		if !d.AudioCopy {
			return "Delivery: Direct stream (audio transcode)"
		}
		return "Delivery: Direct stream (remux)"
	case core.DecisionTranscode:
		var q []string
		if d.MaxHeight > 0 {
			q = append(q, fmt.Sprintf("%dp", d.MaxHeight))
		}
		if d.BitrateKbps > 0 {
			q = append(q, fmt.Sprintf("%.1f Mbps", float64(d.BitrateKbps)/1000))
		}
		if len(q) > 0 {
			return "Delivery: Transcode (" + strings.Join(q, " · ") + ")"
		}
		return "Delivery: Transcode"
	default:
		return "Delivery: " + string(d.Kind)
	}
}

func timelineDecisionWhy(d core.Decision) string {
	reason := strings.TrimSpace(d.Reason)
	if reason != "" {
		return reason
	}
	switch d.Kind {
	case core.DecisionDirectPlay:
		return "Compatible container and streams"
	case core.DecisionRemux:
		if !d.AudioCopy {
			return "Video stream copied; audio transcoded for client compatibility"
		}
		return "Streams copied into a compatible container"
	case core.DecisionTranscode:
		return "Video encoded to fit client capabilities or quality cap"
	default:
		return ""
	}
}

func (m *Manager) noteDecisionTimeline(s *session, d core.Decision) {
	if s == nil || !s.timelineEnabled || d.Kind == "" {
		return
	}
	s.mu.Lock()
	prevDecided := s.timelineDecided
	prevKind := s.timelineDecision
	prevReason := s.timelineReason
	prevBitrate := s.timelineBitrate
	prevHeight := s.timelineHeight

	s.timelineDecided = true
	s.timelineDecision = d.Kind
	s.timelineReason = d.Reason
	if d.BitrateKbps > 0 {
		s.timelineBitrate = d.BitrateKbps
	}
	if d.MaxHeight > 0 {
		s.timelineHeight = d.MaxHeight
	}
	s.mu.Unlock()

	if !prevDecided {
		m.recordTimeline(s, TimelineEvent{
			Kind:    "decision",
			Level:   "info",
			Message: timelineDecisionSummary(d),
			Detail:  timelineDecisionWhy(d),
		})
		m.emitStreamDecision(s, "", d)
		return
	}
	bitrateChanged := prevBitrate > 0 && d.BitrateKbps > 0 && prevBitrate != d.BitrateKbps
	heightChanged := prevHeight > 0 && d.MaxHeight > 0 && prevHeight != d.MaxHeight
	if bitrateChanged || heightChanged {
		var parts []string
		if d.MaxHeight > 0 {
			parts = append(parts, fmt.Sprintf("%dp", d.MaxHeight))
		}
		if d.BitrateKbps > 0 {
			parts = append(parts, fmt.Sprintf("%.1f Mbps", float64(d.BitrateKbps)/1000))
		}
		msg := "Quality switched"
		if len(parts) > 0 {
			msg = "Quality switched to " + strings.Join(parts, " · ")
		}
		m.recordTimeline(s, TimelineEvent{
			Kind:    "quality",
			Level:   "info",
			Message: msg,
			Detail:  timelineDecisionWhy(d),
		})
		m.emitStreamQualityChanged(s, prevHeight, d.MaxHeight, prevBitrate, d.BitrateKbps, d.Reason)
		if prevKind != d.Kind || prevReason != d.Reason {
			m.emitStreamDecision(s, prevKind, d)
		}
		return
	}
	if prevKind != d.Kind || prevReason != d.Reason {
		m.recordTimeline(s, TimelineEvent{
			Kind:    "decision",
			Level:   "info",
			Message: timelineDecisionSummary(d),
			Detail:  timelineDecisionWhy(d),
		})
		m.emitStreamDecision(s, prevKind, d)
	}
}

func (m *Manager) noteTimelineStall(s *session, now time.Time) {
	if s == nil || !s.timelineEnabled || s.caps.BroadcastBitrateKbps > 0 {
		return
	}
	s.mu.Lock()
	stalls := s.exp.stallCount(now)
	if s.caps.NoPlaybackTelemetry || stalls <= s.timelineStalls {
		s.mu.Unlock()
		return
	}
	s.timelineStalls = stalls
	pos := s.viewerPosition
	closed := s.closed
	viewerState := s.viewerState
	s.mu.Unlock()
	if closed || viewerState == "paused" || viewerState == "stopped" {
		return
	}
	detail := ""
	if pos > 0 {
		detail = "at " + pos.Round(time.Second).String()
	}
	m.recordTimeline(s, TimelineEvent{
		At:      now,
		Kind:    "stall",
		Level:   "warn",
		Failing: true,
		Message: fmt.Sprintf("Playback stalled (rebuffer #%d)", stalls),
		Detail:  detail,
	})
	m.emitStreamStalled(s, stalls, pos)
}

func timelineStopEvent(s *session, now time.Time) (TimelineEvent, bool) {
	if s == nil || !s.timelineEnabled {
		return TimelineEvent{}, false
	}
	bytes := s.bytes.Load()
	s.mu.Lock()
	preparedOnly := s.preparedOnly
	failure := s.exp.failure
	hasViewerPos := !s.viewerAt.IsZero()
	viewerPos := s.viewerPosition
	watched := s.viewerState == "playing" || s.exp.reported
	s.mu.Unlock()
	if preparedOnly && bytes == 0 && failure == "" && !watched {
		return TimelineEvent{}, false
	}
	var details []string
	if hasViewerPos && viewerPos > 0 {
		details = append(details, "position "+viewerPos.Round(time.Second).String())
	}
	if bytes > 0 {
		details = append(details, formatTimelineBytes(bytes)+" served")
	}
	msg := "Playback stopped"
	if failure != "" && bytes == 0 {
		msg = "Session closed after failed start"
	}
	return TimelineEvent{
		At:      now,
		Kind:    "stop",
		Level:   "info",
		Message: msg,
		Detail:  strings.Join(details, " · "),
	}, true
}

func formatTimelineBytes(n int64) string {
	const (
		mib = 1 << 20
		gib = 1 << 30
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
