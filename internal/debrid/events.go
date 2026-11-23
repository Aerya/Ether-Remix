package debrid

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

const (
	debridSaturatedCooldown = 30 * time.Second
	premiumWarnWindowDays   = 7
)

func (s *Service) publishEvent(eventType string, payload any, opts ...events.EnvelopeOption) {
	if s == nil || s.opts.Events == nil {
		return
	}
	_ = s.opts.Events.PublishTyped(eventType, payload, opts...)
}

func (s *Service) wireLimiterEvents(e *entry) {
	if s == nil || s.opts.Events == nil || e == nil || e.sem == nil {
		return
	}
	provID := e.cfg.ID
	provName := displayName(e.cfg)
	corr := events.WithCorrelation(events.Correlation{SourceID: provID})
	var lastSatNs atomic.Int64
	e.sem.onSaturated = func(active, maxConns, waiting int) {
		nowNs := s.now().UnixNano()
		prev := lastSatNs.Load()
		if prev > 0 && time.Duration(nowNs-prev) < debridSaturatedCooldown {
			return
		}
		if !lastSatNs.CompareAndSwap(prev, nowNs) {
			return
		}
		s.publishEvent(events.EventProviderConnectionsSaturated, events.ProviderConnectionsSaturatedPayload{
			ProviderID:        provID,
			ProviderName:      provName,
			ActiveConnections: active,
			MaxConnections:    maxConns,
			WaitingReaders:    waiting,
		}, corr)
	}
}

func (s *Service) emitBreakerEvent(e *entry, prevState string, ev *backoffEvent, outageDur time.Duration) {
	if s == nil || s.opts.Events == nil || e == nil || ev == nil {
		return
	}
	provID := e.cfg.ID
	provName := displayName(e.cfg)
	corr := events.WithCorrelation(events.Correlation{SourceID: provID})
	now := s.now()
	switch ev.State {
	case "backoff":
		if prevState != "backoff" {
			backoffSec := max(1, int(time.UnixMilli(ev.Until).Sub(now).Round(time.Second)/time.Second))
			s.publishEvent(events.EventProviderServiceOutage, events.ProviderServiceOutagePayload{
				ProviderID:   provID,
				ProviderName: provName,
				Kind:         e.cfg.Kind,
				Error:        ev.Error,
				BackoffSec:   backoffSec,
			}, corr)
		}
	case "auth_failed":
		if prevState != "auth_failed" {
			s.publishEvent(events.EventProviderAuthRejected, events.ProviderAuthRejectedPayload{
				ProviderID:   provID,
				ProviderName: provName,
				Kind:         e.cfg.Kind,
				StatusCode:   401,
				Error:        ev.Error,
			}, corr)
		}
	case "":
		if prevState == "backoff" || prevState == "auth_failed" {
			s.publishEvent(events.EventProviderServiceRecovered, events.ProviderServiceRecoveredPayload{
				ProviderID:       provID,
				ProviderName:     provName,
				Kind:             e.cfg.Kind,
				OutageDurationMS: outageDur.Milliseconds(),
			}, corr)
		}
	}
}

func releaseTitleKey(rel core.Release) string {
	if rel.TitleIDs.TMDB > 0 {
		return fmt.Sprintf("tmdb:%d", rel.TitleIDs.TMDB)
	}
	if rel.TitleIDs.IMDB != "" {
		return rel.TitleIDs.IMDB
	}
	return ""
}

func (s *Service) emitDebridCached(e *entry, rel core.Release, hash string, sizeBytes int64, wait time.Duration) {
	if s == nil || s.opts.Events == nil || e == nil {
		return
	}
	titleKey := releaseTitleKey(rel)
	s.publishEvent(events.EventProviderDebridCached, events.ProviderDebridCachedPayload{
		ProviderID:   e.cfg.ID,
		ProviderName: displayName(e.cfg),
		InfoHash:     hash,
		TitleKey:     titleKey,
		TitleName:    rel.Name,
		SizeBytes:    sizeBytes,
		WaitMS:       max(int64(0), wait.Milliseconds()),
	}, events.WithCorrelation(events.Correlation{SourceID: e.cfg.ID, TitleID: titleKey}))
}

func (s *Service) emitDebridFailed(e *entry, rel core.Release, hash string, err error) {
	if s == nil || s.opts.Events == nil || err == nil {
		return
	}
	provID, provName := "", ""
	if e != nil {
		provID = e.cfg.ID
		provName = displayName(e.cfg)
	}
	titleKey := releaseTitleKey(rel)
	s.publishEvent(events.EventProviderDebridFailed, events.ProviderDebridFailedPayload{
		ProviderID:   provID,
		ProviderName: provName,
		InfoHash:     hash,
		TitleKey:     titleKey,
		Error:        err.Error(),
	}, events.WithCorrelation(events.Correlation{SourceID: provID, TitleID: titleKey}))
}

func (s *Service) emitQuotaExhausted(cfg config.DebridProvider, metric string, used, limit int64) {
	if s == nil || s.opts.Events == nil {
		return
	}
	s.publishEvent(events.EventProviderQuotaExhausted, events.ProviderQuotaExhaustedPayload{
		ProviderID:   cfg.ID,
		ProviderName: displayName(cfg),
		Kind:         cfg.Kind,
		Metric:       metric,
		Used:         used,
		Limit:        limit,
	}, events.WithCorrelation(events.Correlation{SourceID: cfg.ID}))
}

func (s *Service) emitAccountQuotaEvents(cfg config.DebridProvider, acc Account) {
	if s == nil || s.opts.Events == nil || acc.Expires.IsZero() {
		return
	}
	rem := acc.Expires.Sub(s.now())
	window := time.Duration(premiumWarnWindowDays) * 24 * time.Hour
	if rem <= 0 || !acc.Premium {
		s.emitQuotaExhausted(cfg, "premium_subscription", premiumWarnWindowDays, premiumWarnWindowDays)
		return
	}
	if rem <= window {
		daysLeft := int64(rem / (24 * time.Hour))
		used := max(int64(1), int64(premiumWarnWindowDays)-daysLeft)
		pct := float64(used) * 100 / float64(premiumWarnWindowDays)
		s.publishEvent(events.EventProviderQuotaWarning, events.ProviderQuotaWarningPayload{
			ProviderID:   cfg.ID,
			ProviderName: displayName(cfg),
			Kind:         cfg.Kind,
			Metric:       "premium_subscription",
			Used:         used,
			Limit:        premiumWarnWindowDays,
			PercentUsed:  pct,
		}, events.WithCorrelation(events.Correlation{SourceID: cfg.ID}))
	}
}
