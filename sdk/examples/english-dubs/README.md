# English dubbed anime

First-party Ether plugin that exposes anime series and films with an English dub as ready-made list sources for Home rows, smart collections, and library rules.

## What it provides

- **Anime with an English dub (`dubbed`)** — TV series and films confirmed to have an English dub in the community MAL-Dubs dataset.
- **Dub in progress (`incomplete`)** — TV series and films whose English dub is still being released.
- **Status page** — Displays cached mapped series, movie, and in-progress counts with a manual cache refresh action.

## How it works

The plugin fetches both upstream datasets at runtime and does not bundle any copy of the data:

1. **Dub status list:** [MAL-Dubs](https://github.com/MAL-Dubs/MAL-Dubs) (`data/dubInfo.json`, ~5,700 MyAnimeList IDs across `dubbed` and `incomplete`).
2. **ID mapping:** [Fribb/anime-lists](https://github.com/Fribb/anime-lists) (`anime-list-mini.json`), used at runtime to map MyAnimeList IDs to TMDB IDs (with IMDb and TVDB fallbacks where a TMDB ID is absent) because Ether's list source resolver matches canonical IMDb, TMDB, and TVDB identities.

Both files are cached in plugin KV storage in compacted form and refreshed daily via a scheduled job (`/ether/job/refresh`) using conditional HTTP requests (`ETag` / `If-None-Match` and `Last-Modified` / `If-Modified-Since`). If an upstream refresh fails transiently after the cache is populated, the plugin serves the previously cached index.

## Settings

| Setting | Type | Default | Effect |
| --- | --- | --- | --- |
| Include incomplete dubs (`includeIncomplete`) | Boolean | `false` | Merges entries from the `incomplete` array into the main **Anime with an English dub** list (deduplicated by title). |
| Type filter (`typeFilter`) | Select (`both`, `series`, `movies`) | `both` | Restricts catalog responses to anime series, anime movies, or both. |

## Permissions

- **Network (`raw.githubusercontent.com`)** — Used only to fetch `MAL-Dubs/MAL-Dubs` (`data/dubInfo.json`) and `Fribb/anime-lists` (`anime-list-mini.json`).

## Credits and licenses

- Plugin source code: MIT License.
- Dub list data: [MAL-Dubs](https://github.com/MAL-Dubs/MAL-Dubs), licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](https://github.com/MAL-Dubs/MAL-Dubs/blob/main/LICENSE). No copy of the MAL-Dubs data is bundled with this plugin; it is fetched directly at runtime by each server.
- Anime ID mapping: [Fribb/anime-lists](https://github.com/Fribb/anime-lists).

## Limits

- **Community-maintained data:** Dub coverage and completeness depend on volunteer updates to MAL-Dubs; newly announced simuldubs or recent releases may lag behind broadcast dates.
- **MAL-to-TMDB mapping gaps:** Not every MyAnimeList entry (especially OVAs, specials, and newer titles) has a TMDB, TVDB, or IMDb mapping in `Fribb/anime-lists`. Unmapped entries cannot be matched to Ether's canonical catalogue and are omitted.
- **Season collapsing:** MyAnimeList assigns separate IDs per season or cour, while TMDB groups seasons under one show. Duplicate TMDB show IDs are collapsed into a single show entry, so a show appears once if at least one of its seasons is listed as dubbed.
- **Collection item limit:** Ether bounds each collection or Home row list source read to at most 2,000 items per refresh. The confirmed dubbed series list currently exceeds 2,000 TMDB shows, so full mixed or series-only collections receive the first 2,000 mapped items in source order.

## Verification

From the repository root:

```sh
CGO_ENABLED=0 go build -o ./ether ./cmd/ether
./ether plugin check sdk/examples/english-dubs
./ether plugin test sdk/examples/english-dubs
./ether plugin pack -o ./english-dubs.etherpkg sdk/examples/english-dubs
./ether plugin check ./english-dubs.etherpkg
node --test sdk/examples/english-dubs/tests/plugin.test.ts
```
