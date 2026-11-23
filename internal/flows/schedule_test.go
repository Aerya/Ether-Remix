package flows

import (
	"testing"
	"time"
)

func TestNextScheduleTimeEvery(t *testing.T) {
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

	next, err := NextScheduleTime(ScheduleTriggerConfig{Every: "15m"}, base)
	if err != nil {
		t.Fatalf("NextScheduleTime every 15m: %v", err)
	}
	want := base.Add(15 * time.Minute)
	if !next.Equal(want) {
		t.Fatalf("got %s, want %s", next, want)
	}

	// Sub-second / invalid every must fail.
	if _, err := NextScheduleTime(ScheduleTriggerConfig{Every: "0s"}, base); err == nil {
		t.Fatalf("expected error for zero interval")
	}
	if _, err := NextScheduleTime(ScheduleTriggerConfig{Every: "notaduration"}, base); err == nil {
		t.Fatalf("expected error for invalid duration")
	}
	if _, err := NextScheduleTime(ScheduleTriggerConfig{}, base); err == nil {
		t.Fatalf("expected error for empty schedule config")
	}
}

func TestNextScheduleTimeCronAndTimezones(t *testing.T) {
	tests := []struct {
		name  string
		cfg   ScheduleTriggerConfig
		after time.Time
		want  time.Time
	}{
		{
			name:  "every_5_minutes",
			cfg:   ScheduleTriggerConfig{Cron: "*/5 * * * *"},
			after: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC),
			want:  time.Date(2026, 11, 19, 10, 5, 0, 0, time.UTC),
		},
		{
			name:  "strictly_after_current_minute_with_seconds",
			cfg:   ScheduleTriggerConfig{Cron: "0 10 * * *"},
			after: time.Date(2026, 11, 19, 10, 0, 1, 0, time.UTC),
			want:  time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		},
		{
			name:  "weekday_range_and_list",
			cfg:   ScheduleTriggerConfig{Cron: "30 9 * * 1-5"},
			after: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC), // Friday 09:30 -> Monday 09:30
			want:  time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC),
		},
		{
			name:  "sunday_as_7",
			cfg:   ScheduleTriggerConfig{Cron: "0 12 * * 7"},
			after: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC), // Saturday -> Sunday
			want:  time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "dom_or_dow_semantics",
			cfg:   ScheduleTriggerConfig{Cron: "0 8 25 * 0"}, // 25th OR Sunday
			after: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC),
			want:  time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC), // Sunday 22nd comes before 25th
		},
		{
			name:  "descriptor_daily",
			cfg:   ScheduleTriggerConfig{Cron: "@daily"},
			after: time.Date(2026, 11, 19, 14, 22, 0, 0, time.UTC),
			want:  time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "descriptor_hourly",
			cfg:   ScheduleTriggerConfig{Cron: "@hourly"},
			after: time.Date(2026, 11, 19, 14, 22, 0, 0, time.UTC),
			want:  time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC),
		},
		{
			name:  "iana_timezone_america_new_york",
			cfg:   ScheduleTriggerConfig{Cron: "0 9 * * *", Timezone: "America/New_York"}, // EST = UTC-5 in November
			after: time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC),                         // 08:00 EST
			want:  time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC),                         // 09:00 EST
		},
		{
			name:  "leap_day_feb_29",
			cfg:   ScheduleTriggerConfig{Cron: "0 0 29 2 *"},
			after: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
			want:  time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextScheduleTime(tc.cfg, tc.after)
			if err != nil {
				t.Fatalf("NextScheduleTime(%+v): %v", tc.cfg, err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("got %s, want %s", got.UTC(), tc.want.UTC())
			}
		})
	}

	if _, err := NextScheduleTime(ScheduleTriggerConfig{Cron: "* * *"}, time.Now()); err == nil {
		t.Fatalf("expected error for 3-field cron")
	}
	if _, err := NextScheduleTime(ScheduleTriggerConfig{Cron: "0 9 * * *", Timezone: "Invalid/Zone"}, time.Now()); err == nil {
		t.Fatalf("expected error for invalid timezone")
	}
}
