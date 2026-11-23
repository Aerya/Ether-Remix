package plugins

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// First-run state belongs to each plugin/call and must outlive the manager,
// including failed attempts. A restart must not reset the cadence of either job.
func TestScheduleCadenceAcrossRestarts(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	env.m.now = func() time.Time { return now }
	env.m.store.now = env.m.now
	env.m.deps.Now = env.m.now
	rec := env.installHello(t, "1.0.0")
	rec.Manifest.Ether.Schedule = []ScheduleEntry{
		{Call: "/ether/job/short", Every: "15m"},
		{Call: "/ether/job/long", Every: "1h"},
	}
	var err error
	rec.ManifestRaw, err = json.Marshal(rec.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	env.fake.routes["/ether/job/long"] = func(Request) (Response, error) {
		return Response{Status: 500}, nil
	}
	setEnabled := func(enabled bool) {
		t.Helper()
		rec.Enabled = enabled
		if err := env.m.store.upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	check := func(short, long int) {
		t.Helper()
		counts := map[string]int{}
		for _, path := range env.fake.paths() {
			counts[path]++
		}
		if counts["/ether/job/short"] != short || counts["/ether/job/long"] != long {
			t.Fatalf("schedule calls: %v, want short=%d long=%d", counts, short, long)
		}
	}
	restart := func() {
		t.Helper()
		m, err := New(env.m.deps) // Same durable database, entirely fresh scheduler state.
		if err != nil {
			t.Fatal(err)
		}
		m.RegisterRuntime("hosted", func(Installed) (Runtime, error) { return env.fake, nil })
		env.m = m
	}
	tick := func() {
		env.m.runOnce(ctx, map[string]time.Time{})
		env.m.waitSchedule()
	}

	setEnabled(false)
	tick()
	check(0, 0)
	setEnabled(true)
	env.lead = false
	tick()
	check(0, 0)
	env.lead = true
	tick() // Both unseen calls run on their first eligible tick.
	check(1, 1)
	tick()
	check(1, 1)
	now = now.Add(14 * time.Minute)
	restart()
	tick()
	check(1, 1)
	now = now.Add(time.Minute)
	tick() // Only the short job is due; the failed long job keeps its cadence.
	check(2, 1)
	now = now.Add(46 * time.Minute)
	restart()
	tick() // Overdue jobs run once, without replaying missed periods.
	check(3, 2)
	restart()
	tick()
	check(3, 2)
}
