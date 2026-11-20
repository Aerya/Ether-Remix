# Release index

Searches and imports automatically retain references in a shared index, reused during playback. Under **Remix → Indexing**, the single **Automatically search for releases** switch (`PSE_INDEXER`) controls background searches. It remains off by default; stopping it does not delete the index or disable browsing and reuse. The old `PSE_RELEASE_LIBRARY` setting is ignored regardless of its value. Existing sources, budgets and pauses are preserved.

Actual searches and SFR imports for catalogue titles populate the library. Alkodiques file links preserve the verified AllDebrid file path without adding magnets. Private references and headers are encrypted with Remix's configuration key and never sent to the browser.

Playback offers recent references to the existing resolver, preserving language, quality, player constraints and checks. Stored alternatives can be reused before a rescue search if candidates fail or do not qualify. Resolution, validation and buffering still take time. Background and manual searches continue querying sources.

## Inspect releases

Open a catalogue detail in the console and choose **Indexed releases**. Inspection remains available while automatic search is off. Use the sort selector and direction button to sort by resolution, size, video/audio codec, source, protocol, languages, reported cache, reference status or dates. Text filtering and display refresh do not query sources. Display ordering does not change playback preferences.

For shows, select a season and episode. The initial view represents S01E01, used by catalogue indexing; other episodes are populated by their own searches. An indexed show does not prove availability of every episode.

Publication comes from the source and is unknown when absent. First indexed and last observed are Remix dates. Metadata-only entries lack a retained usable reference. Retained references and reported cache are not playback guarantees. Expired, failed and changed-source references are excluded from reuse.

**Browse the index**: under **Indexing → Releases**, search media (title or original title) or releases (name or source) across the whole index. Filter Movies, Series or Anime, then today, this week (from Monday), this month or an inclusive custom date range in your time zone. Choose first indexed or last observed dates. Series group episodes; clicking one expands its releases directly below the media without leaving the list or losing the search. Releases load on first opening and remain loaded when collapsed. Anime uses catalogue metadata when known. Results are paginated by 50 and sorted by indexing or observation. Global releases use Previous/Next without recounting the entire index; totals remain available for media and releases within a media item. Compact expandable rows reveal codecs, languages and dates without a horizontal table. Searching never contacts sources. On first opening of releases, existing encrypted references are prepared in batches, scoped to the selected media when applicable; only displayable fields are added to the search index.

## Retention

Ordinary HTTP URLs can be reused for one hour; torrent/NZB download links for 24 hours. Stable references (valid hash/magnet and AllDebrid file link) do not expire by age. Failures or source configuration changes exclude them from reuse. Playback checks still apply: a stored hash does not prove debrid cache availability. Quality/language changes remove rejected links; broadening criteria requires new searches. Expired references are removed after 60 days without observation during collection; browsing does not extend validity.

Only results returned by sources can be retained; addon filtering cannot be undone. Old metadata cannot reconstruct missing links: another search or import is needed. Stopping automatic search preserves reference collection during playback searches and imports, as well as reuse of existing references.

## Console monitoring

The **Indexing → Releases** view is always available. Settings, source selection and search activity are under **Indexing**. Collection is continuous, without an overall completion percentage. A show only lists episodes actually present in the index; an initial S01E01 search does not imply a complete season.

Counters also appear in **Overview**, separating metadata from stored references: available, expired, failed or source changed. These sets overlap and must not be added. Counters refresh every 60 seconds while the page is visible. An available reference does not guarantee playback.

Failed counter refreshes keep previous values and no longer close the explorer. A media loading failure displays a Retry button inside its block without clearing other results. Refreshes do not interrupt an ongoing request.

## Media identity and index repair

Episodes without a filename year are admitted after checking the local catalogue: known series year, complete title, exact episode and no known namesake. Conflicting years remain rejected; catalogue errors keep the strict check.

Previously rejected links are not restored from quarantine. A new search or import must supply them; addon caches and budgets still apply. Existing rescue search bypasses the retained index when no candidate is suitable, including when every retained reference exceeds the player resolution limit.

A TMDB/IMDb query is not proof of a correct answer. Even a matching reported ID requires a complete catalogue title (original/alternate titles included) and exact movie year. Conflicting IDs reject the result. Episodes require matching series identity and one compatible episode number; without a matching reported ID, the catalogue series year is required too. A filename year may be omitted only after a successful local namesake check. Season packs, ambiguous names and results without an actual source locator are rejected. Normal searches, rescue searches and stored playback candidates share this rule.

Audit version 2 runs even if the previous audit completed. Existing metadata and encrypted references, including StreamFusion imports, are checked without treating their old TMDB association as its own proof. Unconfirmed rows are preserved privately in `pse_releases_quarantine` and `pse_release_library_quarantine`, excluded from browsing and reuse. Missing evidence does not necessarily mean an incorrect association. Indexing and library reuse wait for the audit; derived counters and old availability verdicts are recomputed.

Previously indexed titles then enter gradual source-reference recovery, respecting existing source budgets and playback pauses. **Titles pending recovery** shows the queue; failures retry later and no link is fabricated from a name. Confirmed stable references are reused directly. StreamFusion import restarts under the new rule. Recovery depends on source answers and does not promise a reference for every title or every episode: general indexing represents shows with S01E01, while previously searched episodes are revisited individually.

`go run ./cmd/ether-release-audit -db /path/copy.db -config /path/copied-config` performs a read-only audit. Use a consistent SQLite backup and private copies of `config.json` and `.credentials-key` (loading may upgrade the copied configuration). `-apply` quarantines unconfirmed rows: stop indexing/the server first and keep a backup. Reports contain source/reason counts only, never playback URLs or secrets.

Global release search scans names without forcing chronological reads; category browsing uses date order to limit the rows read.

## Selection before retention

Links must meet active Remix criteria before storage and reuse: global or scoped profiles in profile mode; simple filters and French catalogue languages in simple mode. Explicit language exceptions remain active without bypassing quality requirements. Unknown language cannot satisfy a French/MULTi/VOSTFR requirement.

Old links are cleaned up after profiles and exceptions are refreshed, on startup, after changes and during periodic maintenance. No video files are deleted. Observations without links remain diagnostic metadata; they do not prove compliance or playback availability. Broadening criteria requires fresh searches to recover rejected links. “Retained reference” does not replace client and playback profile checks.


[Quick setup](QUICK_SOURCES.en.md) retains the existing search and reference mechanisms. An indexed reference never jumps ahead of a higher selected source; source order applies before playback validation.
