package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestGuideWindowDense128x3h(t *testing.T) {
	ctx := context.Background()
	now := guideTestNow.UTC().Truncate(time.Hour)
	s := testService(t, Options{Now: func() time.Time { return now }})

	const (
		totalChannels = 300
		requestCount  = 128
		slotMinutes   = 10
		slotsPerChan  = (48 * 60) / slotMinutes // 48h of 10m dense slots = 288 slots/channel (86,400 stored rows)
	)

	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	src := Source{
		ID:          "dense-src",
		Name:        "Dense Guide",
		PlaylistURL: "https://provider.invalid/playlist.m3U",
		GuideURL:    "https://provider.invalid/guide.xml",
		UpdatedAt:   now.Add(-time.Hour),
	}
	srcBytes, err := s.encodeSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO livetv_sources(id,data) VALUES(?,?)`, src.ID, srcBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO livetv_source_refresh(id,updated_ms) VALUES(?,?)`, src.ID, src.UpdatedAt.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	chStmt, err := tx.Prepare(`INSERT INTO livetv_channels(id,data) VALUES(?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer chStmt.Close()
	gcStmt, err := tx.Prepare(`INSERT INTO livetv_guide_channels(source_id,guide_id,name,logo,names) VALUES(?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer gcStmt.Close()
	prStmt, err := tx.Prepare(`INSERT INTO livetv_programmes(source_id,channel_id,start_ms,end_ms,data) VALUES(?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer prStmt.Close()

	// Shift slot boundaries by 5 minutes so every channel also has programmes crossing both window edges.
	origin := now.Add(-12*time.Hour - 5*time.Minute)
	slotDur := slotMinutes * time.Minute
	payloads := make([][]byte, slotsPerChan)
	starts := make([]time.Time, slotsPerChan)
	for j := range payloads {
		st := origin.Add(time.Duration(j) * slotDur)
		starts[j] = st
		payloads[j], err = json.Marshal(Programme{
			Start: st,
			End:   st.Add(slotDur),
			Item: Item{
				Title:   fmt.Sprintf("Slot %03d", j),
				Summary: "Dense listing slot",
				Rating:  "TV-PG",
				Genres:  []string{"News"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	allIDs := make([]string, totalChannels)
	for i := range allIDs {
		id := fmt.Sprintf("dense-%04d", i)
		allIDs[i] = id
		c := Channel{
			ID:       id,
			Number:   fmt.Sprint(i + 1),
			Name:     fmt.Sprintf("Channel %d", i+1),
			Kind:     "iptv",
			Enabled:  true,
			SourceID: src.ID,
			GuideID:  id,
		}
		b, _ := json.Marshal(c)
		if _, err = chStmt.Exec(id, b); err != nil {
			t.Fatal(err)
		}
		if _, err = gcStmt.Exec(src.ID, id, c.Name, "", "[]"); err != nil {
			t.Fatal(err)
		}
		for j, b := range payloads {
			st := starts[j]
			if _, err = prStmt.Exec(src.ID, id, st.UnixMilli(), st.Add(slotDur).UnixMilli(), b); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Reconcile channel existence once after direct SQL fixture setup.
	if _, err = s.Channels(ctx); err != nil {
		t.Fatal(err)
	}

	reqIDs := allIDs[:requestCount]
	from := now
	until := now.Add(3 * time.Hour)

	statsCtx, stats := store.WithQueryStats(ctx)
	started := time.Now()
	got, err := s.GuideWindowForChannels(statsCtx, ownerUser(), from, until, reqIDs)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("GuideWindowForChannels failed: %v", err)
	}
	if len(got.Channels) != requestCount {
		t.Fatalf("got %d channels, want %d", len(got.Channels), requestCount)
	}
	// With 10-minute slots shifted by -5m, [00:00, 03:00) overlaps 19 slots per channel (2,432 programmes).
	const wantPerChannel = 19
	totalReturned := 0
	for i, row := range got.Channels {
		if row.ChannelID != reqIDs[i] || row.State != GuideWindowReady || len(row.Programmes) != wantPerChannel {
			t.Fatalf("row %d: id=%s state=%s len=%d (want id=%s ready len=%d)", i, row.ChannelID, row.State, len(row.Programmes), reqIDs[i], wantPerChannel)
		}
		if !row.Programmes[0].Start.Before(from) || !row.Programmes[0].End.After(from) {
			t.Fatalf("row %d first programme does not cross left edge: %s..%s", i, row.Programmes[0].Start, row.Programmes[0].End)
		}
		last := row.Programmes[len(row.Programmes)-1]
		if !last.Start.Before(until) || !last.End.After(until) {
			t.Fatalf("row %d last programme does not cross right edge: %s..%s", i, last.Start, last.End)
		}
		totalReturned += len(row.Programmes)
	}
	// Context queries: 1 withPolicy + 1 channelsByIDs + 1 loadGuideWindowSources + 1 loadGuideWindowChannelMeta + 1 programmes overlap = 5.
	if stats.Queries() > 6 {
		t.Fatalf("expected O(1) batched queries (<=6), got %d queries", stats.Queries())
	}
	// Rows read must stay proportional to requested channels + returned programmes, never total stored programmes (86,400).
	maxExpectedRows := int64(requestCount*3 + totalReturned + 10)
	if stats.Rows() > maxExpectedRows {
		t.Fatalf("unbounded row scan: read %d rows (max expected %d out of %d stored)", stats.Rows(), maxExpectedRows, totalChannels*slotsPerChan)
	}
	t.Logf("128ch x 3h dense window: stored_programmes=%d returned_programmes=%d queries=%d db_rows=%d elapsed=%s",
		totalChannels*slotsPerChan, totalReturned, stats.Queries(), stats.Rows(), elapsed)

	if store.DialectOf(s.o.DB) != store.SQLite {
		return
	}
	plans := []string{
		`WITH ch(source_id, channel_id) AS (VALUES ('dense-src','dense-0000'),('dense-src','dense-0001'))
SELECT ch.source_id, ch.channel_id,
	EXISTS(SELECT 1 FROM livetv_guide_channels g WHERE g.source_id=ch.source_id AND g.guide_id=ch.channel_id),
	(SELECT MAX(p.end_ms) FROM livetv_programmes p WHERE p.source_id=ch.source_id AND p.channel_id=ch.channel_id)
FROM ch`,
		`SELECT source_id, channel_id, data FROM livetv_programmes WHERE (source_id,channel_id) IN (('dense-src','dense-0000'),('dense-src','dense-0001')) AND end_ms>0 AND start_ms<1`,
	}
	for _, q := range plans {
		rows, err := s.o.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q)
		if err != nil {
			t.Fatal(err)
		}
		foundSearch := false
		for rows.Next() {
			var a, b, c int
			var detail string
			if err = rows.Scan(&a, &b, &c, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN livetv_programmes") {
				rows.Close()
				t.Fatalf("unbounded table scan on livetv_programmes: %s", detail)
			}
			if strings.Contains(detail, "SEARCH") && strings.Contains(detail, "livetv_programmes") {
				foundSearch = true
			}
		}
		rows.Close()
		if !foundSearch {
			t.Fatalf("missing indexed search on livetv_programmes for query: %s", q)
		}
	}
}

func TestGuideWindowSemantics(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 18, 0, 0, 0, time.UTC)
	s := testService(t, Options{Now: func() time.Time { return now }})

	from := now
	until := now.Add(3 * time.Hour)
	fmtXML := func(tm time.Time) string { return tm.UTC().Format("20060102150405 -0700") }

	// Source 1: healthy playlist + XMLTV with:
	// - "edge": programmes before from, crossing left edge, inside, crossing right edge, and starting at until
	// - "empty": declared <channel> with no programmes in [from, until)
	// - "secret": declared <channel> with listings, but denied to viewer 42
	xml1 := `<tv>` +
		`<channel id="edge"><display-name>Edge TV</display-name></channel>` +
		`<channel id="empty"><display-name>Quiet TV</display-name></channel>` +
		`<channel id="secret"><display-name>Secret TV</display-name></channel>` +
		`<programme channel="edge" start="` + fmtXML(from.Add(-2*time.Hour)) + `" stop="` + fmtXML(from) + `"><title>Ended At From</title></programme>` +
		`<programme channel="edge" start="` + fmtXML(from.Add(-30*time.Minute)) + `" stop="` + fmtXML(from.Add(30*time.Minute)) + `"><title>Crosses Start</title></programme>` +
		`<programme channel="edge" start="` + fmtXML(from.Add(30*time.Minute)) + `" stop="` + fmtXML(until.Add(-30*time.Minute)) + `"><title>Middle Show</title></programme>` +
		`<programme channel="edge" start="` + fmtXML(until.Add(-30*time.Minute)) + `" stop="` + fmtXML(until.Add(45*time.Minute)) + `"><title>Crosses End</title></programme>` +
		`<programme channel="edge" start="` + fmtXML(until) + `" stop="` + fmtXML(until.Add(time.Hour)) + `"><title>Starts At Until</title></programme>` +
		`<programme channel="edge" start="` + fmtXML(until.Add(10*24*time.Hour)) + `" stop="` + fmtXML(until.Add(10*24*time.Hour+time.Hour)) + `"><title>Far Horizon</title></programme>` +
		`<programme channel="empty" start="` + fmtXML(until.Add(10*24*time.Hour)) + `" stop="` + fmtXML(until.Add(10*24*time.Hour+time.Hour)) + `"><title>Later</title></programme>` +
		`<programme channel="secret" start="` + fmtXML(from) + `" stop="` + fmtXML(until) + `"><title>Top Secret</title></programme>` +
		`</tv>`
	m3u1 := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"edge\",Edge TV\nhttps://example.invalid/edge.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"empty\",Quiet TV\nhttps://example.invalid/empty.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"secret\",Secret TV\nhttps://example.invalid/secret.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"unmapped\",Unmapped TV\nhttps://example.invalid/unmapped.m3u8\n"
	if _, err := s.ImportSource(ctx, Source{
		ID:           "src-ok",
		Name:         "Healthy",
		Playlist:     m3u1,
		Guide:        xml1,
		RefreshHours: 6,
	}); err != nil {
		t.Fatal(err)
	}
	// Mark stored source as URL-backed so nextRefresh = UpdatedAt + 6h.
	patchSourceJSON(t, s, "src-ok", map[string]any{"guideUrl": "https://example.invalid/guide.xml"})

	// Source 2: M3U-only source with tvg-id but NO guide configured.
	if _, err := s.ImportSource(ctx, Source{
		ID:       "src-noguide",
		Name:     "Playlist Only",
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"raw-id\",No Guide TV\nhttps://example.invalid/noguide.m3u8\n",
	}); err != nil {
		t.Fatal(err)
	}

	// Source 3: Source whose latest refresh failed.
	if _, err := s.ImportSource(ctx, Source{
		ID:       "src-failed",
		Name:     "Broken Guide",
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"broken\",Broken TV\nhttps://example.invalid/broken.m3u8\n",
		Guide:    `<tv><channel id="broken"><display-name>Broken TV</display-name></channel><programme channel="broken" start="` + fmtXML(from) + `" stop="` + fmtXML(until) + `"><title>Stale Listing</title></programme></tv>`,
	}); err != nil {
		t.Fatal(err)
	}
	failedAt := now.Add(time.Minute).UTC().Format(time.RFC3339Nano)
	patchSourceJSON(t, s, "src-failed", map[string]any{"lastError": "upstream 502", "lastErrorAt": failedAt, "refreshFailures": 1})

	cs, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, c := range cs {
		byName[c.Name] = c.ID
	}

	viewer := &core.User{ID: 42, Username: "viewer"}
	if err := s.SetChannelViewers(ctx, byName["Secret TV"], ChannelViewers{Deny: []int64{viewer.ID}}); err != nil {
		t.Fatal(err)
	}

	// 1. All-ready past window: validUntil MUST be next refresh (now + 6h), NOT capped by until!
	pastFrom := now.Add(-3 * time.Hour)
	pastUntil := now.Add(-1 * time.Hour)
	allReady, err := s.GuideWindowForChannels(ctx, viewer, pastFrom, pastUntil, []string{byName["Quiet TV"], byName["Edge TV"]})
	if err != nil {
		t.Fatal(err)
	}
	wantRefresh := now.Add(6 * time.Hour)
	if !allReady.ValidUntil.Equal(wantRefresh) {
		t.Fatalf("past window validUntil = %s, want next refresh %s (must not cap by until %s)", allReady.ValidUntil, wantRefresh, pastUntil)
	}

	// 2. Mixed batch in arbitrary caller order:
	order := []string{
		byName["Quiet TV"],
		"unknown-channel",
		byName["Edge TV"],
		byName["Secret TV"],
		byName["Unmapped TV"],
		byName["No Guide TV"],
		byName["Broken TV"],
	}
	mixed, err := s.GuideWindowForChannels(ctx, viewer, from, until, order)
	if err != nil {
		t.Fatal(err)
	}
	if len(mixed.Channels) != len(order) {
		t.Fatalf("channels len = %d, want %d", len(mixed.Channels), len(order))
	}
	for i, wantID := range order {
		if mixed.Channels[i].ChannelID != wantID {
			t.Fatalf("entry %d channelId = %q, want %q", i, mixed.Channels[i].ChannelID, wantID)
		}
	}
	// Quiet TV: ready with []
	if mixed.Channels[0].State != GuideWindowReady || len(mixed.Channels[0].Programmes) != 0 || mixed.Channels[0].Programmes == nil {
		t.Fatalf("Quiet TV = %+v, want ready with non-nil []", mixed.Channels[0])
	}
	// Edge TV: ready with 3 programmes (Crosses Start, Middle Show, Crosses End) with original timestamps & IDs
	edge := mixed.Channels[2]
	if edge.State != GuideWindowReady || len(edge.Programmes) != 3 {
		t.Fatalf("Edge TV = %+v, want ready with 3 programmes", edge)
	}
	wantTitles := []string{"Crosses Start", "Middle Show", "Crosses End"}
	wantStarts := []time.Time{from.Add(-30 * time.Minute), from.Add(30 * time.Minute), until.Add(-30 * time.Minute)}
	wantEnds := []time.Time{from.Add(30 * time.Minute), until.Add(-30 * time.Minute), until.Add(45 * time.Minute)}
	for j := range wantTitles {
		p := edge.Programmes[j]
		wantID := fmt.Sprintf("%s-%d", byName["Edge TV"], wantStarts[j].UnixMilli())
		if p.Item.Title != wantTitles[j] || !p.Start.Equal(wantStarts[j]) || !p.End.Equal(wantEnds[j]) || p.ID != wantID {
			t.Fatalf("Edge TV programme %d = %+v, want title=%q start=%s end=%s id=%s", j, p, wantTitles[j], wantStarts[j], wantEnds[j], wantID)
		}
	}
	// All remaining channels (unknown, viewer-denied, unmapped, playlist-only, failed-refresh) must be unavailable with [].
	for _, idx := range []int{1, 3, 4, 5, 6} {
		row := mixed.Channels[idx]
		if row.State != GuideWindowUnavailable || len(row.Programmes) != 0 || row.Programmes == nil {
			t.Fatalf("index %d (%s) = %+v, want unavailable with []", idx, row.ChannelID, row)
		}
	}
	// Since at least one row is unavailable, validUntil MUST be capped at now + 60s.
	wantRetryCap := now.Add(60 * time.Second)
	if !mixed.ValidUntil.Equal(wantRetryCap) {
		t.Fatalf("mixed window validUntil = %s, want %s (now+60s cap)", mixed.ValidUntil, wantRetryCap)
	}
}

func TestGuideWindowTooDense(t *testing.T) {
	ctx := context.Background()
	now := guideTestNow.UTC().Truncate(time.Hour)
	s := testService(t, Options{Now: func() time.Time { return now }})

	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	const (
		channels = 25
		slots    = 850 // 25 * 850 = 21,250 programmes (> 20,000)
	)
	chStmt, err := tx.Prepare(`INSERT INTO livetv_channels(id,data) VALUES(?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer chStmt.Close()
	prStmt, err := tx.Prepare(`INSERT INTO livetv_programmes(source_id,channel_id,start_ms,end_ms,data) VALUES(?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer prStmt.Close()

	step := (3 * time.Hour) / slots
	ids := make([]string, channels)
	payloads := make([][]byte, slots)
	for j := range slots {
		st := now.Add(time.Duration(j) * step)
		payloads[j], _ = json.Marshal(Programme{Start: st, End: st.Add(step), Item: Item{Title: "Clip"}})
	}
	for i := range ids {
		id := fmt.Sprintf("dense-over-%02d", i)
		ids[i] = id
		b, _ := json.Marshal(Channel{ID: id, Number: fmt.Sprint(i + 1), Name: id, Kind: "iptv", Enabled: true, SourceID: "over", GuideID: id})
		if _, err = chStmt.Exec(id, b); err != nil {
			t.Fatal(err)
		}
		for j, pb := range payloads {
			st := now.Add(time.Duration(j) * step)
			if _, err = prStmt.Exec("over", id, st.UnixMilli(), st.Add(step).UnixMilli(), pb); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Channels(ctx); err != nil {
		t.Fatal(err)
	}

	_, err = s.GuideWindowForChannels(ctx, ownerUser(), now, now.Add(3*time.Hour), ids)
	var dense *frontend.LiveGuideWindowTooDenseError
	if !errors.As(err, &dense) {
		t.Fatalf("expected LiveGuideWindowTooDenseError, got %v", err)
	}
	if dense.MaxChannels < 1 || dense.MaxChannels >= channels || dense.MaxWindowMinutes < 1 || dense.MaxWindowMinutes >= 180 {
		t.Fatalf("unexpected suggest bounds: %+v", dense)
	}
}

// patchSourceJSON sets fields of a stored source in Go, so the test runs on
// SQLite and PostgreSQL alike.
func patchSourceJSON(t *testing.T, s *Service, id string, fields map[string]any) {
	t.Helper()
	ctx := context.Background()
	var stored string
	if err := s.o.DB.QueryRowContext(ctx, `SELECT data FROM livetv_sources WHERE id=?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stored), &doc); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		doc[k] = v
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.o.DB.ExecContext(ctx, `UPDATE livetv_sources SET data=? WHERE id=?`, string(b), id); err != nil {
		t.Fatal(err)
	}
}
