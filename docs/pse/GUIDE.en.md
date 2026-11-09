# Ether-Remix configuration guide

[Ether-Remix overview](../../README.en.md) · [Français](GUIDE.fr.md)

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

### Configuring Ether-Remix from the console

Every addition is configured in the console, under **Ether-Remix** (`/ether/accent`): switches, options and the state of each feature (indexed titles, playable titles, music library…). A value saved on that page replaces the environment variable of the same name and applies without a restart; the ↺ button goes back to the variable or the default. The variables below still work for a deployment fully described in `compose.yaml`.

Ether-Remix accepts every Ether environment variable (`ETHER_PORT`, `ETHER_DATA_DIR`, `PUID`/`PGID`…): see [docs/install.md](../install.md). Ether-Remix-specific variables:

| Variable | Default | Effect |
| --- | --- | --- |
| `PSE_NOTIFY_LANGUAGE` | `fr` | Language of owner notifications. `en` keeps Ether's English text. |
| `PSE_INDEXER` | `off` | `on` enables background release indexing. |
| `PSE_INDEXER_SOURCES` | *(all enabled Stremio addons)* | Comma-separated indexer IDs to query. The only way to include a Prowlarr/Jackett/NZBHydra2 indexer. |
| `PSE_INDEXER_DAILY` | `1000` | Maximum requests per source per day (UTC). |
| `PSE_INDEXER_PACE` | `10s` | Minimum delay between two titles (at least `2s`). |
| `PSE_INDEXER_TYPES` | `movie,show` | Indexed types (`movie`, `show`). A show is searched through its first episode. |
| `PSE_PLAYABLE` | `off` | `on` enables the "playable only" catalogue. |
| `PSE_PLAYABLE_MIN` | `2` | Distinct releases (same name counts once) needed to list a title. |
| `PSE_PLAYABLE_FILTER` | *(none)* | Releases that count, e.g. `lang=fr,multi;min=1080;codec=hevc,h264;cached=1`. Languages: `fr` (any French dub: FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF, VF2), `vff`, `vfq`/`qc`, `multi`, `en`, `vostfr` (French subtitles announced), `vo` (no dub announced)… Changing the filter recomputes the catalogue at startup, without reindexing. |
| `PSE_FRENCH` | `off` | `on` enables the French-language catalogue. |
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

### Connecting Scrob

1. Add `PSE_SCROB_VIEWS=on` to the Ether-Remix container and restart it.
2. In Scrob, add a **Jellyfin** connection: URL `http://<host>:32400` (or your `ETHER_PORT`), your Ether-Remix account credentials.
3. In the connection's libraries, **select only "Vus et notés — Films" and "Vus et notés — Séries"**. With no selection, Scrob would import Ether-Remix's whole catalogue.
4. For real time: copy that connection's webhook URL (Scrob → Connections), then in Ether-Remix open **Services → Watch tracking → Scrob**, turn the card on and paste the link for the matching viewer (**Test** button).

### Connecting a music client (Subsonic)

1. In **Console → Ether-Remix → Sources → Music streaming**, enable **AllDebrid**, **Real-Debrid** and/or **TorBox**, or paste an Eclipse addon manifest. Keys and indexers are reused from Ether-Remix. **Test** checks the account and searches for a link (without checking audio); **Test sources** reads the first audio bytes to identify the format. With several accounts of the same service in **Sources › Debrid**, they are tried in priority order.
2. In the client, add a **Subsonic / OpenSubsonic** server: URL `http://<host>:32400` (or the `ETHER_PORT` port), your Ether-Remix account credentials (local password accounts).
3. Enable the client's **plain-text password / legacy authentication** option: Ether-Remix only stores hashes, so token authentication (`t`/`s`) is refused (code 41). Use HTTPS (reverse proxy) outside your LAN.

Debrid services follow Eclipse in the order set in **Sources → Debrid** (the highest-priority enabled account determines the service’s rank, then all its enabled accounts are tried in priority order). Changing accounts or priorities automatically refreshes the resolvers and their caches. Audius retains its first/fallback placement. Files are relayed without local storage or conversion: FLAC/Hi-Res depends on the actual file.

TorBox enforces cached-only additions. Real-Debrid no longer provides a cache precheck: a new addition may briefly start downloading on the service before being removed if not ready. Existing torrents are never deleted. No album is downloaded locally. Automated validation uses simulated APIs; these tests do not certify live Real-Debrid/TorBox playback or Hi-Res availability.

**Jellyfin and Plex clients** (Jellyfin web, Finamp, Findroid, Streamyfin, Plexamp, Plex Web…): nothing to set up, a **Musique** library appears next to the others as soon as a music source is turned on. It shows your favourites and plays, then Deezer charts; search covers the whole catalogue and playback is direct. Plex's global search also shows artists, albums and tracks.

**Deezer and Last.fm playlists**: in **Console → Ether-Remix → Sources → Music streaming**, paste each viewer's Deezer profile link (their public playlists, including Favourite tracks) or Deezer playlist links (app share links `link.deezer.com` included), and/or their Last.fm username (loved tracks, top of the month, all-time top, with the Last.fm API key). The playlists show up in Plexamp, Plex Web, Finamp, Jellyfin and Subsonic clients, read-only, and are re-imported every 6 hours (`PSE_MUSIC_PLAYLISTS_HOURS`). Nothing is downloaded: only track references are kept. Spotify is not supported (its API requires a developer app and a per-account sign-in).

**ListenBrainz playlists**: same screen, ListenBrainz username (free, no API key). Ether-Remix imports the latest playlist of each series ListenBrainz generates for them (Daily Jams, Weekly Jams, Weekly Exploration…) and their public playlists, within ListenBrainz's rate limit.

**Personal and automatic playlists**: from a Subsonic client (Symfonium, Feishin, Supersonic, Tempo, DSub…), each viewer creates, renames, fills, reorders and deletes their own playlists; they keep track references and do not depend on any audio provider. Four automatic playlists come on top, computed from their favourites and plays: **Favourite tracks**, **Recently played**, **Most played**, **Rarely played favourites**. Imported and automatic playlists stay read-only. Plex and Jellyfin show them; editing goes through a Subsonic client.

**Matching and fallback**: Eclipse and Audius match recordings by ISRC or title, artist and duration, rejecting unrequested live, remix, cover, acoustic or karaoke versions. Debrid sources search the album and match track filenames and numbers; they do not verify the file’s ISRC or master. If an audio link has expired or is refused, Ether-Remix moves to the next source (up to three). Audio links aimed at the machine itself or at reserved addresses are refused.

**New releases and source test**: the automatic playlist **New releases from your artists** gathers albums released in the last 4 months by the viewer's favourite and most played artists (computed in the background, refreshed every 6 h). In the console, Music tab, **Test audio sources** tries each configured source on its own on the same track and shows the format the source really sends (FLAC, MP3…), not the advertised one, or why it failed. It shows the real playback order, why each inactive source is inactive and, for each source, the steps reached: track found in the catalogue, source result, link obtained, first bytes checked. Full playback is not tested. **Retry this source** reruns a single source, without caches. Addon links are shown by host only; no key or audio link is shown.

**On-demand subtitled titles (VOSTFR)**: in **Ether-Remix → On-demand subtitles**, search a movie or show and add it. It appears in Movies and Shows even without a French version, and in the **“VOSTFR”** library in Plex and Jellyfin (created on the first addition).

**Discovering music**: every viewer gets **Top France**, **Top Worldwide**… playlists (countries chosen in Console → Ether-Remix → Sources → Music streaming) and **Discoveries**, renewed every 12 hours. **Genres** (Pop, Rap/Hip Hop, Rock, Electro, Jazz…) can be browsed in Plex (Music › By genre), Jellyfin (Genres) and Feishin (Genres), with the current albums, artists and tracks. On an artist page: **similar artists** and a “Same vibe” radio (Plex), **Instant Mix** (Jellyfin), top and similar songs (Feishin).

**Sending plays to Last.fm**: with the Last.fm API key and shared secret, each viewer can click **Connect Last.fm** in **Console → Ether-Remix → Sources → Music streaming**, authorize Ether-Remix on last.fm, then **I authorized Ether-Remix**. Their completed plays are then sent every 2 minutes (plays from before linking or older than 14 days are not sent). The **Overview** also shows a **Music** panel: plays, hours and listeners over the last 30 days, top tracks and artists.

**Feishin and other Subsonic clients**: use Ether-Remix's address, your username and a local password (Your account › Sign-in methods, including for an account signed in through Plex), and tick **Enable legacy authentication**: Ether-Remix does not accept Subsonic token authentication.

The library fills up with favourites (artists, albums, tracks) and plays. Search queries Deezer live; on playback Ether-Remix resolves the track through the enabled sources and relays the stream. Playlists and genres are supported. Lyrics and audio transcoding are not: the stream is relayed as is.

**Free audio sources:** Audius was validated with a complete 320 kbit/s MP3 track through Ether-Remix. No new account-free FLAC/Hi-Res source is certified. Diagnostics inspect the first bytes and report FLAC bit depth/sample rate; this is not a full playback test. [Audit, excluded sources, configuration and limitations (French)](AUDIO-SOURCES.fr.md).

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


### Statistics and source activity

**Ether-Remix → Stats** separates the dashboard from **Movies & shows** settings. It combines catalogue coverage, seven-day request charts (UTC days), stored releases by source, quotas, errors, partial responses and average durations.

Background indexer activity updates every three seconds: the current title and source, elapsed time, then the search outcome. The journal holds the last 50 searches since indexer startup; it resets after a restart or a settings change that restarts indexing. Daily counters persist; new error and duration measurements begin when this version is installed.

Source statistics refresh every thirty seconds. Detailed catalogue and library totals remain a timestamped snapshot that can be recalculated with **Refresh**. Tracking stops when leaving the tab and pauses while the page is hidden. **Pause updates** freezes the display without stopping indexing.

A title search may consume multiple requests. Titles shared by sources or libraries should not be added together; a stored release does not guarantee playback.


### DMM with Real-Debrid

Configure at least one active Real-Debrid account in **Sources → Debrid**, then enable **DMM · Real-Debrid** under **Ether-Remix → Sources → Music streaming**. No manifest link or DMM account is needed. Browsing still uses the Deezer catalogue; DMM adds another way to find albums to play.

This source does not depend on indexers. It follows the priority of your Real-Debrid accounts, just before their indexer searches; accounts are tried in priority order. Other enabled sources take over when the album or track cannot be played.

Ether-Remix sends no credentials to DMM: searches are public, then magnets are resolved directly through Real-Debrid. FLAC/MP3 tracks are selected together to match DMM's cache. Unready additions are removed, with the same Real-Debrid limitation as indexer searches: the service may briefly start a download before removal. **Test** searches for a link; the source diagnostic can check its first audio bytes.


### History and TMDB comparison

Under **Ether-Remix → Stats**, TMDB totals come from official ID exports, downloaded daily in the background and cached with their date. Separate adult exports are excluded. The first snapshot starts roughly one minute after server startup. A failed refresh preserves the last valid snapshot. The percentage compares the local browsable selection with these lists, not playable audio/video availability.

History supports **Day** (30 days), **Week** (12 weeks), **Month** (12 months) and **Year** (10 years), using UTC. Choose **catalogue additions** or **source searches**, then movies, shows, both or the Anime subset. Anime remains included in Movies and Shows without double counting. Anime search tracking begins with this version; earlier periods remain unmeasured. The source/indexer filter applies to release searches. The detailed table shows period and cumulative values; cumulative values include earlier measurements outside the displayed window.

Additions are reconstructed from the entry dates of media still retained locally; undated titles are explicitly excluded. Source searches are stored persistently from this update onward: one search means one title queried on one source, excluding cancellations and quota denials. Titles can be searched repeatedly. Unmeasured periods are marked as such.

Each addition under **On-demand subtitled titles** also queues a priority search without waiting for the normal retry delay. Indexing must be enabled; source budgets and playback pauses still apply. If queueing fails, the VOSTFR selection is retained and the console explains it.

The DMM **Test** button now samples DMM’s own catalogue instead of Deezer charts. It distinguishes catalogue, account, cache and audio-link stages. Logs tagged `component=pse-music source=dmm` report counts and failure categories without keys or playback URLs.

Under **Sources → IPTV**, **Edit** opens the selected provider. Creating, saving and closing its form keeps the IPTV category selected. Links from Live TV open the same editor.

Under **Sources → Music streaming**, settings are grouped into **Eclipse** (manifests and registry), **DMM / Real-Debrid** (DMM catalogue, indexer searches and shared accounts), and **Other sources** (AllDebrid, TorBox, Audius and HiFi API). A separate audio diagnostic tests all families.

Under **Sources → Debrid → Edit**, **Show API key** only reveals the selected account’s key to the signed-in owner. **Hide API key** or closing the editor removes the displayed value without changing the saved key.

Under **Sources → Indexing**, **Imported sources** can be filtered and sorted by category, name or content. **Search priority** expands on demand.

## Anime

In **Ether-Remix → Content & libraries → Anime**, create the Anime preset to get popular and top-rated movie/show collections from TMDB. Mixed libraries appear as separate Movies and Shows sections in Plex. Their Anime category is retained after renaming. Anime libraries respect your language filters, including French/MULTi. Original audio and subtitles remain an explicit choice through on-demand exceptions or the Any language option.

Under **Sources → Stremio**, use **Add addon** and paste the `manifest.json` URL generated by your chosen provider. No provider shortlist is imposed or suggested. Sources, activation and priority are shared with Movies & Shows. For discovery, use TMDB collections, AniList lists or an addon providing catalogues.

The **What's new** button in the console header reopens release notes. The popup only shows unread notes since the last viewed version. Closing or acknowledging it remembers the version and read notes in this browser; rebuilding Docker does not repeat them. The previous marker is migrated automatically. **Full history** provides optional access to earlier notes. The sidebar version is provided by the running server; official images include an automatically increasing Remix build number. The footer links to both Ether and Ether-Remix.

### On-demand subtitled exceptions

**Ether-Remix → On-demand subtitles → On-demand subtitled titles** groups movies, shows and anime explicitly allowed without French dubbing. Each addition queues a priority search when background indexing is enabled, within source budgets and playback pauses. These titles also appear in the VOSTFR library. Your existing selection is preserved.

### Public instances or self-hosting

Music sources identify public instances and link to available self-hosting projects: [UltraMax Music](https://github.com/PaRaN01a-hash/ultramax-music) and [HiFi API](https://github.com/binimum/hifi-api). For Stremio, paste your public or private instance manifest; no provider is recommended. Your personal manifest remains your own configuration.

## Settings organization

- **Sources**: one entry for accounts, Stremio addons, BitTorrent/Usenet indexers, Usenet providers, IPTV and music sources. Ether components and configurations are retained. The Indexing category groups enablement, budgets, pace, sortable source selection and library priorities.
- **Languages & quality**: French catalogue, release languages, resolution, codecs, cache and collection exceptions.
- **On-demand subtitles**: explicit non-dubbed title selection and priority searches.
- **Content & libraries**: movies, shows, anime, music, playlists and custom libraries. Mixed libraries remain available under Movies and Shows.
- **Statistics**: Remix indexing plus Ether’s Sources and Usage dashboards.
- **Notifications & tracking**: notifications, Scrob and Last.fm credentials.

Existing `/sources/...` addresses and old Remix tabs redirect to their new locations. Accounts and manifests are not duplicated. Categories organize sources; they do not guarantee that a particular title is available.

### Browsing and poster badges

Plex and Jellyfin prepare a page’s badges together. New episodes and collection rotations respect each viewer’s state. The playable-only catalogue keeps exact counts, including language exceptions.
