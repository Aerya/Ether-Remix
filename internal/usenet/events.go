package usenet

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
)

const usenetSaturatedCooldown = 30 * time.Second

func (p *pool) wireEvents() {
	if p == nil || p.o == nil || p.o.Events == nil {
		return
	}
	bus := p.o.Events
	provID := p.key
	provName := providerLabel(p.cfg)
	corr := events.WithCorrelation(events.Correlation{SourceID: provID})

	p.brk.onTransition = func(prevState, curState string, backoff, outageDur time.Duration, err error) {
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		switch curState {
		case StateBackoff:
			_ = bus.PublishTyped(events.EventProviderServiceOutage, events.ProviderServiceOutagePayload{
				ProviderID:   provID,
				ProviderName: provName,
				Kind:         "usenet",
				Error:        errText,
				BackoffSec:   max(1, int(backoff.Round(time.Second)/time.Second)),
			}, corr)
		case StateAuthFail:
			code := 0
			var pe *ProtocolError
			if errors.As(err, &pe) {
				code = pe.Code
			}
			_ = bus.PublishTyped(events.EventProviderAuthRejected, events.ProviderAuthRejectedPayload{
				ProviderID:   provID,
				ProviderName: provName,
				Kind:         "usenet",
				StatusCode:   code,
				Error:        errText,
			}, corr)
		case StateOK:
			if prevState == StateBackoff || prevState == StateAuthFail {
				_ = bus.PublishTyped(events.EventProviderServiceRecovered, events.ProviderServiceRecoveredPayload{
					ProviderID:       provID,
					ProviderName:     provName,
					Kind:             "usenet",
					OutageDurationMS: outageDur.Milliseconds(),
				}, corr)
			}
		}
	}

	var lastSatNs atomic.Int64
	p.sem.onSaturated = func(active, maxConns, waiting int) {
		nowNs := time.Now().UnixNano()
		prev := lastSatNs.Load()
		if prev > 0 && time.Duration(nowNs-prev) < usenetSaturatedCooldown {
			return
		}
		if !lastSatNs.CompareAndSwap(prev, nowNs) {
			return
		}
		_ = bus.PublishTyped(events.EventProviderConnectionsSaturated, events.ProviderConnectionsSaturatedPayload{
			ProviderID:        provID,
			ProviderName:      provName,
			ActiveConnections: active,
			MaxConnections:    maxConns,
			WaitingReaders:    waiting,
		}, corr)
	}
}
