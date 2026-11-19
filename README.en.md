<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>

# Ether-Remix

**Movies, shows, anime and music in the applications you already use.**

Ether-Remix is an on-demand media server based on [Ether](https://github.com/braedonsaunders/ether). It brings together remote sources and local files, with a French-oriented catalogue, custom libraries and per-viewer tracking. You provide the sources and accounts required for playback.

**[Français](README.md) · English** · Docker **amd64 / arm64** · **GPL-3.0**

[Installation](#get-started) · [Features](#features) · [Apps](#apps) · [Documentation](#documentation)

<a id="nav-remix-features"></a>
<a id="features"></a>

<a id="nav-movies-shows-and-anime-your-way"></a>
<a id="nav-your-local-movies-and-personal-videos"></a>
<a id="alldebrid-on-a-vps-mediaflow-and-gluetun"></a>
<a id="nav-alldebrid-on-a-vps-mediaflow-and-gluetun"></a>
<a id="nav-external-subtitles-and-translation-with-submaker"></a>
<a id="nav-personal-libraries-and-collections"></a>
<a id="nav-music-in-your-applications"></a>
<a id="nav-your-playlists-and-new-discoveries"></a>
<a id="nav-statistics-and-tracking"></a>
<a id="nav-an-interface-that-fits-your-habits"></a>
<a id="nav-ether-extended-by-remix"></a>

## What Remix does

| Use | Features |
| --- | --- |
| Movies, shows and anime | French/MULTi preferences, quality rules, catalogue filters, subtitled exceptions and SubMaker subtitles. |
| Video sources | Debrid, Usenet, Stremio addons, Prowlarr/Jackett/NZBHydra2, StreamFusion Reborn database and SeerrCatalog. Optional torrent clients, XDCC and FTP/SFTP. |
| Local files | Video and music from folders or mounted network shares, without moving or deleting originals. |
| Libraries and home | Collections, JukeBox quick setup, Xperience imports, layout templates, posters and custom cards. Sharing respects each viewer’s permissions. |
| Music | Deezer catalogue, enabled music sources, local files and iBroadcast; access through Plexamp, Jellyfin or Subsonic, with personal playlists. |
| Tracking | Per-viewer progress and history, video and music activity, Scrob, Last.fm and source statistics. |

<a id="nav-unified-sources-and-searches-ahead-of-playback"></a>

## One release index

Searches and imports retain references meeting active Remix criteria in a shared index. Rejected links are not stored; old links are cleaned up after profiles and exceptions are refreshed. At playback, Remix offers references that are still usable, applies language and quality rules, then searches other sources when necessary. Movie or episode identity is still verified; a stored reference does not guarantee availability.

Under **Remix → Indexing**, a single switch, **Automatically search for releases**, prepares catalogue titles in the background. Choose sources on the same page; pace and budgets live in advanced settings. Disabling automatic search preserves the index, browsing and playback reuse.

Background search does not download or unlock files. A remote addon may still make its own calls. For shows, the initial pass covers **S01E01**: other episodes are retained as they are searched, without implying a complete season. [How the index works](docs/pse/RELEASE_LIBRARY.en.md).

Verified AllDebrid files can take priority when they meet the profile. Quotas pause affected accounts, fallback attempts are capped, and link renewal can be configured to avoid unnecessary calls. [AllDebrid and proxies](docs/alldebrid-mediaflow.md).

<a id="nav-compatible-apps"></a>
<a id="apps"></a>

## Applications

| Access | Example clients |
| --- | --- |
| Plex | Plex apps, Plexamp and Kodi with PM4K. |
| Jellyfin / Emby | Infuse, Swiftfin, Findroid, Streamyfin and compatible TV clients. Emby for TV 2.x on Apple TV is not supported. |
| Silo | Silo clients on desktop, mobile and TV. |
| Stremio | Personal addon; requires an HTTPS address. |
| WebDAV | Infuse, VLC, Kodi and rclone. |
| IPTV / local network | M3U, XMLTV, Xtream and DLNA, depending on enabled access methods. |
| Subsonic | Symfonium, Feishin, Supersonic, DSub and other compatible clients. |

Enable access under **Console → Applications**. Each viewer finds personal links and credentials under **Your account**. Available features depend on the client.

<a id="nav-get-started"></a>
<a id="get-started"></a>

## Get started

1. Install the public image `ghcr.io/aerya/ether-remix:latest` using the [Compose in the guide](docs/pse/GUIDE.en.md). Persist both `/config` and `/cache`.
2. Open `http://<host>:32400/ether/` and follow the setup wizard. Set `ETHER_PORT` to change the port.
3. Add accounts and providers under **Remix → Sources**, then choose languages and quality under **Playback**.
4. Enable automatic search under **Indexing** to prepare the catalogue ahead of time. Browse results under **Releases**.
5. Enable your applications and connect viewers. For token-based Subsonic clients, create music credentials under **Content → Music → Music clients**.

When migrating from Ether, back up `/config`, stop the previous server and reuse its volumes. Use HTTPS for remote access. Torrent and XDCC sources may download to disk; FTP/SFTP also supports remote playback without a full copy. Read the relevant guide before enabling them.

<a id="nav-documentation"></a>
<a id="documentation"></a>

## Documentation

| Need | Guide |
| --- | --- |
| Installation and configuration | [User guide](docs/pse/GUIDE.en.md) |
| Catalogue, filters and priorities | [Catalogue](docs/pse/CATALOGUE.en.md) |
| References, retention and indexed episodes | [Release index](docs/pse/RELEASE_LIBRARY.en.md) |
| Libraries, home and sharing | [Layouts](docs/pse/GUIDE.en.md) |
| StreamFusion Reborn imports | [StreamFusion](docs/pse/STREAMFUSION.fr.md) (French) |
| SeerrCatalog availability | [SeerrCatalog](docs/pse/SEERRCATALOG.en.md) |
| AllDebrid, MediaFlow and proxies | [Networking](docs/alldebrid-mediaflow.md) |
| Subtitles and translation | [SubMaker](docs/pse/SUBMAKER.en.md) |
| Optional sources | [Torrent](docs/pse/TORRENT.en.md) · [XDCC](docs/pse/XDCC.en.md) · [FTP/SFTP](docs/pse/FTP.en.md) |
| Local music and iBroadcast | [User guide](docs/pse/GUIDE.en.md) |

Detailed release notes are available in the console. The guides describe settings and their limits; this README introduces the main uses.

<a id="nav-origin-and-license"></a>

## Origin and license

Ether-Remix is a fork of [Ether](https://github.com/braedonsaunders/ether) by Braedon Saunders, licensed under **GPL-3.0**.

Thanks to **[JukeBox](https://mdblist.com/lists/jukebox8786/)** for the MDBList lists used in quick setup and automatic selections.

Inspirations: [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) and [Silo Server](https://github.com/Silo-Server/silo-server). Tracking integration: [Scrob](https://github.com/Aerya/scrob). XDCC source: [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) by CaptainEpix. FTP / SFTP source: [pkg/sftp](https://github.com/pkg/sftp), [jlaffaye/ftp](https://github.com/jlaffaye/ftp). Video metadata uses [TMDB](https://www.themoviedb.org/), without endorsement or certification by TMDB.

[License](LICENSE) · [Legal information](LEGAL.md) · [Third-party notices](THIRD_PARTY_NOTICES.md)
