# Jellyfin watch-row performance

Offline SQLite reproduction through the mounted MediaBrowser HTTP handler on a shared Mac (GOMAXPROCS=4, CGO_ENABLED=0). The fixture contains 276,000 catalogue rows: 200,000 movies, 20,000 shows, two seasons per show and eight episodes per season for 1,000 opened shows. The owner has 20,000 history entries, in-progress titles, and twenty recently watched series with unwatched successors. WebDAV is mounted and the personalization model is built before requests, matching the expensive production paths. These are sequential first-call and repeated-call observations, not hardware-independent latency promises.

## Cause and change

* Optional WebDAV path enrichment scanned complete authorized movie/show libraries for a home row, and sign-in started another complete path index in the background. Jellyfin lists now use their existing local file labels; playback uses item IDs. Explicit WebDAV access and detail path resolution remain available. File labels need not carry the WebDAV sibling-collision suffix.
* NextUp generated ranked home candidates when a taste model was ready. It now has a dedicated viewer-state activity port, avoiding recommendations, layout, receipts, playlists and live-TV queries. Other home recommendation rows still use personalization.
* Continuation reads batch parent metadata, respect negative batch results, and skip persisted watched successors with indexed state probes before hydrating selected titles. Pending SQLite edits retain the state-overlay-aware selector. Alternate episode ordering retains its existing state checks.
* Favorite promotion checks the candidate shows rather than hydrating the viewer's entire favorite library.
* Lists join stored artwork metadata without a fixed 100ms wait for hashing. Episode and parent grants share a batch when requesting all image kinds.
* The `state_resume_order` registered migration indexes the complete resume ordering, including tied import timestamps. All SQL remains portable to PostgreSQL; the correlated state probe includes `LIMIT 1 OFFSET 0` to prevent a PostgreSQL hashed global probe.

Resume already started from indexed in-progress state and used batched title hydration on the integration base. Seasons already queried the named show's indexed season range. Removing response-enrichment scans is the material fix for those routes. Exact Resume totals remain bounded by the viewer's in-progress state, rather than the catalogue.

## Measured results

The final run returned 5 Resume items, 20 NextUp episodes and 2 seasons in every pass.

| Endpoint | Before first call: time / queries / rows | After first call: time / queries / rows | After repeat: time / queries / rows |
| --- | --- | --- | --- |
| Resume, limit 20 | 23,884 ms / 1,654 / 1,865,626 | 12.31 ms / 22 / 73 | 7.41 ms / 20 / 72 |
| NextUp, limit 20 | 7,721 ms / 1,648 / 427,349 | 45.02 ms / 26 / 893 | 0.98 ms / 2 / 2 |
| Seasons, 2 returned | 1,254 ms / 35 / 70 | 10.66 ms / 26 / 48 | 11.06 ms / 22 / 45 |

Final first-call database times were 3.57 ms, 9.89 ms and 4.11 ms respectively. An earlier baseline pass caught Seasons before the shared WebDAV index had filled: 2,513 ms, 450 queries and 267,289 rows. A repeated NextUp response uses the frontend page cache, so its two queries are authentication; the first-call measurement above exercises selection and rendering with twenty real episodes. Catalogue-cache minute boundaries and background index progress varied the baseline counts, but the fixed requests no longer issue the catalogue browse/tally windows.

Validation passed: pure-Go `go build -p 2 ./...`; `go vet -p 2` on the six touched packages; focused tests `TestItemAndUserDataDenyHiddenParent`, `TestNextStoredEpisodeDoesNotLeaveTheCatalogue`, `TestResumeStartsAtFivePercent`, and `TestShowsNextUpPagesFullRow`; and the opt-in HTTP reproduction. Measurements were iterated on this one focused reproduction to obtain the required before/after results. No full suite, race run, benchmark or CI gate was run.

## Reproduce

The opt-in `TestJFResumeLibraryBound` uses `internal/perfharness.Generate`, real sign-in, mounted Jellyfin and WebDAV handlers, and `store.WithQueryStats`. It requires a disposable directory under `~/.scratch/tmp`; never point it at a real server. It prints endpoint time, queries, result rows, database time, returned item counts and expensive SQL. It does not run server background loops or require providers.

```sh
GOMAXPROCS=4 CGO_ENABLED=0 ETHER_JF_RESUME_PERF=1 \
  ETHER_JF_RESUME_FIXTURE="$HOME/.scratch/tmp/jf-resume-offline" \
  timeout 300 go test -p 2 ./internal/app -run '^TestJFResumeLibraryBound$' -count=1 -timeout 270s -v
```

Remove the disposable fixture after use. SQLite row counters count rows returned to Go, not every B-tree tuple examined. The baseline used origin/integration/0312 before this slice; both versions used the same generated database and household.

## Shared surfaces and remaining verification

Vela Continue uses `experienceContinue` and receives the batched parent, watched-successor and candidate-favorite changes. Plex Continue/On Deck uses the shared `experience.Continue` machinery with its own `ContinueDeck`, so it receives parent prefetch and negative batch handling while keeping its own episode-order and availability selector. The registered resume index serves all users of `state.InProgress`.

Coordinator: run the full integration gate and PostgreSQL plan checks, and verify Jellyfin Web/Infuse/Roku Resume, NextUp paging and series-specific NextUp, season posters/badges, pending progress/unwatch, favorites, alternate episode orders, and Plex/Vela continuation on real devices. Confirm that local Jellyfin file labels still start playback through item IDs with WebDAV enabled, including duplicate title names.

The supplied production log separately shows a 65s Stopped request with two queries and effectively zero DB time, a 62s subtitle request with eight queries and effectively zero DB time, and 7.66s/7.67s HLS segments with one policy query (0ms/29ms DB time). Those waits are not accounted for by the recorded catalogue SQL; this slice does not claim to fix session gates, subtitle fetching or transcode delivery. Shared machine load could still amplify them. They require session/media traces if they remain slow after removing the home scans.
