/**
 * Default league catalog (17 leagues per spec section C) and safe
 * hyphenated ID helpers (no colons or slashes so Ether's colon-delimited
 * pluginID:feedID:id namespace stays unambiguous).
 */

export type SportCategory =
  | "football"
  | "basketball"
  | "hockey"
  | "baseball"
  | "soccer"
  | "motorsport"
  | "mma"
  | "golf";

export type EventShapeMode = "team" | "ranking" | "card";
export type TeamsSource = "teams" | "f1_constructors" | "none";

export interface LeagueConfig {
  id: string;
  name: string;
  abbr: string;
  category: SportCategory;
  espnSport: string;
  espnLeague: string;
  mode: EventShapeMode;
  multiDayEvent: boolean;
  supportsSummary: boolean;
  teamsSource: TeamsSource;
  supportsStandings: boolean;
  regulationPeriods: number;
  defaultLogo: string;
  aliases: string[];
}

export const DEFAULT_LEAGUES: readonly LeagueConfig[] = [
  {
    id: "nfl",
    name: "NFL",
    abbr: "NFL",
    category: "football",
    espnSport: "football",
    espnLeague: "nfl",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 4,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/nfl.png",
    aliases: ["nfl"],
  },
  {
    id: "nba",
    name: "NBA",
    abbr: "NBA",
    category: "basketball",
    espnSport: "basketball",
    espnLeague: "nba",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 4,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/nba.png",
    aliases: ["nba"],
  },
  {
    id: "nhl",
    name: "NHL",
    abbr: "NHL",
    category: "hockey",
    espnSport: "hockey",
    espnLeague: "nhl",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 3,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/nhl.png",
    aliases: ["nhl"],
  },
  {
    id: "mlb",
    name: "MLB",
    abbr: "MLB",
    category: "baseball",
    espnSport: "baseball",
    espnLeague: "mlb",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 9,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/mlb.png",
    aliases: ["mlb"],
  },
  {
    id: "mls",
    name: "MLS",
    abbr: "MLS",
    category: "soccer",
    espnSport: "soccer",
    espnLeague: "usa.1",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/mls.png",
    aliases: ["mls", "usa.1"],
  },
  {
    id: "epl",
    name: "Premier League",
    abbr: "EPL",
    category: "soccer",
    espnSport: "soccer",
    espnLeague: "eng.1",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/leaguelogos/soccer/500/23.png",
    aliases: ["epl", "eng.1", "premier-league", "premier_league"],
  },
  {
    id: "laliga",
    name: "La Liga",
    abbr: "La Liga",
    category: "soccer",
    espnSport: "soccer",
    espnLeague: "esp.1",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/leaguelogos/soccer/500/15.png",
    aliases: ["laliga", "lal", "esp.1", "la-liga", "la_liga"],
  },
  {
    id: "bundesliga",
    name: "Bundesliga",
    abbr: "Bundesliga",
    category: "soccer",
    espnSport: "soccer",
    espnLeague: "ger.1",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/leaguelogos/soccer/500/10.png",
    aliases: ["bundesliga", "bun", "ger.1"],
  },
  {
    id: "seriea",
    name: "Serie A",
    abbr: "Serie A",
    category: "soccer",
    espnSport: "soccer",
    espnLeague: "ita.1",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/leaguelogos/soccer/500/12.png",
    aliases: ["seriea", "sa", "ita.1", "serie-a", "serie_a"],
  },
  {
    id: "ligue1",
    name: "Ligue 1",
    abbr: "Ligue 1",
    category: "soccer",
    espnSport: "soccer",
    espnLeague: "fra.1",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/leaguelogos/soccer/500/9.png",
    aliases: ["ligue1", "l1", "fra.1", "ligue-1", "ligue_1"],
  },
  {
    id: "ucl",
    name: "Champions League",
    abbr: "UCL",
    category: "soccer",
    espnSport: "soccer",
    espnLeague: "uefa.champions",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/leaguelogos/soccer/500/2.png",
    aliases: ["ucl", "uefa.champions", "champions-league", "champions_league"],
  },
  {
    id: "ncaaf",
    name: "NCAAF",
    abbr: "NCAAF",
    category: "football",
    espnSport: "football",
    espnLeague: "college-football",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 4,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/ncaa/500/ncaa.png",
    aliases: ["ncaaf", "cfb", "college-football"],
  },
  {
    id: "ncaam",
    name: "NCAAM",
    abbr: "NCAAM",
    category: "basketball",
    espnSport: "basketball",
    espnLeague: "mens-college-basketball",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 2,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/ncaa/500/ncaa.png",
    aliases: ["ncaam", "cbb", "mens-college-basketball"],
  },
  {
    id: "wnba",
    name: "WNBA",
    abbr: "WNBA",
    category: "basketball",
    espnSport: "basketball",
    espnLeague: "wnba",
    mode: "team",
    multiDayEvent: false,
    supportsSummary: true,
    teamsSource: "teams",
    supportsStandings: true,
    regulationPeriods: 4,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/wnba.png",
    aliases: ["wnba"],
  },
  {
    id: "f1",
    name: "Formula 1",
    abbr: "F1",
    category: "motorsport",
    espnSport: "racing",
    espnLeague: "f1",
    mode: "ranking",
    multiDayEvent: true,
    supportsSummary: false,
    teamsSource: "f1_constructors",
    supportsStandings: true,
    regulationPeriods: 1,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/f1.png",
    aliases: ["f1", "formula1", "formula-1"],
  },
  {
    id: "ufc",
    name: "UFC",
    abbr: "UFC",
    category: "mma",
    espnSport: "mma",
    espnLeague: "ufc",
    mode: "card",
    multiDayEvent: true,
    supportsSummary: false,
    teamsSource: "none",
    supportsStandings: false,
    regulationPeriods: 5,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/ufc.png",
    aliases: ["ufc", "mma"],
  },
  {
    id: "pga",
    name: "PGA Tour",
    abbr: "PGA",
    category: "golf",
    espnSport: "golf",
    espnLeague: "pga",
    mode: "ranking",
    multiDayEvent: true,
    supportsSummary: false,
    teamsSource: "none",
    supportsStandings: false,
    regulationPeriods: 4,
    defaultLogo: "https://a.espncdn.com/i/teamlogos/leagues/500/pga.png",
    aliases: ["pga", "golf"],
  },
];

export const DEFAULT_LEAGUE_IDS: readonly string[] = DEFAULT_LEAGUES.map(
  (l) => l.id,
);

const ALIAS_MAP = new Map<string, LeagueConfig>();
for (const league of DEFAULT_LEAGUES) {
  ALIAS_MAP.set(league.id, league);
  ALIAS_MAP.set(league.abbr.toLowerCase(), league);
  ALIAS_MAP.set(league.espnLeague.toLowerCase(), league);
  for (const alias of league.aliases) {
    ALIAS_MAP.set(alias.toLowerCase(), league);
  }
}

export function resolveLeague(idOrAlias: string): LeagueConfig | undefined {
  const key = idOrAlias.trim().toLowerCase();
  if (!key) return undefined;
  return ALIAS_MAP.get(key);
}

export function resolveEnabledLeagues(configured: unknown): LeagueConfig[] {
  if (!Array.isArray(configured)) {
    return [...DEFAULT_LEAGUES];
  }
  const seen = new Set<string>();
  const out: LeagueConfig[] = [];
  for (const entry of configured) {
    if (typeof entry !== "string") continue;
    const lg = resolveLeague(entry);
    if (lg && !seen.has(lg.id)) {
      seen.add(lg.id);
      out.push(lg);
    }
  }
  return out;
}

export function formatEventId(leagueId: string, rawEventId: string): string {
  const cleanRaw = rawEventId.replace(/[^a-zA-Z0-9_-]/g, "");
  return `${leagueId}-${cleanRaw}`;
}

export function parseEventId(
  eventId: string,
): { league: LeagueConfig; rawEventId: string } | null {
  const trimmed = eventId.trim();
  const dash = trimmed.indexOf("-");
  if (dash <= 0 || dash >= trimmed.length - 1) return null;
  const prefix = trimmed.slice(0, dash);
  const rawEventId = trimmed.slice(dash + 1);
  if (!rawEventId || /[:/]/.test(rawEventId)) return null;
  const league = resolveLeague(prefix);
  if (!league) return null;
  return { league, rawEventId };
}

export function formatEntityId(leagueId: string, rawEntityId: string): string {
  const cleanRaw = rawEntityId.replace(/[^a-zA-Z0-9_-]/g, "");
  return `${leagueId}-${cleanRaw}`;
}
