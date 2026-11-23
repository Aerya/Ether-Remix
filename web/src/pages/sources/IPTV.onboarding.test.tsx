import { ConfirmRoot } from "@braedonsaunders/appkit-ui";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { invalidateRegistry, type Descriptor } from "../../lib/extend";
import livetvDescriptors from "../../mock/extend/descriptors/livetv.json";
import LiveTV from "../livetv/LiveTV";
import { IPTVProviders } from "./IPTV";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const seedChannel = {
  id: "ch-1",
  number: "100",
  name: "News 24",
  kind: "iptv",
  enabled: true,
  favorite: false,
  sourceId: "src-1",
  streamUrl: "https://iptv.example/news.m3u8",
};

function mockBackend(sources: unknown[] = [], extraDescriptors: Descriptor[] = []) {
  const requests: { method: string; url: string; body?: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const method = (init?.method ?? "GET").toUpperCase();
      const body = typeof init?.body === "string" ? init.body : undefined;
      requests.push({ method, url, body });

      if (
        (url.includes("/api/extend/registry") || url.includes("/api/extend/descriptors")) &&
        method === "GET"
      ) {
        return jsonResponse({
          descriptors: [...livetvDescriptors, ...extraDescriptors],
          version: 1,
        });
      }
      if (url.endsWith("/api/config") && method === "GET") {
        return jsonResponse({ catalog: { region: "US", language: "en-US" } });
      }
      if (url.endsWith("/api/users") && method === "GET") {
        return jsonResponse({ users: [] });
      }
      if (url.endsWith("/api/collections") && method === "GET") {
        return jsonResponse({ collections: [] });
      }
      if (url.includes("/api/livetv/channels/page") && method === "GET") {
        return jsonResponse({
          channels: [seedChannel],
          total: 1,
          counts: { all: 1, live: 1, favorites: 0, virtual: 0, iptv: 1, off: 0, missing: 1 },
          groups: [{ key: "src-src-1", sourceId: "src-1", kind: "iptv", count: 1, start: 0 }],
          coverage: { "ch-1": false },
          nextNumber: "101",
        });
      }
      if (url.endsWith("/api/livetv/channels/summary") && method === "GET") {
        return jsonResponse([seedChannel]);
      }
      if (url.endsWith("/api/livetv/sources") && method === "GET") {
        return jsonResponse(sources);
      }
      if (/\/api\/livetv\/sources\/[^/]+\/refresh$/.test(url) && method === "POST") {
        return jsonResponse((sources[0] as Record<string, unknown>) ?? { id: "refreshed" });
      }
      if (url.includes("/api/livetv/sources") && (method === "POST" || method === "PUT")) {
        const parsed = body ? (JSON.parse(body) as Record<string, unknown>) : {};
        return jsonResponse({
          id: (parsed.id as string) || "created-1",
          name: parsed.name ?? "Created",
          auth: parsed.auth,
          hasPlaylist: true,
          hasGuide: true,
        });
      }
      return jsonResponse([]);
    }),
  );
  return requests;
}

function PathProbe() {
  const loc = useLocation();
  return <output data-testid="location-path">{loc.pathname}</output>;
}

function renderIPTV(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ConfirmRoot />
      <PathProbe />
      <Routes>
        <Route path="/livetv/:tab?" element={<LiveTV />} />
        <Route path="/sources/iptv" element={<IPTVProviders />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  invalidateRegistry();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const pluginPresets: Descriptor[] = [
  {
    kind: "liveTVSource",
    id: "pluto",
    owner: "tv.ether.examples.pluto",
    label: { resolved: "Pluto TV" },
    meta: { yieldsChannels: true },
  },
  {
    kind: "liveTVSource",
    id: "usatv",
    owner: "addon.1edeafcd981234",
    label: { resolved: "USA TV Next" },
    meta: { yieldsChannels: true },
  },
];

describe("IPTV source onboarding", () => {
  it("shows 'Guide found in the playlist' until an owner guide overrides it", async () => {
    mockBackend([
      {
        id: "m3u-auto",
        name: "Auto Guide M3U",
        playlistUrl: "https://iptv.example/playlist.m3u",
        playlistGuide: true,
        hasPlaylist: true,
        hasGuide: true,
        channelCount: 12,
        guideChannelCount: 12,
        programmeCount: 240,
        updatedAt: "2026-11-16T00:00:00Z",
      },
    ]);

    renderIPTV("/sources/iptv?source=m3u-auto");

    expect(await screen.findByDisplayValue("Auto Guide M3U")).toBeInTheDocument();
    expect(screen.getByText("Guide found in the playlist")).toBeInTheDocument();

    const guideInput = screen.getByLabelText("XMLTV guide (optional)");
    fireEvent.change(guideInput, { target: { value: "https://owner.example/custom.xml" } });
    await waitFor(() => {
      expect(screen.queryByText("Guide found in the playlist")).not.toBeInTheDocument();
    });

    fireEvent.change(guideInput, { target: { value: "" } });
    expect(await screen.findByText("Guide found in the playlist")).toBeInTheDocument();
  });

  it("splits pasted Xtream URLs across usual shapes into server, username and password without logging the password", async () => {
    const logged: string[] = [];
    for (const method of ["log", "info", "warn", "error", "debug"] as const) {
      vi.spyOn(console, method).mockImplementation((...args: unknown[]) => {
        logged.push(args.map(String).join(" "));
      });
    }

    const requests = mockBackend([]);
    renderIPTV("/sources/iptv?new=xtream");

    const nameInput = await screen.findByLabelText("Source name");
    const serverInput = screen.getByLabelText("Server") as HTMLInputElement;
    const userInput = screen.getByLabelText("Username") as HTMLInputElement;
    const passInput = screen.getByLabelText("Password") as HTMLInputElement;

    // 1. get.php?username=U&password=P&type=m3u_plus via clipboard paste
    fireEvent.paste(serverInput, {
      clipboardData: {
        getData: () =>
          "http://panel.example:8080/get.php?username=alice&password=P%40ssSecret1&type=m3u_plus&output=m3u8",
      },
    });
    expect(serverInput.value).toBe("http://panel.example:8080");
    expect(userInput.value).toBe("alice");
    expect(passInput.value).toBe("P@ssSecret1");

    // 2. player_api.php?username=U&password=P (with reverse-proxy base path)
    fireEvent.paste(serverInput, {
      clipboardData: {
        getData: () => "https://iptv.example/sub/player_api.php?username=bob&password=S3cretTwo",
      },
    });
    expect(serverInput.value).toBe("https://iptv.example/sub");
    expect(userInput.value).toBe("bob");
    expect(passInput.value).toBe("S3cretTwo");

    // 3. Short path /U/P/stream
    fireEvent.change(serverInput, {
      target: { value: "http://short.example:25461/carol/N0LogSecret%21/9988.ts" },
    });
    expect(serverInput.value).toBe("http://short.example:25461");
    expect(userInput.value).toBe("carol");
    expect(passInput.value).toBe("N0LogSecret!");

    fireEvent.change(nameInput, { target: { value: "My Xtream" } });
    fireEvent.click(screen.getByRole("button", { name: "Import source" }));

    await waitFor(() => {
      expect(
        requests.some((r) => r.method === "POST" && r.url.includes("/api/livetv/sources")),
      ).toBe(true);
    });
    const post = requests.find((r) => r.method === "POST" && r.url.includes("/api/livetv/sources"));
    const payload = JSON.parse(post?.body ?? "{}") as {
      auth?: { type?: string; server?: string; username?: string; password?: string };
    };
    expect(payload.auth).toMatchObject({
      type: "xtream",
      server: "http://short.example:25461",
      username: "carol",
      password: "N0LogSecret!",
    });

    for (const secret of ["P@ssSecret1", "S3cretTwo", "N0LogSecret!"]) {
      expect(logged.join("\n")).not.toContain(secret);
    }
  });

  it("renders plain plugin presets, styled inactivity select, sticky footer, and hides mismatched controls per mode", async () => {
    mockBackend([], pluginPresets);
    renderIPTV("/sources/iptv?new=1");

    const dialog = await screen.findByRole("dialog");

    // Plain plugin preset names under 'From plugins' without raw package IDs or hashes.
    expect(await within(dialog).findByText("From plugins")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Pluto TV" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "USA TV Next" })).toBeInTheDocument();
    expect(dialog.textContent).not.toContain("tv.ether.examples.pluto");
    expect(dialog.textContent).not.toContain("addon.1edeafcd981234");

    // Stream-only controls present in M3U playlist mode.
    expect(within(dialog).getByText("Player identity")).toBeInTheDocument();
    expect(within(dialog).getByText("Rating for channels without one")).toBeInTheDocument();

    // Enter a playlist URL so automatic refresh options render, and verify SearchSelect (zero native <select>).
    fireEvent.change(within(dialog).getByLabelText("M3U playlist"), {
      target: { value: "https://iptv.example/playlist.m3u" },
    });
    expect(dialog.querySelectorAll("select")).toHaveLength(0);
    const idleSelect = within(dialog).getByRole("button", { name: "Refresh after inactivity" });
    expect(idleSelect).toHaveTextContent("Off");
    fireEvent.click(idleSelect);
    fireEvent.click(await screen.findByRole("option", { name: "60 minutes" }));
    expect(idleSelect).toHaveTextContent("60 minutes");

    // Sticky drawer footer provides Cancel + Import source.
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Import source" })).toBeInTheDocument();

    // Switching to Guide only (XMLTV) hides stream/channel-only controls and plugin presets.
    fireEvent.click(within(dialog).getByRole("radio", { name: "Guide only (XMLTV)" }));
    expect(within(dialog).queryByText("From plugins")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Pluto TV" })).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Player identity")).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Rating for channels without one")).not.toBeInTheDocument();

    // Switching back to M3U playlist and then Xtream Codes login hides the M3U/Guide toggle and plugin presets.
    fireEvent.click(within(dialog).getByRole("radio", { name: "M3U playlist" }));
    fireEvent.click(within(dialog).getByRole("radio", { name: "Xtream Codes login" }));
    expect(within(dialog).queryByRole("radio", { name: "Guide only (XMLTV)" })).toBeNull();
    expect(within(dialog).queryByText("From plugins")).not.toBeInTheDocument();
    expect(within(dialog).getByText("Player identity")).toBeInTheDocument();
    expect(within(dialog).getByText("Rating for channels without one")).toBeInTheDocument();
  });

  it("surfaces per-source Refresh, last-refresh status, guide horizon and specific refresh errors on Streaming › Sources cards", async () => {
    const requests = mockBackend([
      {
        id: "guide-1",
        name: "Regional XMLTV",
        guideUrl: "https://epg.example/regional.xml.gz",
        hasPlaylist: false,
        hasGuide: true,
        guideChannelCount: 48,
        guideUntil: "2020-01-01T00:00:00Z",
        updatedAt: "2026-11-16T00:00:00Z",
        lastError: "404 Not Found",
      },
      {
        id: "vod-1",
        name: "Xtream Provider",
        auth: { type: "xtream", server: "http://panel.example", username: "u" },
        hasPlaylist: true,
        channelCount: 200,
        useVOD: true,
        vodStatus: { movies: 10, series: 4, error: "timeout" },
      },
    ]);

    renderIPTV("/sources/iptv");

    const guideHeading = await screen.findByRole("heading", { name: "Regional XMLTV" });
    const guideCard = guideHeading.closest("article")!;
    expect(within(guideCard).getByText("Guide source")).toBeInTheDocument();
    expect(within(guideCard).getByText("48 guide channels")).toBeInTheDocument();
    expect(within(guideCard).getByText("Listings have run out")).toBeInTheDocument();
    expect(within(guideCard).getByText(/refreshed /)).toBeInTheDocument();
    expect(within(guideCard).getByRole("alert")).toHaveTextContent(/404/);

    fireEvent.click(within(guideCard).getByRole("button", { name: "Refresh" }));
    await waitFor(() => {
      expect(
        requests.some(
          (r) => r.method === "POST" && r.url.endsWith("/api/livetv/sources/guide-1/refresh"),
        ),
      ).toBe(true);
    });

    const vodHeading = screen.getByRole("heading", { name: "Xtream Provider" });
    const vodCard = vodHeading.closest("article")!;
    expect(within(vodCard).getByRole("alert")).toHaveTextContent(
      "Provider unavailable; retry scheduled",
    );
  });

  it("anchors Guide source and IPTV source drawers over Live TV instead of navigating to /sources/iptv", async () => {
    mockBackend([], pluginPresets);
    renderIPTV("/livetv");

    const lineup = await screen.findByRole("region", { name: "Channels" });
    expect(await within(lineup).findByRole("button", { name: "100 News 24" })).toBeInTheDocument();
    expect(screen.getByTestId("location-path")).toHaveTextContent("/livetv");

    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Guide source/ }));

    const dialog = await screen.findByRole("dialog", { name: "Guide source" });
    expect(screen.getByTestId("location-path")).toHaveTextContent("/livetv");
    expect(screen.getByRole("region", { name: "Channels" })).toBeInTheDocument();
    expect(within(dialog).queryByText("Player identity")).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Rating for channels without one")).not.toBeInTheDocument();
    expect(within(dialog).queryByText("From plugins")).not.toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    expect(screen.getByTestId("location-path")).toHaveTextContent("/livetv");
  });
});
