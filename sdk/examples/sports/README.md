# Sports (`org.ether.sports`)

A first-party hosted TypeScript plugin for Ether that brings live scores, standings, schedules, win probability, play-by-play, and team favourites across 17 leagues using ESPN's public JSON endpoints behind a swappable `SportsProvider` interface.

## What it contributes

- **Live feed (`sports`):** `live_events` capability with a 15 s minimum interval and `team` entity kind. The plugin never polls on its own; Ether calls it on demand while a client is subscribed or an alert window is open.
- **Sports screen (`sports`):** `leagues`, `hero`, `Live now`, `Your teams`, `Today`, `Schedule`, `Standings`, and `Teams` sections matching Ether's generic screen contract and binding Vela wire shapes.
- **Live TV matching:** `broadcasts` (normalized TV / stream / radio with region preference) and `matchHints` (team names, short names, nicknames, locations, abbreviations, and expanded broadcast callsigns) for Ether's "Where to watch" and score-bug overlay matcher.
- **Close-game alerts (`status.close`):**
  - Football (NFL, NCAAF): within 8 points in Q4 under 5 min (or OT within 8).
  - Basketball (NBA, WNBA, NCAAM): within 5 points in the last 3 min of regulation (Q4 for 4-quarter leagues, 2nd half for NCAAM) or OT.
  - Hockey (NHL): tied or within 1 goal in the 3rd period (or OT/SO).
  - Soccer (MLS, EPL, La Liga, Bundesliga, Serie A, Ligue 1, UCL): level at or after 80' (or level in extra time).
  - Baseball (MLB): within 2 runs in the 9th inning or extra innings.

## Default leagues

Enabled by default and selectable in plugin settings (`leagues`):

NFL, NBA, NHL, MLB, MLS, Premier League (`epl`), La Liga (`laliga`), Bundesliga (`bundesliga`), Serie A (`seriea`), Ligue 1 (`ligue1`), Champions League (`ucl`), NCAAF top 25 (`ncaaf`), NCAAM top 25 (`ncaam`), WNBA, Formula 1 (`f1`), UFC, and PGA Tour (`pga`).

## Settings and permissions

- **Settings (`settings.schema.json`):**
  - `leagues` (`multiSelect`): leagues included on the Sports screen and live score feed.
  - `region` (`select`): preferred broadcast region (`us`, `ca`, `gb`, `eu`, `au`, or `all`). When an event has no broadcasts for the chosen region, all listed broadcasts are returned as fallback so Live TV matching keeps working.
- **Network permissions:** `site.api.espn.com` and `site.web.api.espn.com` only.

## Plugin endpoints

- `GET /ether/v1/live/sports/events?from=&to=&league=&ids=`
- `GET /ether/v1/live/sports/events/{id}`
- `GET /ether/v1/live/sports/entities?kind=team&league=&q=`
- `GET /ether/v1/live/sports/standings?league=`
- `GET /ether/health`
- `POST /ether/configure`
- `POST /ether/test`

## Architecture and caching

- **Provider interface (`src/providers/types.ts`):** `EspnSportsProvider` (`src/providers/espn.ts`) implements `SportsProvider`. Another provider can be passed to `createSportsHandler({ provider })` without changing routing, settings validation, close-game heuristics, or caching helpers.
- **Concurrency & caching:** outbound requests use `ether.http.fetchMany` capped at 6 concurrent requests per host (chunked at 64 per call). KV cache TTLs are 15 s for scoreboards/details with live (`in`/`delayed`) events, 5 min for non-live scoreboards/details, 15 min for standings, and 24 h for teams/entities. Transient upstream errors fall back to stale cached values when available.
- **Self-contained hosted sources:** all imports stay relative inside `sdk/examples/sports/src/` so Ether Studio can import and edit `src/plugin.ts` directly without external package imports. `src/contract.ts` mirrors the v0.3.19 `live_events` and `screens` contract types locally until they are exported from `@ether/plugin-sdk`.

## Build, check, and test

From the repository root:

```sh
# Offline fixture tests (zero network)
node --test sdk/examples/sports/tests/plugin.test.ts

# Real esbuild VFS compile + QuickJS runtime + icon/copy check
go test ./sdk/examples/sports

# Pack and check once live_events & screens manifest validation is merged
go run ./cmd/ether plugin check sdk/examples/sports
go run ./cmd/ether plugin pack -o ~/.scratch/sports.etherpkg sdk/examples/sports
```

## Ether Studio recipe

Follow this recipe to open, inspect, customize, and install `org.ether.sports` inside **Ether Studio** (`Server → Plugins → Develop`):

1. **Import or scaffold the draft:**
   - Pack and install `sdk/examples/sports`, then click **Edit in Studio** on `Sports` (all imports stay inside `src/`, so Studio keeps `src/plugin.ts` and sibling modules editable).
   - Or start a hosted TypeScript draft in Studio and copy `manifest.json`, `settings.schema.json`, `assets/icon.svg`, `i18n/en.json`, `i18n/fr.json`, and `src/**`.
   - Confirm `ether.permissions.network` lists only `["site.api.espn.com", "site.web.api.espn.com"]`.
2. **Exercise routes in the Inspector** (enable live network in the sandbox permissions drawer to hit ESPN):
   - `GET /ether/health` → `{ status: "ok", provider: "espn", feed: "sports", leagues: [...], region: "us" }`.
   - `POST /ether/test` → probes the first enabled league on ESPN and returns `{ ok: true, message }`.
   - `POST /ether/configure` with `{"leagues":["nfl","nba","epl","f1"],"region":"ca"}` → `200`; `{"leagues":[]}` → `400` with `fields["/leagues"]`.
   - `GET /ether/v1/live/sports/events?league=nfl` → `{ events, nextPollSec }` with normalized RFC3339 timestamps, `broadcasts`, and `matchHints`.
   - `GET /ether/v1/live/sports/events/{id}` (for example `nfl-401873066`) → `LiveEventDetail` with `tables`, `plays`, and `probabilitySeries` (`t` as `0..1` game fraction).
   - `GET /ether/v1/live/sports/entities?kind=team&league=nfl&q=eagles` → `{ entities: [{ id: "nfl-21", kind: "team", ... }] }`.
   - `GET /ether/v1/live/sports/standings?league=nba` → sorted conference tables (`#` ascending from `1`).
3. **Customize in Studio:**
   - **Default leagues:** edit `settings.schema.json` `leagues.default` to pre-select a smaller set (for example `["nfl", "nhl", "epl", "f1"]`).
   - **Close-game thresholds:** edit `src/close.ts` (`isCloseGame`) to tune margins or clock thresholds.
   - **Swap data provider:** implement `SportsProvider` (`src/providers/types.ts`), add its host to `ether.permissions.network`, and pass `{ provider }` into `createSportsHandler` in `src/plugin.ts`.
4. **Install and publish:** click **Install** in Studio, review the two ESPN network grants and 64 MiB hosted memory cap, select leagues and region in the plugin drawer, run **Test connection**, and publish the plugin to Vela so the **Sports** screen appears in navigation.

## Limits and ESPN API notes

- Uses ESPN's public, undocumented JSON endpoints (`site.api.espn.com` and `site.web.api.espn.com`), which may change without notice.
- ESPN's team-sport scoreboard endpoints reject `YYYYMMDD-YYYYMMDD` date ranges with HTTP 400; multi-day windows fan out bounded single-day requests (up to 7 days) through `http.fetchMany`, while multi-day weekend events (`f1`, `pga`, `ufc`) use range queries.
- ESPN returns HTTP 404 for `/summary` on `f1`, `pga`, and `ufc`; event detail falls back to scoreboard data to build race results/weekend sessions, golf leaderboards, and fight cards.
- NBA and MLB standings returned by ESPN are not pre-sorted by rank; the provider sorts entries by `playoffSeed`/`rank` ascending before rendering tables.
- Test fixtures in `tests/fixtures/` are trimmed recordings of real ESPN responses captured on 2026-11-22.
