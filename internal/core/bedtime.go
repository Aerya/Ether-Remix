package core

import "time"

// Bedtime days name the day the sleep window starts (Sunday=0).
// An empty list means every day. Empty wakeTime defaults to 06:00;
// empty timezone uses the server's local timezone.
type Bedtime struct {
	Time     string `json:"time"`
	WakeTime string `json:"wakeTime"`
	Timezone string `json:"timezone"`
	Days     []int  `json:"days,omitempty"`
}

type BedtimeState struct {
	Active        bool       `json:"active"`
	NextAllowedAt *time.Time `json:"nextAllowedAt,omitempty"`
	StartsAt      *time.Time `json:"startsAt,omitempty"`
}

// ErrBedtime is an admission refusal, never a reason to interrupt an item.
type ErrBedtime struct{ NextAllowedAt time.Time }

func (e *ErrBedtime) Error() string {
	return "It is bedtime. Playback is allowed again at " + e.NextAllowedAt.Format(time.RFC3339) + "."
}
func (e *ErrBedtime) Unwrap() error { return ErrUnauthorized }
