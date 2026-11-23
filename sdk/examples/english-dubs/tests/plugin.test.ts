import { test } from "node:test";
import { strict as assert } from "node:assert";
import {
  CACHE_TTL_MS,
  FRIBB_MAP_URL,
  MAL_DUBS_URL,
  PAGE_SIZE,
  handle,
  mergeCatalogLists,
  parseCatalogRoute,
  parseDubInfoJson,
  parseFribbMiniJson,
  type HostedEtherGlobal,
} from "../src/plugin.ts";

interface MockRequest {
  url: string;
  headers?: Record<string, string>;
}

function createMockEther(options: {
  settings?: Record<string, unknown>;
  fetchImpl?: (req: MockRequest) => {
    status: number;
    headers?: Record<string, string>;
    body?: string;
  };
}) {
  const kv = new Map<string, string>();
  const requests: MockRequest[] = [];
  let currentSettings = options.settings ?? {};

  const mock: HostedEtherGlobal = {
    kv: {
      get: (k) => kv.get(k) ?? null,
      put: (k, v) => {
        kv.set(k, String(v));
      },
      delete: (k) => {
        kv.delete(k);
      },
      list: (prefix = "") => [...kv.keys()].filter((k) => k.startsWith(prefix)),
    },
    settings: {
      get: () => currentSettings,
    },
    http: {
      fetch: (req) => {
        requests.push({ url: req.url, headers: req.headers });
        if (!options.fetchImpl) {
          throw new Error(`Unexpected fetch to ${req.url}`);
        }
        return options.fetchImpl({ url: req.url, headers: req.headers });
      },
    },
    log: () => {},
  };

  (globalThis as unknown as { ether: HostedEtherGlobal }).ether = mock;
  return {
    kv,
    requests,
    setSettings(next: Record<string, unknown>) {
      currentSettings = next;
    },
  };
}

const SAMPLE_DUB_INFO = JSON.stringify({
  _license: "AGPL-3.0",
  _origin: "https://github.com/mal-dubs/mal-dubs",
  dubbed: [1, 5, 20, 21, 999999, 300, 301, 400],
  incomplete: [8, 21, 500],
});

const SAMPLE_FRIBB = JSON.stringify([
  {
    type: "TV",
    mal_id: 1,
    themoviedb_id: { tv: 30991 },
    imdb_id: ["tt0213338"],
    tvdb_id: 76885,
  },
  {
    type: "MOVIE",
    mal_id: 5,
    themoviedb_id: { movie: [11299] },
    imdb_id: ["tt0275277"],
  },
  {
    type: "TV",
    mal_id: 20,
    themoviedb_id: { tv: 46260 },
    imdb_id: ["tt0409591"],
    tvdb_id: 78857,
  },
  {
    type: "TV",
    mal_id: 21,
    themoviedb_id: { tv: 37854 },
    imdb_id: ["tt0388629"],
    tvdb_id: 81797,
  },
  // Second MAL entry mapping to the same TMDB show as MAL 20 (season 2 / OVA)
  {
    type: "OVA",
    mal_id: 300,
    themoviedb_id: { tv: 46260 },
    imdb_id: ["tt0409591"],
    tvdb_id: 78857,
  },
  // No TMDB ID, fallback to TVDB
  {
    type: "TV",
    mal_id: 301,
    tvdb_id: 123456,
    imdb_id: ["tt1111111"],
  },
  // No TMDB ID, movie fallback to IMDb
  {
    type: "MOVIE",
    mal_id: 400,
    imdb_id: ["tt2222222"],
  },
  // Incomplete dub entries
  {
    type: "TV",
    mal_id: 8,
    themoviedb_id: { tv: 99001 },
    imdb_id: ["tt0489000"],
    tvdb_id: 99001,
  },
  {
    type: "MOVIE",
    mal_id: 500,
    themoviedb_id: { movie: [88001] },
  },
  // Irrelevant entry with string braces to test JSON scanner resilience
  {
    type: "TV",
    anidb_id: 777,
    "anime-planet_id": 'show-{with}-"braces"-and-\\backslash',
  },
]);

test("parseFribbMiniJson handles strings with braces, filters unmapped entries, and keeps TMDB/IMDb/TVDB", () => {
  const map = parseFribbMiniJson(SAMPLE_FRIBB);
  assert.ok(map["1"]);
  assert.equal(map["1"][1], 30991);
  assert.equal(map["1"][3], "tt0213338");
  assert.equal(map["1"][4], 76885);
  assert.equal(map["5"][2], 11299);
  assert.equal(map["301"][4], 123456);
  assert.equal(map["400"][3], "tt2222222");
  assert.equal(map["777"], undefined);
});

test("parseDubInfoJson validates shape and deduplicates IDs", () => {
  const parsed = parseDubInfoJson('{"dubbed":[1,5,1,"6",-2,0],"incomplete":[8,8]}');
  assert.deepEqual(parsed.dubbed, [1, 5, 6]);
  assert.deepEqual(parsed.incomplete, [8]);
  assert.throws(() => parseDubInfoJson("{}"), /missing the dubbed array/);
});

test("parseCatalogRoute handles extra skip segment and query skip", () => {
  assert.deepEqual(parseCatalogRoute("/catalog/series/dubbed.json"), {
    type: "series",
    id: "dubbed",
    skip: 0,
  });
  assert.deepEqual(parseCatalogRoute("/catalog/movie/dubbed/skip=200.json"), {
    type: "movie",
    id: "dubbed",
    skip: 200,
  });
  assert.deepEqual(
    parseCatalogRoute("/catalog/series/incomplete/foo=1&skip=40.json"),
    {
      type: "series",
      id: "incomplete",
      skip: 40,
    },
  );
  assert.deepEqual(
    parseCatalogRoute("/catalog/movie/incomplete.json", { skip: ["10"] }),
    {
      type: "movie",
      id: "incomplete",
      skip: 10,
    },
  );
  assert.equal(parseCatalogRoute("/catalog/movie/unknown.json"), null);
});

test("fetches at runtime, deduplicates shows/movies, honors settings, and revalidates with ETag and If-Modified-Since", () => {
  let dubEtag = '"dub-v1"';
  let mapEtag = '"map-v1"';
  const dubLM = "Thu, 19 Nov 2026 00:00:00 GMT";
  const mapLM = "Thu, 19 Nov 2026 01:00:00 GMT";

  const env = createMockEther({
    settings: { includeIncomplete: false, typeFilter: "both" },
    fetchImpl: (req) => {
      if (req.url === MAL_DUBS_URL) {
        if (req.headers?.["If-None-Match"] === dubEtag) {
          return { status: 304, headers: { ETag: dubEtag, "Last-Modified": dubLM } };
        }
        return {
          status: 200,
          headers: { ETag: dubEtag, "Last-Modified": dubLM },
          body: SAMPLE_DUB_INFO,
        };
      }
      if (req.url === FRIBB_MAP_URL) {
        if (req.headers?.["If-None-Match"] === mapEtag) {
          return { status: 304, headers: { ETag: mapEtag, "Last-Modified": mapLM } };
        }
        return {
          status: 200,
          headers: { ETag: mapEtag, "Last-Modified": mapLM },
          body: SAMPLE_FRIBB,
        };
      }
      return { status: 404, body: "not found" };
    },
  });

  // Cold fetch populates KV.
  const seriesRes = handle({ method: "GET", path: "/catalog/series/dubbed.json" });
  assert.equal(seriesRes.status, 200);
  const seriesBody = seriesRes.body as { metas: any[]; hasMore: boolean };
  assert.equal(seriesBody.hasMore, false);
  // MAL 1 (tmdb:30991), MAL 20 (tmdb:46260), MAL 21 (tmdb:37854), MAL 301 (tvdb:123456)
  // MAL 300 collapsed into tmdb:46260; MAL 999999 unmapped.
  assert.deepEqual(
    seriesBody.metas.map((m) => m.id),
    ["tmdb:30991", "tmdb:46260", "tmdb:37854", "tvdb:123456"],
  );
  assert.equal(env.requests.length, 2);

  // Subsequent catalog reads use warm KV cache with zero network requests.
  const movieRes = handle({ method: "GET", path: "/catalog/movie/dubbed.json" });
  const movieBody = movieRes.body as { metas: any[]; hasMore: boolean };
  assert.deepEqual(
    movieBody.metas.map((m) => m.id),
    ["tmdb:11299", "tt2222222"],
  );
  assert.equal(env.requests.length, 2);

  // Incomplete list source returns incomplete entries separately.
  const incSeries = handle({
    method: "GET",
    path: "/catalog/series/incomplete.json",
  }).body as { metas: any[] };
  assert.deepEqual(
    incSeries.metas.map((m) => m.id),
    ["tmdb:99001", "tmdb:37854"],
  );

  // Setting includeIncomplete=true merges incomplete entries into dubbed without duplicating tmdb:37854.
  env.setSettings({ includeIncomplete: true, typeFilter: "both" });
  const mergedSeries = handle({
    method: "GET",
    path: "/catalog/series/dubbed.json",
  }).body as { metas: any[] };
  assert.deepEqual(
    mergedSeries.metas.map((m) => m.id),
    ["tmdb:30991", "tmdb:46260", "tmdb:37854", "tvdb:123456", "tmdb:99001"],
  );
  const mergedMovies = handle({
    method: "GET",
    path: "/catalog/movie/dubbed.json",
  }).body as { metas: any[] };
  assert.deepEqual(
    mergedMovies.metas.map((m) => m.id),
    ["tmdb:11299", "tt2222222", "tmdb:88001"],
  );

  // Type filter restricts movies or series cleanly.
  env.setSettings({ includeIncomplete: false, typeFilter: "series" });
  const filteredMovies = handle({
    method: "GET",
    path: "/catalog/movie/dubbed.json",
  }).body as { metas: any[]; hasMore: boolean };
  assert.deepEqual(filteredMovies, { metas: [], hasMore: false });

  env.setSettings({ includeIncomplete: false, typeFilter: "movies" });
  const filteredSeries = handle({
    method: "GET",
    path: "/catalog/series/dubbed.json",
  }).body as { metas: any[]; hasMore: boolean };
  assert.deepEqual(filteredSeries, { metas: [], hasMore: false });

  // Scheduled refresh sends both If-None-Match and If-Modified-Since and handles 304.
  env.setSettings({ includeIncomplete: false, typeFilter: "both" });
  const jobRes = handle({ method: "POST", path: "/ether/job/refresh", body: {} });
  assert.equal(jobRes.status, 200);
  assert.equal((jobRes.body as any).changed, false);
  assert.equal(env.requests.length, 4);
  assert.equal(env.requests[2].headers?.["If-None-Match"], dubEtag);
  assert.equal(env.requests[2].headers?.["If-Modified-Since"], dubLM);
  assert.equal(env.requests[3].headers?.["If-None-Match"], mapEtag);
  assert.equal(env.requests[3].headers?.["If-Modified-Since"], mapLM);
});

test("falls back to stale cache when upstream fails transiently after TTL expiry", () => {
  let failUpstream = false;
  const env = createMockEther({
    fetchImpl: (req) => {
      if (failUpstream) return { status: 503, body: "upstream down" };
      if (req.url === MAL_DUBS_URL) {
        return { status: 200, headers: { ETag: '"d1"' }, body: SAMPLE_DUB_INFO };
      }
      return { status: 200, headers: { ETag: '"m1"' }, body: SAMPLE_FRIBB };
    },
  });

  // Populate cache.
  handle({ method: "POST", path: "/ether/job/refresh", body: {} });

  // Expire cached timestamps past 24h and simulate upstream 503.
  const expired = Date.now() - CACHE_TTL_MS - 1000;
  env.kv.set(
    "dubs:meta",
    JSON.stringify({ etag: '"d1"', lastModified: "", fetchedAt: expired }),
  );
  env.kv.set(
    "map:meta",
    JSON.stringify({ etag: '"m1"', lastModified: "", fetchedAt: expired }),
  );
  failUpstream = true;

  const res = handle({ method: "GET", path: "/catalog/series/dubbed.json" });
  assert.equal(res.status, 200);
  assert.equal((res.body as any).metas.length, 4);
});

test("paginates cleanly across PAGE_SIZE boundaries with skip and hasMore", () => {
  const total = PAGE_SIZE + 5;
  const dubbed: number[] = [];
  const fribb: any[] = [];
  for (let i = 1; i <= total; i++) {
    dubbed.push(i);
    fribb.push({ type: "TV", mal_id: i, themoviedb_id: { tv: 10000 + i } });
  }
  createMockEther({
    fetchImpl: (req) => ({
      status: 200,
      headers: {},
      body:
        req.url === MAL_DUBS_URL
          ? JSON.stringify({ dubbed, incomplete: [] })
          : JSON.stringify(fribb),
    }),
  });

  const p1 = handle({ method: "GET", path: "/catalog/series/dubbed.json" })
    .body as { metas: any[]; hasMore: boolean };
  assert.equal(p1.metas.length, PAGE_SIZE);
  assert.equal(p1.hasMore, true);

  const p2 = handle({
    method: "GET",
    path: `/catalog/series/dubbed/skip=${PAGE_SIZE}.json`,
  }).body as { metas: any[]; hasMore: boolean };
  assert.equal(p2.metas.length, 5);
  assert.equal(p2.hasMore, false);

  const p3 = handle({
    method: "GET",
    path: `/catalog/series/dubbed/skip=${total + 10}.json`,
  }).body as { metas: any[]; hasMore: boolean };
  assert.deepEqual(p3, { metas: [], hasMore: false });
});

test("mergeCatalogLists avoids duplicating secondary fallback entries already covered by TMDB", () => {
  const merged = mergeCatalogLists(
    [["tmdb:37854", 21, 37854, "tt0388629", 81797]],
    [
      ["tvdb:81797", 999, 0, "tt0388629", 81797],
      ["tmdb:99001", 8, 99001, "tt0489000", 99001],
    ],
  );
  assert.deepEqual(
    merged.map((x) => x[0]),
    ["tmdb:37854", "tmdb:99001"],
  );
});
