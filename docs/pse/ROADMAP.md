# PSE roadmap

Status as of 5 November 2026, based on Ether `v0.3.4`. Each addition ships as its own PR, in the locations defined by [FORK.md](FORK.md).

## Already provided by Ether (not rebuilt)

- **French UI**: web console translated (6,600 messages, complete FR).
- **Jellyfin clients**: Infuse, Swiftfin, Findroid, Streamyfin, Jellyfin Android TV, JellyCon, Roku.
- **Indexers**: native Newznab/Torznab, Prowlarr import, Cardigann engine, Stremio addons as sources.
- **Release monitoring** (`internal/availability`): indexer feeds read every 30 min, release "evidence" stored in the database, recent titles hidden until an acceptable release is seen, per-indexer request budgets.
- **Release languages**: `VFF`, `VFQ`, `VFi`, `MULTi`, `VOSTFR`… recognised (`internal/releaseparse`, `internal/langtag`); VOSTFR never counts as French audio.

PSE additions build on these instead of duplicating them.

## 1. French notifications — ✅ done

Package `internal/pse/notifyfr`, applied at a single point right before delivery (`notify.Service.deliver`, `PSE-HOOK` marker): every channel (Discord, Slack, Telegram, Gotify, ntfy, webhook, e-mail) and the weekly HTML digest. Fixed texts and variable templates (version, server name, provider); unknown text (plugin, new upstream message) is sent in English, nothing is lost. Viewer push notifications only carry show and episode names.

- Setting: `PSE_NOTIFY_LANGUAGE` (`fr` by default, `en` for English).
- Upstream drift: a test collects Ether's notification texts and reports untranslated ones without blocking the sync (blocking with `PSE_STRICT_I18N=1`).
- The notification queue and events broadcast to the console and plugins stay in English: only delivery is translated.

## 2. French-language catalogue — ✅ done

Package `internal/pse/frenchcat`, SQL condition added next to step 5's in `browseWhere` (same `PSE-HOOK`). A title is kept when its original language is French, when TMDB knows a past digital (4) or physical (5) release in one of the `PSE_FRENCH_COUNTRIES` (default `FR`), or when the indexer found a release whose language is in `PSE_FRENCH_RELEASES` (default `french,multi`). Release dates come from Ether's own backfill used in digital-release mode, also enabled when `PSE_FRENCH=on` (second `PSE-HOOK`, in `sync.go`): a movie whose dates are not known yet stays hidden until they are fetched (150 movies every 90 s).

Not used: TV broadcast dates (type 6, not kept by Ether), TMDB watch providers (extra calls), production countries (stored by name, unreliable), show networks.

Original description:

Optional catalogue filter combining:
- TMDB digital (4) / physical (5) / TV (6) release dates for `FR` and `CA`;
- TMDB watch providers for `FR` / `CA`;
- releases actually indexed (steps 3–4) carrying `fr` audio (VFF, VFQ, VFi, TRUEFRENCH, MULTi);
- for shows, French/Québec networks and air dates.

A title is shown when at least one enabled criterion holds. Planned as a catalogue rule (Catalog › Rules) to reuse Ether's mechanisms.

## 3. AllDebrid indexing through Stremio addons — ✅ done

The AllDebrid API no longer has a cache check (`/magnet/instant` removed in 2024–2025): checking a release directly means adding it to the account. PSE therefore **never** calls `magnet/upload` for indexing. Instead it queries the configured Stremio addons in the background (AIOStreams, Torrentio, Comet, StremThru…), which already report releases cached on AllDebrid.

Test on 5 November 2026 against an AIOStreams instance configured for AllDebrid: 8 sources for one movie, with a cache marker (⚡, `[AD]`), full file name, size and resolution. Those fields are enough for indexing.

- Stored per release: title (TMDB/IMDb, season/episode), file name, size, resolution, video/audio codec, HDR, audio/subtitle languages (Ether's parser), source, "cached" status, first and last seen.
- Indexing filters: languages, resolutions, codecs.
- Pace: prioritised title queue (recent, watchlists, popular, then back catalogue), per-addon rate cap (configurable, conservative by default for public instances; a self-hosted AIOStreams is recommended).
- To check before enabling: the AllDebrid account's magnet count before/after a batch of requests, to confirm the addon's cache check adds nothing, even temporarily.
- An addon URL contains its configuration (debrid keys): entered in the UI, stored as a secret, never logged or committed.

## 4. Prowlarr / Jackett / NZBHydra2 indexing — ✅ done

Extends Ether's monitoring beyond its 60-day window: background ID searches (IMDb/TMDB/TVDB) across the catalogue, same filters and storage as step 3, staying within Ether's per-indexer daily budgets (never beyond the background share).

### Implementation (steps 3 and 4)

Package `internal/pse/releasedb`, started from `internal/app/pse_indexer.go` (`PSE-HOOK` marker in `build_integrations.go`).

- Tables `pse_releases`, `pse_index_titles`, `pse_index_budget`, `pse_index_cursor` (SQLite and PostgreSQL).
- Walks the catalogue by descending popularity, alternating movies and shows; a title with releases is revisited after 14 days, one without after 7 days, one released less than 60 days ago after 2 days.
- Searches go through Ether's indexer manager, one source at a time, in background mode: only search requests are allowed (no NZB download, no byte read, no debrid check). PSE keeps its own per-source daily budget on top of Ether's.
- Languages: Ether's parser names, plus `vfq` (VFQ, QC), `vff` (VFF, TRUEFRENCH) and `multi`.
- Cache: the addon's marker (`⚡`, `+`) sets `debrid_cached`; an `[AD]` release without the bolt is not counted as cached.
- Filters (`lang=fr,multi;min=1080;codec=hevc;cached=1`) apply on read: nothing is dropped while indexing, so changing a filter needs no reindex.
- Still for the owner to do: compare the AllDebrid account's magnet count before/after a first batch of requests.

## 5. "Playable only" catalogue — ✅ done

Table `pse_playable` (count of distinct releases passing the filter, per title), refreshed on every indexing pass and recomputed at startup when the filter changes. A SQL condition is added to the catalogue's `browseWhere` (`PSE-HOOK` marker): home, libraries, facets, recommendations, seasons and episodes (through their show). Not filtered: key lists (watchlist, collections, continue watching), the owner's rules browser, search and direct title access. Settings `PSE_PLAYABLE`, `PSE_PLAYABLE_MIN`, `PSE_PLAYABLE_FILTER`, `PSE_PLAYABLE_PENDING`. Ether caches catalogue pages for one minute: a newly playable title shows up within the minute.

Original description:

Option: show only titles with at least *N* distinct indexed releases (default 2) matching language, resolution and codec filters. For shows: per episode, with a season/show visibility rule. Built on the step 3–4 database and Ether's visibility rules.

## 6. Scrob — ✅ done

Scrob connects as Jellyfin. Calls checked against its code (`backend/core/jellyfin.py`) and replayed in a `mediabrowser` package test:

- `Users/{id}/Views`: two PSE "Vus et notés" libraries (package `internal/frontend/kit/scrobview`, view numbers with bit 61) when `PSE_SCROB_VIEWS=on`;
- `Users/{id}/Items?ParentId=…&IncludeItemTypes=Movie|Series|Episode`: watched or rated movies, shows with a watched/rated episode (or rated themselves), watched/rated episodes; `StartIndex`/`Limit` paging, `ProviderIds`, `SeriesId`, `UserData` rendered by Ether. History read through `state.ExportEntries`, titles from the local catalogue mirror (no TMDB call);
- `Items?AnyProviderIdEquals=Tmdb.123` (and `Imdb.tt…`): missing from Ether, added;
- `POST Users/{id}/Items/{id}/UserData` (`Rating` 0–10, `Played`, `IsFavorite`): missing from Ether, added;
- `PlayedItems` (watched/unwatched), `System/Info`, `Users/{id}`: already provided by Ether.

Not covered: `Library/Refresh` (Scrob ignores the failure), Scrob's Plex connection (use the Jellyfin connection).

## 7. Music — ✅

- Starting choice: no MusicBrainz catalogue, and a **Subsonic API** first (`internal/pse/music`, mounted under `/rest/`), the protocol self-hosted music clients speak. Plex and Jellyfin Music libraries were added later, on the same library (see below).
- Default source, **account-free like Remux**: catalogue from Deezer's public API, playback through an **Eclipse addon** (`PSE_MUSIC_ECLIPSE_URL`, a `manifest.json` link; `GET /search?q=artist title` then `GET /stream/{id}` → `{url, quality}`). The best hit is picked on title, artist and duration; several addons are tried in order; the resolved URL is kept for 10 min (seeking).
- **AllDebrid playback** (`PSE_MUSIC_ALLDEBRID=on`, no DroppedNeedle, no storage): when a track starts, the album (title stripped of edition notes) is searched in audio category 3000 on Prowlarr (one call) or Ether's torrent indexers over Torznab; results naming the album and artist come first (FLAC, seeders, discographies last), a `.torrent` link without a hash is read in memory. Up to 6 torrents are opened on AllDebrid cache-only (`debrid.Provider.Acquire`, `CachedOnly`, `RemoveUncached`); the track's file is picked by name and number (and disc), unlocked and relayed. Album files kept 6 h in memory, confirmed misses 30 min, links 50 min; an outage (indexers, account, rate limit) is not a miss: retry after 15 s, doubled on each failure, 5 min at most. When the track is missing from the opened torrent, the next candidates are checked (within the 6). Real-Debrid and TorBox follow the same path; several accounts of one service are tried in priority order, each with its own caches. Eclipse, when set, is tried first (`music.Resolvers`). The account **Test** button looks for a link on chart albums without checking audio; **Test sources** checks the first bytes.
- Advanced option: a **HiFi API**, used only when no other source is active, never as a fallback (`PSE_MUSIC_HIFI_URL`, `/search/`, `/album/`, `/artist/`, `/info/`, `/track/` with a base64 manifest).
- Per-user library (`pse_music_items` table, purged and merged with the account): favourites and plays, with a metadata snapshot. An empty `search3` returns the library (Symfonium sync).
- Playback: JSON manifest → direct URL relayed with `Range` support. DASH manifest → fallback to the next quality.
- Authentication: Ether local accounts (`u`/`p` or `enc:`), 10-minute cache, lockout after 10 failures in 15 minutes. `t`/`s` tokens refused: passwords are hashed.
- Tested against Deezer (live) and a simulated Eclipse addon; real playback to be validated with the user's manifest link.
- **Jellyfin clients** (Jellyfin web, Finamp, Findroid, Streamyfin…, Emby too): a **Music** library (`CollectionType: music`) alongside Ether's. It lists the viewer's favourites and plays first, then Deezer charts so it is never empty; search queries the whole catalogue. Albums, artists (`/Artists`, `/Artists/AlbumArtists`), tracks, artwork, favourites, direct playback (`PlaybackInfo`, `/Audio/{id}/stream…`, no transcoding) and plays (`/Sessions/Playing/Stopped` after 30 s) share the Subsonic per-user library. Music IDs live in their own GUID namespace (`50534d55…`); a layer in front of Ether's router (`internal/frontends/mediabrowser/pse_music.go`) only takes requests that target music and authenticates them itself, everything else goes to Ether unchanged.
- **Plex clients** (Plexamp, Plex Web, Plex apps): a **Musique** section (`type="artist"`, number 5053) with the same contents. Artists, albums, tracks (`/library/sections/5053/all?type=8|9|10`), section hubs (recent plays, favourite albums, popular albums, artists and tracks), search (`/hubs/search` with `sectionId=5053`), artwork (`/library/metadata/{key}/thumb`, `/photo/:/transcode`), direct playback (`/library/parts/{key}/0/file.flac`, `/music/:/transcode/universal/decision` answers "direct play"), music play queues (`/playQueues`), plays (`/:/timeline` stopped after 30 s, `/:/scrobble`) and favourites (`/:/rate`). Music rating keys use kinds Ether does not (50 to 52 × 10^15). A middleware after Ether's authentication (`internal/plex/pse_music.go`) only takes requests that target music.
- Both frontends reach music through `internal/frontend/pse_music.go` (an interface published by the app), so they never import `internal/pse/music` and Ether's dependency boundaries hold.
- Plex global search (`/hubs/search` without a section, `/library/search` when `searchTypes` is empty or includes `music`): the Artists, Albums and Tracks hubs PMS already returns empty are filled; an exact or prefix match moves its hub to the front, as Ether ranks titles. Catalogue searches run in parallel, capped at 2.5 s and kept 2 minutes, so they never slow Ether's search down.
- **Deezer and Last.fm playlists** (`internal/pse/music/playlists*.go`, tables `pse_music_accounts`, `pse_music_playlists`, `pse_music_matches`): per viewer, a Deezer profile (public playlists through `/user/{id}/playlists`, including Favourite tracks) or Deezer playlists, and a Last.fm account (`user.getLovedTracks`, monthly and all-time `user.getTopTracks`, with `PSE_LASTFM_API_KEY`). Last.fm tracks are matched in Deezer (artist and title, tolerating "Remastered", "Live"…) and the match is stored; a miss is retried after a week. Deezer calls are spaced 150 ms apart. Re-imported every `PSE_MUSIC_PLAYLISTS_HOURS` (6 h), and on demand from the console. Served read-only: Subsonic (`getPlaylists`, `getPlaylist`), Jellyfin (music GUID type 4, `/Playlists/{id}[/Items]`, `/Items?ParentId=`, added to Ether's list), Plex (ratingKey 53 × 10^15, `/playlists?playlistType=audio`, `/playlists/{key}/items`, composite artwork, play queues by `playlistID`, added to Ether's untyped list). Spotify left out: its API requires a developer app and a per-account OAuth sign-in.
- Not covered: music on Plex's home page (Jellyfin shows it through `Items/Latest`), playlists created from clients, lyrics, transcoding.

## 8. Stremio catalogs as collections — ✅ done

- Ether can already use a Stremio add-on catalog as a collection source (`plugin:<addon>/<catalog>`, `list_source` role), one at a time. PSE adds bulk import (`web/src/pse/AddonCatalogImport.tsx`): one published collection per ticked movie or series catalog, sorted in add-on order, capped at N titles (100 by default).
- Free `Nh` cadence (1 to 168) on top of `live`, `hourly`, `daily` and `weekly`, accepted by Ether's validation and read by its background rebuild (`internal/collections/pse_refresh.go`). Any unknown value keeps Ether's behaviour.

## Proposed order

1 → 3 → 4 → 5 → 2 → 6 → 7 → 8. The French catalogue (2) gets more accurate once the release database (3–4) is populated.
