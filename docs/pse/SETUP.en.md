# Remix setup assistant

## Testing the BETA branch

Stable keeps the previous assistant. This journey is available only on branch `BETA` and image `ghcr.io/remix-media/ether-remix:beta` (amd64 and arm64). The BETA workflow never publishes `latest`. In Dockge-Enhanced, use a separate test data directory (or an offline copy) and a different port. Never run two instances writing to the same database. Open `/ether/remix/assistant` to begin.


The assistant replaces four-step quick setup. Open **Remix → Start → Run the setup assistant again**. It remains available to advanced users and does not introduce a second effective configuration.

## Journey

1. **Content**: movies, shows, anime and music guide suggestions. Deselecting content does not delete or hide existing libraries.
2. **Languages and availability**: distinguish the French catalogue, release tags, playback rules and subtitles. Original-audio sections and SubMaker link to their existing editors; they are not created automatically.
3. **Discovery**: catalogue, JukeBox, Stremio/Xperience catalogues, SeerrCatalog, RSS Catalog and pre announcements. Discovering a title does not guarantee playback.
4. **Usage**: on-demand playback, downloads and existing files can coexist. Selecting an intent never enables download permissions.
5. **Connections**: relevant providers with requirements and limitations. Configuration and tests open in another tab. Secrets stay in existing editors; opening a link is not a successful connection test.
6. **Libraries and home**: Plex/Jellyfin intentions, app setup, Plex Home, existing JukeBox and theme-library editors. Those editors save independently and immediately. Theme libraries remain shared across protocols; selecting a client does not create client-specific visibility.
7. **Posters**: existing providers, navigation cards and cache duration. Collection artwork remains a separate workstream; no new style is generated automatically.
8. **Review and apply**: compare global values, read warnings and confirm the batch. Connections, collections and changes already saved in other editors are outside this batch.

Non-sensitive intentions are stored in this browser. Unapplied global changes and the last batch’s undo data live only in the component session; reloading loses them. Undo prepares another preview that must be confirmed. Diagnostics and a real playback test remain necessary.

## Identified overlaps

| Options | Actual relationship |
|---|---|
| Languages and quality / Playback → Quality | Two entries to one editor, not separate settings. |
| French catalogue / availability | Cumulative filters. French theatrical release and French audio differ. |
| Profiles / simple criteria | Profiles replace simple criteria; minimum release counts remain. Player audio preferences still apply. |
| Global / type / library / collection rules | Different scopes; inspect replacement mode before treating local rules as global. |
| Original audio, subtitles and mandatory French audio | SubMaker cannot make an audio-rejected release eligible. Pending translations are not available yet. |
| Pre / playable catalogue | Announcements currently count as indexed releases without a link. The assistant warns but preserves that behaviour. |
| Pre RSS / RSS Catalog addon | Release announcements versus discovery lists; not interchangeable. |
| SeerrCatalog / Seerr connection and sharing | Separate roles, not necessarily duplicates. |
| Quick order / advanced priorities | Quick order covers selected video sources, not every local or music playback path. |

## Shared API and delivered scope

Authenticated `GET /ether/api/pse/setup`, `POST /ether/api/pse/setup/preview` and `POST /ether/api/pse/setup/apply` read public state, preview and apply a validated batch. Clients send `revision`, `values`, then the preview’s `plan`. Stale revisions or different plans cause a conflict without writes. A `null` value restores environment inheritance; unrelated settings are preserved.

Only these settings are accepted: `PSE_FRENCH`, `PSE_FRENCH_RELEASES`, `PSE_PLAYABLE`, `PSE_PLAYABLE_MIN`, `PSE_PLAYABLE_SINGLE`, `PSE_PLAYABLE_PENDING`, `PSE_INDEXER`, `PSE_PREDB`, `PSE_LIBRARY_SORT`, `PSE_POSTER_REFRESH_HOURS`. No secrets, manifests, networking, provider activation or download permissions.

This is groundwork for future MCP tools. No Claude, ChatGPT or Codex connector is shipped here. Dedicated permissions, secrets management, durable drafts, library planning and per-content/client rules remain to be designed. AI account and plan requirements must be checked during that integration.
