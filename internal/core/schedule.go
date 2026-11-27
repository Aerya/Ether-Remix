package core

import (
	"fmt"
	"slices"
	"time"
)

// Schedule is the one "when" of a row: days, times of day, months, date
// ranges (one-off or every year) and holidays, all in server local time.
// Every set field must match; within a field, any value matches. The zero
// schedule is "any time". Devices are not part of it: a row's devices are
// a separate placement field (RowPlacement.Devices).
type Schedule struct {
	// Days uses 0 = Sunday … 6 = Saturday.
	Days []int `json:"days,omitempty"`
	// Dayparts are DaypartMorning, DaypartAfternoon, DaypartEvening and
	// DaypartLate (10 PM–5 AM, which belongs to the day it started on).
	Dayparts []string `json:"dayparts,omitempty"`
	// FromHour is inclusive and ToHour exclusive; the window wraps past
	// midnight when FromHour > ToHour; equal hours mean all day.
	FromHour *int `json:"fromHour,omitempty"`
	ToHour   *int `json:"toHour,omitempty"`
	// Months uses 1 = January … 12.
	Months []int `json:"months,omitempty"`
	// Dates are inclusive ranges; any one matching is enough.
	Dates []DateRange `json:"dates,omitempty"`
	// Holidays are HolidayInfo ids; any one matching is enough.
	Holidays []string `json:"holidays,omitempty"`
}

// DateRange is an inclusive range of days: "YYYY-MM-DD" once, or "MM-DD"
// every year (Yearly). A yearly range may wrap the new year (12-20 → 01-06).
type DateRange struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Yearly bool   `json:"yearly,omitempty"`
}

// IsZero reports whether the schedule sets nothing (any time).
func (s *Schedule) IsZero() bool {
	return s == nil || len(s.Days) == 0 && len(s.Dayparts) == 0 && s.FromHour == nil && s.ToHour == nil &&
		len(s.Months) == 0 && len(s.Dates) == 0 && len(s.Holidays) == 0
}

// Clone returns a deep copy.
func (s *Schedule) Clone() *Schedule {
	if s == nil {
		return nil
	}
	c := *s
	c.Days = slices.Clone(s.Days)
	c.Dayparts = slices.Clone(s.Dayparts)
	c.Months = slices.Clone(s.Months)
	c.Dates = slices.Clone(s.Dates)
	c.Holidays = slices.Clone(s.Holidays)
	if s.FromHour != nil {
		h := *s.FromHour
		c.FromHour = &h
	}
	if s.ToHour != nil {
		h := *s.ToHour
		c.ToHour = &h
	}
	return &c
}

// Allows reports whether the schedule lets a row appear at now (local).
func (s *Schedule) Allows(now time.Time) bool {
	if s.IsZero() {
		return true
	}
	if len(s.Months) > 0 && !slices.Contains(s.Months, int(now.Month())) {
		return false
	}
	// Late (10 PM–5 AM) belongs to the evening it started on, so a Friday
	// late row still shows at 1 AM Saturday.
	day := now
	if DaypartAt(now) == DaypartLate && now.Hour() < 5 {
		day = now.AddDate(0, 0, -1)
	}
	if len(s.Days) > 0 && !slices.Contains(s.Days, int(day.Weekday())) {
		return false
	}
	if len(s.Dayparts) > 0 && !slices.Contains(s.Dayparts, DaypartAt(now)) {
		return false
	}
	if s.FromHour != nil || s.ToHour != nil {
		from, to := 0, 24
		if s.FromHour != nil {
			from = *s.FromHour
		}
		if s.ToHour != nil {
			to = *s.ToHour
		}
		h := now.Hour()
		switch {
		case from == to:
		case from < to:
			if h < from || h >= to {
				return false
			}
		default:
			if h < from && h >= to {
				return false
			}
		}
	}
	if len(s.Dates) > 0 {
		ok := false
		for _, r := range s.Dates {
			ok = ok || r.contains(now)
		}
		if !ok {
			return false
		}
	}
	if len(s.Holidays) > 0 {
		ok := false
		for _, h := range s.Holidays {
			ok = ok || HolidayActive(h, now)
		}
		if !ok {
			return false
		}
	}
	return true
}

func (r DateRange) contains(now time.Time) bool {
	if r.Yearly {
		md := now.Format("01-02")
		if r.From <= r.To {
			return md >= r.From && md <= r.To
		}
		return md >= r.From || md <= r.To
	}
	d := now.Format("2006-01-02")
	return (r.From == "" || d >= r.From) && (r.To == "" || d <= r.To)
}

// Validate checks every field; the error names the first bad one.
func (s *Schedule) Validate() error {
	if s == nil {
		return nil
	}
	for _, d := range s.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("days: %d is not 0 (Sunday) to 6 (Saturday)", d)
		}
	}
	for _, p := range s.Dayparts {
		switch p {
		case DaypartMorning, DaypartAfternoon, DaypartEvening, DaypartLate:
		default:
			return fmt.Errorf("dayparts: %q is not morning, afternoon, evening or late", p)
		}
	}
	for _, h := range []*int{s.FromHour, s.ToHour} {
		if h != nil && (*h < 0 || *h > 24) {
			return fmt.Errorf("hours must be 0 to 24")
		}
	}
	for _, m := range s.Months {
		if m < 1 || m > 12 {
			return fmt.Errorf("months: %d is not 1 to 12", m)
		}
	}
	for _, r := range s.Dates {
		layout := "2006-01-02"
		if r.Yearly {
			layout = "01-02"
		}
		for _, v := range []string{r.From, r.To} {
			if v == "" && !r.Yearly {
				continue
			}
			if _, err := time.Parse(layout, v); err != nil {
				return fmt.Errorf("dates: %q is not %s", v, map[bool]string{true: "MM-DD", false: "YYYY-MM-DD"}[r.Yearly])
			}
		}
		if !r.Yearly && r.From != "" && r.To != "" && r.To < r.From {
			return fmt.Errorf("dates: %s ends before it starts", r.From)
		}
	}
	for _, h := range s.Holidays {
		if !ValidHoliday(h) {
			return fmt.Errorf("holidays: %q is not a known holiday", h)
		}
	}
	return nil
}
