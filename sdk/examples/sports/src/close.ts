/**
 * Close-game heuristics (spec section C):
 * - Football (NFL, NCAAF): within 8 points in Q4 under 5 min (or OT within 8)
 * - Basketball (NBA, WNBA, NCAAM): within 5 points in the last 3 min of
 *   regulation (Q4 for 4-quarter leagues, 2nd half for 2-half NCAAM) or OT
 * - Hockey (NHL): tied or within 1 goal in the 3rd period (or OT/SO)
 * - Soccer (MLS, EPL, La Liga, Bundesliga, Serie A, Ligue 1, UCL):
 *   level (tied) at or after 80' (or level in extra time)
 * - Baseball (MLB): within 2 runs in the 9th inning or extra innings
 */

import type { Competitor, LiveEventState } from "./contract.ts";
import type { SportCategory } from "./leagues.ts";

export interface CloseGameInput {
  category: SportCategory;
  state: LiveEventState;
  period?: number;
  regulationPeriods: number;
  clock?: string;
  numericClock?: number;
  competitors?: Competitor[];
}

/**
 * Parses a countdown display clock into remaining seconds.
 * Handles "4:59", "0:42.8", and under-1-minute basketball clocks like "42.8".
 * Returns null for non-clock strings such as "HT", "End", or empty.
 */
export function parseRemainingClockSec(
  displayClock?: string,
  numericClock?: number,
): number | null {
  if (displayClock) {
    const trimmed = displayClock.trim();
    const mmss = /^(\d+):(\d{1,2}(?:\.\d+)?)$/.exec(trimmed);
    if (mmss) {
      const mins = Number(mmss[1]);
      const secs = Number(mmss[2]);
      if (Number.isFinite(mins) && Number.isFinite(secs)) {
        return mins * 60 + secs;
      }
    }
    const plainSec = /^(\d+(?:\.\d+)?)$/.exec(trimmed);
    if (plainSec) {
      const secs = Number(plainSec[1]);
      if (Number.isFinite(secs)) {
        return secs;
      }
    }
  }
  if (typeof numericClock === "number" && Number.isFinite(numericClock) && numericClock >= 0) {
    return numericClock;
  }
  return null;
}

/**
 * Parses elapsed soccer minutes from ESPN's clock ("82'", "90'+3'", "105'")
 * or numeric elapsed seconds.
 */
export function parseElapsedSoccerMinutes(
  displayClock?: string,
  numericClock?: number,
): number | null {
  if (displayClock) {
    const trimmed = displayClock.trim();
    const stoppage = /^(\d+)'\s*\+\s*(\d+)'?$/.exec(trimmed);
    if (stoppage) {
      return Number(stoppage[1]) + Number(stoppage[2]);
    }
    const simple = /^(\d+)'$/.exec(trimmed);
    if (simple) {
      return Number(simple[1]);
    }
  }
  if (typeof numericClock === "number" && Number.isFinite(numericClock) && numericClock > 0) {
    return Math.floor(numericClock / 60);
  }
  return null;
}

function twoTeamScoreDiff(competitors?: Competitor[]): number | null {
  if (!competitors || competitors.length !== 2) return null;
  const s0 = Number(competitors[0].score);
  const s1 = Number(competitors[1].score);
  if (!Number.isFinite(s0) || !Number.isFinite(s1)) return null;
  return Math.abs(s0 - s1);
}

export function isCloseGame(input: CloseGameInput): boolean {
  if (input.state !== "in") return false;
  const diff = twoTeamScoreDiff(input.competitors);
  if (diff === null) return false;
  const period = input.period ?? 0;

  switch (input.category) {
    case "football": {
      if (diff > 8) return false;
      if (period > input.regulationPeriods) return true; // OT
      if (period === input.regulationPeriods) {
        const rem = parseRemainingClockSec(input.clock, input.numericClock);
        return rem !== null && rem < 300;
      }
      return false;
    }

    case "basketball": {
      if (diff > 5) return false;
      if (period > input.regulationPeriods) return true; // OT
      if (period === input.regulationPeriods) {
        const rem = parseRemainingClockSec(input.clock, input.numericClock);
        return rem !== null && rem <= 180;
      }
      return false;
    }

    case "hockey": {
      return period >= input.regulationPeriods && diff <= 1;
    }

    case "soccer": {
      if (diff !== 0) return false;
      if (period >= 3) return true; // Extra time / penalties level
      const mins = parseElapsedSoccerMinutes(input.clock, input.numericClock);
      return mins !== null && mins >= 80;
    }

    case "baseball": {
      return period >= input.regulationPeriods && diff <= 2;
    }

    default:
      return false;
  }
}
