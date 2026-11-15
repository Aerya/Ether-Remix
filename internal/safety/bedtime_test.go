package safety

import (
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestBedtimeWindows(t *testing.T) {
	viewer := &core.User{ID: 1}
	for _, tc := range []struct {
		name, at, start, wake, zone string
		days                        []int
		owner, active               bool
		next                        string
	}{
		{"before", "2026-11-13T19:59:00Z", "20:00", "06:00", "UTC", nil, false, false, "2026-11-13T20:00:00Z"},
		{"start", "2026-11-13T20:00:00Z", "20:00", "", "UTC", nil, false, true, "2026-11-14T06:00:00Z"},
		{"midnight", "2026-11-14T01:00:00Z", "20:00", "06:00", "UTC", []int{5}, false, true, "2026-11-14T06:00:00Z"},
		{"wake boundary", "2026-11-14T06:00:00Z", "20:00", "06:00", "UTC", nil, false, false, "2026-11-14T20:00:00Z"},
		{"off day", "2026-11-14T20:00:00Z", "20:00", "06:00", "UTC", []int{5}, false, false, "2026-11-20T20:00:00Z"},
		{"same day", "2026-11-14T12:00:00Z", "09:00", "15:00", "UTC", nil, false, true, "2026-11-14T15:00:00Z"},
		{"spring", "2026-03-08T06:30:00Z", "20:00", "06:00", "America/Toronto", nil, false, true, "2026-03-08T10:00:00Z"},
		{"fall", "2026-11-01T06:30:00Z", "20:00", "06:00", "America/Toronto", nil, false, true, "2026-11-01T11:00:00Z"},
		{"missing wake", "2026-03-08T06:30:00Z", "20:00", "02:30", "America/Toronto", nil, false, true, "2026-03-08T07:30:00Z"},
		{"repeated wake", "2026-11-01T05:00:00Z", "20:00", "01:30", "America/Toronto", nil, false, true, "2026-11-01T05:30:00Z"},
		{"owner", "2026-11-14T01:00:00Z", "20:00", "06:00", "UTC", nil, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := *viewer
			u.IsOwner = tc.owner
			b := &core.Bedtime{Time: tc.start, WakeTime: tc.wake, Timezone: tc.zone, Days: tc.days}
			if err := validateBedtime(b); err != nil {
				t.Fatal(err)
			}
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			state := BedtimeStatus(&u, b, at)
			if state.Active != tc.active {
				t.Fatalf("active=%v", state.Active)
			}
			next := state.StartsAt
			if state.Active {
				next = state.NextAllowedAt
			}
			if tc.next != "" && (next == nil || next.Format(time.RFC3339) != tc.next) {
				t.Fatalf("next=%v want %s", next, tc.next)
			}
			e := CheckBedtime(&u, Policy{Bedtime: b}, at)
			var bedtime *core.ErrBedtime
			if tc.active != errors.As(e, &bedtime) {
				t.Fatalf("error=%v", e)
			}
			if bedtime != nil && (!errors.Is(e, core.ErrUnauthorized) || !bedtime.NextAllowedAt.Equal(*state.NextAllowedAt)) {
				t.Fatal("typed error loses next allowed")
			}
		})
	}
	t.Run("legacy hours share admission evaluator", func(t *testing.T) {
		p := Policy{AllowedHours: []int{9, 10, 15}, Timezone: "America/Toronto"}
		at := time.Date(2026, 11, 14, 17, 30, 0, 0, time.UTC)
		state := PlaybackTimeState(viewer, p, at)
		want := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
		if !state.Active || state.NextAllowedAt == nil || !state.NextAllowedAt.Equal(want) {
			t.Fatalf("legacy state: %+v", state)
		}
		if !CanPlayWithPolicy(viewer, p, false, at) {
			t.Fatal("continuing item interrupted")
		}
		p.Bedtime = &core.Bedtime{Time: "20:30", WakeTime: "06:00", Timezone: "UTC"}
		if err := CheckBedtime(viewer, p, at); err != nil {
			t.Fatal("legacy hours override replacement bedtime", err)
		}
	})

}
