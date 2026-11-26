# Brand marks

Local copies only. The app does not fetch logos at runtime.

Each SVG is the path geometry from [Simple Icons](https://github.com/simple-icons/simple-icons), which publishes these marks under [CC0-1.0](https://github.com/simple-icons/simple-icons/blob/11.14.0/LICENSE.md). The app, integration, and catalogue marks were retrieved 2026-10-30 from v11.14.0 (`https://raw.githubusercontent.com/simple-icons/simple-icons/11.14.0/icons/<slug>.svg`). Debrid-Link and Stremio were not in that release; they were retrieved 2026-10-31 from v16.36.0 under the same CC0 terms. DLNA was retrieved 2026-11-24 from v16.38.1 (Simple Icons' source: the DLNA logo on Wikimedia Commons, `File:DLNA_logo.svg`); DLNA is a trademark of the Digital Living Network Alliance and identifies the protocol only.

Sanitized on retrieval: XML declaration, titles, roles, and every element other than one `viewBox="0 0 24 24"` root and its `path` data were removed. Fill is `currentColor`. No scripts, external references, or event handlers.

| File                   | Simple Icons slug  | Version |
| ---------------------- | ------------------ | ------- |
| `emby.svg`             | `emby`             | 11.14.0 |
| `jellyfin.svg`         | `jellyfin`         | 11.14.0 |
| `plex.svg`             | `plex`             | 11.14.0 |
| `simkl.svg`            | `simkl`            | 11.14.0 |
| `themoviedatabase.svg` | `themoviedatabase` | 11.14.0 |
| `trakt.svg`            | `trakt`            | 11.14.0 |
| `debridlink.svg`       | `debridlink`       | 16.36.0 |
| `stremio.svg`          | `stremio`          | 16.36.0 |
| `amd.svg`              | `amd`              | 16.36.0 |
| `apple.svg`            | `apple`            | 16.36.0 |
| `intel.svg`            | `intel`            | 16.36.0 |
| `nvidia.svg`           | `nvidia`           | 16.36.0 |
| `dlna.svg`             | `dlna`             | 16.38.1 |

`silo.png` is Silo's own app icon, the `web-app-icon-512.png` its server publishes in `site.webmanifest`, retrieved 2026-10-31 from the reference Silo server and scaled to 112×112. It is shown only on the Silo app card, to identify the app Ether serves.

`opensubsonic.svg` is the OpenSubsonic logo from the OpenSubsonic project website ([opensubsonic/opensubsonic.github.io](https://github.com/opensubsonic/opensubsonic.github.io), `assets/icons/logo.svg`, commit `210552e41fdc3b057ef4a1c5ab61a01e6d8fc5f2`), licensed under the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0). Modified: the path was redrawn in white with its transforms applied, centred on a rounded `#1e4e79` tile. It is byte-identical to the adapter icon `ether.music` declares, so a Subsonic adapter looks the same before and after its plugin ships an icon. It identifies the Subsonic/OpenSubsonic protocol and does not imply endorsement.

`webdav.svg`, `m3u.svg` and `xtream.svg` are Ether's own marks (GPL-3.0 with the rest of this repository), drawn 2026-11-24 because none of these protocols has an official or licensable logo: a cloud with "DAV" (WebDAV), a playlist file with "M3U" (M3U & XMLTV), and a TV with "IPTV" (Xtream-compatible IPTV apps; the defunct Xtream Codes logo is deliberately not used). Each is one 24×24 single-colour shape with its lettering knocked out through an internal mask; no fonts, scripts or external references.

No vendored mark exists for OpenSubtitles, Prowlarr, OMDb, MDBList, Real-Debrid, TorBox, AllDebrid, Premiumize, or Offcloud. Do not invent those logos. Usenet providers and tracker sites keep a monogram of their name.
