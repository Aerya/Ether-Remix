package newznab

// Ajout Ether-Remix : distinguer la pause imposée par le service (limite de
// requêtes, Retry-After) du budget journalier réglé dans Ether, et allonger
// la pause tant que le service continue de refuser. L'état reste en mémoire.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/retryafter"
)

// pseRateState est l'état en mémoire de la pause qui s'allonge ; il est
// protégé par rateLimitState.mu.
type pseRateState struct {
	streak int  // refus consécutifs sans aucun succès
	hadRA  bool // dernier refus : le service a fourni un Retry-After
	plain  bool // assistant d'ajout : erreurs d'origine, sans enveloppe
}

// pseNextPause compte un refus et renvoie la fin de pause. Sans Retry-After,
// la pause double à chaque refus consécutif (15 min, 30 min, 1 h…), au plus
// maxRateLimitPause. Un refus reçu pendant une pause en cours (requête déjà
// partie) ne la prolonge pas. rate.mu doit être tenu.
func (c *Client) pseNextPause(retryAfter string) time.Time {
	now := c.now()
	if !now.Before(c.rate.until) || c.rate.pse.streak == 0 {
		c.rate.pse.streak++
	}
	n := c.rate.pse.streak
	if d, ok := retryafter.Parse(retryAfter, now, maxRateLimitPause); ok {
		c.rate.pse.hadRA = true
		return now.Add(max(time.Second, d))
	}
	c.rate.pse.hadRA = false
	pause := defaultRateLimitPause
	for i := 1; i < n && pause < maxRateLimitPause; i++ {
		pause *= 2
	}
	return now.Add(min(pause, maxRateLimitPause))
}

// pseRateOK remet la pause à sa durée de départ au premier succès.
func (c *Client) pseRateOK() {
	c.rate.mu.Lock()
	c.rate.pse.streak = 0
	c.rate.mu.Unlock()
}

// pseLogPause journalise le refus, sans URL ni clé. rate.mu doit être tenu.
func (c *Client) pseLogPause(cause error, until time.Time) {
	code := http.StatusTooManyRequests
	var ae *APIError
	if errors.As(cause, &ae) {
		code = ae.Code
	}
	c.log.Info("indexer paused by service; will retry automatically", "indexer", c.Name(),
		"status", code, "retryAfterHeader", c.rate.pse.hadRA, "consecutiveRefusals", c.rate.pse.streak,
		"pause", until.Sub(c.now()).Round(time.Second).String(), "retryAt", until.UTC())
}

// pseThrottledCause enveloppe le refus du service ; cause reste dans la chaîne.
func (c *Client) pseThrottledCause(until time.Time, cause error) error {
	if c.rate.pse.plain {
		return cause
	}
	if !errors.Is(cause, core.ErrBudgetExhausted) {
		cause = errors.Join(cause, core.ErrBudgetExhausted)
	}
	return &core.IndexerThrottledError{Indexers: []string{c.Name()}, RetryAt: until, Err: cause}
}

// pseThrottledErr : requête refusée par Ether parce que le service impose une pause.
func (c *Client) pseThrottledErr(until time.Time) error {
	return &core.IndexerThrottledError{Indexers: []string{c.Name()}, RetryAt: until,
		Err: fmt.Errorf("indexer %q paused until %s: %w: %w", c.Name(), until.UTC().Format(time.RFC3339), errLocalBudget, core.ErrBudgetExhausted)}
}

// pseDailyLimitErr : limite journalière réglée dans Ether atteinte (pas une pause du service).
func (c *Client) pseDailyLimitErr(kind string) error {
	return &core.IndexerDailyLimitError{Indexers: []string{c.Name()},
		Err: fmt.Errorf("indexer %q %s budget: %w: %w", c.Name(), kind, errLocalBudget, core.ErrBudgetExhausted)}
}

// pseExhaustedErr : le budget est épuisé, soit par une pause du service, soit par la limite journalière.
func (c *Client) pseExhaustedErr(ctx context.Context) error {
	if until, err := c.rateLimitUntil(ctx); err == nil && c.now().Before(until) {
		return c.pseThrottledErr(until)
	}
	return c.pseDailyLimitErr("hit")
}

// psePauseOrBudget remplace « all indexers » : tous les refus sont des
// budgets épuisés, mais pas forcément le même. Il nomme les indexeurs en
// pause chez le service, la reprise la plus proche, et indique si un autre
// indexeur a répondu sans erreur.
func psePauseOrBudget(_ context.Context, clients []*Client, slot func(i int) (err error, searched bool)) error {
	var paused, daily []string
	var retryAt time.Time
	othersAnswered, local := false, false
	for i := range clients {
		name := clients[i].Name()
		serr, searched := slot(i)
		var thr *core.IndexerThrottledError
		var lim *core.IndexerDailyLimitError
		switch {
		case serr == nil:
			othersAnswered = othersAnswered || searched
		case errors.As(serr, &thr):
			if !slices.Contains(paused, name) {
				paused = append(paused, name)
			}
			local = local || errors.Is(thr.Err, errLocalBudget)
			if !thr.RetryAt.IsZero() && (retryAt.IsZero() || thr.RetryAt.Before(retryAt)) {
				retryAt = thr.RetryAt
			}
		case errors.As(serr, &lim):
			daily = append(daily, name)
		}
	}
	if len(paused) > 0 {
		err := fmt.Errorf("indexers paused by service: %w", core.ErrBudgetExhausted)
		if local {
			err = fmt.Errorf("indexers paused by service: %w: %w", errLocalBudget, core.ErrBudgetExhausted)
		}
		return &core.IndexerThrottledError{Indexers: paused, RetryAt: retryAt, OthersAnswered: othersAnswered, Err: err}
	}
	if len(daily) > 0 {
		return &core.IndexerDailyLimitError{Indexers: daily,
			Err: fmt.Errorf("indexers at their daily limit: %w: %w", errLocalBudget, core.ErrBudgetExhausted)}
	}
	return fmt.Errorf("indexers at their budget: %w", core.ErrBudgetExhausted)
}
