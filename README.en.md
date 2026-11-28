<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>

# Ether-Remix

**Movies, shows, anime and music in the applications you already use.**

Ether-Remix brings remote sources and local files together in an on-demand media server based on [Ether](https://github.com/braedonsaunders/ether). It adds a French-oriented catalogue, custom libraries, music and per-viewer tracking.

**[Français](README.md) · English** · Docker **amd64 / arm64** · **GPL-3.0** · Beta: bugs and breaking changes are possible

[Features](#features) · [Sources](#sources) · [Apps](#apps) · [Installation](#get-started) · [Documentation](#documentation)

You supply the required sources and accounts. For debrid and Usenet, Remix finds a release matching your criteria and streams it without mandatory advance downloads or Sonarr/Radarr, a download client or a mount to scan. It can coexist with an existing Plex, Jellyfin or Emby server. Optional sources have their own storage requirements, described below. A catalogue entry does not guarantee playback.

<a id="nav-remix-features"></a>
<a id="features"></a>
<a id="nav-movies-shows-and-anime-your-way"></a>
<a id="nav-your-local-movies-and-personal-videos"></a>
<a id="nav-external-subtitles-and-translation-with-submaker"></a>
<a id="nav-personal-libraries-and-collections"></a>
<a id="nav-music-in-your-applications"></a>
<a id="nav-your-playlists-and-new-discoveries"></a>
<a id="nav-statistics-and-tracking"></a>
<a id="nav-an-interface-that-fits-your-habits"></a>
<a id="nav-ether-extended-by-remix"></a>
<a id="ether-at-a-glance"></a>
<a id="remix-at-a-glance"></a>
<a id="what-remix-does"></a>

## What you can do

| Use | Features |
| --- | --- |
| **Movies, shows and anime** | TMDB catalogue, French/MULTi preferences, quality rules, subtitled exceptions, SubMaker subtitles and translation. |
| **Music** | Deezer catalogue, local files and iBroadcast; favourites, history, personal playlists, imports and scheduled mixes. |
| **Libraries and home** | Themed collections, JukeBox setup, Xperience imports, layout templates, posters and custom navigation cards. |
| **Sources and playback** | Configurable priorities, background searches and a shared release index; media identity, language and quality checks. |
| **Viewers and tracking** | Private or shared access, parental controls and stream limits; cross-app resume, new episodes in Continue Watching, Scrob, Last.fm and statistics. |
| **Console** | French interface, eight-step setup assistant with a preview before applying, Simple/Expert modes, diagnostics and appearance settings. |

Use **Recommendations** to arrange home and library rows for your viewers. Templates save and reapply layouts. Sharing also applies to Remix libraries in Plex, Stremio and Jellyfin; Jellyfin offers exclusion rules for latest additions. [Detailed features](docs/pse/FEATURES.en.md).

Local folders and iBroadcast accounts are private by default, with optional sharing. Remix reports missing or problematic files without deleting originals. Light lists avoid enrichment while browsing; details load when opening an item or starting playback.

<a id="alldebrid-on-a-vps-mediaflow-and-gluetun"></a>
<a id="nav-alldebrid-on-a-vps-mediaflow-and-gluetun"></a>
<a id="nav-unified-sources-and-searches-ahead-of-playback"></a>
<a id="sources"></a>
<a id="one-release-index"></a>

## Sources and integrations

Catalogue sources describe content; playback sources provide access to it. Choose only the integrations your setup needs.

| Source or service | Purpose and requirements |
| --- | --- |
| **Debrid / Usenet** | On-demand playback with your accounts and providers; configurable priorities, quotas and fallbacks. |
| **Prowlarr, Jackett, NZBHydra2 / Stremio addons** | Release searches and, depending on the addon, stream access. A compatible playback route is still required. One proxy per addon covers requests and playback. |
| **StreamFusion Reborn / SeerrCatalog / Pre** | Catalogue and reference imports or enrichment. SeerrCatalog requires its external service; Pre, in beta, supplies announcements, not playback links. |
| **Alkodiques (StreamFusion API)** | Already verified AllDebrid files from your StreamFusion, read through its read-only API (local copy kept up to date, every episode of catalogue titles, a single link unlock). Requires an AllDebrid account and StreamFusion's read key. Optional. [Guide, in French](docs/pse/ALKODIQUES.fr.md). |
| **Torrent client / Seedstrem / XDCC Grabscher** | Downloads to disk; Seedstrem and Grabscher require their external services. Optional sources. |
| **FTP / SFTP** | Remote playback without a full copy is possible, with caching; requires a server and credentials. Optional. |
| **aMule / Soulseek (slskd)** | Video/music search, manual or on-playback transfers; playback after the download finishes. External services and a shared directory required. |
| **Local files / iBroadcast** | Your videos and audio from folders or network mounts; iBroadcast music libraries through your accounts. |
| **Music sources** | Deezer catalogue with playback through enabled sources, including Eclipse, Audius, DMM/debrid and OpenDeezer with your own Deezer account. |
| **Deezer / Spotify / ListenBrainz / Last.fm** | Playlist browsing, import or synchronization depending on the service; scheduled playlist following and personal mixes. |
| **AudioMuse / Digarr** | AudioMuse suggests already analysed local tracks; Digarr recommends artists/albums and lets you create playlists from matched albums. Optional external services; no guarantee of audio availability. |

Torrent, XDCC, FTP/SFTP, aMule and slskd connectors are off by default. **Priorities** let you prefer Usenet or BitTorrent and limit debrid usage. **Background search** prepares the index without downloading or unrestricting files; remote addons may make their own requests. Initial show indexing covers S01E01, not a whole season. [Priorities](docs/pse/QUICK_SOURCES.en.md) · [Index and limits](docs/pse/RELEASE_LIBRARY.en.md).

<a id="nav-compatible-apps"></a>
<a id="apps"></a>
<a id="applications"></a>

## Compatible applications

| Access | Examples and specifics |
| --- | --- |
| **Plex** | Plex apps, Plexamp and Kodi with PM4K. |
| **Jellyfin / Emby** | Infuse, Swiftfin, Findroid, Streamyfin and compatible TV clients. Emby for TV 2.x on Apple TV is not supported. |
| **Silo** | Desktop, mobile and TV clients. |
| **Stremio** | Personal HTTPS addon, with catalogues or files only; version and quality choices. |
| **Nuvio** | Per-viewer linking, Stremio link installation, two-way resume and history synchronization. |
| **WebDAV** | Infuse, VLC, Kodi and rclone. |
| **IPTV / local network** | M3U, XMLTV, Xtream and DLNA, depending on enabled access. |
| **Subsonic** | Symfonium, Feishin, Supersonic, DSub and other compatible music clients. |

Enable access under **Console → Apps**. Each viewer finds their links in **Your account**. Features and presentation depend on the client. [Compatibility and Stremio/Nuvio settings](docs/pse/FEATURES.en.md#compatible-apps).

<a id="nav-get-started"></a>
<a id="get-started"></a>

## Quick installation

1. Install `ghcr.io/remix-media/ether-remix:latest` using the [guide's Compose file](docs/pse/GUIDE.en.md#installation). Persist `/config` and `/cache`.
2. Open `http://<host>:32400/ether/` and follow the wizard. Change the port with `ETHER_PORT` if needed.
3. Add accounts and providers under **Remix → Sources**; set language, quality and priorities under **Playback**.
4. Optionally enable **Automatically search for releases** under **Indexing**, then browse **Releases**. Turning it off retains the index and its use at playback.
5. Enable apps and connect viewers. For token-based Subsonic clients: **Content → Music → Music clients**. **Expert** mode exposes additional settings; **Diagnostics** helps explain playback failures.

To migrate from Ether, back up `/config`, stop the old server and reuse its volumes. Use HTTPS for remote access. The old `ghcr.io/aerya/ether-remix:latest` image remains maintained through a mirror: existing installations can keep it. [Docker migration](docs/pse/DOCKER-MIGRATION.en.md).

<a id="nav-documentation"></a>
<a id="documentation"></a>

## Documentation by need

| Need | Guides |
| --- | --- |
| Installation, console and local media | [User guide](docs/pse/GUIDE.en.md) · [Setup assistant](docs/pse/SETUP.en.md) · [Folders and iBroadcast](docs/pse/GUIDE.en.md#local-media-and-ibroadcast) |
| Features, libraries, sharing and clients | [Detailed features](docs/pse/FEATURES.en.md) |
| Catalogue, search and priorities | [Catalogue](docs/pse/CATALOGUE.en.md) · [Release index](docs/pse/RELEASE_LIBRARY.en.md) · [Priorities](docs/pse/QUICK_SOURCES.en.md) |
| Catalogue enrichment | [StreamFusion (FR)](docs/pse/STREAMFUSION.fr.md) · [SeerrCatalog](docs/pse/SEERRCATALOG.en.md) · [Pre](docs/pse/PRE.en.md) |
| Downloads and remote sources | [Torrent](docs/pse/TORRENT.en.md) · [Seedstrem](docs/pse/SEEDSTREM.en.md) · [XDCC](docs/pse/XDCC.en.md) · [FTP/SFTP](docs/pse/FTP.en.md) · [aMule/slskd](docs/pse/PEER.en.md) |
| Music, playlists and discovery | [Audio sources (FR)](docs/pse/AUDIO-SOURCES.fr.md) · [Playlists/AudioMuse](docs/pse/MUSIC-FLOWS.en.md) · [Digarr](docs/pse/DIGARR.en.md) |
| Network and subtitles | [AllDebrid/MediaFlow/proxies](docs/alldebrid-mediaflow.md) · [SubMaker](docs/pse/SUBMAKER.en.md) |

Detailed release notes are available in the console. The guides retain each feature's settings, requirements and limitations.

<a id="nav-origin-and-license"></a>
<a id="origin-and-license"></a>

## Origin and contributing

Ether-Remix is a fork of [Ether](https://github.com/braedonsaunders/ether) by Braedon Saunders, licensed under **GPL-3.0**.

Thanks to **[JukeBox](https://mdblist.com/lists/jukebox8786/)** for the MDBList lists used in quick setup and automatic selections.

Inspirations: [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) and [Silo Server](https://github.com/Silo-Server/silo-server). Tracking integration: [Scrob](https://github.com/Aerya/scrob). XDCC source: [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) by CaptainEpix. FTP / SFTP source: [pkg/sftp](https://github.com/pkg/sftp), [jlaffaye/ftp](https://github.com/jlaffaye/ftp). Video metadata uses [TMDB](https://www.themoviedb.org/), without endorsement or certification by TMDB.

[License](LICENSE) · [Legal information](LEGAL.md) · [Third-party notices](THIRD_PARTY_NOTICES.md)

Contributions use a pull request to `main`, with required CI and secret detection. PRs authored by `kesurof` from `kesurof/Ether-Remix` merge automatically after validation. [Contribution and synchronization rules](docs/pse/FORK.fr.md).

The Remix-Media logo identifies the console; the version number and Remix footer link open this repository, alongside the Ether credit.
