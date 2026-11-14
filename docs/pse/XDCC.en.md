# XDCC source (via XDCC Grabscher)

Option under **Sources → XDCC**, off by default. Remix searches the movies and episodes offered by XDCC bots on the IRC networks your [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) follows (CaptainEpix's Mono/Docker fork). It then downloads them on demand, and can play them during the transfer.

> **Files are written to your disk.** Unlike debrid accounts, nothing is read "remotely": Grabscher downloads the file locally and Remix reads it from its folder. Keep this source off if your server must store no media.

## How it works

1. **Search.** Remix sends Grabscher the title, the year (movie) or `S01E02` (episode). Only file names matching the title, year or episode are kept, then ranked by Ether with your usual filters and profiles. Offline, ignored or recently failed bots are left out.
2. **Playback.** When an XDCC release is picked, Remix requests the packet from the bot through Grabscher and waits up to 14 seconds.
3. **Streaming.** If the buffer (8 MB by default) arrives in that time, playback starts on the file being transferred and moves at the bot's pace. Otherwise the viewer gets a message ("the transfer is starting, play again in a moment", "waiting for the bot", "bot offline"…) and Ether tries the other sources on the list.
4. **End.** Once complete, the file stays in Grabscher's folder: temporary cache or kept file, depending on the mode.

Grabscher is a **separate service**. Remix adds no mandatory container: without Grabscher, the source simply stays inactive.

## Docker Compose setup

Remix must **see Grabscher's folders**: the download folder (`dl`) and, for streaming, the temporary folder (`tmp`). The simplest way is to mount them read-only in Remix.

```yaml
services:
  ether-remix:
    image: ghcr.io/aerya/ether-remix:latest
    volumes:
      - ./config:/config
      - ./cache:/cache
      # Grabscher's folders, read-only
      - ./xdcc-grabscher/.config/XG/dl:/xdcc/dl:ro
      - ./xdcc-grabscher/.config/XG/tmp:/xdcc/tmp:ro

  xdcc-grabscher:
    image: ghcr.io/captainepix/xdcc-grabscher:latest
    container_name: xdcc-grabscher
    restart: unless-stopped
    environment:
      # One folder per category: dl/remix (kept) and dl/remix-cache (cache)
      - XG_CATEGORY_FOLDERS=remix,remix-cache
    volumes:
      - ./xdcc-grabscher:/config   # settings, database and downloads
    ports:
      # Web interface for setup, on this machine only.
      # Never "5556:5556" towards the Internet.
      - "127.0.0.1:5556:5556"
```

- **One volume for Grabscher**: `dl` and `tmp` are then on the same disk, and moving a finished file is instant.
- **Permissions**: Grabscher runs as user 99:100. The `./xdcc-grabscher` folder must be writable for it, and its files readable by Remix's user.
- **Read-only**: Remix never deletes anything itself. Cache deletions go through Grabscher.

In the Remix console, **Sources → XDCC**:

| Setting | Value for this example |
|---|---|
| Grabscher address | `http://xdcc-grabscher:5556` |
| Grabscher API key | created in Grabscher, see below |
| Folder mapping (advanced) | `/config/.config/XG=/xdcc` |

The **Test** button checks the connection, the key, the connected IRC servers and that both folders are visible.

### Setting up Grabscher

1. Open `http://127.0.0.1:5556` on the machine (or through an SSH tunnel). Default password: `xgisgreat`, change it.
2. Add your IRC servers and XDCC channels.
3. Under **gear › Api Keys**, create a key **and enable it**: new keys start disabled there.
4. Paste the key in Remix. It is never shown again, never sent to the browser or in an address.

### Grabscher behind a VPN (gluetun)

Grabscher can share a gluetun container's network (`network_mode: "service:gluetun"`) so IRC goes through the VPN.

- Remix then reaches Grabscher by the gluetun container name: `http://gluetun:5556`. Depending on its firewall setup, port 5556 must be allowed from the Docker network.
- Port 5556 must never be opened on the VPN side.

### Passive DCC

Most bots open a port themselves. Some only offer **passive DCC**: Grabscher must then open a port reachable from the Internet. This is the only port to expose, never 5556.

- Without a VPN: forward a few ports on your router (e.g. `50000-50004`), publish them on the container and set `XG_PASSIVE_DCC_PORTS=50000-50004`.
- With gluetun: you need a provider that forwards a port (AirVPN does). Fixed port: `XG_PASSIVE_DCC_PORTS=<port>` and that port in gluetun's `FIREWALL_VPN_INPUT_PORTS`. Changing port: share gluetun's port file and set `XG_PASSIVE_DCC_PORTS_FILE`.
- Without passive DCC, these bots are reported as failed and left out of searches.

Details: [Grabscher's README](https://github.com/CaptainEpix/xdcc-grabscher#passive-dcc).

## The three modes

| Mode | Playback | File |
|---|---|---|
| **Streaming** (default) | during the transfer | temporary cache (`dl/remix-cache`), deleted after the retention time |
| **Streaming and kept file** | during the transfer | kept (`dl/remix`), never deleted by Remix |
| **Download only** | once the file is complete | kept (`dl/remix`) |

Streaming needs Remix to see the `tmp` folder. Otherwise the console says so and playback waits for the transfer to finish. It never offers streaming that does not work.

From the console, a manual search can start a kept download. **Keep** protects a cached file from cleanup without moving it. If deletion has already completed, an error is shown. Starting playback and keeping a file are coordinated with cleanup.

Before a new download, Remix makes room if needed by removing the oldest completed files that are not kept, recently read or inside a library. Limits still apply when several viewers start playback simultaneously.

## Settings

| Setting | Variable | Default | Role |
|---|---|---|---|
| Enable | `PSE_XDCC` | off | Needs the address and the key. |
| Grabscher address | `PSE_XDCC_URL` | | Internal address (Docker or LAN), without credentials. |
| API key | `PSE_XDCC_KEY` | | Key enabled in Grabscher. |
| Mode | `PSE_XDCC_MODE` | `stream` | `stream`, `both` or `download`. |
| Simultaneous downloads | `PSE_XDCC_MAX_ACTIVE` | 2 | Beyond this, playback explains it must wait. |
| Cache size | `PSE_XDCC_CACHE_GB` | 50 | In GB. Beyond this, the oldest unread files are deleted. 0: no cap. |
| Cache retention | `PSE_XDCC_CACHE_HOURS` | 48 | Hours since the last playback. |
| Minimum free space | `PSE_XDCC_MIN_FREE` | 20 | In GB. Below this, no download. 0: no check. |
| Starts per day | `PSE_XDCC_MAX_DAY` | 20 | Applied to playback starts; manual starts may exceed this limit. |
| Buffer before playback | `PSE_XDCC_BUFFER_MB` | 8 | MB received before playback opens. |
| Maximum wait for a slow bot | `PSE_XDCC_WAIT` | `10s` | 2 to 12 s, see "Streaming". |
| Folder mapping | `PSE_XDCC_PATH_MAP` | | `Grabscher folder=Remix folder`, separated by `;`. |
| Temporary folder | `PSE_XDCC_TEMP_DIR` | | Empty: the `tmp` next to the download folder. |
| Results per search | `PSE_XDCC_MAX_RESULTS` | 50 | Packets read per query. |
| Searches per day | `PSE_XDCC_SEARCH_DAILY` | 3000 | The source's cap in Ether. |
| Ignored bots | `PSE_XDCC_BOTS_EXCLUDE` | | Comma-separated names. |

## What is never deleted

Cache cleanup only touches files Remix itself requested in Streaming mode. It always spares:

- kept files ("kept" and "download" modes, manual starts, **Keep** button);
- a file being played;
- any file inside a Remix local library;
- anything Grabscher downloaded without Remix.

If Remix cannot read the library list, it deletes nothing.

## Background indexing

Indexing only queries XDCC when it is checked under **Background indexing → Imported sources**. Its searches follow the source's cap.

An indexed packet is not enough to make a title "playable": it only counts if it was seen again on an online bot, with no recent failure, within the last 48 hours. It is never marked "cached": it has to be downloaded first. Even when counted, it may wait in the bot's queue.

## States in the console

| State | Meaning |
|---|---|
| Available | Packet found on an online bot. |
| Bot offline | Bot absent from the IRC network. |
| Waiting | Requested, the bot is not sending yet (queue, slots taken). |
| Downloading | Transfer under way, with speed and progress. |
| Ready to play (downloading) | Enough bytes received to play during the transfer. |
| Ready to play | Complete file. |
| Failed | Silent bot, refusal, passive DCC impossible… with Grabscher's reason. |

The same packet is never requested twice: several viewers share one transfer.

## Streaming: what works, what does not

XDCC sends a file **in order, from start to end**. Grabscher cannot ask the bot for another position. Remix respects that, without inventing bytes:

- **Playing from the start**: starts once the buffer is received and follows the transfer.
- **Seeking back**: instant, within the received part.
- **Seeking forward within the received part**: instant.
- **Seeking beyond the received part**: Remix waits at most the set delay (10 s by default) if the bytes arrive in time. Otherwise it answers "not available yet" at once. The player can retry; the rest of the movie is not blocked.
- **Formats**: MKV and "web-optimised" MP4 play during the transfer. An MP4 with its index at the end, or an AVI, only plays once complete; the viewer is told.
- **Speed**: if the bot sends slower than the movie's bitrate, playback pauses to buffer. Nothing avoids that except waiting for the transfer to end.

### Measured results (fake bot lab, no public bot)

With the lab shipped by Grabscher (`tools/lab`), a bot at 2 MB/s and a real 2-minute H.264/AAC MKV:

- playable **4.3 s** after the click (8.5 MB received out of 47);
- ffprobe and ffmpeg (the engine of Jellyfin and Emby) read and decode during the transfer;
- a seek to 100 s with 18 % received: clean refusal, no fake frames;
- full read through Ether's engine: bytes identical to the original;
- MP4 with the index at the end: "only plays once complete" message, then normal playback;
- silent bot: failure explained after Grabscher's delay; refusing bot: failure explained; those packets are no longer offered afterwards.

## Known limits

- **Source order.** Ether ranks by quality first. An XDCC release better than a debrid one can lead: its download starts, then Ether plays the next debrid release if the bot is not ready. To avoid this, put XDCC after your other sources in Ether's source order, or keep it for titles found nowhere else.
- **Restart.** Local playback links change on every Remix start. Play again; the transfer itself keeps going in Grabscher.
- **Search by name only.** XDCC packets only have a file name: no IMDb or TMDB id. Season packs and absolute anime numbering are not recognised.
- **No guarantee of instant playback.** Bots have their own queues and limits; Remix shows them but cannot bypass them.

## Testing with the lab

Real integration tests are skipped by default. Start Grabscher's lab (`tools/lab/run.sh`, simulated IRC bots, no Internet), then:

```bash
XDCC_LAB_URL=http://127.0.0.1:15556 XDCC_LAB_KEY=<lab key> XDCC_LAB_MAP=/state=<host folder> go test ./internal/pse/xdcc/ -run TestLab -v
```

`XDCC_LAB_MEDIA` (folder of the videos served by the bot) adds the byte-for-byte comparison. The scenario, the fake bot tweak (it serves real videos) and every command are in [`internal/pse/xdcc/testdata/lab`](../../internal/pse/xdcc/testdata/lab/README.md).

## Credits

[XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher), CaptainEpix's Mono/Docker fork of Lars Formella's XG project. Remix ships none of its code and uses it through its APIs.
