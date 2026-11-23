# Anime Tosho

One-click anime indexer plugin for Ether backed by [Anime Tosho NEW](https://animetosho.net).

## What it does

The original Anime Tosho (`animetosho.org`) stopped indexing new releases in May 2026. [Anime Tosho NEW](https://animetosho.net) is a community-funded successor that mirrors anime torrents from Nyaa.si, TokyoTosho, and nekoBT (2017 to present) and exposes open Torznab and Newznab feeds at `https://feed.animetosho.net` with no API key and no account required.

Installing this plugin adds Anime Tosho as an indexer source in Ether with nothing to paste:

- **Torrents and Usenet NZB mirrors:** returns torrent info hashes with public trackers from Torznab, Usenet NZB links from Newznab, or both at once.
- **Anime-aware search:** supports AniDB (`aid`), MyAnimeList (`malid`), Kitsu (`kitsuid`), TMDB (`tmdbid`), TVDB (`tvdbid`), and IMDb (`imdbid`) lookups, absolute episode numbering (such as long-running series like One Piece), and whole-season packs.
- **No API key:** Anime Tosho NEW's Torznab and Newznab feeds are open since v1.3.1, so connection tests and searches work immediately after installation.

## Settings

| Setting | Default | Description |
| --- | --- | --- |
| **Release types** (`releaseTypes`) | `Torrents and NZBs` | Choose whether searches return public torrents, Usenet NZB mirrors, or both. NZB downloads require a Usenet provider configured in Ether. |
| **Category filter** (`category`) | `All categories` | Optional Torznab/Newznab category filter (`5070` for Anime, `2020` for Movies) passed to Anime Tosho NEW. |

## Public torrents notice

Torrents mirrored on Anime Tosho NEW come from public anime trackers such as Nyaa.si and nekoBT. They are **public torrents**, so Ether's private-torrent ratio and H&R rules do not apply to releases from this plugin.

## Credits and polite usage

All feed data, torrent metadata, and Usenet NZB mirrors are provided by [Anime Tosho NEW](https://animetosho.net), a free community-funded project, with ID mappings from AnimeAPI and the Anime Offline Database (ODbL-1.0).

This plugin identifies itself honestly with Ether's User-Agent (`Ether/0.3 (+https://github.com/braedonsaunders/ether)`), makes a single bounded feed request per search (`limit=75`), declares conservative indexer rate limits (`120` searches/hour, `1000` searches/day), and propagates upstream `429` / `Retry-After` responses so Ether backs off automatically.

## Development

Rebuild the hosted bundle from `plugin.ts`:

```sh
go run ./sdk/examples/anime-tosho/build
```

Run the plugin conformance check, fixture tests, and Go integration suite:

```sh
ether plugin check ./sdk/examples/anime-tosho
ether plugin test ./sdk/examples/anime-tosho
go test ./sdk/examples/anime-tosho/...
```
