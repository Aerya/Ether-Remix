/**
 * Broadcast normalization (with region preference) and Live TV matchHints
 * derivation (spec sections A2, A5, and C).
 */

import type {
  Broadcast,
  Competitor,
  MatchHints,
  RankingRow,
} from "./contract.ts";

export const VALID_REGIONS = new Set(["us", "ca", "gb", "eu", "au", "all"]);

export function normalizeRegion(raw: unknown): string {
  if (typeof raw !== "string") return "us";
  const r = raw.trim().toLowerCase();
  if (VALID_REGIONS.has(r)) return r;
  return "us";
}

export interface RawEspnGeoBroadcast {
  type?: { shortName?: string; id?: string };
  market?: { type?: string };
  media?: { shortName?: string; name?: string; callLetters?: string };
  region?: string;
}

export interface RawEspnBroadcast {
  market?: string;
  names?: string[];
  type?: { shortName?: string };
  media?: { shortName?: string; name?: string; callLetters?: string };
  region?: string;
}

const STREAM_NAME_RE =
  /espn\+|peacock|paramount\+|prime video|amazon prime|apple tv|dazn|fubo|fandango|netflix|hulu|max\b|nfl\+|nba league pass|mls season pass|mlb\.tv|vict\+|stan sport|discovery\+|viaplay|youtube|streaming/i;

const RADIO_NAME_RE = /radio|sirius|xm\b|westwood one|talksport/i;

function classifyBroadcastKind(
  name: string,
  typeShortName?: string,
): "tv" | "stream" | "radio" {
  const t = (typeShortName || "").toLowerCase();
  if (t.includes("radio") || RADIO_NAME_RE.test(name)) return "radio";
  if (
    t.includes("stream") ||
    t.includes("web") ||
    t.includes("subscription") ||
    t.includes("ott") ||
    t.includes("app") ||
    STREAM_NAME_RE.test(name)
  ) {
    return "stream";
  }
  return "tv";
}

const CALLSIGN_EXPANSIONS: Record<string, string[]> = {
  "usa": ["USA Network", "USA"],
  "usa net": ["USA Network", "USA"],
  "usa network": ["USA Network", "USA"],
  "golf chnl": ["Golf Channel"],
  "golf channel": ["Golf Channel"],
  "tennis chnl": ["Tennis Channel"],
  "tennis channel": ["Tennis Channel"],
  "nhl net": ["NHL Network"],
  "nfl net": ["NFL Network"],
  "mlb net": ["MLB Network"],
  "fox": ["FOX"],
  "fox dep": ["FOX Deportes"],
  "cbs": ["CBS"],
  "nbc": ["NBC"],
  "nbc sports": ["NBC Sports"],
  "abc": ["ABC"],
  "tnt": ["TNT"],
  "tbs": ["TBS"],
  "fs1": ["FS1", "FOX Sports 1"],
  "fox sports 1": ["FOX Sports 1", "FS1"],
  "fs2": ["FS2", "FOX Sports 2"],
  "fox sports 2": ["FOX Sports 2", "FS2"],
  "cbssn": ["CBS Sports Network"],
  "espn": ["ESPN"],
  "espn2": ["ESPN2"],
  "espnu": ["ESPNU"],
  "espn+": ["ESPN+"],
  "espn dep": ["ESPN Deportes"],
  "espn deportes": ["ESPN Deportes"],
  "tru": ["truTV"],
  "trutv": ["truTV"],
  "nbatv": ["NBA TV"],
  "nba tv": ["NBA TV"],
  "secn": ["SEC Network"],
  "sec network": ["SEC Network"],
  "accn": ["ACC Network"],
  "acc network": ["ACC Network"],
  "btn": ["Big Ten Network"],
  "big ten network": ["Big Ten Network"],
  "cw": ["The CW", "CW"],
  "the cw": ["The CW", "CW"],
  "tele": ["Telemundo"],
  "telemundo": ["Telemundo"],
  "uni": ["Univision"],
  "univ": ["Univision"],
  "universo": ["Universo"],
  "fdsn": ["FanDuel Sports Network"],
  "fanduel": ["FanDuel Sports Network"],
  "bein": ["beIN Sports"],
  "bein sports": ["beIN Sports"],
  "dazn": ["DAZN", "DAZN 1"],
  "tsn": ["TSN", "TSN 1"],
  "sn": ["Sportsnet", "Sportsnet One"],
  "sportsnet": ["Sportsnet", "Sportsnet One"],
  "sky": ["Sky Sports", "Sky Sports Main Event"],
  "sky sports": ["Sky Sports", "Sky Sports Main Event"],
  "sky sports main event": ["Sky Sports Main Event"],
  "sky sports pl": ["Sky Sports Premier League"],
  "sky sports premier league": ["Sky Sports Premier League"],
  "sky sports f1": ["Sky Sports F1"],
  "sky sports golf": ["Sky Sports Golf"],
  "sky sports football": ["Sky Sports Football"],
  "tnt sports": ["TNT Sports 1", "TNT Sports 2"],
  "tnt sports 1": ["TNT Sports 1"],
  "tnt sports 2": ["TNT Sports 2"],
  "eurosport": ["Eurosport 1"],
  "prime video": ["Prime Video", "Amazon Prime"],
  "peacock": ["Peacock"],
  "apple tv": ["Apple TV", "Apple TV+"],
};

const EU_REGIONS = new Set([
  "eu",
  "de",
  "fr",
  "es",
  "it",
  "nl",
  "be",
  "pt",
  "se",
  "no",
  "dk",
  "pl",
  "at",
  "ch",
  "ie",
]);

function matchesRegion(broadcastRegion: string | undefined, target: string): boolean {
  if (target === "all") return true;
  if (!broadcastRegion) return true;
  const br = broadcastRegion.toLowerCase();
  if (br === target) return true;
  if (target === "gb" && (br === "uk" || br === "gb")) return true;
  if (target === "eu" && EU_REGIONS.has(br)) return true;
  return false;
}

export function extractBroadcasts(
  geoBroadcasts: RawEspnGeoBroadcast[] | undefined,
  broadcasts: RawEspnBroadcast[] | undefined,
  regionSetting: string,
): Broadcast[] {
  const all: Broadcast[] = [];
  const seen = new Set<string>();

  const push = (name: string, kind: "tv" | "stream" | "radio", region?: string) => {
    // Show the network's full name ("Golf Channel", not ESPN's "Golf Chnl").
    const clean = (CALLSIGN_EXPANSIONS[name.trim().toLowerCase()]?.[0] || name).trim();
    if (!clean) return;
    const normRegion = region ? region.trim().toLowerCase() : undefined;
    const key = `${clean.toLowerCase()}|${kind}|${normRegion || ""}`;
    if (seen.has(key)) return;
    seen.add(key);
    const b: Broadcast = { name: clean, kind };
    if (normRegion) b.region = normRegion;
    all.push(b);
  };

  if (Array.isArray(geoBroadcasts)) {
    for (const g of geoBroadcasts) {
      const name = g?.media?.shortName || g?.media?.name || "";
      if (!name) continue;
      push(name, classifyBroadcastKind(name, g?.type?.shortName), g?.region);
    }
  }

  if (Array.isArray(broadcasts)) {
    for (const b of broadcasts) {
      if (b?.media?.shortName || b?.media?.name || b?.media?.callLetters) {
        const name = b.media.shortName || b.media.name || b.media.callLetters || "";
        push(name, classifyBroadcastKind(name, b.type?.shortName), b.region);
      }
      if (Array.isArray(b?.names)) {
        for (const rawName of b.names) {
          if (typeof rawName !== "string" || !rawName.trim()) continue;
          // Skip adding a regionless duplicate if geoBroadcasts already covered this network.
          const lower = rawName.trim().toLowerCase();
          if (all.some((existing) => existing.name.toLowerCase() === lower)) {
            continue;
          }
          push(rawName, classifyBroadcastKind(rawName), b.region);
        }
      }
    }
  }

  const target = normalizeRegion(regionSetting);
  if (target === "all" || all.length === 0) return all;

  const preferred = all.filter((b) => matchesRegion(b.region, target));
  return preferred.length > 0 ? preferred : all;
}

export interface CompetitorHintInput {
  displayName?: string;
  shortDisplayName?: string;
  name?: string;
  location?: string;
  abbreviation?: string;
}

export function buildMatchHints(
  competitors: Competitor[] | undefined,
  rawTeams: CompetitorHintInput[] | undefined,
  ranking: RankingRow[] | undefined,
  broadcasts: Broadcast[] | undefined,
  extraTerms?: string[],
): MatchHints {
  const terms: string[] = [];
  const termSeen = new Set<string>();
  const addTerm = (value?: string) => {
    if (!value) return;
    let clean = value.trim().replace(/\s+/g, " ");
    if (!clean) return;
    // Collapse accidental doubled phrases like "Real Sociedad Real Sociedad".
    const words = clean.split(" ");
    if (words.length >= 2 && words.length % 2 === 0) {
      const half = words.length / 2;
      const left = words.slice(0, half).join(" ");
      const right = words.slice(half).join(" ");
      if (left.toLowerCase() === right.toLowerCase()) {
        clean = left;
      }
    }
    const key = clean.toLowerCase();
    if (termSeen.has(key)) return;
    termSeen.add(key);
    terms.push(clean);
  };

  if (Array.isArray(rawTeams)) {
    for (const t of rawTeams) {
      addTerm(t.displayName);
      addTerm(t.shortDisplayName);
      addTerm(t.name);
      const loc = t.location?.trim();
      const name = t.name?.trim();
      if (loc && name) {
        const locLow = loc.toLowerCase();
        const nameLow = name.toLowerCase();
        if (
          locLow !== nameLow &&
          !nameLow.includes(locLow) &&
          !locLow.includes(nameLow)
        ) {
          addTerm(`${loc} ${name}`);
        }
      }
      addTerm(loc);
      addTerm(t.abbreviation);
    }
  }

  if (Array.isArray(competitors)) {
    for (const c of competitors) {
      addTerm(c.name);
      addTerm(c.short);
      addTerm(c.abbr);
    }
  }

  if (Array.isArray(ranking)) {
    for (const row of ranking.slice(0, 3)) {
      addTerm(row.name);
      addTerm(row.abbr);
    }
  }

  if (Array.isArray(extraTerms)) {
    for (const extra of extraTerms) {
      addTerm(extra);
    }
  }

  const callsigns: string[] = [];
  const callsignSeen = new Set<string>();
  const addCallsign = (value?: string) => {
    if (!value) return;
    const clean = value.trim();
    if (!clean) return;
    const key = clean.toLowerCase();
    if (callsignSeen.has(key)) return;
    callsignSeen.add(key);
    callsigns.push(clean);
  };

  if (Array.isArray(broadcasts)) {
    for (const b of broadcasts) {
      addCallsign(b.name);
      const expanded = CALLSIGN_EXPANSIONS[b.name.toLowerCase()];
      if (expanded) {
        for (const alias of expanded) {
          addCallsign(alias);
        }
      }
    }
  }

  return { terms, callsigns };
}
