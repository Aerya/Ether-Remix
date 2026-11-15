package frontend

import (
	"errors"
	"time"
)

// ErrTrackNotFound reports that an explicit selection does not identify one
// stream in the opened source.
var ErrTrackNotFound = errors.New("selected track is not available")

// ErrTrackSourceUnavailable reports that the broker cannot prove the opened
// source and its track layout across planning and playback admission.
var ErrTrackSourceUnavailable = errors.New("verified track source unavailable")

// ErrReceiptCapacity reports a device whose retained receipts are all in use.
var ErrReceiptCapacity = errors.New("experience: receipt limit reached")

// ReceiptCapacityError carries the earliest time retained authority expires.
type ReceiptCapacityError struct{ RetryAfter time.Duration }

func (e *ReceiptCapacityError) Error() string        { return ErrReceiptCapacity.Error() }
func (e *ReceiptCapacityError) Is(target error) bool { return target == ErrReceiptCapacity }
