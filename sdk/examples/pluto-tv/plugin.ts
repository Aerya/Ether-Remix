import {
  plugin,
  type Manifest,
  type HostFetchResponse,
  type PluginContext,
  type EtherHost,
} from "../../typescript/src/index.ts";
import manifest from "./manifest.json" with { type: "json" };

type Settings = { region: string };
type Timeline = {
  _id?: string;
  start: string;
  stop: string;
  title: string;
  episode?: { description?: string };
};
type Programme = {
  id?: string;
  channelId: string;
  title: string;
  start: string;
  stop: string;
  description?: string;
};
type Channel = {
  _id: string;
  name: string;
  number?: number;
  category?: string;
  colorLogoPNG?: { path?: string };
  stitched?: { urls?: { type: string; url: string }[] };
  timelines?: Timeline[];
};

const HOUR_MS = 3600000;
const GUIDE_BACK_MS = 2 * HOUR_MS;
const GUIDE_AHEAD_MS = 24 * HOUR_MS;
const GUIDE_SLICE_MS = 4 * HOUR_MS;
const MIN_GUIDE_SLICE_MS = HOUR_MS;

function fetchJSON(ctx: PluginContext<Settings>, path: string): unknown {
  const region = ctx.settings.region || "auto";
  const sep = path.includes("?") ? "&" : "?";
  const result = ctx.ether.http.fetch({
    url: `https://api.pluto.tv${path}${sep}marketingRegion=${encodeURIComponent(region)}`,
  }) as HostFetchResponse;
  if (result.status !== 200)
    throw new Error(`Pluto TV returned HTTP ${result.status}`);
  return JSON.parse(result.body);
}

function mapTimelines(channelId: string, timelines?: Timeline[]): Programme[] {
  return (timelines || [])
    .filter((t) => t.start && t.stop && t.title)
    .map((t) => ({
      id: t._id,
      channelId,
      title: t.title,
      start: t.start,
      stop: t.stop,
      description: t.episode?.description,
    }));
}

function addTimelines(
  progMap: Map<string, Programme>,
  channelId: string,
  timelines?: Timeline[],
) {
  for (const p of mapTimelines(channelId, timelines)) {
    const key = p.id || `${p.start}\0${p.stop}\0${p.title}`;
    if (!progMap.has(key)) {
      progMap.set(key, p);
    }
  }
}

function compareProgrammeStart(a: Programme, b: Programme): number {
  const ta = Date.parse(a.start);
  const tb = Date.parse(b.start);
  if (!Number.isNaN(ta) && !Number.isNaN(tb) && ta !== tb) return ta - tb;
  return a.start < b.start ? -1 : a.start > b.start ? 1 : 0;
}

function isTooLargeError(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err);
  return /response larger than|exceeds size cap|too large/i.test(msg);
}

function getChannels(ctx: PluginContext<Settings>): Channel[] {
  const data = fetchJSON(ctx, "/v2/channels.json");
  if (!Array.isArray(data)) return [];
  const configured = ctx.settings.region || "auto";
  const url = data[0]?.stitched?.urls?.[0]?.url || "";
  const detected = /[?&]marketingRegion=([^&]+)/i.exec(url)?.[1]?.toLowerCase();
  if (configured !== "auto" && detected && configured !== detected)
    throw new Error(
      "Pluto TV returned a different region; check server location",
    );
  return data;
}

function fetchGuideSlice(
  ctx: PluginContext<Settings>,
  fromMs: number,
  toMs: number,
  byChannel: Map<string, Map<string, Programme>>,
) {
  const start = new Date(fromMs).toISOString();
  const stop = new Date(toMs).toISOString();
  const path = `/v2/channels.json?start=${encodeURIComponent(start)}&stop=${encodeURIComponent(stop)}`;
  let data: unknown;
  try {
    data = fetchJSON(ctx, path);
  } catch (err) {
    const code = (err as { code?: string } | null)?.code;
    if (code === "governance" || code === "forbidden" || code === "aborted") {
      throw err;
    }
    if (isTooLargeError(err)) {
      const half = Math.floor((toMs - fromMs) / 2);
      if (half >= MIN_GUIDE_SLICE_MS) {
        fetchGuideSlice(ctx, fromMs, fromMs + half, byChannel);
        fetchGuideSlice(ctx, fromMs + half, toMs, byChannel);
      }
    }
    return;
  }
  if (!Array.isArray(data)) return;
  for (const ch of data as Channel[]) {
    const progMap = ch?._id ? byChannel.get(ch._id) : undefined;
    if (progMap && ch.timelines?.length) {
      addTimelines(progMap, ch._id, ch.timelines);
    }
  }
}

function streamURL(ctx: PluginContext<Settings>, id: string): string {
  const client = "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(
    /[xy]/g,
    (c) => {
      const r = Math.floor(Math.random() * 16);
      return (c === "x" ? r : (r & 3) | 8).toString(16);
    },
  );
  const params = `appName=web&appVersion=8.0.0&deviceVersion=122.0.0&deviceModel=web&deviceMake=chrome&deviceType=web&clientID=${client}&clientModelNumber=1.0.0&serverSideAds=false&drmCapabilities=widevine%3AL3`;
  const result = ctx.ether.http.fetch({
    url: `https://boot.pluto.tv/v4/start?${params}`,
    headers: { Origin: "https://pluto.tv", Referer: "https://pluto.tv/" },
  }) as HostFetchResponse;
  if (result.status !== 200)
    throw new Error(`Pluto TV session returned HTTP ${result.status}`);
  const boot = JSON.parse(result.body) as {
    sessionToken?: string;
    stitcherParams?: string;
    servers?: { stitcher?: string };
  };
  const base = boot.servers?.stitcher || "";
  if (!boot.sessionToken || !/^https:\/\/[a-z0-9.-]+\.pluto\.tv$/i.test(base))
    throw new Error("Pluto TV session response is invalid");
  return `${base}/v2/stitch/hls/channel/${encodeURIComponent(id)}/master.m3u8?jwt=${encodeURIComponent(boot.sessionToken)}&masterJWTPassthrough=true${boot.stitcherParams ? "&" + boot.stitcherParams : ""}`;
}

export function createPlugin(host?: EtherHost) {
  return plugin<Settings>({
    manifest: manifest as Manifest,
    host,
    defaultSettings: { region: "auto" },
    routes: {
      channels(ctx) {
        const lineup = getChannels(ctx).filter(
          (c) =>
            c._id &&
            c.name &&
            c.category?.toLowerCase() !== "testing" &&
            c.stitched?.urls?.some((u) => u.type === "hls" && u.url),
        );
        const byChannel = new Map<string, Map<string, Programme>>();
        for (const c of lineup) {
          const progMap = new Map<string, Programme>();
          if (c.timelines?.length) {
            addTimelines(progMap, c._id, c.timelines);
            c.timelines = undefined;
          }
          byChannel.set(c._id, progMap);
        }
        if (lineup.length > 0) {
          const now = Date.now();
          const windowStart = now - GUIDE_BACK_MS;
          const windowEnd = now + GUIDE_AHEAD_MS;
          for (
            let from = windowStart;
            from < windowEnd;
            from += GUIDE_SLICE_MS
          ) {
            fetchGuideSlice(
              ctx,
              from,
              Math.min(from + GUIDE_SLICE_MS, windowEnd),
              byChannel,
            );
          }
        }
        return {
          channels: lineup.map((c) => {
            const programmes = Array.from(
              byChannel.get(c._id)?.values() ?? [],
            ).sort(compareProgrammeStart);
            return {
              id: c._id,
              name: c.name,
              number: c.number,
              group: c.category,
              logo: c.colorLogoPNG?.path,
              guideId: c._id,
              inputs: [
                {
                  url: `plugin://tv.ether.examples.pluto/stream/tv/${encodeURIComponent(c._id)}`,
                },
              ],
              ...(programmes.length > 0 ? { programmes } : {}),
            };
          }),
        };
      },
      guide(req, ctx) {
        const query = `?start=${encodeURIComponent(req.from || new Date().toISOString())}&stop=${encodeURIComponent(req.to || new Date(Date.now() + 86400000).toISOString())}`;
        const channel = fetchJSON(
          ctx,
          `/v2/channels/${encodeURIComponent(req.channelId)}${query}`,
        ) as Channel;
        return {
          programmes: mapTimelines(req.channelId, channel.timelines),
        };
      },
      stream(req, ctx) {
        const channel = getChannels(ctx).find((c) => c._id === req.id);
        const url = channel ? streamURL(ctx, channel._id) : undefined;
        return { streams: url ? [{ name: channel!.name, url }] : [] };
      },
      test(ctx) {
        return { ok: getChannels(ctx).length > 0 };
      },
    },
  });
}
export const handle = createPlugin().handle;
