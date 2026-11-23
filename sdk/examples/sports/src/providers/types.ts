/**
 * Provider interface so alternative sports data providers can slot in
 * without changing plugin routing, caching, close-game heuristics, or
 * settings handling (spec owner decision #1 & section C).
 */

import type {
  GenericTable,
  HostedEtherGlobal,
  HostFetchBatchResult,
  HostFetchRequest,
  HostFetchResponse,
  LiveEntity,
  LiveEvent,
  LiveEventDetail,
} from "../contract.ts";
import type { KvCache } from "../cache.ts";
import type { LeagueConfig } from "../leagues.ts";

export const MAX_FETCH_CONCURRENCY_PER_HOST = 6;
export const MAX_FETCH_MANY_BATCH = 64;

const FATAL_HOST_ERROR_CODES = new Set([
  "check_dependency",
  "forbidden",
  "governance",
  "aborted",
]);

export function isFatalHostError(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const code = (err as { code?: unknown }).code;
  return typeof code === "string" && FATAL_HOST_ERROR_CODES.has(code);
}

export function throwIfFatalHostError(err: unknown): void {
  if (isFatalHostError(err)) {
    throw err;
  }
}

export interface ProviderRuntime {
  http: HostedEtherGlobal["http"];
  cache: KvCache;
  now: () => number;
  log: (level: string, message: string, fields?: Record<string, unknown>) => void;
}

export interface ScoreboardQuery {
  leagues: LeagueConfig[];
  from?: string;
  to?: string;
  ids?: string[];
  region: string;
}

export interface ScoreboardResult {
  events: LiveEvent[];
  nextPollSec: number;
}

export interface EntitiesQuery {
  leagues: LeagueConfig[];
  kind: string;
  q?: string;
  ids?: string[];
}

export interface StandingsQuery {
  leagues: LeagueConfig[];
}

export interface SportsProvider {
  readonly id: string;
  readonly name: string;
  getEvents(query: ScoreboardQuery, rt: ProviderRuntime): ScoreboardResult;
  getEventDetail(
    league: LeagueConfig,
    rawEventId: string,
    region: string,
    rt: ProviderRuntime,
  ): LiveEventDetail | null;
  getEntities(query: EntitiesQuery, rt: ProviderRuntime): LiveEntity[];
  getStandings(query: StandingsQuery, rt: ProviderRuntime): GenericTable[];
  healthCheck(league: LeagueConfig, rt: ProviderRuntime): { ok: boolean; detail: string };
}

/**
 * Issues outbound HTTP requests via `ether.http.fetchMany` with concurrency
 * bounded to at most 6 per host (and batch size <= 64, Ether's per-call cap).
 * Falls back to sequential `http.fetch` if `fetchMany` is unavailable.
 */
export function fetchManyBounded(
  http: HostedEtherGlobal["http"],
  reqs: HostFetchRequest[],
): HostFetchBatchResult[] {
  if (reqs.length === 0) return [];
  const results: HostFetchBatchResult[] = [];

  if (typeof http.fetchMany === "function") {
    for (let i = 0; i < reqs.length; i += MAX_FETCH_MANY_BATCH) {
      const slice = reqs.slice(i, i + MAX_FETCH_MANY_BATCH);
      const batch = http.fetchMany(slice, {
        concurrency: MAX_FETCH_CONCURRENCY_PER_HOST,
      });
      for (const item of batch) {
        if ("error" in item && item.error) {
          throwIfFatalHostError(item.error);
        }
        results.push(item);
      }
    }
    return results;
  }

  for (const req of reqs) {
    try {
      const resp: HostFetchResponse = http.fetch(req);
      results.push(resp);
    } catch (err) {
      throwIfFatalHostError(err);
      results.push({
        error: {
          code: "upstream_error",
          message: err instanceof Error ? err.message : String(err),
        },
      });
    }
  }
  return results;
}
