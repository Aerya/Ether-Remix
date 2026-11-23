// English dubbed anime list source plugin for Ether.
//
// Fetches the community MAL-Dubs index (AGPL-3.0) and Fribb/anime-lists
// MyAnimeList-to-TMDB mapping at runtime, caches compacted data in KV with
// ETag / If-Modified-Since revalidation, and exposes Ether list sources for
// confirmed English dubs and in-progress dubs.

export const MAL_DUBS_URL =
  "https://raw.githubusercontent.com/MAL-Dubs/MAL-Dubs/main/data/dubInfo.json";
export const FRIBB_MAP_URL =
  "https://raw.githubusercontent.com/Fribb/anime-lists/master/anime-list-mini.json";

export const CACHE_TTL_MS = 24 * 60 * 60 * 1000;
export const PAGE_SIZE = 200;

const KV_DUBS_META = "dubs:meta";
const KV_DUBS_DATA = "dubs:data";
const KV_MAP_META = "map:meta";
const KV_MAP_DATA = "map:data";
const KV_INDEX_STATS = "index:stats";
const KV_INDEX_DUBBED_SERIES = "index:dubbed:series";
const KV_INDEX_DUBBED_MOVIE = "index:dubbed:movie";
const KV_INDEX_INCOMPLETE_SERIES = "index:incomplete:series";
const KV_INDEX_INCOMPLETE_MOVIE = "index:incomplete:movie";

const KIND_UNKNOWN = 0;
const KIND_TV = 1;
const KIND_MOVIE = 2;
const KIND_OVA = 3;
const KIND_ONA = 4;
const KIND_SPECIAL = 5;

export type TypeFilter = "both" | "series" | "movies";
export type CatalogMediaType = "series" | "movie";
export type CatalogId = "dubbed" | "incomplete";

export interface PluginSettings {
  includeIncomplete: boolean;
  typeFilter: TypeFilter;
}

export interface PluginRequest {
  method?: string;
  path: string;
  query?: Record<string, string | string[]>;
  headers?: Record<string, string>;
  body?: unknown;
}

export interface PluginResponse {
  status: number;
  headers?: Record<string, string>;
  body: unknown;
}

interface HttpFetchRequest {
  url: string;
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  timeoutMs?: number;
}

interface HttpFetchResponse {
  status: number;
  headers?: Record<string, string>;
  body?: string;
}

export interface HostedEtherGlobal {
  kv: {
    get(key: string): string | null;
    put(key: string, value: string): void;
    delete(key: string): void;
    list(prefix?: string): string[];
  };
  settings: {
    get(): Record<string, unknown> | null | undefined;
  };
  http: {
    fetch(req: HttpFetchRequest): HttpFetchResponse;
  };
  log?(...args: unknown[]): void;
}

interface CacheMeta {
  etag: string;
  lastModified: string;
  fetchedAt: number;
}

export interface DubInfo {
  dubbed: number[];
  incomplete: number[];
}

// Compact tuple persisted in KV: [kindCode, tmdbTv, tmdbMovie, imdb, tvdb]
export type CompactFribbEntry = [
  number,
  number | number[],
  number | number[],
  string,
  number,
];

export type CompactFribbMap = Record<string, CompactFribbEntry>;

// Compact catalog tuple persisted in KV: [id, malId, tmdbId, imdbId, tvdbId]
export type CompactCatalogItem = [string, number, number, string, number];

export interface IndexStats {
  builtAt: number;
  checkedAt: number;
  dubbedRawCount: number;
  incompleteRawCount: number;
  dubbedSeriesCount: number;
  dubbedMovieCount: number;
  incompleteSeriesCount: number;
  incompleteMovieCount: number;
  dubbedUnmapped: number;
  incompleteUnmapped: number;
  lastError?: string;
}

export interface MetaPreview {
  id: string;
  type: CatalogMediaType;
  name: string;
  tmdb_id?: number;
  imdb_id?: string;
  tvdb_id?: number;
}

function getEther(): HostedEtherGlobal {
  const g = (globalThis as unknown as { ether?: HostedEtherGlobal }).ether;
  if (!g) {
    throw new Error("Ether host global is unavailable");
  }
  return g;
}

function ok(body: unknown): PluginResponse {
  return { status: 200, body };
}

function errorResponse(status: number, code: string, message: string): PluginResponse {
  return { status, body: { error: { code, message } } };
}

function logWarn(msg: string): void {
  try {
    getEther().log?.("warn", msg);
  } catch {
    // Logging must never break request handling.
  }
}

function parseBody(raw: unknown): Record<string, unknown> {
  if (raw == null) return {};
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (!trimmed) return {};
    const parsed = JSON.parse(trimmed);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : {};
  }
  if (typeof raw === "object" && !Array.isArray(raw)) {
    return raw as Record<string, unknown>;
  }
  return {};
}

function getHeader(headers: Record<string, string> | undefined, name: string): string {
  if (!headers) return "";
  const want = name.toLowerCase();
  for (const key of Object.keys(headers)) {
    if (key.toLowerCase() === want) {
      return String(headers[key] ?? "").trim();
    }
  }
  return "";
}

function getQueryValue(
  query: Record<string, string | string[]> | undefined,
  key: string,
): string {
  if (!query) return "";
  const val = query[key];
  if (Array.isArray(val)) return String(val[0] ?? "");
  if (typeof val === "string") return val;
  return "";
}

function isFatalHostError(err: unknown): boolean {
  const code = (err as { code?: string } | null)?.code;
  if (
    code === "check_dependency" ||
    code === "forbidden" ||
    code === "governance" ||
    code === "aborted"
  ) {
    return true;
  }
  const msg = err instanceof Error ? err.message : String(err);
  return (
    msg.includes("check_dependency") ||
    msg.includes("forbidden") ||
    msg.includes("governance")
  );
}

export function normalizeSettings(raw: Record<string, unknown> | null | undefined): PluginSettings {
  const obj = raw && typeof raw === "object" ? raw : {};
  const includeIncomplete = Boolean(obj.includeIncomplete);
  const tf = typeof obj.typeFilter === "string" ? obj.typeFilter.trim().toLowerCase() : "both";
  const typeFilter: TypeFilter =
    tf === "series" || tf === "movies" || tf === "both" ? tf : "both";
  return { includeIncomplete, typeFilter };
}

export function validateSettings(raw: Record<string, unknown>): Record<string, string> {
  const errors: Record<string, string> = {};
  if (
    raw.includeIncomplete !== undefined &&
    typeof raw.includeIncomplete !== "boolean"
  ) {
    errors.includeIncomplete = "Include incomplete dubs must be true or false.";
  }
  if (raw.typeFilter !== undefined) {
    const tf =
      typeof raw.typeFilter === "string" ? raw.typeFilter.trim().toLowerCase() : "";
    if (tf !== "both" && tf !== "series" && tf !== "movies") {
      errors.typeFilter = "Type filter must be both, series, or movies.";
    }
  }
  return errors;
}

function readKVJson<T>(key: string): T | null {
  const raw = getEther().kv.get(key);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

function writeKVJson(key: string, value: unknown): void {
  getEther().kv.put(key, JSON.stringify(value));
}

function toPositiveInt(v: unknown): number {
  if (typeof v === "number") {
    return Number.isFinite(v) && v > 0 && Math.floor(v) === v ? v : 0;
  }
  if (typeof v === "string") {
    const s = v.trim();
    if (!/^[0-9]+$/.test(s)) return 0;
    const n = Number(s);
    return Number.isSafeInteger(n) && n > 0 ? n : 0;
  }
  return 0;
}

function toPositiveIntList(v: unknown): number[] {
  if (Array.isArray(v)) {
    const out: number[] = [];
    for (const item of v) {
      const n = toPositiveInt(item);
      if (n > 0 && !out.includes(n)) out.push(n);
    }
    return out;
  }
  const single = toPositiveInt(v);
  return single > 0 ? [single] : [];
}

function compactIntOrList(nums: number[]): number | number[] {
  if (nums.length === 0) return 0;
  if (nums.length === 1) return nums[0];
  return nums;
}

function expandIntOrList(v: number | number[]): number[] {
  if (Array.isArray(v)) return v;
  return v > 0 ? [v] : [];
}

function normalizeImdb(v: unknown): string {
  const check = (s: unknown): string => {
    if (typeof s !== "string") return "";
    const norm = s.trim().toLowerCase();
    return /^tt[0-9]+$/.test(norm) ? norm : "";
  };
  if (Array.isArray(v)) {
    for (const item of v) {
      const ok = check(item);
      if (ok) return ok;
    }
    return "";
  }
  return check(v);
}

function encodeKind(raw: unknown): number {
  if (typeof raw !== "string") return KIND_UNKNOWN;
  switch (raw.trim().toUpperCase()) {
    case "TV":
      return KIND_TV;
    case "MOVIE":
      return KIND_MOVIE;
    case "OVA":
      return KIND_OVA;
    case "ONA":
      return KIND_ONA;
    case "SPECIAL":
      return KIND_SPECIAL;
    default:
      return KIND_UNKNOWN;
  }
}

export function parseDubInfoJson(raw: string): DubInfo {
  let doc: unknown;
  try {
    doc = JSON.parse(raw);
  } catch (err) {
    throw new Error(
      `Invalid MAL-Dubs JSON: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  if (!doc || typeof doc !== "object" || Array.isArray(doc)) {
    throw new Error("MAL-Dubs payload must be a JSON object");
  }
  const rec = doc as Record<string, unknown>;
  if (!Array.isArray(rec.dubbed)) {
    throw new Error("MAL-Dubs payload is missing the dubbed array");
  }
  const collect = (arr: unknown): number[] => {
    if (!Array.isArray(arr)) return [];
    const seen = new Set<number>();
    const out: number[] = [];
    for (const item of arr) {
      const n = toPositiveInt(item);
      if (n > 0 && !seen.has(n)) {
        seen.add(n);
        out.push(n);
      }
    }
    return out;
  };
  return {
    dubbed: collect(rec.dubbed),
    incomplete: collect(rec.incomplete),
  };
}

function mergeCompactEntry(
  existing: CompactFribbEntry | undefined,
  incoming: CompactFribbEntry,
): CompactFribbEntry {
  if (!existing) return incoming;
  const kind = existing[0] !== KIND_UNKNOWN ? existing[0] : incoming[0];
  const tv = expandIntOrList(existing[1]);
  for (const id of expandIntOrList(incoming[1])) {
    if (!tv.includes(id)) tv.push(id);
  }
  const mv = expandIntOrList(existing[2]);
  for (const id of expandIntOrList(incoming[2])) {
    if (!mv.includes(id)) mv.push(id);
  }
  const imdb = existing[3] || incoming[3];
  const tvdb = existing[4] || incoming[4];
  return [kind, compactIntOrList(tv), compactIntOrList(mv), imdb, tvdb];
}

// Parse Fribb/anime-lists anime-list-mini.json and compact it to only entries
// with a MyAnimeList ID and at least one TMDB, IMDb, or TVDB identifier.
// Uses native JSON.parse (compiled C in QuickJS) rather than a JS character
// scanner so the ~5.9 MB upstream file parses in milliseconds.
export function parseFribbMiniJson(raw: string): CompactFribbMap {
  let arr: unknown;
  try {
    arr = JSON.parse(raw);
  } catch (err) {
    throw new Error(
      `Invalid Fribb anime-lists JSON: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  if (!Array.isArray(arr)) {
    throw new Error("Fribb anime-lists payload must be a JSON array");
  }

  const out: CompactFribbMap = {};
  for (let i = 0; i < arr.length; i++) {
    const item = arr[i];
    if (!item || typeof item !== "object" || Array.isArray(item)) continue;
    const entry = item as Record<string, unknown>;

    const malId = toPositiveInt(entry.mal_id);
    if (malId <= 0) continue;

    let tmdbTv: number[] = [];
    let tmdbMovie: number[] = [];
    const rawTmdb = entry.themoviedb_id;
    if (rawTmdb && typeof rawTmdb === "object" && !Array.isArray(rawTmdb)) {
      const t = rawTmdb as Record<string, unknown>;
      tmdbTv = toPositiveIntList(t.tv);
      tmdbMovie = toPositiveIntList(t.movie);
    }

    const imdb = normalizeImdb(entry.imdb_id);
    const tvdbList = toPositiveIntList(entry.tvdb_id);
    const tvdb = tvdbList.length > 0 ? tvdbList[0] : 0;

    if (tmdbTv.length === 0 && tmdbMovie.length === 0 && !imdb && tvdb === 0) {
      continue;
    }

    const key = String(malId);
    const compact: CompactFribbEntry = [
      encodeKind(entry.type),
      compactIntOrList(tmdbTv),
      compactIntOrList(tmdbMovie),
      imdb,
      tvdb,
    ];
    out[key] = mergeCompactEntry(out[key], compact);
  }

  return out;
}

function buildListForMalIds(
  malIds: number[],
  fribbMap: CompactFribbMap,
): { series: CompactCatalogItem[]; movie: CompactCatalogItem[]; unmapped: number } {
  const series: CompactCatalogItem[] = [];
  const movie: CompactCatalogItem[] = [];
  const seenSeriesId = new Set<string>();
  const seenSeriesImdb = new Set<string>();
  const seenSeriesTvdb = new Set<number>();
  const seenMovieId = new Set<string>();
  const seenMovieImdb = new Set<string>();
  let unmapped = 0;

  const pushSeries = (item: CompactCatalogItem, isFallback: boolean): void => {
    const [id, , , imdb, tvdb] = item;
    if (seenSeriesId.has(id)) return;
    if (isFallback) {
      if (tvdb > 0 && seenSeriesTvdb.has(tvdb)) return;
      if (imdb && seenSeriesImdb.has(imdb)) return;
    }
    seenSeriesId.add(id);
    if (imdb) seenSeriesImdb.add(imdb);
    if (tvdb > 0) seenSeriesTvdb.add(tvdb);
    series.push(item);
  };

  const pushMovie = (item: CompactCatalogItem, isFallback: boolean): void => {
    const [id, , , imdb] = item;
    if (seenMovieId.has(id)) return;
    if (isFallback && imdb && seenMovieImdb.has(imdb)) return;
    seenMovieId.add(id);
    if (imdb) seenMovieImdb.add(imdb);
    movie.push(item);
  };

  for (const malId of malIds) {
    const entry = fribbMap[String(malId)];
    if (!entry) {
      unmapped++;
      continue;
    }
    const [kind, rawTv, rawMovie, imdb, tvdb] = entry;
    const tmdbTv = expandIntOrList(rawTv);
    const tmdbMovie = expandIntOrList(rawMovie);

    let mapped = false;
    if (tmdbTv.length > 0) {
      mapped = true;
      for (const tvId of tmdbTv) {
        pushSeries([`tmdb:${tvId}`, malId, tvId, imdb, tvdb], false);
      }
    }
    if (tmdbMovie.length > 0) {
      mapped = true;
      for (const mvId of tmdbMovie) {
        pushMovie([`tmdb:${mvId}`, malId, mvId, imdb, 0], false);
      }
    }

    if (!mapped) {
      if (kind === KIND_MOVIE) {
        if (imdb) {
          mapped = true;
          pushMovie([imdb, malId, 0, imdb, 0], true);
        }
      } else {
        if (tvdb > 0) {
          mapped = true;
          pushSeries([`tvdb:${tvdb}`, malId, 0, imdb, tvdb], true);
        } else if (imdb) {
          mapped = true;
          pushSeries([imdb, malId, 0, imdb, 0], true);
        }
      }
    }

    if (!mapped) {
      unmapped++;
    }
  }

  return { series, movie, unmapped };
}

export function mergeCatalogLists(
  primary: CompactCatalogItem[],
  secondary: CompactCatalogItem[],
): CompactCatalogItem[] {
  if (secondary.length === 0) return primary;
  const out = primary.slice();
  const seenId = new Set<string>();
  const seenImdb = new Set<string>();
  const seenTvdb = new Set<number>();
  for (const item of primary) {
    seenId.add(item[0]);
    if (item[3]) seenImdb.add(item[3]);
    if (item[4] > 0) seenTvdb.add(item[4]);
  }
  for (const item of secondary) {
    const [id, , tmdb, imdb, tvdb] = item;
    if (seenId.has(id)) continue;
    const isFallback = tmdb === 0;
    if (isFallback) {
      if (tvdb > 0 && seenTvdb.has(tvdb)) continue;
      if (imdb && seenImdb.has(imdb)) continue;
    }
    seenId.add(id);
    if (imdb) seenImdb.add(imdb);
    if (tvdb > 0) seenTvdb.add(tvdb);
    out.push(item);
  }
  return out;
}

interface FetchResourceResult<T> {
  changed: boolean;
  data: T;
  meta: CacheMeta;
  staleError?: string;
}

function fetchCachedResource<T>(
  url: string,
  metaKey: string,
  dataKey: string,
  parse: (body: string) => T,
  force: boolean,
  now: number,
): FetchResourceResult<T> {
  const existingMeta = readKVJson<CacheMeta>(metaKey);
  const existingData = readKVJson<T>(dataKey);
  const hasCache = existingData !== null;

  if (
    !force &&
    hasCache &&
    existingMeta &&
    existingMeta.fetchedAt > 0 &&
    now - existingMeta.fetchedAt < CACHE_TTL_MS
  ) {
    return { changed: false, data: existingData, meta: existingMeta };
  }

  const headers: Record<string, string> = { Accept: "application/json" };
  if (hasCache && existingMeta) {
    if (existingMeta.etag) headers["If-None-Match"] = existingMeta.etag;
    if (existingMeta.lastModified) {
      headers["If-Modified-Since"] = existingMeta.lastModified;
    }
  }

  let resp: HttpFetchResponse;
  try {
    resp = getEther().http.fetch({
      url,
      method: "GET",
      headers,
      timeoutMs: 25000,
    });
  } catch (err) {
    if (isFatalHostError(err)) throw err;
    const msg = err instanceof Error ? err.message : String(err);
    if (hasCache && existingMeta) {
      logWarn(`Using cached data for ${url} after fetch error: ${msg}`);
      return {
        changed: false,
        data: existingData,
        meta: existingMeta,
        staleError: msg,
      };
    }
    throw new Error(`Failed to fetch ${url}: ${msg}`);
  }

  if (resp.status === 304 && hasCache) {
    const nextMeta: CacheMeta = {
      etag: getHeader(resp.headers, "etag") || existingMeta?.etag || "",
      lastModified:
        getHeader(resp.headers, "last-modified") ||
        existingMeta?.lastModified ||
        "",
      fetchedAt: now,
    };
    writeKVJson(metaKey, nextMeta);
    return { changed: false, data: existingData, meta: nextMeta };
  }

  if (resp.status !== 200) {
    const msg = `HTTP ${resp.status} from ${url}`;
    if (hasCache && existingMeta) {
      logWarn(`Using cached data for ${url} after ${msg}`);
      return {
        changed: false,
        data: existingData,
        meta: existingMeta,
        staleError: msg,
      };
    }
    throw new Error(msg);
  }

  const body =
    typeof resp.body === "string"
      ? resp.body
      : resp.body == null
        ? ""
        : JSON.stringify(resp.body);

  let parsed: T;
  try {
    parsed = parse(body);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (hasCache && existingMeta) {
      logWarn(`Using cached data for ${url} after parse error: ${msg}`);
      return {
        changed: false,
        data: existingData,
        meta: existingMeta,
        staleError: msg,
      };
    }
    throw err;
  }

  const nextMeta: CacheMeta = {
    etag: getHeader(resp.headers, "etag"),
    lastModified: getHeader(resp.headers, "last-modified"),
    fetchedAt: now,
  };
  writeKVJson(dataKey, parsed);
  writeKVJson(metaKey, nextMeta);
  return { changed: true, data: parsed, meta: nextMeta };
}

export function refreshCache(force: boolean): {
  changed: boolean;
  stats: IndexStats;
} {
  const now = Date.now();
  const dubsRes = fetchCachedResource<DubInfo>(
    MAL_DUBS_URL,
    KV_DUBS_META,
    KV_DUBS_DATA,
    parseDubInfoJson,
    force,
    now,
  );
  const mapRes = fetchCachedResource<CompactFribbMap>(
    FRIBB_MAP_URL,
    KV_MAP_META,
    KV_MAP_DATA,
    parseFribbMiniJson,
    force,
    now,
  );

  const existingStats = readKVJson<IndexStats>(KV_INDEX_STATS);
  const hasLists =
    existingStats !== null &&
    getEther().kv.get(KV_INDEX_DUBBED_SERIES) !== null &&
    getEther().kv.get(KV_INDEX_DUBBED_MOVIE) !== null &&
    getEther().kv.get(KV_INDEX_INCOMPLETE_SERIES) !== null &&
    getEther().kv.get(KV_INDEX_INCOMPLETE_MOVIE) !== null;

  const errors: string[] = [];
  if (dubsRes.staleError) errors.push(dubsRes.staleError);
  if (mapRes.staleError) errors.push(mapRes.staleError);
  const lastError = errors.length > 0 ? errors.join("; ") : undefined;

  if (!dubsRes.changed && !mapRes.changed && hasLists && existingStats) {
    const updatedStats: IndexStats = {
      ...existingStats,
      checkedAt: now,
      lastError,
    };
    writeKVJson(KV_INDEX_STATS, updatedStats);
    return { changed: false, stats: updatedStats };
  }

  const dubbedBuilt = buildListForMalIds(dubsRes.data.dubbed, mapRes.data);
  const incompleteBuilt = buildListForMalIds(
    dubsRes.data.incomplete,
    mapRes.data,
  );

  writeKVJson(KV_INDEX_DUBBED_SERIES, dubbedBuilt.series);
  writeKVJson(KV_INDEX_DUBBED_MOVIE, dubbedBuilt.movie);
  writeKVJson(KV_INDEX_INCOMPLETE_SERIES, incompleteBuilt.series);
  writeKVJson(KV_INDEX_INCOMPLETE_MOVIE, incompleteBuilt.movie);

  const stats: IndexStats = {
    builtAt: now,
    checkedAt: now,
    dubbedRawCount: dubsRes.data.dubbed.length,
    incompleteRawCount: dubsRes.data.incomplete.length,
    dubbedSeriesCount: dubbedBuilt.series.length,
    dubbedMovieCount: dubbedBuilt.movie.length,
    incompleteSeriesCount: incompleteBuilt.series.length,
    incompleteMovieCount: incompleteBuilt.movie.length,
    dubbedUnmapped: dubbedBuilt.unmapped,
    incompleteUnmapped: incompleteBuilt.unmapped,
    lastError,
  };
  writeKVJson(KV_INDEX_STATS, stats);
  return { changed: true, stats };
}

function ensureFreshIndex(): IndexStats {
  const now = Date.now();
  const stats = readKVJson<IndexStats>(KV_INDEX_STATS);
  const dubsMeta = readKVJson<CacheMeta>(KV_DUBS_META);
  const mapMeta = readKVJson<CacheMeta>(KV_MAP_META);
  if (
    stats &&
    dubsMeta &&
    mapMeta &&
    dubsMeta.fetchedAt > 0 &&
    mapMeta.fetchedAt > 0 &&
    now - dubsMeta.fetchedAt < CACHE_TTL_MS &&
    now - mapMeta.fetchedAt < CACHE_TTL_MS &&
    getEther().kv.get(KV_INDEX_DUBBED_SERIES) !== null
  ) {
    return stats;
  }
  return refreshCache(false).stats;
}

export function parseCatalogRoute(
  path: string,
  query?: Record<string, string | string[]>,
): { type: CatalogMediaType; id: CatalogId; skip: number } | null {
  const cleanPath = path.split("?")[0];
  if (!cleanPath.startsWith("/catalog/") || !cleanPath.endsWith(".json")) {
    return null;
  }
  const inner = cleanPath.slice("/catalog/".length, -".json".length);
  const parts = inner.split("/");
  if (parts.length < 2 || parts.length > 3) return null;

  let rawType: string;
  let rawId: string;
  try {
    rawType = decodeURIComponent(parts[0]).toLowerCase();
    rawId = decodeURIComponent(parts[1]).toLowerCase();
  } catch {
    return null;
  }

  let type: CatalogMediaType;
  if (rawType === "series" || rawType === "show" || rawType === "tv") {
    type = "series";
  } else if (rawType === "movie") {
    type = "movie";
  } else {
    return null;
  }

  if (rawId !== "dubbed" && rawId !== "incomplete") {
    return null;
  }

  let skip = 0;
  if (parts.length === 3 && parts[2]) {
    for (const pair of parts[2].split("&")) {
      const eq = pair.indexOf("=");
      if (eq <= 0) continue;
      const k = decodeURIComponent(pair.slice(0, eq));
      const v = decodeURIComponent(pair.slice(eq + 1));
      if (k === "skip") {
        const n = Number.parseInt(v, 10);
        if (Number.isFinite(n) && n > 0) skip = n;
      }
    }
  }
  if (skip === 0) {
    const qSkip = getQueryValue(query, "skip");
    if (qSkip) {
      const n = Number.parseInt(qSkip, 10);
      if (Number.isFinite(n) && n > 0) skip = n;
    }
  }

  return { type, id: rawId, skip };
}

function itemToMeta(item: CompactCatalogItem, type: CatalogMediaType): MetaPreview {
  const [id, malId, tmdbId, imdbId, tvdbId] = item;
  const meta: MetaPreview = {
    id,
    type,
    name: `MAL ${malId}`,
  };
  if (tmdbId > 0) meta.tmdb_id = tmdbId;
  if (imdbId) meta.imdb_id = imdbId;
  if (tvdbId > 0) meta.tvdb_id = tvdbId;
  return meta;
}

function handleCatalog(req: PluginRequest): PluginResponse {
  const parsed = parseCatalogRoute(req.path, req.query);
  if (!parsed) {
    return errorResponse(404, "not_found", "Catalog not found.");
  }

  const settings = normalizeSettings(getEther().settings.get());
  if (
    (parsed.type === "movie" && settings.typeFilter === "series") ||
    (parsed.type === "series" && settings.typeFilter === "movies")
  ) {
    return ok({ metas: [], hasMore: false });
  }

  ensureFreshIndex();

  let list: CompactCatalogItem[];
  if (parsed.id === "dubbed") {
    const primaryKey =
      parsed.type === "series" ? KV_INDEX_DUBBED_SERIES : KV_INDEX_DUBBED_MOVIE;
    const primary = readKVJson<CompactCatalogItem[]>(primaryKey) ?? [];
    if (settings.includeIncomplete) {
      const incKey =
        parsed.type === "series"
          ? KV_INDEX_INCOMPLETE_SERIES
          : KV_INDEX_INCOMPLETE_MOVIE;
      const secondary = readKVJson<CompactCatalogItem[]>(incKey) ?? [];
      list = mergeCatalogLists(primary, secondary);
    } else {
      list = primary;
    }
  } else {
    const incKey =
      parsed.type === "series"
        ? KV_INDEX_INCOMPLETE_SERIES
        : KV_INDEX_INCOMPLETE_MOVIE;
    list = readKVJson<CompactCatalogItem[]>(incKey) ?? [];
  }

  if (parsed.skip >= list.length) {
    return ok({ metas: [], hasMore: false });
  }

  const page = list.slice(parsed.skip, parsed.skip + PAGE_SIZE);
  const hasMore = parsed.skip + page.length < list.length;
  return ok({
    metas: page.map((item) => itemToMeta(item, parsed.type)),
    hasMore,
  });
}

function formatTypeFilter(tf: TypeFilter): string {
  if (tf === "series") return "series only";
  if (tf === "movies") return "movies only";
  return "series and movies";
}

function handleStatusView(): PluginResponse {
  const settings = normalizeSettings(getEther().settings.get());
  const stats = readKVJson<IndexStats>(KV_INDEX_STATS);

  const mappedSeries = stats ? String(stats.dubbedSeriesCount) : "0";
  const mappedMovies = stats ? String(stats.dubbedMovieCount) : "0";
  const mappedIncomplete = stats
    ? String(stats.incompleteSeriesCount + stats.incompleteMovieCount)
    : "0";

  const filterSummary = `Active settings: ${formatTypeFilter(settings.typeFilter)}, incomplete dubs ${
    settings.includeIncomplete ? "included in the main list" : "kept separate"
  }.`;

  let cacheStatus: string;
  if (!stats) {
    cacheStatus = `Cache not populated yet. ${filterSummary} Run Refresh cache or wait for the daily schedule.`;
  } else {
    const checkedIso = new Date(stats.checkedAt || stats.builtAt).toISOString();
    const gapNote =
      stats.dubbedUnmapped > 0
        ? ` ${stats.dubbedUnmapped} of ${stats.dubbedRawCount} MAL-Dubs entries had no TMDB, IMDb, or TVDB mapping.`
        : "";
    const errNote = stats.lastError
      ? ` Serving cached data after last refresh error: ${stats.lastError}.`
      : "";
    cacheStatus = `Last checked ${checkedIso}. ${filterSummary}${gapNote}${errNote}`;
  }

  return ok({
    title: "English dubbed anime",
    spec: {
      specVersion: 1,
      layout: "detail",
      header: [
        {
          kind: "page-header",
          title: "$t:page_title",
          description: { $: "subtitle" },
        },
      ],
      body: [
        {
          kind: "stat-tile",
          iconKey: "tv",
          accent: "primary",
          label: "$t:stat_mapped_series",
          value: { $: "mappedSeries" },
        },
        {
          kind: "stat-tile",
          iconKey: "film",
          accent: "primary",
          label: "$t:stat_mapped_movies",
          value: { $: "mappedMovies" },
        },
        {
          kind: "stat-tile",
          iconKey: "clock",
          accent: "primary",
          label: "$t:stat_incomplete",
          value: { $: "mappedIncomplete" },
        },
        {
          kind: "panel",
          title: "$t:panel_cache",
          blocks: [
            { kind: "text", content: { $: "cacheStatus" } },
            {
              kind: "widget",
              widget: "ether.actionButton",
              props: {
                label: "$t:action_refresh",
                action: "refresh",
              },
            },
          ],
        },
        {
          kind: "panel",
          title: "$t:panel_source",
          blocks: [{ kind: "text", content: "$t:source_credit" }],
        },
      ],
    },
    data: {
      subtitle:
        "Provides English-dubbed anime list sources for Home rows, collections, and library rules.",
      mappedSeries,
      mappedMovies,
      mappedIncomplete,
      cacheStatus,
    },
  });
}

function handleTest(): PluginResponse {
  try {
    const res = refreshCache(true);
    const total = res.stats.dubbedSeriesCount + res.stats.dubbedMovieCount;
    return ok({
      ok: total > 0 && !res.stats.lastError,
      message: res.stats.lastError
        ? `Connected with cached fallback: ${res.stats.lastError}`
        : `Mapped ${res.stats.dubbedSeriesCount} series and ${res.stats.dubbedMovieCount} movies from ${res.stats.dubbedRawCount} MAL-Dubs entries.`,
    });
  } catch (err) {
    if (isFatalHostError(err)) throw err;
    return ok({
      ok: false,
      message: err instanceof Error ? err.message : String(err),
    });
  }
}

export function handle(req: PluginRequest): PluginResponse {
  const path = (req.path || "").split("?")[0];

  if (path === "/ether/health") {
    return ok({ status: "ok" });
  }

  if (path === "/ether/configure") {
    return ok({ errors: validateSettings(parseBody(req.body)) });
  }

  if (path === "/ether/test") {
    return handleTest();
  }

  if (path === "/ether/job/refresh") {
    const body = parseBody(req.body);
    const force = body.force === false ? false : true;
    const res = refreshCache(force);
    return ok({
      done: true,
      changed: res.changed,
      stats: res.stats,
    });
  }

  if (path === "/ether/action/refresh") {
    const res = refreshCache(true);
    const total = res.stats.dubbedSeriesCount + res.stats.dubbedMovieCount;
    const toast = res.stats.lastError
      ? `Kept cached ${total} titles after refresh error.`
      : `English dubbed anime cache ready (${res.stats.dubbedSeriesCount} series, ${res.stats.dubbedMovieCount} movies).`;
    return ok({ toast, refresh: true });
  }

  if (path === "/ether/view/status") {
    return handleStatusView();
  }

  if (path.startsWith("/catalog/")) {
    return handleCatalog(req);
  }

  return errorResponse(404, "not_found", "Route not found.");
}

(globalThis as unknown as { handle?: typeof handle }).handle = handle;
