package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/logredact"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlayTimelineCaptureCapRetentionAndEmptyState(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	svc := New(db, Options{Now: func() time.Time { return now }})
	defer svc.Close(ctx)

	sink := NewSessionLogSink()
	sink.SetResolver(func(id string) string {
		if id == "enc-public-1" {
			return "sess-fail-1"
		}
		return id
	})
	svc.AttachLogSink(sink)

	var out bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	base := slog.NewTextHandler(&out, &slog.HandlerOptions{Level: level})
	logger := slog.New(logredact.Handler(sink.Wrap(base)))

	// 1. Healthy session: Info+ captured, Debug suppressed, unrelated logs ignored.
	healthy := &core.PlaySession{
		ID:          "sess-ok",
		UserID:      7,
		ClientID:    "apple-tv",
		Key:         1001,
		Username:    "Sam",
		TitleName:   "Arrival (2016)",
		Client:      core.ClientCaps{ClientID: "apple-tv", DeviceName: "Living Room TV", Platform: "tvOS"},
		Decision:    core.DecisionDirectPlay,
		StartedAt:   now,
		BytesServed: 8 << 20,
	}
	if err := svc.Record(ctx, healthy, false, ""); err != nil {
		t.Fatal(err)
	}
	svc.ObserveTimeline(playback.TimelineEvent{
		SessionID: "sess-ok",
		At:        now,
		Kind:      "started",
		Level:     "info",
		Message:   "Playback requested",
		Detail:    "Living Room TV · tvOS",
	})
	logger.Debug("routine chunk read", "session", "sess-ok", "offset", 0)
	logger.Info("playback delivery decision", "session", "sess-ok", "decision", "directplay")
	logger.Info("unrelated background task", "job", "cleanup")
	now = now.Add(5 * time.Second)
	if err := svc.Record(ctx, healthy, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.FlushTimeline(ctx); err != nil {
		t.Fatal(err)
	}

	tl, err := svc.PlayTimeline(ctx, "sess-ok")
	if err != nil {
		t.Fatal(err)
	}
	if tl.Failed || tl.Error != "" || len(tl.Entries) != 2 {
		t.Fatalf("healthy timeline = %+v, want 2 entries (event + info log, no debug)", tl)
	}
	if tl.Entries[0].Type != "event" || tl.Entries[1].Type != "log" || !strings.Contains(tl.Entries[1].Detail, "decision=directplay") {
		t.Fatalf("unexpected healthy entries: %+v", tl.Entries)
	}

	// 2. Failing session with encoder ID mapping, secret redaction, and buffered Debug promotion.
	now = now.Add(10 * time.Minute)
	failStart := now
	svc.ObserveTimeline(playback.TimelineEvent{
		SessionID: "sess-fail-1",
		At:        failStart,
		Kind:      "started",
		Level:     "info",
		Message:   "Playback requested",
	})
	sessLog := logger.With("session", "sess-fail-1")
	now = now.Add(200 * time.Millisecond)
	sessLog.Debug("probing candidate header", "release", "rel-a", "apikey", "topsecret-key")
	now = now.Add(400 * time.Millisecond)
	// Encoder log attributed to public encoder ID resolves to sess-fail-1 and triggers failing Debug capture.
	logger.Warn("source read failed", "session", "enc-public-1", "url", "https://news.example/get?X-Plex-Token=secret-plex-tok")
	now = now.Add(300 * time.Millisecond)
	sessLog.Debug("retrying backup provider", "provider", "spare")
	failedPlay := &core.PlaySession{
		ID:          "sess-fail-1",
		UserID:      7,
		ClientID:    "apple-tv",
		Key:         2002,
		Username:    "Sam",
		TitleName:   "Blade Runner 2049 (2017)",
		Client:      core.ClientCaps{ClientID: "apple-tv", DeviceName: "Living Room TV", Platform: "tvOS"},
		StartedAt:   failStart,
		BytesServed: 0,
		Experience: &core.PlayExperience{
			Requested:    true,
			RequestedAt:  failStart,
			StartTime:    -1,
			StartFailure: "every release failed (2 tried): open Blade.Runner.2049: missing article",
		},
	}
	if err := svc.Record(ctx, failedPlay, true, "every release failed (2 tried): open Blade.Runner.2049: missing article (apikey=rawsecret)"); err != nil {
		t.Fatal(err)
	}
	if err := svc.FlushTimeline(ctx); err != nil {
		t.Fatal(err)
	}

	// Zero-byte failed plays must resolve via PlayByID AND HTTP /plays/{id}/timeline.
	detail, err := svc.PlayByID(ctx, "sess-fail-1")
	if err != nil {
		t.Fatalf("PlayByID zero-byte failed play: %v", err)
	}
	gotFailed, _ := detail["failed"].(bool)
	gotErr, _ := detail["error"].(string)
	if !gotFailed || strings.Contains(gotErr, "rawsecret") || !strings.Contains(gotErr, "every release failed (2 tried)") {
		t.Fatalf("PlayByID detail = %+v", detail)
	}

	mux := svc.ActivityHandler()
	req := httptest.NewRequest(http.MethodGet, "/plays/sess-fail-1/timeline", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /plays/sess-fail-1/timeline = %d %s", rec.Code, rec.Body.String())
	}
	var failTL PlayTimeline
	if err := json.Unmarshal(rec.Body.Bytes(), &failTL); err != nil {
		t.Fatal(err)
	}
	if !failTL.Failed || !strings.Contains(failTL.Error, "every release failed (2 tried)") || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("failed timeline payload leaked secret or missed error: %s", rec.Body.String())
	}
	var sawBufferedDebug, sawAfterDebug, sawWarn bool
	for _, e := range failTL.Entries {
		if e.Level == "debug" && strings.Contains(e.Message, "probing candidate header") {
			sawBufferedDebug = true
		}
		if e.Level == "debug" && strings.Contains(e.Message, "retrying backup provider") {
			sawAfterDebug = true
		}
		if e.Level == "warn" && strings.Contains(e.Message, "source read failed") {
			sawWarn = true
		}
	}
	if !sawBufferedDebug || !sawWarn || !sawAfterDebug {
		t.Fatalf("expected buffered debug, warn, and post-failure debug entries; got %+v", failTL.Entries)
	}

	// 3. Coalesced viewing sessions re-key timeline rows onto the canonical stats_plays row.
	now = now.Add(10 * time.Minute)
	part1 := &core.PlaySession{ID: "coalesce-1", UserID: 9, ClientID: "shield", Key: 3003, Username: "Alex", TitleName: "Heat (1995)", StartedAt: now, BytesServed: 2 << 20}
	svc.ObserveTimeline(playback.TimelineEvent{SessionID: "coalesce-1", At: now, Kind: "started", Level: "info", Message: "Initial open"})
	if err := svc.Record(ctx, part1, true, ""); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	part2 := &core.PlaySession{ID: "coalesce-2", UserID: 9, ClientID: "shield", Key: 3003, Username: "Alex", TitleName: "Heat (1995)", StartedAt: now, BytesServed: 4 << 20}
	svc.ObserveTimeline(playback.TimelineEvent{SessionID: "coalesce-2", At: now, Kind: "quality", Level: "info", Message: "Reopened after quality change"})
	if err := svc.Record(ctx, part2, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.FlushTimeline(ctx); err != nil {
		t.Fatal(err)
	}
	coalescedTL, err := svc.PlayTimeline(ctx, "coalesce-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(coalescedTL.Entries) != 2 || coalescedTL.Entries[1].Message != "Reopened after quality change" {
		t.Fatalf("coalesced timeline = %+v", coalescedTL.Entries)
	}

	// 4. Cap at TimelineCap (300): keep opening 60 + newest 240.
	now = now.Add(10 * time.Minute)
	cappedPlay := &core.PlaySession{ID: "sess-cap", UserID: 7, ClientID: "web", Key: 4004, Username: "Sam", TitleName: "Dune (2021)", StartedAt: now, BytesServed: 8 << 20}
	if err := svc.Record(ctx, cappedPlay, true, ""); err != nil {
		t.Fatal(err)
	}
	for i := range 360 {
		svc.ObserveTimeline(playback.TimelineEvent{
			SessionID: "sess-cap",
			At:        now.Add(time.Duration(i) * 100 * time.Millisecond),
			Kind:      "candidate",
			Level:     "info",
			Message:   fmt.Sprintf("step-%03d", i),
		})
	}
	if err := svc.FlushTimeline(ctx); err != nil {
		t.Fatal(err)
	}
	capTL, err := svc.PlayTimeline(ctx, "sess-cap")
	if err != nil {
		t.Fatal(err)
	}
	if !capTL.Capped || len(capTL.Entries) != TimelineCap {
		t.Fatalf("capped timeline len=%d capped=%v, want %d and true", len(capTL.Entries), capTL.Capped, TimelineCap)
	}
	if capTL.Entries[0].Message != "step-000" || capTL.Entries[timelineKeepHead-1].Message != fmt.Sprintf("step-%03d", timelineKeepHead-1) {
		t.Fatalf("head not preserved: first=%q 60th=%q", capTL.Entries[0].Message, capTL.Entries[timelineKeepHead-1].Message)
	}
	if capTL.Entries[len(capTL.Entries)-1].Message != "step-359" {
		t.Fatalf("tail not preserved: last=%q", capTL.Entries[len(capTL.Entries)-1].Message)
	}

	// 5. Pre-existing play without timeline rows returns empty entries (200 OK), unknown play returns 404.
	if _, err := db.ExecContext(ctx, `INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated,ended,bytes,duration_ms,failovers,error,node_id,measured,played) VALUES('legacy-play',7,5005,'Sam','Alien (1979)','tvOS','Apple TV','directplay',1700000000,1700001000,1700001000,10485760,1000000,0,'','solo',0,1)`); err != nil {
		t.Fatal(err)
	}
	legacyRec := httptest.NewRecorder()
	mux.ServeHTTP(legacyRec, httptest.NewRequest(http.MethodGet, "/plays/legacy-play/timeline", nil))
	if legacyRec.Code != http.StatusOK {
		t.Fatalf("legacy play timeline status = %d", legacyRec.Code)
	}
	var legacyTL PlayTimeline
	if err := json.Unmarshal(legacyRec.Body.Bytes(), &legacyTL); err != nil {
		t.Fatal(err)
	}
	if len(legacyTL.Entries) != 0 || legacyTL.Failed {
		t.Fatalf("legacy play timeline = %+v, want empty entries", legacyTL)
	}
	missingRec := httptest.NewRecorder()
	mux.ServeHTTP(missingRec, httptest.NewRequest(http.MethodGet, "/plays/does-not-exist/timeline", nil))
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing play timeline status = %d, want 404", missingRec.Code)
	}

	// 6. Retention: Prune keeps timelines whose stats_plays row exists (even months later) and drops old orphans.
	svc.ObserveTimeline(playback.TimelineEvent{SessionID: "orphan-session", At: now, Kind: "started", Level: "info", Message: "Unclaimed prepare"})
	if err := svc.FlushTimeline(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(180 * 24 * time.Hour)
	if _, err := svc.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	afterPrune, err := svc.PlayTimeline(ctx, "sess-cap")
	if err != nil || len(afterPrune.Entries) != TimelineCap {
		t.Fatalf("Prune dropped retained play timeline: len=%d err=%v", len(afterPrune.Entries), err)
	}
	var orphanCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stats_play_timeline WHERE play_id='orphan-session'`).Scan(&orphanCount); err != nil {
		t.Fatal(err)
	}
	if orphanCount != 0 {
		t.Fatalf("orphan timeline rows remaining after Prune = %d, want 0", orphanCount)
	}
}
