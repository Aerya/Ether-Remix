var __plugin = (() => {
  var __defProp = Object.defineProperty;
  var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
  var __getOwnPropNames = Object.getOwnPropertyNames;
  var __hasOwnProp = Object.prototype.hasOwnProperty;
  var __export = (target, all) => {
    for (var name in all)
      __defProp(target, name, { get: all[name], enumerable: true });
  };
  var __copyProps = (to, from, except, desc) => {
    if (from && typeof from === "object" || typeof from === "function") {
      for (let key of __getOwnPropNames(from))
        if (!__hasOwnProp.call(to, key) && key !== except)
          __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
    }
    return to;
  };
  var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);

  // src/plugin.ts
  var plugin_exports = {};
  __export(plugin_exports, {
    CACHE_TTL_MS: () => CACHE_TTL_MS,
    FRIBB_MAP_URL: () => FRIBB_MAP_URL,
    MAL_DUBS_URL: () => MAL_DUBS_URL,
    PAGE_SIZE: () => PAGE_SIZE,
    handle: () => handle,
    mergeCatalogLists: () => mergeCatalogLists,
    normalizeSettings: () => normalizeSettings,
    parseCatalogRoute: () => parseCatalogRoute,
    parseDubInfoJson: () => parseDubInfoJson,
    parseFribbMiniJson: () => parseFribbMiniJson,
    refreshCache: () => refreshCache,
    validateSettings: () => validateSettings
  });
  var MAL_DUBS_URL = "https://raw.githubusercontent.com/MAL-Dubs/MAL-Dubs/main/data/dubInfo.json";
  var FRIBB_MAP_URL = "https://raw.githubusercontent.com/Fribb/anime-lists/master/anime-list-mini.json";
  var CACHE_TTL_MS = 24 * 60 * 60 * 1e3;
  var PAGE_SIZE = 200;
  var KV_DUBS_META = "dubs:meta";
  var KV_DUBS_DATA = "dubs:data";
  var KV_MAP_META = "map:meta";
  var KV_MAP_DATA = "map:data";
  var KV_INDEX_STATS = "index:stats";
  var KV_INDEX_DUBBED_SERIES = "index:dubbed:series";
  var KV_INDEX_DUBBED_MOVIE = "index:dubbed:movie";
  var KV_INDEX_INCOMPLETE_SERIES = "index:incomplete:series";
  var KV_INDEX_INCOMPLETE_MOVIE = "index:incomplete:movie";
  var KIND_UNKNOWN = 0;
  var KIND_TV = 1;
  var KIND_MOVIE = 2;
  var KIND_OVA = 3;
  var KIND_ONA = 4;
  var KIND_SPECIAL = 5;
  function getEther() {
    const g = globalThis.ether;
    if (!g) {
      throw new Error("Ether host global is unavailable");
    }
    return g;
  }
  function ok(body) {
    return { status: 200, body };
  }
  function errorResponse(status, code, message) {
    return { status, body: { error: { code, message } } };
  }
  function logWarn(msg) {
    try {
      getEther().log?.("warn", msg);
    } catch {
    }
  }
  function parseBody(raw) {
    if (raw == null) return {};
    if (typeof raw === "string") {
      const trimmed = raw.trim();
      if (!trimmed) return {};
      const parsed = JSON.parse(trimmed);
      return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
    }
    if (typeof raw === "object" && !Array.isArray(raw)) {
      return raw;
    }
    return {};
  }
  function getHeader(headers, name) {
    if (!headers) return "";
    const want = name.toLowerCase();
    for (const key of Object.keys(headers)) {
      if (key.toLowerCase() === want) {
        return String(headers[key] ?? "").trim();
      }
    }
    return "";
  }
  function getQueryValue(query, key) {
    if (!query) return "";
    const val = query[key];
    if (Array.isArray(val)) return String(val[0] ?? "");
    if (typeof val === "string") return val;
    return "";
  }
  function isFatalHostError(err) {
    const code = err?.code;
    if (code === "check_dependency" || code === "forbidden" || code === "governance" || code === "aborted") {
      return true;
    }
    const msg = err instanceof Error ? err.message : String(err);
    return msg.includes("check_dependency") || msg.includes("forbidden") || msg.includes("governance");
  }
  function normalizeSettings(raw) {
    const obj = raw && typeof raw === "object" ? raw : {};
    const includeIncomplete = Boolean(obj.includeIncomplete);
    const tf = typeof obj.typeFilter === "string" ? obj.typeFilter.trim().toLowerCase() : "both";
    const typeFilter = tf === "series" || tf === "movies" || tf === "both" ? tf : "both";
    return { includeIncomplete, typeFilter };
  }
  function validateSettings(raw) {
    const errors = {};
    if (raw.includeIncomplete !== void 0 && typeof raw.includeIncomplete !== "boolean") {
      errors.includeIncomplete = "Include incomplete dubs must be true or false.";
    }
    if (raw.typeFilter !== void 0) {
      const tf = typeof raw.typeFilter === "string" ? raw.typeFilter.trim().toLowerCase() : "";
      if (tf !== "both" && tf !== "series" && tf !== "movies") {
        errors.typeFilter = "Type filter must be both, series, or movies.";
      }
    }
    return errors;
  }
  function readKVJson(key) {
    const raw = getEther().kv.get(key);
    if (!raw) return null;
    try {
      return JSON.parse(raw);
    } catch {
      return null;
    }
  }
  function writeKVJson(key, value) {
    getEther().kv.put(key, JSON.stringify(value));
  }
  function toPositiveInt(v) {
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
  function toPositiveIntList(v) {
    if (Array.isArray(v)) {
      const out = [];
      for (const item of v) {
        const n = toPositiveInt(item);
        if (n > 0 && !out.includes(n)) out.push(n);
      }
      return out;
    }
    const single = toPositiveInt(v);
    return single > 0 ? [single] : [];
  }
  function compactIntOrList(nums) {
    if (nums.length === 0) return 0;
    if (nums.length === 1) return nums[0];
    return nums;
  }
  function expandIntOrList(v) {
    if (Array.isArray(v)) return v;
    return v > 0 ? [v] : [];
  }
  function normalizeImdb(v) {
    const check = (s) => {
      if (typeof s !== "string") return "";
      const norm = s.trim().toLowerCase();
      return /^tt[0-9]+$/.test(norm) ? norm : "";
    };
    if (Array.isArray(v)) {
      for (const item of v) {
        const ok2 = check(item);
        if (ok2) return ok2;
      }
      return "";
    }
    return check(v);
  }
  function encodeKind(raw) {
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
  function parseDubInfoJson(raw) {
    let doc;
    try {
      doc = JSON.parse(raw);
    } catch (err) {
      throw new Error(
        `Invalid MAL-Dubs JSON: ${err instanceof Error ? err.message : String(err)}`
      );
    }
    if (!doc || typeof doc !== "object" || Array.isArray(doc)) {
      throw new Error("MAL-Dubs payload must be a JSON object");
    }
    const rec = doc;
    if (!Array.isArray(rec.dubbed)) {
      throw new Error("MAL-Dubs payload is missing the dubbed array");
    }
    const collect = (arr) => {
      if (!Array.isArray(arr)) return [];
      const seen = /* @__PURE__ */ new Set();
      const out = [];
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
      incomplete: collect(rec.incomplete)
    };
  }
  function mergeCompactEntry(existing, incoming) {
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
  function parseFribbMiniJson(raw) {
    let arr;
    try {
      arr = JSON.parse(raw);
    } catch (err) {
      throw new Error(
        `Invalid Fribb anime-lists JSON: ${err instanceof Error ? err.message : String(err)}`
      );
    }
    if (!Array.isArray(arr)) {
      throw new Error("Fribb anime-lists payload must be a JSON array");
    }
    const out = {};
    for (let i = 0; i < arr.length; i++) {
      const item = arr[i];
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const entry = item;
      const malId = toPositiveInt(entry.mal_id);
      if (malId <= 0) continue;
      let tmdbTv = [];
      let tmdbMovie = [];
      const rawTmdb = entry.themoviedb_id;
      if (rawTmdb && typeof rawTmdb === "object" && !Array.isArray(rawTmdb)) {
        const t = rawTmdb;
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
      const compact = [
        encodeKind(entry.type),
        compactIntOrList(tmdbTv),
        compactIntOrList(tmdbMovie),
        imdb,
        tvdb
      ];
      out[key] = mergeCompactEntry(out[key], compact);
    }
    return out;
  }
  function buildListForMalIds(malIds, fribbMap) {
    const series = [];
    const movie = [];
    const seenSeriesId = /* @__PURE__ */ new Set();
    const seenSeriesImdb = /* @__PURE__ */ new Set();
    const seenSeriesTvdb = /* @__PURE__ */ new Set();
    const seenMovieId = /* @__PURE__ */ new Set();
    const seenMovieImdb = /* @__PURE__ */ new Set();
    let unmapped = 0;
    const pushSeries = (item, isFallback) => {
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
    const pushMovie = (item, isFallback) => {
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
  function mergeCatalogLists(primary, secondary) {
    if (secondary.length === 0) return primary;
    const out = primary.slice();
    const seenId = /* @__PURE__ */ new Set();
    const seenImdb = /* @__PURE__ */ new Set();
    const seenTvdb = /* @__PURE__ */ new Set();
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
  function fetchCachedResource(url, metaKey, dataKey, parse, force, now) {
    const existingMeta = readKVJson(metaKey);
    const existingData = readKVJson(dataKey);
    const hasCache = existingData !== null;
    if (!force && hasCache && existingMeta && existingMeta.fetchedAt > 0 && now - existingMeta.fetchedAt < CACHE_TTL_MS) {
      return { changed: false, data: existingData, meta: existingMeta };
    }
    const headers = { Accept: "application/json" };
    if (hasCache && existingMeta) {
      if (existingMeta.etag) headers["If-None-Match"] = existingMeta.etag;
      if (existingMeta.lastModified) {
        headers["If-Modified-Since"] = existingMeta.lastModified;
      }
    }
    let resp;
    try {
      resp = getEther().http.fetch({
        url,
        method: "GET",
        headers,
        timeoutMs: 25e3
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
          staleError: msg
        };
      }
      throw new Error(`Failed to fetch ${url}: ${msg}`);
    }
    if (resp.status === 304 && hasCache) {
      const nextMeta2 = {
        etag: getHeader(resp.headers, "etag") || existingMeta?.etag || "",
        lastModified: getHeader(resp.headers, "last-modified") || existingMeta?.lastModified || "",
        fetchedAt: now
      };
      writeKVJson(metaKey, nextMeta2);
      return { changed: false, data: existingData, meta: nextMeta2 };
    }
    if (resp.status !== 200) {
      const msg = `HTTP ${resp.status} from ${url}`;
      if (hasCache && existingMeta) {
        logWarn(`Using cached data for ${url} after ${msg}`);
        return {
          changed: false,
          data: existingData,
          meta: existingMeta,
          staleError: msg
        };
      }
      throw new Error(msg);
    }
    const body = typeof resp.body === "string" ? resp.body : resp.body == null ? "" : JSON.stringify(resp.body);
    let parsed;
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
          staleError: msg
        };
      }
      throw err;
    }
    const nextMeta = {
      etag: getHeader(resp.headers, "etag"),
      lastModified: getHeader(resp.headers, "last-modified"),
      fetchedAt: now
    };
    writeKVJson(dataKey, parsed);
    writeKVJson(metaKey, nextMeta);
    return { changed: true, data: parsed, meta: nextMeta };
  }
  function refreshCache(force) {
    const now = Date.now();
    const dubsRes = fetchCachedResource(
      MAL_DUBS_URL,
      KV_DUBS_META,
      KV_DUBS_DATA,
      parseDubInfoJson,
      force,
      now
    );
    const mapRes = fetchCachedResource(
      FRIBB_MAP_URL,
      KV_MAP_META,
      KV_MAP_DATA,
      parseFribbMiniJson,
      force,
      now
    );
    const existingStats = readKVJson(KV_INDEX_STATS);
    const hasLists = existingStats !== null && getEther().kv.get(KV_INDEX_DUBBED_SERIES) !== null && getEther().kv.get(KV_INDEX_DUBBED_MOVIE) !== null && getEther().kv.get(KV_INDEX_INCOMPLETE_SERIES) !== null && getEther().kv.get(KV_INDEX_INCOMPLETE_MOVIE) !== null;
    const errors = [];
    if (dubsRes.staleError) errors.push(dubsRes.staleError);
    if (mapRes.staleError) errors.push(mapRes.staleError);
    const lastError = errors.length > 0 ? errors.join("; ") : void 0;
    if (!dubsRes.changed && !mapRes.changed && hasLists && existingStats) {
      const updatedStats = {
        ...existingStats,
        checkedAt: now,
        lastError
      };
      writeKVJson(KV_INDEX_STATS, updatedStats);
      return { changed: false, stats: updatedStats };
    }
    const dubbedBuilt = buildListForMalIds(dubsRes.data.dubbed, mapRes.data);
    const incompleteBuilt = buildListForMalIds(
      dubsRes.data.incomplete,
      mapRes.data
    );
    writeKVJson(KV_INDEX_DUBBED_SERIES, dubbedBuilt.series);
    writeKVJson(KV_INDEX_DUBBED_MOVIE, dubbedBuilt.movie);
    writeKVJson(KV_INDEX_INCOMPLETE_SERIES, incompleteBuilt.series);
    writeKVJson(KV_INDEX_INCOMPLETE_MOVIE, incompleteBuilt.movie);
    const stats = {
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
      lastError
    };
    writeKVJson(KV_INDEX_STATS, stats);
    return { changed: true, stats };
  }
  function ensureFreshIndex() {
    const now = Date.now();
    const stats = readKVJson(KV_INDEX_STATS);
    const dubsMeta = readKVJson(KV_DUBS_META);
    const mapMeta = readKVJson(KV_MAP_META);
    if (stats && dubsMeta && mapMeta && dubsMeta.fetchedAt > 0 && mapMeta.fetchedAt > 0 && now - dubsMeta.fetchedAt < CACHE_TTL_MS && now - mapMeta.fetchedAt < CACHE_TTL_MS && getEther().kv.get(KV_INDEX_DUBBED_SERIES) !== null) {
      return stats;
    }
    return refreshCache(false).stats;
  }
  function parseCatalogRoute(path, query) {
    const cleanPath = path.split("?")[0];
    if (!cleanPath.startsWith("/catalog/") || !cleanPath.endsWith(".json")) {
      return null;
    }
    const inner = cleanPath.slice("/catalog/".length, -".json".length);
    const parts = inner.split("/");
    if (parts.length < 2 || parts.length > 3) return null;
    let rawType;
    let rawId;
    try {
      rawType = decodeURIComponent(parts[0]).toLowerCase();
      rawId = decodeURIComponent(parts[1]).toLowerCase();
    } catch {
      return null;
    }
    let type;
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
  function itemToMeta(item, type) {
    const [id, malId, tmdbId, imdbId, tvdbId] = item;
    const meta = {
      id,
      type,
      name: `MAL ${malId}`
    };
    if (tmdbId > 0) meta.tmdb_id = tmdbId;
    if (imdbId) meta.imdb_id = imdbId;
    if (tvdbId > 0) meta.tvdb_id = tvdbId;
    return meta;
  }
  function handleCatalog(req) {
    const parsed = parseCatalogRoute(req.path, req.query);
    if (!parsed) {
      return errorResponse(404, "not_found", "Catalog not found.");
    }
    const settings = normalizeSettings(getEther().settings.get());
    if (parsed.type === "movie" && settings.typeFilter === "series" || parsed.type === "series" && settings.typeFilter === "movies") {
      return ok({ metas: [], hasMore: false });
    }
    ensureFreshIndex();
    let list;
    if (parsed.id === "dubbed") {
      const primaryKey = parsed.type === "series" ? KV_INDEX_DUBBED_SERIES : KV_INDEX_DUBBED_MOVIE;
      const primary = readKVJson(primaryKey) ?? [];
      if (settings.includeIncomplete) {
        const incKey = parsed.type === "series" ? KV_INDEX_INCOMPLETE_SERIES : KV_INDEX_INCOMPLETE_MOVIE;
        const secondary = readKVJson(incKey) ?? [];
        list = mergeCatalogLists(primary, secondary);
      } else {
        list = primary;
      }
    } else {
      const incKey = parsed.type === "series" ? KV_INDEX_INCOMPLETE_SERIES : KV_INDEX_INCOMPLETE_MOVIE;
      list = readKVJson(incKey) ?? [];
    }
    if (parsed.skip >= list.length) {
      return ok({ metas: [], hasMore: false });
    }
    const page = list.slice(parsed.skip, parsed.skip + PAGE_SIZE);
    const hasMore = parsed.skip + page.length < list.length;
    return ok({
      metas: page.map((item) => itemToMeta(item, parsed.type)),
      hasMore
    });
  }
  function formatTypeFilter(tf) {
    if (tf === "series") return "series only";
    if (tf === "movies") return "movies only";
    return "series and movies";
  }
  function handleStatusView() {
    const settings = normalizeSettings(getEther().settings.get());
    const stats = readKVJson(KV_INDEX_STATS);
    const mappedSeries = stats ? String(stats.dubbedSeriesCount) : "0";
    const mappedMovies = stats ? String(stats.dubbedMovieCount) : "0";
    const mappedIncomplete = stats ? String(stats.incompleteSeriesCount + stats.incompleteMovieCount) : "0";
    const filterSummary = `Active settings: ${formatTypeFilter(settings.typeFilter)}, incomplete dubs ${settings.includeIncomplete ? "included in the main list" : "kept separate"}.`;
    let cacheStatus;
    if (!stats) {
      cacheStatus = `Cache not populated yet. ${filterSummary} Run Refresh cache or wait for the daily schedule.`;
    } else {
      const checkedIso = new Date(stats.checkedAt || stats.builtAt).toISOString();
      const gapNote = stats.dubbedUnmapped > 0 ? ` ${stats.dubbedUnmapped} of ${stats.dubbedRawCount} MAL-Dubs entries had no TMDB, IMDb, or TVDB mapping.` : "";
      const errNote = stats.lastError ? ` Serving cached data after last refresh error: ${stats.lastError}.` : "";
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
            description: { $: "subtitle" }
          }
        ],
        body: [
          {
            kind: "stat-tile",
            iconKey: "tv",
            accent: "primary",
            label: "$t:stat_mapped_series",
            value: { $: "mappedSeries" }
          },
          {
            kind: "stat-tile",
            iconKey: "film",
            accent: "primary",
            label: "$t:stat_mapped_movies",
            value: { $: "mappedMovies" }
          },
          {
            kind: "stat-tile",
            iconKey: "clock",
            accent: "primary",
            label: "$t:stat_incomplete",
            value: { $: "mappedIncomplete" }
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
                  action: "refresh"
                }
              }
            ]
          },
          {
            kind: "panel",
            title: "$t:panel_source",
            blocks: [{ kind: "text", content: "$t:source_credit" }]
          }
        ]
      },
      data: {
        subtitle: "Provides English-dubbed anime list sources for Home rows, collections, and library rules.",
        mappedSeries,
        mappedMovies,
        mappedIncomplete,
        cacheStatus
      }
    });
  }
  function handleTest() {
    try {
      const res = refreshCache(true);
      const total = res.stats.dubbedSeriesCount + res.stats.dubbedMovieCount;
      return ok({
        ok: total > 0 && !res.stats.lastError,
        message: res.stats.lastError ? `Connected with cached fallback: ${res.stats.lastError}` : `Mapped ${res.stats.dubbedSeriesCount} series and ${res.stats.dubbedMovieCount} movies from ${res.stats.dubbedRawCount} MAL-Dubs entries.`
      });
    } catch (err) {
      if (isFatalHostError(err)) throw err;
      return ok({
        ok: false,
        message: err instanceof Error ? err.message : String(err)
      });
    }
  }
  function handle(req) {
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
        stats: res.stats
      });
    }
    if (path === "/ether/action/refresh") {
      const res = refreshCache(true);
      const total = res.stats.dubbedSeriesCount + res.stats.dubbedMovieCount;
      const toast = res.stats.lastError ? `Kept cached ${total} titles after refresh error.` : `English dubbed anime cache ready (${res.stats.dubbedSeriesCount} series, ${res.stats.dubbedMovieCount} movies).`;
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
  globalThis.handle = handle;
  return __toCommonJS(plugin_exports);
})();
