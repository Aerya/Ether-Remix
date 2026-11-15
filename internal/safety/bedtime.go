package safety

import (
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func cloneBedtime(b *core.Bedtime) *core.Bedtime {
	if b == nil {
		return nil
	}
	c := *b
	c.Days = append([]int(nil), b.Days...)
	return &c
}
func bedtimeClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, fmt.Errorf("bedtime must use HH:MM")
	}
	return t.Hour()*60 + t.Minute(), nil
}
func validateBedtime(b *core.Bedtime) error {
	if b == nil {
		return nil
	}
	start, err := bedtimeClock(b.Time)
	if err != nil {
		return err
	}
	wake := b.WakeTime
	if wake == "" {
		wake = "06:00"
	}
	end, err := bedtimeClock(wake)
	if err != nil {
		return err
	}
	if start == end {
		return fmt.Errorf("bedtime and wake time must differ")
	}
	if b.Timezone != "" {
		if _, err := time.LoadLocation(b.Timezone); err != nil {
			return fmt.Errorf("invalid bedtime timezone")
		}
	}
	for _, day := range b.Days {
		if day < 0 || day > 6 {
			return fmt.Errorf("bedtime days must be 0..6")
		}
	}
	return nil
}

// BedtimeStatus is the one calendar evaluator used for admission and client
// countdowns. Windows are anchored on their start day, including across DST.
// A repeated clock time uses its first occurrence; a missing clock time moves
// forward by the DST gap, preserving the intended minutes.
func BedtimeStatus(u *core.User, b *core.Bedtime, now time.Time) core.BedtimeState {
	out := core.BedtimeState{}
	if u == nil || u.IsOwner || b == nil {
		return out
	}
	loc := time.Local
	if b.Timezone != "" {
		var err error
		loc, err = time.LoadLocation(b.Timezone)
		if err != nil {
			return out
		}
	}
	start, _ := bedtimeClock(b.Time)
	wake := b.WakeTime
	if wake == "" {
		wake = "06:00"
	}
	end, _ := bedtimeClock(wake)
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
	for offset := -1; offset <= 7; offset++ {
		date := day.AddDate(0, 0, offset)
		enabled := len(b.Days) == 0
		for _, d := range b.Days {
			if int(date.Weekday()) == d {
				enabled = true
			}
		}
		if !enabled {
			continue
		}
		from := bedtimeInstant(date, start, loc)
		wakeDate := date
		if end <= start {
			wakeDate = date.AddDate(0, 0, 1)
		}
		until := bedtimeInstant(wakeDate, end, loc)
		if !now.Before(from) && now.Before(until) {
			out.Active = true
			v := until.UTC()
			out.NextAllowedAt = &v
			return out
		}
		if now.Before(from) && out.StartsAt == nil {
			v := from.UTC()
			out.StartsAt = &v
		}
	}
	return out
}

func bedtimeInstant(date time.Time, minute int, loc *time.Location) time.Time {
	t := time.Date(date.Year(), date.Month(), date.Day(), minute/60, minute%60, 0, 0, loc)
	// Go may normalize a missing spring-forward time backward. Advance it by
	// the offset gap. Choose the earlier occurrence on fall-back explicitly.
	wall := t.In(loc)
	actual := wall.Hour()*60 + wall.Minute()
	if wall.Day() == date.Day() && actual < minute {
		t = t.Add(time.Duration(minute-actual) * time.Minute)
	}
	for _, delta := range []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour} {
		earlier := t.Add(-delta).In(loc)
		if earlier.Year() == date.Year() && earlier.YearDay() == date.YearDay() && earlier.Hour()*60+earlier.Minute() == minute {
			t = earlier
		}
	}
	return t
}
func CheckBedtime(u *core.User, p Policy, now time.Time) error {
	state := PlaybackTimeState(u, p, now)
	if state.Active {
		return &core.ErrBedtime{NextAllowedAt: *state.NextAllowedAt}
	}
	return nil
}

// PlaybackTimeState evaluates the single allowed-times policy. Old disjoint
// hourly schedules remain exact until the owner replaces them with bedtime.
func PlaybackTimeState(u *core.User, p Policy, now time.Time) core.BedtimeState {
	if u == nil || u.IsOwner {
		return core.BedtimeState{}
	}
	if p.Bedtime != nil {
		b := *p.Bedtime
		if b.Timezone == "" {
			b.Timezone = p.Timezone
		}
		return BedtimeStatus(u, &b, now)
	}
	if len(p.AllowedHours) == 0 {
		return core.BedtimeState{}
	}
	loc := time.Local
	// Legacy empty timezone historically meant UTC; preserve it on upgrade.
	if p.Timezone == "" {
		loc = time.UTC
	} else {
		var err error
		loc, err = time.LoadLocation(p.Timezone)
		if err != nil {
			return core.BedtimeState{}
		}
	}
	allowed := func(at time.Time) bool {
		h := at.In(loc).Hour()
		for _, v := range p.AllowedHours {
			if h == v {
				return true
			}
		}
		return false
	}
	current := allowed(now)
	state := core.BedtimeState{Active: !current}
	// A local day can have 25 hours. Minute stepping also handles half-hour
	// DST transitions and repeated hours without ambiguous local arithmetic.
	for at := now.Truncate(time.Minute).Add(time.Minute); at.Before(now.Add(27 * time.Hour)); at = at.Add(time.Minute) {
		if allowed(at) != current {
			v := at.UTC()
			if current {
				state.StartsAt = &v
			} else {
				state.NextAllowedAt = &v
			}
			break
		}
	}
	return state
}
