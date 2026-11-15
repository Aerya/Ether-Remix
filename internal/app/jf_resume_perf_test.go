package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/perfharness"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

// Opt-in offline reproduction: keep the household fixed while growing the
// catalogue. No provider requests or background server loops are needed.
func TestJFResumeLibraryBound(t *testing.T) {
	if os.Getenv("ETHER_JF_RESUME_PERF") == "" {
		t.Skip("set ETHER_JF_RESUME_PERF=1")
	}
	ctx := context.Background()
	dir := os.Getenv("ETHER_JF_RESUME_FIXTURE")
	if dir == "" {
		t.Fatal("set ETHER_JF_RESUME_FIXTURE under ~/.scratch")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Join(home, ".scratch", "tmp")+string(os.PathSeparator)) {
		t.Fatal("fixture must be under ~/.scratch/tmp")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_titles").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		scale := perfharness.Scale{Movies: 200000, Shows: 20000, SeasonsPerShow: 2, EpisodesPerSeason: 8, EpisodeShows: 1000, Viewers: 1, HistoryPerViewer: 20000, InProgressPerViewer: 40}
		if err = perfharness.Generate(ctx, db, scale, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	// This is a disposable fixture; repeated measurements must not exhaust
	// the protocol's persisted sign-in rate bucket.
	if _, err := db.ExecContext(ctx, `DELETE FROM identity_rate_buckets`); err != nil {
		t.Fatal(err)
	}
	viewers, err := perfharness.MintCredentials(ctx, db, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure the fixture has unfinished series even after deep history has
	// watched every episode in the taste sample. Keep twenty newest anchors
	// with an unwatched successor, identically in the before and after runs.
	showRows, err := db.QueryContext(ctx, `SELECT DISTINCT show_key FROM catalog_titles WHERE type='episode' ORDER BY show_key LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}
	var shows []core.RatingKey
	for showRows.Next() {
		var key core.RatingKey
		if err := showRows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		shows = append(shows, key)
	}
	if err := showRows.Err(); err != nil {
		t.Fatal(err)
	}
	showRows.Close()
	for n, show := range shows {
		lo, hi := core.EpisodeKey(show.TMDB(), 0, 0), core.EpisodeKey(show.TMDB()+1, 0, 0)
		if _, err := db.ExecContext(ctx, `DELETE FROM state_watch WHERE user_id=? AND rating_key>=? AND rating_key<?`, viewers[0].ID, lo, hi); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(n) * time.Minute).UnixNano()
		key := core.EpisodeKey(show.TMDB(), 1, 1)
		if _, err := db.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at,updated_at) VALUES(?,?,1,?,?)`, viewers[0].ID, key, at, at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO state_history(user_id,rating_key,viewed_at,completed) VALUES(?,?,?,1)`, viewers[0].ID, key, at); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	a, err := New(ctx, Options{DataDir: dir, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.personalize.Rebuild(ctx, owner); err != nil {
		t.Fatal(err)
	}
	inst, err := a.frontends.registry.Mount(ctx, core.Protocol("jellyfin"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.frontends.registry.Unmount(ctx, core.Protocol("jellyfin"))
	if _, err = a.frontends.registry.Mount(ctx, core.Protocol("webdav")); err != nil {
		t.Fatal(err)
	}
	defer a.frontends.registry.Unmount(ctx, core.Protocol("webdav"))
	login := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/Users/AuthenticateByName", strings.NewReader(`{"Username":"`+viewers[0].Username+`","Pw":"`+perfharness.Password+`"}`))
	loginReq.Header.Set("Authorization", `MediaBrowser Client="Web", DeviceId="perf"`)
	inst.Handler().ServeHTTP(login, loginReq)
	var signed struct{ AccessToken string }
	if err = json.Unmarshal(login.Body.Bytes(), &signed); err != nil || signed.AccessToken == "" {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	var show core.RatingKey
	if err = a.db.QueryRowContext(ctx, "SELECT key FROM catalog_titles WHERE type='show' AND key IN (SELECT show_key FROM catalog_titles WHERE type='episode') LIMIT 1").Scan(&show); err != nil {
		t.Fatal(err)
	}
	var report strings.Builder
	for _, path := range []string{"/UserItems/Resume?Limit=20", "/Shows/NextUp?Limit=20", "/Shows/" + ids.RatingKeyGUID(show) + "/Seasons"} {
		for pass := 0; pass < 2; pass++ {
			measured, stats := store.WithQueryStats(reqmemo.With(ctx))
			req := httptest.NewRequest("GET", path, nil).WithContext(measured)
			req.Header.Set("Authorization", `MediaBrowser Client="Web", DeviceId="perf", Token="`+signed.AccessToken+`"`)
			w := httptest.NewRecorder()
			start := time.Now()
			inst.Handler().ServeHTTP(w, req)
			elapsed := time.Since(start)
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
			var body struct{ Items []json.RawMessage }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Items) == 0 {
				t.Errorf("%s returned no items", path)
			}
			line := path + " " + elapsed.String()
			t.Logf("%s pass=%d queries=%d rows=%d db=%s items=%d", line, pass, stats.Queries(), stats.Rows(), stats.DBTime(), len(body.Items))
			report.WriteString(line + "\n")
			statements := stats.Statements()
			if stats.Queries() <= 30 {
				statements = statements[:min(5, len(statements))]
			}
			for _, st := range statements {
				t.Logf("  %dx %d rows %s %.180s", st.Count, st.Rows, st.Time, st.SQL)
			}
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "last-report.txt"), []byte(report.String()), 0600)
}
