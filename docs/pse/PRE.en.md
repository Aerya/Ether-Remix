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
| Public feeds | [predb.net](https://api.predb.net/feed/) (general feed) and its [anime API](https://api.predb.net/?type=section&q=ANiME), then predb.me: [movies](https://predb.me/?cats=movies&rss=1), [shows](https://predb.me/?cats=tv&rss=1), [anime](https://predb.me/?cats=anime&rss=1). Untick to use only your own sites. |
| Languages kept | FRENCH / VF, MULTi, VOSTFR / SUBFRENCH. “Other language + MULTi” releases and fixes (FIX) are always dropped. |
| Other sites (RSS feed or API) | As many sites as you like (20 at most): an optional name, the address of an RSS feed or JSON API, and an optional API key per site (sent as an `X-API-Key` header to that site only, never shown again, not resent after a redirect to another host). `http` or `https` addresses without credentials inside. In the statistics they are called “Site 1”, “Site 2”… (or the name you gave). |
| Proxy for public feeds | None (direct read), **HTTP/SOCKS proxy** (`http://`, `https://`, `socks5://`) or **MediaFlow relay** (e.g. `https://mediaflow.example`). `user:password` credentials for the proxy, or the MediaFlow API password. Applies to the public feeds only; your sites are read directly. Useful when the server is at a host whose addresses predb.me refuses. |
| Delay before counting (advanced) | 1 h by default: a pre only counts this long after its announcement. `0s`: immediately. |
| Read interval (advanced) | 1 min by default (the predb.net feed only keeps the 20 latest announcements), 30 s minimum. |

## How it works

1. Each feed is read at the chosen interval; non-video sections, fixes and unwanted languages are dropped.
2. A pre is only considered after the chosen delay.
3. Its name is matched to a movie or show: exact title, year for a movie, no match when ambiguous. Otherwise Remix searches TMDB and records the title if it is missing.
4. At most 40 titles are resolved per pass; a pre without a title is retried up to 6 times, then dropped. Processed pre are remembered for 30 days.

Requests go through Ether’s network guard (`safefetch`). Status is shown in the console.

## Statistics

**Remix → Statistics → Summary**, visible from Simple mode, always shows releases indexed overall and by source. When the option is on, a **Pre (beta)** block is added: releases indexed overall, releases coming from pre (with their share of the total), pending pre and the time of the last read. The per-source release detail, including pre, is under **Statistics → Searches** (“Stored releases by source”). A table then details, **for each pre feed** (the five public feeds and your other sites, named “Site 1”, “Site 2”… without ever showing their address), the pre read over 30 days, indexed releases, pending pre and those whose title was not found, with a total. A **Last read** column tells, for each feed, whether it answered (announcements received and kept) or the error met (for example “feed refused (403)”, never showing the address), so you can tell which feed is unreachable. A pre seen in several feeds is counted for the first one that saw it; releases whose pre is older than 30 days, or read before this tracking, are grouped in an “Older or unknown feed” line. While the option is off, a note says so and links to **Indexing → Pre (beta)** to enable it.

## Limits

- An announcement does not prove the release is on your sources: the one-hour delay is an estimate.
- No link is kept: if no source offers the release, the title is not playable.
- Public feeds may change format or availability; a personal API format is read tolerantly, without guarantee.
- **From a hosted server, the predb.me feeds are refused** (error 403, Cloudflare “error code: 1005”, measured on 21/11/2026 from a OneProvider server): predb.me blocks server addresses. The predb.net feed answers, but only holds the 20 latest announcements across all sections: Remix rereads it every minute and also reads the predb.net anime API, so announcements are not lost.me and “OK · 20 announcements · 0 kept” for predb.net, and the counters stay at zero. Errors are also written to the logs (`pse pre feed failed`, with the feed concerned). To work around this, set a **proxy** or a **MediaFlow relay** going through a residential address (“Proxy for public feeds” setting), or add another site your server can reach. The **Test the feeds** button (same view) reads every feed right away with the settings shown, even unsaved, and shows for each the result, announcements received and kept and a few examples, without saving anything.
