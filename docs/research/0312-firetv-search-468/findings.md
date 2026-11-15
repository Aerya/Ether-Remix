# Issue #468: Fire TV search opens a show without seasons

## Evidence

Read the issue and all four comments, and downloaded both November 14 attachments.
The ZIP contains the same-named text log. The log records Ether **0.3.10** starting
at 09:52:59.247 (timestamps below are November 14, 2026, UTC−05:00).

| Time | Logged request / result |
| --- | --- |
| 10:01:20.282 | `GET /library/search`, performance warning; query text redacted, `limit=100` visible |
| 10:02:39.674 | `invalid child query`, `parameters=[libraryHubsOnly]` |
| 10:02:39.674 | `/library/metadata/2000000000194583/children`, `plex: invalid child query` |
| 10:03:18.895 | `GET /library/search`, performance warning; query text redacted, `limit=100` visible |
| 10:03:52.696 | `invalid child query`, `parameters=[libraryHubsOnly]` |
| 10:03:52.696 | `/library/metadata/2000000000001402/children`, `plex: invalid child query` |

`childQuery` returns that error when its query-tree parser rejects a field.
`writeErr` maps this ordinary error to HTTP 500. Thus the season list fails;
this is not evidence that the shows have no seasons in the catalogue.

These are INFO-level logs, not full HTTP captures. They do **not** expose the
value of `libraryHubsOnly`, complete query strings, client headers, response
bodies, intervening show-metadata calls, or a search-hub call. The sequence
search → show detail → children is consistent with the report, but only the
search and children endpoints above are directly logged. Do not invent a full
request trace or identify a specific client's request from absent headers.

## Cause and change

Commit `d7b22d000ac794c81db9e35c303e5ef30a61dcae`, present in both v0.3.10
and v0.3.11, already ignores async/check/skip/include/exclude request options
on children and supports subtree index sorting. It does not recognize
`libraryHubsOnly`, so v0.3.11 still has the newly logged failure.

Treat `libraryHubsOnly` as a response-scope option in the shared children-option
classifier, before query parsing or catalogue filtering. This option must not
become a season/episode predicate regardless of its value. The same helper
serves show/season `/children` and `/allLeaves` for every Plex client. A request
with only this option now takes the existing unfiltered fast path. Real filters,
sort validation, viewer visibility, and pagination keep their existing handling.
No catalogue-wide browse, refresh, background work, or client-version branch is
introduced. Section query-editor validation is unchanged.

At the coordinator's direction, unknown children options are also ignored after
identifying the known browse fields. Known field values/operators, sort, limit,
and query structure still go through existing validation. This prevents another
inherited or newly introduced app option from producing the same failure.

## PMS comparison and limits

Reviewed `internal/conform/plex/plex.go` and decoded the six recorded JSON
children responses in `testdata/conform/plex/{web,ios,appletv}`. For all three
profiles PMS answers show children with HTTP 200, `viewGroup=season`, ten season
metadata records plus the All episodes directory (`size=11`). Season children
return HTTP 200, `viewGroup=episode`, and ten episodes (`size=10`). This agrees
with Ether's existing successful children renderer; the failure occurs before
rendering.

The recorded requests have no `libraryHubsOnly` option. The coordinator supplied
additional read-only evidence from the owner's real PMS; this worker did not
access it. For show 9-1-1, ratingKey 5484:

- `/library/metadata/5484/children`: HTTP 200, size 11; All episodes first with
  `leafCount=147`, then seasons.
- The same call with `libraryHubsOnly=1`: HTTP 200, identical response.
- The same call with `includeUserState=1&libraryHubsOnly=1&excludeAllLeaves=1`:
  HTTP 200, size 10, no All episodes directory; seasons start at index 1.

This confirms that PMS ignores the reported option and honours
`excludeAllLeaves`. Ether already honours the latter in `handleChildren`; the
shared option classifier already keeps it out of browse predicates.
`docs/design.md`, requested by CLAUDE.md, is absent from this checkout and the
canonical checkout's docs directory.

## Coordinator device verification

On legacy Plex for Android (TV) / Fire TV 10.30.1.3505, search for both affected
shows, open each result, confirm seasons appear, open a season, and play an
episode. Capture the actual search, metadata, and children requests and verify
HTTP 200 rather than the recorded child-query error. Repeat on current Fire TV,
Plex Web, iOS, and Apple TV. Exercise children with the scope option alone and
combined with `sort=index`, paging, and watch filters. Verify `/allLeaves` too.
This slice has not run those real-device checks.

## Local checks

Passed with `CGO_ENABLED=0`, `GOMAXPROCS=4`, one command at a time:

- `timeout 180 go test -p 2 ./internal/frontends/plex -run '^TestChildWatchQueryBeforePageAndFlagValidation$' -count=1 -timeout 120s`
- `timeout 180 go vet -p 2 ./internal/frontends/plex`
- `timeout 240 go build -p 2 ./...` (final change)
- `git diff --check`

The existing focused test checks subtree filtering, pagination, and flag/sort
validation; it does not replay the missing Fire TV request. It and vet passed
before the additional unknown-option handling requested by the coordinator;
the final change is also compiled with a pure-Go full build. No per-bug test
was added, per the release brief. The coordinator owns the full test gate.

## Issue-reply draft (do not post until verified)

Thanks for the new logs. They identify the option that the first fix missed:
`libraryHubsOnly`. Ether 0.3.10 rejected it twice while loading a show's seasons,
at 10:02:39 and 10:03:52, and returned an error instead of the season list. That
explains why the problem continued after the earlier async-option fix. The same
rejection also remains in 0.3.11.

The follow-up change makes Ether accept this option on season and episode
lists. It is in the shared Plex request handling, so it applies to every Plex
app, including the older Fire TV app. It does not require a library rescan.
We also compared the request on Plex Media Server: it returns the same seasons
with or without `libraryHubsOnly=1`. Ether now ignores unknown children options
so that another app option cannot cause this same rejection.

The code change is ready for release verification. We still need to confirm
the full search → show → season → episode flow on Fire TV 10.30.1.3505 before
calling this resolved. Once the release containing it is available, please
retry both shows from search and confirm that you can open a season and start
an episode. If it still fails, a fresh log from that attempt will help identify
the exact remaining request.
