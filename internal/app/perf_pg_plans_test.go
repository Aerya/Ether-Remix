package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/perfharness"
	"github.com/braedonsaunders/ether/internal/store"
)

// The library-scale gate runs on SQLite. PostgreSQL plans the same SQL
// differently, and a plan that is an index seek on SQLite can be a scan of
// the whole catalogue there. TestPostgresHotPlans replays the hot client
// paths (Home, Continue Watching and On Deck, section pages sorted and
// filtered, search, detail, multi-key metadata detail batches, episode release
// joins and counts, and restricted remote artwork lookups) against a small
// library on PostgreSQL, with the catalogue's planner statistics scaled to a
// full TMDB mirror, and fails on any plan that would read a catalogue or watch
// table whole: a sequential scan, a bitmap heap scan without an index
// condition, or a hashed subplan over one without an index condition. It runs
// in the PostgreSQL CI job (ETHER_TEST_POSTGRES) in about a minute.

// plansClients are the replayed paths: Plex for Apple TV's Home, section
// pages, filters, search and detail, and Infuse's resume and next-up.
var plansSteps = map[string][]string{
	"plex-appletv": {"/library/sections", "/hubs/continueWatching", "/library/onDeck", "/hubs", "/hubs/sections/movies", "movies all titleSort p1", "movies all titleSort deep", "movies all addedAt", "movies all rating",
		"movies all popularity", "popular movie detail", "movies filter genre", "movies filter genre deep", "movies filter year", "movies filter decade", "movies filter actor", "movies filter director", "movies filter country", "movies filter studio", "movies filter unwatched",
		"movies jump bar", "movies genre filter list", "shows all titleSort p1", "shows allLeaves p1", "show detail", "show children", "season children", "episode detail", "movie detail",
		"/hubs/search star", "/library/search star", "person media", "shows onDeck", "shows filter addedAt before", "/hubs/promoted"},
	"infuse-jellyfin": {"/UserItems/Resume", "/Shows/NextUp", "/UserViews", "movies p1 SortName", "movies p1 Genres=Drama", "movies deep Genres=Drama", "movies p1 Years", "search items star"},
}

// plansExtraPlexSteps adds the #598 episode release browse/shelf and multi-key
// metadata detail batch paths to the unrestricted Plex client replay.
func plansExtraPlexSteps() []perfharness.Step {
	return []perfharness.Step{
		{Name: "shows episodes releasedAt desc", Class: "browse", Path: "/library/sections/{showSection}/all?type=4&sort=originallyAvailableAt:desc&includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=60"},
		{Name: "shows episodes addedAt desc", Class: "browse", Path: "/library/sections/{showSection}/all?type=4&sort=addedAt:desc&includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=60"},
		{Name: "/library/recentlyAdded", Class: "home", Path: "/library/recentlyAdded?X-Plex-Container-Start=0&X-Plex-Container-Size=50"},
		{Name: "/hubs/home/recentlyAdded tv", Class: "home", Path: "/hubs/home/recentlyAdded?type=2&X-Plex-Container-Start=0&X-Plex-Container-Size=20"},
		{Name: "metadata detail batch", Class: "detail", Path: "/library/metadata/{batchMetadataKeys}?includeExtras=1&includeRelated=1&includeOnDeck=1&includeChapters=1&includeGuids=1&includeMarkers=1&includePreferences=1&includeReviews=1&asyncAugmentMetadata=1"},
	}
}

// plansRestrictedSteps replays the #598 paths under the restricted fixture
// viewer (rating cap + share filter): remote artwork lookup, episode release
// join and count, recentlyAdded, and multi-key metadata detail batch.
func plansRestrictedSteps(base []perfharness.Step) []perfharness.Step {
	want := map[string]int{
		"/library/sections":       0,
		"movies all titleSort p1": 4,
		"shows all titleSort p1":  4,
		"shows allLeaves p1":      6,
		"show children":           2,
	}
	var out []perfharness.Step
	for _, st := range base {
		limit, ok := want[st.Name]
		if !ok {
			continue
		}
		if limit > 0 {
			st.Discover = plansChainDiscover(st.Discover, plansDiscoverBatchKeys(limit))
		}
		out = append(out, st)
	}
	return append(out,
		perfharness.Step{Name: "restricted artwork lookup", Class: "playback", Path: "/photo/:/transcode?width=300&height=450&url=https%3A%2F%2Fimage.tmdb.org%2Ft%2Fp%2Fw500%2Frestricted-probe.jpg"},
		perfharness.Step{Name: "restricted shows episodes releasedAt desc", Class: "browse", Path: "/library/sections/{showSection}/all?type=4&sort=originallyAvailableAt:desc&includeGuids=1&X-Plex-Container-Start=0&X-Plex-Container-Size=60"},
		perfharness.Step{Name: "restricted recentlyAdded", Class: "home", Path: "/library/recentlyAdded?X-Plex-Container-Start=0&X-Plex-Container-Size=50"},
		perfharness.Step{Name: "restricted metadata detail batch", Class: "detail", Path: "/library/metadata/{batchMetadataKeys}?includeExtras=1&includeRelated=1&includeOnDeck=1&includeChapters=1&includeGuids=1&includeMarkers=1&includePreferences=1&includeReviews=1&asyncAugmentMetadata=1"},
	)
}

func plansChainDiscover(a, b func(map[string]string, []byte)) func(map[string]string, []byte) {
	return func(vars map[string]string, body []byte) {
		if a != nil {
			a(vars, body)
		}
		if b != nil {
			b(vars, body)
		}
	}
}

func plansDiscoverBatchKeys(limit int) func(map[string]string, []byte) {
	return func(vars map[string]string, body []byte) {
		var c struct {
			MediaContainer struct {
				Metadata []struct {
					RatingKey string `json:"ratingKey"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if json.Unmarshal(body, &c) != nil {
			return
		}
		seen := map[string]bool{}
		var keys []string
		if cur := vars["batchMetadataKeys"]; cur != "" {
			for _, k := range strings.Split(cur, ",") {
				if k != "" && !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
		added := 0
		for _, m := range c.MediaContainer.Metadata {
			if m.RatingKey == "" || seen[m.RatingKey] {
				continue
			}
			seen[m.RatingKey] = true
			keys = append(keys, m.RatingKey)
			added++
			if added >= limit {
				break
			}
		}
		if len(keys) > 0 {
			vars["batchMetadataKeys"] = strings.Join(keys, ",")
		}
	}
}

// plansFullMirror is the catalogue size the statistics are scaled to.
const plansFullMirror = 1_300_000

// plansHousehold is the watch history the state tables are scaled to: a
// household of a few heavy viewers.
const plansHousehold = 200_000

// plansWatched are the tables a plan must never read whole.
var plansWatched = regexp.MustCompile(`^(catalog_(titles|credits|genres|keywords|studios|countries|alt_titles|genre_pairs|person_names|people|crowd|home_release|home_release_dates|release_names|studio_names)|state_(watch|history|history_events|history_rank|playlist_items))$`)

type planTracedStmt struct {
	sites map[string]bool
	steps map[string]bool
}

func TestPostgresHotPlans(t *testing.T) {
	pgURL := os.Getenv("ETHER_TEST_POSTGRES")
	if pgURL == "" {
		t.Skip("set ETHER_TEST_POSTGRES to check PostgreSQL plans of the hot paths")
	}
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ports := freePorts(t, 3)
	if err = cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Frontends = map[string]config.Frontend{"plex": {Enabled: true}, "jellyfin": {Enabled: true}}
		c.FrontendPorts = map[string]int{"jellyfin": ports[0], "emby": ports[1], "silo": ports[2]}
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Server.BindAddress = "127.0.0.1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	scale := perfharness.Scale{Movies: 2000, Shows: 100, SeasonsPerShow: 2, EpisodesPerSeason: 4, Viewers: 2, HistoryPerViewer: 1500, InProgressPerViewer: 20}
	if err := perfharness.Generate(ctx, db, scale, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Seed restricted-only artwork on two TV-MA shows so a restricted viewer's
	// /photo/:/transcode check runs artworkPolicyLookupSQL and PreloadStored
	// without dialling an external image host.
	if _, err := db.ExecContext(ctx, `UPDATE catalog_titles SET poster='/restricted-probe.jpg' WHERE key IN (SELECT key FROM catalog_titles WHERE type='show' AND content_rating='TV-MA' LIMIT 2)`); err != nil {
		t.Fatal(err)
	}
	viewers, err := perfharness.MintCredentials(ctx, db, 3)
	if err != nil {
		t.Fatal(err)
	}
	var restricted []perfharness.Viewer
	for _, v := range viewers {
		if !v.Owner && v.Username == "viewer02" {
			restricted = append(restricted, v)
		}
	}
	if len(restricted) == 0 {
		t.Fatal("expected restricted viewer02 in minted credentials")
	}
	db.Close()
	defer perfGatePostgres(t, ctx, dir, "plans", pgURL)()
	pg, schema := plansConn(t, dir)
	defer pg.Close()

	batchDiscover := map[string]int{
		"movies all titleSort p1": 4,
		"shows all titleSort p1":  4,
		"shows allLeaves p1":      6,
		"show children":           2,
	}

	// Each release mode once: the default (playable) and digital, whose
	// home-release gate adds its own predicates to listings and rows.
	byTag := map[string]perfharness.TraceRecord{}
	for _, mode := range []string{config.ModePlayable, config.ModeDigital} {
		if err := cfg.Update(func(c *config.Config) error {
			c.Availability.Mode = mode
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		trace := filepath.Join(dir, "trace-"+mode+".jsonl")
		t.Setenv(PerfTraceEnv, trace)
		a, err := New(ctx, Options{DataDir: dir, Logger: slog.New(slog.DiscardHandler)})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(a.Handler())
		a.frontends.start(ctx, server.Listener.Addr())
		var cs []perfharness.Client
		var restrictedClients []perfharness.Client
		for _, c := range perfharness.Only(perfharness.Clients(), []string{"plex-appletv", "infuse-jellyfin"}) {
			rawSteps := c.Steps
			keep := map[string]bool{}
			for _, n := range plansSteps[c.Name] {
				keep[n] = true
			}
			var steps []perfharness.Step
			for _, st := range rawSteps {
				if keep[st.Name] {
					if limit := batchDiscover[st.Name]; c.Name == "plex-appletv" && limit > 0 {
						st.Discover = plansChainDiscover(st.Discover, plansDiscoverBatchKeys(limit))
					}
					steps = append(steps, st)
				}
			}
			if c.Name == "plex-appletv" {
				steps = append(steps, plansExtraPlexSteps()...)
				rc := c
				rc.Name = "plex-appletv-restricted"
				rc.Steps = plansRestrictedSteps(rawSteps)
				restrictedClients = append(restrictedClients, rc)
			}
			c.Steps = steps
			cs = append(cs, c)
		}
		r := perfharness.NewRunner(server.Listener.Addr().(*net.TCPAddr).Port, true, viewers)
		r.SetTimeout(30 * time.Second)
		r.Sequential(ctx, "cold", viewers[:1], cs)
		r.Sequential(ctx, "warm", viewers[:1], cs)
		r.Sequential(ctx, "cold", restricted, restrictedClients)
		r.Sequential(ctx, "warm", restricted, restrictedClients)
		server.Close()
		a.Close()
		got, _, err := perfharness.ReadTrace(trace)
		if err != nil {
			t.Fatal(err)
		}
		for tag, rec := range got {
			byTag[mode+"|"+tag] = rec
		}
	}
	stmts := map[string]*planTracedStmt{}
	for tag, rec := range byTag {
		parts := strings.SplitN(tag, "|", 5)[1:]
		if len(parts) < 3 {
			continue
		}
		for _, st := range rec.Statements {
			e := stmts[st.SQL]
			if e == nil {
				e = &planTracedStmt{sites: map[string]bool{}, steps: map[string]bool{}}
				stmts[st.SQL] = e
			}
			e.sites[strings.SplitN(st.Site, " < ", 2)[0]] = true
			e.steps[parts[1]+" "+parts[2]] = true
		}
	}
	if len(stmts) < 50 {
		t.Fatalf("the replay traced only %d statements", len(stmts))
	}
	keys := make([]string, 0, len(stmts))
	for q := range stmts {
		keys = append(keys, q)
	}
	sort.Strings(keys)
	// Two statistics states: as ANALYZE leaves this small library (a
	// catalogue small next to a heavy viewer's history, where PostgreSQL
	// hashes a whole catalogue subquery), and scaled to a full mirror.
	checked, skipped := 0, 0
	fullMirrorPlans := map[string][]string{}
	for _, state := range []string{"analysed", "full mirror"} {
		if state == "full mirror" {
			plansSeedStatistics(t, ctx, pg, scale)
		}
		for _, q := range keys {
			plan, err := plansExplain(ctx, pg, schema, q)
			if err != nil {
				if state == "analysed" {
					skipped++
					t.Logf("not checked (%v): %.120s", err, q)
				}
				continue
			}
			if state == "full mirror" {
				fullMirrorPlans[q] = plan
			}
			checked++
			for _, issue := range plansIssues(plan, state == "full mirror") {
				e := stmts[q]
				t.Errorf("%s (statistics %s)\n  steps: %s\n  sites: %s\n  sql: %.600s\n  plan:\n%s", issue, state, keysOf(e.steps), keysOf(e.sites), q, strings.Join(plan, "\n"))
			}
		}
	}
	plansCheckCatalog598(t, stmts, fullMirrorPlans)
	covered := map[string]bool{}
	for _, e := range stmts {
		for site := range e.sites {
			covered[site] = true
		}
	}
	t.Logf("call sites covered: %s", keysOf(covered))
	t.Logf("checked %d statements of %d steps' requests, %d not explainable", checked, len(byTag), skipped)
}

// plansCheckCatalog598 asserts plan properties (index use and no sequential
// scan or whole-table hash on catalog_titles) for the three #598 paths:
// restricted artwork lookup, episode release join and count, and metadata
// detail batch. It checks plans only — never query counts or timings.
func plansCheckCatalog598(t *testing.T, stmts map[string]*planTracedStmt, plans map[string][]string) {
	t.Helper()
	tracedSteps := map[string]bool{}
	for _, e := range stmts {
		for step := range e.steps {
			tracedSteps[step] = true
		}
	}
	for _, wantStep := range []string{
		"plex-appletv shows episodes releasedAt desc",
		"plex-appletv /library/recentlyAdded",
		"plex-appletv metadata detail batch",
		"plex-appletv-restricted restricted artwork lookup",
		"plex-appletv-restricted restricted shows episodes releasedAt desc",
		"plex-appletv-restricted restricted metadata detail batch",
	} {
		if !tracedSteps[wantStep] {
			t.Errorf("expected step %q to be replayed and traced", wantStep)
		}
	}

	hasShowKeyProbe := func(joined string) bool {
		return strings.Contains(joined, "key = e.show_key") &&
			(strings.Contains(joined, "catalog_titles_pkey") || strings.Contains(joined, "catalog_titles_type_key_page"))
	}

	var artworkChecked, episodeCountChecked, episodeJoinChecked, batchTitlesChecked int
	for q, plan := range plans {
		e := stmts[q]
		joined := strings.Join(plan, "\n")

		// 1. Restricted remote artwork lookup: all four partial artwork indexes,
		// no Seq Scan on catalog_titles.
		if strings.Contains(q, "WHERE adult=0 AND poster<>'' AND poster=?") {
			artworkChecked++
			for _, idx := range []string{
				"catalog_titles_artwork_poster",
				"catalog_titles_artwork_backdrop",
				"catalog_titles_artwork_still",
				"catalog_titles_artwork_logo",
			} {
				if !strings.Contains(joined, idx) {
					t.Errorf("artwork lookup plan missing index %s:\n  sql: %.400s\n  plan:\n%s", idx, q, joined)
				}
			}
			if strings.Contains(joined, "Seq Scan on catalog_titles") {
				t.Errorf("artwork lookup plan sequentially scans catalog_titles:\n  plan:\n%s", joined)
			}
		}

		// 2a. Episode release subtraction counts (unaired and availability gate
		// cutoff): seek a partial episode-release index on e and probe show by
		// key, no Hash Join or Seq Scan on catalog_titles.
		if strings.HasPrefix(q, "SELECT COUNT(*) FROM catalog_titles e") && strings.Contains(q, "e.released_ts>?") {
			episodeCountChecked++
			if !(strings.Contains(joined, "catalog_titles_episode_release") || strings.Contains(joined, "catalog_titles_episode_recent")) {
				t.Errorf("episode release count plan missing partial episode release index:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
			if !hasShowKeyProbe(joined) {
				t.Errorf("episode release count plan missing indexed show key probe:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
			if strings.Contains(joined, "Hash Join") || strings.Contains(joined, "Seq Scan on catalog_titles") {
				t.Errorf("episode release count plan hashes or sequentially scans catalog_titles:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
		}

		// 2b. Episode release-ordered browse and RecentPage: walk
		// catalog_titles_episode_release on e and probe show by key, never Hash
		// Join, Seq Scan, or full-type catalog_plex_shelf_order on episodes.
		if strings.Contains(q, "ORDER BY e.released_ts DESC, e.key ASC") || strings.Contains(q, "recent_ep") {
			episodeJoinChecked++
			if !strings.Contains(joined, "catalog_titles_episode_release on catalog_titles e") {
				t.Errorf("episode release page plan missing catalog_titles_episode_release on e:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
			if !hasShowKeyProbe(joined) {
				t.Errorf("episode release page plan missing indexed show key probe:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
			if strings.Contains(joined, "Hash Join") || strings.Contains(joined, "Seq Scan on catalog_titles") || strings.Contains(joined, "catalog_plex_shelf_order on catalog_titles e") {
				t.Errorf("episode release page plan uses Hash Join, Seq Scan, or unselective shelf index on episodes:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
		}

		// 3. Multi-key metadata detail batch: every read touching catalog_titles
		// must use an index and never Seq Scan catalog_titles.
		if (e.steps["plex-appletv metadata detail batch"] || e.steps["plex-appletv-restricted restricted metadata detail batch"]) && strings.Contains(q, "catalog_titles") {
			batchTitlesChecked++
			if strings.Contains(joined, "Seq Scan on catalog_titles") {
				t.Errorf("metadata detail batch plan sequentially scans catalog_titles:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
			if !(strings.Contains(joined, "Index Scan") || strings.Contains(joined, "Index Only Scan") || strings.Contains(joined, "Bitmap Index Scan")) {
				t.Errorf("metadata detail batch plan on catalog_titles uses no index:\n  steps: %s\n  sql: %.400s\n  plan:\n%s", keysOf(e.steps), q, joined)
			}
		}
	}

	if artworkChecked == 0 {
		t.Error("artwork lookup SQL was not plan-checked")
	}
	if episodeCountChecked == 0 {
		t.Error("episode release count SQL was not plan-checked")
	}
	if episodeJoinChecked == 0 {
		t.Error("episode release page/join SQL was not plan-checked")
	}
	if batchTitlesChecked == 0 {
		t.Error("metadata detail batch catalog_titles SQL was not plan-checked")
	}
}

func keysOf(m map[string]bool) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// plansConn opens the schema the data dir now points at.
func plansConn(t *testing.T, dir string) (*sql.DB, string) {
	t.Helper()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	u := cfg.Get().Database.URL
	db, err := sql.Open(store.PostgresDriverName, u)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	var schema string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return db, schema
}

// plansSeedStatistics makes PostgreSQL plan the small library as a full
// mirror: after ANALYZE, every catalogue table's (and index's) tuple count
// is scaled so the planner's estimate (tuples per page times the table's
// actual pages) is a full mirror's, and the watch tables' a large
// household's. Fractions in pg_statistic (null and most-common-value
// frequencies, negative n_distinct) scale with them. Autovacuum is held off
// so nothing re-analyses the tables during the check.
func plansSeedStatistics(t *testing.T, ctx context.Context, db *sql.DB, s perfharness.Scale) {
	t.Helper()
	catalogue := float64(plansFullMirror) / float64(s.Titles())
	history := float64(plansHousehold) / float64(max(1, (s.Viewers+1)*s.HistoryPerViewer))
	rows, err := db.QueryContext(ctx, `SELECT c.relname FROM pg_class c WHERE c.relnamespace=current_schema()::regnamespace AND c.relkind='r'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	for _, table := range tables {
		var pages int64
		if err := db.QueryRowContext(ctx, `SELECT pg_relation_size('"`+table+`"'::regclass) / current_setting('block_size')::bigint`).Scan(&pages); err != nil {
			t.Fatal(err)
		}
		if pages == 0 {
			// An empty table (catalog_people on most mirrors) cannot be
			// scaled: the planner reads its actual size. Its scans are not
			// judged.
			plansEmpty[table] = true
			continue
		}
		var factor float64
		switch {
		case strings.HasPrefix(table, "catalog_") && !strings.Contains(table, "tally") && !strings.HasPrefix(table, "catalog_meta"):
			factor = catalogue
		case strings.HasPrefix(table, "state_"):
			factor = history
		default:
			continue
		}
		f := strconv.FormatFloat(factor, 'f', 2, 64)
		for _, q := range []string{
			`ALTER TABLE "` + table + `" SET (autovacuum_enabled = false)`,
			// The table and each of its indexes: tuples scaled, pages as they
			// are, so tuples per page carries the factor.
			`UPDATE pg_class SET reltuples = GREATEST(reltuples, 1) * ` + f + `, relpages = GREATEST(relpages, 1) WHERE oid = '"` + table + `"'::regclass
			  OR oid IN (SELECT indexrelid FROM pg_index WHERE indrelid = '"` + table + `"'::regclass)`,
		} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
}

// plansExplain plans q generically, as a prepared statement's generic plan
// on PostgreSQL 14 (EXPLAIN (GENERIC_PLAN) needs 16): no parameter value
// steers it.
func plansExplain(ctx context.Context, db *sql.DB, schema, q string) ([]string, error) {
	if strings.HasPrefix(q, store.SQLiteOnly) {
		return nil, fmt.Errorf("SQLite-only statement")
	}
	upper := strings.ToUpper(strings.TrimSpace(q))
	if !(strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")) {
		return nil, fmt.Errorf("not a read")
	}
	bound, n := store.Rebind(q)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	for _, s := range []string{`SET search_path TO ` + schema, `SET plan_cache_mode = force_generic_plan`} {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return nil, err
		}
	}
	plansSeq++
	name := "ether_plan_" + strconv.Itoa(plansSeq)
	if _, err := conn.ExecContext(ctx, `PREPARE `+name+` AS `+bound); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), `DEALLOCATE `+name)
	args := make([]string, n)
	for i := range args {
		args[i] = "NULL"
	}
	explain := `EXPLAIN EXECUTE ` + name
	if n > 0 {
		explain += `(` + strings.Join(args, ",") + `)`
	}
	rows, err := conn.QueryContext(ctx, explain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		plan = append(plan, line)
	}
	return plan, rows.Err()
}

var (
	plansSeq   int
	plansEmpty = map[string]bool{}
)

var (
	plansNode       = regexp.MustCompile(`^(\s*)(?:->\s+)?([A-Z][A-Za-z ]+?)(?: using \S+)? on (\S+)`)
	plansHashedRef  = regexp.MustCompile(`hashed SubPlan (\d+)`)
	plansSubPlanDef = regexp.MustCompile(`^(\s*)SubPlan (\d+)`)
)

// plansIssues finds the scans of plansWatched tables that read the table
// whole. A hashed subplan over a catalogue table hashes the catalogue (or
// all of one type) whatever its condition, so it fails in every statistics
// state; a scan without an index condition fails only at full-mirror
// sizes, where it cannot be a small table read whole.
func plansIssues(plan []string, atScale bool) []string {
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }
	// The extent of each node: its own detail lines, before its children.
	hasCond := func(i int) bool {
		base := indent(plan[i])
		for j := i + 1; j < len(plan); j++ {
			l := strings.TrimSpace(plan[j])
			if indent(plan[j]) <= base || strings.HasPrefix(l, "->") || strings.HasPrefix(l, "SubPlan") || strings.HasPrefix(l, "InitPlan") {
				return false
			}
			if strings.HasPrefix(l, "Index Cond:") || strings.HasPrefix(l, "Recheck Cond:") {
				return true
			}
		}
		return false
	}
	hashed := map[string]bool{}
	for _, l := range plan {
		for _, m := range plansHashedRef.FindAllStringSubmatch(l, -1) {
			hashed[m[1]] = true
		}
	}
	var issues []string
	inHashed, hashedIndent := "", 0
	for i, l := range plan {
		if m := plansSubPlanDef.FindStringSubmatch(l); m != nil {
			inHashed, hashedIndent = "", 0
			if hashed[m[2]] {
				inHashed, hashedIndent = m[2], len(m[1])
			}
			continue
		}
		if inHashed != "" && indent(l) <= hashedIndent {
			inHashed = ""
		}
		m := plansNode.FindStringSubmatch(l)
		if m == nil || !plansWatched.MatchString(m[3]) || plansEmpty[m[3]] {
			continue
		}
		node, table := strings.TrimSpace(m[2]), m[3]
		switch {
		case inHashed != "" && strings.HasPrefix(table, "catalog_"):
			issues = append(issues, "hashed subplan "+inHashed+" reads "+table+" ("+node+")")
		case inHashed != "" && !hasCond(i) && node != "Seq Scan":
			issues = append(issues, "hashed subplan "+inHashed+" reads "+table+" whole")
		case !atScale:
		case node == "Seq Scan":
			issues = append(issues, "sequential scan of "+table)
		case node == "Bitmap Heap Scan" && !hasCond(i):
			issues = append(issues, "bitmap heap scan of "+table+" without an index condition")
		}
	}
	return issues
}
