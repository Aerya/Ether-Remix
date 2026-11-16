# Release library — Beta

Enable **Remix → Indexing → Release library (Beta)** to retain usable source references. `PSE_RELEASE_LIBRARY` is **off by default**. It does not automatically enable indexing, which keeps its existing source selection, budgets and playback pauses.

Actual searches and SFR imports for catalogue titles populate the library. Alkolites file links preserve the verified AllDebrid file path without adding magnets. Private references and headers are encrypted with Remix's configuration key and never sent to the browser.

Playback offers recent references to the existing resolver, preserving language, quality, player constraints and checks. Stored alternatives can be reused before a rescue search if candidates fail or do not qualify. Resolution, validation and buffering still take time. Background and manual searches continue querying sources.

## Inspect releases

Open a catalogue detail in the console and choose **Indexed releases · Beta**. Inspection remains available with reuse disabled. Click column headings to sort by resolution, size, video/audio codec, source, protocol, languages, reported cache, reference status or dates. Text filtering and display refresh do not query sources. Table ordering does not change playback preferences.

For shows, select a season and episode. The initial view represents S01E01, used by catalogue indexing; other episodes are populated by their own searches. An indexed show does not prove availability of every episode.

Publication comes from the source and is unknown when absent. First indexed and last observed are Remix dates. Metadata-only entries lack a retained usable reference. Available references and reported cache are not playback guarantees. Expired, failed and changed-source references are excluded from reuse.

## Retention

Ordinary HTTP references expire after one hour; torrent/NZB references and AllDebrid file links after 24 hours. Source configuration changes invalidate previous references. Quality and language changes keep references available for local filtering. Entries remain inspectable and are pruned after 60 days without observation during a later collection. Inspection and reuse do not extend observation dates.

Only results returned by sources can be retained; addon filtering cannot be undone. Old metadata cannot reconstruct missing links: another search or import is needed. Disabling beta stops reference collection and reuse without deleting existing metadata.

## Console monitoring

Enable the beta in **Quick start** or **Indexing**, then save. The **Indexed releases** tab appears after saving. It lists content and episodes by latest observation, in pages of 50; select content to inspect sortable releases. Active and recent searches show real activity. Collection is continuous, without an overall completion percentage.

Counters also appear in **Statistics**, separating metadata from stored references: available, expired, failed or source changed. These sets overlap and must not be added. The display refreshes every 15 seconds while the page is visible. An available reference does not guarantee playback.
