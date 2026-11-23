import type { LiveApp } from "./ChannelApps";

/** Apps the TV guide can preview: every Live TV app, plus Vela. */
export type GuideAppFilter = "all" | LiveApp | "vela";

/** The channel fields the guide's filters read. */
export type GuideFilterChannel = {
  id: string;
  enabled: boolean;
  group?: string;
  customGroup?: boolean;
  hiddenApps?: string[];
};

/** Whether one app's lineup includes this channel ("all" always does). */
export function channelSeenInApp(c: GuideFilterChannel, app: GuideAppFilter): boolean {
  return app === "all" || !(c.hiddenApps ?? []).includes(app);
}

/**
 * Group filter values: "" every group, "yours" the owner's own groups,
 * "provider" groups that came from a provider, "name:<group>" one group.
 */
export function channelInGroup(c: GuideFilterChannel, group: string): boolean {
  if (!group) return true;
  if (group === "yours") return Boolean(c.customGroup && c.group);
  if (group === "provider") return Boolean(c.group && !c.customGroup);
  if (group.startsWith("name:")) return c.group === group.slice(5);
  return true;
}

/** On-air channels one app and group selection gets, in lineup order. */
export function filterGuideChannels<C extends GuideFilterChannel>(
  channels: C[],
  filter: { app: GuideAppFilter; group: string },
): C[] {
  return channels.filter(
    (c) => c.enabled && channelSeenInApp(c, filter.app) && channelInGroup(c, filter.group),
  );
}

/** Distinct group names, split into the owner's own and providers'. */
export function groupGuideChannels(channels: GuideFilterChannel[]): {
  yours: string[];
  provider: string[];
} {
  const yours = new Set<string>();
  const provider = new Set<string>();
  for (const c of channels) {
    if (!c.group) continue;
    (c.customGroup ? yours : provider).add(c.group);
  }
  return { yours: [...yours].sort(), provider: [...provider].sort() };
}
