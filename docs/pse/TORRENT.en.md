# Torrent client (qBittorrent, rTorrent / ruTorrent)

Option under **Sources → Torrent client**, off by default. Remix sends torrent releases found on your indexers (Prowlarr, Jackett) to your BitTorrent client, then plays them once downloaded: movies, shows, anime and music.

Unlike debrid accounts, **files are stored on the client's disk** and seeded within your limits. Remix never deletes a torrent or a file.

## Requirements

Remix reads finished files straight from disk: its container must see the client's download folder, like Radarr or Sonarr.

```yaml
services:
  accent:
    volumes:
      - /srv/torrents:/downloads:ro   # same folder as the client
  qbittorrent:
    volumes:
      - /srv/torrents:/downloads
```

When both containers mount the folder at different paths, fill in **Folder mapping** (advanced options), e.g. `/downloads=/data/torrents`. Remix reads nothing outside the mapped folders.

## Settings

| Setting | Variable | Purpose |
|---|---|---|
| Enable | `PSE_TORRENT=on` | Off by default. |
| Client | `PSE_TORRENT_CLIENT` | `qbittorrent` (default) or `rtorrent`. |
| Client address | `PSE_TORRENT_URL` | qBittorrent: `http://user:password@qbittorrent:8080`. rTorrent: XML-RPC address, `http://user:password@rutorrent/RPC2` or ruTorrent's `…/plugins/rpc/rpc.php`. |
| Download what is missing | `PSE_TORRENT_DOWNLOAD` | On by default. Off: Remix only plays torrents already complete on the client. |
| Music | `PSE_TORRENT_MUSIC` | On by default. One album at a time. |
| Ratio | `PSE_TORRENT_RATIO` | E.g. `1`. Empty: the client's setting. |
| Seeding time | `PSE_TORRENT_SEED_TIME` | E.g. `72h`. Empty: the client's setting. |
| Additions per day | `PSE_TORRENT_MAX_DAY` | 10 by default, to protect your tracker accounts. |
| cross-seed | `PSE_TORRENT_XSEED_URL`, `PSE_TORRENT_XSEED_KEY` | Optional, e.g. `http://cross-seed:2468` and its API key (`cross-seed api-key`). |
| Category | `PSE_TORRENT_CATEGORY` | `remix` by default (qBittorrent category or ruTorrent label). |
| Download folder | `PSE_TORRENT_SAVE_PATH` | Path as seen by the client. Empty: the client's own. |
| Folder mapping | `PSE_TORRENT_PATH_MAP` | `client folder=Remix folder`, separated by `;`. |

## How it works

- **Playback**: a torrent complete on the client plays like a cached file, including one you added yourself. When no source is available, Remix sends the best release to the client; the player says the download started. Play again once it finishes.
- **Debrid**: your debrid accounts stay cached-only. Only the torrent client downloads.
- **Checks**: background indexing and checks never download anything.
- **Seeding**: qBittorrent enforces the ratio and time set on each torrent; its action when reached (pause, stop) stays the one set in qBittorrent. With rTorrent, Remix stops seeding every 5 minutes once a limit is reached, deleting nothing. The first limit reached ends seeding, as in qBittorrent.
- **cross-seed**: notified when each download in the Remix category completes, at most 3 torrents every 5 minutes. The notification is recorded on the client (`remix-xseed` tag in qBittorrent), so tracker searches are never triggered twice.
- **Music**: a missing album is sent to the client, at most one every 2 minutes, so a playlist cannot send dozens of albums at once. It plays once downloaded.

## Limits

- Playback starts once the download is complete, not during it.
- Magnets carry the trackers of the original `.torrent`: the client fetches metadata from peers.
- In cluster mode, only the node that sees the download folder can play these files.
- The daily addition cap resets when Remix restarts.
