import { plugin, PluginRouteError } from "../../typescript/src/router.ts";
import type { MaybePromise, RawRequest, RawResponse, StreamItem } from "../../typescript/src/types.ts";
import manifest from "./manifest.json";
import schema from "./settings.schema.json";

const API_BASE = "https://feed.animetosho.net";
const USER_AGENT = "Ether/0.3 (+https://github.com/braedonsaunders/ether)";
const DEFAULT_LIMIT = 75;
const MAX_XML_BYTES = 4 * 1024 * 1024;
const MAX_PARSED_ITEMS = 200;
const MAX_TRACKERS = 8;

export type ReleaseTypes = "both" | "torrents" | "nzbs";

export interface AnimeToshoSettings {
  releaseTypes: ReleaseTypes;
  category: string;
}

export const DEFAULT_SETTINGS: AnimeToshoSettings = {
  releaseTypes: "both",
  category: "",
};

type AnimeToshoStreamItem = StreamItem & {
  nzbUrl?: string;
  sources?: string[];
};

export interface ParsedMediaID {
  mediaType: "movie" | "series";
  prefix: "tt" | "tmdb" | "tvdb" | "anidb" | "mal" | "kitsu";
  id: string;
  season?: number;
  episode?: number;
  isAnimeEntryId: boolean;
}

export interface SearchPlan {
  kind: "movie" | "episode" | "season_pack";
  endpointPath: "/api" | "/api/torznab" | "/api/newznab";
  url: string;
  season?: number;
  episode?: number;
  isAnimeEntryId: boolean;
  categories: string[];
}

export interface FeedItem {
  title: string;
  infoHash: string;
  nzbUrl: string;
  trackers: string[];
  size: number;
  seeders?: number;
  files?: number;
  season?: number;
  episode?: number;
  categories: string[];
}

export interface ParsedFeed {
  items: FeedItem[];
  apiErrorCode?: string;
  apiErrorDescription?: string;
}

const VALID_RELEASE_TYPES = new Set<string>(["both", "torrents", "nzbs"]);
const CATEGORY_LIST_RE = /^\d+(?:,\d+)*$/;
const HEX40_RE = /^[a-fA-F0-9]{40}$/;
const BASE32_32_RE = /^[A-Z2-7]{32}$/i;
const VIDEO_EXT_RE = /\.(mkv|mp4|m4v|avi|webm|ts|mov)$/i;
const BASE32_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

export function normalizeSettings(raw: Partial<AnimeToshoSettings> | Record<string, unknown> | undefined): AnimeToshoSettings {
  const releaseTypesRaw = typeof raw?.releaseTypes === "string" ? raw.releaseTypes.trim().toLowerCase() : "";
  const releaseTypes: ReleaseTypes = VALID_RELEASE_TYPES.has(releaseTypesRaw)
    ? (releaseTypesRaw as ReleaseTypes)
    : DEFAULT_SETTINGS.releaseTypes;
  const categoryRaw = typeof raw?.category === "string" ? raw.category.replace(/\s+/g, "") : "";
  const category = categoryRaw && CATEGORY_LIST_RE.test(categoryRaw) ? categoryRaw : "";
  return { releaseTypes, category };
}

export function validateSettings(raw: Partial<AnimeToshoSettings> | Record<string, unknown> | undefined): Record<string, string> {
  const errors: Record<string, string> = {};
  if (raw && raw.releaseTypes !== undefined && raw.releaseTypes !== "") {
    const val = typeof raw.releaseTypes === "string" ? raw.releaseTypes.trim().toLowerCase() : "";
    if (!VALID_RELEASE_TYPES.has(val)) {
      errors["/releaseTypes"] = "Choose torrents, NZBs, or both.";
    }
  }
  if (raw && raw.category !== undefined && raw.category !== "") {
    const val = typeof raw.category === "string" ? raw.category.replace(/\s+/g, "") : "";
    if (!CATEGORY_LIST_RE.test(val)) {
      errors["/category"] = "Choose a valid Torznab or Newznab category.";
    }
  }
  return errors;
}

function parseNonNegInt(raw: string | undefined): number | undefined {
  if (!raw || !/^\d+$/.test(raw)) return undefined;
  const n = Number(raw);
  return Number.isSafeInteger(n) && n >= 0 ? n : undefined;
}

function parsePosInt(raw: string | undefined): number | undefined {
  const n = parseNonNegInt(raw);
  return n !== undefined && n > 0 ? n : undefined;
}

export function parseMediaID(mediaType: string, rawId: string): ParsedMediaID | null {
  if (mediaType !== "movie" && mediaType !== "series") return null;
  const trimmed = (rawId || "").trim();
  if (!trimmed) return null;

  const parts = trimmed.split(":");
  if (parts[0].startsWith("tt")) {
    if (!/^tt\d+$/.test(parts[0])) return null;
    if (mediaType === "movie") {
      if (parts.length !== 1) return null;
      return { mediaType, prefix: "tt", id: parts[0], isAnimeEntryId: false };
    }
    if (parts.length === 2) {
      const season = parseNonNegInt(parts[1]);
      if (season === undefined) return null;
      return { mediaType, prefix: "tt", id: parts[0], season, isAnimeEntryId: false };
    }
    if (parts.length === 3) {
      const season = parseNonNegInt(parts[1]);
      const episode = parsePosInt(parts[2]);
      if (season === undefined || episode === undefined) return null;
      return { mediaType, prefix: "tt", id: parts[0], season, episode, isAnimeEntryId: false };
    }
    return null;
  }

  const prefix = parts[0].toLowerCase();
  if (prefix === "tmdb" || prefix === "tvdb") {
    const numericId = parts[1];
    if (!numericId || !/^\d+$/.test(numericId) || numericId === "0") return null;
    if (mediaType === "movie") {
      if (prefix !== "tmdb" || parts.length !== 2) return null;
      return { mediaType, prefix, id: numericId, isAnimeEntryId: false };
    }
    if (parts.length === 3) {
      const season = parseNonNegInt(parts[2]);
      if (season === undefined) return null;
      return { mediaType, prefix, id: numericId, season, isAnimeEntryId: false };
    }
    if (parts.length === 4) {
      const season = parseNonNegInt(parts[2]);
      const episode = parsePosInt(parts[3]);
      if (season === undefined || episode === undefined) return null;
      return { mediaType, prefix, id: numericId, season, episode, isAnimeEntryId: false };
    }
    return null;
  }

  if (prefix === "anidb" || prefix === "mal" || prefix === "kitsu") {
    const numericId = parts[1];
    if (!numericId || !/^\d+$/.test(numericId) || numericId === "0") return null;
    if (mediaType === "movie") {
      if (parts.length !== 2) return null;
      return { mediaType, prefix, id: numericId, isAnimeEntryId: true };
    }
    if (parts.length === 2) {
      return { mediaType, prefix, id: numericId, isAnimeEntryId: true };
    }
    if (parts.length === 3) {
      const episode = parsePosInt(parts[2]);
      if (episode === undefined) return null;
      return { mediaType, prefix, id: numericId, episode, isAnimeEntryId: true };
    }
    return null;
  }

  return null;
}

function endpointForSettings(settings: AnimeToshoSettings): "/api" | "/api/torznab" | "/api/newznab" {
  if (settings.releaseTypes === "nzbs") return "/api/newznab";
  if (settings.releaseTypes === "torrents") return "/api/torznab";
  return "/api";
}

function buildQueryString(params: Array<[string, string]>): string {
  return params.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`).join("&");
}

function cleanImdbHint(raw: string | undefined): string {
  const s = (raw || "").trim().toLowerCase();
  return /^tt\d+$/.test(s) ? s : "";
}

function cleanNumericHint(raw: string | undefined): string {
  const s = (raw || "").trim();
  return /^\d+$/.test(s) && s !== "0" ? s : "";
}

export function buildSearchPlan(
  mediaType: string,
  rawId: string,
  query: Record<string, string>,
  settings: AnimeToshoSettings,
): SearchPlan | null {
  const parsed = parseMediaID(mediaType, rawId);
  if (!parsed) return null;

  const endpointPath = endpointForSettings(settings);
  const categories = settings.category ? settings.category.split(",").filter(Boolean) : [];
  const params: Array<[string, string]> = [];

  if (parsed.mediaType === "movie") {
    if (parsed.prefix === "anidb") {
      // Anime Tosho NEW caps list aid on t=search rather than t=movie.
      params.push(["t", "search"], ["aid", parsed.id]);
    } else if (parsed.prefix === "mal") {
      params.push(["t", "movie"], ["malid", parsed.id]);
    } else if (parsed.prefix === "kitsu") {
      params.push(["t", "movie"], ["kitsuid", parsed.id]);
    } else {
      const imdbId = parsed.prefix === "tt" ? parsed.id : cleanImdbHint(query.imdbId);
      const tmdbId = parsed.prefix === "tmdb" ? parsed.id : cleanNumericHint(query.tmdbId);
      if (imdbId) {
        params.push(["t", "movie"], ["imdbid", imdbId]);
      } else if (tmdbId) {
        params.push(["t", "movie"], ["tmdbid", tmdbId]);
      } else {
        return null;
      }
    }
    if (categories.length > 0) params.push(["cat", categories.join(",")]);
    params.push(["limit", String(DEFAULT_LIMIT)]);
    return {
      kind: "movie",
      endpointPath,
      url: `${API_BASE}${endpointPath}?${buildQueryString(params)}`,
      isAnimeEntryId: parsed.isAnimeEntryId,
      categories,
    };
  }

  // Series: episode or season pack.
  params.push(["t", "tvsearch"]);

  if (parsed.prefix === "anidb") {
    params.push(["aid", parsed.id]);
  } else if (parsed.prefix === "mal") {
    params.push(["malid", parsed.id]);
  } else if (parsed.prefix === "kitsu") {
    params.push(["kitsuid", parsed.id]);
  } else {
    // Anime Tosho NEW maps multi-season TV series much more reliably by TMDB
    // than by IMDb, so prefer tmdbid when Ether supplies it in the query hints.
    const tmdbId = parsed.prefix === "tmdb" ? parsed.id : cleanNumericHint(query.tmdbId);
    const tvdbId = parsed.prefix === "tvdb" ? parsed.id : "";
    const imdbId = parsed.prefix === "tt" ? parsed.id : cleanImdbHint(query.imdbId);
    if (tmdbId) {
      params.push(["tmdbid", tmdbId]);
    } else if (tvdbId) {
      params.push(["tvdbid", tvdbId]);
    } else if (imdbId) {
      params.push(["imdbid", imdbId]);
    } else {
      return null;
    }
  }

  const querySeason = parseNonNegInt(query.season);
  const hasQuerySeason = querySeason !== undefined;
  const hasQueryEpisode = parsePosInt(query.episode) !== undefined;
  const explicitPack = query.pack === "1" || query.pack === "true";
  const isSeasonPack = parsed.isAnimeEntryId
    ? parsed.episode === undefined || explicitPack
    : parsed.episode === undefined || explicitPack || (hasQuerySeason && !hasQueryEpisode);

  const season = parsed.isAnimeEntryId ? undefined : (parsed.season ?? querySeason);
  const episode = isSeasonPack ? undefined : parsed.episode;

  if (isSeasonPack) {
    if (!parsed.isAnimeEntryId && season !== undefined && season > 0) {
      params.push(["season", String(season)]);
    }
  } else if (episode !== undefined) {
    if (parsed.isAnimeEntryId) {
      params.push(["ep", String(episode)]);
    } else if (season === 1 && episode > 12) {
      // Absolute episode alias (e.g. One Piece :1:1071 or Attack on Titan :1:52):
      // omit season so Anime Tosho matches absolute numbering across seasons.
      params.push(["ep", String(episode)]);
    } else {
      if (season !== undefined) params.push(["season", String(season)]);
      params.push(["ep", String(episode)]);
    }
  }

  if (categories.length > 0) params.push(["cat", categories.join(",")]);
  params.push(["limit", String(DEFAULT_LIMIT)]);

  return {
    kind: isSeasonPack ? "season_pack" : "episode",
    endpointPath,
    url: `${API_BASE}${endpointPath}?${buildQueryString(params)}`,
    season,
    episode,
    isAnimeEntryId: parsed.isAnimeEntryId,
    categories,
  };
}

export function decodeXmlEntities(input: string): string {
  if (!input || !input.includes("&")) return input;
  return input.replace(/&(#x[0-9a-fA-F]+|#\d+|amp|lt|gt|quot|apos);/g, (full, ent: string) => {
    switch (ent) {
      case "amp":
        return "&";
      case "lt":
        return "<";
      case "gt":
        return ">";
      case "quot":
        return '"';
      case "apos":
        return "'";
      default: {
        const code = ent.startsWith("#x") || ent.startsWith("#X")
          ? parseInt(ent.slice(2), 16)
          : parseInt(ent.slice(1), 10);
        if (!Number.isFinite(code) || code <= 0 || code > 0x10ffff) return full;
        try {
          return String.fromCodePoint(code);
        } catch {
          return full;
        }
      }
    }
  });
}

function unwrapCdata(raw: string): string {
  const trimmed = raw.trim();
  if (trimmed.startsWith("<![CDATA[") && trimmed.endsWith("]]>")) {
    return trimmed.slice(9, -3);
  }
  return decodeXmlEntities(trimmed);
}

function parseTagAttributes(tagText: string): Record<string, string> {
  const attrs: Record<string, string> = {};
  const re = /([a-zA-Z0-9_:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(tagText)) !== null) {
    attrs[m[1].toLowerCase()] = decodeXmlEntities(m[2] ?? m[3] ?? "");
  }
  return attrs;
}

export function base32ToHex(b32: string): string {
  if (!BASE32_32_RE.test(b32)) return "";
  const upper = b32.toUpperCase();
  let bits = 0;
  let value = 0;
  let out = "";
  for (let i = 0; i < upper.length; i++) {
    const idx = BASE32_ALPHABET.indexOf(upper[i]);
    if (idx < 0) return "";
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      const byte = (value >> bits) & 0xff;
      out += byte.toString(16).padStart(2, "0");
    }
  }
  return out.length === 40 ? out : "";
}

function normalizeInfoHash(raw: string): string {
  const trimmed = (raw || "").trim();
  if (HEX40_RE.test(trimmed)) return trimmed.toLowerCase();
  if (BASE32_32_RE.test(trimmed)) return base32ToHex(trimmed);
  return "";
}

function safeDecodeURI(raw: string): string {
  try {
    return decodeURIComponent(raw.replace(/\+/g, " "));
  } catch {
    return raw;
  }
}

export function parseMagnet(magnetUrl: string): { infoHash: string; trackers: string[] } {
  if (!magnetUrl || !magnetUrl.toLowerCase().startsWith("magnet:?")) {
    return { infoHash: "", trackers: [] };
  }
  let infoHash = "";
  const trackers: string[] = [];
  const seenTrackers = new Set<string>();
  const query = magnetUrl.slice(magnetUrl.indexOf("?") + 1);
  for (const part of query.split("&")) {
    if (!part) continue;
    const eq = part.indexOf("=");
    const key = (eq < 0 ? part : part.slice(0, eq)).toLowerCase();
    const val = eq < 0 ? "" : safeDecodeURI(part.slice(eq + 1)).trim();
    if (key === "xt" && !infoHash) {
      const m = /^urn:btih:([a-zA-Z0-9]+)$/i.exec(val);
      if (m) infoHash = normalizeInfoHash(m[1]);
    } else if (key === "tr" && trackers.length < MAX_TRACKERS) {
      if (/^(https?|udp):\/\/\S+$/i.test(val) && !seenTrackers.has(val)) {
        seenTrackers.add(val);
        trackers.push(val);
      }
    }
  }
  return { infoHash, trackers };
}

function isSafeHttpUrl(raw: string): boolean {
  return /^https?:\/\/[^\s"'<>]+$/i.test(raw.trim());
}

function extractTagContent(block: string, tagName: string): string {
  const open = `<${tagName}`;
  const close = `</${tagName}>`;
  let pos = block.indexOf(open);
  while (pos >= 0) {
    const nextChar = block[pos + open.length];
    if (nextChar === ">" || nextChar === " " || nextChar === "\t" || nextChar === "\n" || nextChar === "\r") {
      const gt = block.indexOf(">", pos + open.length);
      if (gt < 0) return "";
      const end = block.indexOf(close, gt + 1);
      if (end < 0) return "";
      return unwrapCdata(block.slice(gt + 1, end));
    }
    pos = block.indexOf(open, pos + open.length);
  }
  return "";
}

export function parseTorznabFeed(xmlInput: string): ParsedFeed {
  if (!xmlInput) return { items: [] };
  const xml = xmlInput.length > MAX_XML_BYTES ? xmlInput.slice(0, MAX_XML_BYTES) : xmlInput;

  // Check top-level Newznab/Torznab XML error element.
  const errMatch = /<error\b([^>]*?)\/?\s*>/i.exec(xml);
  if (errMatch && !xml.includes("<channel")) {
    const attrs = parseTagAttributes(errMatch[1]);
    return {
      items: [],
      apiErrorCode: attrs.code || "error",
      apiErrorDescription: attrs.description || "Upstream feed error.",
    };
  }

  const items: FeedItem[] = [];
  let cursor = 0;
  while (items.length < MAX_PARSED_ITEMS) {
    const start = xml.indexOf("<item", cursor);
    if (start < 0) break;
    const afterName = xml[start + 5];
    if (afterName !== ">" && afterName !== " " && afterName !== "\t" && afterName !== "\n" && afterName !== "\r") {
      cursor = start + 5;
      continue;
    }
    const startGt = xml.indexOf(">", start + 5);
    if (startGt < 0) break;
    const end = xml.indexOf("</item>", startGt + 1);
    if (end < 0) break;
    cursor = end + 7;

    const block = xml.slice(startGt + 1, end);
    const title = extractTagContent(block, "title").trim();
    if (!title) continue;

    let infoHash = "";
    let nzbUrl = "";
    let magnetUrl = "";
    let size = 0;
    let seeders: number | undefined;
    let files: number | undefined;
    let season: number | undefined;
    let episode: number | undefined;
    const catSet = new Set<string>();

    // Inspect <enclosure .../> tags (may have both torrent and nzb enclosures).
    const encRe = /<enclosure\b([^>]*?)\/?\s*>/gi;
    let encMatch: RegExpExecArray | null;
    while ((encMatch = encRe.exec(block)) !== null) {
      const a = parseTagAttributes(encMatch[1]);
      const type = (a.type || "").toLowerCase();
      const url = (a.url || "").trim();
      const len = parseNonNegInt(a.length);
      if (len && len > size) size = len;
      if (type === "application/x-nzb" && isSafeHttpUrl(url) && !nzbUrl) {
        nzbUrl = url;
      }
    }

    // Inspect <torznab:attr .../> and <newznab:attr .../> tags.
    const attrRe = /<(?:torznab|newznab):attr\b([^>]*?)\/?\s*>/gi;
    let attrMatch: RegExpExecArray | null;
    while ((attrMatch = attrRe.exec(block)) !== null) {
      const a = parseTagAttributes(attrMatch[1]);
      const name = (a.name || "").toLowerCase();
      const value = (a.value || "").trim();
      if (!name || !value) continue;
      switch (name) {
        case "infohash": {
          const h = normalizeInfoHash(value);
          if (h) infoHash = h;
          break;
        }
        case "magneturl":
          if (!magnetUrl) magnetUrl = value;
          break;
        case "nzburl":
          if (isSafeHttpUrl(value)) nzbUrl = value;
          break;
        case "size": {
          const n = parseNonNegInt(value);
          if (n !== undefined && n > 0) size = n;
          break;
        }
        case "seeders": {
          const n = parseNonNegInt(value);
          if (n !== undefined) seeders = n;
          break;
        }
        case "files": {
          const n = parsePosInt(value);
          if (n !== undefined) files = n;
          break;
        }
        case "season": {
          const n = parseNonNegInt(value);
          if (n !== undefined) season = n;
          break;
        }
        case "episode": {
          const n = parsePosInt(value);
          if (n !== undefined) episode = n;
          break;
        }
        case "category":
          if (/^\d+$/.test(value)) catSet.add(value);
          break;
      }
    }

    const magnet = parseMagnet(magnetUrl);
    if (!infoHash && magnet.infoHash) infoHash = magnet.infoHash;

    items.push({
      title,
      infoHash,
      nzbUrl,
      trackers: magnet.trackers,
      size,
      seeders,
      files,
      season,
      episode,
      categories: Array.from(catSet),
    });
  }

  return { items };
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return unit === 0 ? `${Math.round(value)} B` : `${value.toFixed(1)} ${units[unit]}`;
}

export function itemsToStreams(
  items: FeedItem[],
  plan: SearchPlan,
  settings: AnimeToshoSettings,
): AnimeToshoStreamItem[] {
  const wantedCats = new Set(plan.categories);
  const ranked: Array<{ tier: number; index: number; item: FeedItem }> = [];

  for (let i = 0; i < items.length; i++) {
    const item = items[i];
    if (wantedCats.size > 0 && item.categories.length > 0) {
      if (!item.categories.some((c) => wantedCats.has(c))) continue;
    }

    let tier = 1;
    if (plan.kind === "season_pack") {
      // Single-episode items carry an explicit episode attribute; exclude them for pack searches.
      if (item.episode !== undefined) continue;
      if (!plan.isAnimeEntryId && plan.season !== undefined && plan.season > 0) {
        if (item.season !== undefined) {
          if (item.season !== plan.season) continue;
          tier = 0;
        }
      }
    } else if (plan.kind === "episode" && !plan.isAnimeEntryId && plan.season !== undefined) {
      if (item.season !== undefined) {
        if (item.season === plan.season) {
          tier = 0;
        } else if (plan.season === 1 && plan.episode !== undefined && plan.episode > 12) {
          // Absolute-episode alias (e.g. :1:52 matching season 3 ep 52): keep behind exact S1 matches.
          tier = 2;
        } else {
          continue;
        }
      }
    }

    ranked.push({ tier, index: i, item });
  }

  ranked.sort((a, b) => (a.tier !== b.tier ? a.tier - b.tier : a.index - b.index));

  const includeTorrents = settings.releaseTypes !== "nzbs";
  const includeNzbs = settings.releaseTypes !== "torrents";
  const seenHashes = new Set<string>();
  const seenNzbs = new Set<string>();
  const streams: AnimeToshoStreamItem[] = [];

  for (const { item } of ranked) {
    const sizeLabel = formatBytes(item.size);
    const behaviorHints: Record<string, unknown> = {};
    if (item.size > 0) behaviorHints.videoSize = item.size;

    if (includeTorrents && item.infoHash && !seenHashes.has(item.infoHash)) {
      seenHashes.add(item.infoHash);
      const details: string[] = [];
      if (item.seeders !== undefined) details.push(`👤 ${item.seeders}`);
      if (sizeLabel) details.push(`💾 ${sizeLabel}`);
      const torrentHints: Record<string, unknown> = { ...behaviorHints };
      if (plan.kind !== "season_pack" && item.files === 1 && VIDEO_EXT_RE.test(item.title)) {
        torrentHints.filename = item.title;
      }
      const stream: AnimeToshoStreamItem = {
        name: "Anime Tosho",
        title: details.length > 0 ? `${item.title}\n${details.join("  ")}` : item.title,
        infoHash: item.infoHash,
      };
      if (item.trackers.length > 0) {
        stream.sources = item.trackers.map((tr) => `tracker:${tr}`);
      }
      if (Object.keys(torrentHints).length > 0) {
        stream.behaviorHints = torrentHints;
      }
      streams.push(stream);
    }

    if (includeNzbs && item.nzbUrl && !seenNzbs.has(item.nzbUrl)) {
      seenNzbs.add(item.nzbUrl);
      const details: string[] = [];
      if (sizeLabel) details.push(`💾 ${sizeLabel}`);
      details.push("Usenet NZB");
      const stream: AnimeToshoStreamItem = {
        name: "Anime Tosho NZB",
        title: `${item.title}\n${details.join(" · ")}`,
        nzbUrl: item.nzbUrl,
      };
      if (Object.keys(behaviorHints).length > 0) {
        stream.behaviorHints = behaviorHints;
      }
      streams.push(stream);
    }
  }

  return streams;
}

function getHeaderCI(headers: Record<string, string> | undefined, name: string): string {
  if (!headers) return "";
  const target = name.toLowerCase();
  for (const [k, v] of Object.entries(headers)) {
    if (k.toLowerCase() === target) return String(v || "").trim();
  }
  return "";
}

function parseRetryAfter(headers: Record<string, string> | undefined): string {
  const raw = getHeaderCI(headers, "Retry-After");
  return raw || "60";
}

let pendingRetryAfter = "";

const settingsManifest = {
  ...manifest,
  ether: { ...manifest.ether, settings: schema },
} as any;

export const animeTosho = plugin<AnimeToshoSettings>({
  manifest: settingsManifest,
  defaultSettings: DEFAULT_SETTINGS,
  routes: {
    health() {
      return { status: "ok" };
    },

    configure(settings) {
      const errors = validateSettings(settings as unknown as Record<string, unknown>);
      return { errors };
    },

    test(ctx) {
      const res = ctx.ether.http.fetch({
        url: `${API_BASE}/api?t=caps`,
        headers: {
          "User-Agent": USER_AGENT,
          Accept: "application/xml, text/xml",
        },
      }) as { status: number; headers?: Record<string, string>; body?: string };

      if (res.status === 429) {
        return { ok: false, message: "Anime Tosho NEW rate limit reached. Try again later." };
      }
      if (res.status !== 200 || !res.body || !res.body.includes("<caps")) {
        return { ok: false, message: "Could not read Anime Tosho NEW feed capabilities." };
      }
      const settings = normalizeSettings(ctx.settings);
      const modeLabel =
        settings.releaseTypes === "torrents"
          ? "Torznab torrents"
          : settings.releaseTypes === "nzbs"
            ? "Newznab Usenet NZBs"
            : "Torznab torrents and Newznab Usenet NZBs";
      return {
        ok: true,
        message: `Connected to Anime Tosho NEW (${modeLabel}).`,
      };
    },

    stream(req, ctx) {
      const settings = normalizeSettings(ctx.settings);
      const mergedQuery: Record<string, string> = { ...req.extra, ...ctx.query };
      const plan = buildSearchPlan(req.type, req.id, mergedQuery, settings);
      if (!plan) {
        return { streams: [] };
      }

      const res = ctx.ether.http.fetch({
        url: plan.url,
        headers: {
          "User-Agent": USER_AGENT,
          Accept: "application/xml, text/xml",
        },
      }) as { status: number; headers?: Record<string, string>; body?: string };

      if (res.status === 429 || (res.status === 503 && getHeaderCI(res.headers, "Retry-After"))) {
        pendingRetryAfter = parseRetryAfter(res.headers);
        throw new PluginRouteError(
          429,
          "rate_limited",
          "Anime Tosho NEW rate limit reached. Try again later.",
        );
      }
      if (res.status !== 200) {
        throw new PluginRouteError(
          502,
          "upstream_error",
          `Anime Tosho NEW search failed with HTTP ${res.status}.`,
        );
      }

      const parsed = parseTorznabFeed(res.body || "");
      if (parsed.apiErrorCode) {
        const desc = parsed.apiErrorDescription || "Anime Tosho NEW returned an error.";
        if (parsed.apiErrorCode === "429" || /rate\s*limit|too many requests/i.test(desc)) {
          pendingRetryAfter = parseRetryAfter(res.headers);
          throw new PluginRouteError(429, "rate_limited", desc);
        }
        throw new PluginRouteError(502, "upstream_error", desc);
      }

      return { streams: itemsToStreams(parsed.items, plan, settings) };
    },
  },
});

export function handle(req: RawRequest): MaybePromise<RawResponse> {
  pendingRetryAfter = "";
  const finalize = (res: RawResponse): RawResponse => {
    const retryAfter = pendingRetryAfter;
    pendingRetryAfter = "";
    if (res.status === 429 && retryAfter) {
      return {
        ...res,
        headers: {
          ...(res.headers ?? {}),
          "Retry-After": retryAfter,
        },
      };
    }
    return res;
  };
  const out = animeTosho.handle(req);
  return out instanceof Promise ? out.then(finalize) : finalize(out);
}

if (
  typeof globalThis !== "undefined" &&
  typeof (globalThis as Record<string, unknown>).ether === "object" &&
  (globalThis as Record<string, unknown>).ether !== null
) {
  (globalThis as Record<string, unknown>).handle = handle;
}
