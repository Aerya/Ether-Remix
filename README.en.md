> [!WARNING]
> **BETA — Ether-Remix is still in active development. Bugs and breaking changes may occur.**

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>

# Ether-Remix

**Ether in French, with music.**

A fork of [Ether](https://github.com/braedonsaunders/ether), Ether-Remix adds a French-oriented catalogue, custom libraries and music shared across your apps, retaining Ether’s server, debrid/Usenet sources and clients.

**[🇫🇷 Français](README.md) · 🇬🇧 English**

<p align="center">
  <img src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-lightgrey" alt="amd64 and arm64">
  <img src="https://img.shields.io/badge/licence-GPL--3.0-green" alt="GPL-3.0">
</p>

## Features

| Feature | What it provides |
| --- | --- |
| **French catalogue and quality** | Movies, shows and anime filtered by country, language (VFF, VFQ, VFi, MULTi…), resolution, codec and cache. Collection/library exceptions and **French subtitles on demand** with priority searches. **Playable only** mode requires a minimum release count; detailed rules below. |
| **Indexing and sources** | Stremio addons and Prowlarr, Jackett, NZBHydra2 indexers: background searches, daily budgets, playback pauses and collection/library priorities. Unified Ether and music sources, category/content sorting, on-demand debrid key display. |
| **Libraries and collections** | Plex libraries by service, studio, anime, documentary, concert or live show; TMDB collections and imported Stremio catalogues, custom rows, presets and language exceptions. Configurable 1–168 h refresh. Anime under **Content and libraries → Anime**, with French/MULTi criteria and any addon manifest, using shared priorities. |
| **SeerrCatalog** | Imports available requests, TMDB records and previously checked sources without duplicate searches; normal playback discovery remains the fallback. Optional integration, detailed below. |
| **Browsing and statistics** | Batched Plex/Jellyfin poster badges, per-viewer watch state, direct playable-catalogue access and exact counts. Remix opens on statistics, with coloured progress rings and a thin-bar history chart. TMDB comparison, daily/weekly/monthly/yearly history, period or cumulative values, movie/show/anime and source filters, live tracking and activity log. |
| **Music** | Public Deezer catalogue through Plex/Plexamp, Jellyfin and Subsonic; per-user favourites and history, without local music files. Sources, automatic fallback and clients detailed below. |
| **Playlists** | Deezer discovery, personal Spotify connection, Deezer/Spotify/ListenBrainz links or CSV/XSPF imports, with previews and matching progress. Public Deezer profiles, Last.fm and ListenBrainz sync; personal playlists editable through Subsonic. |
| **Music discovery** | Favourites, recent plays, most-played tracks, country charts, discoveries and new releases from followed artists; genres, similar artists, radios and mixes where supported by the client. |
| **Tracking and notifications** | Bidirectional Scrob via Jellyfin, with automatically refreshed connection activity: outgoing video events, incoming watched status and ratings per viewer; “Watched and rated” libraries for history imports without scanning the catalogue; per-user Last.fm music scrobbling and Overview statistics. Dedicated **Notifications and tracking** tab. |
| **Interface** | Six Remix tabs, grouped language/quality settings, music sources by family, collapsible source and priority sections preserving unsaved settings. Six palettes — Ether, Nord, Catppuccin, Solarized, Gruvbox, Rosé Pine — in light/dark mode, with instant preview and per-browser persistence under **Appearance**. |
| **Updates** | Unread updates in the console; daily Ether synchronization, pre-merge checks, failure reporting and `amd64`/`arm64` Docker images. |
Plex, Jellyfin, Emby, Infuse, Swiftfin, Silo and Stremio compatibility, along with debrid/Usenet video streaming, come from **Ether**. Ether-Remix extends these capabilities.

Under **Sources → Stremio, Indexers and Indexing**, click a section title to collapse or expand it.

## SeerrCatalog: from requests to available titles

[SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog) turns Seerr requests into Stremio catalogues and shares availability checks and sources with Remix, avoiding duplicate searches.

1. In SeerrCatalog, open **Settings → Ether Remix integration**, generate a token and enable export.
2. In Remix, open **Sources → Stremio → SeerrCatalog → Ether Remix**, enter the base address (e.g. `https://seerrcatalog.example`, without `/api/…`) and token, enable and save. **Test connection** checks the feed without importing; synchronization runs every minute.
3. The updated SeerrCatalog automatically exports previously confirmed titles without running new searches.

SeerrCatalog availability passes the **playable-only** filter without querying addons again. Other visibility filters and viewer playback policies still apply. Imports retrieve TMDB metadata without creating collections.

Availability lasts **26 h**, rechecked after 24 h with an hourly wake-up. URLs and headers are reused for at most **30 min**; torrent hashes until observation expiry. Expired, policy-rejected or failed sources fall back to normal discovery. URLs may expire earlier or depend on IP restrictions. Only checked episodes provide reusable sources.

Paginated synchronization recovers changes and deletions after outages; incomplete imports preserve valid data. Sensitive sources stay in memory, outside catalogue APIs, and reload on restart. Disabling removes integration trust. The token shares sources from **all SeerrCatalog users**: use a trusted Remix instance and HTTPS outside private networks.

Equivalent variables: `PSE_SEERRCATALOG=on`, `PSE_SEERRCATALOG_URL=http://seerrcatalog:7000`, `PSE_SEERRCATALOG_TOKEN=…`. Disabled by default.

**Complementary search for SeerrCatalog**: select Ether Remix in SeerrCatalog settings and enter its base URL. Generate a **dedicated search token** in that section, then paste it into **Remix → Sources → Stremio → Searches for SeerrCatalog** and enable searches. Save on both sides. Availability sharing has its own token and can remain disabled. After updating both applications, configure this new token: the old sharing token no longer authorizes searches. When addons return no primary-language release and user fallback is enabled, SeerrCatalog asks Remix to search its configured sources, including Prowlarr. SeerrCatalog still applies primary/fallback tags, deduplication and thresholds, keeping J+1/J+3 checks and permanent confirmation. Searches respect availability budgets and cannot circularly reuse SeerrCatalog observations. Torrents with infoHashes and actual HTTP streams can be returned; NZB URLs are never exposed as video streams. Test the connection from SeerrCatalog without launching a search.

## Music sources

Enable sources independently in **Console → Ether-Remix → Sources → Music streaming**. Existing debrid accounts and indexers are reused.

| Source | Access and role |
| --- | --- |
| **Audius** | Public MP3 without an account or API key, for matching catalogue tracks. Comes first or acts as a fallback. |
| **Tido, Internet Archive, UltraMax, Deezer Ricky, SoundCloud (Eclipse and Ricky)** | Dedicated entries with setup links and masked personal manifests. UltraMax and Deezer Ricky require a Deezer account. Quality and availability depend on the provider. |
| **Other Eclipse addons** | One or more `manifest.json` links, tried in order. |
| **DMM · Real-Debrid** | Searches Debrid Media Manager’s music catalogue, then streams using your existing Real-Debrid accounts, without Prowlarr. |
| **AllDebrid, Real-Debrid, TorBox** | Your existing accounts, to stream tracks from albums already cached. Several accounts of the same service are tried one after another. |
| **HiFi API** | A separate mode using an external instance: used **only** when no other source is active, never added as a fallback. |

Sources are tried automatically in priority order, with fallback to the next source. The console shows their order and status. Provider setup links are refreshed from the public Eclipse registry.

Streams are relayed without audio conversion or local storage. Audio availability and quality depend on the sources and files they provide.

### Music clients

| Client | Access |
| --- | --- |
| Plexamp, Plex Web, Plex apps | Plex **Music** section |
| Finamp, Jellyfin web, Findroid, Streamyfin | Jellyfin **Music** library |
| Symfonium, Feishin, Supersonic, Tempo, DSub… | Subsonic API, **legacy** authentication |

## Which titles are shown and searched

With the **playable-only** catalogue, Remix knows the whole TMDB catalogue but only shows a title with enough indexed releases (2 by default) matching the chosen filter, for example French or MULTi, cached on debrid. **VOSTFR** titles and language exceptions count releases in any language.

- **Filtered lists**: home, Movies, TV shows, recommendations. Search, watchlist, collections and continue watching are not filtered: a chosen title stays reachable, and playback queries sources live.
- **Search order**: TMDB popularity, highest first, alternating movies and shows. VOSTFR titles and language exceptions go first, then prioritized collections and libraries.
- **Immediate search**: adding a VOSTFR title, the **Search now** button, and a search in Plex or Jellyfin. For a search, the first 5 titles found move to the front of the queue if they were never searched, or found nothing more than 6 h ago.
- **Risky titles**: the **Also show risky titles** option keeps titles with fewer releases than the minimum (at least one). Plex and Jellyfin flag them with “⚠ 1 source” at the start of the summary, a “1 SOURCE” badge on Ether posters, or both. With another poster tool, pick the summary.
- **Local catalogue**: Ether does not list all of TMDB, only titles popular enough and already released, per **Catalog → Hide obscure titles**. Remix searches releases within that set.

## Refresh intervals

**TMDB metadata**

| Data | Frequency |
| --- | --- |
| Full catalogue | Every 24 h (configurable) |
| New releases and trending | Every 4 h |
| Missing detailed records | In batches every 90 s, recent releases and popular titles first |
| Ongoing series | Record refreshed on each catalogue cycle; 1 h at most when an active season is displayed |

**Release search**

| Last search result | Next attempt after |
| --- | --- |
| Title released less than 60 days ago | 2 days |
| No release found | 7 days |
| Releases found | 14 days |
| Source down or partial answer | 24 h |

These intervals are minimums. Searching depends on each source's daily budget (1000 requests by default) and the delay between two titles (10 s by default), and pauses during playback: on a large catalogue, a title may wait longer. A release not seen again for 60 days no longer counts.

Under **Sources → Indexing**, the collapsible **Search priority** block moves a collection or library ahead of the rest: *High* retries a title without releases after 1 day, *Maximum* after 6 h. **Search now** skips these delays for the selection. Per-source budgets still apply. On import, SeerrCatalog (hourly refresh, maximum priority) and RSS Catalog (every 3 h, high priority) are recognized and preset; you can change these settings.

**Collections and libraries**

Each collection has its own schedule: `live` (on demand), `hourly`, `daily` (default), `weekly` or every N hours, from 1 to 168. Some sources enforce a minimum delay. Libraries update along with their collections. This schedule is independent of release-search priority; **Refresh**, in the same section, immediately refreshes a collection or every collection in a library.

## Get started

1. **Install** `ghcr.io/aerya/ether-remix:latest` using the [provided Compose file](docs/pse/GUIDE.en.md#installation), through Dockge-Enhanced. The image is public; no GHCR login is required.
2. **Open the console** at `http://<host>:32400/ether/` and follow the setup assistant. Set the port with `ETHER_PORT`.
3. **Configure your sources**—debrid, Usenet, indexers and addons—under **Ether-Remix → Sources**, then your languages, content and libraries in their respective tabs.
4. **Connect your clients**. For Subsonic, use the server address, a local password account and **legacy** authentication; prefer HTTPS for remote access.

Migrating from Ether? The data format is preserved: back up `/config`, stop the old server and reuse its `/config` and `/cache` volumes.

## Documentation

- [English guide: installation, variables, Scrob, music and clients](docs/pse/GUIDE.en.md) · [Guide français](docs/pse/GUIDE.fr.md)

## Origin and license

Ether-Remix is a fork of [Ether](https://github.com/braedonsaunders/ether) by Braedon Saunders, licensed under **GPL-3.0**.

Inspirations: [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) and [Silo Server](https://github.com/Silo-Server/silo-server). Tracking integration: [Scrob](https://github.com/Aerya/scrob). Video metadata uses [TMDB](https://www.themoviedb.org/), without endorsement or certification by TMDB.

[License](LICENSE) · [Legal information](LEGAL.md) · [Third-party notices](THIRD_PARTY_NOTICES.md)
