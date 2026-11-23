package flows

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduleEntry represents one persisted trigger.schedule row in flows_schedules.
type ScheduleEntry struct {
	FlowID  string
	NodeID  string
	NextRun time.Time
	LastRun time.Time
}

// NextScheduleTime computes the next wall-clock UTC firing time strictly after
// after for cfg. Monotonic clock readings are stripped so sleep/resume or clock
// adjustments coalesce missed firings rather than creating a backlog storm.
func NextScheduleTime(cfg ScheduleTriggerConfig, after time.Time) (time.Time, error) {
	after = after.Round(0).UTC()

	hasCron := strings.TrimSpace(cfg.Cron) != ""
	hasEvery := strings.TrimSpace(cfg.Every) != ""
	if hasCron == hasEvery {
		return time.Time{}, fmt.Errorf("flows: schedule trigger requires exactly one of cron or every")
	}

	if hasEvery {
		d, err := ParseDuration(strings.TrimSpace(cfg.Every))
		if err != nil || d <= 0 {
			return time.Time{}, fmt.Errorf("flows: invalid schedule interval %q", cfg.Every)
		}
		return after.Add(d).UTC(), nil
	}

	loc := time.UTC
	if tz := strings.TrimSpace(cfg.Timezone); tz != "" {
		var err error
		loc, err = time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, fmt.Errorf("flows: invalid schedule timezone %q: %w", cfg.Timezone, err)
		}
	}

	spec, err := parseCronSpec(cfg.Cron)
	if err != nil {
		return time.Time{}, err
	}
	return spec.next(after.In(loc))
}

type cronSpec struct {
	minute  [60]bool
	hour    [24]bool
	dom     [32]bool // 1..31
	month   [13]bool // 1..12
	dow     [7]bool  // 0..6 (0 = Sunday)
	domWild bool
	dowWild bool
}

func parseCronSpec(expr string) (cronSpec, error) {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@monthly":
		expr = "0 0 1 * *"
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronSpec{}, fmt.Errorf("flows: cron expression must have 5 fields (min hour dom month dow)")
	}

	var spec cronSpec
	spec.domWild = fields[2] == "*"
	spec.dowWild = fields[4] == "*"

	mins, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return cronSpec{}, fmt.Errorf("flows: cron minute: %w", err)
	}
	for _, v := range mins {
		spec.minute[v] = true
	}

	hours, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return cronSpec{}, fmt.Errorf("flows: cron hour: %w", err)
	}
	for _, v := range hours {
		spec.hour[v] = true
	}

	doms, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return cronSpec{}, fmt.Errorf("flows: cron day-of-month: %w", err)
	}
	for _, v := range doms {
		spec.dom[v] = true
	}

	months, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return cronSpec{}, fmt.Errorf("flows: cron month: %w", err)
	}
	for _, v := range months {
		spec.month[v] = true
	}

	dows, err := parseCronField(fields[4], 0, 7)
	if err != nil {
		return cronSpec{}, fmt.Errorf("flows: cron day-of-week: %w", err)
	}
	for _, v := range dows {
		if v == 7 {
			v = 0
		}
		spec.dow[v] = true
	}

	return spec, nil
}

func parseCronField(field string, minVal, maxVal int) ([]int, error) {
	var out []int
	for _, seg := range strings.Split(field, ",") {
		if seg == "" {
			return nil, fmt.Errorf("empty list segment")
		}
		base, stepStr, hasStep := strings.Cut(seg, "/")
		step := 1
		if hasStep {
			var err error
			step, err = strconv.Atoi(stepStr)
			if err != nil || step <= 0 || step > maxVal {
				return nil, fmt.Errorf("invalid step %q", stepStr)
			}
		}

		var lo, hi int
		switch {
		case base == "*":
			lo, hi = minVal, maxVal
		case strings.Contains(base, "-"):
			loStr, hiStr, _ := strings.Cut(base, "-")
			var err1, err2 error
			lo, err1 = strconv.Atoi(loStr)
			hi, err2 = strconv.Atoi(hiStr)
			if err1 != nil || err2 != nil || lo < minVal || hi > maxVal || lo > hi {
				return nil, fmt.Errorf("invalid range %q", base)
			}
		default:
			v, err := strconv.Atoi(base)
			if err != nil || v < minVal || v > maxVal {
				return nil, fmt.Errorf("value %q out of range [%d..%d]", base, minVal, maxVal)
			}
			lo = v
			if hasStep {
				hi = maxVal
			} else {
				hi = v
			}
		}

		for v := lo; v <= hi; v += step {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("field %q matches no values", field)
	}
	return out, nil
}

func (s cronSpec) matchesDay(t time.Time) bool {
	domMatch := s.dom[t.Day()]
	dowMatch := s.dow[int(t.Weekday())]
	if !s.domWild && !s.dowWild {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

// next finds the earliest UTC instant strictly after after (in its Location)
// matching s, searching up to 5 years ahead (handles leap years and DST).
func (s cronSpec) next(after time.Time) (time.Time, error) {
	loc := after.Location()
	// Advance to the next whole minute strictly after after.
	t := after.Truncate(time.Minute).Add(time.Minute).In(loc)
	limit := after.AddDate(5, 0, 0)

	// Bounded iteration count protects against any pathological TZ/cron combo.
	for range 100000 {
		if t.After(limit) {
			return time.Time{}, fmt.Errorf("flows: cron expression has no matching time within 5 years")
		}
		if !s.month[int(t.Month())] {
			y, m := t.Year(), t.Month()+1
			if m > 12 {
				y++
				m = 1
			}
			t = time.Date(y, m, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.matchesDay(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.hour[t.Hour()] {
			// Step by an hour in real time or to next local hour boundary,
			// ensuring strictly forward progress across fall-back DST transitions.
			nextH := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			if !nextH.After(t) {
				nextH = t.Add(time.Hour)
			}
			t = nextH
			continue
		}
		if !s.minute[t.Minute()] {
			t = t.Add(time.Minute).In(loc)
			continue
		}
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("flows: cron search exceeded iteration bound")
}
