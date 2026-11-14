# SeerrCatalog integration

Under **Sources → Stremio → SeerrCatalog**, one section groups **Availability to Remix** (import SeerrCatalog results) and **Search via Remix** (searches requested by SeerrCatalog). Each feature keeps its own switch and token. Test availability sharing in Remix and searches in SeerrCatalog. Standalone Prowlarr has separate settings in SeerrCatalog.

1. In SeerrCatalog, open **Settings → Ether Remix integration**, generate a token and enable export.
2. In Remix, open **Sources → Stremio → SeerrCatalog → Availability to Remix**, enter the base address (e.g. `https://seerrcatalog.example`, without `/api/…`) and token, enable and save. **Test connection** checks the feed without importing; synchronization runs every minute.
3. The updated SeerrCatalog automatically exports previously confirmed titles without running new searches.

SeerrCatalog availability passes the **playable-only** filter without querying addons again. Hidden or upcoming titles do not block synchronization of other availability records. Catalogue filters and viewer playback policies still apply. Imports retrieve TMDB metadata. Remix keeps two collections, for movies and shows: **SeerrCatalog** (every available title) and **SeerrCatalog · VOSTFR** (titles SeerrCatalog only found as a fallback: French subtitles or original audio with external subtitles). The latter is placed in the **VOSTFR** library. Those titles never show in Movies, Shows or the home screen; the Remix indexer keeps looking for a French version.

**Confirmed availability persists until the media is deleted** in SeerrCatalog. Older unconfirmed observations expire after 26 h. Playback URLs and headers are reused for at most **30 min**; a retained torrent hash remains usable for confirmed media. Expired, policy-rejected or failed sources fall back to normal discovery. URLs may expire earlier or depend on IP restrictions. Only checked episodes provide reusable sources.

Paginated synchronization recovers changes and deletions after outages; incomplete imports preserve valid data. Sensitive sources stay in memory, outside catalogue APIs, and reload on restart. Disabling removes integration trust. The token shares sources from **all SeerrCatalog users**: use a trusted Remix instance and HTTPS outside private networks.

Equivalent variables: `PSE_SEERRCATALOG=on`, `PSE_SEERRCATALOG_URL=http://seerrcatalog:7000`, `PSE_SEERRCATALOG_TOKEN=…`. Disabled by default.

## Complementary search

Open **SeerrCatalog → Settings → Ether Remix integration → Search via Remix**, enable search and enter the Remix base URL. Generate a **dedicated search token** in that section, then paste it into **Remix → Sources → Stremio → SeerrCatalog → Search via Remix** and enable searches. Save on both sides.

Availability sharing has its own token and can remain disabled. After updating both applications, configure this new token: the old sharing token no longer authorizes searches. When addons return no primary-language release and user fallback is enabled, SeerrCatalog asks Remix to search its configured sources, including Prowlarr. SeerrCatalog still applies primary/fallback tags, deduplication and thresholds, keeping J+1/J+3 checks and permanent confirmation. Searches respect availability budgets and cannot circularly reuse SeerrCatalog observations. Torrents with infoHashes and actual HTTP streams can be returned; NZB URLs are never exposed as video streams. Test the connection from SeerrCatalog without launching a search.

For external and translated SRTs, see the [SubMaker guide](SUBMAKER.en.md). The [SeerrCatalog README](https://github.com/Aerya/Stremio-Seerr-Catalog#intégration-ether-remix) documents the HTTP contract.
