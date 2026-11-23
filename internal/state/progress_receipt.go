package state

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type progressReceiptKey struct{}

// ProgressWithReceipt commits receipt, watch state and history together. The
// callback may run again after a row conflict and must use only the supplied
// transaction. This synchronous path never acknowledges an outbox-only write.
func (s *State) ProgressWithReceipt(ctx context.Context, user int64, key core.RatingKey, position, duration time.Duration, status string, receipt func(*sql.Tx) error) error {
	if receipt == nil {
		return errors.New("state: missing progress receipt")
	}
	if position < 0 {
		position = 0
	}
	ctx = context.WithValue(ctx, progressReceiptKey{}, receipt)
	return s.progressAt(ctx, user, key, progressWrite{offset: position, duration: duration, state: status, origin: originFrom(ctx)})
}

// AcceptProgress commits a timeline before returning and hands notification
// delivery to the caller. Durable state/history/journal writes remain in the
// transaction; subscribers may perform slow work only after acceptance.
func (s *State) AcceptProgress(ctx context.Context, user int64, key core.RatingKey, position, duration time.Duration, status string) (func(), error) {
	var events []Event
	ctx = context.WithValue(ctx, acceptedEventsKey{}, &events)
	err := s.ProgressWithReceipt(ctx, user, key, position, duration, status, func(*sql.Tx) error { return nil })
	if err != nil {
		return nil, err
	}
	return func() {
		for _, event := range events {
			s.emit(event)
		}
	}, nil
}

type acceptedEventsKey struct{}
