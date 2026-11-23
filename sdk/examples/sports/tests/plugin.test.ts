import { test } from "node:test";
import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";

import { isCloseGame } from "../src/close.ts";
import type {
  EntitiesResponse,
  EventsResponse,
  HostedEtherGlobal,
  HostFetchBatchResult,
  HostFetchRequest,
  HostFetchResponse,
  LiveEventDetail,
  StandingsResponse,
} from "../src/contract.ts";
import { buildMatchHints, extractBroadcasts } from "../src/hints.ts";
import { DEFAULT_LEAGUES } from "../src/leagues.ts";
import { createSportsHandler } from "../src/plugin.ts";
import type { SportsProvider } from "../src/providers/types.ts";

function loadFixture(name: string): string {
  return readFileSync(new URL(`./fixtures/${name}`, import.meta.url), "utf8");
}

function loadJSON(relPath: string): unknown {
  return JSON.parse(readFileSync(new URL(relPath, import.meta.url), "utf8"));
}

const FIXTURES: Record<string, string> = {
  "football/nfl/scoreboard": loadFixture("scoreboard-nfl.json"),
  "basketball/nba/scoreboard": loadFixture("scoreboard-nba.json"),
  "hockey/nhl/scoreboard": loadFixture("scoreboard-nhl.json"),
  "soccer/eng.1/scoreboard": loadFixture("scoreboard-epl.json"),
  "racing/leaderboard?league=f1": loadFixture("scoreboard-f1.json"),
  "golf/leaderboard?league=pga": loadFixture("scoreboard-pga.json"),
  "mma/ufc/scoreboard": loadFixture("scoreboard-ufc.json"),
  "football/nfl/summary?event=401873066": loadFixture("summary-nfl.json"),
  "basketball/nba/summary?event=401910023": loadFixture("summary-nba.json"),
  "soccer/eng.1/summary?event=401879234": loadFixture("summary-epl.json"),
  "football/nfl/teams": loadFixture("teams-nfl.json"),
  "soccer/eng.1/teams": loadFixture("teams-epl.json"),
  "football/nfl/standings": loadFixture("standings-nfl.json"),
  "basketball/nba/standings": loadFixture("standings-nba.json"),
  "hockey/nhl/standings": loadFixture("standings-nhl.json"),
  "soccer/eng.1/standings": loadFixture("standings-epl.json"),
  "racing/f1/standings": loadFixture("standings-f1.json"),
};

interface MockHostControls {
  ether: HostedEtherGlobal;
  kv: Map<string, string>;
  urls: string[];
  batchCalls: Array<{ count: number; concurrency?: number }>;
  setNow(ms: number): void;
  advance(ms: number): void;
  now(): number;
  setSettings(next: Record<string, unknown>): void;
}

function createMockHost(options?: {
  settings?: Record<string, unknown>;
  nowMs?: number;
  httpOverride?: (req: HostFetchRequest) => HostFetchBatchResult | null;
}): MockHostControls {
  let settings: Record<string, unknown> = options?.settings ?? {
    leagues: ["nfl", "nba", "nhl", "epl", "f1", "pga", "ufc"],
    region: "us",
  };
  let currentNow = options?.nowMs ?? Date.parse("2026-11-22T15:15:00.000Z");
  const kv = new Map<string, string>();
  const urls: string[] = [];
  const batchCalls: Array<{ count: number; concurrency?: number }> = [];

  const resolveRequest = (req: HostFetchRequest): HostFetchBatchResult => {
    urls.push(req.url);
    if (options?.httpOverride) {
      const override = options.httpOverride(req);
      if (override !== null) return override;
    }
    const u = new URL(req.url);
    // Reject multi-day ranges on team sports, matching live ESPN API behavior.
    const datesParam = u.searchParams.get("dates") || "";
    const isRange = datesParam.includes("-");
    const isMultiDaySport =
      u.pathname.includes("/racing/") ||
      u.pathname.includes("/golf/") ||
      u.pathname.includes("/mma/ufc/");
    if (isRange && !isMultiDaySport) {
      return { status: 400, headers: {}, body: '{"message":"Bad Request"}' };
    }

    const withQuery = u.pathname +
      (u.searchParams.get("event")
        ? `?event=${u.searchParams.get("event")}`
        : u.searchParams.get("league")
          ? `?league=${u.searchParams.get("league")}`
          : "");
    for (const [suffix, body] of Object.entries(FIXTURES)) {
      if (withQuery.endsWith(suffix) || u.pathname.endsWith(suffix)) {
        return { status: 200, headers: { "content-type": "application/json" }, body };
      }
    }
    return { status: 404, headers: {}, body: '{"message":"Not Found"}' };
  };

  const ether: HostedEtherGlobal = {
    settings: {
      get: () => settings,
    },
    kv: {
      get: (k) => kv.get(k) ?? null,
      put: (k, v) => {
        kv.set(k, v);
      },
      delete: (k) => {
        kv.delete(k);
      },
      list: (prefix = "") => [...kv.keys()].filter((k) => k.startsWith(prefix)),
    },
    http: {
      fetch: (req) => {
        const res = resolveRequest(req);
        if ("error" in res) throw res.error;
        return res as HostFetchResponse;
      },
      fetchMany: (reqs, opts) => {
        batchCalls.push({ count: reqs.length, concurrency: opts?.concurrency });
        return reqs.map(resolveRequest);
      },
    },
    log: () => {},
  };

  return {
    ether,
    kv,
    urls,
    batchCalls,
    setNow: (ms) => {
      currentNow = ms;
    },
    advance: (ms) => {
      currentNow += ms;
    },
    now: () => currentNow,
    setSettings: (next) => {
      settings = next;
    },
  };
}

// Mirror of internal/plugins/manifest.go LintCopy for offline verification.
function lintCopy(s: string, max: number): string | null {
  const trimmed = s.trim();
  if (max > 0 && [...trimmed].length > max) return `longer than ${max}`;
  if (/[—–]/.test(trimmed)) return "contains em/en dash";
  if (trimmed.includes("!")) return "contains exclamation mark";
  if (/\b(simply|just|easily)\b/i.test(trimmed)) return "contains banned word";
  const firstLetter = /\p{L}/u.exec(trimmed)?.[0];
  if (firstLetter && firstLetter === firstLetter.toLowerCase() && firstLetter !== firstLetter.toUpperCase()) {
    return "must use sentence case";
  }
  const words = trimmed.split(/\s+/).filter(Boolean);
  if (words.length >= 3) {
    let caps = 0;
    for (const w of words) {
      const ch = w[0];
      if (ch && ch === ch.toUpperCase() && ch !== ch.toLowerCase()) caps++;
    }
    if (caps * 3 > words.length * 2) return "must not be Title Case";
  }
  return null;
}

test("manifest, settings schema, i18n, and 17 default leagues satisfy contract and copy rules", () => {
  const manifest = loadJSON("../manifest.json") as any;
  const schema = loadJSON("../settings.schema.json") as any;
  const en = loadJSON("../i18n/en.json") as Record<string, string>;
  const fr = loadJSON("../i18n/fr.json") as Record<string, string>;

  assert.equal(manifest.id, "org.ether.sports");
  assert.equal(manifest.ether.kind, "extension");
  assert.deepEqual(manifest.ether.capabilities, ["live_events"]);
  assert.equal(manifest.ether.runtime.kind, "hosted");
  assert.equal(manifest.ether.runtime.entry, "src/plugin.ts");
  assert.equal(manifest.ether.schedule, undefined, "plugin must never declare a background schedule");
  assert.deepEqual(manifest.ether.permissions.network, [
    "site.api.espn.com",
    "site.web.api.espn.com",
  ]);

  // Live feed contribution (spec A1)
  assert.equal(manifest.ether.contributes.liveFeeds.length, 1);
  const feed = manifest.ether.contributes.liveFeeds[0];
  assert.equal(feed.id, "sports");
  assert.ok(feed.minIntervalSec >= 10);
  assert.deepEqual(feed.entityKinds, ["team"]);

  // Screen contribution (spec A3 + binding Vela wire section types)
  assert.equal(manifest.ether.contributes.screens.length, 1);
  const screen = manifest.ether.contributes.screens[0];
  assert.equal(screen.id, "sports");
  assert.equal(screen.icon, "trophy");
  const types = screen.sections.map((s: any) => s.type);
  assert.deepEqual(types, [
    "leagues",
    "hero",
    "events",
    "events",
    "events",
    "schedule",
    "table",
    "entities",
  ]);
  const liveSection = screen.sections.find((s: any) => s.id === "live");
  assert.deepEqual(liveSection.query, { state: ["in"] });
  const favSection = screen.sections.find((s: any) => s.id === "your-teams");
  assert.deepEqual(favSection.query, { favorites: true, window: "week" });

  // All 17 default leagues configured in settings schema and registry
  assert.equal(DEFAULT_LEAGUES.length, 17);
  const leaguesField = schema.fields.find((f: any) => f.key === "leagues");
  assert.equal(leaguesField.kind, "multiSelect");
  assert.equal(leaguesField.default.length, 17);
  assert.equal(leaguesField.options.length, 17);
  assert.deepEqual(
    leaguesField.default,
    DEFAULT_LEAGUES.map((l) => l.id),
  );

  // Every $t: reference exists in en & fr, and passes LintCopy
  const refs = new Set<string>();
  const walk = (node: unknown) => {
    if (typeof node === "string" && node.startsWith("$t:")) {
      refs.add(node.slice(3));
    } else if (Array.isArray(node)) {
      node.forEach(walk);
    } else if (node && typeof node === "object") {
      Object.values(node).forEach(walk);
    }
  };
  walk(manifest);
  walk(schema);

  for (const key of refs) {
    assert.ok(key in en, `missing en.json key ${key}`);
    assert.ok(key in fr, `missing fr.json key ${key}`);
    const maxLen = key.endsWith("_hint") ? 120 : key === "summary" ? 80 : 40;
    const problem = lintCopy(en[key], maxLen);
    assert.equal(problem, null, `en.json[${key}] failed LintCopy: ${problem} (${en[key]})`);
    if (key.endsWith("_hint")) {
      assert.ok(!en[key].replace(/\.+$/, "").includes("."), `hint ${key} must be one sentence`);
    }
  }
  assert.equal(lintCopy(manifest.name, 40), null);
  assert.equal(lintCopy(manifest.description, 300), null);
});

test("maps recorded NFL, NBA, NHL, EPL, F1, PGA and UFC scoreboards to spec A2 LiveEvent shape", () => {
  const host = createMockHost();
  const handle = createSportsHandler({ getEther: () => host.ether, now: host.now });

  const resp = handle({ method: "GET", path: "/ether/v1/live/sports/events" });
  assert.equal(resp.status, 200);
  const body = resp.body as EventsResponse;

  // Live EPL + PGA events exist -> 15 s poll cadence
  assert.equal(body.nextPollSec, 15);
  assert.ok(body.events.length >= 12);

  // Every event obeys A2 core invariants
  for (const ev of body.events) {
    assert.equal(ev.feed, "sports");
    assert.ok(!ev.id.includes(":") && !ev.id.includes("/"), `unsafe event id ${ev.id}`);
    assert.ok(!Number.isNaN(Date.parse(ev.start)), `invalid RFC3339 start ${ev.start}`);
    assert.ok(ev.start.endsWith("Z"), `start must be normalized RFC3339: ${ev.start}`);
    assert.ok(!Number.isNaN(Date.parse(ev.updatedAt)));
    assert.equal((ev as any).spoiler, undefined, "plugin must never set spoiler");
  }

  // Live events sort before pre-game, which sort before post-game
  const firstPostIdx = body.events.findIndex((e) => e.status.state === "post");
  const lastInIdx = body.events.map((e) => e.status.state).lastIndexOf("in");
  assert.ok(lastInIdx >= 0 && firstPostIdx > lastInIdx);

  // NFL final game (IND @ HOU): scores, winner, Prime Video -> stream, matchHints
  const nflFinal = body.events.find((e) => e.id === "nfl-401873066")!;
  assert.ok(nflFinal);
  assert.equal(nflFinal.category, "football");
  assert.equal(nflFinal.status.state, "post");
  assert.equal(nflFinal.competitors?.length, 2);
  const awayNfl = nflFinal.competitors!.find((c) => !c.home)!;
  const homeNfl = nflFinal.competitors!.find((c) => c.home)!;
  assert.equal(awayNfl.abbr, "IND");
  assert.equal(homeNfl.abbr, "HOU");
  assert.equal(homeNfl.winner, true);
  assert.ok(homeNfl.score && awayNfl.score);
  assert.ok(homeNfl.entityId?.startsWith("nfl-"));
  assert.ok(homeNfl.color?.startsWith("#"));
  assert.ok(nflFinal.broadcasts?.some((b) => b.name === "Prime Video" && b.kind === "stream"));
  assert.ok(nflFinal.matchHints?.terms.includes("Houston Texans"));
  assert.ok(nflFinal.matchHints?.terms.includes("Texans"));
  assert.ok(nflFinal.matchHints?.terms.includes("HOU"));
  assert.ok(nflFinal.matchHints?.callsigns.includes("Prime Video"));

  // NFL pre-game omits score ("0" should not render on pre-game cards) and status.detail
  const nflPre = body.events.find((e) => e.id === "nfl-401873070")!;
  assert.equal(nflPre.status.state, "pre");
  assert.equal(nflPre.status.detail, undefined, "pre-game events must leave status.detail empty");
  assert.equal(nflPre.title, undefined, "2-competitor team events must omit title");
  assert.equal(nflPre.competitors?.[0].score, undefined);
  assert.ok(nflPre.broadcasts?.some((b) => b.name === "CBS" && b.kind === "tv"));

  // EPL live match (BHA @ HUL 60')
  const eplLive = body.events.find((e) => e.id === "epl-401879234")!;
  assert.equal(eplLive.status.state, "in");
  assert.equal(eplLive.status.clock, "60'");
  assert.equal(eplLive.title, undefined, "2-competitor soccer event must omit title");
  assert.equal(eplLive.status.close, undefined, "60' 0-0 is before 80' close threshold");
  assert.ok(eplLive.matchHints?.callsigns.includes("USA Network"));
  for (const term of eplLive.matchHints?.terms || []) {
    const words = term.split(" ");
    if (words.length >= 2 && words.length % 2 === 0) {
      const half = words.length / 2;
      assert.notEqual(
        words.slice(0, half).join(" ").toLowerCase(),
        words.slice(half).join(" ").toLowerCase(),
        `doubled match term must not be emitted: ${term}`,
      );
    }
  }

  // F1 uses ranking rows + empty competitors[] + time/gap detail + constructor secondary
  const f1 = body.events.find((e) => e.id === "f1-600057449")!;
  assert.equal(f1.category, "motorsport");
  assert.deepEqual(f1.competitors, [], "ranking events must emit competitors: []");
  assert.ok(f1.ranking && f1.ranking.length === 10);
  assert.equal(f1.ranking[0].pos, 1);
  assert.equal(f1.ranking[0].name, "Max Verstappen");
  assert.equal(f1.ranking[0].abbr, "VER");
  assert.match(f1.ranking[0].detail || "", /^\d+:\d+:\d+\.\d+$/, "F1 P1 detail should be race time");
  assert.equal(f1.ranking[0].secondary, "Red Bull");
  assert.match(f1.ranking[1].detail || "", /^\+\d/, "F1 P2+ detail should be time gap");
  assert.ok(f1.ranking[1].secondary, "F1 P2+ secondary should carry constructor");
  for (const r of f1.ranking) {
    assert.notEqual(r.detail, "Winner", "F1 detail must never be 'Winner'");
    assert.notEqual(r.detail, "Britain", "F1 detail must never be nationality");
    assert.notEqual(r.detail, "Australia", "F1 detail must never be nationality");
  }
  assert.equal(f1.situation?.Session, "Race");

  // PGA live tournament uses ranking rows with empty competitors[], total-to-par detail, thru secondary, and tie-aware pos
  const pga = body.events.find((e) => e.league.id === "pga")!;
  assert.equal(pga.status.state, "in");
  assert.deepEqual(pga.competitors, [], "golf ranking events must emit competitors: []");
  assert.ok(pga.ranking && pga.ranking.length === 10);
  assert.equal(pga.ranking[0].pos, 1);
  assert.match(pga.ranking[0].detail || "", /^([+-]\d+|E)$/, "golf detail must be total-to-par only");
  assert.ok(!pga.ranking[0].detail?.includes("("), "golf detail must not include round scores");
  assert.match(pga.ranking[0].secondary || "", /^(thru \d+|F|CUT|WD)$/, "golf secondary must carry thru/F/CUT/WD");
  // Verify ties share identical pos (e.g. multiple players tied at 2nd all have pos === 2)
  const tiedAtSecond = pga.ranking.filter((r) => r.pos === 2);
  assert.ok(tiedAtSecond.length >= 2, `expected tied players at pos 2, got ${JSON.stringify(pga.ranking.slice(0, 6))}`);

  // UFC selects Main Event (last bout in ESPN payload)
  const ufc = body.events.find((e) => e.league.id === "ufc")!;
  assert.equal(ufc.competitors?.length, 2);
  assert.ok(ufc.competitors!.some((c) => c.name.includes("Procházka")));
  assert.ok(ufc.competitors!.some((c) => c.name.includes("Stirling") && c.winner));

  // Verify fetchMany was used with concurrency <= 6
  assert.ok(host.batchCalls.length >= 1);
  for (const call of host.batchCalls) {
    assert.ok(call.concurrency !== undefined && call.concurrency <= 6);
  }
});

test("maps recorded NFL, NBA, and EPL summaries with tables, plays, and 0..1 probabilitySeries", () => {
  const host = createMockHost();
  const handle = createSportsHandler({ getEther: () => host.ether, now: host.now });

  // NFL detail
  const nflResp = handle({ method: "GET", path: "/ether/v1/live/sports/events/nfl-401873066" });
  assert.equal(nflResp.status, 200);
  const nfl = nflResp.body as LiveEventDetail;
  assert.equal(nfl.id, "nfl-401873066");
  assert.ok(nfl.tables && nfl.tables.length >= 3);
  assert.equal(nfl.tables[0].title, "Line score");
  assert.ok(nfl.tables.some((t) => t.title === "Team stats"));
  assert.ok(nfl.tables.some((t) => t.title.toLowerCase().includes("passing")));
  assert.ok(nfl.plays && nfl.plays.length > 0);
  assert.ok(nfl.probability && nfl.probability.value >= 0 && nfl.probability.value <= 1);
  assert.ok(nfl.probabilitySeries && nfl.probabilitySeries.length >= 10);
  for (let i = 0; i < nfl.probabilitySeries.length; i++) {
    const pt = nfl.probabilitySeries[i];
    assert.equal(typeof pt.t, "number", "binding Vela wire shape prefers 0..1 number for t");
    assert.ok((pt.t as number) >= 0 && (pt.t as number) <= 1);
    assert.ok(pt.value >= 0 && pt.value <= 1);
    if (i > 0) {
      assert.ok((pt.t as number) >= (nfl.probabilitySeries[i - 1].t as number));
    }
  }
  assert.equal(nfl.probabilitySeries[0].t, 0);
  assert.equal(nfl.probabilitySeries[nfl.probabilitySeries.length - 1].t, 1);

  // NBA detail (boxscore + plays + winprobability)
  const nbaResp = handle({ method: "GET", path: "/ether/v1/live/sports/events/nba-401910023" });
  assert.equal(nbaResp.status, 200);
  const nba = nbaResp.body as LiveEventDetail;
  assert.ok(nba.tables?.some((t) => t.title.toLowerCase().includes("box score")));
  assert.ok(nba.plays && nba.plays.length > 0);
  assert.ok(nba.probabilitySeries && nba.probabilitySeries.length > 0);

  // EPL live detail (keyEvents -> plays with yellow card emphasis="key")
  const eplResp = handle({ method: "GET", path: "/ether/v1/live/sports/events/epl-401879234" });
  assert.equal(eplResp.status, 200);
  const epl = eplResp.body as LiveEventDetail;
  assert.equal(epl.status.state, "in");
  assert.ok(epl.plays?.some((p) => p.emphasis === "key" && /yellow card/i.test(p.text)));

  // Accepts colon-namespaced IDs defensively
  const namespaced = handle({
    method: "GET",
    path: "/ether/v1/live/sports/events/org.ether.sports:sports:nfl-401873066",
  });
  assert.equal(namespaced.status, 200);

  // F1, PGA, and UFC build detail tables from scoreboard without calling 404 /summary
  const urlsBeforeF1 = host.urls.length;
  const f1Resp = handle({ method: "GET", path: "/ether/v1/live/sports/events/f1-600057449" });
  assert.equal(f1Resp.status, 200);
  const f1 = f1Resp.body as LiveEventDetail;
  const raceTable = f1.tables?.find((t) => t.title === "Race results");
  assert.ok(raceTable, "F1 detail should include Race results table");
  assert.deepEqual(raceTable!.columns, ["Pos", "Driver", "Constructor", "Time / Gap", "Pts"]);
  assert.equal(raceTable!.rows[0][1], "Max Verstappen");
  assert.equal(raceTable!.rows[0][2], "Red Bull");
  assert.match(raceTable!.rows[0][3], /^\d+:\d+:\d+\.\d+$/);
  assert.equal(raceTable!.rows[0][4], "25");
  assert.ok(f1.tables?.some((t) => t.title === "Weekend sessions"));
  const f1Urls = host.urls.slice(urlsBeforeF1);
  assert.ok(f1Urls.every((u) => !u.includes("/summary")), "F1 must not call ESPN /summary (404)");

  // PGA detail table shows tie-aware Pos (T2), clean Score, Thru, and rounds
  const pgaId = (handle({ method: "GET", path: "/ether/v1/live/sports/events?league=pga" }).body as EventsResponse).events[0].id;
  const pgaDetailResp = handle({ method: "GET", path: `/ether/v1/live/sports/events/${pgaId}` });
  assert.equal(pgaDetailResp.status, 200);
  const pgaDetail = pgaDetailResp.body as LiveEventDetail;
  const lbTable = pgaDetail.tables?.find((t) => t.title === "Leaderboard");
  assert.ok(lbTable, "PGA detail should include Leaderboard table");
  assert.equal(lbTable!.columns[0], "Pos");
  assert.equal(lbTable!.columns[2], "Score");
  assert.equal(lbTable!.columns[3], "Thru");
  assert.ok(lbTable!.rows.some((r) => r[0] === "T2"), `expected T2 ties in PGA leaderboard table, got ${JSON.stringify(lbTable!.rows.slice(0, 6))}`);

  // Unknown event returns 404
  const notFound = handle({ method: "GET", path: "/ether/v1/live/sports/events/unknown-123" });
  assert.equal(notFound.status, 404);
});

test("maps teams/entities and standings (including sorting unsorted ESPN NBA standings)", () => {
  const host = createMockHost({
    settings: { leagues: ["nfl", "epl", "f1"], region: "us" },
  });
  const handle = createSportsHandler({ getEther: () => host.ether, now: host.now });

  // Entities across NFL, EPL, and F1 constructors
  const allEnt = handle({ method: "GET", path: "/ether/v1/live/sports/entities?kind=team" });
  assert.equal(allEnt.status, 200);
  const entities = (allEnt.body as EntitiesResponse).entities;
  assert.ok(entities.some((e) => e.id === "nfl-21" && e.name === "Philadelphia Eagles" && e.color?.startsWith("#")));
  assert.ok(entities.some((e) => e.league === "epl" && e.abbr === "ARS"));
  assert.ok(entities.some((e) => e.league === "f1" && e.name === "Mercedes"));

  // Entity search query q=
  const filtered = handle({
    method: "GET",
    path: "/ether/v1/live/sports/entities",
    query: { kind: "team", q: "eagles" },
  });
  assert.equal((filtered.body as EntitiesResponse).entities.length, 1);
  assert.equal((filtered.body as EntitiesResponse).entities[0].abbr, "PHI");

  // Unsupported entity kind returns empty list
  const emptyKind = handle({
    method: "GET",
    path: "/ether/v1/live/sports/entities",
    query: { kind: "stadium" },
  });
  assert.deepEqual((emptyKind.body as EntitiesResponse).entities, []);

  // NBA standings fixture is intentionally unsorted (seed 15 first, seed 1 last);
  // verify the plugin sorts by playoffSeed ascending.
  const nbaSt = handle({
    method: "GET",
    path: "/ether/v1/live/sports/standings",
    query: { league: "nba" },
  });
  assert.equal(nbaSt.status, 200);
  const nbaTables = (nbaSt.body as StandingsResponse).tables;
  assert.equal(nbaTables.length, 2);
  const east = nbaTables.find((t) => t.title.includes("Eastern"))!;
  assert.equal(east.rows[0][0], "1");
  assert.equal(east.rows[0][1], "Philadelphia 76ers");
  assert.equal(east.rows[east.rows.length - 1][0], "15");
  assert.equal(east.rows[east.rows.length - 1][1], "Milwaukee Bucks");

  // EPL standings
  const eplSt = handle({
    method: "GET",
    path: "/ether/v1/live/sports/standings",
    query: { league: "epl" },
  });
  const eplTables = (eplSt.body as StandingsResponse).tables;
  assert.equal(eplTables.length, 1);
  assert.deepEqual(eplTables[0].columns, ["#", "Team", "GP", "W", "D", "L", "GF", "GA", "GD", "PTS"]);
  assert.equal(eplTables[0].rows[0][1], "Arsenal");

  // F1 standings uses compact Driver / Constructor tables
  const f1St = handle({
    method: "GET",
    path: "/ether/v1/live/sports/standings",
    query: { league: "f1" },
  });
  const f1Tables = (f1St.body as StandingsResponse).tables;
  assert.equal(f1Tables.length, 2);
  assert.deepEqual(f1Tables[0].columns, ["Pos", "Driver", "PTS"]);
  assert.deepEqual(f1Tables[1].columns, ["Pos", "Constructor", "PTS"]);
});

test("close-game heuristics match spec across football, basketball (4Q and 2H), hockey, soccer, baseball", () => {
  const mk = (a: string, b: string) => [
    { id: "t1", name: "A", abbr: "AAA", score: a },
    { id: "t2", name: "B", abbr: "BBB", score: b },
  ];

  // Football: Q4 under 5 min within 8 pts (or OT within 8)
  assert.equal(
    isCloseGame({ category: "football", state: "in", period: 4, regulationPeriods: 4, clock: "4:59", competitors: mk("24", "17") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "football", state: "in", period: 4, regulationPeriods: 4, clock: "5:00", competitors: mk("24", "21") }),
    false,
  );
  assert.equal(
    isCloseGame({ category: "football", state: "in", period: 4, regulationPeriods: 4, clock: "1:30", competitors: mk("30", "21") }),
    false,
  );
  assert.equal(
    isCloseGame({ category: "football", state: "in", period: 5, regulationPeriods: 4, clock: "8:00", competitors: mk("27", "24") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "football", state: "post", period: 4, regulationPeriods: 4, clock: "0:00", competitors: mk("24", "21") }),
    false,
  );

  // Basketball: NBA (4Q) & NCAAM (2H) within 5 pts in last 3 min, including under-1-min decimal clocks
  assert.equal(
    isCloseGame({ category: "basketball", state: "in", period: 4, regulationPeriods: 4, clock: "2:59", competitors: mk("108", "103") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "basketball", state: "in", period: 4, regulationPeriods: 4, clock: "42.8", competitors: mk("108", "106") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "basketball", state: "in", period: 4, regulationPeriods: 4, clock: "3:01", competitors: mk("108", "106") }),
    false,
  );
  assert.equal(
    isCloseGame({ category: "basketball", state: "in", period: 4, regulationPeriods: 4, clock: "1:00", competitors: mk("110", "104") }),
    false,
  );
  assert.equal(
    isCloseGame({ category: "basketball", state: "in", period: 2, regulationPeriods: 2, clock: "2:15", competitors: mk("72", "70") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "basketball", state: "in", period: 1, regulationPeriods: 2, clock: "0:45", competitors: mk("35", "34") }),
    false,
  );

  // Hockey: tied or within 1 in the 3rd period (or OT)
  assert.equal(
    isCloseGame({ category: "hockey", state: "in", period: 3, regulationPeriods: 3, clock: "18:00", competitors: mk("2", "2") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "hockey", state: "in", period: 3, regulationPeriods: 3, clock: "1:05", competitors: mk("3", "2") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "hockey", state: "in", period: 3, regulationPeriods: 3, clock: "1:05", competitors: mk("4", "2") }),
    false,
  );
  assert.equal(
    isCloseGame({ category: "hockey", state: "in", period: 2, regulationPeriods: 3, clock: "0:10", competitors: mk("2", "2") }),
    false,
  );

  // Soccer: level after 80'
  assert.equal(
    isCloseGame({ category: "soccer", state: "in", period: 2, regulationPeriods: 2, clock: "80'", competitors: mk("1", "1") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "soccer", state: "in", period: 2, regulationPeriods: 2, clock: "90'+3'", competitors: mk("0", "0") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "soccer", state: "in", period: 2, regulationPeriods: 2, clock: "79'", competitors: mk("1", "1") }),
    false,
  );
  assert.equal(
    isCloseGame({ category: "soccer", state: "in", period: 2, regulationPeriods: 2, clock: "86'", competitors: mk("2", "1") }),
    false,
  );

  // Baseball: 9th or extras within 2
  assert.equal(
    isCloseGame({ category: "baseball", state: "in", period: 9, regulationPeriods: 9, competitors: mk("4", "3") }),
    true,
  );
  assert.equal(
    isCloseGame({ category: "baseball", state: "in", period: 8, regulationPeriods: 9, competitors: mk("4", "3") }),
    false,
  );
});

test("region preference filters broadcasts and falls back cleanly when region has no match", () => {
  const geo = [
    { type: { shortName: "TV" }, media: { shortName: "CBS" }, region: "us" },
    { type: { shortName: "Subscription" }, media: { shortName: "ESPN+" }, region: "us" },
    { type: { shortName: "TV" }, media: { shortName: "TSN" }, region: "ca" },
    { type: { shortName: "TV" }, media: { shortName: "Sportsnet" }, region: "ca" },
  ];
  const ca = extractBroadcasts(geo, undefined, "ca");
  assert.deepEqual(ca.map((b) => b.name), ["TSN", "Sportsnet"]);

  const us = extractBroadcasts(geo, undefined, "us");
  assert.deepEqual(us, [
    { name: "CBS", kind: "tv", region: "us" },
    { name: "ESPN+", kind: "stream", region: "us" },
  ]);

  // No GB broadcasts listed -> falls back to all broadcasts so Live TV matcher still has callsigns
  const gb = extractBroadcasts(geo, undefined, "gb");
  assert.equal(gb.length, 4);

  const all = extractBroadcasts(geo, undefined, "all");
  assert.equal(all.length, 4);
});

test("caches live scoreboards for 15 s and non-live scoreboards for 5 min, and avoids range queries on team sports", () => {
  const host = createMockHost({
    settings: { leagues: ["nfl", "epl"], region: "us" },
    // Set well before NFL 18:00Z kickoff so NFL is idle (5 min TTL) while EPL is live (15 s TTL).
    nowMs: Date.parse("2026-11-22T15:00:00.000Z"),
  });
  const handle = createSportsHandler({ getEther: () => host.ether, now: host.now });

  // Initial fetch hits both leagues
  handle({ method: "GET", path: "/ether/v1/live/sports/events" });
  const initialCount = host.urls.length;
  assert.equal(initialCount, 2);

  // At +10s: both EPL (live 15s) and NFL (idle 5m) hit KV cache
  host.advance(10_000);
  handle({ method: "GET", path: "/ether/v1/live/sports/events" });
  assert.equal(host.urls.length, initialCount);

  // At +16s: EPL expires and refetches, while NFL stays cached!
  host.advance(6_000);
  handle({ method: "GET", path: "/ether/v1/live/sports/events" });
  assert.equal(host.urls.length, initialCount + 1);
  assert.ok(host.urls[host.urls.length - 1].includes("soccer/eng.1/scoreboard"));

  // At +301s from start: NFL expires and refetches too
  host.advance(290_000);
  handle({ method: "GET", path: "/ether/v1/live/sports/events" });
  assert.equal(host.urls.length, initialCount + 3);

  // Multi-day date window on team sport fans out single days (never sends HTTP-400 range)
  const beforeRange = host.urls.length;
  const multi = handle({
    method: "GET",
    path: "/ether/v1/live/sports/events",
    query: { league: "nfl", from: "2026-11-22", to: "2026-11-24" },
  });
  assert.equal(multi.status, 200);
  const rangeUrls = host.urls.slice(beforeRange);
  assert.equal(rangeUrls.length, 3);
  assert.ok(rangeUrls.every((u) => !u.includes("-")));

  // ids= narrows upstream fetches to only the referenced league
  const beforeIds = host.urls.length;
  host.kv.clear();
  const byId = handle({
    method: "GET",
    path: "/ether/v1/live/sports/events",
    query: { ids: "nfl-401873066" },
  });
  assert.equal(byId.status, 200);
  assert.equal((byId.body as EventsResponse).events.length, 1);
  assert.equal(host.urls.slice(beforeIds).length, 1);
  assert.ok(host.urls[beforeIds].includes("football/nfl/scoreboard"));
});

test("propagates fatal host errors, serves stale cache on transient errors, and supports custom providers", () => {
  // Fatal host errors (check_dependency / forbidden) must propagate
  const fatalHost = createMockHost({
    httpOverride: () => ({ error: { code: "check_dependency", message: "offline check" } }),
  });
  const fatalHandle = createSportsHandler({ getEther: () => fatalHost.ether, now: fatalHost.now });
  assert.throws(
    () => fatalHandle({ method: "GET", path: "/ether/v1/live/sports/events" }),
    (err: any) => err?.code === "check_dependency",
  );

  // Stale cache served when upstream transiently returns 503
  let failUpstream = false;
  const resilientHost = createMockHost({
    settings: { leagues: ["epl"], region: "us" },
    httpOverride: () => (failUpstream ? { status: 503, headers: {}, body: "unavailable" } : null),
  });
  const resilientHandle = createSportsHandler({
    getEther: () => resilientHost.ether,
    now: resilientHost.now,
  });
  const warm = resilientHandle({ method: "GET", path: "/ether/v1/live/sports/events" });
  assert.equal((warm.body as EventsResponse).events.length, 3);
  failUpstream = true;
  resilientHost.advance(20_000); // expire 15s live cache
  const fallback = resilientHandle({ method: "GET", path: "/ether/v1/live/sports/events" });
  assert.equal(fallback.status, 200);
  assert.equal((fallback.body as EventsResponse).events.length, 3);

  // Configure validation & test & custom provider injection
  const customProvider: SportsProvider = {
    id: "custom",
    name: "Custom",
    getEvents: () => ({ events: [], nextPollSec: 300 }),
    getEventDetail: () => null,
    getEntities: () => [],
    getStandings: () => [],
    healthCheck: () => ({ ok: true, detail: "Custom OK" }),
  };
  const customHandle = createSportsHandler({
    provider: customProvider,
    getEther: () => resilientHost.ether,
    now: resilientHost.now,
  });
  const health = customHandle({ method: "GET", path: "/ether/health" });
  assert.equal((health.body as any).provider, "custom");

  const testOk = customHandle({ method: "POST", path: "/ether/test" });
  assert.equal(testOk.status, 200);
  assert.equal((testOk.body as any).ok, true);

  const badCfg = customHandle({
    method: "POST",
    path: "/ether/configure",
    body: { leagues: [], region: "mars" },
  });
  assert.equal(badCfg.status, 400);
  assert.ok((badCfg.body as any).error.fields["/leagues"]);
  assert.ok((badCfg.body as any).error.fields["/region"]);

  const goodCfg = customHandle({
    method: "POST",
    path: "/ether/configure",
    body: { leagues: ["nfl", "epl", "f1"], region: "ca" },
  });
  assert.equal(goodCfg.status, 200);
  assert.deepEqual((goodCfg.body as any).settings, {
    leagues: ["nfl", "epl", "f1"],
    region: "ca",
  });
});

test("maps live in-progress NFL situation, probability, status.close and ranks close games first", () => {
  // Derive an in-progress Q4 close game from the recorded NFL scoreboard fixture.
  const nflRaw = JSON.parse(FIXTURES["football/nfl/scoreboard"]);
  const liveEv = structuredClone(nflRaw.events.find((e: any) => e.id === "401873076"));
  liveEv.status = {
    clock: 134,
    displayClock: "2:14",
    period: 4,
    type: {
      name: "STATUS_IN_PROGRESS",
      state: "in",
      completed: false,
      description: "In Progress",
      detail: "2:14 - 4th",
      shortDetail: "2:14 - 4th",
    },
  };
  const comp = liveEv.competitions[0];
  comp.status = liveEv.status;
  const home = comp.competitors.find((c: any) => c.homeAway === "home");
  const away = comp.competitors.find((c: any) => c.homeAway === "away");
  home.score = "24";
  away.score = "21";
  comp.situation = {
    shortDownDistanceText: "3rd & 6",
    possessionText: "PIT 18",
    possession: home.id,
    isRedZone: true,
    lastPlay: {
      text: "J. Hurts pass to A. Brown for 14 yds",
      probability: { homeWinPercentage: 0.72 },
    },
  };
  nflRaw.events = [liveEv];

  const host = createMockHost({
    settings: { leagues: ["epl", "nfl"], region: "us" },
    httpOverride: (req) =>
      req.url.includes("football/nfl/scoreboard")
        ? { status: 200, headers: {}, body: JSON.stringify(nflRaw) }
        : null,
  });
  const handle = createSportsHandler({ getEther: () => host.ether, now: host.now });

  const resp = handle({ method: "GET", path: "/ether/v1/live/sports/events" });
  assert.equal(resp.status, 200);
  const { events, nextPollSec } = resp.body as EventsResponse;
  assert.equal(nextPollSec, 15);
  // Close NFL game must bubble ahead of non-close live EPL game!
  assert.equal(events[0].id, "nfl-401873076");
  assert.equal(events[0].status.state, "in");
  assert.equal(events[0].status.detail, "Q4 · 2:14");
  assert.equal(events[0].status.period, 4);
  assert.equal(events[0].status.clock, "2:14");
  assert.equal(events[0].status.close, true);
  assert.deepEqual(events[0].situation, {
    Down: "3rd & 6",
    "Ball on": "PIT 18",
    Possession: "PHI",
    Zone: "Red Zone",
    Clock: "Q4 · 2:14",
  });
  assert.deepEqual(events[0].probability, {
    label: "PHI win",
    value: 0.72,
    competitorId: `nfl-${home.id}`,
  });
});

test("uses proper short league abbreviations, dedupes identical location/name match terms, and maps live NBA/NHL situation + ids entity filter", () => {
  const byId = new Map(DEFAULT_LEAGUES.map((l) => [l.id, l]));
  assert.equal(byId.get("laliga")?.abbr, "La Liga");
  assert.equal(byId.get("bundesliga")?.abbr, "Bundesliga");
  assert.equal(byId.get("seriea")?.abbr, "Serie A");
  assert.equal(byId.get("ligue1")?.abbr, "Ligue 1");
  assert.equal(byId.get("epl")?.abbr, "EPL");
  assert.equal(byId.get("ucl")?.abbr, "UCL");

  // NBA live situation (bonus + timeouts + clock) and NHL live situation (power play + clock)
  const nbaRaw = JSON.parse(FIXTURES["basketball/nba/scoreboard"]);
  const nbaLive = structuredClone(nbaRaw.events[0]);
  nbaLive.status = {
    clock: 252,
    displayClock: "4:12",
    period: 3,
    type: { name: "STATUS_IN_PROGRESS", state: "in", detail: "4:12 - 3rd" },
  };
  nbaLive.competitions[0].status = nbaLive.status;
  nbaLive.competitions[0].situation = {
    homeBonus: true,
    awayTimeouts: 4,
    homeTimeouts: 5,
    probability: { homeWinPercentage: 0.64 },
  };
  nbaRaw.events = [nbaLive];

  const nhlRaw = JSON.parse(FIXTURES["hockey/nhl/scoreboard"]);
  const nhlLive = structuredClone(nhlRaw.events[0]);
  nhlLive.status = {
    clock: 665,
    displayClock: "11:05",
    period: 2,
    type: { name: "STATUS_IN_PROGRESS", state: "in", detail: "11:05 - 2nd" },
  };
  nhlLive.competitions[0].status = nhlLive.status;
  nhlLive.competitions[0].situation = {
    awayPowerPlay: true,
  };
  nhlRaw.events = [nhlLive];

  const host = createMockHost({
    settings: { leagues: ["nba", "nhl", "nfl", "epl"], region: "us" },
    httpOverride: (req) => {
      if (req.url.includes("basketball/nba/scoreboard")) {
        return { status: 200, headers: {}, body: JSON.stringify(nbaRaw) };
      }
      if (req.url.includes("hockey/nhl/scoreboard")) {
        return { status: 200, headers: {}, body: JSON.stringify(nhlRaw) };
      }
      return null;
    },
  });
  const handle = createSportsHandler({ getEther: () => host.ether, now: host.now });

  const resp = handle({ method: "GET", path: "/ether/v1/live/sports/events?league=nba,nhl" });
  assert.equal(resp.status, 200);
  const evs = (resp.body as EventsResponse).events;
  const nbaEv = evs.find((e) => e.league.id === "nba")!;
  const nhlEv = evs.find((e) => e.league.id === "nhl")!;
  assert.ok(nbaEv.situation?.Bonus);
  assert.ok(nbaEv.situation?.Timeouts?.includes("4"));
  assert.equal(nbaEv.situation?.Clock, "Q3 · 4:12");
  assert.equal(nbaEv.probability?.value, 0.64);
  assert.ok(nhlEv.situation?.["Power play"]);
  assert.equal(nhlEv.situation?.Clock, "2nd · 11:05");

  // Entities ?ids= fetches only referenced leagues and returns matching IDs
  const urlsBefore = host.urls.length;
  const byIds = handle({
    method: "GET",
    path: "/ether/v1/live/sports/entities",
    query: { kind: "team", ids: "nfl-21,epl-359" },
  });
  assert.equal(byIds.status, 200);
  const entList = (byIds.body as EntitiesResponse).entities;
  assert.deepEqual(
    entList.map((e) => e.id).sort(),
    ["epl-359", "nfl-21"],
  );
  const fetched = host.urls.slice(urlsBefore);
  assert.equal(fetched.length, 2, "ids= query should fetch only referenced leagues");

  // Abbreviated ESPN broadcast names expand to both abbreviated and full channel names in matchHints.callsigns.
  const abbrevHints = buildMatchHints([], undefined, undefined, [
    { name: "USA Net", kind: "tv" },
    { name: "Tele", kind: "tv" },
    { name: "Golf Chnl", kind: "tv" },
    { name: "beIN Sports", kind: "tv" },
    { name: "FS1", kind: "tv" },
  ]);
  for (const want of [
    "USA Net",
    "USA Network",
    "Tele",
    "Telemundo",
    "Golf Chnl",
    "Golf Channel",
    "beIN Sports",
    "FS1",
    "FOX Sports 1",
  ]) {
    assert.ok(
      abbrevHints.callsigns.includes(want),
      `expected callsigns to include ${want}, got ${JSON.stringify(abbrevHints.callsigns)}`,
    );
  }
});
