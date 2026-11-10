# BETA — Ether-Remix

**Still in active development: bugs and breaking changes may occur.**

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

### ![](https://api.iconify.design/lucide/clapperboard.svg?color=%2314b8a6&width=24&height=24) Movies, shows and anime your way

- **French-oriented catalogue**: choose languages and variants — VFF, VFQ, VFi, MULTi… — along with resolution, codec and cache requirements.
- **Playable-only catalogue**: show titles with a minimum number of releases matching your preferences. Search, collections, watchlists and continue watching remain accessible.
- **Targeted exceptions**: allow other languages for selected collections or libraries without changing your general preferences.
- **French subtitles on demand**: add the titles you want; each addition triggers a priority search when indexing is enabled.
- **Anime**: create TMDB-based libraries and collections with your French/MULTi preferences and any Stremio addons you choose.

### ![](https://api.iconify.design/lucide/radar.svg?color=%2314b8a6&width=24&height=24) Unified sources and searches ahead of playback

- **Shared Ether and Remix configuration**: Stremio addons, indexers, Usenet services, debrid accounts, music sources and IPTV in one place.
- **Background indexing**: search releases through your addons and Prowlarr, Jackett or NZBHydra2 indexers before playback starts.
- **Controlled searches**: daily per-source budgets, pauses during playback, collection/library priorities and immediate manual searches.
- **Search from Plex or Jellyfin**: top results never searched or previously without releases receive priority, subject to their retry interval.
- **SeerrCatalog integration**: import availability and previously checked sources for Seerr requests without duplicate searches. Normal playback discovery remains the fallback.

### ![](https://api.iconify.design/lucide/library.svg?color=%2314b8a6&width=24&height=24) Personal libraries and collections

- Organize Plex libraries by **service, studio, anime, documentaries, concerts or live shows**.
- Build their rows with TMDB collections, imported Stremio catalogues and ready-made presets.
- Set language exceptions, search priorities and collection refresh intervals from **1 to 168 hours**.
- Adjust when movies appear before or after their digital/physical release with a **day offset**.
- Benefit from batched poster badge preparation in Plex and Jellyfin, per-viewer watch state and exact counts.

### ![](https://api.iconify.design/lucide/headphones.svg?color=%2314b8a6&width=24&height=24) Music in your applications

- Browse the **public Deezer catalogue** from Plex/Plexamp, Jellyfin clients or a Subsonic application: Symfonium, Feishin, Supersonic, DSub…
- Listen through **Audius, Eclipse addons, DMM with Real-Debrid or your AllDebrid, Real-Debrid and TorBox accounts**, depending on enabled sources.
- Choose source order: when a source fails, Remix tries the next one. A separate external HiFi API mode is also available.
- Keep **per-user favourites and listening history**, without building a local music-file library. Available quality depends on the sources.

### ![](https://api.iconify.design/lucide/list-music.svg?color=%2314b8a6&width=24&height=24) Your playlists and new discoveries

- **Browse Deezer playlists** and connect your Spotify account to find playlists you can access.
- **Import by URL** from Deezer, Spotify or ListenBrainz, or from CSV/XSPF files.
- Review the preview, choose the destination viewer and track matched or missing tracks. Imports create personal copies without replacing other playlists.
- Synchronize public Deezer profiles, Last.fm and ListenBrainz; manage personal playlists through Subsonic clients.
- Explore **country charts, recent plays, most-played tracks and new releases from followed artists**. Genres, similar artists, radios and mixes depend on the client.

### ![](https://api.iconify.design/lucide/chart-no-axes-combined.svg?color=%2314b8a6&width=24&height=24) Statistics and tracking

- Remix opens on statistics: coloured progress rings, a thin-bar history chart and live per-source searches, errors, quotas, durations and results.
- View history **by day, week, month or year**, as period or cumulative values, filtered by movies, shows, anime and sources.
- Compare the local catalogue with TMDB totals and review operations in the activity log.
- Connect Remix to **Scrob as a Jellyfin server** and choose sync directions and schedules in Scrob: watched status and ratings, one way or both. The real-time event webhook (**60-second** timeout) and history-only libraries are optional.
- Connect **Last.fm** per user for music scrobbling and find listening statistics in Overview.

### ![](https://api.iconify.design/lucide/palette.svg?color=%2314b8a6&width=24&height=24) An interface that fits your habits

- Six tabs: **Sources**, **Languages and quality**, **French subtitles on demand**, **Content and libraries**, **Statistics**, **Notifications and tracking**.
- Sources grouped by family, category/content sorting, on-demand debrid key display and collapsible sections that preserve your input. Choose palettes in **Appearance**.
- Six palettes: **Ether, Nord, Catppuccin, Solarized, Gruvbox and Rosé Pine**, each with light and dark modes, instant preview and per-browser persistence.

### ![](https://api.iconify.design/lucide/refresh-cw.svg?color=%2314b8a6&width=24&height=24) Ether, extended by Remix

- Retains Ether's features and compatible clients: **Plex, Jellyfin, Emby, Infuse, Swiftfin, Silo and Stremio**, with its debrid and Usenet video sources.
- Daily synchronization with the original project, checks before merging and failure reporting.
- Docker images for **amd64 and arm64**, with update notes in the console.

## SeerrCatalog: from requests to available titles

Under **Sources → Stremio → SeerrCatalog**, one section groups **Availability to Remix** (import SeerrCatalog results) and **Search via Remix** (searches requested by SeerrCatalog). Each feature keeps its own switch and token. Test availability sharing in Remix and searches in SeerrCatalog. Standalone Prowlarr has separate settings in SeerrCatalog.

[SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog) turns Seerr requests into Stremio catalogues and shares availability checks and sources with Remix, avoiding duplicate searches.

1. In SeerrCatalog, open **Settings → Ether Remix integration**, generate a token and enable export.
2. In Remix, open **Sources → Stremio → SeerrCatalog → Availability to Remix**, enter the base address (e.g. `https://seerrcatalog.example`, without `/api/…`) and token, enable and save. **Test connection** checks the feed without importing; synchronization runs every minute.
3. The updated SeerrCatalog automatically exports previously confirmed titles without running new searches.

SeerrCatalog availability passes the **playable-only** filter without querying addons again. Hidden or upcoming titles do not block synchronization of other availability records. Catalogue filters and viewer playback policies still apply. Imports retrieve TMDB metadata without creating collections.

Availability lasts **26 h**, rechecked after 24 h with an hourly wake-up. URLs and headers are reused for at most **30 min**; torrent hashes until observation expiry. Expired, policy-rejected or failed sources fall back to normal discovery. URLs may expire earlier or depend on IP restrictions. Only checked episodes provide reusable sources.

Paginated synchronization recovers changes and deletions after outages; incomplete imports preserve valid data. Sensitive sources stay in memory, outside catalogue APIs, and reload on restart. Disabling removes integration trust. The token shares sources from **all SeerrCatalog users**: use a trusted Remix instance and HTTPS outside private networks.

Equivalent variables: `PSE_SEERRCATALOG=on`, `PSE_SEERRCATALOG_URL=http://seerrcatalog:7000`, `PSE_SEERRCATALOG_TOKEN=…`. Disabled by default.

**Complementary search for SeerrCatalog**: open **SeerrCatalog → Settings → Ether Remix integration → Search via Remix**, enable search and enter the Remix base URL. Generate a **dedicated search token** in that section, then paste it into **Remix → Sources → Stremio → SeerrCatalog → Search via Remix** and enable searches. Save on both sides. Availability sharing has its own token and can remain disabled. After updating both applications, configure this new token: the old sharing token no longer authorizes searches. When addons return no primary-language release and user fallback is enabled, SeerrCatalog asks Remix to search its configured sources, including Prowlarr. SeerrCatalog still applies primary/fallback tags, deduplication and thresholds, keeping J+1/J+3 checks and permanent confirmation. Searches respect availability budgets and cannot circularly reuse SeerrCatalog observations. Torrents with infoHashes and actual HTTP streams can be returned; NZB URLs are never exposed as video streams. Test the connection from SeerrCatalog without launching a search.

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
