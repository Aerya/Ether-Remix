import { ConfirmRoot } from "@braedonsaunders/appkit-ui";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { invalidateRegistry } from "../lib/extend";
import livetvDescriptors from "../mock/extend/descriptors/livetv.json";
import LiveTV from "../pages/livetv/LiveTV";
import { IPTVProviders } from "../pages/sources/IPTV";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function LocationProbe() {
  const loc = useLocation();
  return <output data-testid="loc">{`${loc.pathname}${loc.search}`}</output>;
}

function stubLiveTV({
  channels = [],
  sources = [],
  coverage = [],
}: {
  channels?: unknown[];
  sources?: unknown[];
  coverage?: unknown[];
}) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url =
        typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      if (url.includes("/api/extend/registry") || url.includes("/api/extend/descriptors")) {
        return jsonResponse({ descriptors: livetvDescriptors, version: 1 });
      }
      if (url.endsWith("/api/config")) {
        return jsonResponse({ catalog: { region: "US", language: "en-US" } });
      }
      if (url.endsWith("/api/users")) return jsonResponse({ users: [] });
      if (url.endsWith("/api/collections")) return jsonResponse({ collections: [] });
      if (url.endsWith("/api/livetv/channels/summary")) return jsonResponse(channels);
      if (url.endsWith("/api/livetv/sources")) return jsonResponse(sources);
      if (url.endsWith("/api/livetv/guide/coverage")) return jsonResponse(coverage);
      return jsonResponse([]);
    }),
  );
}

function renderGuide(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <ConfirmRoot />
      <LocationProbe />
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

describe("Live TV › Guide tabs and Channel mapping layout", () => {
  it("offers only TV guide and Channel mapping, and lands old ?sub=sources URLs on TV guide", async () => {
    stubLiveTV({
      channels: [
        {
          id: "c1",
          number: "101",
          name: "News 24",
          kind: "iptv",
          enabled: true,
          favorite: false,
          sourceId: "src1",
          streamUrl: "https://iptv.example/news.m3u8",
        },
      ],
      sources: [
        {
          id: "epg1",
          name: "Regional XMLTV",
          guideUrl: "https://epg.example/guide.xml",
          hasPlaylist: false,
          hasGuide: true,
          guideChannelCount: 25,
        },
      ],
      coverage: [{ channelId: "c1", programmes: 0 }],
    });

    renderGuide("/livetv/guide?sub=sources");

    const guideNav = await screen.findByRole("radiogroup", { name: "Guide" });
    const radios = within(guideNav).getAllByRole("radio");
    expect(radios.map((r) => r.textContent)).toEqual(["TV guide", "Channel mapping · 1"]);
    expect(within(guideNav).queryByRole("radio", { name: /Guide sources/ })).toBeNull();

    // Lands on the TV guide grid in place without redirecting away.
    expect(screen.getByRole("grid", { name: "Programme guide" })).toBeInTheDocument();
    expect(screen.getByTestId("loc")).toHaveTextContent("/livetv/guide?sub=sources");
  });

  it("opens SourceDrawer in place from Channel mapping empty states and links management to /sources/iptv", async () => {
    stubLiveTV({
      channels: [
        {
          id: "v1",
          number: "1",
          name: "80s Classics",
          kind: "virtual",
          enabled: true,
          favorite: false,
        },
      ],
      sources: [],
      coverage: [],
    });

    renderGuide("/livetv/guide-data");

    const rail = await screen.findByRole("complementary", { name: "Guide sources" });
    const sourcesLink = within(rail).getByRole("link", { name: "Sources" });
    expect(sourcesLink).toHaveAttribute("href", "/sources/iptv");

    fireEvent.click(within(rail).getByRole("button", { name: "New guide source" }));
    const guideDialog = await screen.findByRole("dialog", { name: "Guide source" });
    expect(screen.getByTestId("loc")).toHaveTextContent("/livetv/guide-data?new=guide");
    fireEvent.click(within(guideDialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    const mappingCard = screen.getByRole("region", { name: "Channel guide mapping" });
    fireEvent.click(within(mappingCard).getByRole("button", { name: "Add IPTV" }));
    expect(await screen.findByRole("dialog", { name: "IPTV source" })).toBeInTheDocument();
    expect(screen.getByTestId("loc")).toHaveTextContent("/livetv/guide-data?new=playlist");
  });

  it("bounds Channel mapping columns with minmax(0, …), keeps controls inside the card, and adds title tooltips", async () => {
    const longChannelName =
      "Ultra Long Regional Sports Network High Definition Feed East Coast Primary Broadcast";
    const longGuideId = "ultra.long.xmltv.identifier.sports.east.primary.hd.broadcast.tv";

    stubLiveTV({
      channels: [
        {
          id: "c1",
          number: "402",
          name: longChannelName,
          group: "Sports",
          kind: "iptv",
          enabled: true,
          favorite: false,
          sourceId: "src1",
          guideSourceId: "epg1",
          guideId: longGuideId,
          streamUrl: "https://iptv.example/sports.m3u8",
        },
      ],
      sources: [
        { id: "src1", name: "Primary Provider Feed", hasPlaylist: true },
        {
          id: "epg1",
          name: "North America Extended XMLTV Guide",
          hasPlaylist: false,
          hasGuide: true,
          guideChannelCount: 120,
        },
      ],
      coverage: [{ channelId: "c1", programmes: 0 }],
    });

    const { container } = renderGuide("/livetv/guide-data");

    const mappingCard = await screen.findByRole("region", { name: "Channel guide mapping" });
    expect(container.querySelectorAll("select")).toHaveLength(0);
    expect(container.querySelector(".max-w-5xl")).toBeNull();

    // Controls live inside the Channel guide mapping card.
    expect(within(mappingCard).getByRole("button", { name: "Auto-match" })).toBeInTheDocument();
    expect(
      within(mappingCard).getByRole("radiogroup", { name: "Show channels" }),
    ).toBeInTheDocument();

    // Truncated channel title and mapped guide identifier expose full text via title attributes.
    expect(mappingCard.querySelector(`[title="402 ${longChannelName}"]`)).toBeInTheDocument();
    expect(
      mappingCard.querySelector(`[title="${longGuideId} · North America Extended XMLTV Guide"]`),
    ).toBeInTheDocument();
  });
});
