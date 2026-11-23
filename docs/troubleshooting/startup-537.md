# v0.3.12: unsupported type "Movies" at startup

`catalog: catalog: browse: unsupported type "Movies"` comes from binding a
saved `browse:type` condition as a catalogue predicate. Top-level rule
`mediaType: "Movies"` produces a different validation error.

The editor in `web/src/pages/libraries/browser/RuleCondition.tsx` exposed Type
as a free-text input. `internal/libraries/service.go` SetRules validated with
`libraryfilter.CompilePolicy`, which decoded browse fields by reflection without
checking the Type enum. Thus the library writer accepted `"Movies"` while the
startup compiler rejected it. The v0.3.12 commit `a58395bae` introduced
browse conditions and bound them during catalogue startup. The same commit
bound PutViewerRules during writes, so that writer already rejected this value;
older or externally written viewer documents remain another recovery boundary.
Without the reporter's database we cannot identify their exact offending row.
AI collection builders, smart collections, imports and owner includes do not
write this startup document through their normal paths. Owner includes store
rating keys; canonical owner rules live in `libraries.rules`.

Stop Ether, copy `ether.db`, then run:

```sh
sqlite3 /path/to/ether.db < docs/troubleshooting/startup-537.sql
```

The script repairs `browse:type` values of exactly `Movies` in both
`catalog_viewer_rules.document` and `libraries.rules`, including nested groups.
Rule names, title search text and unrelated fields are preserved. A test SQLite
database verified multiple nested occurrences in both tables. Other malformed
rules require v0.3.13 recovery or an owner edit; this script never deletes rules.

v0.3.13 accepts historical spellings on read and write, migrates viewer rules,
library/owner rules and collection specs once, and replaces Type and the other
finite browse enums with selects. Invalid viewer documents leave the server
running and hide titles for that viewer, including direct lookups and owner
includes. The viewer's rules drawer displays a warning and lets the owner edit
or remove the invalid rules. Canonical libraries compile independently and a
broken library stays hidden while its rules are repaired.
