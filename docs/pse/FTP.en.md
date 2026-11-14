# FTP / SFTP source

This optional source adds movies and episodes stored on a remote server to Remix search results. It is **off by default**. Configure one server account with multiple folders; no intermediary service is required.

## Setup

Open **Ether-Remix → Sources → FTP / SFTP**. Enter the protocol, hostname (without scheme or port), port, account and folders separated by `;`, such as `/movies ; /shows`. Save before using **Test connection**: actions use saved settings.

- **SFTP** (default, port 22): SSH password or mounted private key. A server `SHA256:…` fingerprint is required. The test displays it without accepting it automatically. Compare it with the fingerprint supplied by your provider or administrator, save it and test again. A changed fingerprint blocks access.
- **Explicit FTPS** (21) or **implicit FTPS** (990): TLS certificate verification. For a self-signed certificate, verify and save the SHA-256 fingerprint shown by the test.
- **FTP** (21): unencrypted connection, including the password. Use only on a trusted network.

Enable the source and scan the folders. Automatic scans run every six hours by default. Searches query the local index, not the remote directory tree. Temporarily unreadable folders retain their previously indexed entries.

## Playback and storage

| Mode | Behaviour |
| --- | --- |
| `stream` (default) | Direct remote playback with seeking. No complete media copy in the download folder; the normal Ether engine cache still applies. |
| `download` | Complete temporary copy before playback. If it is not ready, a message asks you to retry once it finishes. |
| `both` | Immediate remote playback and a kept background copy when actual playback starts. |

Copy modes require a writable download folder; without one Remix falls back to streaming. Searches and background availability checks do not start copies. Manual copies and `both` create **kept** files, excluded from automatic cleanup and the temporary cache cap. Manage their storage separately.

Defaults: one copy at a time, 50 GiB temporary cache, 48-hour retention and 20 GiB minimum free space. Cleanup reclaims old temporary copies to make room. Kept files, active reads and local-library files are protected. Remote files are never deleted. **Keep** preserves a copy; **Stop** cancels a transfer and removes its partial file. Transfers interrupted by a Remix shutdown can resume for the same server in a download-enabled mode.

## Docker volumes

Password-based streaming needs no extra media volume. For SSH key authentication, mount only the required key read-only:

```yaml
volumes:
  - /private/path/id_ed25519:/config/ssh/id_ed25519:ro
```

Enter the container path as the key file. For copy modes, add a dedicated writable folder:

```yaml
volumes:
  - /path/remix-copies:/downloads/ftp
```

Set the download folder to `/downloads/ftp`. Preserve existing service volumes. Passwords and passphrases are masked in the console; also protect configuration files and backups containing them.

## Environment settings

Saved console settings take precedence over environment variables.

| Variable | Value or default |
| --- | --- |
| `PSE_FTP` | `off`; enable with `on` |
| `PSE_FTP_PROTOCOL` | `sftp`, `ftps`, `ftps-implicit`, `ftp` |
| `PSE_FTP_HOST`, `PSE_FTP_PORT`, `PSE_FTP_USER` | Host, port (`0` = protocol default), account |
| `PSE_FTP_PASSWORD` | Password |
| `PSE_FTP_KEY_FILE`, `PSE_FTP_KEY_PASSPHRASE` | SSH key file and optional passphrase |
| `PSE_FTP_HOST_KEY` | SSH or FTPS certificate fingerprint |
| `PSE_FTP_ROOTS`, `PSE_FTP_EXCLUDE` | Folders separated by `;`, excluded names separated by `,` |
| `PSE_FTP_DEPTH`, `PSE_FTP_SCAN_HOURS` | `6`, `6` |
| `PSE_FTP_MODE`, `PSE_FTP_DOWNLOAD_DIR` | `stream`, empty folder |
| `PSE_FTP_CONNECTIONS`, `PSE_FTP_MAX_ACTIVE` | `4` FTP connections, `1` copy |
| `PSE_FTP_CACHE_GB`, `PSE_FTP_CACHE_HOURS`, `PSE_FTP_MIN_FREE` | `50`, `48`, `20` |
| `PSE_FTP_SEARCH_DAILY` | `100000` (environment only) |

## Limits and testing

File or parent-folder names must identify movies and episodes. Archives, samples and videos smaller than 32 MiB are excluded. Scans have depth and entry-count limits. FTP seeking requires `REST`; otherwise use complete downloads. Remote bandwidth and latency directly affect playback. Removed files may remain in cached search results until refreshed; playback checks their presence.

Automated tests cover HTTP ranges, SFTP reconnects, limited FTP connections, copies, cleanup and secret masking. The [lab](../../internal/pse/ftp/testdata/lab/README.md) reads real MKV and MP4 videos through Ether using ffprobe/ffmpeg, including MP4 files with their index at the end. Local tests do not validate remote seedboxes or every TV player.
