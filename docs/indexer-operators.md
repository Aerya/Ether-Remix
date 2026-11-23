# Ether for Indexer Operators

Ether is a self-hosted personal media server that searches Newznab and Torznab indexers on behalf of its owner. Ether identifies itself honestly by default and is built to stay within per-indexer quotas and rate limits.

## User-Agent

By default, Ether sends:

```text
Ether/<version> (+https://github.com/braedonsaunders/ether/blob/main/docs/indexer-operators.md)
```

`<version>` is the release version (for example `0.3.17`), or `dev` on unversioned builds. Server owners can optionally configure a compatibility rule for indexers that only accept specific client user agents, but Ether ships with honest identification enabled by default.

## Requests Ether Makes and When

Ether only talks to indexers that the server owner explicitly configures with their own API key. It makes three kinds of requests:

1. **Capabilities (`t=caps`)** — fetched lazily before searching when no fresh cached copy exists, or when the owner runs a connection test in the console.
2. **Searches (`t=movie`, `t=tvsearch`, `t=search`)** — primarily driven by viewer play intent:
   - **Pressing Play:** searches configured indexers using the title's external IDs first (`imdbid`, `tmdbid`, `tvdbid`, etc.) and only tries fallback queries when needed.
   - **Opening a title's detail page:** when enabled (`cache.detailWarmup`, on by default and configurable to `off`), opening a detail page on a previously seen playback device runs a single primary targeted query (no fallback fan-out) to warm playback, capped at 15% of the indexer's daily API limit.
   - **Next-episode warm-up & watchlist checks:** when used, send only the primary targeted query (no fallback fan-out) and share a separate 10% cap of the indexer's daily limit.
   - **Optional recent-release monitoring:** when enabled by the owner (`availability.monitor`, configurable and switchable to digital-date mode), polls recent category feeds (`t=search`) on a bounded schedule (default every 15 minutes, up to 5 pages) while reserving 80% of each indexer's daily cap for playback by default.
   - **Search result caching:** successful searches are cached for 6 hours by default, scaling up to 7 days for titles older than a year. Empty searches are cached for 3 minutes for titles released within the past week and 30 minutes for older titles.
3. **NZB and torrent downloads** — `t=get` or feed-supplied NZB / `.torrent` URLs when a chosen release is grabbed. Concurrent fetches for the same release are coalesced in flight, and downloaded NZB/torrent payloads are cached locally so Ether does not re-download the same file.

## Capabilities (`t=caps`) Caching

- Successful `t=caps` responses are cached for **24 hours** per `(indexer, endpoint)` both in memory and in Ether's database, persisting across restarts.
- Failed `t=caps` requests back off for **1 hour** before Ether tries `t=caps` again.

## Rate Limits, Quotas, and Backoff

Ether enforces per-indexer limits locally and respects server-side signals:

- **Account-reported limits:** Ether reads `<newznab:apilimits apimax="…" grabmax="…"/>` from XML or JSON responses and persists them across restarts so background shares stay proportional to the account's real allowance (assuming 1,000/day when neither reported nor configured).
- **Owner-configured daily caps:** Owners can set per-indexer UTC daily API (`dailyLimit`) and download (`dailyGrabs`) ceilings. Usage is tracked atomically in the database across restarts and cluster nodes; once reached, Ether stops sending requests locally. Indexers with a configured `dailyLimit` also run fallback queries serially instead of in parallel.
- **HTTP `429` and quota error documents:** On HTTP `429` or Newznab/Torznab quota errors (error codes `500`/`501` or limit-reached descriptions), Ether immediately pauses requests to that indexer across the cluster. It honours `Retry-After` (delta-seconds or HTTP-date, bounded to 24 hours) and pauses for **15 minutes** when no `Retry-After` header is provided.
- **Per-host concurrency limit:** Ether bounds concurrent in-flight requests per origin (`scheme://host:port`) to **8** by default.
- **Circuit breaker:** After **3** consecutive transport or `5xx` failures, Ether pauses the indexer starting at **2 minutes** (doubling up to **30 minutes**) and probes with a single request before ramping concurrency back (`1 -> 2 -> 4 -> full`).

## No AI or LLM in Indexer Request Paths

No AI model or LLM is ever used in an indexer request path — not for capabilities, not for search queries, and not for NZB or torrent downloads.

## Contact

If you operate an indexer and have questions, see unexpected traffic from Ether, or would like to report a compatibility issue, please open an issue on GitHub:

<https://github.com/braedonsaunders/ether/issues>
