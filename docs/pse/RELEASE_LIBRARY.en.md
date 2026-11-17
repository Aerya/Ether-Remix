# Release library — Beta

Enable **Remix → Indexing → Release library (Beta)** to retain usable source references. `PSE_RELEASE_LIBRARY` is **off by default**. It does not automatically enable indexing, which keeps its existing source selection, budgets and playback pauses.

Actual searches and SFR imports for catalogue titles populate the library. Alkolites file links preserve the verified AllDebrid file path without adding magnets. Private references and headers are encrypted with Remix's configuration key and never sent to the browser.

Playback offers recent references to the existing resolver, preserving language, quality, player constraints and checks. Stored alternatives can be reused before a rescue search if candidates fail or do not qualify. Resolution, validation and buffering still take time. Background and manual searches continue querying sources.

## Inspect releases

Open a catalogue detail in the console and choose **Indexed releases · Beta**. Inspection remains available with reuse disabled. Use the sort selector and direction button to sort by resolution, size, video/audio codec, source, protocol, languages, reported cache, reference status or dates. Text filtering and display refresh do not query sources. Display ordering does not change playback preferences.

For shows, select a season and episode. The initial view represents S01E01, used by catalogue indexing; other episodes are populated by their own searches. An indexed show does not prove availability of every episode.

Publication comes from the source and is unknown when absent. First indexed and last observed are Remix dates. Metadata-only entries lack a retained usable reference. Available references and reported cache are not playback guarantees. Expired, failed and changed-source references are excluded from reuse.

**Browse the index**: under **Indexing → Releases**, search media (title or original title) or releases (name or source) across the whole index. Filter Movies, Series or Anime, then today, this week (from Monday), this month or an inclusive custom date range in your time zone. Choose first indexed or last observed dates. Series group episodes; clicking one expands its releases directly below the media without leaving the list or losing the search. Releases load on first opening and remain loaded when collapsed. Anime uses catalogue metadata when known. Results are paginated by 50 and sorted by indexing or observation. Global releases use Previous/Next without recounting the entire index; totals remain available for media and releases within a media item. Compact expandable rows reveal codecs, languages and dates without a horizontal table. Searching never contacts sources. On first opening of releases, existing encrypted references are prepared in batches, scoped to the selected media when applicable; only displayable fields are added to the search index.

## Retention

Ordinary HTTP references expire after one hour; torrent/NZB references and AllDebrid file links after 24 hours. Source configuration changes invalidate previous references. Quality and language changes keep references available for local filtering. Entries remain inspectable and are pruned after 60 days without observation during a later collection. Inspection and reuse do not extend observation dates.

Only results returned by sources can be retained; addon filtering cannot be undone. Old metadata cannot reconstruct missing links: another search or import is needed. Disabling beta stops reference collection and reuse without deleting existing metadata.

## Console monitoring

Enable the beta in **Overview → Quick start** or **Indexing**, then save. The **Indexing → Releases** view appears after saving. Search and browse media and releases by category and date range. Active and recent searches show real activity. Collection is continuous, without an overall completion percentage.

Counters also appear in **Overview**, separating metadata from stored references: available, expired, failed or source changed. These sets overlap and must not be added. Counters refresh every 60 seconds while the page is visible. An available reference does not guarantee playback.

Failed counter refreshes keep previous values and no longer close the explorer. A media loading failure displays a Retry button inside its block without clearing other results. Refreshes do not interrupt an ongoing request.

## Media identity and index repair

A TMDB/IMDb search alone is not proof: a source can ignore the request. Source-reported release IDs are checked against the requested media; echoed request IDs are not evidence. Without matching reported IDs, a matching title (including original/alternate titles) is required, plus a known compatible year for movies. Identifier, media type, year and episode conflicts and placeholders such as `aucun_flux` are excluded. Packs covering the requested season remain eligible, without guaranteeing playback.

On the first startup after this fix, a local audit checks existing metadata and encrypted references. Indexing and retained library reuse wait for completion while the server stays available. Unconfirmed rows move intact to `pse_releases_quarantine` and `pse_release_library_quarantine` with a reason. Unconfirmed does not necessarily mean incorrect: missing metadata may prevent verification. Direct StreamFusion imports retain their typed TMDB association. Derived counters are rebuilt and the console shows the number of quarantined associations.

`go run ./cmd/ether-release-audit -db /path/copy.db -config /path/copied-config` performs a read-only audit. Use a consistent SQLite backup and private copies of `config.json` and `.credentials-key` (loading may upgrade the copied configuration). `-apply` quarantines unconfirmed rows: stop indexing/the server first and keep a backup. Reports contain source/reason counts only, never playback URLs or secrets.
