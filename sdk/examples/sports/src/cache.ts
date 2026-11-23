/**
 * KV-backed TTL cache for ESPN responses (spec section C):
 * - Scoreboards: 15 s while any event is live (in/delayed), 5 min otherwise
 * - Event details: 15 s while live, 5 min otherwise
 * - Standings: 15 min
 * - Teams/entities: 24 hours
 */

import type { HostedEtherGlobal, LiveEvent } from "./contract.ts";

export const TTL_LIVE_MS = 15_000;
export const TTL_IDLE_MS = 300_000;
export const TTL_STANDINGS_MS = 900_000;
export const TTL_TEAMS_MS = 86_400_000;

interface CacheEnvelope<T> {
  storedAt: number;
  ttlMs: number;
  value: T;
}

export interface CacheRead<T> {
  fresh: boolean;
  value: T;
  storedAt: number;
  ttlMs: number;
}

export class KvCache {
  private readonly kv: HostedEtherGlobal["kv"] | undefined;
  private readonly now: () => number;

  constructor(kv: HostedEtherGlobal["kv"] | undefined, now: () => number) {
    this.kv = kv;
    this.now = now;
  }

  read<T>(key: string): CacheRead<T> | null {
    if (!this.kv) return null;
    try {
      const raw = this.kv.get(key);
      if (!raw) return null;
      const parsed = JSON.parse(raw) as Partial<CacheEnvelope<T>>;
      if (
        typeof parsed.storedAt !== "number" ||
        typeof parsed.ttlMs !== "number" ||
        !("value" in parsed)
      ) {
        return null;
      }
      const age = this.now() - parsed.storedAt;
      const fresh = age >= 0 && age < parsed.ttlMs;
      return {
        fresh,
        value: parsed.value as T,
        storedAt: parsed.storedAt,
        ttlMs: parsed.ttlMs,
      };
    } catch {
      return null;
    }
  }

  write<T>(key: string, value: T, ttlMs: number): void {
    if (!this.kv || ttlMs <= 0) return;
    try {
      const env: CacheEnvelope<T> = {
        storedAt: this.now(),
        ttlMs,
        value,
      };
      this.kv.put(key, JSON.stringify(env));
    } catch {
      // KV write failure must never fail a read path.
    }
  }
}

export function isLiveState(state: string): boolean {
  return state === "in" || state === "delayed";
}

/**
 * Computes scoreboard cache TTL:
 * - 15 s while any event is live (in/delayed)
 * - 5 min otherwise (capped gently if an upcoming game starts within 5 min
 *   so we don't serve stale pre-game state past kickoff).
 */
export function computeScoreboardTtlMs(
  events: LiveEvent[],
  nowMs: number,
): number {
  let soonestPreDelta = Infinity;
  for (const ev of events) {
    if (isLiveState(ev.status.state)) {
      return TTL_LIVE_MS;
    }
    if (ev.status.state === "pre") {
      const startMs = Date.parse(ev.start);
      if (!Number.isNaN(startMs)) {
        const delta = startMs - nowMs;
        if (delta > 0 && delta < soonestPreDelta) {
          soonestPreDelta = delta;
        }
      }
    }
  }
  if (soonestPreDelta < TTL_IDLE_MS) {
    return Math.max(TTL_LIVE_MS, soonestPreDelta);
  }
  return TTL_IDLE_MS;
}

export function computeNextPollSec(events: LiveEvent[], nowMs: number): number {
  const ttlMs = computeScoreboardTtlMs(events, nowMs);
  return Math.max(15, Math.round(ttlMs / 1000));
}
