# Catalogue and indexing

With the **playable-only** catalogue, Remix only shows a title from its local catalogue with enough indexed releases (2 by default) matching the chosen filter, for example French or MULTi, cached on debrid. **VOSTFR** titles and language exceptions count releases in any language.

- **Filtered lists**: home, Movies, TV shows, recommendations. Search, watchlist, collections and continue watching are not filtered: a chosen title stays reachable, and playback queries sources live.
- **Search order**: TMDB popularity, highest first, alternating movies and shows. VOSTFR titles and language exceptions go first, then prioritized collections and libraries.
- **Immediate search**: adding a VOSTFR title, the **Search now** button, and a search in Plex or Jellyfin. For a search, the first 5 titles found move to the front of the queue if they were never searched, or found nothing more than 6 h ago.
- **Risky titles**: the **Also show risky titles** option keeps titles with fewer releases than the minimum (at least one). Plex and Jellyfin flag them with “⚠ 1 source” at the start of the summary, a “1 SOURCE” badge on Ether posters, or both. With another poster tool, pick the summary.
- **Local catalogue**: Ether does not list all of TMDB, only titles popular enough and already released, per **Catalog → Hide obscure titles**. Remix searches releases within that set.

Availability confirmed by [SeerrCatalog](SEERRCATALOG.en.md) follows its criteria and remains confirmed until deletion.

## Refresh intervals

**TMDB metadata**

| Data | Frequency |
| --- | --- |
| Full catalogue | Every 24 h (configurable) |
| New releases and trending | Every 4 h |
| Missing detailed records | In batches every 90 s, recent releases and popular titles first |
| Ongoing series | Record refreshed on each catalogue cycle; 1 h at most when an active season is displayed |

**Release search**

| Last search result | Next attempt after |
| --- | --- |
| Title released less than 60 days ago | 2 days |
| No release found | 7 days |
| Releases found | 14 days |
| Source down or partial answer | 24 h |

These intervals are minimums. Searching depends on each source's daily budget (1000 requests by default) and the delay between two titles (10 s by default), and pauses during playback: on a large catalogue, a title may wait longer. A release not seen again for 60 days no longer counts.

Under **Sources → Indexing**, the collapsible **Search priority** block moves a collection or library ahead of the rest: *High* retries a title without releases after 1 day, *Maximum* after 6 h. **Search now** skips these delays for the selection. Per-source budgets still apply. On import, SeerrCatalog (hourly refresh, maximum priority) and RSS Catalog (every 3 h, high priority) are recognized and preset; you can change these settings.

**Collections and libraries**

Each collection has its own schedule: `live` (on demand), `hourly`, `daily` (default), `weekly` or every N hours, from 1 to 168. Some sources enforce a minimum delay. Libraries update along with their collections. This schedule is independent of release-search priority; **Refresh**, in the same section, immediately refreshes a collection or every collection in a library.
