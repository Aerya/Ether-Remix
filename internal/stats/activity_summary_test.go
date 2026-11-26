package stats

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestActivitySummary(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := time.Date(2026, 10, 18, 23, 30, 0, 0, time.UTC)
	for _, r := range []struct {
		id, decision                string
		offset, end, watched, bytes int64
		audio                       any
	}{{"a", "directplay", 0, 7200, 1000, 1000, nil}, {"b", "copy", 3600, 7200, 2000, 2000, 0}} {
		_, err = db.Exec(`INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated,ended,watched_ms,bytes,audio_copy) VALUES(?,1,1000000000000001,'u','t','p','d',?,?,?,?,?,?,?)`, r.id, r.decision, start.Unix()+r.offset, start.Unix()+r.end, start.Unix()+r.end, r.watched, r.bytes, r.audio)
		if err != nil {
			t.Fatal(err)
		}
	}
	s := New(db, Options{})
	sum, err := s.Summary(ctx, start.Add(-time.Hour), start.Add(4*time.Hour), 0, 120)
	if err != nil {
		t.Fatal(err)
	}
	if sum.PeakStreams != 2 || sum.AvgBitrateKbps != 8 || len(sum.Daily) != 1 || sum.Daily[0].Label != "2026-10-19" || len(sum.Heatmap) != 2 || sum.Heatmap[0].Weekday != 1 || sum.Heatmap[0].Hour != 1 || sum.DailyDelivery[0].Direct != 1 || sum.DailyDelivery[0].Transcode != 1 {
		t.Fatalf("summary: %+v", sum)
	}
}

func TestSummaryPlatformNormalizationAndMerging(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	rows := []struct {
		id, platform string
		bytes        int64
	}{
		{"w1", "web", 1000},
		{"w2", "Web", 500},
		{"t1", "tvos", 2000},
		{"t2", "tvOS", 3000},
		{"t3", "Apple TV", 4000},
		{"a1", "Android", 700},
		{"a2", "android", 300},
		{"atv", "androidtv", 800},
		{"l1", "linux", 600},
		{"u1", "", 100},
		{"u2", "Unknown", 200},
	}
	for _, r := range rows {
		_, err = db.Exec(`INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated,ended,watched_ms,bytes) VALUES(?,1,1000000000000001,'u','t',?,'d','directplay',?,?,?,1000,?)`,
			r.id, r.platform, start.Unix(), start.Unix()+60, start.Unix()+60, r.bytes)
		if err != nil {
			t.Fatal(err)
		}
	}
	sum, err := New(db, Options{}).Summary(ctx, start.Add(-time.Minute), start.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Bucket{
		{Label: "Apple TV", Plays: 3, WatchedMS: 3000, Bytes: 9000},
		{Label: "Web", Plays: 2, WatchedMS: 2000, Bytes: 1500},
		{Label: "Android", Plays: 2, WatchedMS: 2000, Bytes: 1000},
		{Label: "", Plays: 2, WatchedMS: 2000, Bytes: 300},
		{Label: "Android TV", Plays: 1, WatchedMS: 1000, Bytes: 800},
		{Label: "Linux", Plays: 1, WatchedMS: 1000, Bytes: 600},
	}
	if len(sum.Platforms) != len(want) {
		t.Fatalf("platforms = %+v, want %+v", sum.Platforms, want)
	}
	for i := range want {
		if sum.Platforms[i] != want[i] {
			t.Fatalf("platforms[%d] = %+v, want %+v", i, sum.Platforms[i], want[i])
		}
	}
}

func TestActivityCatalogueAndLegacyCopy(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	statements := []string{
		`INSERT INTO catalog_titles(key,type,title) VALUES(2000000000000001,'show','Series')`,
		`INSERT INTO catalog_titles(key,type,show_key,show_title) VALUES(4000000010010001,'episode',2000000000000001,'Series'),(4000000010010002,'episode',2000000000000001,'Series')`,
		`INSERT INTO catalog_titles(key,type,title) VALUES(1000000000000001,'movie','Film')`,
		`INSERT INTO catalog_genres(key,genre) VALUES(2000000000000001,'Drama'),(1000000000000001,'Comedy')`,
	}
	for _, q := range statements {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i, key := range []int64{4000000010010001, 4000000010010002, 1000000000000001} {
		_, err = db.Exec(`INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated,ended,watched_ms,bytes) VALUES(?,1,?,'u','t','p','d','copy',?,?,?,?,?)`, string(rune('a'+i)), key, start.Unix(), start.Unix()+60, start.Unix()+60, 1000, 1000)
		if err != nil {
			t.Fatal(err)
		}
	}
	sum, err := New(db, Options{}).Summary(ctx, start.Add(-time.Second), start.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Shows) != 1 || sum.Shows[0].Label != "Series" || sum.Shows[0].Plays != 2 || len(sum.Genres) != 2 || sum.Genres[0].Label != "Drama" || sum.Genres[0].WatchedMS != 2000 || len(sum.Decisions) != 1 || sum.Decisions[0].Label != "direct-stream" || sum.DailyDelivery[0].Direct != 3 {
		t.Fatalf("summary: %+v", sum)
	}
}

func TestActivityPlayAddressAndAudioDecision(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s := New(db, Options{Now: func() time.Time { return now }})
	p := &core.PlaySession{ID: "one", UserID: 1, Key: core.MovieKey(1), StartedAt: now, BytesServed: 8 << 20, Client: core.ClientCaps{ClientIP: "192.0.2.1"}, Decision: core.DecisionRemux, DecisionVideoCopy: true, DecisionAudioCopy: false}
	if err = s.Record(ctx, p, false, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := s.History(ctx, now.Add(-time.Second), now.Add(time.Second), 0, 10)
	if err != nil || len(rows) != 1 || rows[0].IP == nil || *rows[0].IP != "192.0.2.1" {
		t.Fatalf("history: %+v %v", rows, err)
	}
	detail, err := s.PlayByID(ctx, "one")
	if err != nil || detail["ip"] != "192.0.2.1" {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	sum, err := s.Summary(ctx, now.Add(-time.Second), now.Add(time.Second), 0)
	if err != nil || len(sum.Decisions) != 1 || sum.Decisions[0].Label != "audio-transcode" {
		t.Fatalf("summary: %+v %v", sum.Decisions, err)
	}
}

func TestActivityMatchesSummary(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// 2026-11-24 20:42:00 EST == 2026-11-25 01:42:00 UTC (tz = -300).
	now := time.Date(2026, 11, 25, 1, 42, 0, 0, time.UTC)
	tz := -300
	s := New(db, Options{Now: func() time.Time { return now }})

	rows := []struct {
		id        string
		userID    int64
		ratingKey int64
		channelID string
		started   time.Time
		watchedMS int64
		bytes     int64
	}{
		// Outside 30d window: excluded from both Summary and Activity.
		{"too-old", 1, 1000000000000001, "", now.AddDate(0, 0, -30).Add(-time.Minute), 1_800_000, 10 << 20},
		// Boundary day (2026-10-25 21:00 EST): inside rolling 30d window!
		{"boundary", 1, 1000000000000001, "", time.Date(2026, 10, 26, 2, 0, 0, 0, time.UTC), 150_000, 5 << 20},
		// Failed 0-byte play: excluded from both Summary and Activity.
		{"failed-zero", 2, 1000000000000002, "", now.Add(-24 * time.Hour), 0, 0},
		// Same user, same movie twice on the same local day (must count as 2 plays, not collapsed to 1).
		{"vod-1", 1, 1000000000000001, "", time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC), 150_000, 8 << 20},
		{"vod-2", 1, 1000000000000001, "", time.Date(2026, 11, 23, 21, 0, 0, 0, time.UTC), 150_000, 8 << 20},
		// Live TV play on a different viewer the same local day (2026-11-23).
		{"live-1", 2, 0, "ch-espn", time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC), 1_800_000, 20 << 20},
		// Non-catalog/plugin play on today (2026-11-24 EST, after UTC midnight).
		{"plugin-1", 3, 9005000000092613, "", time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC), 150_000, 3 << 20},
	}
	for _, r := range rows {
		_, err = db.Exec(
			`INSERT INTO stats_plays(id,user_id,rating_key,username,title,channel_id,platform,device,decision,started,updated,ended,watched_ms,bytes) VALUES(?,?,?,'u','t',?,'p','d','directplay',?,?,?,?,?)`,
			r.id, r.userID, r.ratingKey, r.channelID, r.started.Unix(), r.started.Unix()+60, r.started.Unix()+60, r.watchedMS, r.bytes,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	since, until := s.daysWindow(30)
	sum, err := s.Summary(ctx, since, until, 0, tz)
	if err != nil {
		t.Fatal(err)
	}
	days, err := s.Activity(ctx, 30, tz)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 31 || days[0].Date != "2026-10-25" || days[len(days)-1].Date != "2026-11-24" {
		t.Fatalf("days span = %d (%s..%s), want 31 (2026-10-25..2026-11-24)", len(days), days[0].Date, days[len(days)-1].Date)
	}
	var totalPlays int64
	var totalWatchedMS int64
	byDate := map[string]core.ActivityDay{}
	for _, d := range days {
		totalPlays += int64(d.Plays)
		totalWatchedMS += d.WatchedMS
		byDate[d.Date] = d
	}
	if totalPlays != sum.Plays || totalPlays != 5 {
		t.Fatalf("activity plays = %d, summary plays = %d, want 5", totalPlays, sum.Plays)
	}
	if totalWatchedMS != sum.WatchedMS || totalWatchedMS != 2_400_000 {
		t.Fatalf("activity watchedMs = %d, summary watchedMs = %d, want 2400000", totalWatchedMS, sum.WatchedMS)
	}
	// Verify local-day bucketing matches Summary.Daily and distinct viewer counts.
	if len(sum.Daily) != 3 {
		t.Fatalf("summary daily = %+v, want 3 active days", sum.Daily)
	}
	for _, b := range sum.Daily {
		d, ok := byDate[b.Label]
		if !ok || int64(d.Plays) != b.Plays || d.WatchedMS != b.WatchedMS {
			t.Fatalf("day %s mismatch: activity=%+v summary=%+v", b.Label, d, b)
		}
	}
	if got := byDate["2026-11-23"]; got.Plays != 3 || got.UniqueViewers != 2 {
		t.Fatalf("2026-11-23 = %+v, want 3 plays and 2 unique viewers", got)
	}
	if got := byDate["2026-10-25"]; got.Plays != 1 || got.WatchedMS != 150_000 || got.Hours != 0 {
		t.Fatalf("2026-10-25 = %+v, want 1 play, 150000ms, 0.0h rounded day", got)
	}
}
