package flows

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDuration parses a flow duration: Go's time.ParseDuration units plus
// "d" (24h) and "w" (7d), which the editor offers ("7d", "2d") and people
// type. Units may be combined ("1d12h", "1w2d").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if !strings.ContainsAny(s, "dw") {
		return time.ParseDuration(s)
	}
	neg := false
	rest := s
	if rest[0] == '-' || rest[0] == '+' {
		neg = rest[0] == '-'
		rest = rest[1:]
	}
	var total time.Duration
	var plain strings.Builder
	for rest != "" {
		i := 0
		for i < len(rest) && (rest[i] == '.' || (rest[i] >= '0' && rest[i] <= '9')) {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		num := rest[:i]
		j := i
		for j < len(rest) && !(rest[j] == '.' || (rest[j] >= '0' && rest[j] <= '9')) {
			j++
		}
		unit := rest[i:j]
		rest = rest[j:]
		switch unit {
		case "d", "w":
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid duration %q", s)
			}
			day := 24 * time.Hour
			if unit == "w" {
				day *= 7
			}
			total += time.Duration(f * float64(day))
		default:
			plain.WriteString(num)
			plain.WriteString(unit)
		}
	}
	if plain.Len() > 0 {
		d, err := time.ParseDuration(plain.String())
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		total += d
	}
	if neg {
		total = -total
	}
	return total, nil
}

// humanDuration renders a validation bound the way people write it
// ("30d", "7d", "24h", "1m") rather than Go's "720h0m0s".
func humanDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	case d >= time.Hour && d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d >= time.Minute && d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	case d >= time.Second && d%time.Second == 0:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return d.String()
}

// durationIssue explains why a duration field is invalid, or "" when it
// parses and lies within [min, max]. what names the field ("delay").
func durationIssue(raw, what string, minD, maxD time.Duration) string {
	d, err := ParseDuration(raw)
	if err != nil {
		return fmt.Sprintf("%s %q is not a duration; use a number with s, m, h or d (for example 90s, 20m, 3h or 2d)", what, strings.TrimSpace(raw))
	}
	if d < minD || d > maxD {
		return fmt.Sprintf("%s must be between %s and %s", what, humanDuration(minD), humanDuration(maxD))
	}
	return ""
}
