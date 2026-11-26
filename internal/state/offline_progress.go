package state

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// ApplyOfflineProgress compares and writes in the same transaction, then
// publishes through the usual progress and completion journal.
func (s *State) ApplyOfflineProgress(ctx context.Context, user int64, key core.RatingKey, sample frontend.OfflineProgress) (frontend.TimelinePersistenceResult, error) {
	if sample.ObservedAt.IsZero() || sample.Position < 0 {
		return frontend.TimelinePersistenceResult{}, core.ErrNotAvailable
	}
	if sample.ReceivedAt.IsZero() {
		sample.ReceivedAt = s.now()
	}
	at := sample.ObservedAt
	if at.After(sample.ReceivedAt) {
		at = sample.ReceivedAt
	}
	var result frontend.TimelinePersistenceResult
	err := s.progressAt(ctx, user, key, progressWrite{
		offset: sample.Position, duration: sample.Duration, state: "stopped",
		at: at, origin: originFrom(ctx), offline: &sample, offlineResult: &result,
	})
	return result, err
}
