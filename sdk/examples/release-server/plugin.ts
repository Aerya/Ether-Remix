// A stream_source plugin that answers Music and Books searches (#792).
//
// It fronts an owner-run release server with a small JSON API:
//   GET {server}/api/search?q=&cat=      -> [{id, name, size}]
//   GET {server}/api/releases/{id}       -> {files: [{path, size}]}
//   GET {server}/stream/{id}/{fileIndex} -> the file's bytes, with Range
// Adapt the three fetches to your server; the Ether side stays the same.
//
// Ether calls sourceSearch only for the categories declared in
// manifest.json (contributes.source.search), lists a release's files with
// releaseFiles, and opens the chosen file with openStream. The URL openStream
// returns must be on an owner-approved host (permissions.network "operator").
import {
  plugin,
  type EtherHost,
  type HostFetchResponse,
  type Manifest,
  type PluginContext,
  type SourceSearchRequest,
} from "../../typescript/src/index.ts";
import manifest from "./manifest.json" with { type: "json" };
import en from "./i18n/en.json" with { type: "json" };

type Settings = { server?: string };
type Release = { id: string; name: string; size?: number };

function base(ctx: PluginContext<Settings>): string {
  const server = (ctx.settings.server ?? "").trim().replace(/\/+$/, "");
  if (!server) throw new Error("Set the release server address in the plugin settings.");
  return server;
}

function getJSON<T>(ctx: PluginContext<Settings>, path: string): T {
  const res = ctx.ether.http.fetch({ url: base(ctx) + path }) as HostFetchResponse;
  if (res.status !== 200) throw new Error(`Release server returned HTTP ${res.status}`);
  return JSON.parse(res.body) as T;
}

// An album lookup sharpens the text query when the caller sent one.
function searchText(req: SourceSearchRequest): string {
  const f = req.lookup?.type === "album" ? req.lookup.fields ?? {} : {};
  const typed = [f.artist, f.title].filter(Boolean).join(" ");
  return typed || req.query;
}

// Lazy releases: Ether lists their files and opens them only on a pick.
function lazy(found: Release[]) {
  return found.slice(0, 100).map((r) => ({
    name: "Release server",
    ether: { lazy: true, id: r.id, releaseName: r.name, size: r.size },
  }));
}

export function createPlugin(host?: EtherHost) {
  return plugin<Settings>({
    manifest: manifest as Manifest,
    strings: { en },
    host,
    routes: {
      // Movies and shows: Ether asks by title id and passes title hints.
      stream(req, ctx) {
        const title = ctx.query.title;
        if (!title) return { streams: [] };
        const q = encodeURIComponent([title, ctx.query.year].filter(Boolean).join(" "));
        const cat = req.type === "movie" ? 2000 : 5000;
        return { streams: lazy(getJSON<Release[]>(ctx, `/api/search?q=${q}&cat=${cat}`)) };
      },
      // Music and books: only the categories manifest.json declares.
      sourceSearch(req, ctx) {
        const q = encodeURIComponent(searchText(req));
        const cat = encodeURIComponent(req.categories.join(","));
        return { streams: lazy(getJSON<Release[]>(ctx, `/api/search?q=${q}&cat=${cat}`)) };
      },
      releaseFiles(req, ctx) {
        return getJSON<{ files: { path: string; size?: number }[] }>(ctx, `/api/releases/${encodeURIComponent(req.id)}`);
      },
      openStream(req, ctx) {
        // Ether passes the file its caller picked from releaseFiles.
        let index = req.fileIndex ?? 0;
        if (req.file) {
          const { files } = getJSON<{ files: { path: string }[] }>(ctx, `/api/releases/${encodeURIComponent(req.streamId)}`);
          index = files.findIndex((f) => f.path === req.file);
          if (index < 0) throw new Error("That file is not in the release.");
        }
        return { url: `${base(ctx)}/stream/${encodeURIComponent(req.streamId)}/${index}` };
      },
    },
  });
}
export const handle = createPlugin().handle;
