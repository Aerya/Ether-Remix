import { describe, expect, it } from "vitest";
import type { Rule } from "./contract";
import { formatBytes, formatCompactNumber, formatNanos, relativeTime } from "./format";
import {
  cacheHealth,
  catalogHealth,
  computeHealth,
  transcodeHealth,
  countsOverall,
  decisionLabel,
  indexersHealth,
  normalizeSessions,
  overallLevel,
  plexDirectHealth,
  remoteHealth,
  usenetHealth,
  worstLevel,
} from "./health";
import { COALESCE_MS, historyReducer, initHistory } from "./history";
import {
  blankSpec,
  describeRule,
  isRuleComplete,
  newRule,
  normalizeSpec,
  previewableSpec,
  specProblems,
  valueFor,
  withOp,
  FIELD_BY_NAME,
} from "./rules";

describe("rules", () => {
  it("creates field-appropriate defaults", () => {
    expect(newRule("genre")).toEqual({ field: "genre", op: "in", value: [] });
    expect(newRule("rating")).toEqual({ field: "rating", op: "gte", value: 7 });
    expect(newRule("released")).toEqual({ field: "released", op: "within", value: "30d" });
    expect(newRule("latestSeasonPremiere")).toEqual({
      field: "latestSeasonPremiere",
      op: "within",
      value: "30d",
    });
    expect(newRule("latestEpisode")).toEqual({
      field: "latestEpisode",
      op: "within",
      value: "30d",
    });
    expect(newRule("watched")).toEqual({ field: "watched", op: "is", value: true });
  });

  it("converts values when the operator changes", () => {
    const year: Rule = { field: "year", op: "gte", value: 1990 };
    const between = withOp(year, "between");
    expect(between.value).toEqual([1990, new Date().getFullYear()]);
    expect(withOp(between, "lte").value).toBe(1990);
    const genre: Rule = { field: "genre", op: "in", value: ["Horror", "Thriller"] };
    expect(withOp(genre, "notIn").value).toEqual(["Horror", "Thriller"]);
    expect(valueFor(FIELD_BY_NAME.released, "between", "2020-01-01")).toEqual([
      "2020-01-01",
      expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
    ]);
  });

  it("knows when a rule is complete", () => {
    expect(isRuleComplete({ field: "genre", op: "in", value: [] })).toBe(false);
    expect(isRuleComplete({ field: "genre", op: "in", value: ["Drama"] })).toBe(true);
    expect(isRuleComplete({ field: "rating", op: "between", value: [6, ""] })).toBe(false);
    expect(isRuleComplete({ field: "person", op: "is", value: "" })).toBe(false);
    expect(isRuleComplete({ field: "watched", op: "is", value: false })).toBe(true);
  });

  it("describes rules in plain language using labels for ids", () => {
    expect(describeRule({ field: "genre", op: "in", value: ["Horror", "Thriller"] })).toBe(
      "Genre is any of Horror, Thriller",
    );
    expect(
      describeRule({ field: "person", op: "in", value: [1, 2, 3, 4], label: "A, B, C, D" }),
    ).toBe("Person is any of A, B and 2 more");
    expect(describeRule({ field: "runtime", op: "lte", value: 90 })).toBe(
      "Runtime is at most 90 min",
    );
    expect(describeRule({ field: "released", op: "within", value: "6m" })).toBe(
      "Released in the last 6 months",
    );
    expect(describeRule({ field: "latestSeasonPremiere", op: "within", value: "180d" })).toBe(
      "Latest season premiere in the last 180 days",
    );
    expect(describeRule({ field: "latestEpisode", op: "gte", value: "2026-06-01" })).toBe(
      "Latest aired episode on or after 2026-06-01",
    );
    expect(describeRule({ field: "watched", op: "is", value: false })).toBe(
      "Not watched by the viewer",
    );
  });

  it("previews only the complete parts of a spec", () => {
    const spec = {
      ...blankSpec(),
      sources: [{ kind: "tmdbPerson" as const, id: 0 }],
      rules: [newRule("genre"), newRule("rating")],
    };
    const p = previewableSpec(spec);
    expect(p.sources).toEqual([{ kind: "catalog" }]);
    expect(p.rules).toEqual([{ field: "rating", op: "gte", value: 7 }]);
  });

  it("lists problems that block saving", () => {
    expect(specProblems(blankSpec())).toEqual(["Give the collection a name."]);
    const bad = {
      ...blankSpec(),
      name: "X",
      rules: [newRule("genre")],
      artwork: { mode: "custom" as const },
    };
    expect(specProblems(bad)).toEqual([
      "Finish or remove the incomplete rule.",
      "Add an image URL for custom artwork.",
    ]);
  });

  it("normalizes partial specs from the server", () => {
    const s = normalizeSpec({ name: "N" });
    expect(s.browseInApps).toBe(true);
    expect(s.rules).toEqual([]);
    expect(s.sources).toEqual([{ kind: "catalog" }]);
  });
});

describe("undo history", () => {
  it("records steps, coalesces rapid edits to the same field, and undoes/redoes", () => {
    let h = initHistory("a");
    h = historyReducer(h, { type: "set", value: "ab", key: "name", at: 1000 });
    h = historyReducer(h, { type: "set", value: "abc", key: "name", at: 1000 + COALESCE_MS / 2 });
    expect(h.past).toEqual(["a"]);
    h = historyReducer(h, { type: "set", value: "abcd", key: "name", at: 1000 + COALESCE_MS * 3 });
    expect(h.past).toEqual(["a", "abc"]);
    h = historyReducer(h, { type: "undo" });
    expect(h.present).toBe("abc");
    h = historyReducer(h, { type: "redo" });
    expect(h.present).toBe("abcd");
    h = historyReducer(h, { type: "undo" });
    h = historyReducer(h, { type: "set", value: "x", at: 99999 });
    expect(h.future).toEqual([]);
  });
  it("ignores undo with nothing to undo and identical sets", () => {
    const h = initHistory(1);
    expect(historyReducer(h, { type: "undo" })).toBe(h);
    expect(historyReducer(h, { type: "set", value: 1, at: 0 })).toBe(h);
  });
});

const LIVE = {
  cache: { budget: 21474836480, used: 970344835 },
  catalog: {
    movies: 24929,
    shows: 24298,
    browsableMovies: 24928,
    browsableShows: 24298,
    lastSync: "2026-10-19T13:16:36Z",
    sync: { running: false },
  },
  indexers: [
    {
      indexer: "NZBgeek",
      hitsUsed: 6,
      hitsLimit: 200,
      hitsRemaining: 194,
      grabsUsed: 10,
      grabsLimit: 50,
      grabsRemaining: 40,
    },
    {
      indexer: "DOGnzb",
      hitsUsed: 20,
      hitsLimit: 20,
      hitsRemaining: 0,
      grabsUsed: 0,
      grabsLimit: 10,
      grabsRemaining: 10,
    },
  ],
  providers: [
    {
      id: "p1",
      name: "Giganews",
      backfill: false,
      state: "ok",
      configuredConnections: 16,
      activeConnections: 4,
    },
    {
      id: "p2",
      name: "Frugal",
      backfill: false,
      state: "backoff",
      configuredConnections: 16,
      activeConnections: 0,
    },
  ],
  remote: {
    claimed: true,
    ownerUsername: "o",
    remoteAccess: false,
    listed: true,
    presence: { connected: true },
    cert: {
      present: true,
      verified: true,
      subject: "*.x.plex.direct",
      notBefore: "2026-01-17T04:36:33Z",
      notAfter: "2027-01-17T04:36:33Z",
    },
  },
};

describe("health", () => {
  const now = new Date("2026-10-19T14:00:00Z");
  it("summarises the live dashboard shapes", () => {
    const h = computeHealth(LIVE, now);
    expect(h.map((x) => [x.id, x.level])).toEqual([
      ["usenet", "warn"],
      ["indexers", "warn"],
      ["cache", "ok"],
      ["catalog", "ok"],
      ["remote", "off"],
      ["plexdirect", "ok"],
    ]);
    expect(usenetHealth(LIVE.providers).fill).toBeCloseTo(4 / 32);
    expect(indexersHealth(LIVE.indexers).summary).toBe("1 of 2 indexers out of searches today");
    expect(cacheHealth(LIVE.cache).summary).toBe("925.4 MiB of 20.0 GiB");
    expect(worstLevel(h.map((x) => x.level))).toBe("warn");
    expect(h.find((x) => x.id === "plexdirect")?.statusLabel).toBe("Valid");
    expect(overallLevel(h)).toBe("warn");
    expect(transcodeHealth(undefined)).toBeNull();
    const tone = transcodeHealth({
      playbackError: "This FFmpeg cannot tone-map HDR video to SDR",
      notice: "filter list unread",
    });
    expect(tone?.level).toBe("warn");
    expect(tone?.summary).toContain("tone-map");
    expect(tone?.details).toEqual(["filter list unread"]);
    expect(
      computeHealth({ ...LIVE, hwaccel: { notice: "no colours" } }, now).some(
        (x) => x.id === "transcode",
      ),
    ).toBe(true);
  });
  it("accepts debrid in place of Usenet", () => {
    expect(usenetHealth([], { enabled: true }).level).toBe("off");
    const emptySources = usenetHealth([], { enabled: false });
    expect(emptySources.setupGap).toBe(true);
    expect(emptySources.level).toBe("unknown");
    expect(emptySources.countsOverall).toBe(false);
  });
  it("warns when debrid has no torrent indexer or nothing is enabled", () => {
    const nzb = { indexer: "NZBgeek", hitsLimit: 0, enabled: true, torrent: false };
    const tz = { indexer: "Torznab", hitsLimit: 0, enabled: true, torrent: true };
    const debrid = { enabled: true };
    const noTorrent = indexersHealth([nzb], debrid);
    expect(noTorrent.level).toBe("warn");
    expect(noTorrent.summary).toBe("No torrent indexer for debrid");
    expect(noTorrent.details[0]).toContain("Stremio addon or Torznab");
    // A disabled torrent indexer does not count.
    expect(indexersHealth([nzb, { ...tz, enabled: false }], debrid).level).toBe("warn");
    expect(indexersHealth([nzb, tz], debrid).level).toBe("ok");
    // Without debrid, Usenet-only indexers are fine.
    expect(indexersHealth([nzb], { enabled: false }).level).toBe("ok");
    expect(indexersHealth([nzb]).level).toBe("ok");
    const off = indexersHealth([{ ...nzb, enabled: false }]);
    expect([off.level, off.summary]).toEqual(["warn", "No indexers enabled"]);
    expect(
      computeHealth({ indexers: [nzb], debrid }, now).find((x) => x.id === "indexers")?.level,
    ).toBe("warn");
  });
  it("surfaces missing site definitions, paused indexers, and simultaneous-IP NNTP refusals", () => {
    const ipLimitSingle = usenetHealth([
      {
        id: "p1",
        name: "Giganews",
        backfill: false,
        state: "backoff",
        lastError: "nntp: 481 max number of simultaneous IP addresses reached",
        configuredConnections: 20,
        activeConnections: 0,
      },
    ]);
    expect(ipLimitSingle.level).toBe("error");
    expect(ipLimitSingle.statusLabel).toBe("Needs attention");
    expect(ipLimitSingle.summary).toBe("Account in use from another IP address.");

    const ipLimitPartial = usenetHealth([
      {
        id: "p1",
        name: "Giganews",
        backfill: false,
        state: "backoff",
        lastError: "nntp: 481 max number of simultaneous IP addresses reached",
        configuredConnections: 20,
        activeConnections: 0,
      },
      {
        id: "p2",
        name: "Frugal",
        backfill: false,
        state: "ok",
        configuredConnections: 20,
        activeConnections: 4,
      },
    ]);
    expect(ipLimitPartial.level).toBe("warn");
    expect(ipLimitPartial.details[0]).toContain(
      "Giganews: Account in use from another IP address.",
    );

    const healthyNzb = { indexer: "NZBgeek", hitsLimit: 100, hitsRemaining: 90, enabled: true };
    const missingSite = {
      indexer: "TorrentGalaxy",
      hitsLimit: 0,
      enabled: true,
      torrent: true,
      missingDefinition: true,
    };
    const partialMissing = indexersHealth([healthyNzb, missingSite], { enabled: false }, now);
    expect(partialMissing.level).toBe("warn");
    expect(partialMissing.summary).toBe("1 of 2 indexers missing their definition");
    expect(partialMissing.details).toContain("TorrentGalaxy: definition missing");

    // When the only torrent indexer is missing its site definition, debrid still lacks a usable torrent source.
    const withDebrid = indexersHealth([healthyNzb, missingSite], { enabled: true }, now);
    expect(withDebrid.level).toBe("warn");
    expect(withDebrid.details.some((d) => d.includes("Stremio addon or Torznab"))).toBe(true);

    // When all enabled indexers are missing definitions or paused, level escalates to error.
    const allMissing = indexersHealth(
      [missingSite, { ...missingSite, indexer: "1337x" }],
      { enabled: false },
      now,
    );
    expect(allMissing.level).toBe("error");
    expect(allMissing.summary).toBe("2 of 2 indexers missing their definition");

    const singleMissing = indexersHealth([missingSite], { enabled: false }, now);
    expect([singleMissing.level, singleMissing.summary]).toEqual(["error", "Definition missing"]);

    const pausedUntil = Math.floor(now.getTime() / 1000) + 600;
    const paused = indexersHealth(
      [
        healthyNzb,
        { indexer: "DOGnzb", hitsLimit: 100, hitsRemaining: 80, enabled: true, pausedUntil },
      ],
      { enabled: false },
      now,
    );
    expect(paused.level).toBe("warn");
    expect(paused.summary).toBe("1 of 2 indexers paused after failures");
  });
  it("flags real problems", () => {
    expect(usenetHealth([]).setupGap).toBe(true);
    expect(usenetHealth({ error: "boom" }).summary).toBe("boom");
    const dnsFail = usenetHealth([
      {
        id: "p1",
        name: "Primary",
        backfill: false,
        state: "backoff",
        lastError: "dial tcp: lookup news.placeholder.invalid: no such host",
        configuredConnections: 20,
        activeConnections: 0,
      },
    ]);
    expect(dnsFail.level).toBe("error");
    expect(dnsFail.statusLabel).toBe("Needs attention");
    expect(dnsFail.summary).toBe("Host not found (DNS lookup failed). Check the hostname.");
    const connFail = usenetHealth([
      {
        id: "p1",
        name: "Primary",
        backfill: false,
        state: "backoff",
        lastError: "dial tcp 127.0.0.1:563: connect: connection refused",
        configuredConnections: 20,
        activeConnections: 0,
      },
    ]);
    expect(connFail.statusLabel).toBe("Needs attention");
    expect(connFail.summary).toBe("Connection refused. Check the host and port.");
    const authFail = usenetHealth([
      {
        id: "p1",
        name: "Primary",
        backfill: false,
        state: "auth_failed",
        lastError: "nntp: authentication failed: 481 invalid login",
        configuredConnections: 20,
        activeConnections: 0,
      },
    ]);
    expect(authFail.statusLabel).toBe("Needs attention");
    expect(authFail.summary).toBe("Login rejected. Check the username and password.");
    expect(catalogHealth({ lastSync: "2026-10-01T00:00:00Z" }, now).level).toBe("warn");
    expect(
      catalogHealth(
        {
          browsableMovies: 38438,
          browsableShows: 10000,
          lastSync: now.toISOString(),
          sync: { running: true, titlesSeen: 113420 },
        },
        now,
      ).summary,
    ).toBe("48,438 titles · syncing (113,420 checked)");
    expect(
      catalogHealth({ movies: 0, shows: 0, sync: { running: true, titlesSeen: 2500 } }, now)
        .summary,
    ).toBe("Syncing… 2,500 checked");
    // A finished attempt's error warns; while its retry is running the
    // card shows the sync in progress instead of asking for a fix, with
    // the error kept in the details (#407).
    const failed = catalogHealth(
      { lastSync: now.toISOString(), sync: { running: false, lastError: "fixture: page failed" } },
      now,
    );
    expect(failed.level).toBe("warn");
    expect(failed.details.some((line) => line.includes("fixture: page failed"))).toBe(true);
    const retrying = catalogHealth(
      {
        browsableMovies: 10,
        browsableShows: 5,
        lastSync: now.toISOString(),
        sync: { running: true, titlesSeen: 40, lastError: "fixture: page failed" },
      },
      now,
    );
    expect(retrying.level).toBe("ok");
    expect(retrying.summary).toBe("15 titles · syncing (40 checked)");
    expect(retrying.details.some((line) => line.includes("fixture: page failed"))).toBe(true);
    // And once that retry succeeds the error is gone entirely.
    const recovered = catalogHealth(
      { browsableMovies: 10, browsableShows: 5, lastSync: now.toISOString(), sync: {} },
      now,
    );
    expect(recovered.level).toBe("ok");
    expect(recovered.details.some((line) => line.includes("fixture: page failed"))).toBe(false);
    const correcting = catalogHealth(
      {
        lastSync: "2026-10-30T00:00:00Z",
        yearCorrection: { running: true, pending: 12, stored: 3, refreshed: 1 },
      },
      now,
    );
    expect(correcting.level).toBe("ok");
    expect(correcting.details.some((line) => line.includes("12") && line.includes("4"))).toBe(true);
    const finished = catalogHealth(
      {
        lastSync: "2026-10-30T00:00:00Z",
        yearCorrection: { finished: true, pending: 0, stored: 30, refreshed: 4 },
      },
      now,
    );
    expect(finished.details.some((line) => line.includes("movie years"))).toBe(false);
    expect(remoteHealth({ claimed: false }).level).toBe("error");
    const unclaimed = computeHealth({ remote: { claimed: false } }, now);
    expect(unclaimed.filter((item) => item.id === "remote" || item.id === "plexdirect")).toEqual([
      expect.objectContaining({
        id: "remote",
        summary: "Sign in to finish connecting",
        href: "/apps/plex/account",
        setupGap: true,
        level: "unknown",
      }),
    ]);
    const fresh = computeHealth(
      {
        providers: [],
        debrid: { enabled: false },
        indexers: [{ indexer: "Torrentio", enabled: true, hitsLimit: 0 }],
        cache: { used: 0, budget: 32 * 1024 ** 3 },
        catalog: { movies: 0, shows: 0 },
        database: { integrity: { ok: true, checked: now.toISOString() } },
      },
      now,
      { plexEnabled: false },
    );
    expect(fresh.filter((item) => item.setupGap).map((item) => item.id)).toEqual([
      "usenet",
      "catalog",
    ]);
    expect(fresh.find((item) => item.id === "usenet")?.summary).toBe(
      "Torrentio finds releases; add a debrid or Usenet account to play.",
    );
    expect(overallLevel(fresh)).toBe("ok");
    expect(
      remoteHealth({ claimed: true, remoteAccess: true, presence: { connected: false } }).level,
    ).toBe("warn");
    expect(
      plexDirectHealth({ cert: { present: true, notAfter: "2026-10-25T00:00:00Z" } }, now).level,
    ).toBe("warn");
    expect(plexDirectHealth({ cert: { present: false } }, now).level).toBe("warn");
    const fallback = plexDirectHealth(
      { cert: { present: true, selfSigned: true, notAfter: "2028-01-01T00:00:00Z" } },
      now,
    );
    expect([fallback.level, fallback.summary]).toEqual(["warn", "Self-signed certificate"]);
    expect(plexDirectHealth({ cert: { present: true, reissueRequired: true } }, now).summary).toBe(
      "Certificate reissue pending",
    );
    expect(
      plexDirectHealth({ cert: { present: true }, certError: "check failed" }, now).level,
    ).toBe("warn");
    const expired = plexDirectHealth(
      { cert: { present: true, notAfter: "2020-01-01T00:00:00Z" } },
      now,
    );
    expect([expired.level, expired.summary]).toEqual(["error", "Certificate expired"]);
    const actionable = [
      plexDirectHealth({ cert: { present: false } }, now),
      plexDirectHealth({ cert: { present: true, notAfter: "2026-10-25T00:00:00Z" } }, now),
      expired,
      fallback,
      plexDirectHealth({ cert: { present: true, reissueRequired: true } }, now),
      plexDirectHealth({ cert: { present: true }, certError: "check failed" }, now),
    ];
    expect(actionable.every(countsOverall)).toBe(true);
    const installed = plexDirectHealth(
      {
        cert: {
          present: true,
          verified: true,
          notBefore: "2026-01-01T00:00:00Z",
          notAfter: "2028-01-01T00:00:00Z",
        },
      },
      now,
    );
    expect(installed.summary).toMatch(/^Expires /);
    expect(installed.level).toBe("ok");
    expect(installed.statusLabel).toBe("Valid");
    expect(installed.note).toBe("Trusted by Plex apps");
    expect(installed.details).toContain("Trusted by Plex apps");
    expect(countsOverall(installed)).toBe(true);
    const untrusted = plexDirectHealth(
      {
        cert: {
          present: true,
          verified: false,
          verifyReason: "chain",
          notBefore: "2026-01-01T00:00:00Z",
          notAfter: "2028-01-01T00:00:00Z",
        },
      },
      now,
    );
    expect(untrusted.summary).toBe("Chain not trusted");
    expect(untrusted.level).toBe("warn");
    expect(untrusted.note).toMatch(/plex.direct certificate/);
    const mismatch = plexDirectHealth(
      {
        cert: {
          present: true,
          verified: false,
          verifyReason: "identity",
          notBefore: "2026-01-01T00:00:00Z",
          notAfter: "2028-01-01T00:00:00Z",
        },
      },
      now,
    );
    expect(mismatch.summary).toBe("Doesn't match this server");
    const pending = plexDirectHealth(
      {
        cert: {
          present: true,
          notBefore: "2026-01-01T00:00:00Z",
          notAfter: "2028-01-01T00:00:00Z",
        },
      },
      now,
    );
    expect(pending.statusLabel).toBe("Checking certificate");
    expect(pending.countsOverall).toBe(false);
    expect(countsOverall(pending)).toBe(false);
    expect(countsOverall({ ...pending, level: "error" })).toBe(true);
    const missingExpiry = plexDirectHealth(
      { cert: { present: true, notBefore: "2026-01-01T00:00:00Z" } },
      now,
    );
    expect(missingExpiry.summary).toBe("Certificate dates are incomplete");
    expect(missingExpiry.level).toBe("unknown");
    expect(countsOverall(missingExpiry)).toBe(true);
    expect(overallLevel([missingExpiry])).toBe("unknown");
    const missingStart = plexDirectHealth(
      { cert: { present: true, notAfter: "2028-01-01T00:00:00Z" } },
      now,
    );
    expect(missingStart.summary).toBe("Certificate dates are incomplete");
    expect(countsOverall(missingStart)).toBe(true);
    const future = plexDirectHealth(
      {
        cert: {
          present: true,
          notBefore: "2027-01-01T00:00:00Z",
          notAfter: "2028-01-01T00:00:00Z",
        },
      },
      now,
    );
    expect([future.level, future.summary]).toEqual(["warn", "Certificate is not valid yet"]);
    expect(countsOverall(future)).toBe(true);
    const reported = computeHealth(undefined, now);
    expect(reported.every((x) => x.level === "unknown" || x.level === "warn")).toBe(true);
    expect(
      reported
        .filter((x) => x.id === "remote" || x.id === "plexdirect")
        .every((x) => x.level === "unknown" && countsOverall(x)),
    ).toBe(true);
    expect(overallLevel(reported)).toBe("unknown");
    const proxy = computeHealth(
      {
        providers: [{ name: "News", state: "ok", configuredConnections: 1, activeConnections: 0 }],
        indexers: [{ indexer: "NZB", hitsLimit: 0, enabled: true }],
        cache: { mode: "off" },
        catalog: { movies: 1, shows: 1, lastSync: "2026-10-19T12:00:00Z" },
        remote: {
          claimed: true,
          remoteAccess: false,
          presence: { connected: true },
          publishedUris: ["https://plex.example.com:443"],
          connections: [{ uri: "https://plex.example.com:443", custom: true, protocol: "https" }],
          cert: {
            present: true,
            selfSigned: false,
            custom: true,
            notBefore: "2026-01-01T00:00:00Z",
            notAfter: "2028-01-01T00:00:00Z",
          },
        },
      },
      now,
    );
    expect(proxy.map((x) => [x.id, x.level])).toEqual([
      ["usenet", "ok"],
      ["indexers", "ok"],
      ["cache", "off"],
      ["catalog", "ok"],
      ["remote", "off"],
      ["plexdirect", "unknown"],
    ]);
    expect(proxy.find((x) => x.id === "remote")?.summary).toBe("Custom connection published");
    expect(overallLevel(proxy)).toBe("ok");
  });
  it("uses the reported playhead, including zero, independently of transferred bytes", () => {
    const base = { BytesServed: 33, Info: { size: 100, duration: 60e9 } };
    const sessions = normalizeSessions([
      base,
      { ...base, viewOffset: 55000 },
      { ...base, viewOffset: 10000 },
      { ...base, viewOffset: 0 },
      { ...base, viewOffset: -1000 },
      { ...base, viewOffset: 90000 },
      { ...base, viewOffset: null },
      { ...base, viewOffset: NaN },
    ]);
    expect(sessions.map((s) => s.progress)).toEqual([null, 55 / 60, 1 / 6, 0, 0, 1, null, null]);
  });
  it("normalizes now-playing sessions from Go field names", () => {
    const [s] = normalizeSessions([
      {
        ID: "s1",
        Key: 123,
        TitleName: "Dune (2021)",
        Username: "Sam",
        Client: { product: "Plex Web", deviceName: "Chrome", remote: true },
        Decision: "transcode",
        BytesServed: 50,
        Info: { size: 100, duration: 7.2e12 },
        viewOffset: 3.6e6,
        Resolution: { release: { name: "Dune.2021.1080p" } },
      },
    ]);
    expect(s).toMatchObject({
      id: "s1",
      key: "123",
      title: "Dune (2021)",
      user: "Sam",
      device: "Chrome",
      decision: "transcode",
      remote: true,
      size: 100,
      release: "Dune.2021.1080p",
    });
    expect(s!.progress).toBeCloseTo(0.5);
    const [byBytes] = normalizeSessions([
      { Info: { size: 100, duration: 7200e9 }, viewOffset: 3.6e6, BufferedBytes: 25 },
    ]);
    expect(byBytes!.buffered).toBeCloseTo(0.75);
    const [byUntil] = normalizeSessions([
      { Info: { size: 100, duration: 7200e9 }, BufferedUntil: 5.4e12 },
    ]);
    expect(byUntil!.buffered).toBeCloseTo(0.75);
    expect(normalizeSessions(null)).toEqual([]);
  });
  it("labels every now-playing decision kind", () => {
    const label = (decision: string, videoCopy = false, audioCopy = false) =>
      decisionLabel({ decision, videoCopy, audioCopy });
    expect(label("directplay", true, true)).toBe("Direct play");
    expect(label("copy", true, true)).toBe("Direct stream");
    // An HLS remux copying the video but re-encoding the audio.
    expect(label("copy", true, false)).toBe("Transcoding (audio)");
    expect(label("transcode", true, false)).toBe("Transcoding (audio)");
    expect(label("transcode", false, false)).toBe("Transcoding");
    expect(label("transcode", false, true)).toBe("Transcoding");
    expect(label("")).toBe("Starting");
    expect(label("something-new")).toBe("Starting");
    // The copy flags survive normalization from the Go snapshot fields.
    const [s] = normalizeSessions([
      { ID: "s2", Decision: "copy", decisionVideoCopy: true, decisionAudioCopy: false },
    ]);
    expect(s).toMatchObject({ decision: "copy", videoCopy: true, audioCopy: false });
    expect(decisionLabel(s!)).toBe("Transcoding (audio)");
  });
});

describe("format", () => {
  it("formats bytes, durations, compact axis ticks and relative times", () => {
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(-1)).toBe("—");
    expect(formatCompactNumber(0)).toBe("0");
    expect(formatCompactNumber(950)).toBe("950");
    expect(formatCompactNumber(1500)).toBe("1.5K");
    expect(formatCompactNumber(500_000)).toBe("500K");
    expect(formatCompactNumber(1_000_000)).toBe("1M");
    expect(formatNanos(1.8e9)).toBe("1.8 s");
    expect(formatNanos(3.1e8)).toBe("310 ms");
    expect(relativeTime("0001-01-01T00:00:00Z")).toBe("never");
    expect(relativeTime(new Date(Date.now() - 3 * 3600_000))).toMatch(/3 hours ago/);
  });
});
