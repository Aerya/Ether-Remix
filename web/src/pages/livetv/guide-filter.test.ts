import { describe, expect, it } from "vitest";
import {
  channelSeenInApp,
  filterGuideChannels,
  groupGuideChannels,
  type GuideFilterChannel,
} from "./guide-filter";

const ch = (over: Partial<GuideFilterChannel> & { id: string }): GuideFilterChannel => ({
  enabled: true,
  ...over,
});

describe("guide channel filtering", () => {
  const channels = [
    ch({ id: "a", group: "News" }),
    ch({ id: "b", group: "News", hiddenApps: ["plex"] }),
    ch({ id: "c", group: "Mine", customGroup: true, hiddenApps: ["jellyfin", "emby"] }),
    ch({ id: "d", enabled: false }),
    ch({ id: "e" }),
  ];

  it("shows a channel in every app except the ones it hides", () => {
    expect(channelSeenInApp(channels[1]!, "plex")).toBe(false);
    expect(channelSeenInApp(channels[1]!, "jellyfin")).toBe(true);
    expect(channelSeenInApp(channels[0]!, "vela")).toBe(true);
    expect(channelSeenInApp(channels[0]!, "all")).toBe(true);
  });

  it("filters by app and drops off-air channels", () => {
    expect(filterGuideChannels(channels, { app: "plex", group: "" }).map((c) => c.id)).toEqual([
      "a",
      "c",
      "e",
    ]);
    expect(filterGuideChannels(channels, { app: "all", group: "" }).map((c) => c.id)).toEqual([
      "a",
      "b",
      "c",
      "e",
    ]);
  });

  it("filters by your groups, provider groups and one group name", () => {
    expect(filterGuideChannels(channels, { app: "all", group: "yours" }).map((c) => c.id)).toEqual([
      "c",
    ]);
    expect(
      filterGuideChannels(channels, { app: "all", group: "provider" }).map((c) => c.id),
    ).toEqual(["a", "b"]);
    expect(
      filterGuideChannels(channels, { app: "all", group: "name:News" }).map((c) => c.id),
    ).toEqual(["a", "b"]);
  });

  it("splits group names into yours and the provider's", () => {
    expect(groupGuideChannels(channels)).toEqual({ yours: ["Mine"], provider: ["News"] });
  });
});
