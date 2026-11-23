package usenet

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// IsProviderBackoff reports whether err indicates Usenet provider backoff.
func (f *Fetcher) IsProviderBackoff(err error) bool {
	return errors.Is(err, ErrProviderBackoff)
}

// HasServingProvider reports whether at least one enabled primary Usenet
// provider can currently serve requests (neither disabled by credentials nor
// blocked in backoff without any established socket). When only backfill
// accounts are configured, they are considered instead.
func (f *Fetcher) HasServingProvider() bool {
	if f == nil {
		return false
	}
	f.mu.RLock()
	pools := f.pools
	f.mu.RUnlock()
	hasPrimary := false
	for _, p := range pools {
		if !p.cfg.Backfill {
			hasPrimary = true
			break
		}
	}
	for _, p := range pools {
		if hasPrimary && p.cfg.Backfill {
			continue
		}
		if p.brk.idleAllowed() != nil {
			continue
		}
		if p.brk.allow() == nil || p.establishedCount() > 0 {
			return true
		}
	}
	return false
}

// WaitProviderBackoff waits until at least one provider able to serve
// messageID leaves backoff (or returns an idle connection), or until no
// non-disabled provider remains, or ctx ends. It returns ok=true when the
// caller should retry the article fetch.
func (f *Fetcher) WaitProviderBackoff(ctx context.Context, messageID string, cause error) (bool, error) {
	if f == nil || !errors.Is(cause, ErrProviderBackoff) {
		return false, nil
	}
	id, err := normalizeMessageID(messageID)
	if err != nil {
		return false, err
	}
	for {
		if err := ctxErr(ctx); err != nil {
			return false, err
		}
		f.mu.RLock()
		if f.closed {
			f.mu.RUnlock()
			return false, ErrPoolClosed
		}
		fChanged := f.changed
		pools := append([]*pool(nil), f.pools...)
		f.mu.RUnlock()

		knownMissing := f.misses.get(id)
		var (
			able    int
			soonest time.Duration
			haveDue bool
			signals []<-chan struct{}
		)
		if fChanged != nil {
			signals = append(signals, fChanged)
		}
		for _, p := range pools {
			if slices.Contains(knownMissing, p.key) {
				continue
			}
			ready, disabled, hasTimer, wait, brkCh, idleCh := p.backoffWait()
			if disabled {
				continue
			}
			able++
			if ready {
				return true, nil
			}
			if hasTimer && (!haveDue || wait < soonest) {
				soonest = wait
				haveDue = true
			}
			if brkCh != nil {
				signals = append(signals, brkCh)
			}
			if idleCh != nil {
				signals = append(signals, idleCh)
			}
		}
		if able == 0 {
			return false, nil
		}
		if err := waitAnySignal(ctx, haveDue, soonest, signals); err != nil {
			return false, err
		}
	}
}

func waitAnySignal(ctx context.Context, haveDue bool, wait time.Duration, signals []<-chan struct{}) error {
	if haveDue && wait <= 0 {
		return nil
	}
	var timer *time.Timer
	var timerC <-chan time.Time
	if haveDue {
		timer = time.NewTimer(wait)
		defer timer.Stop()
		timerC = timer.C
	}
	switch len(signals) {
	case 0:
		select {
		case <-ctx.Done():
			return ctxErr(ctx)
		case <-timerC:
			return nil
		}
	case 1:
		select {
		case <-ctx.Done():
			return ctxErr(ctx)
		case <-timerC:
			return nil
		case <-signals[0]:
			return nil
		}
	case 2:
		select {
		case <-ctx.Done():
			return ctxErr(ctx)
		case <-timerC:
			return nil
		case <-signals[0]:
			return nil
		case <-signals[1]:
			return nil
		}
	case 3:
		select {
		case <-ctx.Done():
			return ctxErr(ctx)
		case <-timerC:
			return nil
		case <-signals[0]:
			return nil
		case <-signals[1]:
			return nil
		case <-signals[2]:
			return nil
		}
	case 4:
		select {
		case <-ctx.Done():
			return ctxErr(ctx)
		case <-timerC:
			return nil
		case <-signals[0]:
			return nil
		case <-signals[1]:
			return nil
		case <-signals[2]:
			return nil
		case <-signals[3]:
			return nil
		}
	default:
		wake := make(chan struct{})
		done := make(chan struct{})
		var once sync.Once
		var wg sync.WaitGroup
		for _, ch := range signals {
			wg.Add(1)
			go func(ch <-chan struct{}) {
				defer wg.Done()
				select {
				case <-ch:
					once.Do(func() { close(wake) })
				case <-done:
				}
			}(ch)
		}
		defer func() {
			close(done)
			wg.Wait()
		}()
		select {
		case <-ctx.Done():
			return ctxErr(ctx)
		case <-timerC:
			return nil
		case <-wake:
			return nil
		}
	}
}
