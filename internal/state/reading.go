package state

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("state", 12, `
ALTER TABLE state_watch ADD COLUMN reading_locator TEXT NOT NULL DEFAULT '';
ALTER TABLE state_watch ADD COLUMN reading_fraction REAL NOT NULL DEFAULT 0;
ALTER TABLE state_watch ADD COLUMN reading_label TEXT NOT NULL DEFAULT '';
ALTER TABLE state_watch ADD COLUMN reading_observed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE state_watch ADD COLUMN reading_updated_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX state_watch_reading_inprogress ON state_watch(user_id, last_viewed_at DESC, updated_at DESC) WHERE reading_locator <> '' AND reading_fraction > 0 AND reading_fraction < 0.98;`)
}

const (
	// ReadingCompletedThreshold is the fraction at which a book is marked read.
	ReadingCompletedThreshold = 0.98
	// ReadingObservedMaxSkew clamps client-supplied observedAt timestamps so a
	// skewed clock cannot pin future reading progress.
	ReadingObservedMaxSkew = 5 * time.Minute
)

var _ core.ReadingState = (*State)(nil)

// ErrInvalidReadingProgress reports malformed reading progress arguments.
var ErrInvalidReadingProgress = errors.New("state: invalid reading progress")

func isReadingInProgress(r *core.ReadingProgress) bool {
	return r != nil && r.Locator != "" && r.Fraction > 0 && r.Fraction < ReadingCompletedThreshold
}

func scanReading(locator string, fraction float64, label string, observedNS, updatedNS int64) *core.ReadingProgress {
	if locator == "" {
		return nil
	}
	return &core.ReadingProgress{
		Locator:    locator,
		Fraction:   fraction,
		Label:      label,
		ObservedAt: unixTime(observedNS),
		UpdatedAt:  unixTime(updatedNS),
	}
}

func readingColumns(r *core.ReadingProgress) (locator string, fraction float64, label string, observedNS, updatedNS int64) {
	if r == nil || r.Locator == "" {
		return "", 0, "", 0, 0
	}
	return r.Locator, r.Fraction, r.Label, unixNano(r.ObservedAt), unixNano(r.UpdatedAt)
}

// SetReadingProgress stores per-user reading progress for a book item on
// state_watch.
//
// Ordering:
//   - When ObservedAt is non-zero, it is clamped to now + 5 min; updates whose
//     clamped ObservedAt is earlier than the stored reading_observed_at are
//     ignored (newest-observed-wins).
//   - When ObservedAt is zero, the update wins on arrival (reading_observed_at
//     is stamped with server now).
//
// Completion:
//   - Fraction >= 0.98 marks the book played (increments view_count once and
//     sets counted = true). Completion is sticky until Unscrobble clears it.
func (s *State) SetReadingProgress(ctx context.Context, userID int64, key core.RatingKey, progress core.ReadingProgress) error {
	locator := strings.TrimSpace(progress.Locator)
	if locator == "" || math.IsNaN(progress.Fraction) || math.IsInf(progress.Fraction, 0) || progress.Fraction < 0 || progress.Fraction > 1 {
		return ErrInvalidReadingProgress
	}
	label := strings.TrimSpace(progress.Label)
	var applied bool
	var restoreContinue bool
	err := s.update(ctx, userID, []core.RatingKey{key}, func(cur *row, now time.Time) (bool, bool, []Event) {
		applied = false
		restoreContinue = false

		effectiveObserved := now
		if !progress.ObservedAt.IsZero() {
			obs := progress.ObservedAt.UTC()
			if maxObs := now.Add(ReadingObservedMaxSkew); obs.After(maxObs) {
				obs = maxObs
			}
			if cur.ws.Reading != nil && !cur.ws.Reading.ObservedAt.IsZero() && obs.Before(cur.ws.Reading.ObservedAt) {
				return false, false, nil
			}
			effectiveObserved = obs
		}

		applied = true
		cur.ws.Reading = &core.ReadingProgress{
			Locator:    locator,
			Fraction:   progress.Fraction,
			Label:      label,
			ObservedAt: effectiveObserved,
			UpdatedAt:  now,
		}
		cur.ws.LastViewedAt = now
		cur.ws.UpdatedAt = now
		cur.watchAt = now

		completed := false
		if progress.Fraction >= ReadingCompletedThreshold {
			if !cur.counted {
				if cur.ws.ViewCount == 0 {
					cur.ws.ViewCount = 1
				} else {
					cur.ws.ViewCount++
				}
				completed = true
			}
			cur.counted = true
		} else if progress.Fraction > 0 {
			restoreContinue = true
		}

		ev := Event{
			Kind:      EventProgress,
			UserID:    userID,
			Key:       cur.ws.Key,
			Fraction:  progress.Fraction,
			Completed: completed,
			At:        now,
		}
		evs := []Event{ev}
		if completed {
			comp := ev
			comp.Kind = EventCompleted
			evs = append(evs, comp)
		}
		return completed, completed, evs
	})
	if err != nil {
		return err
	}
	if applied && restoreContinue {
		_ = s.RestoreContinue(ctx, userID, key)
	}
	return nil
}
