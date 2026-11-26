# Release server

A `stream_source` plugin that answers category searches, so content kinds
such as Music (`category: "3000"`) and Books (`"7000"`) can find and play
releases from the owner's own release server.

- `manifest.json` declares `contributes.source.search` with the categories
  (and the `album` lookup) the plugin answers. Ether asks it for nothing else
  on a category search, and every add-on without the declaration keeps
  refusing scoped searches.
- `stream` answers movie and show lookups by title, as any source plugin does.
- `sourceSearch` (`GET /ether/search`) answers lazy releases for the declared
  categories.
- `releaseFiles` (`GET /ether/files/{id}`) lists a release's files.
- `openStream` (`GET /ether/open/{id}?file=&fileIndex=`) returns the file's
  URL. Ether reads it with Range requests and only on hosts the owner
  approved for the plugin (`permissions.network: ["operator"]`).

A Music or Books plugin then sees candidates with
`pluginRelease: {plugin, id}` in its `indexer_search` results, lists their
files with `release_files`, and returns a `release` source with
`release.file` or `release.fileIndex`.

See "Source plugins that answer category searches" in
[docs/plugin-kinds.md](../../../docs/plugin-kinds.md).
