/**
 * First-party Ether Sports hosted plugin (org.ether.sports).
 *
 * Implements Ether's v0.3.19 Live Events contract (spec sections A1, A2, A3, C):
 * - GET /ether/v1/live/sports/events?from=&to=&league=&ids=
 * - GET /ether/v1/live/sports/events/{id}
 * - GET /ether/v1/live/sports/entities?kind=team&league=&q=
 * - GET /ether/v1/live/sports/standings?league=
 * - GET /ether/health
 * - POST /ether/configure
 * - POST /ether/test
 *
 * The plugin never polls on its own; Ether calls it on demand.
 */

import { KvCache } from "./cache.ts";
import type {
  EntitiesResponse,
  EventsResponse,
  HostedEtherGlobal,
  HostedRequest,
  HostedResponse,
  StandingsResponse,
} from "./contract.ts";
import { normalizeRegion, VALID_REGIONS } from "./hints.ts";
import {
  DEFAULT_LEAGUE_IDS,
  parseEventId,
  resolveEnabledLeagues,
  resolveLeague,
  type LeagueConfig,
} from "./leagues.ts";
import { EspnSportsProvider } from "./providers/espn.ts";
import {
  throwIfFatalHostError,
  type ProviderRuntime,
  type SportsProvider,
} from "./providers/types.ts";

export interface SportsSettings {
  leagues: string[];
  region: string;
}

export interface SportsHandlerOptions {
  provider?: SportsProvider;
  getEther?: () => HostedEtherGlobal;
  now?: () => number;
}

const FEED_PREFIX = "/ether/v1/live/sports";

function defaultGetEther(): HostedEtherGlobal {
  const g = globalThis as unknown as { ether?: HostedEtherGlobal };
  if (!g.ether) {
    throw new Error("Ether hosted runtime globalThis.ether is not available");
  }
  return g.ether;
}

function queryParam(
  query: Record<string, string | string[]> | undefined,
  key: string,
): string | undefined {
  if (!query) return undefined;
  const val = query[key];
  if (typeof val === "string") return val.trim() || undefined;
  if (Array.isArray(val)) {
    for (const item of val) {
      if (typeof item === "string" && item.trim()) return item.trim();
    }
  }
  return undefined;
}

function queryList(
  query: Record<string, string | string[]> | undefined,
  key: string,
): string[] {
  if (!query) return [];
  const val = query[key];
  const rawList = typeof val === "string" ? [val] : Array.isArray(val) ? val : [];
  const out: string[] = [];
  for (const raw of rawList) {
    if (typeof raw !== "string") continue;
    for (const part of raw.split(",")) {
      const clean = part.trim();
      if (clean) out.push(clean);
    }
  }
  return out;
}

function parseSettings(raw: Record<string, unknown> | undefined): {
  leagues: LeagueConfig[];
  region: string;
} {
  const enabled = resolveEnabledLeagues(raw?.leagues);
  const leagues = enabled.length > 0 ? enabled : resolveEnabledLeagues(DEFAULT_LEAGUE_IDS);
  const region = normalizeRegion(raw?.region);
  return { leagues, region };
}

function validateConfigureBody(body: unknown): {
  settings?: SportsSettings;
  errors?: Record<string, string>;
} {
  if (body !== undefined && body !== null && typeof body !== "object") {
    return { errors: { "/": "must be an object" } };
  }
  const obj = (body || {}) as Record<string, unknown>;
  const errors: Record<string, string> = {};

  let leagueIds: string[] = [...DEFAULT_LEAGUE_IDS];
  if (obj.leagues !== undefined) {
    if (!Array.isArray(obj.leagues)) {
      errors["/leagues"] = "must be a list of league IDs";
    } else if (obj.leagues.length === 0) {
      errors["/leagues"] = "select at least one league";
    } else {
      const resolved: string[] = [];
      const seen = new Set<string>();
      const invalid: string[] = [];
      for (const item of obj.leagues) {
        if (typeof item !== "string") {
          invalid.push(String(item));
          continue;
        }
        const lg = resolveLeague(item);
        if (!lg) {
          invalid.push(item);
        } else if (!seen.has(lg.id)) {
          seen.add(lg.id);
          resolved.push(lg.id);
        }
      }
      if (invalid.length > 0) {
        errors["/leagues"] = `unknown league(s): ${invalid.join(", ")}`;
      } else {
        leagueIds = resolved;
      }
    }
  }

  let region = "us";
  if (obj.region !== undefined && obj.region !== "") {
    if (typeof obj.region !== "string" || !VALID_REGIONS.has(obj.region.trim().toLowerCase())) {
      errors["/region"] = "must be one of us, ca, gb, eu, au, or all";
    } else {
      region = obj.region.trim().toLowerCase();
    }
  }

  if (Object.keys(errors).length > 0) {
    return { errors };
  }
  return { settings: { leagues: leagueIds, region } };
}

function resolveRequestedLeagues(
  leagueParam: string[],
  fallbackLeagues: LeagueConfig[],
): { leagues?: LeagueConfig[]; unknown?: string[] } {
  if (leagueParam.length === 0) {
    return { leagues: fallbackLeagues };
  }
  const resolved: LeagueConfig[] = [];
  const seen = new Set<string>();
  const unknown: string[] = [];
  for (const item of leagueParam) {
    const lg = resolveLeague(item);
    if (!lg) {
      unknown.push(item);
    } else if (!seen.has(lg.id)) {
      seen.add(lg.id);
      resolved.push(lg);
    }
  }
  if (unknown.length > 0) {
    return { unknown };
  }
  return { leagues: resolved };
}

function jsonResponse(status: number, body: unknown): HostedResponse {
  return {
    status,
    headers: { "content-type": "application/json" },
    body,
  };
}

function errorResponse(
  status: number,
  code: string,
  message: string,
  extra?: Record<string, unknown>,
): HostedResponse {
  return jsonResponse(status, {
    error: { code, message, ...extra },
  });
}

function parsePathAndQuery(req: HostedRequest): {
  path: string;
  query: Record<string, string | string[]>;
} {
  const merged: Record<string, string | string[]> = {};
  if (req.query) {
    for (const [k, v] of Object.entries(req.query)) {
      merged[k] = v;
    }
  }
  let cleanPath = req.path || "/";
  const qIdx = cleanPath.indexOf("?");
  if (qIdx >= 0) {
    const qs = cleanPath.slice(qIdx + 1);
    cleanPath = cleanPath.slice(0, qIdx);
    for (const pair of qs.split("&")) {
      if (!pair) continue;
      const eq = pair.indexOf("=");
      const rawK = eq >= 0 ? pair.slice(0, eq) : pair;
      const rawV = eq >= 0 ? pair.slice(eq + 1) : "";
      const k = decodeURIComponent(rawK.replace(/\+/g, " "));
      const v = decodeURIComponent(rawV.replace(/\+/g, " "));
      if (merged[k] === undefined) {
        merged[k] = v;
      }
    }
  }
  if (cleanPath.length > 1 && cleanPath.endsWith("/")) {
    cleanPath = cleanPath.slice(0, -1);
  }
  return { path: cleanPath, query: merged };
}

function decodeSegment(seg: string): string {
  try {
    return decodeURIComponent(seg);
  } catch {
    return seg;
  }
}

export function createSportsHandler(
  options: SportsHandlerOptions = {},
): (req: HostedRequest) => HostedResponse {
  const provider: SportsProvider = options.provider || new EspnSportsProvider();
  const getEther = options.getEther || defaultGetEther;
  const now = options.now || (() => Date.now());

  return function handleRequest(req: HostedRequest): HostedResponse {
    const method = (req.method || "GET").toUpperCase();
    const { path, query } = parsePathAndQuery(req);

    try {
      const host = getEther();
      const rawSettings = host.settings?.get ? host.settings.get() : {};
      const { leagues: enabledLeagues, region } = parseSettings(rawSettings);
      const rt: ProviderRuntime = {
        http: host.http,
        cache: new KvCache(host.kv, now),
        now,
        log: (level, message, fields) => {
          if (typeof host.log === "function") {
            const detail = fields ? `${message} ${JSON.stringify(fields)}` : message;
            host.log(level, detail);
          }
        },
      };

      // Health endpoint
      if (path === "/ether/health" && method === "GET") {
        return jsonResponse(200, {
          status: "ok",
          provider: provider.id,
          feed: "sports",
          leagues: enabledLeagues.map((l) => l.id),
          region,
        });
      }

      // Configure / settings validation endpoint
      if (path === "/ether/configure" && method === "POST") {
        const check = validateConfigureBody(req.body);
        if (check.errors) {
          return errorResponse(400, "invalid_settings", "Invalid plugin settings", {
            fields: check.errors,
          });
        }
        return jsonResponse(200, { ok: true, settings: check.settings });
      }

      // Live connectivity test endpoint
      if (path === "/ether/test" && method === "POST") {
        const probeLeague = enabledLeagues[0];
        const result = provider.healthCheck(probeLeague, rt);
        if (!result.ok) {
          return errorResponse(502, "upstream_error", result.detail);
        }
        return jsonResponse(200, { ok: true, message: result.detail });
      }

      // GET /ether/v1/live/sports/events?from=&to=&league=&ids=
      if (path === `${FEED_PREFIX}/events` && method === "GET") {
        const leagueParams = queryList(query, "league");
        const resolved = resolveRequestedLeagues(leagueParams, enabledLeagues);
        if (resolved.unknown) {
          return errorResponse(
            400,
            "unknown_league",
            `Unknown league: ${resolved.unknown.join(", ")}`,
          );
        }
        const ids = queryList(query, "ids").map((raw) => {
          const colon = raw.lastIndexOf(":");
          return colon >= 0 ? raw.slice(colon + 1) : raw;
        });
        const from = queryParam(query, "from");
        const to = queryParam(query, "to");

        const result: EventsResponse = provider.getEvents(
          {
            leagues: resolved.leagues || enabledLeagues,
            from,
            to,
            ids: ids.length > 0 ? ids : undefined,
            region,
          },
          rt,
        );
        return jsonResponse(200, result);
      }

      // GET /ether/v1/live/sports/events/{id}
      if (path.startsWith(`${FEED_PREFIX}/events/`) && method === "GET") {
        const rawSeg = decodeSegment(path.slice(`${FEED_PREFIX}/events/`.length));
        if (!rawSeg || rawSeg.includes("/")) {
          return errorResponse(404, "not_found", "Event not found");
        }
        const colon = rawSeg.lastIndexOf(":");
        const localId = colon >= 0 ? rawSeg.slice(colon + 1) : rawSeg;
        const parsed = parseEventId(localId);
        if (!parsed) {
          return errorResponse(404, "not_found", "Event not found");
        }
        const detail = provider.getEventDetail(
          parsed.league,
          parsed.rawEventId,
          region,
          rt,
        );
        if (!detail) {
          return errorResponse(404, "not_found", "Event not found");
        }
        return jsonResponse(200, detail);
      }

      // GET /ether/v1/live/sports/entities?kind=team&league=&q=&ids=
      if (path === `${FEED_PREFIX}/entities` && method === "GET") {
        const kind = queryParam(query, "kind") || "team";
        const leagueParams = queryList(query, "league");
        const resolved = resolveRequestedLeagues(leagueParams, enabledLeagues);
        if (resolved.unknown) {
          return errorResponse(
            400,
            "unknown_league",
            `Unknown league: ${resolved.unknown.join(", ")}`,
          );
        }
        const ids = queryList(query, "ids").map((raw) => {
          const colon = raw.lastIndexOf(":");
          return colon >= 0 ? raw.slice(colon + 1) : raw;
        });
        let targetLeagues = resolved.leagues || enabledLeagues;
        if (ids.length > 0 && leagueParams.length === 0) {
          const byId = new Map<string, LeagueConfig>();
          for (const id of ids) {
            const parsed = parseEventId(id);
            if (parsed) byId.set(parsed.league.id, parsed.league);
          }
          if (byId.size > 0) {
            targetLeagues = [...byId.values()];
          }
        }
        const q = queryParam(query, "q");
        const entities = provider.getEntities(
          {
            leagues: targetLeagues,
            kind,
            q,
            ids: ids.length > 0 ? ids : undefined,
          },
          rt,
        );
        const payload: EntitiesResponse = { entities };
        return jsonResponse(200, payload);
      }

      // GET /ether/v1/live/sports/standings?league=
      if (path === `${FEED_PREFIX}/standings` && method === "GET") {
        const leagueParams = queryList(query, "league");
        let targetLeagues: LeagueConfig[];
        if (leagueParams.length > 0) {
          const resolved = resolveRequestedLeagues(leagueParams, enabledLeagues);
          if (resolved.unknown) {
            return errorResponse(
              400,
              "unknown_league",
              `Unknown league: ${resolved.unknown.join(", ")}`,
            );
          }
          targetLeagues = resolved.leagues || [];
        } else {
          const first = enabledLeagues.find((l) => l.supportsStandings);
          targetLeagues = first ? [first] : [];
        }
        const tables = provider.getStandings({ leagues: targetLeagues }, rt);
        const payload: StandingsResponse = { tables };
        return jsonResponse(200, payload);
      }

      return errorResponse(404, "not_found", `Route not found: ${method} ${path}`);
    } catch (err) {
      throwIfFatalHostError(err);
      const message = err instanceof Error ? err.message : String(err);
      return errorResponse(500, "internal_error", message);
    }
  };
}

const defaultHandle = createSportsHandler();

export function handle(req: HostedRequest): HostedResponse {
  return defaultHandle(req);
}

(globalThis as unknown as { handle?: typeof handle }).handle = handle;
