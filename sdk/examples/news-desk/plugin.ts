/**
 * News desk: the smallest complete plugin screen (docs/plugin-screens.md).
 *
 * - screens/desk.json is the screen: Vela's node language plus two sources,
 *   `front` (shared by every viewer) and `mine` (this viewer's follows).
 * - The `follow-topic` action is declared in the manifest; Ether checks its
 *   arguments and the viewer before calling us.
 * - Pictures go through `asset()` (a file in this package) or `image()` (a
 *   URL on one of your permissions.network hosts): never as a bare URL.
 *
 * A real desk would fetch a feed with ether.http and keep stories as
 * records it updates with ether.screens.putRecords; this one is static so it
 * runs offline.
 */
import { asset, plugin, type PluginContext } from "../../typescript/src/index.ts";
import manifest from "./manifest.json" with { type: "json" };

type Topic = "top" | "local" | "tech";

const TOPICS: { id: Topic; name: string }[] = [
  { id: "top", name: "Top stories" },
  { id: "local", name: "Local" },
  { id: "tech", name: "Technology" },
];

const STORIES: Record<Topic, { title: string; section: string; published: string }[]> = {
  top: [
    { title: "Council approves the new river crossing", section: "City", published: "2026-11-24T09:10:00Z" },
    { title: "Winter storm watch for the weekend", section: "Weather", published: "2026-11-24T08:30:00Z" },
  ],
  local: [{ title: "Library extends evening hours", section: "Community", published: "2026-11-24T07:45:00Z" }],
  tech: [{ title: "A smaller, faster media server, explained", section: "Software", published: "2026-11-23T18:00:00Z" }],
};

function topicOf(raw: unknown): Topic {
  return raw === "local" || raw === "tech" ? raw : "top";
}

// Follows are per viewer: viewer-scoped sources and actions carry the viewer
// (X-Ether-Viewer), so they key the plugin's KV store by viewer.
function followKey(ctx: PluginContext): string {
  const viewer = ctx.headers["x-ether-viewer"] ?? ctx.headers["X-Ether-Viewer"] ?? "0";
  return `follows:${Array.isArray(viewer) ? viewer[0] : viewer}`;
}

function follows(ctx: PluginContext): Topic[] {
  const raw = ctx.ether.kv.get(followKey(ctx)) as string | null | undefined;
  return raw ? (JSON.parse(raw) as Topic[]) : [];
}

export function createPlugin() {
  const p = plugin({ manifest: manifest as never, routes: {} });

  p.onScreenSource("desk", "front", ({ args }) => {
    const topic = topicOf(args.topic);
    return {
      value: {
        topics: TOPICS,
        topicName: TOPICS.find((t) => t.id === topic)?.name ?? "",
        breaking: topic === "top" ? { title: "Storm watch issued for Saturday" } : null,
        stories: STORIES[topic].map((s) => ({ ...s, image: asset("assets/story.svg") })),
      },
      // Every viewer shares this answer for two minutes; while someone has
      // the desk open, Ether asks again every minute.
      maxAgeSec: 120,
      nextRefreshSec: 60,
    };
  });

  p.onScreenSource("desk", "following", (req, ctx) => {
    const topic = topicOf(req.args.topic);
    return { value: { followsTopic: follows(ctx).includes(topic), topics: follows(ctx) }, maxAgeSec: 5 };
  });

  p.onScreenAction("desk", "follow-topic", ({ args }, ctx) => {
    const topic = topicOf(args.topic);
    const on = args.on === true;
    const next = follows(ctx).filter((t) => t !== topic);
    if (on) next.push(topic);
    ctx.ether.kv.put(followKey(ctx), JSON.stringify(next));
    // Patch the viewer's `mine` source at once; the toast is plain text.
    return {
      sources: { mine: { followsTopic: on, topics: next } },
      toast: on ? "Following" : "Unfollowed",
    };
  });

  return p;
}

export const handle = createPlugin().handle;
