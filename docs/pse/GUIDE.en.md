# Ether-Remix configuration guide

[Ether-Remix overview](../../README.en.md) · [Français](GUIDE.fr.md)

**Contents**

- [Quick start](#nav-demarrage-rapide)
- [Installation](#nav-installation)
  - [Configuring Ether-Remix from the console](#nav-configuring-ether-remix-from-the-console)
  - [Connecting Scrob](#nav-connecting-scrob)
  - [Connecting a music client (Subsonic)](#nav-connecting-a-music-client-subsonic)
  - [Eclipse community sources](#nav-eclipse-community-sources)
  - [Statistics and source activity](#nav-statistics-and-source-activity)
  - [DMM with Real-Debrid](#nav-dmm-with-real-debrid)
  - [History and TMDB comparison](#nav-history-and-tmdb-comparison)
- [Anime](#nav-anime)
  - [On-demand subtitled exceptions](#nav-on-demand-subtitled-exceptions)
  - [Public instances or self-hosting](#nav-public-instances-or-self-hosting)
- [Settings organization](#nav-settings-organization)
  - [Browsing and poster badges](#nav-browsing-and-poster-badges)
- [Playlists and appearance](#nav-playlists-and-appearance)
- [Plex Home](#plex-home)
- [Seedstrem](#nav-seedstrem)
- [SubMaker](#nav-submaker)
  - [Unified history and Listening](#nav-unified-history-and-listening)
- [Local media and iBroadcast](#nav-local-media-and-ibroadcast)
  - [Video and audio folders](#nav-video-and-audio-folders)
  - [iBroadcast account](#nav-ibroadcast-account)
- [Search caching and source outages](#nav-search-caching-and-source-outages)
  - [Navigation and configuration](#nav-navigation-and-configuration)
- [Nuvio-inspired navigation cards](#nuvio-inspired-navigation-cards)
- [Xperience import](#xperience-import)
- [JukeBox quick setup](#nav-jukebox-quick-setup)
  - [Local media health](#nav-local-media-health)

<a id="nav-demarrage-rapide"></a>
## Quick start

In the console, open **Ether-Remix → Overview → Quick start**. This view opens by default while indexing is disabled, remains available and does not change settings automatically.

1. **Sources**: add, test and enable your providers. An addon supplying its own playback links does not require a second debrid key for searches.
2. **Indexing**: enable and save background indexing to prepare the video catalogue. Automatic mode uses enabled Stremio addons; switch to Custom to choose sources, including Prowlarr/Jackett indexers. An empty custom selection performs no searches. Respect provider quotas.
3. **Playback → Quality**: check criteria and the playable catalogue threshold. An empty catalogue may simply be waiting for matching releases.
4. **Overview → Activity**: check recent searches and searched-title counts. Indexing pauses during playback. Usage statistics are separate.
5. **Content**: customize collections if needed. Music and local files alone do not require video indexing; configure their sources and sharing.
6. **Apps**: enable the client protocol, use the viewer’s credentials and try playback. Subsonic access details are also under **Content → Music**.

<a id="nav-installation"></a>

## Installation

The Docker image is public. No GHCR login or GitHub token is needed to download it.

```yaml
# compose.yaml — deploy through Dockge-Enhanced
services:
  pse:
    image: ghcr.io/aerya/ether-remix:latest
    container_name: accent
    restart: unless-stopped
    network_mode: host            # Plex LAN discovery (GDM) and UPnP
    environment:
      - TZ=Europe/Paris
#     - ETHER_PORT=32450          # if a Plex Media Server already uses 32400
    volumes:
      - ./config:/config          # config.json, database, identity
      - ./cache:/cache            # segment cache
#   devices:
#     - /dev/dri:/dev/dri         # Intel/AMD hardware transcoding (optional)
```

Open **http://&lt;host&gt;:32400/ether/** (or the `ETHER_PORT` port) and follow the first-run assistant.

> **Migrating from Ether:** same data format. Stop Ether, point Ether-Remix's `/config` and `/cache` volumes to the existing directories, start Ether-Remix. Keep a copy of `/config` first.

> **Update notices:** the console may announce new *Ether* versions. In a container Ether never updates itself: ignore the notice, Ether-Remix images are updated by the rolling release (Dockge-Enhanced automatic updates or `docker compose pull`).

<a id="nav-configuring-ether-remix-from-the-console"></a>

### Configuring Ether-Remix from the console

Under **Playback → Transcoding**, adaptive Direct Stream qualities offer encoded variants for limited connections; this is off by default for new configurations. Also disable accelerated start to avoid reducing resolution in the opening segments when video copy is possible. Existing settings are retained; start a new playback session to apply your choices. Scrob Copy buttons copy Server URL, Token and User ID without revealing the token.

Every addition is configured in the console, under **Ether-Remix** (`/ether/remix`): switches, options and the state of each feature (indexed titles, playable titles, music library…). A value saved on that page replaces the environment variable of the same name and applies without a restart; the ↺ button goes back to the variable or the default. The variables below still work for a deployment fully described in `compose.yaml`.

Ether-Remix accepts every Ether environment variable (`ETHER_PORT`, `ETHER_DATA_DIR`, `PUID`/`PGID`…): see [docs/install.md](../install.md). Ether-Remix-specific variables:

References from searches and imports are automatically stored in a shared index, available under **Indexing → Releases**. **Automatically search for releases** adds background searches, disabled by default. The old `PSE_RELEASE_LIBRARY` setting is ignored; turning it off no longer hides references.

| Variable | Default | Effect |
| --- | --- | --- |
| `PSE_NOTIFY_LANGUAGE` | `fr` | Language of owner notifications. `en` keeps Ether's English text. |
| `PSE_INDEXER` | `off` | `on` automatically searches for releases. `off` keeps the index and its use during playback. |
| `PSE_INDEXER_SOURCES` | *(all enabled Stremio addons)* | Comma-separated indexer IDs to query. The only way to include a Prowlarr/Jackett/NZBHydra2 indexer. |
| `PSE_INDEXER_DAILY` | `1000` | Maximum requests per source per day (UTC). |
| `PSE_INDEXER_PACE` | `10s` | Minimum delay between two titles (at least `2s`). |
| `PSE_INDEXER_TYPES` | `movie,show` | Indexed types (`movie`, `show`). A show is searched through its first episode. |
| `PSE_PLAYABLE` | `off` | `on` enables the "playable only" catalogue, with the same scope as `PSE_FRENCH`. |
| `PSE_PLAYABLE_MIN` | `2` | Distinct releases (same name counts once) needed to list a title. |
| `PSE_PLAYABLE_FILTER` | *(none)* | Releases that count, e.g. `lang=fr,multi;min=1080;codec=hevc,h264;cached=1`. Languages: `fr` (any French dub: FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF, VF2), `vff`, `vfq`/`qc`, `multi`, `en`, `vostfr` (French subtitles announced), `vo` (no dub announced)… Changing the filter recomputes the catalogue at startup, without reindexing. |
| `PSE_POSTER_REFRESH_HOURS` | `24` | With a poster provider (PostersPlus, XRDB, RPDB…), delay in hours (1 to 168) after which apps fetch its posters again. Ether used 6 hours. |
| `PSE_LIBRARY_SORT` | `release` | Movies and Shows library order when opened: `release` (release date, newest first) or `title` (A → Z). A sort chosen in the app still wins. |
| `PSE_FRENCH` | `off` | `on` enables the French-language catalogue: Movies and Shows pages, Home rows (including Trending), collections and similar titles. Upcoming collections, the watchlist and Continue Watching are left alone. |
| `PSE_FRENCH_COUNTRIES` | `FR` | Countries whose digital or physical release counts (ISO codes). `FR,CA` adds Canada, not always dubbed in French. |
| `PSE_FRENCH_RELEASES` | `french,multi` | Indexed release languages that count (`vfq` for Québec French only, `vff`…). |
| `PSE_FRENCH_TYPES` | `movie,show` | Filtered types. `movie` leaves shows untouched. |
| `PSE_SCROB_VIEWS` | `off` | `on` adds the "Vus et notés" libraries (also visible in other Jellyfin/Emby apps). |
| `PSE_SCROB_PUSH` | `off` | `on` sends playback to the Scrob webhooks set per viewer (Services → Watch tracking → Scrob). |
| `PSE_MUSIC_ALLDEBRID` | `off` | `on`: playback from the AllDebrid account set up in Ether. When a track starts, the album is searched on Prowlarr or Ether's torrent indexers, opened on AllDebrid only if already cached, then streamed. Nothing is stored on disk; an uncached torrent is removed right away. |
| `PSE_MUSIC_DMM` | `off` | `on`: searches the public DMM album catalogue and streams through existing Real-Debrid accounts, without indexers or Prowlarr. |
| `PSE_MUSIC_REALDEBRID` | `off` | `on`: uses the existing Real-Debrid account and indexers to stream available album tracks. Selects all audio tracks on new additions; leaves existing torrent selections untouched. |
| `PSE_MUSIC_TORBOX` | `off` | `on`: uses the existing TorBox account, with cached-only additions and individual streaming links for each track. |
| `PSE_MUSIC_AUDIUS` | `off` | Public MP3 source without an account or key: `first` before Eclipse addons, `last` after Eclipse and debrid accounts as a fallback, `off` disabled. Only recordings matching the Deezer catalogue. Takes precedence over HiFi API mode. |
| `PSE_MUSIC_ECLIPSE_URL` | *(empty)* | `manifest.json` link of an Eclipse addon, generated on the addon's site (as for Eclipse or Remux). Several links separated by spaces are tried in order. Optional when another music source is enabled. The link carries a token: the console masks it. |
| `PSE_MUSIC_HIFI_URL` | *(empty)* | Alternative mode, used only without Eclipse, Audius or an enabled music debrid source: URL of a HiFi API (`https://…`, HTTP Basic credentials allowed). |
| `PSE_MUSIC_QUALITY` | `LOSSLESS` | Quality requested from the HiFi API: `LOSSLESS`, `HI_RES_LOSSLESS`, `HIGH` or `LOW`. With an Eclipse addon, quality is set on the addon's site. |
| `PSE_MUSIC_COVERS_URL` | `https://resources.tidal.com/images` | Artwork origin for the HiFi API. With an Eclipse addon, artwork comes from Deezer. |
| `PSE_LASTFM_API_KEY` | *(empty)* | Last.fm API key (free, last.fm/api/account/create) to import viewers' Last.fm loved and top tracks. Masked in the console. |
| `PSE_LASTFM_SECRET` | *(empty)* | Last.fm shared secret (same page as the API key). With the key, sends viewers' plays to Last.fm (scrobbling). Masked in the console. |
| `PSE_MUSIC_PLAYLISTS_HOURS` | `6` | How often Deezer and Last.fm playlists are re-imported, from 1 to 168 hours. |
| `PSE_MUSIC_CHARTS` | `FR,WW` | Deezer charts added as playlists for every viewer: FR, WW (worldwide), US, UK, BE, CA, DE, ES, IT, BR, NL, NG. Empty: none. |
| `PSE_MUSIC_DISCOVER` | `on` | Per-viewer “Discoveries” playlist: about fifty tracks by artists close to their plays and playlists. |
| `PSE_PLAYABLE_PENDING` | `hide` | `show` keeps titles not yet indexed visible (handy during the first indexing run). |

**Stremio catalogues**: the sync button starts background work and displays its status until completion. Failures preserve the last import; other catalogues can still synchronize. `PSE_CATALOGUE_SYNC_TIMEOUT` sets the per-catalogue budget in seconds (180 by default, 30–900), after queue waiting. Logs distinguish fetching from persistence.

**Release titles**: matching tolerates accents and apostrophes replaced with separators. A part marker inserted into a French title is accepted only when a catalogue alternative confirms its number; year and identifier checks remain enforced.

<a id="nav-connecting-scrob"></a>

Full Plex Media Server history import also fills recent activity using original dates, without duplicates. Run the full-history import again to complete an earlier import; newer local watched states remain authoritative.

Unfinished series stay in Continue Watching even after months without playback, with their next episode. Items are ordered by last viewing; Plex retains its 40-item limit and viewer hide preferences. Selection reads only the current viewer’s history, without scanning other accounts’ history.

### Connecting Scrob

The webhook relays starts, pauses and actual stops, including playback replacement and inactive-session cleanup. Identity stays stable per session and viewer; repeated stops are not sent again and do not alter history.


1. Under **Ether-Remix → Tracking → Scrob**, create the viewer’s Jellyfin credentials.
2. In Scrob → **Connections → Media Players → Add Connection**, choose **Jellyfin** and copy **Server URL**, **Token** and **User ID**. Click **Test**, then **Add**. The token is scoped to that viewer and can be revoked in their devices.
3. Select libraries in that connection. **Movies** and **Shows** work directly. Remix’s advanced “Offer history-only libraries” option adds **Vus et notés — Films/Séries** to import just history items.
4. Choose **SERVER → SCROB**, **SCROB → SERVER**, or both, and **Pull/Push** schedules in Scrob. Incoming sync covers watched/unwatched status and ratings, not resume positions. It needs no switch or webhook in Remix.
5. Optionally, to also send real-time playback events, copy **this same connection’s** webhook into Remix’s collapsible panel and enable “Send playback events immediately” in advanced settings. Scheduled sync works without a webhook. **Test** in that panel only tests the webhook. Tests and event delivery wait up to 60 seconds on slow connections; an unreachable address can still fail.

Scrob’s **Failed to connect to Jellyfin or invalid User ID** error can also indicate a proxy or firewall HTTP 403 before Remix receives the token. Check `/jellyfin/System/Info` and `/jellyfin/Users/{UserId}` with the token from the Scrob server, alongside proxy logs. Sync may enumerate many URLs: anti-crawling protection must distinguish successful authenticated reads from rejected access attempts.

The panel automatically refreshes the viewer’s latest Jellyfin access (Scrob or another client) and webhook status, without reloading the page or clearing unsaved inputs. An API access does not prove that a sync completed. **Trigger Library Scan** requests a refresh from Remix: because its catalogue is virtual, the request is acknowledged immediately without a file scan or global reindex. Episode lists without an explicit sort use the stored, paginated catalogue when episode order is not customized. Use **Pull** to import data into Scrob and **Push** to send it back to Remix.

<a id="nav-connecting-a-music-client-subsonic"></a>

### Connecting a music client (Subsonic)

1. In **Console → Ether-Remix → Sources → Music streaming**, enable **AllDebrid**, **Real-Debrid** and/or **TorBox**, or paste an Eclipse addon manifest. Keys and indexers are reused from Ether-Remix. **Test** checks the account and searches for a link (without checking audio); **Test sources** reads the first audio bytes to identify the format. With several accounts of the same service in **Sources › Debrid**, they are tried in priority order.
2. Under **Content → Music → Music clients**, the administrator creates access for a viewer. Copy the server address, `music-…` username and music password into Arpeggi or another **Subsonic / OpenSubsonic** client. The password is shown only when created and works with token authentication (`t`/`s`), including for viewers who sign into Remix through Plex.
3. Use HTTPS outside your LAN. Each viewer has one music access, restricted to their permissions; replacing or revoking it rejects subsequent requests from every client using it. The main login password is independent. Clients using local accounts can keep **legacy** authentication.

Debrid services follow Eclipse in the order set in **Sources → Debrid** (the highest-priority enabled account determines the service’s rank, then all its enabled accounts are tried in priority order). Changing accounts or priorities automatically refreshes the resolvers and their caches. Audius retains its first/fallback placement. Files are relayed without local storage or conversion: FLAC/Hi-Res depends on the actual file.

TorBox enforces cached-only additions. Real-Debrid no longer provides a cache precheck: a new addition may briefly start downloading on the service before being removed if not ready. Existing torrents are never deleted. No album is downloaded locally. Automated validation uses simulated APIs; these tests do not certify live Real-Debrid/TorBox playback or Hi-Res availability.

**Jellyfin and Plex clients** (Jellyfin web, Finamp, Findroid, Streamyfin, Plexamp, Plex Web…): nothing to set up, a **Musique** library appears next to the others as soon as a music source is turned on. It shows your favourites and plays, then Deezer charts; search covers the whole catalogue and playback is direct. Plex's global search also shows artists, albums and tracks.

**Plex / Jellyfin music playback**: albums and artists share the same cache safely when switching between clients.

**Deezer and Last.fm playlists**: in **Console → Ether-Remix → Sources → Music streaming**, paste each viewer's Deezer profile link (their public playlists, including Favourite tracks) or Deezer playlist links (app share links `link.deezer.com` included), and/or their Last.fm username (loved tracks, top of the month, all-time top, with the Last.fm API key). The playlists show up in Plexamp, Plex Web, Finamp, Jellyfin and Subsonic clients, read-only, and are re-imported every 6 hours (`PSE_MUSIC_PLAYLISTS_HOURS`). Nothing is downloaded: only track references are kept. For Spotify and manual imports, see [Playlists and appearance](#playlists-and-appearance).

**ListenBrainz playlists**: same screen, ListenBrainz username (free, no API key). Ether-Remix imports the latest playlist of each series ListenBrainz generates for them (Daily Jams, Weekly Jams, Weekly Exploration…) and their public playlists, within ListenBrainz's rate limit.

**Personal and automatic playlists**: from a Subsonic client (Symfonium, Feishin, Supersonic, Tempo, DSub…), each viewer creates, renames, fills, reorders and deletes their own playlists; they keep track references and do not depend on any audio provider. Four automatic playlists come on top, computed from their favourites and plays: **Favourite tracks**, **Recently played**, **Most played**, **Rarely played favourites**. Imported and automatic playlists stay read-only. Plex and Jellyfin show them; editing goes through a Subsonic client.

**Matching and fallback**: Eclipse and Audius match recordings by ISRC or title, artist and duration, rejecting unrequested live, remix, cover, acoustic or karaoke versions. Debrid sources search the album and match track filenames and numbers; they do not verify the file’s ISRC or master. If an audio link has expired or is refused, Ether-Remix moves to the next source (up to three). Audio links aimed at the machine itself or at reserved addresses are refused.

**New releases and source test**: the automatic playlist **New releases from your artists** gathers albums released in the last 4 months by the viewer's favourite and most played artists (computed in the background, refreshed every 6 h). In the console, Music tab, **Test audio sources** tries each configured source on its own on the same track and shows the format the source really sends (FLAC, MP3…), not the advertised one, or why it failed. It shows the real playback order, why each inactive source is inactive and, for each source, the steps reached: track found in the catalogue, source result, link obtained, first bytes checked. Full playback is not tested. **Retry this source** reruns a single source, without caches. Addon links are shown by host only; no key or audio link is shown.

**On-demand subtitled titles (VOSTFR)**: in **Ether-Remix → Playback → Subtitles**, search a movie or show and add it. It appears in the **“VOSTFR”** library in Plex (created on the first addition), but not in Movies or Shows until a French version is found.

**Discovering music**: every viewer gets **Top France**, **Top Worldwide**… playlists (countries chosen in Console → Ether-Remix → Sources → Music streaming) and **Discoveries**, renewed every 12 hours. **Genres** (Pop, Rap/Hip Hop, Rock, Electro, Jazz…) can be browsed in Plex (Music › By genre), Jellyfin (Genres) and Feishin (Genres), with the current albums, artists and tracks. On an artist page: **similar artists** and a “Same vibe” radio (Plex), **Instant Mix** (Jellyfin), top and similar songs (Feishin).

**Sending plays to Last.fm**: with the Last.fm API key and shared secret, each viewer can click **Connect Last.fm** in **Console → Ether-Remix → Sources → Music streaming**, authorize Ether-Remix on last.fm, then **I authorized Ether-Remix**. Their completed plays are then sent every 2 minutes (plays from before linking or older than 14 days are not sent). The **Overview** also shows a **Music** panel: plays, hours and listeners over the last 30 days, top tracks and artists.

**Arpeggi, Feishin and other Subsonic clients**: use the generated music credentials above with token authentication. If using the local account password instead, enable **Enable legacy authentication**.

The library fills up with favourites (artists, albums, tracks) and plays. Search queries Deezer live; on playback Ether-Remix resolves the track through the enabled sources and relays the stream. Playlists and genres are supported. Lyrics and audio transcoding are not: the stream is relayed as is.

**Free audio sources:** Audius was validated with a complete 320 kbit/s MP3 track through Ether-Remix. No new account-free FLAC/Hi-Res source is certified. Diagnostics inspect the first bytes and report FLAC bit depth/sample rate; this is not a full playback test. [Audit, excluded sources, configuration and limitations (French)](AUDIO-SOURCES.fr.md).

<a id="nav-eclipse-community-sources"></a>

### Eclipse community sources

Under **Ether-Remix → Sources → Music streaming → Eclipse community sources**, open **Configure** for your provider. Generate its link, paste the `manifest.json` and save. An empty field disables the source. Select **Music** for Internet Archive. UltraMax and Deezer Ricky request your Deezer account on their own website; Ether-Remix only stores the generated link, masked in the console.

| Provider | Manifest environment variable |
| --- | --- |
| Tido | `PSE_MUSIC_TIDO_URL` |
| Internet Archive | `PSE_MUSIC_INTERNETARCHIVE_URL` |
| UltraMax | `PSE_MUSIC_ULTRAMAX_URL` |
| Deezer Ricky | `PSE_MUSIC_DEEZER_RICKY_URL` |
| SoundCloud Eclipse | `PSE_MUSIC_SOUNDCLOUD_URL` |
| SoundCloud Ricky | `PSE_MUSIC_SOUNDCLOUD_RICKY_URL` |

Dedicated sources run in this order, after custom Eclipse links and before debrid accounts. The catalogue remains Deezer’s; missing or uncertain matches are not played. **Test** checks the manifest and catalogue; **Test sources** checks the first audio bytes. Neither a valid manifest nor a FLAC label guarantees full playback or Hi-Res.

**Address tracking:** `PSE_MUSIC_ADDON_REGISTRY=on` by default, checking the public Eclipse registry every 6 hours while music is active. The refresh button also works when automatic checks are disabled (at most one attempt per minute). Public links are cached in the database across restarts and outages. Changes from the address shipped with Ether-Remix are flagged: verify the provider before recreating your link. Personal manifests are never changed automatically. Providers absent from the registry retain their last known address. No Discord monitoring.


<a id="nav-statistics-and-source-activity"></a>

### Statistics and source activity

**Ether-Remix → Stats** separates the dashboard from **Movies & shows** settings. It combines catalogue coverage, seven-day request charts (UTC days), stored releases by source, quotas, errors, partial responses and average durations.

Background indexer activity updates every three seconds: the current title and source, elapsed time, then the search outcome. The journal holds the last 50 searches since indexer startup; it resets after a restart or a settings change that restarts indexing. Daily counters persist; new error and duration measurements begin when this version is installed.

Source statistics refresh every thirty seconds. Detailed catalogue and library totals remain a timestamped snapshot that can be recalculated with **Refresh**. Tracking stops when leaving the tab and pauses while the page is hidden. **Pause updates** freezes the display without stopping indexing.

A title search may consume multiple requests. Titles shared by sources or libraries should not be added together; a stored release does not guarantee playback.


<a id="nav-dmm-with-real-debrid"></a>

### DMM with Real-Debrid

By default, Remix queries the [public DMM instance](https://debridmediamanager.com/albums). **DMM instance address** (`PSE_MUSIC_DMM_URL`) selects a private instance reachable from the Remix container. Enter its base address without `/albums` or `/api/music`. The [DMM repository](https://github.com/debridmediamanager/debrid-media-manager#setup) documents installation; your instance must supply its catalogue and the `/api/music/search` and `/api/music/library` APIs. The public catalogue is not automatically copied.

Configure at least one active Real-Debrid account in **Sources → Debrid**, then enable **DMM · Real-Debrid** under **Ether-Remix → Sources → Music streaming**. No manifest link or DMM account is needed. Browsing still uses the Deezer catalogue; DMM adds another way to find albums to play.

This source does not depend on indexers. It follows the priority of your Real-Debrid accounts, just before their indexer searches; accounts are tried in priority order. Other enabled sources take over when the album or track cannot be played.

Ether-Remix sends no credentials to DMM: searches are public, then magnets are resolved directly through Real-Debrid. FLAC/MP3 tracks are selected together to match DMM's cache. Unready additions are removed, with the same Real-Debrid limitation as indexer searches: the service may briefly start a download before removal. **Test** searches for a link; the source diagnostic can check its first audio bytes.


<a id="nav-history-and-tmdb-comparison"></a>

### History and TMDB comparison

Under **Ether-Remix → Stats**, TMDB totals come from official ID exports, downloaded daily in the background and cached with their date. Separate adult exports are excluded. The first snapshot starts roughly one minute after server startup. A failed refresh preserves the last valid snapshot. The percentage compares the local browsable selection with these lists, not playable audio/video availability.

History supports **Day** (30 days), **Week** (12 weeks), **Month** (12 months) and **Year** (10 years), using UTC. Choose **catalogue additions** or **source searches**, then movies, shows, both or the Anime subset. Anime remains included in Movies and Shows without double counting. Anime search tracking begins with this version; earlier periods remain unmeasured. The source/indexer filter applies to release searches. The detailed table shows period and cumulative values; cumulative values include earlier measurements outside the displayed window.

Additions are reconstructed from the entry dates of media still retained locally; undated titles are explicitly excluded. Source searches are stored persistently from this update onward: one search means one title queried on one source, excluding cancellations and quota denials. Titles can be searched repeatedly. Unmeasured periods are marked as such.

Each addition under **On-demand subtitled titles** also queues a priority search without waiting for the normal retry delay. Indexing must be enabled; source budgets and playback pauses still apply. If queueing fails, the VOSTFR selection is retained and the console explains it.

The DMM **Test** button now samples DMM’s own catalogue instead of Deezer charts. It distinguishes catalogue, account, cache and audio-link stages. Logs tagged `component=pse-music source=dmm` report counts and failure categories without keys or playback URLs.

Xtream sources can also import the provider’s VOD movies and series: enable the source’s VOD option to include them.

Under **Sources → IPTV**, **Edit** opens the selected provider. Creating, saving and closing its form keeps the IPTV category selected. Links from Live TV open the same editor.

Under **Sources → Music streaming**, settings are grouped into **Eclipse** (manifests and registry), **DMM / Real-Debrid** (DMM catalogue, indexer searches and shared accounts), and **Other sources** (AllDebrid, TorBox, Audius and HiFi API). A separate audio diagnostic tests all families.

Under **Sources → Debrid → Edit**, **Show API key** only reveals the selected account’s key to the signed-in owner. **Hide API key** or closing the editor removes the displayed value without changing the saved key.

Under **Indexing**, **Imported sources** can be filtered and sorted by category, name or content. **Titles to index first** expands on demand. “Searched” includes searches without results; “Shown” counts titles meeting catalogue criteria. Counts can be refreshed and may reflect the latest cached snapshot.

<a id="nav-anime"></a>

## Anime

In **Ether-Remix → Content → Anime**, create the Anime preset to get popular and top-rated movie/show collections from TMDB. Mixed libraries appear as separate Movies and Shows sections in Plex. Their Anime category is retained after renaming. Anime libraries respect your language filters, including French/MULTi. Original audio and subtitles remain an explicit choice through on-demand exceptions or the Any language option.

Under **Sources → Stremio**, use **Add addon** and paste the `manifest.json` URL generated by your chosen provider. No provider shortlist is imposed or suggested. Sources, activation and priority are shared with Movies & Shows. For discovery, use TMDB collections, AniList lists or an addon providing catalogues.

The **What's new** button in the console header reopens release notes. The popup only shows unread notes since the last viewed version. Closing or acknowledging it remembers the version and read notes in this browser; rebuilding Docker does not repeat them. The previous marker is migrated automatically. **Full history** provides optional access to earlier notes. The sidebar version is provided by the running server; official images include an automatically increasing Remix build number. The footer links to both Ether and Ether-Remix.

<a id="nav-on-demand-subtitled-exceptions"></a>

### On-demand subtitled exceptions

**Ether-Remix → Playback → Subtitles → On-demand subtitled titles** groups movies, shows and anime explicitly allowed without French dubbing. Each addition queues a priority search when background indexing is enabled, within source budgets and playback pauses. These titles also appear in the VOSTFR library. Your existing selection is preserved.

<a id="nav-public-instances-or-self-hosting"></a>

### Public instances or self-hosting

Music sources identify public instances and link to available self-hosting projects: [UltraMax Music](https://github.com/PaRaN01a-hash/ultramax-music) and [HiFi API](https://github.com/binimum/hifi-api). For Stremio, paste your public or private instance manifest; no provider is recommended. Your personal manifest remains your own configuration.

<a id="nav-settings-organization"></a>

## Settings organization

Indexers (Prowlarr, Jackett, NZBHydra2) find releases. Configure the appropriate playback path: a debrid account, a BitTorrent client or a Stremio addon such as Seedstrem. Seedstrem uses its own torrent client. For Usenet, Remix reads through its NNTP engine and your Usenet providers; SABnzbd/NZBGet are not integrated external download clients.

- **Sources**: one entry for accounts, Stremio addons, BitTorrent/Usenet indexers, Usenet providers, IPTV and music sources. Ether components and configurations are retained. Indexers can be filtered by Prowlarr/Jackett instance or manual origin, with one shared configuration.
- **Indexing → Settings**: enablement, budgets, pace and source selection: Automatic for enabled Stremio addons, or Custom, including no sources. This does not disable playback sources. Priority titles show catalogue, searched, with-release and shown counts for each library or collection; groups can overlap.
- **Overview**: quick start, Remix indexing activity plus Ether’s Sources and Usage dashboards.
- **Playback → Priorities**: preferences among equivalent-quality sources. This does not enforce sequential network searches or universally rank local files, torrent clients, XDCC and FTP. Last-resort behavior is configured per Stremio addon in Sources.
- **Playback → Quality**: French catalogue, release languages, resolution, codecs, cache and collection exceptions.
- **Playback → Subtitles**: explicit non-dubbed title selection and priority searches.
- **Content**: movies, shows, anime, music, playlists and custom libraries. Mixed libraries remain available under Movies and Shows.
- **Tracking**: notifications, Scrob and Last.fm credentials.

Existing `/sources/...` addresses and old Remix tabs redirect to their new locations. Accounts and manifests are not duplicated. Categories organize sources; they do not guarantee that a particular title is available.

<a id="nav-browsing-and-poster-badges"></a>

### Browsing and poster badges

Plex and Jellyfin prepare a page’s badges together. New episodes and collection rotations respect each viewer’s state. The playable-only catalogue keeps exact counts, including language exceptions.

**Faster posters**: with a poster provider (PostersPlus, XRDB, RPDB…), Plex keeps each poster for a day instead of asking the server again on every screen. A TMDB fallback poster, served when the provider does not answer, is still rechecked every time so it does not stay on screen after the outage. Apps fetch provider posters again every 24 hours; set that delay in **Console → Ether-Remix → Visuals → Posters** (`PSE_POSTER_REFRESH_HOURS`, 1 to 168). Jellyfin and Emby receive the poster at the size the app asked for, and Jellyfin no longer waits before sending an image. Without a provider, a TV asking for a 600 px poster receives the 500 px one rather than the 780 px one, more than twice as heavy.

**Release-date sorting**: Movies and Shows open newest first. Plex uses this sort until you pick another in the app. Jellyfin and Emby use it when the app does not ask for a sort; Jellyfin Web and Infuse keep their own: change it once in the app, which remembers it.

**Movies and Shows always in French**: a title without a French version (chosen as VOSTFR, placed in an “Any language” library or an exception collection, or found only with French subtitles by SeerrCatalog) stays visible in its library or collection, but never in Movies, Shows or the home screen.


<a id="nav-playlists-and-appearance"></a>

## Playlists and appearance

Under **Content → Music → Playlists**:

- **Discover**: Deezer trends and search; Spotify connection and your playlists; links to ListenBrainz, Soundplate and DailyPlaylists for further discovery.
- **My accounts**: automatically synced public Deezer profiles, Last.fm and ListenBrainz (every 6 hours by default). Private Deezer playlists cannot be accessed through a profile link.
- **Import**: Deezer, Spotify or ListenBrainz playlist links, or CSV/XSPF files (2 MB maximum). CSV columns: `title` and `artist`, or Exportify’s `Track Name` and `Artist Name(s)`; comma and semicolon delimiters are supported. Audio URLs in XSPF files are never downloaded.

Choose a viewer, inspect the preview and confirm. Import creates a personal copy editable through Subsonic without replacing other playlists. These copies do not automatically sync. It processes the first 2,000 items and reports progress and unmatched catalogue tracks. Playback still depends on your music sources; playlist metadata does not guarantee an available audio stream. Progress is retained by the server for one hour and does not survive a server restart.

**Spotify**: under Discover → Spotify, enter your application’s **Client ID**. Add the exact redirect URI displayed by Remix to your Spotify developer dashboard, then connect the chosen viewer’s account. HTTPS is required except on loopback. No Client Secret is needed: OAuth with PKCE, server-encrypted tokens, read-only access and a disconnect button. In development mode, the app owner needs Premium and users must be allowed in Spotify Developers. Spotify may deny playlist items unless you own or collaborate on the playlist; directory links do not bypass this limit.

**Appearance**: the palette button in the top bar offers Ether, Nord, Catppuccin, Solarized, Gruvbox and Rosé Pine. Select light or dark mode separately; previews are immediate and your preference is stored in this browser. Other users keep their own preferences.

<a id="nav-seedstrem"></a>

## Seedstrem

[Install Seedstrem and connect its manifest to Remix](SEEDSTREM.en.md): Prowlarr, qBittorrent or Deluge, and HTTP playback during download.

<a id="nav-submaker"></a>

## SubMaker

[Configure external subtitles and optional translation](SUBMAKER.en.md), globally or per library/collection, with SeerrCatalog.

For visibility rules and search intervals, see [Catalogue and indexing](CATALOGUE.en.md). To connect the addon, see [SeerrCatalog](SEERRCATALOG.en.md).

<a id="nav-unified-history-and-listening"></a>

### Unified history and Listening

**Display name**: on a viewer’s page (**Viewers**), the pencil next to their name sets the name used in statistics, activity and lists. Plex does not overwrite it and plays already recorded take the new name. Sign-in does not change. An empty field restores the Plex name.

**Activity → History** combines movies, shows and music in one chronological table, with search, sorting, pagination and details. The list contains the latest 500 plays in the selected period. Music retains artist/album metadata; its duration is the track length, not measured listening time. Completion and session-duration metrics remain explicitly video-only.

**Activity → Listening** uses the same period, year and user filters as Viewing. Counts, daily listens, weekday/hour heatmap and track, artist, album and listener rankings cover the entire period, independently of the history limit. Charts use the browser time zone. Existing listens remain included even when the music source is disabled.

<a id="nav-local-media-and-ibroadcast"></a>

## Local media and iBroadcast

<a id="nav-video-and-audio-folders"></a>

### Video and audio folders

Add folders to your Dockge-Enhanced stack volumes and restart the container:

```yaml
volumes:
  - ./config:/config
  - ./cache:/cache
  - /host/path/videos:/media/videos:ro
  - /host/path/music:/media/music:ro
```

In **Sources → Local files**, enter a unique name, the **container path** (for example `/media/videos`) and video or audio type. Mount network folders on the host before starting the stack. The container user needs read access.

Scans run after adding a folder, at startup, hourly and with **Scan now**. The panel shows progress and errors. Unchanged metadata is reused. Unavailable folders retain their previous index. Missing files also remain after successful scans, so empty or partially disconnected mounts never empty a library. Returning files are refreshed on the next scan. Removing a source never deletes files. Symbolic links are skipped.

Videos are matched **against the Remix catalogue first, then TMDB**. Movie names with a year (`Title.2020.mkv`) and explicit episodes (`Show.S01E02.mkv`) allow conservative identification: exact normalized title, matching year and a unique result. Identified files reuse existing entries, including the corresponding seasons and episodes, and provide local playback. Without a reliable match, a video keeps its filename title and needs no TMDB entry. Each folder feeds a library and collection; personal videos do not inflate TMDB counters.

After the first scan, open **Libraries and collections** beneath the video folder in **Sources → Local files**. Select existing libraries to display its dedicated collection, or custom collections for the folder to feed; each change saves immediately. This lets you group folders under “Animated movies” or “Animated series”. Creation links open library and collection editors. Remix continues to manage the folder’s dedicated collection. Built-in collections, other viewers’ collections and upcoming-release collections cannot be edited here.

Existing collection sources, filters and refresh intervals are preserved. New files appear after scanning and collection refresh. Movie libraries show movies, show libraries show identified shows; unmatched videos remain movie-type videos. Sharing is unchanged: destinations fed by private folders are restricted to viewers allowed on every contributing folder. Unlinking a library’s final collection removes that empty library without deleting media, and scans do not recreate it. A collection whose only source is this folder needs another source before unlinking; permanently removing the folder disables that collection.

Local audio joins Music in Plex/Plexamp, Jellyfin/Emby and Subsonic clients. Tags supply title, artist, album, disc and track; filenames and folder names are used as fallbacks. Files are served in their original format, so audio compatibility depends on the client. Local files are never sent to a music provider.

Recognized formats: MKV, MP4/M4V, AVI, MOV, WebM, TS/M2TS, MPEG/MPG, WMV, FLV, OGV, VOB, 3GP; MP3, FLAC, M4A/AAC, Ogg/Opus, WAV, AIFF, APE, WMA and ALAC. Files must contain the selected stream type and be readable by `ffprobe`, included in the Linux Docker image. Folders are **private by default**, accessible only to the owner who adds them. Under **Private · sharing options**, select individual viewers or everyone and save. Only the administrator can add server filesystem paths.

<a id="nav-ibroadcast-account"></a>

### iBroadcast account

In **Sources → Music streaming → iBroadcast**, or your **personal portal** as a viewer, first choose **Add account** and give it a name:

1. On [iBroadcast](https://media.ibroadcast.com/), open the account menu → **Apps → developer** and create a private application.
2. Edit it, select **Grant Type → Device Code**, save and copy its **Client ID** into Remix. No Client Secret, password or redirect URL is needed.
3. Click **Connect iBroadcast**, open the link and authorize **read-only** account and library access. The panel updates automatically.
4. Import starts after authorization, then runs hourly; **Refresh library** starts it manually.

Each iBroadcast account is **private by default**, belongs to the viewer who connects it and has separate encrypted credentials. You can add multiple accounts and optionally share each with selected viewers or everyone. Authorized libraries appear together in Music; favourites and listening history remain personal. Enable **Import my iBroadcast playlists** to bring them into your clients, preserving track order and repeated songs while skipping deleted or unavailable tracks. Playlists refresh with the library and remain personal to the account owner. Tracks stream as **MP3 128 kbps**, including free accounts. Playlist and tag edits are not written back to iBroadcast. Tokens are encrypted in the database, refreshed automatically and excluded from client responses. Disconnecting removes the library from Remix; revoke the provider authorization separately in iBroadcast **Apps**.

<a id="nav-search-caching-and-source-outages"></a>

## Search caching and source outages

The resolver's “Remember searches for” duration is a strict ceiling (six hours by default). A title's age never overrides it. Empty results expire sooner: up to three minutes for recent or undated titles, thirty minutes for older titles, always within the configured ceiling. A successful empty search replaces previous results.

During a temporary outage (timeout, connection failure, server error or automatic pause after repeated failures), results expired for less than 24 hours may serve as fallback candidates. They are neither renewed nor stored as a fresh complete search. Only torrent/NZB descriptors are reused; expired direct HTTP streams are excluded. Debrid cache hints are cleared and the normal playback path validates candidates. Background availability searches do not use this fallback.

This does not hide credential errors, exhausted quotas, cancellation or valid empty responses. Disabling caching or the source disables this fallback too. Changes to the source address, credentials or search configuration invalidate the cache identity. Fallback use is logged as `using expired search candidates`.


<a id="nav-navigation-and-configuration"></a>

### Navigation and configuration

The console uses `/ether/remix`. Old bookmarks redirect with their parameters preserved. Configuration sections start collapsed; opening or closing them preserves drafts. Sources → Music streaming groups Eclipse, DMM / Real-Debrid, iBroadcast and other providers. Subsonic address and library counts are under Content → Music. For Spotify, use `/ether/remix/contenus?content=musique` as the allowed callback path; callbacks already started with the old path remain accepted.

## Nuvio-inspired navigation cards

**Artwork packs**: preview a Nuvio Assets, Kometa, nichtlegacy, Kaptain, FR-Badges, UltraMax or TVFlix/Nuvio set, then apply its matches to selected cards. Custom artwork is protected by default, unmatched cards stay unchanged, and ambiguous variants can be selected in a paginated review panel. Import a JSON file (up to 4 MB) or its public HTTPS URL: only names and covers are read, never addons/catalogues. Save with “Apply cards to my home screens”. Individual customization and gallery search remain available.

**Movie and TV posters**: BetterPosters already has an integration; its configurator is linked from Services. Pictorium and TOP Posters have their own optional accounts using the service’s poster URL template. Private parameters stay server-side and failures fall back to TMDB. Pictorium can run elsewhere: using a public instance does not require a local installation.

A **style for multiple cards** can be prepared from one gallery: choose cards, search their artwork, filter by creator, set or folder and select unique matches in one click. Review variants before bulk application; cards without a choice keep their artwork. Available metadata varies by source, and the same creator does not guarantee the same style. Searches are limited to 30 cards per batch; nothing is saved until “Apply cards to my home screens”.

This is an **optional BETA**, disabled until you apply it. Open **Ether-Remix → Visuals → Cards → Nuvio-inspired cards → Quick setup**.

1. Use **Starter selection** or build your own. **Studios and labels** searches TMDB production companies by name or ID; it is not a comprehensive music-label directory. **Genres** offers TMDB movie genres, with TV equivalents where available.
2. Under **Services**, choose a country: services load automatically. Providers come from TMDB / JustWatch movie and TV lists. Select services, then change countries without losing your selection. Use a two-letter ISO code for other countries. Country means catalog availability, not studio origin, and grants no subscription access.
3. Country services load automatically. The editor opens beneath the card; GitHub, ThePosterDB and ACdb galleries let you search and select artwork directly in Remix. MediUX covers public genre galleries (English search), not studios/services. Fanart.tv has no integrated navigation-card search; file/URL import remains available. **Customize a card** previews your selection directly in the editor and overview. Automatic artwork uses TMDB logos or mosaics, with a title fallback. The **[nichtlegacy / kometa-config-images](https://github.com/nichtlegacy/kometa-config-images)** gallery offers matching images, including logos and portrait formats: choose artwork suited to your card. The list is cached for six hours; failures are reported.
4. **Import · file or URL** accepts a direct image URL or a JPEG/PNG/WebP file up to 10 MB. Uploaded files are resized to at most 1,200 pixels and 256 KiB and retained in Remix data; URLs remain remote and may become unavailable. Links to [ThePosterDB](https://theposterdb.com/), [MediUX](https://mediux.pro/), [Fanart.tv](https://fanart.tv/) and [ACdb Art Department](https://acdb.tv/art-department) open those sites to select an image: manual import remains an alternative to the integrated galleries described above. Respect source terms and creator rights. These settings concern navigation cards, not global movie/TV poster providers.
5. Choose whether to **Group services by country on home screens**, then **Apply cards to my home screens**. Up to 200 cards can be selected per operation. Groups replace Plex and Jellyfin/Emby recommendations; each card opens its collection. Movies and shows are separate, and cards lead collection lists unless the client imposes its own sort.

Collections fill asynchronously from TMDB, refresh daily and contain up to 100 titles each. Genres without a TV equivalent only create movie collections. Empty collections are hidden and viewer restrictions remain enforced. Remix playback sources are still required; setup starts no media download.

Reopening loads saved cards and images. Applying adds missing cards without removing previous ones. Only artwork explicitly edited here is replaced, on both movie and TV collections; existing names, rules and other customizations are retained. **Clear selection** deletes nothing on the server. Disable or delete an existing card’s collection in the editor to remove it. **Restore previous home** disables the overlay without deleting collections or rewriting the saved layout.

For existing collections, including Xperience imports, use **Audience and destinations → Remix navigation card**. Content remains unchanged. **No card** removes group membership without deleting the collection.

This preset is independent of Nuvio and copies neither its files nor its settings. No Xperience manifest is required. Clients control the final appearance: Plex may retain portrait frames and Jellyfin may prefix rows with “Latest”. Refresh clients after saving; their caches may delay new images. Native-client testing remains necessary during beta. Other protocols receive collections without home replacement.

## Xperience import

In **Ether-Remix → Content** (`/ether/remix/contenus`), open **Import a manifest** in **Import Xperience**. Set up your catalogues on [xperience-app.com](https://xperience-app.com/) and paste the personal `manifest.json` link (HTTPS or `stremio://`). **Show catalogues** previews the choices without installing a source or creating libraries.

Select up to **30 catalogues** per import: movies and shows stay separate, including Animation lists. Search for a catalogue, rename it and choose its parameters; **No additional filter** maps to `genre=None` when offered by Xperience. Search catalogues are excluded. Choose **Libraries and collections**, **Collections only** or **Libraries only**. Libraries-only uses backing collections without publishing them to the Collections tab or Home rows.

New collections preserve Xperience ordering without random rotation and refresh on the selected import schedule (24 hours by default, from 1 to 168 hours). Set a limit of **1 to 2,000 titles per catalogue**, 500 by default; this is not an unlimited import. Libraries are published to compatible clients (Plex, Jellyfin, Emby, Stremio, Xtream and Silo). Filling happens in the background. IDs are matched against the Remix catalogue; some titles may be missing. Language, availability and viewer access filters still apply. This refresh does not freeze other Home rows.

Xperience supplies lists, not files or playback sources: your Remix sources are still required, without `.strm` files. The personal manifest is saved in the Stremio add-on’s private configuration; collections only store its reference and selected parameters. Do not share the link. The importer installs or reuses the Xperience plugin in Ether; it is required for refreshes. Manifest polling (available catalogues and parameters) has its own interval, configurable from 1 to 168 hours, defaulting to 1 hour. Reimport applies this interval to the existing source but preserves existing collection schedules, editable in their editor. Newly advertised catalogues are not imported automatically.

Creating collections from **Plugins → Catalogues** or **Home** carries the catalogue media type and default parameters. Validation errors identify the rejected field. Deleted collections no longer reappear in the row catalogue from historical impressions; current names are used. Uninstalling the plugin does not delete its collections: they remain stored but their source becomes unavailable. Reimporting the same catalogues and parameters reuses existing entries, including after partial creation, without overwriting names, limits, destinations or other customizations. Edit these settings in the existing collections and libraries. A new import limit therefore does not replace an existing selection’s limit.

Library additions, renames, hiding and deletions reach Plex without restarting Ether. Reload the Plex client if its list is still cached. Hiding the general Movies / TV Shows libraries does not disable Xperience libraries: they retain their own publication and access permissions. An empty collection is not necessarily still loading: also check its parameters, filters and source status.


<a id="nav-jukebox-quick-setup"></a>

## JukeBox quick setup

In **Libraries** or **Ether-Remix → Content**, open **Set up with JukeBox**. Movies, Shows and Anime are preselected; **Create my selection** creates their collections and libraries for Plex, Jellyfin, Emby, Stremio, Xtream and Silo. Choose **Collections / automatic selections only** for automatic rows without new libraries. Collections target the Collections tab, home and compatible section rows.

**Browse all JukeBox lists** loads the public lists from [their MDBList profile](https://mdblist.com/lists/jukebox8786/). Search for “Documentaire”, for example, select lists and adjust their Movies / Shows / Movies and shows type. The three suggestions remain usable if the remote directory is unavailable. Mixed lists create separate movie and show collections and libraries; the Movies suggestion keeps only movies from the “Les plus piratés” list.

Collections refresh daily, keep released titles only and sort by release date descending. Keep the general library sort on **Release date** (the default); explicit client sorting takes precedence. Initial population runs in the background. Lists add no playback sources, download no videos and do not guarantee French audio: sources, language filters, viewer permissions and availability rules still apply.

Retrying reuses entries already created, including after a partial error, without overwriting your edits. You can rename or hide libraries and change collection sources, refresh frequency, sorting and targets afterwards. A deleted library can be recreated by running the selection again; deleting a library does not delete its collection.

<a id="nav-local-media-health"></a>

### Local media health

The diagnostic loads when its section in **Sources → Local files** is first opened. Collapsing and reopening it preserves the result; use **Refresh diagnostics** to run the check again.

In **Remix → Local media**, refresh diagnostics for previously indexed files without running `ffprobe`. Missing paths, permission failures and other access failures are distinguished. Files in an unavailable folder cannot be checked; missing files in a readable folder are marked missing. Size/date changes require another scan.

Filter by folder, state, name or relative path. Unidentified videos remain valid local videos. Potential duplicates share a matched title or filename within a source folder; different editions may be flagged, and content equality is not verified. Files are sorted largest first, in pages of 50.

Logical sizes are grouped by source folder (one video library per folder; audio folders join Music). Accessible sizes are checked now; known sizes are from the index and include missing files. Hardlinks may be counted more than once: these figures are neither allocated disk blocks nor free disk space. No files are modified or deleted. Missing entries remain until the source is explicitly removed; removing and adding the source rebuilds its index and requires sharing to be configured again.

<a id="plex-home"></a>
## Plex Home

In `/ether/home`, select the same viewer and device type as in Plex: the typical viewer does not represent a personalized account. Global settings and viewer overrides both apply. The Continue Watching preview includes next episodes; On Deck is hidden when it repeats those episodes. Movie and TV recommendation pages remain separate from Home.

Plex Web and apps may present cards differently and cache previous responses. Reload the client’s Home after changing settings. Per-library requests should not append entire Movie and TV recommendation pages to Home. Infuse retains its collection-shelf presentation.
