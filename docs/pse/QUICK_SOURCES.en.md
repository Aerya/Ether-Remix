# Limit debrid usage, or even prefer BitTorrent/Usenet download & streaming

[Remix guide](GUIDE.en.md) · [Français](QUICK_SOURCES.fr.md)

Under **Remix → Playback → Priorities**, enable **Quick setup**, which is off by default. Select one or more sources, then order them freely with **↑ / ↓** or drag and drop: every source can be moved, checked or not, and a checked source keeps the position it is in. Checked sources are tried from top to bottom. Click **Save**. The selection, full order and Seedstrem instance survive restarts. Turning quick setup off restores advanced behavior without deleting its settings.

| Source | Existing configuration |
| --- | --- |
| StreamFusion Reborn — Alkodiques only | PostgreSQL under Sources → Stremio and an enabled AllDebrid account. Only verified `sfr-alko:` files: no ordinary torrents, magnets or cache discovery. |
| Usenet | Enabled NNTP providers and Usenet indexers. |
| BitTorrent — Ether-Remix remote client | Native torrent client and BitTorrent indexers. Shared directories, path mappings, quotas and download limits still apply. |
| BitTorrent — Seedstrem | Explicitly select the installed instance from the Stremio addons. Playback links remain HTTP and use Seedstrem’s remote client. |

Unconfigured sources display a link to their settings. “Configured” describes saved settings, not a network check or an availability guarantee. No account, indexer or PostgreSQL permission is created automatically. For Usenet, also configure indexers under Sources → Indexers.

## Ordering and fallback

Movies, show episodes and anime follow the same order. Remix searches the first source, applies language, quality, identity and compatibility filters, then tries its usable releases. Quality fallbacks below profile requirements do not replace a matching release from the next source. Empty results, rejected candidates, unavailable clients and playback failures allow the next source to run.

After all selected sources fail, other enabled Stremio addons are queried, including Debrider when configured, using their usual search and rescue mechanisms. With no sources selected, only these other addons are queried. The selected Seedstrem instance is not queried again as a final fallback.

The existing manager keeps its caches, budgets, timeouts and pauses. Each stage retains the validator’s existing limits; results already examined are not retried during the same resolution. Playback failover excludes failed releases. Indexed references, explicit version choices and pins cannot jump ahead of quick source order; these settings remain saved for advanced mode.

Indexing and browsing never start Seedstrem downloads. The native torrent client retains its safeguards and only queues a download for requested playback when its download setting allows it. A pending download does not block other sources. Network routes, MediaFlow, AllDebrid quotas and PostgreSQL permissions remain as configured.

Local media and music retain their usual paths. XDCC, FTP and IPTV are not additional quick-setup stages. Turn quick setup off for these sources and advanced preferences.
