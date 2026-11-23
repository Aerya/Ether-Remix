import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { InstalledPluginDrawer } from "../pages/plugins/InstalledPluginDrawer";
import {
  buildPlacements,
  type PluginDetail,
  type PluginLiveEventsOverview,
} from "../pages/plugins/types";

function makeLiveEventsDetail(overrides: Partial<PluginDetail> = {}): PluginDetail {
  return {
    id: "org.example.events",
    name: "Live Events",
    version: "1.2.0",
    kind: "extension",
    summary: "Live event feeds and screens.",
    enabled: true,
    verified: true,
    shadow: false,
    autoUpdate: true,
    health: "ok",
    capabilities: ["live_events"],
    roles: [{ role: "live_events", on: true }],
    installedAt: 1_762_000_000,
    manifest: {
      id: "org.example.events",
      version: "1.2.0",
      name: "Live Events",
      description: "Live event feeds and screens.",
      ether: {
        protocol: 1,
        kind: "extension",
        capabilities: ["live_events"],
        runtime: { kind: "hosted", entry: "plugin.js" },
        contributes: {
          liveFeeds: [
            {
              id: "main",
              title: "Main feed",
              minIntervalSec: 15,
              entityKinds: ["group", "performer"],
            },
          ],
          screens: [
            {
              id: "hub",
              title: "Live Hub",
              icon: "radio",
              frontends: ["vela"],
            },
          ],
        },
      },
    },
    permissions: {
      lines: ["Adds a screen: Live Hub", "Provides live event feeds"],
      declared: {},
      grants: {},
      needsViewers: false,
      needsFolders: false,
    },
    healthDetail: {
      status: "ok",
      restarts: 0,
      slow: 0,
      drops: 0,
      egressBytes: 0,
    },
    privacy: {
      declaredHosts: ["feeds.example.org"],
      observedHosts: { "feeds.example.org": 10 },
    },
    impact: {
      calls: 10,
      errors: 0,
      p50Ms: 90,
      p95Ms: 180,
      egressBytes: 12_000,
      kvBytes: 2048,
      slow: 0,
      eventDrops: 0,
      restarts: 0,
    },
    ...overrides,
  };
}

function makeOverviewResponse(
  overrides: Partial<PluginLiveEventsOverview> = {},
): PluginLiveEventsOverview {
  const now = Date.now();
  return {
    pluginId: "org.example.events",
    feeds: [
      {
        id: "main",
        title: "Main feed",
        minIntervalSec: 15,
        entityKinds: ["group", "performer"],
        active: true,
        health: {
          lastPollAt: new Date(now - 10_000).toISOString(),
          lastOkAt: new Date(now - 10_000).toISOString(),
          consecutiveErrors: 0,
          nextPollSec: 15,
          subscribers: 1,
          polling: true,
        },
        eventCount: 9,
        liveCount: 2,
        upcomingCount: 1,
        leagues: [
          { id: "alpha", name: "Alpha Circuit", abbr: "ALP", eventCount: 5 },
          { id: "beta", name: "Beta Series", abbr: "BET", eventCount: 4 },
        ],
        entityCounts: { group: 32, performer: 12 },
        entityTotal: 44,
      },
    ],
    screens: [
      {
        id: "hub",
        title: "Live Hub",
        icon: "radio",
        frontends: [{ id: "vela", name: "Vela", published: true, reason: "published" }],
      },
    ],
    events: [
      {
        id: "ev-live-1",
        feed: "main",
        title: "North vs South",
        league: { id: "alpha", name: "Alpha Circuit", abbr: "ALP" },
        status: { state: "in", detail: "Period 3 · 04:12", close: true },
        start: new Date(now - 3600_000).toISOString(),
        competitors: [
          { id: "g1", name: "North", score: "19", home: false },
          { id: "g2", name: "South", score: "17", home: true },
        ],
        watchCount: 2,
      },
      {
        id: "ev-live-2",
        feed: "main",
        title: "Night Finale",
        league: { id: "beta", name: "Beta Series", abbr: "BET" },
        status: { state: "in", detail: "Stage 4 / 6" },
        start: new Date(now - 1800_000).toISOString(),
        competitors: [],
        ranking: [{ pos: 1, name: "A. Rivera", detail: "01:12.4" }],
        watchCount: 1,
      },
      {
        id: "ev-up-1",
        feed: "main",
        title: "East vs West",
        league: { id: "alpha", name: "Alpha Circuit", abbr: "ALP" },
        status: { state: "pre", detail: "Tonight · 8:00 PM" },
        start: new Date(now + 7200_000).toISOString(),
        competitors: [
          { id: "g3", name: "East", home: false },
          { id: "g4", name: "West", home: true },
        ],
        watchCount: 0,
      },
    ],
    totalEvents: 9,
    liveCount: 2,
    upcomingCount: 1,
    ...overrides,
  };
}

function stubDrawerApi(detail: PluginDetail, overview: PluginLiveEventsOverview) {
  let overviewCalls = 0;
  const spy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? "GET").toUpperCase();
    const jsonResp = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      });

    if (url.includes("/ether/api/extend/registry")) {
      return jsonResp({ version: 1, descriptors: [] });
    }
    if (url.includes("/ether/api/collections")) {
      return jsonResp({ collections: [] });
    }
    if (url.includes("/ether/api/plugins/org.example.events/live-events")) {
      if (!url.includes("stream=1")) overviewCalls += 1;
      return jsonResp(overview);
    }
    if (url.includes("/ether/api/plugins/org.example.events/calls")) {
      return jsonResp({ calls: [] });
    }
    if (url.includes("/ether/api/plugins/org.example.events/journal")) {
      return jsonResp({ entries: [] });
    }
    if (url.includes("/ether/api/plugins/org.example.events/jobs")) {
      return jsonResp([]);
    }
    if (url.includes("/ether/api/plugins/org.example.events/actions")) {
      return jsonResp({ actions: [], jobs: [] });
    }
    if (method === "POST" && url.includes("/ether/api/plugins/org.example.events/test")) {
      return jsonResp({ ok: true, message: "Checked" });
    }
    if (url.includes("/ether/api/plugins/org.example.events")) {
      return jsonResp(detail);
    }
    return jsonResp({});
  });
  return { spy, getOverviewCalls: () => overviewCalls };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("Plugin Overview for live events and screens", () => {
  it("renders Live now events, Vela screen publish state, and feed leagues/entities", async () => {
    const detail = makeLiveEventsDetail();
    const overview = makeOverviewResponse();
    stubDrawerApi(detail, overview);

    render(
      <MemoryRouter>
        <InstalledPluginDrawer
          pluginId={detail.id}
          onClose={() => {}}
          onChanged={() => {}}
          onRemoved={() => {}}
        />
      </MemoryRouter>,
    );

    expect(await screen.findByText("2 live")).toBeInTheDocument();
    expect(screen.getByText("Live now")).toBeInTheDocument();
    expect(screen.getByText("1 upcoming")).toBeInTheDocument();

    // Head-to-head live event with score, status, league, watch target count.
    expect(screen.getByText("North")).toBeInTheDocument();
    expect(screen.getByText("South")).toBeInTheDocument();
    expect(screen.getByText("19")).toBeInTheDocument();
    expect(screen.getByText("17")).toBeInTheDocument();
    expect(screen.getByText("Period 3 · 04:12")).toBeInTheDocument();
    expect(screen.getByText("2 watch targets")).toBeInTheDocument();

    // Ranked event + upcoming event.
    expect(screen.getByText("Night Finale")).toBeInTheDocument();
    expect(screen.getByText(/1\. A\. Rivera/)).toBeInTheDocument();
    expect(screen.getByText("1 watch target")).toBeInTheDocument();
    expect(screen.getByText("East vs West")).toBeInTheDocument();
    expect(screen.getByText("Tonight · 8:00 PM")).toBeInTheDocument();
    expect(screen.getByText("0 watch targets")).toBeInTheDocument();

    // Placements: screen publish state + feed leagues and entity counts.
    expect(screen.getByText("Placements")).toBeInTheDocument();
    expect(screen.getByText("Vela › Live Hub")).toBeInTheDocument();
    expect(screen.getByText("Published")).toBeInTheDocument();
    expect(screen.getByText("Shown in Vela navigation.")).toBeInTheDocument();
    const appsLink = screen.getByRole("link", { name: /Manage in Apps/i });
    expect(appsLink).toHaveAttribute("href", "/apps/vela");

    expect(screen.getByText("Main feed")).toBeInTheDocument();
    expect(screen.getByText("Live feed")).toBeInTheDocument();
    expect(screen.getByText(/2 leagues · 44 entities/)).toBeInTheDocument();
    expect(screen.getByText("ALP · 5")).toBeInTheDocument();
    expect(screen.getByText("BET · 4")).toBeInTheDocument();
    expect(screen.getByText("32 group · 12 performer")).toBeInTheDocument();

    // Contribution tab for live_events is present.
    expect(screen.getByRole("tab", { name: "Live events" })).toBeInTheDocument();
  });

  it("shows feed health through the existing ProblemCard when polling fails and updates on retry", async () => {
    const detail = makeLiveEventsDetail();
    const now = Date.now();
    const overview = makeOverviewResponse({
      feeds: [
        {
          id: "main",
          title: "Main feed",
          minIntervalSec: 15,
          entityKinds: ["group"],
          active: true,
          health: {
            lastPollAt: new Date(now - 60_000).toISOString(),
            lastOkAt: new Date(now - 600_000).toISOString(),
            lastError: "liveevents: upstream HTTP 503",
            consecutiveErrors: 3,
            nextPollSec: 120,
            subscribers: 1,
            polling: true,
          },
          eventCount: 2,
          liveCount: 0,
          upcomingCount: 0,
          leagues: [],
          entityTotal: 0,
        },
      ],
      events: [],
      liveCount: 0,
      upcomingCount: 0,
      screens: [
        {
          id: "hub",
          title: "Live Hub",
          frontends: [{ id: "vela", name: "Vela", published: false, reason: "frontend_disabled" }],
        },
      ],
    });
    const api = stubDrawerApi(detail, overview);

    render(
      <MemoryRouter>
        <InstalledPluginDrawer
          pluginId={detail.id}
          onClose={() => {}}
          onChanged={() => {}}
          onRemoved={() => {}}
        />
      </MemoryRouter>,
    );

    const titleEl = await screen.findByText("Live event feed “Main feed” is failing");
    const statusCard = titleEl.closest('[role="status"]') as HTMLElement;
    expect(statusCard).not.toBeNull();
    expect(within(statusCard).getByText("upstream HTTP 503")).toBeInTheDocument();
    expect(within(statusCard).getByText(/Last poll /)).toBeInTheDocument();
    expect(within(statusCard).getByText(/3 errors/)).toBeInTheDocument();
    expect(within(statusCard).getByText(/Next poll in 2m/)).toBeInTheDocument();

    // Unpublished screen when Vela is turned off.
    expect(screen.getByText("Vela off")).toBeInTheDocument();
    expect(screen.getByText("Turn on Vela in Apps to publish this screen.")).toBeInTheDocument();

    const beforeRetry = api.getOverviewCalls();
    fireEvent.click(within(statusCard).getByRole("button", { name: /Try again/i }));
    await waitFor(() => expect(api.getOverviewCalls()).toBeGreaterThan(beforeRetry));
  });

  it("includes manifest screens and live feeds in buildPlacements", () => {
    const detail = makeLiveEventsDetail();
    const links = buildPlacements([], detail.id, detail.manifest);
    expect(links).toEqual([
      {
        id: "screen:vela:hub",
        kind: "screen",
        area: "Vela › Live Hub",
        label: "Vela › Live Hub",
        href: "/apps/vela",
      },
      {
        id: "liveFeed:main",
        kind: "liveFeed",
        area: "Live events",
        label: "Main feed",
        href: "/apps/vela",
      },
    ]);
  });
});
