# Vela release distribution

Owners open **Apps → Vela** to check the stable release channel and explicitly
choose **Install Vela**, **Update**, or **Roll back**. There are no automatic
asset downloads or viewer-path checks. An installation can be cancelled from the
console; closing its tab cancels the active request as well.

The default descriptor is
`https://github.com/braedonsaunders/vela/releases/download/vela-stable/channel.json`,
with a detached base64 Ed25519 signature at the same URL plus `.sig`. Verification
uses the exact descriptor bytes before JSON decoding. The production Vela public
key is compiled into `internal/veladist`; this key is independent of Ether's
self-update key. Two string pins can be overridden using linker flags:

```
-X github.com/braedonsaunders/ether/internal/veladist.PublicKeyB64=<current>
-X github.com/braedonsaunders/ether/internal/veladist.NextPublicKeyB64=<next>
```

The next pin is empty by default. Lab builds replace the current pin with the
lab public key. Production must never include a lab pin. Set a release Ether
version too (for example `internal/updater.Version=0.3.13`); unversioned `dev`
builds cannot satisfy a descriptor's minimum Ether version.

Owner config accepts `{"vela":{"channelURL":"https://…/channel.json"}}`, also
available through the existing `PATCH /ether/api/config/vela` section API.
An explicitly configured `file:///absolute/path/channel.json` allows local
lab assets; it still requires a pinned signature. Otherwise every download and
redirect must use HTTPS. No key or release URLs are returned in distribution
status responses.

Owner-only endpoints (normal admin header authentication and mutating-request
CSRF checks apply):

| Method | Path under `/ether/api` | Result |
| --- | --- | --- |
| GET | `/vela/status` | Installed and available versions, installed/rollback flags, busy phase, downloaded/total bytes, last error |
| POST | `/vela/check` | Fetch and verify descriptor only; return status |
| POST | `/vela/install` | Install the checked newer version; return status |
| POST | `/vela/rollback` | Activate newest local rollback; return status |

POST bodies are `{}`. Check timeout is 30 seconds; installation timeout is 15
minutes. Descriptor and manifest reads are capped at 8 MiB, archives at 512 MiB,
and extracted regular files at the signed inventory total (at most 2 GiB).
Tar links and special files are rejected, as are traversal paths and duplicate
regular files. Every full-inventory file is hashed, and each signed engine
inventory must agree exactly with the corresponding full-inventory subtree.

Files stage under `data/clients/.vela-staging-<version>`. Verification failure
leaves live Vela intact. Activation renames live to `.vela-rollback-<timestamp>`,
then staging to `vela`, restoring the previous live directory if activation
fails. Rollbacks are ordered by directory modification time, stamped when taken; retention
always protects the rollback just created and keeps two. Owner-staged `diagnostic-core-*`,
`core-app-*.html`, `universal/<key>`, and `engines/cinematic/<id>` items absent
from the new build are copied before activation without replacing release
items. The same carry-over runs before rollback, including root-level owner JSON
files excluded from the outgoing build inventory. Verified inventories are saved
as `.ether-inventory.json` for offline classification; pre-existing manual builds
can provide their full `vela-*.manifest.json`. Standard shipped root manifests
(`app.version.json`, `engines.json`, `manifest.json`) and internal metadata are
always excluded from additive JSON carry-over. The installed descriptor is
recorded as `.ether-release.json`.
Existing manual installs are recognized through `app.version.json` or a
`vela-*.manifest.json`; an unknown installed version must be identified before
updating, to avoid accidental downgrades.

Mock previews: `/ether/apps/vela`, `?vela`, `?vela&velaDist=installing`, and
`?vela&velaDist=rollback`. The mock never downloads release assets.
