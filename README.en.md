# BETA — Ether-Remix

**Still in active development: bugs and breaking changes may occur.**

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>


**Ether in French, with music.**

A fork of [Ether](https://github.com/braedonsaunders/ether), Ether-Remix adds a French-oriented catalogue, custom libraries and music shared across your apps, retaining Ether’s server, debrid/Usenet sources and clients.

**[🇫🇷 Français](README.md) · 🇬🇧 English**

<p align="center">
  <img src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-lightgrey" alt="amd64 and arm64">
  <img src="https://img.shields.io/badge/licence-GPL--3.0-green" alt="GPL-3.0">
</p>

## Features

### ![](https://api.iconify.design/lucide/clapperboard.svg?color=%2314b8a6&width=24&height=24) Movies, shows and anime your way

- **Quality profiles**: formats, codecs, resolutions, HDR/Dolby Vision, size, languages and regex, globally or per library/collection. Choose profiles or simple criteria without stacking them, including detailed formats, unknown resolution, original language and declared debrid cache. Import AIOStreams from JSON or a manifest/saved source with the configuration password, preview and edit before saving (variants are not reproduced).
- **French-oriented catalogue**: choose languages and variants — VFF, VFQ, VFi, MULTi… — and cache requirements. A movie not yet available in French shows neither on Home (Trending included) nor in collections.
- **Playable-only catalogue**: show titles with a minimum number of releases matching your preferences. The rule also covers Home and collections; search, watchlists, continue watching and upcoming collections stay complete.
- **Targeted exceptions**: allow other languages for selected collections or libraries without changing your general preferences.
- **French subtitles on demand**: add the titles you want; each addition triggers a priority search when indexing is enabled.
- **Anime**: create TMDB-based libraries and collections with your French/MULTi preferences and any Stremio addons you choose.

### ![](https://api.iconify.design/lucide/radar.svg?color=%2314b8a6&width=24&height=24) Unified sources and searches ahead of playback

- **Shared Ether and Remix configuration**: Stremio addons, indexers, Usenet services, debrid accounts, music sources and IPTV in one place.
- **Background indexing**: search releases through your addons and Prowlarr, Jackett or NZBHydra2 indexers before playback starts.
- **Controlled searches**: daily per-source budgets, pauses during playback, collection/library priorities and immediate manual searches.
- **Search from Plex or Jellyfin**: top results never searched or previously without releases receive priority, subject to their retry interval.
- **SeerrCatalog availability**: import confirmed availability and recent sources from [SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog), without another search for the playable-only catalogue. Confirmation persists until deletion; expired playback sources are searched again when needed.
- **Searches for SeerrCatalog**: authorize complementary searches through Remix sources, including Prowlarr, using a dedicated token. Search and availability sharing are independent features grouped in the SeerrCatalog settings.
- **StreamFusion Reborn database**: import French, MULTi or VOSTFR releases from your instance, read-only. A title added to the catalogue later also gets its releases. [Setup, in French](docs/pse/STREAMFUSION.fr.md).

### ![](https://api.iconify.design/lucide/captions.svg?color=%2314b8a6&width=24&height=24) External subtitles and translation with SubMaker

- **Choose your instance**: use [public](https://submaker.elfhosted.com/configure) or self-hosted [SubMaker](https://github.com/xtremexq/StremioSubMaker) with a personal manifest; the integration is disabled by default.
- **Languages and scopes**: choose source/target languages, English → French by default, and allow processing globally or per library/collection.
- **Controlled preparation**: find an SRT matching the release, translate when necessary, and manage processing with a queue, cache and daily budget. Only complete, validated results are accepted; translations must retain timestamps and cue counts.
- **Playback and SeerrCatalog**: attach the prepared SRT to its matching release and send it to SeerrCatalog through authenticated search. A pending translation does not confirm availability; restart an already open playback after preparation. [Setup and limitations](docs/pse/SUBMAKER.en.md).

### ![](https://api.iconify.design/lucide/library.svg?color=%2314b8a6&width=24&height=24) Personal libraries and collections

- Organize Plex libraries by **service, studio, anime, documentaries, concerts or live shows**. Concerts combine recorded-performance keywords with the Music genre to reduce fiction matches.
- Build their rows with TMDB collections, imported Stremio catalogues and ready-made presets.
- Set collection refresh intervals from **1 to 168 hours**, independently of release searches.
- Adjust when movies appear before or after their digital/physical release with a **day offset**.
- Benefit from batched poster badge preparation in Plex and Jellyfin, per-viewer watch state and exact counts.

### ![](https://api.iconify.design/lucide/headphones.svg?color=%2314b8a6&width=24&height=24) Music in your applications

- **Music activity**: current playback reported by Plex, Jellyfin/Emby and Subsonic; one chronological history for video and music; a **Listening** tab with daily trends, listening times and track, artist, album and listener rankings, filterable by period and user.
- Browse the **public Deezer catalogue** from Plex/Plexamp, Jellyfin clients or a Subsonic application: Symfonium, Feishin, Supersonic, DSub…
- Listen through **Audius, Eclipse addons, DMM with Real-Debrid or your AllDebrid, Real-Debrid and TorBox accounts**, depending on enabled sources.
- Choose source order: when a source fails, Remix tries the next one. External HiFi API mode is used only when no other music source is active.
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

## Get started

1. **Install** `ghcr.io/aerya/ether-remix:latest` using the [provided Compose file](docs/pse/GUIDE.en.md#installation), through Dockge-Enhanced. The image is public; no GHCR login is required.
2. **Open the console** at `http://<host>:32400/ether/` and follow the setup assistant. Set the port with `ETHER_PORT`.
3. **Configure your sources**—debrid, Usenet, indexers and addons—under **Ether-Remix → Sources**, then your languages, content and libraries in their respective tabs.
4. **Connect your clients**. For Subsonic, use the server address, a local password account and **legacy** authentication; prefer HTTPS for remote access.

Migrating from Ether? The data format is preserved: back up `/config`, stop the old server and reuse its `/config` and `/cache` volumes.

## Documentation

| Need | Guide |
| --- | --- |
| Installation, settings, music sources, playlists, Scrob and clients | [Configuration guide](docs/pse/GUIDE.en.md) |
| Title visibility, priorities and search intervals | [Catalogue and indexing](docs/pse/CATALOGUE.en.md) |
| Availability sharing and complementary searches | [SeerrCatalog integration](docs/pse/SEERRCATALOG.en.md) |
| Read-only import from a StreamFusion Reborn database | [StreamFusion Reborn, in French](docs/pse/STREAMFUSION.fr.md) |
| Public or self-hosted instance, languages and translation limits | [SubMaker](docs/pse/SUBMAKER.en.md) |

## Origin and license

Ether-Remix is a fork of [Ether](https://github.com/braedonsaunders/ether) by Braedon Saunders, licensed under **GPL-3.0**.

Inspirations: [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) and [Silo Server](https://github.com/Silo-Server/silo-server). Tracking integration: [Scrob](https://github.com/Aerya/scrob). Video metadata uses [TMDB](https://www.themoviedb.org/), without endorsement or certification by TMDB.

[License](LICENSE) · [Legal information](LEGAL.md) · [Third-party notices](THIRD_PARTY_NOTICES.md)
