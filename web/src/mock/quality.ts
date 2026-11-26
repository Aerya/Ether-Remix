/**
 * Quality mock routes: GET /quality/devices, GET /quality/clients,
 * GET /quality/summary, and POST /quality/explain against the ordered rules
 * model (contract v1.3).
 */
import type {
  DeviceMatch,
  EtherConfig,
  QualityAnswer,
  QualityClient,
  QualityDecisionSource,
  QualityDevices,
  QualityLayer,
  QualityProfile,
  QualityQuestion,
  QualityRule,
  QualitySummary,
  QualitySummaryCell,
  QualitySummaryCellPair,
  QualitySummaryException,
  QualitySummaryRow,
} from "../lib/types";

type Reply = { status: number; body: unknown };

const ok = (body: unknown, status = 200): Reply => ({ status, body });
const fail = (status: number, code: string, message: string): Reply => ({
  status,
  body: { error: { code, message } },
});

export interface MockLibrarySummaryInput {
  id: number;
  name: string;
  source: string;
  mediaType?: string;
  pluginId?: string;
  pluginName?: string;
  sort: number;
  hidden?: boolean;
}

export interface MockTitleQualityInput {
  kind: "movie" | "episode";
  libraryIds: number[];
  chosenByPlugin?: boolean;
}

type ClientCapsShape = {
  maxHeight: number;
  hdr: boolean;
  dolbyVision: boolean;
  androidMobile?: boolean;
};

const CLIENTS: (QualityClient &
  ClientCapsShape & { group: "tv" | "mobile" | "computer"; type: string; app: string })[] = [
  {
    id: "plex-apple-tv",
    name: "Plex for Apple TV",
    product: "Plex for Apple TV",
    platform: "tvOS",
    device: "Apple TV",
    group: "tv",
    type: "Apple TV",
    app: "plex:Plex for Apple TV",
    maxHeight: 2160,
    hdr: true,
    dolbyVision: true,
  },
  {
    id: "plex-ios",
    name: "Plex for iOS",
    product: "Plex for iOS",
    platform: "iOS",
    device: "iPhone",
    group: "mobile",
    type: "iPhone",
    app: "plex:Plex for iOS",
    maxHeight: 2160,
    hdr: true,
    dolbyVision: true,
  },
  {
    id: "plex-android",
    name: "Plex for Android",
    product: "Plex for Android (Mobile)",
    platform: "Android",
    device: "Pixel 8",
    group: "mobile",
    type: "Android Phone",
    app: "plex:Plex for Android",
    maxHeight: 1080,
    hdr: false,
    dolbyVision: false,
    androidMobile: true,
  },
  {
    id: "plex-roku",
    name: "Plex for Roku",
    product: "Plex for Roku",
    platform: "Roku",
    device: "Roku",
    group: "tv",
    type: "Roku",
    app: "plex:Plex for Roku",
    maxHeight: 2160,
    hdr: true,
    dolbyVision: false,
  },
  {
    id: "plex-web-chrome",
    name: "Plex Web (Chrome)",
    product: "Plex Web",
    platform: "Chrome",
    device: "Chrome",
    group: "computer",
    type: "Web Browser",
    app: "plex:Plex Web",
    maxHeight: 2160,
    hdr: false,
    dolbyVision: false,
  },
];

export const MOCK_QUALITY_DEVICES: QualityDevices = {
  groups: [
    { id: "tv", name: "TVs & streaming boxes" },
    { id: "mobile", name: "Phones & tablets" },
    { id: "computer", name: "Computers & browsers" },
  ],
  types: [
    { id: "Apple TV", name: "Apple TV", group: "tv" },
    { id: "NVIDIA Shield", name: "NVIDIA Shield", group: "tv" },
    { id: "Roku", name: "Roku", group: "tv" },
    { id: "Fire TV", name: "Fire TV", group: "tv" },
    { id: "iPhone", name: "iPhone", group: "mobile" },
    { id: "iPad", name: "iPad", group: "mobile" },
    { id: "Android Phone", name: "Android Phone", group: "mobile" },
    { id: "Web Browser", name: "Web Browser", group: "computer" },
  ],
  apps: [
    {
      id: "plex:Plex for Apple TV",
      name: "Plex for Apple TV",
      protocol: "plex",
      devices: ["Apple TV"],
    },
    {
      id: "plex:Plex for Android TV",
      name: "Plex for Android TV",
      protocol: "plex",
      devices: ["NVIDIA Shield"],
    },
    {
      id: "plex:Plex for iOS",
      name: "Plex for iOS",
      protocol: "plex",
      devices: ["iPhone", "iPad"],
    },
    {
      id: "plex:Plex for Android",
      name: "Plex for Android",
      protocol: "plex",
      devices: ["Android Phone"],
    },
    { id: "plex:Plex for Roku", name: "Plex for Roku", protocol: "plex", devices: ["Roku"] },
    { id: "plex:Plex Web", name: "Plex Web", protocol: "plex", devices: ["Web Browser"] },
    {
      id: "jellyfin:Infuse",
      name: "Infuse",
      protocol: "jellyfin",
      devices: ["Apple TV", "iPhone", "iPad"],
    },
    { id: "vela:Vela", name: "Vela", protocol: "vela" },
  ],
  clients: [
    {
      id: "client-shield-lr",
      name: "Living room Shield",
      group: "tv",
      type: "NVIDIA Shield",
      app: "plex:Plex for Android TV",
      viewer: "Alex Rivera",
      lastSeen: new Date(Date.now() - 2 * 3600 * 1000).toISOString(),
    },
    {
      id: "client-atv-basement",
      name: "Basement Apple TV",
      group: "tv",
      type: "Apple TV",
      app: "vela:Vela",
      viewer: "Alex Rivera",
      lastSeen: new Date(Date.now() - 24 * 3600 * 1000).toISOString(),
    },
    {
      id: "client-sam-iphone",
      name: "Sam's iPhone",
      group: "mobile",
      type: "iPhone",
      app: "jellyfin:Infuse",
      viewer: "Sam Rivera",
      lastSeen: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
    },
    {
      id: "client-pixel-8",
      name: "Pixel 8",
      group: "mobile",
      type: "Android Phone",
      app: "plex:Plex for Android",
      viewer: "Sam Rivera",
      lastSeen: new Date(Date.now() - 30 * 60 * 1000).toISOString(),
    },
  ],
};

interface DeviceContext {
  field: "" | "group" | "type" | "app" | "client" | "contains";
  id: string;
  group?: string;
  typeId?: string;
  appId?: string;
  clientId?: string;
  product?: string;
  platform?: string;
  device?: string;
  caps: ClientCapsShape;
}

const GENERIC_CAPS: ClientCapsShape = { maxHeight: 2160, hdr: true, dolbyVision: true };

function capsForSeen(
  seen: NonNullable<QualityDevices["clients"]>[number] | undefined,
): ClientCapsShape {
  if (!seen) return GENERIC_CAPS;
  if (seen.type === "Android Phone") {
    return { maxHeight: 1080, hdr: false, dolbyVision: false, androidMobile: true };
  }
  if (seen.group === "computer") {
    return { maxHeight: 2160, hdr: false, dolbyVision: false };
  }
  if (seen.type === "Roku") {
    return { maxHeight: 2160, hdr: true, dolbyVision: false };
  }
  return GENERIC_CAPS;
}

function parseDeviceParam(param: string, catalog: QualityDevices): DeviceContext {
  if (!param) return { field: "", id: "", caps: GENERIC_CAPS };
  const idx = param.indexOf(":");
  if (idx <= 0) return { field: "", id: "", caps: GENERIC_CAPS };
  const field = param.slice(0, idx) as DeviceContext["field"];
  const id = param.slice(idx + 1);
  const clients = catalog.clients ?? [];
  const apps = catalog.apps ?? [];
  const types = catalog.types ?? [];

  if (field === "client") {
    const seen = clients.find((c) => c.id === id);
    const app = seen ? apps.find((a) => a.id === seen.app) : undefined;
    return {
      field: "client",
      id,
      clientId: id,
      group: seen?.group,
      typeId: seen?.type,
      appId: seen?.app,
      product: app?.name ?? seen?.name,
      device: seen?.type,
      caps: capsForSeen(seen),
    };
  }
  if (field === "group") {
    return { field: "group", id, group: id, caps: GENERIC_CAPS };
  }
  if (field === "type") {
    const dt = types.find((t) => t.id === id);
    return {
      field: "type",
      id,
      typeId: id,
      group: dt?.group,
      device: id,
      caps:
        id === "Android Phone"
          ? { maxHeight: 1080, hdr: false, dolbyVision: false, androidMobile: true }
          : GENERIC_CAPS,
    };
  }
  if (field === "app") {
    const ap = apps.find((a) => a.id === id);
    const preset = CLIENTS.find((c) => c.app === id);
    return {
      field: "app",
      id,
      appId: id,
      group: preset?.group,
      typeId: preset?.type,
      product: ap?.name ?? id,
      platform: preset?.platform,
      device: preset?.device,
      caps: preset ?? GENERIC_CAPS,
    };
  }
  return { field: "contains", id, product: id, caps: GENERIC_CAPS };
}

function resolveExplainDevice(
  q: QualityQuestion,
  catalog: QualityDevices,
): { dev: DeviceContext } | { error: Reply } {
  if (q.clientId === "any") {
    return { dev: { field: "", id: "any", caps: GENERIC_CAPS } };
  }
  if (q.clientId) {
    const seen = (catalog.clients ?? []).find((c) => c.id === q.clientId);
    if (seen) return { dev: parseDeviceParam(`client:${seen.id}`, catalog) };
    const preset = CLIENTS.find((c) => c.id === q.clientId);
    if (preset) {
      return {
        dev: {
          field: "app",
          id: preset.app,
          appId: preset.app,
          group: preset.group,
          typeId: preset.type,
          product: preset.product,
          platform: preset.platform,
          device: preset.device,
          caps: preset,
        },
      };
    }
    if (q.clientId.includes(":")) {
      return { dev: parseDeviceParam(q.clientId, catalog) };
    }
    return { error: fail(400, "unknown_client", "no such device or app") };
  }
  if (q.client?.product?.trim()) {
    const prod = q.client.product.trim();
    const plat = q.client.platform?.trim();
    const dev = q.client.device?.trim();
    const preset = CLIENTS.find(
      (c) =>
        c.product.toLowerCase() === prod.toLowerCase() ||
        c.name.toLowerCase() === prod.toLowerCase(),
    );
    const app = (catalog.apps ?? []).find((a) => a.name.toLowerCase() === prod.toLowerCase());
    const dt = dev
      ? (catalog.types ?? []).find((t) => t.id.toLowerCase() === dev.toLowerCase())
      : undefined;
    return {
      dev: {
        field: preset ? "app" : "contains",
        id: preset?.app ?? app?.id ?? prod,
        appId: preset?.app ?? app?.id,
        typeId: dt?.id ?? preset?.type,
        group: dt?.group ?? preset?.group,
        product: prod,
        platform: plat ?? preset?.platform,
        device: dev ?? preset?.device,
        caps: preset ?? GENERIC_CAPS,
      },
    };
  }
  return { error: fail(400, "missing_client", "choose a device or app") };
}

function matchesSingleDevice(m: DeviceMatch, dev: DeviceContext): boolean {
  if (!dev.field) return false;
  if (m.group) return dev.group === m.group;
  if (m.type) return dev.typeId === m.type;
  if (m.app) return dev.appId === m.app;
  if (m.client) return dev.clientId === m.client;
  if (m.contains) {
    const needle = m.contains.toLowerCase();
    return [dev.product, dev.platform, dev.device, dev.id].some((v) =>
      Boolean(v && v.toLowerCase().includes(needle)),
    );
  }
  return false;
}

function matchesRuleViewer(r: QualityRule, viewerId: number): boolean {
  if (!r.viewers?.length) return true;
  if (!viewerId) return false;
  const hit = r.viewers.includes(viewerId);
  return r.exceptViewers ? !hit : hit;
}

function matchesRuleDevice(r: QualityRule, dev: DeviceContext): boolean {
  if (!r.devices?.length) return true;
  if (!dev.field) return false;
  const hit = r.devices.some((m) => matchesSingleDevice(m, dev));
  return r.exceptDevices ? !hit : hit;
}

function libraryKinds(lib: MockLibrarySummaryInput): ("movie" | "episode")[] {
  if (lib.source === "plugin") return [];
  if (lib.mediaType === "movie") return ["movie"];
  if (lib.mediaType === "show") return ["episode"];
  return ["movie", "episode"];
}

function libraryRelation(
  r: QualityRule,
  lib: MockLibrarySummaryInput,
  allLibs: MockLibrarySummaryInput[],
): "direct" | "partial" | "none" {
  if (!r.libraries?.length) return "direct";
  const inList = r.libraries.includes(lib.id);
  if (r.exceptLibraries) {
    return inList ? "none" : "direct";
  }
  if (inList) return "direct";
  if (lib.source === "plugin") return "none";
  const myKinds = libraryKinds(lib);
  const overlaps = r.libraries.some((otherId) => {
    const other = allLibs.find((l) => l.id === otherId);
    if (!other || other.source === "plugin") return false;
    const otherKinds = libraryKinds(other);
    return myKinds.some((k) => otherKinds.includes(k));
  });
  return overlaps ? "partial" : "none";
}

function effectiveRules(
  config: EtherConfig,
  getPolicy: (id: number) => Record<string, unknown> | undefined,
  viewerHint?: number,
): QualityRule[] {
  const base = (config.quality.rules ?? []).map((r, i) => ({
    ...r,
    id: r.id || `r_${i + 1}`,
  }));
  const out = [...base];
  const viewerIds = new Set<number>([1, 2, 3, 4, 5, 6, 7]);
  if (viewerHint) viewerIds.add(viewerHint);
  for (const uid of viewerIds) {
    const cap = Number(getPolicy(uid)?.maxBitrateKbps) || 0;
    if (cap > 0 && !out.some((r) => r.viewers?.includes(uid) && r.maxBitrateKbps === cap)) {
      out.push({
        id: `r_viewer_${uid}`,
        viewers: [uid],
        maxBitrateKbps: cap,
      });
    }
  }
  return out;
}

export function createQualityMock() {
  function summarize(
    q: URLSearchParams,
    config: EtherConfig,
    libs: MockLibrarySummaryInput[],
    getPolicy: (id: number) => Record<string, unknown> | undefined,
  ): Reply {
    const viewerId = Number(q.get("viewer") || 0);
    const deviceParam = q.get("device") || "";
    const dev = parseDeviceParam(deviceParam, MOCK_QUALITY_DEVICES);
    const unfiltered = !viewerId && !deviceParam;
    const rules = effectiveRules(config, getPolicy, viewerId || undefined);
    const profiles = config.quality.profiles ?? [];
    const defaultHome = config.quality.default || profiles[0]?.name || "4K";
    const defaultAway = config.quality.defaultAway ?? "";

    const sortedLibs = [...libs].sort((a, b) => a.sort - b.sort || a.id - b.id);
    const rows: QualitySummaryRow[] = sortedLibs.map((lib) => {
      const chosenByPlugin = lib.source === "plugin";
      const plugin = chosenByPlugin ? lib.pluginName || lib.pluginId || "" : "";
      const kinds = libraryKinds(lib);

      const evalCell = (
        cellKind: "" | "movie" | "episode",
        where: "home" | "away",
      ): QualitySummaryCell => {
        let profile = "";
        let source: QualityDecisionSource = chosenByPlugin ? "plugin" : "default";
        let profileRuleId: string | undefined;
        let maxHeight = 0;
        let maxBitrateKbps = 0;
        let stereo = false;
        const limitRuleIds: string[] = [];
        const partialRuleIds: string[] = [];

        for (const r of rules) {
          if (unfiltered && (Boolean(r.viewers?.length) || Boolean(r.devices?.length))) continue;
          if (!unfiltered) {
            if (!matchesRuleViewer(r, viewerId)) continue;
            if (!matchesRuleDevice(r, dev)) continue;
          }
          if (r.where && r.where !== where) continue;
          if (r.kind) {
            if (chosenByPlugin) continue;
            if (cellKind && r.kind !== cellKind) continue;
          }
          const rel = libraryRelation(r, lib, sortedLibs);
          if (rel === "none") continue;
          const hasLimits = Boolean(r.maxHeight || r.maxBitrateKbps || r.stereoAudio);
          const picksProfile = !chosenByPlugin && !profile && Boolean(r.profile);
          if (rel === "partial") {
            if ((picksProfile || hasLimits) && !partialRuleIds.includes(r.id)) {
              partialRuleIds.push(r.id);
            }
            continue;
          }
          if (picksProfile && r.profile) {
            profile = r.profile;
            source = "rule";
            profileRuleId = r.id;
          }
          if (hasLimits) {
            if (r.maxHeight && (!maxHeight || r.maxHeight < maxHeight)) maxHeight = r.maxHeight;
            if (r.maxBitrateKbps && (!maxBitrateKbps || r.maxBitrateKbps < maxBitrateKbps)) {
              maxBitrateKbps = r.maxBitrateKbps;
            }
            if (r.stereoAudio) stereo = true;
            limitRuleIds.push(r.id);
          }
        }

        if (chosenByPlugin) {
          profile = "";
          source = "plugin";
        } else if (!profile) {
          // The fallback row decides, and its limits apply with the rules'.
          const away = where === "away" && Boolean(defaultAway);
          profile = away ? defaultAway : defaultHome;
          source = away ? "defaultAway" : "default";
          const l = (away ? config.quality.defaultAwayLimits : config.quality.defaultLimits) ?? {};
          if (l.maxHeight && (!maxHeight || l.maxHeight < maxHeight)) maxHeight = l.maxHeight;
          if (l.maxBitrateKbps && (!maxBitrateKbps || l.maxBitrateKbps < maxBitrateKbps)) {
            maxBitrateKbps = l.maxBitrateKbps;
          }
          if (l.stereoAudio) stereo = true;
          if (l.maxHeight || l.maxBitrateKbps || l.stereoAudio) limitRuleIds.push(source);
        }

        return {
          profile,
          source,
          ...(profileRuleId ? { profileRuleId } : {}),
          maxHeight,
          maxBitrateKbps,
          stereo,
          limitRuleIds,
          partialRuleIds,
        };
      };

      const splitsByKind =
        !chosenByPlugin &&
        kinds.length === 2 &&
        rules.some((r) => {
          if (!r.kind) return false;
          if (!unfiltered && (!matchesRuleViewer(r, viewerId) || !matchesRuleDevice(r, dev))) {
            return false;
          }
          return libraryRelation(r, lib, sortedLibs) !== "none";
        });

      const cellKinds: ("" | "movie" | "episode")[] = chosenByPlugin
        ? [""]
        : splitsByKind
          ? ["movie", "episode"]
          : kinds.length === 1
            ? [kinds[0]!]
            : [""];

      const cells: QualitySummaryCellPair[] = cellKinds.map((k) => ({
        kind: k,
        home: evalCell(k, "home"),
        away: evalCell(k, "away"),
      }));

      const exceptions: QualitySummaryException[] = [];
      if (unfiltered) {
        for (const r of rules) {
          if (!r.viewers?.length && !r.devices?.length) continue;
          if (r.kind && chosenByPlugin) continue;
          if (r.kind && kinds.length === 1 && r.kind !== kinds[0]) continue;
          if (libraryRelation(r, lib, sortedLibs) === "none") continue;
          const hasEffect =
            (!chosenByPlugin && Boolean(r.profile)) ||
            Boolean(r.maxHeight || r.maxBitrateKbps || r.stereoAudio);
          if (!hasEffect) continue;
          exceptions.push({
            ruleId: r.id,
            profile: chosenByPlugin ? "" : (r.profile ?? ""),
            maxHeight: r.maxHeight ?? 0,
            maxBitrateKbps: r.maxBitrateKbps ?? 0,
            stereo: Boolean(r.stereoAudio),
          });
        }
      }

      return {
        libraryId: lib.id,
        library: lib.name,
        plugin,
        chosenByPlugin,
        cells,
        exceptions,
      };
    });

    const summary: QualitySummary = { rows };
    return ok(summary);
  }

  function explain(
    q: QualityQuestion & { mediaType?: string },
    config: EtherConfig,
    libs: MockLibrarySummaryInput[],
    policy: Record<string, unknown> | undefined,
    getPolicy: (id: number) => Record<string, unknown> | undefined,
    resolveTitle?: (key: string) => MockTitleQualityInput | null,
  ): Reply {
    const resolved = resolveExplainDevice(q, MOCK_QUALITY_DEVICES);
    if ("error" in resolved) return resolved.error;
    const { dev } = resolved;
    const profiles = config.quality.profiles ?? [];
    const pick = (name: string | undefined): QualityProfile | undefined =>
      name ? profiles.find((p) => p.name === name) : undefined;

    const titleInfo = q.titleKey && resolveTitle ? resolveTitle(q.titleKey) : null;
    if (q.titleKey && resolveTitle && !titleInfo) {
      return fail(404, "unknown_title", "no such movie or episode");
    }
    const targetLib = q.libraryId ? libs.find((l) => l.id === q.libraryId) : undefined;
    const memberLibs = titleInfo
      ? new Set<number>(titleInfo.libraryIds)
      : targetLib
        ? new Set<number>([targetLib.id])
        : null;
    const rawKind =
      titleInfo?.kind ||
      q.kind ||
      (q.mediaType === "tv" ? "episode" : q.mediaType === "movie" ? "movie" : "");
    // A real play is a movie or an episode; with nothing to say which, the
    // server answers for a movie and reports kindAssumed.
    const named: "" | "movie" | "episode" =
      rawKind === "movie" || rawKind === "episode"
        ? rawKind
        : targetLib?.mediaType === "show"
          ? "episode"
          : targetLib?.mediaType === "movie"
            ? "movie"
            : "";
    const kind: "movie" | "episode" = named || "movie";
    const kindAssumed = !named;

    const where: "home" | "away" = q.remote ? "away" : "home";
    const rules = effectiveRules(config, getPolicy, q.userId);
    const chosenByPlugin = Boolean(titleInfo?.chosenByPlugin || targetLib?.source === "plugin");

    let profileName = "";
    let profileSource: QualityDecisionSource = chosenByPlugin ? "plugin" : "default";
    let profileRuleId: string | undefined;
    let profileRuleNum: number | undefined;
    let pickedRuleWithDevice = false;

    const limitRuleIds: string[] = [];
    const matchingLimitLayers: { rule: QualityRule; ruleNum: number }[] = [];
    let ruleMaxHeight = 0;
    let ruleMaxBitrate = 0;
    let stereo = false;

    for (let i = 0; i < rules.length; i++) {
      const r = rules[i]!;
      if (r.where && r.where !== where) continue;
      if (r.kind && kind && r.kind !== kind) continue;
      if (!matchesRuleViewer(r, q.userId ?? 0)) continue;
      if (!matchesRuleDevice(r, dev)) continue;
      if (r.libraries?.length) {
        if (!memberLibs) continue;
        const hit = r.libraries.some((id) => memberLibs.has(id));
        if (r.exceptLibraries ? hit : !hit) continue;
      }

      const inConfigIdx = (config.quality.rules ?? []).findIndex((cr) => cr.id === r.id);
      const ruleNum = inConfigIdx >= 0 ? inConfigIdx + 1 : 0;

      if (!chosenByPlugin && !profileName && r.profile) {
        const found = pick(r.profile);
        if (found) {
          profileName = found.name;
          profileSource = "rule";
          profileRuleId = r.id;
          profileRuleNum = ruleNum || i + 1;
          pickedRuleWithDevice = Boolean(r.devices?.length) && !r.exceptDevices;
        }
      }
      if (r.maxHeight || r.maxBitrateKbps || r.stereoAudio) {
        if (r.maxHeight && (!ruleMaxHeight || r.maxHeight < ruleMaxHeight)) {
          ruleMaxHeight = r.maxHeight;
        }
        if (r.maxBitrateKbps && (!ruleMaxBitrate || r.maxBitrateKbps < ruleMaxBitrate)) {
          ruleMaxBitrate = r.maxBitrateKbps;
        }
        if (r.stereoAudio) stereo = true;
        limitRuleIds.push(r.id);
        matchingLimitLayers.push({ rule: r, ruleNum });
      }
    }

    if (!chosenByPlugin && !profileName) {
      const away = where === "away" && Boolean(config.quality.defaultAway);
      profileName = away
        ? (config.quality.defaultAway ?? "")
        : config.quality.default || profiles[0]?.name || "4K";
      profileSource = away ? "defaultAway" : "default";
      // The fallback row's limits apply with the rules', attributed to the row.
      const l = (away ? config.quality.defaultAwayLimits : config.quality.defaultLimits) ?? {};
      if (l.maxHeight || l.maxBitrateKbps || l.stereoAudio) {
        if (l.maxHeight && (!ruleMaxHeight || l.maxHeight < ruleMaxHeight)) {
          ruleMaxHeight = l.maxHeight;
        }
        if (l.maxBitrateKbps && (!ruleMaxBitrate || l.maxBitrateKbps < ruleMaxBitrate)) {
          ruleMaxBitrate = l.maxBitrateKbps;
        }
        if (l.stereoAudio) stereo = true;
        limitRuleIds.push(profileSource);
        matchingLimitLayers.push({ rule: { id: profileSource, ...l }, ruleNum: 0 });
      }
    }

    const prof = pick(profileName);
    const configured = prof?.maxHeight ?? (chosenByPlugin ? 0 : 2160);
    const androidLifted = Boolean(
      dev.caps.androidMobile && pickedRuleWithDevice && configured > 1080,
    );
    const deviceMax = androidLifted ? configured : dev.caps.maxHeight;
    const ceilings = [configured, deviceMax, ruleMaxHeight].filter((n) => n > 0);
    const maxHeight = ceilings.length ? Math.min(...ceilings) : 0;

    const layers: QualityLayer[] = [];
    if (!chosenByPlugin) {
      layers.push({
        kind: "profile",
        reason: "",
        profile: prof?.name ?? profileName,
        source: profileSource,
        ...(profileRuleNum ? { rule: profileRuleNum } : {}),
        ...(profileRuleId ? { ruleId: profileRuleId } : {}),
        maxHeight: configured,
        hdr: Boolean(prof?.allowHdr ?? true),
        dolbyVision: Boolean(prof?.allowDolbyVision ?? true),
        decisive: Boolean(maxHeight && configured === maxHeight),
      });
    }

    layers.push({
      kind: "device",
      reason: "",
      maxHeight: dev.caps.maxHeight,
      hdr: dev.caps.hdr,
      dolbyVision: dev.caps.dolbyVision,
      decisive: Boolean(
        maxHeight &&
        dev.caps.maxHeight === maxHeight &&
        configured !== maxHeight &&
        ruleMaxHeight !== maxHeight &&
        !androidLifted,
      ),
    });

    if (androidLifted && profileRuleId) {
      layers.push({
        kind: "android-lift",
        reason: "",
        ...(profileRuleNum ? { rule: profileRuleNum } : {}),
        ruleId: profileRuleId,
        maxHeight: configured,
        decisive: false,
      });
    }

    for (const { rule, ruleNum } of matchingLimitLayers) {
      const decisive = Boolean(
        (rule.maxHeight && rule.maxHeight === maxHeight && configured !== maxHeight) ||
        (rule.maxBitrateKbps && rule.maxBitrateKbps === ruleMaxBitrate) ||
        rule.stereoAudio,
      );
      layers.push({
        kind: "rule-limit",
        reason: "",
        ...(ruleNum ? { rule: ruleNum } : {}),
        ruleId: rule.id,
        ...(rule.maxHeight ? { maxHeight: rule.maxHeight } : {}),
        ...(rule.maxBitrateKbps ? { bitrateKbps: rule.maxBitrateKbps } : {}),
        ...(rule.stereoAudio ? { stereo: true } : {}),
        decisive,
      });
    }

    const blocked = q.remote && Boolean(policy?.remoteDisabled);
    if (blocked) {
      layers.push({ kind: "remote-blocked", reason: "", decisive: true });
    }

    const answer: QualityAnswer = {
      profile: chosenByPlugin ? "" : (prof?.name ?? profileName),
      kind,
      ...(kindAssumed ? { kindAssumed } : {}),
      profileSource,
      ...(profileRuleId ? { profileRuleId } : {}),
      limitRuleIds,
      maxHeight,
      hdr: Boolean(prof?.allowHdr ?? true) && (dev.caps.hdr || androidLifted),
      dolbyVision: Boolean(prof?.allowDolbyVision ?? true) && dev.caps.dolbyVision,
      bitrateKbps: ruleMaxBitrate,
      ...(stereo ? { stereo: true } : {}),
      blocked,
      layers,
    };
    return ok(answer);
  }

  function handle(
    method: string,
    seg: string[],
    q: URLSearchParams,
    body: unknown,
    config: EtherConfig,
    libs: MockLibrarySummaryInput[],
    getPolicy: (id: number) => Record<string, unknown> | undefined,
    resolveTitle?: (key: string) => MockTitleQualityInput | null,
  ): Reply | null {
    if (seg[0] !== "quality") return null;
    const route = `${method} /${seg.join("/")}`;
    if (route === "GET /quality/devices") return ok(MOCK_QUALITY_DEVICES);
    if (route === "GET /quality/clients") {
      return ok({
        clients: CLIENTS.map(({ id, name, product, platform, device }) => ({
          id,
          name,
          product,
          platform,
          device,
        })),
      });
    }
    if (route === "GET /quality/summary") {
      return summarize(q, config, libs, getPolicy);
    }
    if (route === "POST /quality/explain") {
      const req = (body ?? {}) as QualityQuestion & { mediaType?: string };
      // No viewer is "anyone", as on the server: rules for particular viewers don't apply.
      const policy = req.userId ? getPolicy(req.userId) : undefined;
      return explain(req, config, libs, policy, getPolicy, resolveTitle);
    }
    return null;
  }

  return { handle };
}
