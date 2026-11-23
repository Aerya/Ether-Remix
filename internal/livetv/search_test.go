package livetv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Search remains bounded over a large lineup; overlapping guides never duplicate
// identities and hidden-guide membership cannot bypass viewer access policy.
func TestLiveSearchAcrossGuidesScale(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s := testService(t, Options{Now: func() time.Time { return now }})
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 10000; n++ {
		id := fmt.Sprintf("c%05d", n)
		c := Channel{ID: id, Name: "Station " + id, Number: fmt.Sprint(n), Kind: "iptv", Enabled: true, SourceID: "provider", GuideID: id, Group: "News", StreamURL: "https://example.invalid/live"}
		if n == 9999 {
			c.Group = "Hidden"
		}
		b, _ := json.Marshal(c)
		if _, err = stmt.ExecContext(ctx, id, b); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	p := Programme{Start: now.Add(-time.Minute), End: now.Add(time.Hour), Item: Item{Title: "Morning Report", EpisodeTitle: "Satellite Launch", Summary: "A telescope explores the stars", Rating: "G"}}
	b, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, `INSERT INTO livetv_programmes(source_id,channel_id,start_ms,end_ms,data) VALUES(?,?,?,?,?)`, "provider", "c00042", p.Start.UnixMilli(), p.End.UnixMilli(), b); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveOwnerGroups(ctx, []OwnerGroup{{ID: "news", Name: "News", Groups: []OwnerGroupSource{{SourceID: "provider", Name: "News"}}}, {ID: "overlap", Name: "Overlap", Channels: []string{"c00042"}}, {ID: "hidden", Name: "Hidden", Hidden: true, Groups: []OwnerGroupSource{{SourceID: "provider", Name: "Hidden"}}}}); err != nil {
		t.Fatal(err)
	}
	host := liveTVHost{s: s}
	owner := &core.User{ID: 1, IsOwner: true}
	first, e := host.SearchLiveTV(ctx, owner, "42", 20)
	if e != nil || len(first.Channels) == 0 {
		t.Fatalf("first search fallback: %+v %v", first, e)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		hits, e := host.SearchLiveTV(ctx, owner, "c00042", 20)
		if e != nil {
			t.Fatal(e)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("index not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	virtual := testChannel()
	virtual.ID = "virtual-search"
	virtual.Name = "Virtual movies"
	virtual.SourceID = "provider"
	virtual.Group = "News"
	virtual.Number = "10001"
	virtual.Items[0].Title = "VirtualSearchUnique"
	if _, err = s.SaveChannel(ctx, virtual); err != nil {
		t.Fatal(err)
	}
	if err = s.refreshVirtualGuide(ctx); err != nil {
		t.Fatal(err)
	}
	builds := s.virtualGuide.builds.Load()
	vh, e := host.SearchLiveTV(ctx, owner, "VirtualSearchUnique", 5)
	if e != nil || len(vh.Programmes) == 0 || vh.Programmes[0].ChannelID != virtual.ID {
		t.Fatalf("snapshot programme search: %+v %v", vh, e)
	}
	if s.virtualGuide.builds.Load() != builds {
		t.Fatal("search computed an extra schedule")
	}

	for _, query := range []string{"c00042", "42"} {
		hits, e := host.SearchLiveTV(ctx, owner, query, 20)
		if e != nil || len(hits.Channels) == 0 || hits.Channels[0].ID != "c00042" {
			t.Fatalf("channel %q: %+v %v", query, hits, e)
		}
	}
	for _, query := range []string{"Morning Report", "Satellite", "telescope"} {
		hits, e := host.SearchLiveTV(ctx, owner, query, 20)
		if e != nil || len(hits.Programmes) != 1 || len(hits.ProgrammeChannels) != 1 || hits.Programmes[0].ChannelID != "c00042" {
			t.Fatalf("programme %q: %+v %v", query, hits, e)
		}
	}
	hits, err := host.SearchLiveTV(ctx, owner, "c09999", 20)
	if err != nil || len(hits.Channels) != 0 {
		t.Fatalf("hidden: %+v %v", hits, err)
	}
	if err = s.SavePolicy(ctx, Policy{UserID: 2, Restricted: true, Channels: []string{"c00043"}}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"c00042", "Satellite"} {
		hits, e := host.SearchLiveTV(ctx, &core.User{ID: 2}, query, 20)
		if e != nil || len(hits.Channels)+len(hits.Programmes) != 0 {
			t.Fatalf("policy: %+v %v", hits, e)
		}
	}
	private := p
	private.ID = "personal-2-c00043"
	private.ChannelID = "c00043"
	private.Item.Title = "PrivateRundownNeedle"
	tx, err = s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeSearchProgramme(ctx, tx, 2, private.ChannelID, private); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []*core.User{owner, {ID: 2}} {
		hits, e := host.SearchLiveTV(ctx, viewer, "PrivateRundownNeedle", 10)
		want := 0
		if viewer.ID == 2 {
			want = 1
		}
		if e != nil || len(hits.Programmes) != want {
			t.Fatalf("personal programme isolation viewer=%d: %+v %v", viewer.ID, hits, e)
		}
	}
	hits, err = host.SearchLiveTV(ctx, owner, "Station", 7)
	if err != nil || len(hits.Channels) != 7 {
		t.Fatalf("bound: %+v %v", hits, err)
	}
	if store.DialectOf(s.o.DB) == store.Postgres {
		tx, err := s.o.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// Ask whether the production expression can use its GIN index even
		// when this small fixture would make a sequential scan cheaper.
		if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan=off`); err != nil {
			t.Fatal(err)
		}
		var plan string
		if err := tx.QueryRowContext(ctx, `EXPLAIN (FORMAT JSON) SELECT id FROM livetv_channels WHERE to_tsvector('simple', livetv_channel_search_text(data::jsonb->>'name', data::jsonb->>'number', data::jsonb->>'guideId')) @@ to_tsquery('simple','c00042:*') ORDER BY id LIMIT 400`).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, `"Index Name": "livetv_search_channels"`) {
			t.Fatalf("search lost its GIN index: %s", plan)
		}
		return
	}

	rows, err := s.o.DB.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM livetv_search_channels WHERE livetv_search_channels MATCH 'station*' ORDER BY rank LIMIT 400`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, "VIRTUAL TABLE INDEX")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("search lost its FTS index")
	}
}

// Space- and punctuation-normalised channel search stays indexed across
// word-prefix and compact queries (both after backfill and via live triggers).
func TestLiveSearchSpaceInsensitiveAndIndexed(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s := testService(t, Options{Now: func() time.Time { return now }})
	owner := &core.User{ID: 1, IsOwner: true}

	seed := []Channel{
		{ID: "nfl-redzone", Name: "NFL RedZone", Number: "701", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "nfl.rz", Group: "Sports", StreamURL: "https://example.invalid/rz"},
		{ID: "fox-5", Name: "FOX 5", Number: "5", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "fox5.us", Group: "Local", StreamURL: "https://example.invalid/fox5"},
		{ID: "nick-jr", Name: "Nick Jr.", Number: "302", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "nickjr.us", Group: "Kids", StreamURL: "https://example.invalid/nickjr"},
		{ID: "espn-2", Name: "ESPN 2", Number: "209", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "espn2.us", Group: "Sports", StreamURL: "https://example.invalid/espn2"},
		{ID: "espn2", Name: "ESPN2", Number: "210", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "espn2hd.us", Group: "Sports", StreamURL: "https://example.invalid/espn2b"},
		{ID: "a-and-e", Name: "A&E", Number: "118", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "ae.us", Group: "Entertainment", StreamURL: "https://example.invalid/ae"},
		{ID: "hbo", Name: "HBO", Number: "501", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "hbo.us", Group: "Movies", StreamURL: "https://example.invalid/hbo"},
	}
	for _, c := range seed {
		if _, err := s.SaveChannel(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	host := liveTVHost{s: s}
	for deadline := time.Now().Add(5 * time.Second); ; {
		hits, err := host.SearchLiveTV(ctx, owner, "redzone", 20)
		if err != nil {
			t.Fatal(err)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("index not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Also verify live trigger indexing after the index is active.
	if _, err := s.SaveChannel(ctx, Channel{ID: "us-fox-5", Name: "US | FOX 5 HD", Number: "505", Kind: "iptv", Enabled: true, SourceID: "p", GuideID: "us.fox5", Group: "Local", StreamURL: "https://example.invalid/usfox5"}); err != nil {
		t.Fatal(err)
	}

	assertContains := func(query string, wantIDs []string, forbidID string) {
		t.Helper()
		hits, err := host.SearchLiveTV(ctx, owner, query, 20)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		got := map[string]bool{}
		for _, c := range hits.Channels {
			got[c.ID] = true
		}
		for _, id := range wantIDs {
			if !got[id] {
				t.Fatalf("query %q missing %q in %+v", query, id, hits.Channels)
			}
		}
		if forbidID != "" && got[forbidID] {
			t.Fatalf("query %q unexpectedly matched %q: %+v", query, forbidID, hits.Channels)
		}
	}

	for _, q := range []string{"red zone", "redzone", "redz", "nfl red", "nfl red zone", "nflredzone", "red z"} {
		assertContains(q, []string{"nfl-redzone"}, "hbo")
	}
	assertContains("fox5", []string{"fox-5", "us-fox-5"}, "hbo")
	assertContains("fox 5", []string{"fox-5", "us-fox-5"}, "hbo")
	assertContains("nickjr", []string{"nick-jr"}, "hbo")
	assertContains("nick jr", []string{"nick-jr"}, "hbo")
	assertContains("espn2", []string{"espn-2", "espn2"}, "hbo")
	assertContains("espn 2", []string{"espn-2", "espn2"}, "hbo")
	assertContains("ae", []string{"a-and-e"}, "hbo")

	if store.DialectOf(s.o.DB) != store.Postgres {
		checkPlan := func(q, want string, args ...any) {
			t.Helper()
			rows, err := s.o.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			matched := false
			for rows.Next() {
				var a, b, c int
				var detail string
				if err := rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
				if strings.Contains(detail, want) {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("plan for %q missing %q: %v", q, want, details)
			}
		}
		checkPlan(`SELECT id FROM livetv_search_channels_compact WHERE livetv_search_channels_compact MATCH ? ORDER BY rank LIMIT 400`, "VIRTUAL TABLE INDEX", `"redzone"`)
		checkPlan(`SELECT id FROM livetv_channels WHERE `+sqliteCompactNameExpr+`>=? AND `+sqliteCompactNameExpr+`<? LIMIT 400`, "livetv_search_compact_fallback", "ae", "ae\uffff")
	}
}

// A logical backup crosses engines without exporting SQLite docids or claiming
// an absent FTS cache is ready. Restoring leaves cache work until search demand.
func TestLiveSearchRestoreDemand(t *testing.T) {
	ctx := t.Context()
	db, err := store.OpenPath(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := testService(t, Options{DB: db})
	c := Channel{ID: "restore-search", Name: "RestoreSearchNeedle", Number: "1", Kind: "iptv", Enabled: true, StreamURL: "https://example.invalid/live"}
	if _, err := source.SaveChannel(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := source.buildSearchIndex(ctx, true); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := store.ExportLogical(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"livetv_search_state", "livetv_search_channel_keys", "livetv_search_programme_keys"} {
		if bytes.Contains(dump.Bytes(), []byte(`"table":"`+table+`"`)) {
			t.Fatalf("dump contains cache %s", table)
		}
	}
	target := testService(t, Options{})
	if err := store.ImportLogical(ctx, target.o.DB, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var active, ready int
	if err := target.o.DB.QueryRowContext(ctx, `SELECT active,ready FROM livetv_search_state WHERE id=1`).Scan(&active, &ready); err != nil {
		t.Fatal(err)
	}
	wantActive := 0
	if store.DialectOf(target.o.DB) == store.Postgres {
		wantActive = 1
	}
	if active != wantActive || ready != 0 {
		t.Fatalf("restored search state: %d/%d, want %d/0", active, ready, wantActive)
	}
	if store.DialectOf(target.o.DB) != store.Postgres {
		var count int
		if err := target.o.DB.QueryRowContext(ctx, `SELECT count(*) FROM livetv_search_channel_keys`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("restore eagerly indexed %d channels", count)
		}
	}
	host := liveTVHost{s: target}
	for deadline := time.Now().Add(5 * time.Second); ; {
		hits, err := host.SearchLiveTV(ctx, &core.User{ID: 1, IsOwner: true}, "RestoreSearchNeedle", 5)
		if err != nil {
			t.Fatal(err)
		}
		if !hits.Partial {
			if len(hits.Channels) != 1 || hits.Channels[0].ID != c.ID {
				t.Fatalf("restored search: %+v", hits)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restored search index not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
