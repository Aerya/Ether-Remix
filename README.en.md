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

## Ether at a glance

Ether is an **on-demand** media server. It shows the whole TMDB catalogue inside the apps you already use, without downloading anything in advance. When you play a title, it picks the best release from your debrid accounts and Usenet providers, checks that it works, then streams it. This workflow needs no Sonarr, Radarr, download client or mount to scan. It can run alongside an existing Plex, Jellyfin or Emby server.

Ether adapts to each app: it is a Plex server to Plex apps, a Jellyfin server to Infuse or Swiftfin, and so on. Every viewer has their own account and personal Home, and resumes where they left off from one app to another. Stream limits and parental controls apply everywhere.

Customize Plex Home in `/ether/home`: select the viewer and device type when comparing the preview with a client. Movie and TV library recommendations stay on their own pages instead of being appended wholesale to Home by Plex’s per-library requests.

## Remix at a glance

**Layout templates** save Home, libraries and collection presentation: capture your current setup, duplicate A/B/C variants and reapply them with undo. Plex theme libraries can combine local folders and indexed sources for everyone or selected viewers; private content remains filtered per viewer. [Layouts and sharing guide](docs/pse/LAYOUTS.en.md).

Ether-Remix retains Ether’s server and compatible applications, adding a French-oriented catalogue and language and quality settings. Depending on your choices, Home and collections favour French/MULTi titles or releases matching your criteria. Background indexing finds sources ahead of playback; it does not download videos.

Your **local videos**, movies, episodes and personal recordings join your libraries from host folders or network mounts. Remix looks for a match in its catalogue, then TMDB, and keeps a local presentation when there is no reliable match. The **Sources → Local files** view reports missing files, readability problems and potential duplicates without deleting your media.

**Optional sources** extend debrid and Usenet: torrent clients, XDCC through Grabscher and FTP / SFTP servers. Torrent and XDCC write files to disk; FTP / SFTP also supports remote playback without a complete copy, with Ether’s normal cache. These sources are off by default.

For **music**, bring together the Deezer catalogue, your audio files and your iBroadcast accounts in Plexamp, Jellyfin clients or Subsonic apps. Enabled music sources and debrid accounts provide playback; playlists can be browsed, imported and managed per user. Local folders and iBroadcast accounts are private by default, with optional sharing.

**JukeBox** quick setup helps create libraries and collections. SubMaker subtitles, Scrob and Last.fm tracking, statistics and the French console round out these features.

**Contents**

- [Compatible apps](#nav-compatible-apps)
- [Remix Features](#nav-remix-features)
  - [Movies, shows and anime your way](#nav-movies-shows-and-anime-your-way)
  - [Your local movies and personal videos](#nav-your-local-movies-and-personal-videos)
  - [Unified sources and searches ahead of playback](#nav-unified-sources-and-searches-ahead-of-playback)
  - [AllDebrid on a VPS: MediaFlow and Gluetun](#nav-alldebrid-on-a-vps-mediaflow-and-gluetun)
  - [External subtitles and translation with SubMaker](#nav-external-subtitles-and-translation-with-submaker)
  - [Personal libraries and collections](#nav-personal-libraries-and-collections)
  - [Music in your applications](#nav-music-in-your-applications)
    - [Your playlists and new discoveries](#nav-your-playlists-and-new-discoveries)
  - [Statistics and tracking](#nav-statistics-and-tracking)
  - [An interface that fits your habits](#nav-an-interface-that-fits-your-habits)
  - [Ether, extended by Remix](#nav-ether-extended-by-remix)
- [Get started](#nav-get-started)
- [Documentation](#nav-documentation)
- [Origin and license](#nav-origin-and-license)

<a id="nav-compatible-apps"></a>

## Compatible apps

| Access                                        | Apps                                                                                                                                                                              |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Stored releases, browsing and beta monitoring | [Release library](docs/pse/RELEASE_LIBRARY.en.md)                                                                                                                                 | Media/release associations are checked before indexing using source-reported identifiers, alternate titles, year and episode. Unconfirmed old entries are preserved in quarantine, outside search and reuse. Global search and category browsing use queries suited to large indexes.
| **Plex**                                      | Official apps: Apple TV, iOS, Android, Android TV / Shield, Fire TV, Roku, PlayStation, Xbox, smart TVs (LG, Samsung, VIDAA), desktop and Web. Kodi through Plex for Kodi (PM4K). |
| **Jellyfin**                                  | Infuse, Swiftfin, Findroid, Streamyfin, Jellyfin for Android TV, JellyCon (Kodi), Jellyfin for Roku.                                                                              |
| **Emby**                                      | Emby apps (Android TV, iOS, Android, Roku, smart TVs) and Infuse. Emby for TV 2.x on Apple TV is not supported.                                                                   |
| **Silo**                                      | Apple TV, iOS, macOS, Android, Android TV.                                                                                                                                        |
| **Stremio**                                   | Any Stremio app, through a personal addon. An HTTPS address is required.                                                                                                          |
| **Files (WebDAV)**                            | Infuse, VLC, Kodi, rclone.                                                                                                                                                        |
| **M3U & XMLTV**                               | Channels DVR, TiviMate and IPTV players.                                                                                                                                          |
| **Xtream**                                    | TiviMate, Televizo, IPTV Smarters… Off by default.                                                                                                                                |
| **DLNA**                                      | TVs and players on the local network.                                                                                                                                             |
| **Music** _(Remix)_                           | Plex and Plexamp, Jellyfin clients (Finamp…), Subsonic apps: Symfonium, Feishin, Supersonic, DSub…                                                                                |

Turn access on under **Console → Apps**. Each viewer finds their links and app passwords on their **Your account** page.

<a id="nav-remix-features"></a>

## Remix Features

<a id="nav-movies-shows-and-anime-your-way"></a>

### ![](https://api.iconify.design/lucide/clapperboard.svg?color=%2314b8a6&width=24&height=24) Movies, shows and anime your way

- **Direct Stream quality**: new configurations no longer offer encoded variants by default when video can be copied. Under Playback → Transcoding, enable them for limited connections or also disable accelerated start to preserve the picture from the beginning. Saved preferences are retained.
- **Quality profiles**: formats, codecs, resolutions, HDR/Dolby Vision, size, languages and regex, globally or per library/collection. Choose profiles or simple criteria without stacking them, including detailed formats, unknown resolution, original language and declared debrid cache. Import AIOStreams from JSON or a manifest/saved source with the configuration password, preview and edit before saving (variants are not reproduced).
- **French-oriented catalogue**: choose languages and variants — VFF, VFQ, VFi, MULTi… — and cache requirements. A movie not yet available in French shows neither on Home (Trending included) nor in collections.
- **Playable-only catalogue**: show titles with a minimum number of releases matching your preferences. The rule also covers Home and collections; search, watchlists, continue watching and upcoming collections stay complete.
- **Targeted exceptions**: allow other languages for selected collections or libraries without changing your general preferences. Titles without a French version only show in their library or collection, never in Movies, Shows or the home screen.
- **Release-date sorting**: Movies and Shows open newest first in Plex, Jellyfin and Emby (`PSE_LIBRARY_SORT` setting). A sort chosen in the app still wins.
- **French subtitles on demand**: add the titles you want; each addition triggers a priority search when indexing is enabled. Optionally, SubMaker prepares their French subtitles, and each title shows its subtitle state.
- **Anime**: create TMDB-based libraries and collections with your French/MULTi preferences and any Stremio addons you choose.
- **Release matching**: recognizes accents, apostrophes and alternative titles; part numbers must be corroborated by the catalogue.

<a id="nav-your-local-movies-and-personal-videos"></a>

### ![](https://api.iconify.design/lucide/folder-open.svg?color=%2314b8a6&width=24&height=24) Your local movies and personal videos

- **Local media health**: section in **Sources → Local files**, loaded when first opened, for folder diagnostics, missing/unidentified files, potential duplicates and sizes per folder. Entries remain in the catalogue when a mount becomes empty or inaccessible; no files are deleted.
- **Host folders or network mounts**: expose them through read-only Docker volumes. Scan on demand and hourly, without moving or deleting files.
- **Catalogue matching**: search existing Remix entries first, then TMDB; identified movies and episodes reuse their entries. Without a reliable match, personal videos retain their local presentation.
- **Libraries and collections per folder**: link video folders to libraries and custom collections under Sources → Local files. Private by default with optional sharing; destinations respect folder permissions. [Setup and formats](docs/pse/GUIDE.en.md#local-media-and-ibroadcast).

<a id="nav-unified-sources-and-searches-ahead-of-playback"></a>

### ![](https://api.iconify.design/lucide/radar.svg?color=%2314b8a6&width=24&height=24) Unified sources and searches ahead of playback

Indexers (Prowlarr, Jackett, NZBHydra2) find releases. Configure the appropriate playback path: a debrid account, a BitTorrent client or a Stremio addon such as Seedstrem. Seedstrem uses its own torrent client. For Usenet, Remix reads through its NNTP engine and your Usenet providers; SABnzbd/NZBGet are not integrated external download clients.

- **IPTV and Xtream VOD**: live channels, EPG and imports of the provider’s movies and series.
- **Shared Ether and Remix configuration**: Stremio addons, indexers, Usenet services, debrid accounts, music sources and IPTV in one place.
- **Self-hosted Seedstrem**: add its manifest from Sources → Stremio; the addon list opens after saving so you can find and edit it. Prowlarr search and playback during download via qBittorrent or Deluge; files stay on the Seedstrem host, with no volume to mount in Remix. Downloads start only for playback; optional last-resort playback keeps searches available for indexing. [Setup and connection](docs/pse/SEEDSTREM.en.md).
- **Stremio catalogues**: background manual synchronization, visible status and retention of the last import on failure.
- **Quick start**: the **Overview → Quick start** view guides initial setup — sources, video indexing, languages and quality, statistics and clients. It opens by default while indexing is disabled; a reminder then points to it. The release library beta can also be enabled and saved here.
- **Remix console**: settings under `/ether/remix`, collapsible sections closed by default; iBroadcast sits alongside music providers, with Subsonic information under Content → Music.
- **Background indexing**: search releases through your addons and Prowlarr, Jackett or NZBHydra2 indexers before playback starts.
- **Temporary indexer outages**: reuse results expired for less than 24 hours without renewing their validity or bypassing playback checks. Access errors and quotas remain enforced.
- **Controlled searches**: daily per-source budgets, pauses during playback, collection/library priorities and immediate manual searches.
- **Search from Plex or Jellyfin**: top results never searched or previously without releases receive priority, subject to their retry interval.
- **SeerrCatalog availability**: import confirmed availability and recent sources from [SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog), without another search for the playable-only catalogue. Confirmation persists until deletion; expired playback sources are searched again when needed. A title found only with French subtitles (SeerrCatalog fallback) goes to the **VOSTFR** library and the **SeerrCatalog** collections, never to Movies or Shows.
- **Searches for SeerrCatalog**: authorize complementary searches through Remix sources, including Prowlarr, using a dedicated token. Search and availability sharing are independent features grouped in the SeerrCatalog settings.
- **Optional torrent client**: send Prowlarr or Jackett releases to one to three qBittorrent or rTorrent/ruTorrent clients, with ratio and seeding time per client and per tracker, cross-seed and a free-space alert. The client can be remote, but Remix must see its download folder (same machine or NFS/SMB share) to play the files. Off by default; debrid accounts stay cached-only. [Details](docs/pse/TORRENT.en.md)
- **Optional XDCC source**: search movies and episodes from the XDCC bots your [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) follows, then play them during the transfer, download only, or both. Bot and transfer states, cache space reclaimed before a new transfer, limits enforced for simultaneous playback starts and kept files protected from cleanup. Grabscher stays a separate service, never exposed to the Internet. Off by default: files are written to disk. [Setup](docs/pse/XDCC.en.md)
- **Optional FTP / SFTP source**: remote movies and episodes, local index and direct playback with seeking. SFTP, FTPS and FTP; optional complete download or streaming with a kept copy. Off by default; remote playback without a full copy is the default mode (Ether’s normal cache still applies). [Setup and storage](docs/pse/FTP.en.md)
- **StreamFusion Reborn database**: import French, MULTi or VOSTFR releases and search movies, episodes and packs directly in your read-only database. Verified Alkodiques files are imported and played through their AllDebrid file links without adding magnets; a configured AllDebrid account is still required. For ordinary torrents, debrid cache evidence expires; playback rechecks availability and retains other sources as fallbacks. Titles added to the catalogue later are also caught up. They go ahead of other sources when they fit the profile (`resolver.verifiedFiles` setting: `first` by default, `tie` or `off`). When AllDebrid reports its daily quota reached, the account is paused for 1 hour then probed with a single request (one test per hour per node in a cluster); debrid-dependent playback fallbacks are dropped when no account is usable (quota or rejected key on every provider) and attempts are capped (`resolver.failoverAttempts`, 1 to 10, 4 by default). The Debrid page then shows “Quota reached” with the time of the next try, and clears the last error at the first success. [Setup, in French](docs/pse/STREAMFUSION.fr.md). An explicit cache miss on a new AllDebrid addition avoids repeating the request on its other accounts; account-specific errors and incomplete existing torrents retain backup accounts.

**Release library (Beta, disabled by default)**: enable encrypted reference retention under Remix → Indexing to reuse candidates during playback and fallback. Catalogue details show indexed releases sortable by resolution, size, codecs, source, languages and dates. Alkodiques files retain their AllDebrid playback path without magnets. [Behavior and limits](docs/pse/RELEASE_LIBRARY.en.md). Names and years must agree even when the source reports the expected identifier; ambiguous answers are rejected. A new audit and recovery queue gradually collect references for previously indexed titles.

**Indexing → Releases**: global media and release search, Movies/Series/Anime categories, calendar periods and custom dates, grouped series and releases expanded below each media without switching views. Scoped loading preserves the list during refreshes and errors. [Guide](docs/pse/RELEASE_LIBRARY.en.md).

When enabled, the **Indexing → Releases** view lets you browse content and releases and monitor active searches and recent results. Counters also appear in **Overview**. Enable the option in **Overview → Quick start** or **Indexing**, then save.

<a id="alldebrid-on-a-vps-mediaflow-and-gluetun"></a>
<a id="nav-alldebrid-on-a-vps-mediaflow-and-gluetun"></a>

### ![](https://api.iconify.design/lucide/network.svg?color=%2314b8a6&width=24&height=24) AllDebrid on a VPS: MediaFlow and Gluetun

AllDebrid may block VPS/server, datacenter and some VPN exit IPs, including **Cloudflare WARP**. Access from several countries or interpreted as account sharing may lead to suspension or a permanent account ban.

Under **Sources → Debrid** (`/ether/remix/sources?source=debrid`), enable **Use MediaFlow for AllDebrid**, then enter your instance URL and API password. AllDebrid API calls and files fetched by Remix use MediaFlow, without direct fallback on failure. Disabled by default; requires an existing MediaFlow instance and does not install containers.

On a VPS, route **MediaFlow through Gluetun**, connected to an accepted VPN or your home tunnel. MediaFlow alone keeps the server’s exit IP. Check the VPN providers offered in [your AllDebrid account](https://alldebrid.fr/account/). [AirVPN is one option — affiliate link](https://airvpn.org/?referred_by=483746); being listed does not guarantee acceptance of every exit IP.

Use **the same exit for every tool on this machine** accessing AllDebrid, including workers, schedulers and cleanup scripts. Tools without MediaFlow integration can use the associated Gluetun proxy. Calls made by externally hosted addons are outside Remix’s control. [Setup and checks](docs/alldebrid-mediaflow.md). The panel links to the MediaFlow + Gluetun Compose and can test AllDebrid through the saved connection.

**Ready-to-use Compose files:** [Ether-Remix](docker-compose.example.yml) · [MediaFlow + Gluetun](docker-compose.mediaflow-gluetun.yml) · [Prerequisites and setup](docs/alldebrid-mediaflow.md).

An existing HTTP/HTTPS/SOCKS5 proxy (such as SSDV2 Proxy Manager) can also be used directly under **Settings → General → Source proxy**, without MediaFlow. Paste its complete URL: credentials are moved into separate fields, then save. Remix must be able to reach the proxy on its network. Enable Debrid routing and, for addon streams, Stremio routing.

**Per-source proxies**: each debrid account can use its own HTTP/HTTPS/SOCKS5 proxy for API calls and video or music playback. Under **Sources → Stremio**, choose separate proxies for requests (manifest, searches) and HTTP streams. Full authenticated URLs can be pasted into either form. A dedicated proxy takes precedence over MediaFlow and the global proxy, without direct fallback; disabling it restores global behavior. Torrents resolved by Remix use the debrid account’s proxy.

<a id="nav-external-subtitles-and-translation-with-submaker"></a>

### ![](https://api.iconify.design/lucide/captions.svg?color=%2314b8a6&width=24&height=24) External subtitles and translation with SubMaker

- **Choose your instance**: use [public](https://submaker.elfhosted.com/configure) or self-hosted [SubMaker](https://github.com/xtremexq/StremioSubMaker) with a personal manifest; the integration is disabled by default.
- **Languages and scopes**: choose source/target languages, English → French by default, and allow processing globally or per library/collection.
- **Controlled preparation**: find an SRT matching the release, translate when necessary, and manage processing with a queue, cache and daily budget. Only complete, validated results are accepted; translations must retain timestamps and cue counts.
- **Playback and SeerrCatalog**: attach the prepared SRT to its matching release and send it to SeerrCatalog through authenticated search. A pending translation does not confirm availability; restart an already open playback after preparation.
- **Subtitled release first**: with equal quality, audio language and format score, playback picks the release whose SRT is already ready over one with more seeders or a larger file. [Setup and limitations](docs/pse/SUBMAKER.en.md).

<a id="nav-personal-libraries-and-collections"></a>

### ![](https://api.iconify.design/lucide/library.svg?color=%2314b8a6&width=24&height=24) Personal libraries and collections

**Nuvio-inspired navigation cards — optional BETA**: Country services load automatically. The editor opens beneath the card; GitHub, ThePosterDB and ACdb galleries let you search and select artwork directly in Remix. MediUX covers public genre galleries (English search), not studios/services. Fanart.tv has no integrated navigation-card search; file/URL import remains available. search TMDB studios and labels, select services from multiple countries and genres. Preview each card with automatic artwork, a GitHub gallery image or an imported file/URL. Services can form country groups on Plex and Jellyfin/Emby home screens; rendering depends on the client and the previous home remains restorable. [Guide](docs/pse/GUIDE.en.md#nuvio-inspired-navigation-cards).

**Artwork packs**: preview a Nuvio Assets, Kometa, nichtlegacy, Kaptain, FR-Badges, UltraMax or TVFlix/Nuvio set, then apply its matches to selected cards. Custom artwork is protected by default, unmatched cards stay unchanged, and ambiguous variants can be selected in a paginated review panel. Import a JSON file (up to 4 MB) or its public HTTPS URL: only names and covers are read, never addons/catalogues. Save with “Apply cards to my home screens”. Individual customization and gallery search remain available.

**Movie and TV posters**: BetterPosters already has an integration; its configurator is linked from Services. Pictorium and TOP Posters have their own optional accounts using the service’s poster URL template. Private parameters stay server-side and failures fall back to TMDB. Pictorium can run elsewhere: using a public instance does not require a local installation.

A **style for multiple cards** can be prepared from one gallery: choose cards, search their artwork, filter by creator, set or folder and select unique matches in one click. Review variants before bulk application; cards without a choice keep their artwork. Available metadata varies by source, and the same creator does not guarantee the same style. Searches are limited to 30 cards per batch; nothing is saved until “Apply cards to my home screens”.

- **Xperience import**: in Content, paste your [Xperience](https://xperience-app.com/) manifest, select catalogues and parameters, then create libraries, collections or both. Source ordering and Remix filters are preserved; separate manifest and new-collection refresh intervals are configurable from 1 to 168 hours. Libraries fill in the background; Plex libraries update without restarting Ether. [How to use it](docs/pse/GUIDE.en.md#xperience-import).
- **French quick setup**: create Movies, Shows and Anime libraries from three suggested JukeBox lists, or choose other lists (documentaries, studios…). Collection-only mode, daily refresh and newest-release-first sorting. Libraries publish to Plex, Jellyfin and Emby; your sources and language settings still apply. [Instructions](docs/pse/GUIDE.en.md#jukebox-quick-setup).
- Organize Plex libraries by **service, studio, anime, documentaries, concerts or live shows**. Concerts combine recorded-performance keywords with the Music genre to reduce fiction matches.
- Build their rows with TMDB collections, imported Stremio catalogues and ready-made presets.
- Set collection refresh intervals from **1 to 168 hours**, independently of release searches.
- Adjust when movies appear before or after their digital/physical release with a **day offset**.
- Benefit from batched poster badge preparation in Plex and Jellyfin, per-viewer watch state and exact counts.
- Show posters faster: with a poster provider (PostersPlus, XRDB, RPDB…), Plex keeps each poster instead of asking for it again on every screen, and you set the refresh delay. Jellyfin and Emby receive the poster at the requested size, with no wait. Large resolutions requested for detail views are preserved.

<a id="nav-music-in-your-applications"></a>

### ![](https://api.iconify.design/lucide/headphones.svg?color=%2314b8a6&width=24&height=24) Music in your applications

- **Plex / Jellyfin music playback**: albums and artists share the same cache safely when switching between clients.
- Browse the **public Deezer catalogue** from Plex/Plexamp, Jellyfin clients or a Subsonic application: Symfonium, Feishin, Supersonic, DSub…
- Listen through **Audius, Eclipse addons, DMM with Real-Debrid or your AllDebrid, Real-Debrid and TorBox accounts**, depending on enabled sources.
- Public DMM instance: [catalogue](https://debridmediamanager.com/albums). Configure its address in Sources → Music streaming → DMM / Real-Debrid to use a self-hosted instance with its own music catalogue and API.
- **Local music**: play your files in their original format, with artists, albums and tags, from host folders or network mounts exposed through read-only Docker volumes. Scan on demand and hourly, without moving or deleting files.
- **iBroadcast**: each viewer can connect multiple accounts through OAuth device authorization and browse their libraries. Read-only access, MP3 128 kbps playback; optional playlist import.
- **Private access and sharing**: music folders and iBroadcast accounts are private by default, optionally shared with selected viewers or everyone. Authorized sources appear together in Music. [Setup and formats](docs/pse/GUIDE.en.md#local-media-and-ibroadcast).
- Choose source order: when a source fails, Remix tries the next one. External HiFi API mode is used only when no other music source is active.
- Keep **per-user favourites and listening history**. Available quality depends on the sources.
- **Music activity**: current playback reported by Plex, Jellyfin/Emby and Subsonic; one chronological history for video and music; a **Listening** tab with daily trends, listening times and track, artist, album and listener rankings, filterable by period and user.

<a id="nav-your-playlists-and-new-discoveries"></a>

#### ![](https://api.iconify.design/lucide/list-music.svg?color=%2314b8a6&width=24&height=24) Your playlists and new discoveries

- **Browse Deezer playlists** and connect your Spotify account to find playlists you can access.
- **Import by URL** from Deezer, Spotify or ListenBrainz, or from CSV/XSPF files.
- Review the preview, choose the destination viewer and track matched or missing tracks. Imports create personal copies without replacing other playlists.
- Synchronize public Deezer profiles, Last.fm and ListenBrainz; manage personal playlists through Subsonic clients.
- **iBroadcast playlists**: enable import for each connected account; track order and repetitions are preserved, with updates without duplicates and no changes sent to iBroadcast.
- Explore **country charts, recent plays, most-played tracks and new releases from followed artists**. Genres, similar artists, radios and mixes depend on the client.

<a id="nav-statistics-and-tracking"></a>

### ![](https://api.iconify.design/lucide/chart-no-axes-combined.svg?color=%2314b8a6&width=24&height=24) Statistics and tracking

- **Release library**: when beta is enabled, statistics show stored references and their state; **Indexing → Releases** opens content and active searches.
- Remix opens on statistics: coloured progress rings, a thin-bar history chart and live per-source searches, errors, quotas, durations and results.
- View history **by day, week, month or year**, as period or cumulative values, filtered by movies, shows, anime and sources.
- Compare the local catalogue with TMDB totals and review operations in the activity log.
- Unfinished series stay in Continue Watching even after months without playback, with their next episode. Items are ordered by last viewing; Plex retains its 40-item limit and viewer hide preferences. Selection reads only the current viewer’s history, without scanning other accounts’ history.
- Full Plex Media Server history import also fills recent activity using original dates, without duplicates. Run the full-history import again to complete an earlier import; newer local watched states remain authoritative.
- Connect Remix to **Scrob as a Jellyfin server** and choose sync directions and schedules in Scrob: watched status and ratings, one way or both. The real-time event webhook (**60-second** timeout) and history-only libraries are optional. Stops and playback changes are relayed per session without duplicates.
- Connect **Last.fm** per user for music scrobbling and find listening statistics in Overview.

<a id="nav-an-interface-that-fits-your-habits"></a>

### ![](https://api.iconify.design/lucide/palette.svg?color=%2314b8a6&width=24&height=24) An interface that fits your habits

- Seven tabs: **Overview** (quick start, activity, sources, usage), **Sources** (including local media health), **Indexing** (settings and releases), **Playback** (priorities, quality, subtitles), **Content**, **Visuals** (Nuvio cards, packs, poster refresh) and **Tracking**. Old links and bookmarks are redirected.
- Automatic indexing of enabled Stremio addons or a custom selection, including none; Prowlarr/Jackett instance filters and catalogue, searched, with-release and shown counts for each prioritized library or collection.
- Sources grouped by family, category/content sorting, on-demand debrid key display and collapsible sections that preserve your input. Choose palettes in **Appearance**.
- Six palettes: **Ether, Nord, Catppuccin, Solarized, Gruvbox and Rosé Pine**, each with light and dark modes, instant preview and per-browser persistence.

<a id="nav-ether-extended-by-remix"></a>

### ![](https://api.iconify.design/lucide/refresh-cw.svg?color=%2314b8a6&width=24&height=24) Ether, extended by Remix

- **Display name**: rename a viewer in the console for statistics and activity, even with a Plex account. Sign-in does not change.
- Daily synchronization with the original project, checks before merging and failure reporting. Ether’s catalogue-source libraries, IPTV improvements and release ranking coexist with Remix filters and sources.
- Docker images for **amd64 and arm64**, with update notes in the console.

<a id="nav-get-started"></a>

## Get started

1. **Install** `ghcr.io/aerya/ether-remix:latest` using the [provided Compose file](docs/pse/GUIDE.en.md#installation), through Dockge-Enhanced. The image is public; no GHCR login is required.
2. **Open the console** at `http://<host>:32400/ether/` and follow the setup assistant. Set the port with `ETHER_PORT`.
3. **Configure your sources**—debrid, Usenet, indexers and addons—under **Ether-Remix → Sources**, then enable any optional sources you need (torrent, XDCC, FTP / SFTP). Set languages, content and libraries in their tabs; JukeBox quick setup can create your first libraries.
4. **Connect your clients**. For Arpeggi and token-authenticated Subsonic clients, create access under **Content → Music → Music clients**: server address, dedicated music username and password, revocable per viewer. Local accounts still work with **legacy** authentication. Use HTTPS remotely.

Migrating from Ether? The data format is preserved: back up `/config`, stop the old server and reuse its `/config` and `/cache` volumes.

<a id="nav-documentation"></a>

## Documentation

| Need                                                                | Guide                                                                         |
| ------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| AllDebrid on a VPS, MediaFlow + Gluetun and per-source proxies      | [Networking and proxies (French)](docs/alldebrid-mediaflow.md)                |
| Installation, settings, music sources, playlists, Scrob and clients | [Configuration guide](docs/pse/GUIDE.en.md)                                   |
| Title visibility, priorities and search intervals                   | [Catalogue and indexing](docs/pse/CATALOGUE.en.md)                            |
| Availability sharing and complementary searches                     | [SeerrCatalog integration](docs/pse/SEERRCATALOG.en.md)                       |
| Read-only import from a StreamFusion Reborn database                | [StreamFusion Reborn, in French](docs/pse/STREAMFUSION.fr.md)                 |
| Public or self-hosted instance, languages and translation limits    | [SubMaker](docs/pse/SUBMAKER.en.md)                                           |
| Torrent clients and download storage                                | [Torrent client](docs/pse/TORRENT.en.md)                                      |
| Videos, local music and iBroadcast accounts                         | [Local media and iBroadcast](docs/pse/GUIDE.en.md#local-media-and-ibroadcast) |
| XDCC bots via XDCC Grabscher: Docker, VPN, streaming and limits     | [XDCC source](docs/pse/XDCC.en.md)                                            |
| FTP / SFTP: connection, playback and storage                        | [FTP / SFTP source](docs/pse/FTP.en.md)                                       |

<a id="nav-origin-and-license"></a>

## Origin and license

Ether-Remix is a fork of [Ether](https://github.com/braedonsaunders/ether) by Braedon Saunders, licensed under **GPL-3.0**.

Thanks to **[JukeBox](https://mdblist.com/lists/jukebox8786/)** for the MDBList lists used in quick setup and automatic selections.

Inspirations: [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) and [Silo Server](https://github.com/Silo-Server/silo-server). Tracking integration: [Scrob](https://github.com/Aerya/scrob). XDCC source: [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) by CaptainEpix. FTP / SFTP source: [pkg/sftp](https://github.com/pkg/sftp), [jlaffaye/ftp](https://github.com/jlaffaye/ftp). Video metadata uses [TMDB](https://www.themoviedb.org/), without endorsement or certification by TMDB.

[License](LICENSE) · [Legal information](LEGAL.md) · [Third-party notices](THIRD_PARTY_NOTICES.md)


AllDebrid `MAGNET_TOO_MANY` refusals pause only new additions after repeated refusals; existing torrents and file links remain usable. The release library cannot turn an unknown hash into a file link.

**Link renewal during playback**: by default, when a debrid's media server fails during a read (5xx, cut connection, 429), Remix asks for a new link, or for another provider. On a shared account these requests use up the link quota and can block other users. In **Sources → Debrid**, choose **Keep the link and retry** (`debrid.playbackRelink` = `"replay"`): a file that was started reads to the end on its link, with no call to the provider API. A link that is really dead (401, 403, 404, 410, redirect) is still renewed; in this mode, at most once every 30 seconds per file. The change applies without a restart.
