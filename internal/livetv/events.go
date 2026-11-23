package livetv

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

func (s *Service) publishEvent(eventType string, payload any, opts ...events.EnvelopeOption) {
	if s == nil || s.o.Events == nil {
		return
	}
	_ = s.o.Events.PublishTyped(eventType, payload, opts...)
}

func (s *Service) cachedChannelInfo(channelID string) (name string, hasBackup bool) {
	if s == nil || channelID == "" {
		return "", false
	}
	s.chanSnap.mu.Lock()
	idx, ok := s.chanSnap.byID[channelID]
	var c Channel
	if ok && idx >= 0 && idx < len(s.chanSnap.list) {
		c = s.chanSnap.list[idx]
	}
	s.chanSnap.mu.Unlock()
	if !ok {
		return "", false
	}
	enabled := 0
	for _, src := range c.StreamSources {
		if src.Enabled && src.URL != "" {
			enabled++
		}
	}
	if enabled == 0 && c.StreamURL != "" {
		enabled = 1
	}
	return c.Name, enabled > 1
}

func (s *Service) cachedSourceName(sourceID string) string {
	if s == nil || sourceID == "" {
		return ""
	}
	s.authCache.mu.Lock()
	e, ok := s.authCache.m[sourceID]
	s.authCache.mu.Unlock()
	if ok && e.pool != nil {
		return e.pool.Name
	}
	return ""
}

func clientDisplayName(caps core.ClientCaps) string {
	if p := strings.TrimSpace(caps.Product); p != "" {
		return p
	}
	return strings.TrimSpace(caps.ClientID)
}

func sessionPlayoutModeLocked(ss *session) string {
	if ss == nil {
		return ""
	}
	if ss.broadcast != nil {
		return "broadcast"
	}
	if ss.owned != nil {
		if ss.owned.relay != nil {
			return "relay"
		}
		return "passthrough"
	}
	if ss.info.Channel.Kind == "virtual" {
		return "virtual"
	}
	return "direct"
}

func (s *Service) emitChannelTuned(info SessionInfo, inputSource StreamSource, user core.User, caps core.ClientCaps, mode string) {
	if s == nil || s.o.Events == nil {
		return
	}
	c := info.Channel
	srcID := info.ActiveSourceID
	if srcID == "" {
		srcID = inputSource.ID
	}
	if srcID == "" {
		srcID = c.SourceID
	}
	providerID := inputSource.AuthSourceID
	if providerID == "" {
		providerID = c.SourceID
	}
	p := events.LiveTVChannelTunedPayload{
		ChannelID:     c.ID,
		ChannelName:   c.Name,
		ChannelNumber: c.Number,
		SourceID:      srcID,
		ProviderID:    providerID,
		Mode:          mode,
		ViewerID:      info.UserID,
		Username:      user.Username,
		Client:        clientDisplayName(caps),
	}
	s.publishEvent(events.EventLiveTVChannelTuned, p, events.WithCorrelation(events.Correlation{
		SessionID: info.ID,
		ViewerID:  info.UserID,
		ChannelID: c.ID,
		SourceID:  srcID,
	}))
}

func (s *Service) emitChannelStopped(info SessionInfo, inputSource StreamSource, watchedMS, bytesServed int64, mode string) {
	if s == nil || s.o.Events == nil {
		return
	}
	if watchedMS < 0 {
		watchedMS = 0
	}
	c := info.Channel
	srcID := info.ActiveSourceID
	if srcID == "" {
		srcID = inputSource.ID
	}
	if srcID == "" {
		srcID = c.SourceID
	}
	p := events.LiveTVChannelStoppedPayload{
		ChannelID:   c.ID,
		ChannelName: c.Name,
		SourceID:    srcID,
		ViewerID:    info.UserID,
		WatchedMS:   watchedMS,
		BytesServed: bytesServed,
		Mode:        mode,
	}
	s.publishEvent(events.EventLiveTVChannelStopped, p, events.WithCorrelation(events.Correlation{
		SessionID: info.ID,
		ViewerID:  info.UserID,
		ChannelID: c.ID,
		SourceID:  srcID,
	}))
}

func (s *Service) emitInputHealthEvent(g *inputGrant, ev UpstreamEvent, stalledAt time.Time) {
	if s == nil || s.o.Events == nil || g == nil {
		return
	}
	channelID := g.histChannel
	sourceID := g.histSrc.ID
	if sourceID == "" {
		sourceID = g.source
	}
	channelName, hasBackup := s.cachedChannelInfo(channelID)
	transport := ev.Health.Kind

	corr := events.WithCorrelation(events.Correlation{ChannelID: channelID, SourceID: sourceID})
	switch ev.Kind {
	case InputStalled:
		s.publishEvent(events.EventLiveTVInputStalled, events.LiveTVInputStalledPayload{
			ChannelID:      channelID,
			ChannelName:    channelName,
			SourceID:       sourceID,
			Transport:      transport,
			DataAgeMS:      ev.Health.DataAge.Milliseconds(),
			ReserveDepthMS: ev.Health.ReserveDepth.Milliseconds(),
			StallAfterMS:   ev.Health.StallAfter.Milliseconds(),
			Reconnects:     ev.Health.Reconnects,
		}, corr)
	case InputRecovered:
		var downtimeMS int64
		if !stalledAt.IsZero() && !ev.At.IsZero() && ev.At.After(stalledAt) {
			downtimeMS = ev.At.Sub(stalledAt).Milliseconds()
		}
		s.publishEvent(events.EventLiveTVInputRecovered, events.LiveTVInputRecoveredPayload{
			ChannelID:   channelID,
			ChannelName: channelName,
			SourceID:    sourceID,
			Transport:   transport,
			DowntimeMS:  downtimeMS,
			Reconnects:  ev.Health.Reconnects,
		}, corr)
	case InputFailed:
		errMsg := "upstream input lost"
		if ev.Err != nil {
			errMsg = ev.Err.Error()
		}
		s.publishEvent(events.EventLiveTVInputLost, events.LiveTVInputLostPayload{
			ChannelID:   channelID,
			ChannelName: channelName,
			SourceID:    sourceID,
			Transport:   transport,
			Reconnects:  ev.Health.Reconnects,
			Error:       errMsg,
			HasBackup:   hasBackup,
		}, corr)
	}
}

func (s *Service) emitSourceFailover(c Channel, fromID, toID, reason, mode string) {
	if s == nil || s.o.Events == nil || fromID == toID {
		return
	}
	s.publishEvent(events.EventLiveTVSourceFailover, events.LiveTVSourceFailoverPayload{
		ChannelID:    c.ID,
		ChannelName:  c.Name,
		FromSourceID: fromID,
		ToSourceID:   toID,
		Reason:       reason,
		Mode:         mode,
	}, events.WithCorrelation(events.Correlation{ChannelID: c.ID, SourceID: toID}))
}

const degradedStallsPerHour = 2.0

func isSourceDegraded(h SourceHistory) bool {
	return (h.DamageEvents > 0 && h.DamagePerHour() >= historyDamagePerHour) ||
		(h.Stalls > 0 && h.StallsPerHour() >= degradedStallsPerHour) ||
		h.MaxKeyframeGap() >= historyKeyframeGap
}

func (s *Service) emitSourceDegraded(h SourceHistory) {
	if s == nil || s.o.Events == nil {
		return
	}
	channelName, _ := s.cachedChannelInfo(h.ChannelID)
	s.publishEvent(events.EventLiveTVSourceDegraded, events.LiveTVSourceDegradedPayload{
		ChannelID:        h.ChannelID,
		ChannelName:      channelName,
		SourceID:         h.SourceID,
		DamagePerHour:    h.DamagePerHour(),
		StallsPerHour:    h.StallsPerHour(),
		MaxKeyframeGapMS: h.MaxKeyframeGap().Milliseconds(),
		MediaSec:         h.MediaSeconds,
	}, events.WithCorrelation(events.Correlation{ChannelID: h.ChannelID, SourceID: h.SourceID}))
}

func (s *Service) emitSourceCooldown(channelID, sourceID string, failures int, errorClass string, retryAfter time.Time) {
	if s == nil || s.o.Events == nil {
		return
	}
	var ra *time.Time
	if !retryAfter.IsZero() {
		t := retryAfter.UTC()
		ra = &t
	}
	s.publishEvent(events.EventLiveTVSourceCooldown, events.LiveTVSourceCooldownPayload{
		ChannelID:  channelID,
		SourceID:   sourceID,
		Failures:   failures,
		ErrorClass: errorClass,
		RetryAfter: ra,
	}, events.WithCorrelation(events.Correlation{ChannelID: channelID, SourceID: sourceID}))
}

func (s *Service) emitProviderAuthFailed(providerID, providerName, accountID string, statusCode int, err error) {
	if s == nil || s.o.Events == nil {
		return
	}
	if providerName == "" {
		providerName = s.cachedSourceName(providerID)
	}
	errMsg := "provider rejected credentials"
	if err != nil {
		errMsg = err.Error()
	}
	s.publishEvent(events.EventLiveTVProviderAuthFailed, events.LiveTVProviderAuthFailedPayload{
		ProviderID:   providerID,
		ProviderName: providerName,
		AccountID:    accountID,
		StatusCode:   statusCode,
		Error:        errMsg,
	}, events.WithCorrelation(events.Correlation{SourceID: providerID}))
}

func (s *Service) emitProviderLimitReached(providerID string, active, maxConns int, channelID string) {
	if s == nil || s.o.Events == nil || providerID == "" {
		return
	}
	s.publishEvent(events.EventLiveTVProviderLimitReached, events.LiveTVProviderLimitReachedPayload{
		ProviderID:        providerID,
		ProviderName:      s.cachedSourceName(providerID),
		ActiveConnections: active,
		MaxConnections:    maxConns,
		ChannelID:         channelID,
	}, events.WithCorrelation(events.Correlation{ChannelID: channelID, SourceID: providerID}))
}

func (s *Service) emitGuideRefreshed(src Source, dur time.Duration) {
	if s == nil || s.o.Events == nil {
		return
	}
	ms := dur.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	s.publishEvent(events.EventLiveTVGuideRefreshed, events.LiveTVGuideRefreshedPayload{
		SourceID:   src.ID,
		SourceName: src.Name,
		Channels:   src.ChannelCount,
		Programs:   src.ProgrammeCount,
		DurationMS: ms,
	}, events.WithCorrelation(events.Correlation{SourceID: src.ID}))
}

func (s *Service) emitGuideFailed(src Source, err error) {
	if s == nil || s.o.Events == nil || err == nil || errors.Is(err, context.Canceled) {
		return
	}
	var staleHours float64
	if !src.UpdatedAt.IsZero() {
		if d := s.o.Now().Sub(src.UpdatedAt); d > 0 {
			staleHours = d.Hours()
		}
	}
	s.publishEvent(events.EventLiveTVGuideFailed, events.LiveTVGuideFailedPayload{
		SourceID:   src.ID,
		SourceName: src.Name,
		Error:      err.Error(),
		StaleHours: staleHours,
	}, events.WithCorrelation(events.Correlation{SourceID: src.ID}))
}

// providerAuthError preserves the exact auth-refused error string while
// carrying the HTTP status code so callers can emit livetv.provider.auth_failed.
type providerAuthError struct {
	StatusCode int
}

func (e *providerAuthError) Error() string {
	return "source refused the credentials (HTTP " + strconv.Itoa(e.StatusCode) + ")"
}
