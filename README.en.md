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

Ether-Remix extends [Ether](https://github.com/braedonsaunders/ether) with a French-oriented catalogue, custom libraries and a music library shared across your apps. You keep Ether’s server, debrid/Usenet sources and compatible clients; Ether-Remix adds the features below.

**[🇫🇷 Français](README.md) · 🇬🇧 English**

<p align="center">
  <img src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-lightgrey" alt="amd64 and arm64">
  <img src="https://img.shields.io/badge/licence-GPL--3.0-green" alt="GPL-3.0">
</p>

**What’s new** appears after a release and remains available from the console header.

## Features

| Addition | Features |
| --- | --- |
| **A French-oriented catalogue** | Movies and shows filtered by French availability and detected releases: VFF, VFQ, VFi, MULTi… Configurable countries and languages, exceptions for collections or libraries, and an **On demand** tab for explicit French-subtitled exceptions with priority searches. |
| **A “playable only” catalogue** | Shows titles with a minimum number of indexed releases matching your language, resolution, codec and cache criteria. Search, watchlist and continue watching remain accessible without this filter. |
| **Source searches ahead of playback** | Background indexing of Stremio addons and Prowlarr, Jackett or NZBHydra2 indexers, with daily budgets and pauses during playback. |
| **A dedicated dashboard** | A Stats tab with TMDB comparison, daily, weekly, monthly and yearly history, period or cumulative values, movie/show/anime and source filters, live tracking and an activity log. |
| **Libraries tailored to you** | Plex libraries for services, studios, anime, documentaries, concerts or live shows, with collection rows and language exceptions. Import Stremio catalogues as collections, use ready-made presets and set refresh intervals from 1 to 168 hours. |
| **Anime in your libraries** | Anime libraries and collections from TMDB, your language criteria including French/MULTi, UwU+ under Sources → Stremio → Suggested providers, and any other Stremio addon with shared priorities. |
| **Music in your apps** | A music library in Plex/Plexamp and Jellyfin clients, plus a Subsonic API for Symfonium, Feishin, Supersonic, DSub… Public Deezer catalogue, with each user’s own favourites and listening history. |
| **Playlists and discovery** | Deezer, Last.fm and ListenBrainz imports; personal playlists editable through Subsonic; favourites, recent plays, most-played tracks, country charts, discoveries and new releases from followed artists. Browse genres, similar artists, radios and mixes where supported by the client. |
| **Tracking with Scrob and Last.fm** | Send video events to Scrob and expose “Watched and rated” libraries to import history without scanning the whole catalogue. Per-user Last.fm linking for music plays, with listening statistics in Overview. |
| **Convenient settings and notifications** | Six Remix tabs, one configuration for Ether and music sources, indexing selection sortable by category, grouped language and quality filters, a What’s new popup and French owner notifications. |
| **A fork that follows Ether** | Daily synchronization with checks before merging, failure reporting and `amd64`/`arm64` Docker images. Ether’s features and fixes remain the project’s foundation. |

Plex, Jellyfin, Emby, Infuse, Swiftfin, Silo and Stremio compatibility, along with debrid/Usenet video streaming, come from **Ether**. Ether-Remix extends these capabilities; you do not need to build a local music-file library.

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

In **Libraries**, **Search priority** moves a collection or library ahead of the rest: *High* retries a title without releases after 1 day, *Maximum* after 6 h. **Search now** skips these delays for the selection. Per-source budgets still apply. On import, SeerrCatalog (hourly refresh, maximum priority) and RSS Catalog (every 3 h, high priority) are recognized and preset; you can change these settings.

This priority applies to release search. A collection's content refresh follows its own schedule, set per collection (live, every N hours, daily, weekly). To run it right away, the **Refresh** button in the same section restarts the collection, or every collection in a library.

**Collections and libraries**

Each collection has its own schedule: `live` (on demand), `hourly`, `daily` (default), `weekly` or every N hours, from 1 to 168. Some sources enforce a minimum delay. Theme libraries update along with their collections.

## Get started

1. **Install** `ghcr.io/aerya/ether-remix:latest` using the [provided Compose file](docs/pse/GUIDE.en.md#installation), through Dockge-Enhanced. The image is public; no GHCR login is required.
2. **Open the console** at `http://<host>:32400/ether/` and follow the setup assistant. Set the port with `ETHER_PORT`.
3. **Configure your sources**—debrid, Usenet, indexers and addons—in Ether’s pages, then enable the features you want under **Ether-Remix**.
4. **Connect your clients**. For Subsonic, use the server address, a local password account and **legacy** authentication; prefer HTTPS for remote access.

Migrating from Ether? The data format is preserved: back up `/config`, stop the old server and reuse its `/config` and `/cache` volumes.

## Documentation

- [English guide: installation, variables, Scrob, music and clients](docs/pse/GUIDE.en.md) · [Guide français](docs/pse/GUIDE.fr.md)

## Origin and license

Ether-Remix is a fork of [Ether](https://github.com/braedonsaunders/ether) by Braedon Saunders, licensed under **GPL-3.0**.

Inspirations: [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) and [Silo Server](https://github.com/Silo-Server/silo-server). Tracking integration: [Scrob](https://github.com/Aerya/scrob). Video metadata uses [TMDB](https://www.themoviedb.org/), without endorsement or certification by TMDB.

[License](LICENSE) · [Legal information](LEGAL.md) · [Third-party notices](THIRD_PARTY_NOTICES.md)
