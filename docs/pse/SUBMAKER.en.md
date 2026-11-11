# External subtitles with SubMaker

Disabled by default. Use a [public instance](https://submaker.elfhosted.com/configure) or [self-host SubMaker](https://github.com/xtremexq/StremioSubMaker). Configure subtitle providers and a translation engine in SubMaker, then copy its personal `https://submaker.example/addon/<configuration>/manifest.json` URL. A `/configure` URL is insufficient. Keep this manifest private.

In **Sources → Stremio → SubMaker**, enable the feature, enter the manifest and source/target languages (English/French by default), and save the page settings. Test the saved manifest without starting translation. Separately save the global scope and library/collection overrides. Collection overrides take precedence over libraries, then global; the first matching rule wins ties. Unknown SeerrCatalog membership uses global scope.

For [SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog), configure authenticated search via Remix, enable external subtitles in the user's source filters and select the same target language. Availability sharing retains its own switch and token.

## VOSTFR on demand

In **Remix → French subtitles on demand**, the **Prepare French subtitles with SubMaker** option (off by default) puts the chosen titles in SubMaker’s scope, even when the global scope is off. It is only offered when SubMaker is enabled with a valid manifest. An explicit collection rule in SubMaker’s scope still wins; a library rule does not.

When it is turned on, titles without known French subtitles are searched again; SubMaker then prepares the releases found, within the daily budget. Each title shows a state: French audio, French subtitles announced, French SRT ready, translated SRT (AI), preparing, failed (retried later), no French subtitles, or no release indexed. For a show, the state reflects the episode prepared during indexing; other episodes are prepared at playback.

SubMaker results must match the normalized release filename exactly; title-only matches are rejected. Target-language subtitles are preferred, otherwise source-language SRTs are translated. A completed SRT must have valid cues, and a translation must preserve cue count and timestamps. This does not guarantee linguistic quality or actual synchronization; check playback. Translated results carry an AI label. Providers without a matching filename cannot be automatically accepted.

One worker processes up to two releases per search. The persistent budget defaults to 10 attempts per UTC day (1–100), including retries, with a one-hour failure cooldown. Each SRT is limited to 512 KiB; preparation stops at a 512 MiB cache. Provider limits and fees still apply. No video files are stored.

Remix attaches only the selected release's cached subtitle to playback. Restart an already open session after preparation. Quality/audio rules remain enforced: a profile requiring French audio can still exclude an original-language release. Series indexing prepares its representative episode, not every episode.

At playback, a release whose SRT is already ready ranks ahead of releases of the same level: same quality, same accepted audio language and same custom format score. It then wins over a release from a higher-priority indexer, with more seeders or a larger file. An explicit Usenet or debrid preference still applies before it. A release with better quality, a better accepted language or a higher score still ranks first, even without an SRT; SubMaker prepares it for the next playback. The preference applies only when SubMaker is enabled and the title is within the allowed scope. Ether may still prefer an equivalent release already cached on the debrid service, or a faster source, so playback starts. The check is a single query per playback; the reason shows in the ranking details.

SeerrCatalog receives the SRT through authenticated search, without the SubMaker manifest. Pending translations do not count toward availability and are checked again after five minutes. Two distinct qualifying releases confirm availability; one retains J+1/J+3 retries and manual approval. Confirmed availability remains permanent until deletion. Resolution and addon-count rules still apply.

The SeerrCatalog Stremio `subtitles` resource serves stored subtitles only for available media and an exact video filename. Clients must send `filename`; otherwise the response is empty. Reinstall the addon if its resource list is cached. Deletion removes access to the associated subtitles.

Automated tests use a local simulated provider. A live smoke test translated two English cues into French while preserving timestamps, and the Remix connector retrieved a French SRT containing 1,263 cues. Synchronized video playback and the complete SeerrCatalog flow still require validation before production. Draft PR review requires no credentials or user configuration.
