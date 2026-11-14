# PSE fork rules

“Ether-Remix” is the fork's public name. The code keeps the historical `pse` prefix (packages, `PSE_*` variables, `PSE-HOOK` markers) so neither configurations nor the upstream sync break.

Goal: merge Ether upstream every day **without breaking PSE additions**, and without automatic conflict resolution on code.

## Where code goes

| Kind | Fork-owned location |
| --- | --- |
| Go | `internal/pse/<feature>/` (one package per addition) |
| Go imported by a frontend | `internal/frontend/kit/<feature>/`: `TestFrontendImports` forbids frontends any other internal package (e.g. `scrobview`) |
| Web UI | `web/src/pse/` |
| PSE translations | `web/src/pse/i18n/` (supplementary catalogues, never in `web/src/i18n/locales/*.json`) |
| Workflows | `.github/workflows/pse-*.yml`, support files in `.github/pse/` |
| Documentation | `README.md`, `README.fr.md`, `docs/pse/` |

Ether files are only changed for **hook points**: a few lines calling PSE code (registering a route, a service, a catalogue filter…). Every hook point is marked:

```go
// PSE-HOOK: <addition name> — see docs/pse/FORK.md
```

`git grep PSE-HOOK` lists everything that can conflict with upstream. Current hooks:

| Ether file | Addition |
| --- | --- |
| `internal/notify/notify.go` (`deliver`) | French notifications (`internal/notify/pse_localize.go`) |
| `internal/app/build_integrations.go` | release indexer (`internal/app/pse_indexer.go`), Subsonic API (`internal/app/pse_music.go`) |
| `internal/catalog/browse.go` (`browseWhere`) | playable-only and French-language catalogue (`internal/catalog/pse_hooks.go`) |
| `internal/app/build_personalization.go` (`FilterAvailable`), `internal/app/collections.go` (`collectionOptions`), `internal/catalog/lists.go` (`Similar`), `internal/collections/collections.go` (`serve` renamed `serveMembers`, franchises) | French and playable filter for Home rows, collections and similar titles (`internal/catalog/pse_discovery.go`, `internal/collections/pse_keys.go`) |
| `internal/catalog/browse_tally.go` (`tallyTotal`) | precomputed tally skipped when Ether-Remix filters apply (`pseFiltersCatalog`) |
| `internal/core/interfaces.go` (`Filter.PSEUnplayable`, `Filter.PSEThemed`) | PSE indexer walks titles outside the playable-only catalogue (`internal/app/pse_indexer.go`); French filter on themed libraries despite `IncludeKeys` (`internal/catalog/pse_hooks.go`) |
| `internal/catalog/sync.go` (background loop) | release dates for the French-language catalogue (`internal/catalog/pse_hooks.go`) |
| `internal/frontends/mediabrowser/browse.go` (views, `/Items`, folder) | "Vus et notés" libraries, `AnyProviderIdEquals` (`internal/frontends/mediabrowser/pse_scrob.go`) |
| `internal/frontends/mediabrowser/frontend.go` (`Handler`, wrapped router) | Music library for Jellyfin clients (`internal/frontends/mediabrowser/pse_music.go`, `internal/pse/music/jellyfin.go`) |
| `internal/frontends/plex/routes.go` (middleware after `authenticate`), `internal/frontends/plex/handlers_library.go` (`handleSections`), `internal/frontends/plex/handlers_server.go` (`handleProviders`), `internal/frontends/plex/handlers_hubs.go` (`handleHubSearch`, `handleLibrarySearch`) | Music section for Plex clients (`internal/frontends/plex/pse_music.go`, `internal/pse/music/plex.go`); bridge without importing `internal/pse`: `internal/frontend/pse_music.go` |
| `internal/frontends/plex/playlist_queries.go` (`writePlaylistListing`), `internal/frontends/mediabrowser/playlists.go` (`writePlaylists`) | Deezer and Last.fm playlists added to Ether's playlists (`internal/frontends/plex/pse_music.go`, `internal/frontends/mediabrowser/pse_music.go`, `internal/pse/music/playlists*.go`) |
| `internal/frontends/mediabrowser/userdata.go` (`mutationRoutes`) | `POST …/UserData` (`internal/frontends/mediabrowser/pse_scrob.go`) |
| `internal/app/extensions.go` (`extensionHandlers`) | PSE page API `/ether/api/pse/` (`internal/app/pse_console.go`, `internal/pse/console`) |
| `internal/app/extend_builtins.go` | "PSE" menu entry (`internal/app/pse_descriptors.go`) |
| `web/src/pages/settings/ConnectedServices.tsx` (import, “Watch tracking” group) | Scrob card and its side panel (`web/src/pse/ScrobCard.tsx`); playback push: `internal/pse/scrobpush`, `internal/app/pse_scrob.go` |
| `web/src/pages/sources/Sources.tsx` (tab, gallery routing) and `web/src/pages/sources/Indexers.tsx` (`view` prop, filtered list, merged order) | Sources Stremio tab (`web/src/pse/stremioTab.ts`); adapted Ether test: `web/src/test/indexer-stremio.test.tsx` |
| `internal/collections/builtins.go` (`refreshInterval`), `internal/collections/spec.go` (`refresh` validation), `web/src/lib/contract.ts` (`Refresh` type) | free "Nh" collection refresh, 1 to 168 h (`internal/collections/pse_refresh.go`, `web/src/pse/refresh.ts`) |
| `web/src/pages/collections/SettingsPanels.tsx` (Refresh field), `web/src/pages/collections/Editor.tsx` and `web/src/pages/collections/CollectionDetail.tsx` (refresh label) | "Every N h" choice (`web/src/pse/RefreshField.tsx`) |
| `web/src/pages/collections/Gallery.tsx` (import, header) | "Import an add-on" button: one collection per Stremio add-on catalog (`web/src/pse/AddonCatalogImport.tsx`) |
| `internal/frontends/plex/server.go` (`sectionByID`, `canSeeSection`), `internal/frontends/plex/handlers_library.go` (`handleSections`, `parseBrowse`), `internal/frontends/plex/handlers_server.go` (`handleProviders`), `internal/frontends/plex/handlers_hubs.go` (`buildSectionHubs`), `internal/frontends/plex/handlers_metadata.go` (`sectionCollections`) | themed libraries for Plex clients, sections 5101 to 5199 (`internal/frontends/plex/pse_themes.go`, `internal/pse/themes`, `internal/collections/pse_keys.go`); bridge without importing `internal/pse`: `internal/frontend/pse_themes.go`; indexer priority titles: `internal/app/pse_themes.go` |
| `web/src/App.tsx` (import, `BUILTIN_PAGES` with `/:tab?` tabs, `Languages` icon in the lucide import and `NAV_ICONS`) | Ether-Remix page (`web/src/pse/PsePage.tsx`), at the top of the menu under Overview (`internal/app/pse_descriptors.go`) |
| `web/src/App.tsx` (sidebar footer) | version number linking to the GitHub repository (`web/src/pse/VersionLink.tsx`) |
| `web/src/pages/Home.tsx` (import, `ActivityPanel` column) | Music panel on the Overview (`web/src/pse/MusicActivity.tsx`, `internal/pse/music/plays.go`) |
| `internal/artwork/providers.go` (`GetPoster`, `posterVersion`), `internal/frontends/plex/discovery_features.go` (`serveDiscoveryPoster`), `internal/frontends/plex/images.go` (`serveItemImage`), `internal/artwork/service/read.go` (`readEntry`), `internal/frontends/mediabrowser/frontend.go` (`Handler`) and `images.go` (`image`) | faster posters: "from the provider" marker, adjustable refresh (`PSE_POSTER_REFRESH_HOURS`) and in-memory resize cache (`internal/artwork/pse_posters.go`, aliases in `internal/frontend/pse_posters.go`); cache header and TMDB poster size for Plex (`internal/frontends/plex/pse_posters.go`); requested size and images without the fingerprint wait for Jellyfin/Emby (`internal/frontends/mediabrowser/pse_posters.go`); width selection (`internal/frontend/kit/images/pse_size.go`) |
| `.github/workflows/pr-image-build.yml` (`paths-ignore`) | no preview image for a PR that only touches documentation (`*.md`, `docs/`, `LICENSE`); `pr-image-publish.yml` only runs after a successful build, so it is skipped too |

Every PSE setting goes through `internal/pse/pseenv`: the value saved in the console (`pse_settings` table) wins over the environment variable. A new addition reads its settings with `pseenv.Get` and declares them in `internal/pse/console/fields.go`.

## Automatic synchronization

Every day at 05:17 UTC, the workflow compares Ether with the public revision recorded in `.github/pse/upstream-base`. It runs a three-way merge of the fork and Ether from that revision, without importing private history or adding Ether commits as parents. The merge goes through temporary commits sharing that revision, so Git follows files and directories Ether moves: a Remix file added in a directory Ether moves follows it, and a Remix change to a moved file lands in its new location.

The three READMEs, `.github/`, `docs/pse/`, `internal/pse/` and `web/src/pse/` remain owned by Ether-Remix. Ether workflow changes require manual review. The embedded UI is rebuilt from merged sources.

Conflicts open an issue and leave main unchanged. It lists the conflicting files, each conflict's kind (content, file deleted by Ether but modified by Remix, and so on) and the run link. Otherwise a temporary branch is published and Go/web checks run against its exact commit. Only that verified commit can be promoted; promotion fails if main has advanced. Docker images for amd64 and arm64 are then rebuilt.

The built-in `GITHUB_TOKEN` is sufficient: no personal token or synchronization secret is needed, since upstream workflows are excluded.

## Manual conflict resolution

From a clean clone:

```bash
git fetch origin
git fetch https://github.com/braedonsaunders/ether.git main
upstream_sha=$(git rev-parse FETCH_HEAD)
git switch -c sync/manual origin/main
python3 .github/pse/apply-upstream.py "$upstream_sha"
# On conflict (exit code 2), files carry <<<<<<< ||||||| >>>>>>> markers.
# Resolve them while preserving Ether-Remix additions; git diff --name-only --diff-filter=U lists them.
git add -A
printf '%s\n' "$upstream_sha" > .github/pse/upstream-base
git add .github/pse/upstream-base
cd web && pnpm install --frozen-lockfile && pnpm build && cd ..
git add -A internal/admin/dist
git commit -m "Synchroniser Ether et reconstruire l’interface"
git push origin sync/manual
# Open a PR and wait for checks before merging.
```

## Disabled Ether workflows

`ci.yml`, `api-drift.yml`, `plex-parity.yml`, `reference-drift.yml`, `refresh-update-feed.yml` and `release.yml` stay in the tree (to avoid conflicts) but are disabled in the repository's Actions settings: they publish Ether releases or use many minutes (Windows/macOS matrix) on a private repository.

## Markdown files

Ether's `.gitignore` ignores unlisted `*.md` files. Add new PSE documentation with `git add -f`.

## Remix navigation and synchronization

Navigation and categories live in `web/src/pse/navigation.tsx`, `RemixSources.tsx` and `PsePage.tsx`. Native source components retain their APIs and configuration. Ether’s Sources and Collections pages match upstream; catalogue import is available from Content & libraries. Legacy source routes redirect to Remix.

The sync script merges JSON translation conflicts by key only when edits are independent. Different translations of the same message remain blocking; the checkpoint stays unchanged. Catalogues and embedded UI are regenerated before verification. `AGENTS.md`, Remix files and workflows remain protected. The historical 775-commit backlog was integrated through `6d9bb9f17adb661540fdf292fee42ac0bb8ac353`.

The protected `internal/catalog/pse_tier_sources.go` adapter classifies fork tables for Ether’s catalogue migration: language exceptions remain protected, while automatic indexing caches do not retain the entire catalogue.

## Music in Activity

Music observations join `/status/sessions`, shared by Activity → Now playing and Overview. Plex timelines and Jellyfin/Emby reports carry playing, paused, position and stopped events; Subsonic reports starts through `scrobble?submission=false`. Sessions are isolated by viewer, protocol and device. Audio URLs and tokens are never retained. Video stop controls are not offered for music observations.

Plex/Jellyfin/Emby sessions expire after two minutes without reports. Subsonic generally has no stop signal, so observations expire after track duration plus thirty seconds (five minutes when unknown). Clients must report playback; downloads and prefetches do not create sessions. In-memory observations reset on restart or music-source reconfiguration.

Activity → History also shows existing `pse_music_plays` rows in a dedicated music section via the owner-only `/pse/music/history` API. Period, year and viewer filters apply, with pages of one hundred listens. Displayed duration is track length, not measured listening time; video statistics remain separate. Subsonic submissions populate history; Plex and Jellyfin/Emby record stops after at least thirty seconds, as before. Start notifications alone do not create history.

Hooks: `internal/app/admin.go`, `internal/app/pse_console.go`, `internal/frontend/pse_music.go`, `internal/frontends/mediabrowser/pse_music.go`, `web/src/lib/health.ts`, `web/src/components/SessionArtwork.tsx`, `web/src/pages/activity/Activity.tsx` and `NowPlaying.tsx`.

## Concert selection

The Concerts preset requires the TMDB Music genre (10402) and at least one recorded-performance keyword: `concert film` (156205), `music concert` (304615) or `live concert` (318730). The generic `concert` keyword (6029), which may describe a fictional scene, is removed. Video releases are included. Precision takes priority over completeness: poorly tagged recordings may be missing, and incorrect TMDB keywords can still produce false positives.

At startup, the three original standard queries in preset-created collections are upgraded and their members rebuilt in the background. IDs, names and other settings are retained, including renamed collections. Custom queries and personal collections are not migrated. Titles leave Concerts after rebuilding; no media is deleted.

Owned media: `internal/pse/localmedia` confines file access to configured roots; `pse_owned_media.go` publishes collections/libraries and schedules scans. Catalogue hooks preserve local keys without remote searches; resolver/playback delegate to local files. `music/owned.go` shares metadata without merging viewer history; `music/ibroadcast.go` handles OAuth and temporary playback URLs. Upstream synchronization workflows and footer versioning are unchanged.
