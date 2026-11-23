package sports_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/srwiley/oksvg"
)

func loadSources(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir("src", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(p)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func loadFixtureMap(t *testing.T) map[string]string {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("tests", "fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return map[string]string{
		"football/nfl/scoreboard":                read("scoreboard-nfl.json"),
		"basketball/nba/scoreboard":              read("scoreboard-nba.json"),
		"hockey/nhl/scoreboard":                  read("scoreboard-nhl.json"),
		"soccer/eng.1/scoreboard":                read("scoreboard-epl.json"),
		"racing/leaderboard?league=f1":           read("scoreboard-f1.json"),
		"golf/leaderboard?league=pga":            read("scoreboard-pga.json"),
		"mma/ufc/scoreboard":                     read("scoreboard-ufc.json"),
		"football/nfl/summary?event=401873066":   read("summary-nfl.json"),
		"basketball/nba/summary?event=401910023": read("summary-nba.json"),
		"soccer/eng.1/summary?event=401879234":   read("summary-epl.json"),
		"football/nfl/teams":                     read("teams-nfl.json"),
		"soccer/eng.1/teams":                     read("teams-epl.json"),
		"football/nfl/standings":                 read("standings-nfl.json"),
		"basketball/nba/standings":               read("standings-nba.json"),
		"hockey/nhl/standings":                   read("standings-nhl.json"),
		"soccer/eng.1/standings":                 read("standings-epl.json"),
		"racing/f1/standings":                    read("standings-f1.json"),
	}
}

// TestCompileAndRunInQuickJS compiles src/plugin.ts through Ether's real
// esbuild VFS bundler (wasm.Compile) and exercises every plugin route inside
// Ether's real QuickJS runtime using recorded offline fixtures.
func TestCompileAndRunInQuickJS(t *testing.T) {
	files := loadSources(t)
	js, _, diags := wasm.Compile(files, "src/plugin.ts")
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("wasm.Compile error: %s:%d: %s", d.File, d.Line, d.Text)
		}
	}
	if len(js) == 0 {
		t.Fatal("wasm.Compile produced empty bundle")
	}

	fixtures := loadFixtureMap(t)
	kv := map[string]string{}
	settings := map[string]any{
		"leagues": []string{"nfl", "nba", "nhl", "epl", "f1", "pga", "ufc"},
		"region":  "us",
	}

	resolveHTTP := func(rawURL string) map[string]any {
		u, err := url.Parse(rawURL)
		if err != nil {
			return map[string]any{"status": 400, "headers": map[string]string{}, "body": ""}
		}
		withEvent := u.Path
		if ev := u.Query().Get("event"); ev != "" {
			withEvent += "?event=" + ev
		} else if lg := u.Query().Get("league"); lg != "" {
			withEvent += "?league=" + lg
		}
		for suffix, body := range fixtures {
			if strings.HasSuffix(withEvent, suffix) || strings.HasSuffix(u.Path, suffix) {
				return map[string]any{
					"status":  200,
					"headers": map[string]string{"content-type": "application/json"},
					"body":    body,
				}
			}
		}
		return map[string]any{"status": 404, "headers": map[string]string{}, "body": "{}"}
	}

	host := wasm.HostFunc(func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "settings.get":
			return json.Marshal(settings)
		case "kv.get":
			var a []string
			_ = json.Unmarshal(args, &a)
			if v, ok := kv[a[0]]; ok {
				return json.Marshal(v)
			}
			return json.RawMessage("null"), nil
		case "kv.put":
			var a []string
			_ = json.Unmarshal(args, &a)
			kv[a[0]] = a[1]
			return json.RawMessage("null"), nil
		case "kv.delete":
			var a []string
			_ = json.Unmarshal(args, &a)
			delete(kv, a[0])
			return json.RawMessage("null"), nil
		case "http.fetch":
			var a []struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(args, &a)
			return json.Marshal(resolveHTTP(a[0].URL))
		case "http.fetchMany":
			var a []json.RawMessage
			_ = json.Unmarshal(args, &a)
			var reqs []struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(a[0], &reqs)
			out := make([]map[string]any, len(reqs))
			for i, r := range reqs {
				out[i] = resolveHTTP(r.URL)
			}
			return json.Marshal(out)
		case "log":
			return json.RawMessage("null"), nil
		default:
			return nil, &wasm.Fault{Code: "forbidden", Message: "unsupported host call " + name}
		}
	})

	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Close()

	plug, err := eng.LoadJS("org.ether.sports", "1.0.0", string(js), host, wasm.Limits{MemoryBytes: 64 << 20})
	if err != nil {
		t.Fatalf("LoadJS: %v", err)
	}
	defer plug.Close()

	ctx := context.Background()

	// 1. Health
	res, err := plug.Call(ctx, wasm.Request{Method: "GET", Path: "/ether/health"})
	if err != nil || res.Status != 200 {
		t.Fatalf("GET /ether/health: status=%d err=%v body=%s", res.Status, err, string(res.Body))
	}

	// 2. Events across all 7 fixture leagues (must unmarshal into core.LiveEventsResponse)
	res, err = plug.Call(ctx, wasm.Request{Method: "GET", Path: "/ether/v1/live/sports/events"})
	if err != nil || res.Status != 200 {
		t.Fatalf("GET /events: status=%d err=%v body=%s", res.Status, err, string(res.Body))
	}
	var evResp core.LiveEventsResponse
	if err := json.Unmarshal(res.Body, &evResp); err != nil {
		t.Fatalf("decode events into core.LiveEventsResponse: %v", err)
	}
	if len(evResp.Events) < 12 || evResp.NextPollSec != 15 {
		t.Fatalf("unexpected events response: count=%d nextPollSec=%d", len(evResp.Events), evResp.NextPollSec)
	}

	// 3. Event detail (NFL summary with tables, plays, 0..1 probabilitySeries -> core.LiveEventDetail)
	res, err = plug.Call(ctx, wasm.Request{Method: "GET", Path: "/ether/v1/live/sports/events/nfl-401873066"})
	if err != nil || res.Status != 200 {
		t.Fatalf("GET /events/nfl-401873066: status=%d err=%v body=%s", res.Status, err, string(res.Body))
	}
	var detail core.LiveEventDetail
	if err := json.Unmarshal(res.Body, &detail); err != nil {
		t.Fatalf("decode detail into core.LiveEventDetail: %v", err)
	}
	if len(detail.Tables) < 3 || len(detail.Plays) == 0 || len(detail.ProbabilitySeries) < 10 {
		t.Fatalf("incomplete detail: tables=%d plays=%d prob=%d", len(detail.Tables), len(detail.Plays), len(detail.ProbabilitySeries))
	}

	// 4. Entities (teams -> core.LiveEventEntitiesResponse)
	res, err = plug.Call(ctx, wasm.Request{
		Method: "GET",
		Path:   "/ether/v1/live/sports/entities",
		Query:  map[string][]string{"kind": {"team"}, "league": {"nfl"}},
	})
	if err != nil || res.Status != 200 {
		t.Fatalf("GET /entities: status=%d err=%v body=%s", res.Status, err, string(res.Body))
	}
	var entResp core.LiveEventEntitiesResponse
	if err := json.Unmarshal(res.Body, &entResp); err != nil {
		t.Fatalf("decode entities into core.LiveEventEntitiesResponse: %v", err)
	}
	if len(entResp.Entities) == 0 {
		t.Fatal("expected non-empty NFL team entities")
	}

	// 5. Standings (unsorted ESPN NBA fixture must come out sorted seed 1 first -> core.LiveEventStandingsResponse)
	res, err = plug.Call(ctx, wasm.Request{
		Method: "GET",
		Path:   "/ether/v1/live/sports/standings",
		Query:  map[string][]string{"league": {"nba"}},
	})
	if err != nil || res.Status != 200 {
		t.Fatalf("GET /standings: status=%d err=%v body=%s", res.Status, err, string(res.Body))
	}
	var stResp core.LiveEventStandingsResponse
	if err := json.Unmarshal(res.Body, &stResp); err != nil {
		t.Fatalf("decode standings into core.LiveEventStandingsResponse: %v", err)
	}
	if len(stResp.Tables) != 2 || len(stResp.Tables[0].Rows) == 0 || stResp.Tables[0].Rows[0][0] != "1" {
		t.Fatalf("expected sorted NBA standings starting at seed 1, got %+v", stResp.Tables)
	}
}

// TestIconAndCopyRules verifies assets/icon.svg parses cleanly under oksvg's
// StrictErrorMode and every English string satisfies plugins.LintCopy.
func TestIconAndCopyRules(t *testing.T) {
	svg, err := os.ReadFile(filepath.Join("assets", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	icon, err := oksvg.ReadIconStream(strings.NewReader(string(svg)), oksvg.StrictErrorMode)
	if err != nil || icon == nil {
		t.Fatalf("assets/icon.svg failed oksvg strict parse: %v", err)
	}

	rawEn, err := os.ReadFile(filepath.Join("i18n", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var en map[string]string
	if err := json.Unmarshal(rawEn, &en); err != nil {
		t.Fatal(err)
	}
	for k, v := range en {
		maxLen := 40
		if strings.HasSuffix(k, "_hint") {
			maxLen = 120
		} else if k == "summary" {
			maxLen = 80
		}
		if msg := plugins.LintCopy(v, maxLen); msg != "" {
			t.Errorf("i18n/en.json[%q] (%q): %s", k, v, msg)
		}
	}
}
