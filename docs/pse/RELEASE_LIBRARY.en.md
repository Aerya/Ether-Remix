# Release library — Beta

Enable **Remix → Indexing → Release library (Beta)** to retain usable source references. `PSE_RELEASE_LIBRARY` is **off by default**. It does not automatically enable indexing, which keeps its existing source selection, budgets and playback pauses.

Actual searches and SFR imports for catalogue titles populate the library. Alkodiques file links preserve the verified AllDebrid file path without adding magnets. Private references and headers are encrypted with Remix's configuration key and never sent to the browser.

Playback offers recent references to the existing resolver, preserving language, quality, player constraints and checks. Stored alternatives can be reused before a rescue search if candidates fail or do not qualify. Resolution, validation and buffering still take time. Background and manual searches continue querying sources.

## Inspect releases

Open a catalogue detail in the console and choose **Indexed releases · Beta**. Inspection remains available with reuse disabled. Use the sort selector and direction button to sort by resolution, size, video/audio codec, source, protocol, languages, reported cache, reference status or dates. Text filtering and display refresh do not query sources. Display ordering does not change playback preferences.

For shows, select a season and episode. The initial view represents S01E01, used by catalogue indexing; other episodes are populated by their own searches. An indexed show does not prove availability of every episode.

Publication comes from the source and is unknown when absent. First indexed and last observed are Remix dates. Metadata-only entries lack a retained usable reference. Available references and reported cache are not playback guarantees. Expired, failed and changed-source references are excluded from reuse.

**Browse the index**: under **Indexing → Releases**, search media (title or original title) or releases (name or source) across the whole index. Filter Movies, Series or Anime, then today, this week (from Monday), this month or an inclusive custom date range in your time zone. Choose first indexed or last observed dates. Series group episodes; clicking one expands its releases directly below the media without leaving the list or losing the search. Releases load on first opening and remain loaded when collapsed. Anime uses catalogue metadata when known. Results are paginated by 50 and sorted by indexing or observation. Global releases use Previous/Next without recounting the entire index; totals remain available for media and releases within a media item. Compact expandable rows reveal codecs, languages and dates without a horizontal table. Searching never contacts sources. On first opening of releases, existing encrypted references are prepared in batches, scoped to the selected media when applicable; only displayable fields are added to the search index.

## Retention

Ordinary HTTP URLs can be reused for one hour; torrent/NZB download links for 24 hours. Stable references (valid hash/magnet and AllDebrid file link) do not expire by age. Failures or source configuration changes exclude them from reuse. Playback checks still apply: a stored hash does not prove debrid cache availability. Quality/language changes allow local reranking. Expired references are removed after 60 days without observation during collection; browsing does not extend validity.

Only results returned by sources can be retained; addon filtering cannot be undone. Old metadata cannot reconstruct missing links: another search or import is needed. Disabling beta stops reference collection and reuse without deleting existing metadata.

## Console monitoring

Enable the beta in **Overview → Quick start** or **Indexing**, then save. The **Indexing → Releases** view appears after saving. Search and browse media and releases by category and date range. Active and recent searches show real activity. Collection is continuous, without an overall completion percentage.

Counters also appear in **Overview**, separating metadata from stored references: available, expired, failed or source changed. These sets overlap and must not be added. Counters refresh every 60 seconds while the page is visible. An available reference does not guarantee playback.

Failed counter refreshes keep previous values and no longer close the explorer. A media loading failure displays a Retry button inside its block without clearing other results. Refreshes do not interrupt an ongoing request.

## Media identity and index repair

A TMDB/IMDb query is not proof of a correct answer. Even a matching reported ID requires a complete catalogue title (original/alternate titles included) and exact movie year. Conflicting IDs reject the result. Episodes require matching series identity and one compatible episode number; without a matching reported ID, the series year is required too. Season packs, ambiguous names and results without an actual source locator are rejected. Normal searches, rescue searches and stored playback candidates share this rule.

Audit version 2 runs even if the previous audit completed. Existing metadata and encrypted references, including StreamFusion imports, are checked without treating their old TMDB association as its own proof. Unconfirmed rows are preserved privately in `pse_releases_quarantine` and `pse_release_library_quarantine`, excluded from browsing and reuse. Missing evidence does not necessarily mean an incorrect association. Indexing and library reuse wait for the audit; derived counters and old availability verdicts are recomputed.

Previously indexed titles then enter gradual source-reference recovery, respecting existing source budgets and playback pauses. **Titles pending recovery** shows the queue; failures retry later and no link is fabricated from a name. Confirmed stable references are reused directly. StreamFusion import restarts under the new rule. Recovery depends on source answers and does not promise a reference for every title or every episode: general indexing represents shows with S01E01, while previously searched episodes are revisited individually.

`go run ./cmd/ether-release-audit -db /path/copy.db -config /path/copied-config` performs a read-only audit. Use a consistent SQLite backup and private copies of `config.json` and `.credentials-key` (loading may upgrade the copied configuration). `-apply` quarantines unconfirmed rows: stop indexing/the server first and keep a backup. Reports contain source/reason counts only, never playback URLs or secrets.

Global release search scans names without forcing chronological reads; category browsing uses date order to limit the rows read.
