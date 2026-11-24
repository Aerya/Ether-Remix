# aMule and Soulseek (slskd)

These optional sources search for movies, episodes and music, then download on playback. They are disabled by default. **Playback requires a complete download**: retry after the transfer finishes. Searches and availability checks never start downloads.

Under **Remix → Sources**, enable **Expert** mode and open **aMule** or **Soulseek (slskd)**. Save settings before testing the connection.

- aMule requires its official `amuleapi` REST interface (`/api/v1`) and administrator password. Use its base address, usually `http://amule:4713`, not the EC port or old amuleweb interface. The project moved to [amule-org/amule](https://github.com/amule-org/amule); see the [amuleapi quick start](https://github.com/amule-org/amule/blob/master/docs/QUICKSTART-AMULEAPI.md). Searches use connected eD2k servers (`global`).
- slskd requires a read/write API key and the download batches endpoint `POST /api/v0/transfers/downloads/batches`, supporting `options.destination`. Base address: usually `http://slskd:5030`. Remix files go under `remix/<identifier>/` in its downloads directory. Configure the Soulseek account and sharing in slskd; see its [configuration guide](https://github.com/slskd/slskd/blob/master/docs/config.md).
- Mount the completed downloads directory in Remix, preferably read-only. Supply its local path. For aMule, also supply the daemon's path to the same directory when different. Subdirectories are preserved; files outside the mount are rejected.

Settings: `PSE_AMULE` / `PSE_SLSKD` (`off` by default), corresponding `_URL`, `_KEY` and `_ROOT` variables. `PSE_AMULE_REMOTE_ROOT` maps the daemon's completed directory to the Remix mount; empty means the same path. Secrets remain masked and are sent in request headers or bodies. HTTP redirects are not followed.

Both connectors participate in video searches using Ether's filters and budgets. Movie names must match title and year; episodes must match title and episode numbers. Uncertain matches are rejected. For music, the Deezer catalogue provides discovery, while these connectors are playback fallbacks after existing sources. The remote path/name must identify the artist, album and exact track without substituting a live/remix version.

The panel also supports manual searches, explicit downloads and transfer refresh. Persistent task records prevent duplicate launches after restart. Limits per connector: two active downloads and twenty starts per rolling 24 hours. Files are retained; use the external client to cancel transfers or manage sharing and cleanup. No automatic deletion occurs.

Playback checks completion and file size, supports video HTTP byte ranges through a local token server and opens audio directly within the mount. Symlinks cannot escape the mount. Add the mount as a local library with appropriate sharing permissions to browse downloaded files independently of on-demand catalogue playback.

Older aMule versions without amuleapi and slskd versions without the batches API are incompatible. The connectors do not install clients, change network settings or stream incomplete files.

Music playback retries use the recorded transfer before running another search, avoiding a second copy. Numeric titles (for example “1979”) are preserved when stripping a track number.
