package newznab

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

const indexerBudgetWarnPercent = 80.0

func (c *Client) indexerKind() string {
	cfg := c.Config()
	if cfg.Kind != "" {
		return cfg.Kind
	}
	if cfg.IsTorrent() {
		return "torznab"
	}
	return "newznab"
}

func (c *Client) publishDomainEvent(eventType string, payload any) {
	if c == nil || c.events == nil {
		return
	}
	_ = c.events.PublishTyped(eventType, payload, events.WithCorrelation(events.Correlation{
		SourceID: c.indexerID(),
	}))
}

func (c *Client) emitRequestFailed(kind string, err error, latency time.Duration) {
	if c == nil || c.events == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, errLocalBudget) || errors.Is(err, ErrIndexerPaused) {
		return
	}
	statusCode := 0
	var ae *APIError
	if errors.As(err, &ae) {
		statusCode = ae.Code
	}
	c.publishDomainEvent(events.EventIndexerRequestFailed, events.IndexerRequestFailedPayload{
		IndexerID:   c.indexerID(),
		IndexerName: c.Name(),
		Kind:        kind,
		StatusCode:  statusCode,
		Error:       err.Error(),
		LatencyMS:   latency.Milliseconds(),
	})
}

func (c *Client) emitBudgetWarning(day, kind string, used, limit int) {
	if c == nil || c.events == nil || limit <= 0 || used <= 0 {
		return
	}
	pct := float64(used) * 100 / float64(limit)
	if pct < indexerBudgetWarnPercent {
		return
	}
	metric := budgetMetricName(kind)
	c.budgetEventMu.Lock()
	if c.budgetWarnDay == nil {
		c.budgetWarnDay = map[string]string{}
	}
	if c.budgetWarnDay[metric] == day {
		c.budgetEventMu.Unlock()
		return
	}
	c.budgetWarnDay[metric] = day
	c.budgetEventMu.Unlock()

	c.publishDomainEvent(events.EventIndexerBudgetWarning, events.IndexerBudgetWarningPayload{
		IndexerID:   c.indexerID(),
		IndexerName: c.Name(),
		Metric:      metric,
		Used:        used,
		Limit:       limit,
		PercentUsed: pct,
	})
}

func (c *Client) emitBudgetExhausted(day, kind string, used, limit int) {
	if c == nil || c.events == nil || limit <= 0 {
		return
	}
	metric := budgetMetricName(kind)
	c.budgetEventMu.Lock()
	if c.budgetExhaustDay == nil {
		c.budgetExhaustDay = map[string]string{}
	}
	if c.budgetExhaustDay[metric] == day {
		c.budgetEventMu.Unlock()
		return
	}
	c.budgetExhaustDay[metric] = day
	c.budgetEventMu.Unlock()

	now := c.now().UTC()
	resetsAt := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	c.publishDomainEvent(events.EventIndexerBudgetExhausted, events.IndexerBudgetExhaustedPayload{
		IndexerID:   c.indexerID(),
		IndexerName: c.Name(),
		Metric:      metric,
		Used:        used,
		Limit:       limit,
		ResetsAt:    &resetsAt,
	})
}

func (c *Client) emitRateLimited(until time.Time, cause error) {
	if c == nil || c.events == nil {
		return
	}
	retryMS := max(int64(0), until.Sub(c.now()).Milliseconds())
	statusCode := 429
	var ae *APIError
	if errors.As(cause, &ae) && ae.Code > 0 {
		statusCode = ae.Code
	}
	c.publishDomainEvent(events.EventIndexerRateLimited, events.IndexerRateLimitedPayload{
		IndexerID:    c.indexerID(),
		IndexerName:  c.Name(),
		RetryAfterMS: retryMS,
		StatusCode:   statusCode,
	})
}

func budgetMetricName(kind string) string {
	if kind == "grab" {
		return "grabs"
	}
	return "hits"
}

var _ = core.ErrBudgetExhausted
