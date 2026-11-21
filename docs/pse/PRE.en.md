# Pre (beta)

**Optional, beta and off by default.** The public feeds and personal API formats have not yet been proven in real conditions.

A “pre” is the announcement of a release. Remix reads these announcements and keeps **FRENCH / VF**, **MULTi** and **VOSTFR / SUBFRENCH** ones. It assumes an announced release is available, within about an hour, on your usual sources (debrid, BitTorrent, Usenet). It:

- counts as an **indexed release** (“playable only” catalogue, priorities);
- adds its movie or show to the catalogue **without waiting** for it to arrive through TMDB.

A pre holds **no link and no hash**: it is never used for playback. Playback always goes through your sources.

## Enable

In the console: **Remix → Indexing → Pre (beta)**, then **Enable** and save. Nothing is read while the option is off.

| Setting | Purpose |
| --- | --- |
| Enable | Master switch. |
| Public feeds | [predb.net](https://predb.net/) ([feed](https://api.predb.net/feed/)) and predb.me: [movies](https://predb.me/?cats=movies&rss=1), [shows](https://predb.me/?cats=tv&rss=1), [anime](https://predb.me/?cats=anime&rss=1). Untick to use only your personal feeds. |
| Languages kept | FRENCH / VF, MULTi, VOSTFR / SUBFRENCH. “Other language + MULTi” releases and fixes (FIX) are always dropped. |
| Personal feeds or API | Your own RSS feeds or a JSON API endpoint you have access to, separated by spaces. `http` or `https` addresses without credentials inside; never shown again. |
| Personal API key | Sent as an `X-API-Key` header to the personal addresses only, never in an address, never shown again. Not resent after a redirect to another host. |
| Delay before counting (advanced) | 1 h by default: a pre only counts this long after its announcement. `0s`: immediately. |
| Read interval (advanced) | 10 min by default, 2 min minimum. |

## How it works

1. Each feed is read at the chosen interval; non-video sections, fixes and unwanted languages are dropped.
2. A pre is only considered after the chosen delay.
3. Its name is matched to a movie or show: exact title, year for a movie, no match when ambiguous. Otherwise Remix searches TMDB and records the title if it is missing.
4. At most 40 titles are resolved per pass; a pre without a title is retried up to 6 times, then dropped. Processed pre are remembered for 30 days.

Requests go through Ether’s network guard (`safefetch`). Status is shown in the console.

## Statistics

When the option is on, **Remix → Statistics → Summary** shows a **Pre (beta)** block: releases indexed overall, releases coming from pre (with their share of the total), pending pre and the time of the last read. The per-source release detail, including pre, is under **Statistics → Searches** (“Stored releases by source”). Nothing is shown while the option is off.

## Limits

- An announcement does not prove the release is on your sources: the one-hour delay is an estimate.
- No link is kept: if no source offers the release, the title is not playable.
- Public feeds may change format or availability; a personal API format is read tolerantly, without guarantee.
