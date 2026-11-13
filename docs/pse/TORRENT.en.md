# Torrent client (qBittorrent, rTorrent / ruTorrent)

Option under **Sources → Torrent client**, off by default. Remix sends torrent releases found on your indexers (Prowlarr, Jackett) to your BitTorrent client, then plays them once downloaded: movies, shows, anime and music. Up to three clients.

Unlike debrid accounts, **files are stored on the client's disk** and seeded within your limits. Remix never deletes a torrent or a file.

## Shared folder: required for playback

The client can run on another machine, but **Remix reads the files from its own disk**. Its container must therefore see the client's download folder: same machine, or a mounted network share (NFS, SMB). With a remote seedbox and no shared folder, adding works but playback does not.

```yaml
services:
  accent:
    volumes:
      - /srv/torrents:/downloads:ro   # same folder as the client
  qbittorrent:
    volumes:
      - /srv/torrents:/downloads
```

When Remix does not see the folder at the client's path, fill in **Folder mapping** (advanced options), e.g. `/downloads=/data/torrents`. Remix reads nothing outside the mapped folders.

## Connecting to the client

- **qBittorrent**: web UI address.
  - With credentials: `http://user:password@qbittorrent:8080`.
  - With an **API key** (qBittorrent 5.2 and later, *Options › Web UI › API key*): address without credentials, key in **qBittorrent API key**.
  - With nothing, when qBittorrent bypasses authentication on the local network.
- **rTorrent / ruTorrent**: XML-RPC address, `http://user:password@rutorrent/RPC2` or ruTorrent's `…/plugins/rpc/rpc.php`. rTorrent has no API key.

The password and the key are never shown again in the console.

## Settings

Main client (`PSE_TORRENT_…`); clients 2 and 3 in their own blocks (`PSE_TORRENT2`, `PSE_TORRENT2_…`, `PSE_TORRENT3_…`, same fields).

| Setting | Variable | Purpose |
|---|---|---|
| Enable | `PSE_TORRENT=on` | Off by default. Clients 2 and 3: `PSE_TORRENT2=on`, `PSE_TORRENT3=on`. |
| Client | `PSE_TORRENT_CLIENT` | `qbittorrent` (default) or `rtorrent`. |
| Client address | `PSE_TORRENT_URL` | See “Connecting to the client”. |
| qBittorrent API key | `PSE_TORRENT_KEY` | Replaces the credentials (qBittorrent 5.2 and later). |
| Ratio | `PSE_TORRENT_RATIO` | E.g. `1`. Empty: the client's setting. |
| Seeding time | `PSE_TORRENT_SEED_TIME` | E.g. `72h`. Empty: the client's setting. |
| Download folder | `PSE_TORRENT_SAVE_PATH` | Path as seen by the client. Empty: the client's own. Also used to measure free space. |
| Folder mapping | `PSE_TORRENT_PATH_MAP` | `client folder=Remix folder`, separated by `;`. |
| Download what is missing | `PSE_TORRENT_DOWNLOAD` | On by default. Off: Remix only plays torrents already complete. |
| Music | `PSE_TORRENT_MUSIC` | On by default. One album at a time. |
| Per-tracker limits | `PSE_TORRENT_TRACKERS` | E.g. `c411=2/72h; example.org=1; other=/168h`. See below. |
| Additions per day | `PSE_TORRENT_MAX_DAY` | 10 by default, all clients together. |
| Minimum free space | `PSE_TORRENT_MIN_FREE` | In GB, 20 by default; 0: no check. |
| cross-seed | `PSE_TORRENT_XSEED_URL`, `PSE_TORRENT_XSEED_KEY` | Optional, e.g. `http://cross-seed:2468` and its API key (`cross-seed api-key`). |
| Category | `PSE_TORRENT_CATEGORY` | `remix` by default (qBittorrent category or ruTorrent label), on every client. |

## Seeding limits

- Each torrent gets its client's ratio and seeding time when added.
- A **tracker rule** overrides them, field by field: `c411=2/72h` gives ratio 2 and 72 h to torrents whose announce address contains “c411”. `example.org=1` only changes the ratio, `other=/168h` only the time. The first matching rule applies.
- The first limit reached (ratio or time) ends seeding, deleting nothing.
- qBittorrent enforces these limits itself; its action when reached (pause, stop) stays the one set in qBittorrent. With rTorrent, Remix stores the limits on the torrent and stops seeding every 5 minutes once one is reached.

## Several clients

- A torrent complete on any client plays.
- A new download goes to the first client with enough space that accepts it: the main one, then 2, then 3.

## Free space

- Remix measures the free space of each client's download folder, as Remix sees it (the configured folder, otherwise the client's own).
- Below the threshold, Remix sends nothing more to that client. When no client has room, the player says “Not enough free space on the torrent client”.
- One alert goes out through Ether notifications (“Provider unavailable” event), then a message once space is back (“Provider recovered”). Enable these events in Ether notifications to receive them (Discord, ntfy, email…).
- The console shows each client's free space.

## While downloading

- Playback starts once the torrent reaches 100%, not before.
- **Plex** shows instead of the video: “Downloading on the torrent client: 42%, about 12 min left. Play again once it finishes.”
- **Jellyfin and Emby** show the same message on screen, alongside their playback error.
- **The console** (Sources → Torrent client) lists Remix downloads: progress, time left, size, ratio and client. **Refresh** button.
- Nothing tells you automatically when a download finishes: play again.

## How it works

- **Debrid**: your debrid accounts stay cached-only. Only the torrent clients download.
- **Checks**: background indexing and checks never download anything.
- **cross-seed**: notified when each download in the Remix category completes, at most 3 torrents every 5 minutes. The notification is recorded on the client (`remix-xseed` tag in qBittorrent), so tracker searches are never triggered twice.
- **Music**: a missing album is sent to the client, at most one every 2 minutes, so a playlist cannot send dozens of albums at once.

## Limits

- Magnets carry the trackers of the original `.torrent`: the client fetches metadata from peers.
- In cluster mode, only the node that sees the download folder can play these files.
- The daily addition cap resets when Remix restarts.

A complete copy takes priority over an unfinished copy on another client. An offline client does not block new downloads on healthy clients. Pending albums are checked again on the next playback attempt after a minimum delay of 5 seconds. Music diagnostics never start downloads.
