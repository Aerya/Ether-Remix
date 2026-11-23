package stream

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type statTrustChecker interface {
	StatUntrusted() bool
}

const (
	engineStatTrustTTL     = 10 * time.Minute
	engineStatTrustMax     = 16_384
	engineStatTrustWindow  = 64
	engineStatMinSamples   = 3
	engineStatUntrustRate  = 0.25
	engineStatRecoveryRate = 0.10
)

// engineStatTrust is the fallback STAT-vs-BODY tracker used when Engine wraps a
// plain core.ArticleFetcher rather than *usenet.Fetcher (which tracks per
// provider pool and implements statTrustChecker).
type engineStatTrust struct {
	mu    sync.Mutex
	now   func() time.Time
	items map[string]*list.Element
	order list.List

	window    [engineStatTrustWindow]bool
	count     int
	fails     int
	pos       int
	untrusted bool
}

type engineStatItem struct {
	id          string
	statPresent bool
	bodyDone    bool
	bodyOK      bool
	counted     bool
	expires     time.Time
}

func newEngineStatTrust(now func() time.Time) *engineStatTrust {
	if now == nil {
		now = time.Now
	}
	return &engineStatTrust{
		now:   now,
		items: make(map[string]*list.Element),
	}
}

func canonicalMsgID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return id
}

func (s *engineStatTrust) getOrCreateLocked(id string, now time.Time) *engineStatItem {
	if el, ok := s.items[id]; ok {
		it := el.Value.(*engineStatItem)
		if now.After(it.expires) {
			*it = engineStatItem{id: id, expires: now.Add(engineStatTrustTTL)}
			s.order.MoveToBack(el)
		}
		return it
	}
	it := &engineStatItem{id: id, expires: now.Add(engineStatTrustTTL)}
	s.items[id] = s.order.PushBack(it)
	for s.order.Len() > 0 {
		front := s.order.Front()
		oldest := front.Value.(*engineStatItem)
		if s.order.Len() <= engineStatTrustMax && now.Before(oldest.expires) {
			break
		}
		s.order.Remove(front)
		delete(s.items, oldest.id)
	}
	return it
}

func (s *engineStatTrust) recordLocked(failed bool) {
	if s.count < engineStatTrustWindow {
		s.window[s.pos] = failed
		if failed {
			s.fails++
		}
		s.count++
		s.pos = (s.pos + 1) % engineStatTrustWindow
	} else {
		if s.window[s.pos] {
			s.fails--
		}
		s.window[s.pos] = failed
		if failed {
			s.fails++
		}
		s.pos = (s.pos + 1) % engineStatTrustWindow
	}
	if s.count < engineStatMinSamples {
		return
	}
	rate := float64(s.fails) / float64(s.count)
	if !s.untrusted && rate >= engineStatUntrustRate {
		s.untrusted = true
	} else if s.untrusted && rate <= engineStatRecoveryRate {
		s.untrusted = false
	}
}

func (s *engineStatTrust) noteStatPresent(id string) {
	if s == nil {
		return
	}
	id = canonicalMsgID(id)
	if id == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.getOrCreateLocked(id, now)
	it.statPresent = true
	if it.bodyDone && !it.counted {
		it.counted = true
		s.recordLocked(!it.bodyOK)
	}
}

func (s *engineStatTrust) noteBodyOutcome(id string, ok bool) {
	if s == nil {
		return
	}
	id = canonicalMsgID(id)
	if id == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.getOrCreateLocked(id, now)
	it.bodyDone = true
	it.bodyOK = ok
	if it.statPresent && !it.counted {
		it.counted = true
		s.recordLocked(!ok)
	}
}

func (s *engineStatTrust) isUntrusted() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.untrusted
}

func (e *Engine) statUntrusted() bool {
	if tc, ok := e.fetcher.(statTrustChecker); ok {
		return tc.StatUntrusted()
	}
	return e.statTrust.isUntrusted()
}

func (e *Engine) noteStatPresent(id string) {
	if _, ok := e.fetcher.(statTrustChecker); ok {
		return
	}
	e.statTrust.noteStatPresent(id)
}

func (e *Engine) noteBodyOutcome(id string, err error, ctx context.Context) {
	if _, ok := e.fetcher.(statTrustChecker); ok {
		return
	}
	if err == nil {
		e.statTrust.noteBodyOutcome(id, true)
		return
	}
	if (ctx != nil && ctx.Err() != nil) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, core.ErrBudgetExhausted) ||
		errors.Is(err, core.ErrSourceUnavailable) ||
		e.isProviderBackoff(err) {
		return
	}
	if isMissing(err) || errors.Is(err, core.ErrArticleCorrupt) || errors.Is(err, ErrCorrupt) {
		e.statTrust.noteBodyOutcome(id, false)
	}
}

// untrustedBodyProbeSlots expands BODY verification across the spread sample
// when STAT cannot be trusted, selecting head, mid, and tail slots plus one
// slot (fullSlot) for a full decoded article fetch.
func untrustedBodyProbeSlots(n int) (map[int]bool, int) {
	switch {
	case n <= 0:
		return nil, -1
	case n == 1:
		return map[int]bool{0: true}, 0
	case n == 2:
		return map[int]bool{0: true, 1: true}, 0
	case n == 3:
		return map[int]bool{0: true, 1: true, 2: true}, 1
	default:
		full := n / 3
		return map[int]bool{0: true, full: true, (2 * n) / 3: true, n - 1: true}, full
	}
}

// probeFullSampleBody downloads and yEnc-decodes the entire sampled segment to
// catch mid-body truncation, CRC mismatches, or stub bodies behind STAT 223.
func (e *Engine) probeFullSampleBody(ctx context.Context, ref slotRef, id string) error {
	prio := healthPriority(ctx)
	data, part, err := e.fetcher.BodyPart(ctx, id, ref.pf.groups, prio)
	e.noteBodyOutcome(id, err, ctx)
	if err != nil {
		return err
	}
	e.fetches.Add(1)
	e.fetchedBytes.Add(int64(len(data)))
	if _, err := e.normalize(ref.pf, ref.slot, data, part); err != nil {
		return err
	}
	want := ref.pf.expectedPart(ref.slot)
	if (want.Begin > 0 && part.Begin > 0 && part.Begin != want.Begin) ||
		(want.End > 0 && part.End > 0 && part.End != want.End) {
		return fmt.Errorf("stream: %s part %d yEnc range [%d,%d] != want [%d,%d]: %w",
			ref.pf.Name(), ref.slot+1, part.Begin, part.End, want.Begin, want.End, core.ErrArticleCorrupt)
	}
	return nil
}
