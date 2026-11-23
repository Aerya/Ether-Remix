import { ConfirmRoot } from "@braedonsaunders/appkit-ui";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { invalidateRegistry } from "../lib/extend";
import LiveTV from "../pages/livetv/LiveTV";
import { IPTVProviders } from "../pages/sources/IPTV";
import { ProgrammingEditor } from "../pages/livetv/ProgrammingEditor";
import {
  compileOwnerGroupMatcher,
  compileOwnerGroupRegExp,
  streamQualityTier,
  type Channel,
  type OwnerGroup,
  type OwnerGroupRule,
} from "../pages/livetv/model";
import { ScheduleTimeline } from "../pages/livetv/ScheduleTimeline";
import { calls as fakeCalls } from "./harness";
import { renderAppWith } from "./routes";

const hlsLoads = vi.hoisted(() => [] as string[]);
vi.mock("hls.js", () => {
  class FakeHls {
    static Events = { ERROR: "hlsError", LEVEL_SWITCHED: "hlsLevelSwitched" };
    static isSupported = () => true;
    on() {}
    attachMedia() {}
    loadSource(u: string) {
      hlsLoads.push(u);
    }
    destroy() {}
  }
  return { default: FakeHls };
});

const WAIT = { timeout: 4000 };

function renderLiveTV(path = "/livetv") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ConfirmRoot />
      <Routes>
        <Route path="/livetv/:tab?" element={<LiveTV />} />
        <Route path="/sources/iptv" element={<IPTVProviders />} />
      </Routes>
    </MemoryRouter>,
  );
}

type Call = { url: string; init?: RequestInit };

// The console asks for pages; this mirrors the server's page shape so the
// tests can keep declaring a flat channel array.
function channelPage(channels: any[], url: string) {
  const u = new URL(url, "http://local");
  const offset = Number(u.searchParams.get("offset") ?? 0);
  const limit = Number(u.searchParams.get("limit") ?? 200);
  const search = (u.searchParams.get("q") ?? "").trim().toLowerCase();
  const filter = u.searchParams.get("filter") ?? "all";
  const numeric = (c: any) => parseFloat(c.number) || 0;
  const sorted = [...channels].sort(
    (a, b) =>
      Number(a.kind === "virtual" ? 0 : 1) - Number(b.kind === "virtual" ? 0 : 1) ||
      String(a.sourceId ?? "").localeCompare(String(b.sourceId ?? "")) ||
      numeric(a) - numeric(b) ||
      String(a.number).localeCompare(String(b.number)),
  );
  const matches = sorted.filter((c) => {
    if (filter === "live" && !c.enabled) return false;
    if (filter === "favorites" && !c.favorite) return false;
    if (filter === "virtual" && c.kind !== "virtual") return false;
    if (filter === "iptv" && c.kind !== "iptv") return false;
    if (filter === "off" && c.enabled) return false;
    if (filter === "missing" && (!c.enabled || c.kind !== "iptv" || c.guideId)) return false;
    return !search || `${c.number} ${c.name} ${c.group ?? ""}`.toLowerCase().includes(search);
  });
  const groups: any[] = [];
  matches.forEach((c, i) => {
    const key = c.kind === "virtual" ? "virtual" : `src-${c.sourceId ?? ""}`;
    if (!groups.length || groups[groups.length - 1].key !== key)
      groups.push({
        key,
        sourceId: c.sourceId,
        kind: c.kind === "virtual" ? "virtual" : "iptv",
        count: 0,
        start: i,
      });
    groups[groups.length - 1].count++;
  });
  const counts = {
    all: channels.length,
    live: channels.filter((c) => c.enabled).length,
    favorites: channels.filter((c) => c.favorite).length,
    virtual: channels.filter((c) => c.kind === "virtual").length,
    iptv: channels.filter((c) => c.kind === "iptv").length,
    off: channels.filter((c) => !c.enabled).length,
    missing: channels.filter((c) => c.enabled && c.kind === "iptv" && !c.guideId).length,
  };
  const page = matches.slice(offset, offset + limit);
  return {
    channels: page,
    total: matches.length,
    counts,
    groups,
    coverage: Object.fromEntries(
      page.map((c) => [c.id, c.kind === "virtual" || Boolean(c.guideId)]),
    ),
    nextNumber: String(Math.max(0, ...channels.map((c) => Math.floor(numeric(c)))) + 1),
  };
}

function backend(
  channels: unknown[] = [],
  extra: (url: string, init?: RequestInit) => unknown = () => undefined,
) {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, init });
      const method = init?.method ?? "GET";
      let data: unknown = await extra(url, init);
      if (data instanceof Response) return data;
      if (data !== undefined) return new Response(JSON.stringify(data), { status: 200 });
      data = [];
      if (url.endsWith("/config")) data = { config: {} };
      else if (/\/livetv\/channels\/[^/]+\/timeline\?/.test(url))
        data = {
          channelId: url.split("/channels/")[1]?.split("/")[0],
          timezone: "UTC",
          now: Date.now(),
          elements: [],
          dayparts: [],
        };
      else if (url.includes("/livetv/channels/page")) data = channelPage(channels, url);
      else if (url.endsWith("/livetv/channels/summary")) data = channels;
      else if (/\/livetv\/channels\/[^/?]+$/.test(url) && method === "GET")
        data = channels.find((c: any) => url.endsWith(`/livetv/channels/${c.id}`)) ?? {};
      else if (url.endsWith("/livetv/channels") && method === "GET") data = channels;
      else if (/\/livetv\/channels(\/|$)/.test(url)) data = JSON.parse(String(init?.body ?? "{}"));
      else if (url.endsWith("/users"))
        data = { users: [{ id: 12, title: "Sam", username: "sam" }] };
      else if (url.endsWith("/collections"))
        data = {
          collections: [
            { id: "42", ratingKey: "6000000000000042", name: "Family favourites", itemCount: 8 },
          ],
        };
      else if (url.includes("/catalog/search"))
        data = {
          items: [
            {
              key: "1000000000000007",
              type: "movie",
              title: "Adventure",
              runtimeMinutes: 108,
              contentRating: "PG",
            },
          ],
        };
      return new Response(JSON.stringify(data), { status: 200 });
    }),
  );
  return calls;
}
const sent = (calls: Call[], method: string, re: RegExp) =>
  calls.filter((c) => re.test(c.url) && (c.init?.method ?? "GET") === method);
const body = (c: Call | undefined) => JSON.parse(String(c?.init?.body ?? "null"));

async function newBlankChannel(name: string) {
  fireEvent.click(await screen.findByRole("button", { name: "Add" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: /Virtual channel/ }));
  fireEvent.click(await screen.findByRole("button", { name: "Blank channel template" }));
  fireEvent.change(await screen.findByLabelText("Name"), { target: { value: name } });
}

const iptv = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  number: "100",
  name: "News 24",
  kind: "iptv",
  enabled: true,
  favorite: false,
  sourceId: "src1",
  guideId: "",
  streamUrl: "https://iptv.example/news.m3u8",
  epoch: "1970-01-01T00:00:00Z",
  seed: 1,
  shuffle: false,
  items: [],
  ...extra,
});

describe("Live TV studio", () => {
  it("finds channels in the lineup and narrows to favourites", async () => {
    backend(
      Array.from({ length: 25 }, (_, i) => ({
        id: String(i),
        number: String(i + 1),
        name: `Station ${i + 1}`,
        kind: "iptv",
        enabled: true,
        favorite: i === 24,
        group: i === 24 ? "Weather" : "General",
      })),
    );
    renderLiveTV();
    const lineup = await screen.findByRole("region", { name: "Channels" });
    await within(lineup).findByRole("button", { name: "1 Station 1" }, WAIT);
    fireEvent.change(screen.getByRole("searchbox", { name: "Search channels" }), {
      target: { value: "Weather" },
    });
    await waitFor(
      () => expect(within(lineup).queryByRole("button", { name: "1 Station 1" })).toBeNull(),
      WAIT,
    );
    expect(within(lineup).getByRole("button", { name: "25 Station 25" })).toBeInTheDocument();
    fireEvent.change(screen.getByRole("searchbox", { name: "Search channels" }), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Show" }));
    fireEvent.click(screen.getByRole("menuitemradio", { name: /Favourites/ }));
    await waitFor(
      () => expect(within(lineup).queryByRole("button", { name: "1 Station 1" })).toBeNull(),
      WAIT,
    );
    expect(within(lineup).getByRole("button", { name: "25 Station 25" })).toBeInTheDocument();
  });

  it("builds a virtual channel from a catalogue search and named viewer", async () => {
    const calls = backend([iptv("seed")], (url, init) =>
      url.endsWith("/livetv/channels") && init?.method === "POST"
        ? { ...JSON.parse(String(init.body)), id: "v1" }
        : undefined,
    );
    renderLiveTV();
    await newBlankChannel("Movie night");
    fireEvent.click(screen.getAllByRole("button", { name: "Add" }).at(-1)!);
    fireEvent.click(await screen.findByRole("button", { name: "Programme block" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Block name" }), {
      target: { value: "Movie night" },
    });

    fireEvent.change(screen.getByLabelText("Search titles"), { target: { value: "Adventure" } });
    fireEvent.click(await screen.findByRole("button", { name: /Adventure/ }, WAIT));
    expect(await screen.findByRole("button", { name: "Remove Adventure" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    fireEvent.click(screen.getByRole("tab", { name: "Viewers" }));
    // Sam has no Live TV limits, so a new channel reaches them until switched off.
    const sam = await screen.findByRole("switch", { name: "Sam" });
    expect(sam).toBeChecked();
    fireEvent.click(sam);
    fireEvent.click(screen.getByRole("button", { name: /Create channel/ }));
    await waitFor(() =>
      expect(sent(calls, "PUT", /\/livetv\/channels\/v1\/viewers$/)).toHaveLength(1),
    );
    const created = body(sent(calls, "POST", /\/livetv\/channels$/)[0]);
    expect(created).toMatchObject({
      name: "Movie night",
      number: "101",
      blocks: [
        {
          sources: [
            {
              items: [
                { key: 1000000000000007, title: "Adventure", durationMs: 6480000, rating: "PG" },
              ],
            },
          ],
        },
      ],
    });
    expect(created).not.toHaveProperty("allowedUsers");
    expect(body(sent(calls, "PUT", /\/livetv\/channels\/v1\/viewers$/)[0])).toEqual({
      allow: [],
      deny: [12],
    });
  });

  it("shows and edits each viewer's allowed channels from the channel's Viewers tab", async () => {
    const calls = backend([iptv("c1")], (url) => {
      if (url.endsWith("/users"))
        return {
          users: [
            { id: 1, title: "Owner", username: "owner", isOwner: true },
            { id: 12, title: "Sam", username: "sam" },
            { id: 13, title: "Kim", username: "kim" },
            { id: 14, title: "Lee", username: "lee" },
          ],
        };
      if (url.endsWith("/livetv/policies"))
        return [
          { userId: 13, disabled: false, restricted: true, channels: ["c9"] },
          { userId: 14, disabled: true, restricted: false },
        ];
      return undefined;
    });
    renderLiveTV("/livetv?channel=c1");
    fireEvent.click(await screen.findByRole("tab", { name: "Viewers" }, WAIT));
    expect(await screen.findByRole("switch", { name: "Sam" }, WAIT)).toBeChecked();
    expect(screen.getByRole("switch", { name: "Kim" })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: "Lee" })).toBeDisabled();
    expect(screen.getByText("Live TV is off")).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Owner" })).toBeNull();
    expect(screen.getByText("Only 1 viewer")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Allow everyone" }));
    expect(screen.getByRole("switch", { name: "Kim" })).toBeChecked();
    expect(screen.getByText("Everyone with Live TV access")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));
    await waitFor(() =>
      expect(sent(calls, "PUT", /\/livetv\/channels\/c1\/viewers$/)).toHaveLength(1),
    );
    expect(body(sent(calls, "PUT", /\/livetv\/channels\/c1\/viewers$/)[0])).toEqual({
      allow: [13],
      deny: [],
    });
    expect(body(sent(calls, "PUT", /\/livetv\/channels\/c1$/)[0])).not.toHaveProperty(
      "allowedUsers",
    );
  });

  it("makes a channel private and gives it to chosen viewers", async () => {
    const calls = backend([iptv("c1")], (url) => {
      if (url.endsWith("/users"))
        return {
          users: [
            { id: 12, title: "Sam", username: "sam" },
            { id: 13, title: "Kim", username: "kim" },
          ],
        };
      if (url.endsWith("/livetv/policies"))
        return [{ userId: 13, disabled: false, restricted: false, granted: [] }];
      return undefined;
    });
    renderLiveTV("/livetv?channel=c1");
    fireEvent.click(await screen.findByRole("tab", { name: "Viewers" }, WAIT));
    expect(await screen.findByRole("switch", { name: "Sam" }, WAIT)).toBeChecked();
    fireEvent.click(screen.getByRole("switch", { name: "Private" }));
    // Private hides it from everyone not given it.
    expect(screen.getByRole("switch", { name: "Sam" })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: "Kim" })).not.toBeChecked();
    fireEvent.click(screen.getByRole("switch", { name: "Kim" }));
    fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));
    await waitFor(() =>
      expect(sent(calls, "PUT", /\/livetv\/channels\/c1\/viewers$/)).toHaveLength(1),
    );
    expect(body(sent(calls, "PUT", /\/livetv\/channels\/c1$/)[0]).private).toBe(true);
    expect(body(sent(calls, "PUT", /\/livetv\/channels\/c1\/viewers$/)[0])).toEqual({
      allow: [13],
      deny: [],
    });
  });

  it("programmes a named block from a collection chosen by name", async () => {
    const calls = backend([iptv("seed")]);
    renderLiveTV();
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/collections"))).toBe(true));
    await newBlankChannel("Family TV");
    fireEvent.click(screen.getAllByRole("button", { name: "Add" }).at(-1)!);
    fireEvent.click(await screen.findByRole("button", { name: "Programme block" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Block name" }), {
      target: { value: "Family afternoons" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Source type" }));
    fireEvent.click(await screen.findByRole("option", { name: "Collection" }));
    fireEvent.click(screen.getByRole("button", { name: "Collection" }));
    fireEvent.click(await screen.findByRole("option", { name: "Family favourites" }));
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    fireEvent.click(screen.getByRole("button", { name: /Create channel/ }));
    await waitFor(() => expect(sent(calls, "POST", /\/livetv\/channels$/)).toHaveLength(1));
    expect(
      body(sent(calls, "POST", /\/livetv\/channels$/)[0]).blocks[0].sources[0].collection,
    ).toBe(6000000000000042);
  });

  it("restores the default Start season on blur when the field is cleared", async () => {
    const calls = backend([iptv("seed")]);
    renderLiveTV();
    await newBlankChannel("Reruns");
    fireEvent.click(screen.getAllByRole("button", { name: "Add" }).at(-1)!);
    fireEvent.click(await screen.findByRole("button", { name: "Programme block" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Block name" }), {
      target: { value: "Reruns" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Source type" }));
    fireEvent.click(await screen.findByRole("option", { name: "TV show" }));
    const seasonInput = await screen.findByRole("spinbutton", { name: "Start season" });
    fireEvent.change(seasonInput, { target: { value: "" } });
    expect(seasonInput).toHaveValue(null);
    fireEvent.blur(seasonInput);
    expect(seasonInput).toHaveValue(1);
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    fireEvent.click(screen.getByRole("button", { name: /Create channel/ }));
    await waitFor(() => expect(sent(calls, "POST", /\/livetv\/channels$/)).toHaveLength(1));
    expect(
      body(sent(calls, "POST", /\/livetv\/channels$/)[0]).blocks[0].sources[0].startSeason,
    ).toBe(1);
  });

  it("lets a block's repeat-gap field be cleared with backspace and restores 0 on blur", async () => {
    const channel: Channel = {
      id: "c1",
      number: "1",
      name: "Movies",
      kind: "virtual",
      enabled: true,
      favorite: false,
      epoch: "2026-01-01",
      seed: 1,
      shuffle: false,
      items: [],
      blocks: [
        {
          id: "b1",
          start: 0,
          end: 1440,
          items: [],
          shuffle: false,
          name: "Movies",
          repeatDays: 5,
          sources: [{ kind: "movies", pool: 40 }],
        },
      ],
    };
    const onChange = vi.fn();
    render(
      <MemoryRouter>
        <ProgrammingEditor
          channel={channel}
          onChange={onChange}
          collections={[]}
          editRequest={{ blockId: "b1" }}
        />
      </MemoryRouter>,
    );
    const repeatInput = await screen.findByRole("spinbutton", {
      name: "No repeats within (days)",
    });
    fireEvent.change(repeatInput, { target: { value: "" } });
    expect(repeatInput).toHaveValue(null);
    fireEvent.blur(repeatInput);
    expect(repeatInput).toHaveValue(0);
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onChange).toHaveBeenCalledWith({
      blocks: [expect.objectContaining({ repeatDays: 0 })],
    });
  });

  it("keeps a running strip's startDate when only its start season or episode is corrected", async () => {
    const channel: Channel = {
      id: "c1",
      number: "1",
      name: "Reruns",
      kind: "virtual",
      enabled: true,
      favorite: false,
      epoch: "2026-01-01",
      seed: 1,
      shuffle: false,
      items: [],
      blocks: [
        {
          id: "b1",
          start: 0,
          end: 1440,
          items: [],
          shuffle: false,
          name: "Reruns",
          sources: [
            {
              kind: "show",
              showKey: 123,
              name: "Some Show",
              strip: true,
              startSeason: 3,
              startEpisode: 2,
              // Already airing - started a month ago, now mid-season 3.
              startDate: "2026-10-01",
            },
          ],
        },
      ],
    };
    const onChange = vi.fn();
    render(
      <MemoryRouter>
        <ProgrammingEditor
          channel={channel}
          onChange={onChange}
          collections={[]}
          editRequest={{ blockId: "b1" }}
        />
      </MemoryRouter>,
    );
    const episodeInput = await screen.findByRole("spinbutton", { name: "Start episode" });
    // Correcting a typo (meant episode 5, not 2) must not restart the strip's clock.
    fireEvent.change(episodeInput, { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onChange).toHaveBeenCalledWith({
      blocks: [
        expect.objectContaining({
          sources: [expect.objectContaining({ startDate: "2026-10-01", startEpisode: 5 })],
        }),
      ],
    });
  });

  it("edits a schedule block in a drawer and previews its applied programming", async () => {
    const calls = backend();
    renderLiveTV("/livetv?channel=new");
    fireEvent.click(await screen.findByRole("button", { name: "80s Action 24/7 template" }));
    fireEvent.click(screen.getByRole("tab", { name: "Programming" }));
    fireEvent.click(screen.getByRole("tab", { name: "Blocks" }));
    fireEvent.click(screen.getByRole("gridcell", { name: "Monday at 00:00" }));
    const dialog = await screen.findByRole("dialog", { name: "Programme block" });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    fireEvent.change(within(dialog).getByRole("textbox", { name: "Block name" }), {
      target: { value: "Morning movies" },
    });
    // Add an explicit source: a grid block begins as an empty selected-title list.
    fireEvent.click(within(dialog).getByRole("button", { name: "Add source" }));
    fireEvent.change(within(dialog).getByLabelText("Search titles"), {
      target: { value: "Adventure" },
    });
    fireEvent.click(await within(dialog).findByRole("button", { name: /Adventure/ }, WAIT));
    fireEvent.click(within(dialog).getByRole("button", { name: "Apply" }));
    await waitFor(() => {
      const previews = sent(calls, "POST", /\/livetv\/preview/);
      expect(
        previews.some(
          (c) => body(c).blocks?.[0]?.sources?.[0]?.items?.[0]?.key === 1000000000000007,
        ),
      ).toBe(true);
    }, WAIT);
  });

  it("maps a channel without listings from the Guide tab's drawer", async () => {
    const calls = backend([iptv("c1")], (url) => {
      if (url.endsWith("/livetv/guide/coverage")) return [{ channelId: "c1", programmes: 0 }];
      if (url.includes("/livetv/guide/channels"))
        return [
          {
            sourceId: "epg1",
            sourceName: "Regional listings",
            guideId: "news24.regional",
            name: "News 24 HD",
            programmes: 96,
          },
        ];
      return undefined;
    });
    renderLiveTV("/livetv/guide-data");
    fireEvent.click(await screen.findByRole("button", { name: /Map guide for News 24/ }, WAIT));
    fireEvent.click(await screen.findByRole("button", { name: /News 24 HD/ }, WAIT));
    fireEvent.click(await screen.findByRole("button", { name: /Use this guide/ }, WAIT));
    await waitFor(() => expect(sent(calls, "PUT", /\/livetv\/channels\/c1$/)).toHaveLength(1));
    expect(body(sent(calls, "PUT", /\/livetv\/channels\/c1$/)[0])).toMatchObject({
      guideSourceId: "epg1",
      guideId: "news24.regional",
    });
  });

  it("proposes guide matches and applies only the accepted ones", async () => {
    const calls = backend(
      [iptv("c1"), iptv("c2", { number: "101", name: "Cartoons" })],
      (url, init) => {
        if (url.endsWith("/livetv/guide/coverage"))
          return [
            { channelId: "c1", programmes: 0 },
            { channelId: "c2", programmes: 0 },
          ];
        if (url.endsWith("/livetv/guide/match")) {
          const b = JSON.parse(String(init?.body ?? "{}"));
          return {
            matched: b.dryRun ? 2 : 1,
            unmatched: 0,
            changes: [
              {
                channelId: "c1",
                guideSourceId: "epg1",
                guideId: "n24",
                guideName: "News 24 HD",
                method: "name",
                reviewRegion: true,
              },
              {
                channelId: "c2",
                guideSourceId: "epg1",
                guideId: "toons",
                guideName: "Toons",
                method: "id",
              },
            ],
          };
        }
        return undefined;
      },
    );
    renderLiveTV("/livetv/guide-data");
    const autoMatch = await screen.findByRole("button", { name: /Auto-match/ }, WAIT);
    await waitFor(() => expect(autoMatch).toBeEnabled());
    fireEvent.click(autoMatch);
    const uncertain = await screen.findByLabelText("Accept match for News 24", {}, WAIT);
    expect(uncertain).not.toBeChecked();
    expect(screen.getByText("Check region")).toBeVisible();
    expect(screen.getByLabelText("Accept match for Cartoons")).toBeChecked();
    fireEvent.click(uncertain);
    fireEvent.click(await screen.findByLabelText("Accept match for Cartoons", {}, WAIT));
    fireEvent.click(await screen.findByRole("button", { name: /Apply 1/ }, WAIT));
    await waitFor(() => expect(sent(calls, "POST", /\/livetv\/guide\/match$/)).toHaveLength(2));
    const [dry, apply] = sent(calls, "POST", /\/livetv\/guide\/match$/);
    expect(body(dry)).toEqual({ dryRun: true });
    expect(body(apply)).toEqual({ channelIds: ["c1"] });
  });

  it("imports a guide-only XMLTV source", async () => {
    const calls = backend([iptv("seed")]);
    renderLiveTV();
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Guide source/ }));
    fireEvent.change(await screen.findByLabelText("Source name"), {
      target: { value: "Regional listings" },
    });
    fireEvent.change(screen.getByLabelText("XMLTV guide"), {
      target: { value: "https://epg.example/regional.xml.gz" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Import source" }));
    await waitFor(() => expect(sent(calls, "POST", /\/livetv\/sources(\?|$)/)).toHaveLength(1));
    expect(body(sent(calls, "POST", /\/livetv\/sources(\?|$)/)[0])).toMatchObject({
      name: "Regional listings",
      guideUrl: "https://epg.example/regional.xml.gz",
      playlistUrl: "",
      playlist: "",
    });
  });

  it("orders a primary and backup stream source", async () => {
    const calls = backend([iptv("c1")]);
    renderLiveTV("/livetv?channel=c1");
    fireEvent.click(await screen.findByRole("button", { name: /Add backup/ }, WAIT));
    fireEvent.change(screen.getByLabelText("Source 2 URL"), {
      target: { value: "https://backup.example/news.m3u8" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));
    await waitFor(() => expect(sent(calls, "PUT", /\/livetv\/channels\/c1$/)).toHaveLength(1));
    const saved = body(sent(calls, "PUT", /\/livetv\/channels\/c1$/)[0]);
    expect(saved.streamUrl).toBe("https://iptv.example/news.m3u8");
    expect(saved.streamSources).toMatchObject([
      { url: "https://iptv.example/news.m3u8", enabled: true, priority: 0 },
      { url: "https://backup.example/news.m3u8", enabled: true, priority: 1 },
    ]);
  });

  it("rebinds an open channel drawer to another channel and names that channel on delete", async () => {
    const channels: Record<string, unknown>[] = [iptv("news", { number: "2", name: "News 24" })];
    const calls = backend(channels, (url, init) => {
      const method = init?.method ?? "GET";
      if (url.endsWith("/livetv/channels") && method === "POST") {
        const saved = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
        saved.id = "created";
        channels.push(saved);
        return saved;
      }
      return undefined;
    });
    renderLiveTV();
    await newBlankChannel("Movie night");
    fireEvent.click(screen.getAllByRole("button", { name: "Add" }).at(-1)!);
    fireEvent.click(await screen.findByRole("button", { name: "Programme block" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Block name" }), {
      target: { value: "Movie night" },
    });

    fireEvent.change(screen.getByLabelText("Search titles"), { target: { value: "Adventure" } });
    fireEvent.click(await screen.findByRole("button", { name: /Adventure/ }, WAIT));
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    fireEvent.click(screen.getByRole("button", { name: /Create channel/ }));
    await waitFor(() => expect(sent(calls, "POST", /\/livetv\/channels$/)).toHaveLength(1));
    expect(await screen.findByLabelText("Name")).toHaveValue("Movie night");
    const lineup = screen.getByRole("region", { name: "Channels" });
    fireEvent.click(await within(lineup).findByRole("button", { name: "2 News 24" }));
    expect(await screen.findByLabelText("Name")).toHaveValue("News 24");
    fireEvent.click(screen.getByRole("tab", { name: "Details" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete News 24" }));
    const heading = await screen.findByRole("heading", { name: "Delete News 24?" });
    fireEvent.click(
      within(heading.closest('[role="dialog"]') as HTMLElement).getByRole("button", {
        name: "Delete News 24",
      }),
    );
    await waitFor(() => expect(sent(calls, "DELETE", /\/livetv\/channels\/news$/)).toHaveLength(1));
    expect(sent(calls, "DELETE", /\/livetv\/channels\/created$/)).toHaveLength(0);
  });
});

describe("Live TV preview player", () => {
  it("tunes the saved channel in a mini player and stops the session on close", async () => {
    const user = userEvent.setup();
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    const calls = backend([iptv("c1")], (url, init) => {
      if (url.split("?")[0]?.endsWith("/livetv/channels/c1/play") && init?.method === "POST")
        return { id: "s1", playlist: "sessions/s1/index.m3u8", activeSourceName: "Samsung" };
      return undefined;
    });
    renderLiveTV("/livetv?channel=c1");
    await user.click(await screen.findByRole("button", { name: "Watch" }, WAIT));
    const player = await screen.findByRole("dialog", { name: "Watching News 24" });
    await waitFor(() => expect(hlsLoads).toContain("/ether/api/livetv/sessions/s1/index.m3u8"));
    expect(await within(player).findByText("via Samsung")).toBeInTheDocument();
    await user.click(within(player).getByRole("button", { name: "Stop watching" }));
    await waitFor(() => expect(sent(calls, "DELETE", /\/livetv\/sessions\/s1$/)).toHaveLength(1));
    expect(screen.queryByRole("dialog", { name: "Watching News 24" })).toBeNull();
  });
});

describe("Live TV source sign-in", () => {
  it("imports an Xtream Codes source from a server and login", async () => {
    const calls = backend([iptv("seed")]);
    renderLiveTV();
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /IPTV source/ }));
    fireEvent.change(await screen.findByLabelText("Source name"), {
      target: { value: "My provider" },
    });
    fireEvent.click(screen.getByRole("radio", { name: /Xtream Codes login/ }));
    fireEvent.change(screen.getByLabelText("Server"), {
      target: { value: "http://panel.example:8080" },
    });
    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "me" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "pw" } });
    fireEvent.click(screen.getByRole("button", { name: "Import source" }));
    await waitFor(() => expect(sent(calls, "POST", /\/livetv\/sources(\?|$)/)).toHaveLength(1));
    expect(body(sent(calls, "POST", /\/livetv\/sources(\?|$)/)[0])).toMatchObject({
      name: "My provider",
      playlistUrl: "",
      auth: {
        type: "xtream",
        server: "http://panel.example:8080",
        username: "me",
        password: "pw",
        output: "ts",
      },
    });
  });

  it("keeps a stored password by sending the placeholder back", async () => {
    const calls = backend([], (url, init) => {
      if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET")
        return [
          {
            id: "p1",
            name: "Protected",
            playlistUrl: "https://iptv.example/list.m3u",
            updatedAt: "2026-10-19T00:00:00Z",
            hasPlaylist: true,
            auth: { type: "basic", username: "owner", password: "••••", userAgent: "VLC/3.0" },
          },
        ];
      return undefined;
    });
    renderLiveTV("/livetv?source=p1");
    expect(await screen.findByText(/Saved. Leave as is to keep it/, {}, WAIT)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save and re-import" }));
    await waitFor(() => expect(sent(calls, "PUT", /\/livetv\/sources\/p1(\?|$)/)).toHaveLength(1));
    expect(body(sent(calls, "PUT", /\/livetv\/sources\/p1(\?|$)/)[0]).auth).toEqual({
      type: "basic",
      username: "owner",
      password: "••••",
      userAgent: "VLC/3.0",
    });
  });

  it("saves an uploaded-playlist source without re-uploading its file", async () => {
    const calls = backend([], (url, init) => {
      if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET")
        return [
          {
            id: "u1",
            name: "Uploaded list",
            updatedAt: "2026-10-19T00:00:00Z",
            hasPlaylist: true,
            hasGuide: true,
            channelCount: 98,
          },
        ];
      return undefined;
    });
    renderLiveTV("/livetv?source=u1");
    expect(await screen.findByDisplayValue("Uploaded list", {}, WAIT)).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save and re-import" });
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await waitFor(() => expect(sent(calls, "PUT", /\/livetv\/sources\/u1(\?|$)/)).toHaveLength(1));
    // GET never returns inline uploads; the server keeps the stored ones when the body has none.
    expect(body(sent(calls, "PUT", /\/livetv\/sources\/u1(\?|$)/)[0])).toMatchObject({
      playlist: "",
      playlistUrl: "",
      guide: "",
      guideUrl: "",
    });
  });

  it("still needs a playlist when a linked source's URL is cleared", async () => {
    backend([], (url, init) => {
      if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET")
        return [
          {
            id: "l1",
            name: "Linked list",
            playlistUrl: "https://iptv.example/list.m3u",
            updatedAt: "2026-10-19T00:00:00Z",
            hasPlaylist: true,
          },
        ];
      return undefined;
    });
    renderLiveTV("/livetv?source=l1");
    expect(await screen.findByDisplayValue("Linked list", {}, WAIT)).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save and re-import" });
    expect(save).toBeEnabled();
    fireEvent.change(screen.getByLabelText("M3U playlist"), { target: { value: "" } });
    expect(save).toBeDisabled();
  });

  it("saves an uploaded-XMLTV guide-only source without re-uploading its file", async () => {
    const calls = backend([], (url, init) => {
      if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET")
        return [
          {
            id: "g1",
            name: "Uploaded guide",
            updatedAt: "2026-10-19T00:00:00Z",
            hasPlaylist: false,
            hasGuide: true,
          },
        ];
      return undefined;
    });
    renderLiveTV("/livetv?source=g1");
    expect(await screen.findByDisplayValue("Uploaded guide", {}, WAIT)).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save and re-import" });
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await waitFor(() => expect(sent(calls, "PUT", /\/livetv\/sources\/g1(\?|$)/)).toHaveLength(1));
  });

  it.each([true, false])(
    "edits an Xtream connection with live TV %s and preserves its login",
    async (useLiveTV) => {
      const calls = backend([], (url, init) => {
        if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET")
          return [
            {
              id: "x1",
              name: "Big panel",
              useLiveTV,
              useVOD: true,
              updatedAt: "2026-10-19T00:00:00Z",
              hasPlaylist: useLiveTV,
              channelCount: 130,
              auth: {
                type: "xtream",
                server: "http://panel.example",
                username: "me",
                password: "••••",
              },
              account: {
                status: "Active",
                trial: true,
                expiresAt: new Date(Date.now() + 5 * 3600e3).toISOString(),
                maxConnections: 1,
                activeConnections: 0,
                checkedAt: "2026-10-19T00:00:00Z",
              },
              groups: [
                { name: "EVENTS", channels: 20 },
                { name: "UK | News", channels: 100 },
                { name: "Adult", channels: 10, adult: true, excluded: true },
              ],
              excludeGroups: ["Adult"],
            },
          ];
        return undefined;
      });
      renderLiveTV("/livetv?source=x1");
      expect(await screen.findByText("Allows 1 connection", {}, WAIT)).toBeInTheDocument();
      expect(screen.getAllByText("Trial").length).toBeGreaterThan(0);
      expect(screen.getByRole("switch", { name: "Use for movies & shows" })).toBeChecked();
      expect(screen.getByRole("switch", { name: "Use for Live TV" })).toHaveProperty(
        "checked",
        useLiveTV,
      );
      fireEvent.click(screen.getByRole("tab", { name: /Channel groups/ }));
      expect(screen.getByText("120 channels will be imported")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("checkbox", { name: /EVENTS/ }));
      expect(screen.getByText("100 channels will be imported")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("switch", { name: "Include new groups automatically" }));
      fireEvent.click(screen.getByRole("radio", { name: "My order" }));
      fireEvent.click(screen.getByRole("button", { name: "Save and re-import" }));
      await waitFor(() =>
        expect(sent(calls, "PUT", /\/livetv\/sources\/x1(?:\?|$)/)).toHaveLength(1),
      );
      expect(body(sent(calls, "PUT", /\/livetv\/sources\/x1(?:\?|$)/)[0])).toMatchObject({
        auth: { type: "xtream", server: "http://panel.example", username: "me", password: "••••" },
        useLiveTV,
        useVOD: true,
        excludeGroups: ["Adult", "EVENTS"],
        newGroups: "exclude",
        hideAdult: false,
        groupOrder: ["UK | News"],
      });
    },
  );

  it("lets a backup input use another source's login", async () => {
    const calls = backend(
      [
        iptv("c1", {
          streamSources: [
            {
              id: "imp",
              name: "Primary",
              url: "https://a.example/1.m3u8",
              enabled: true,
              priority: 0,
            },
            {
              id: "bk",
              name: "Backup",
              url: "https://b.example/1.m3u8",
              enabled: true,
              priority: 1,
            },
          ],
        }),
      ],
      (url, init) => {
        if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET")
          return [
            {
              id: "b",
              name: "Provider B",
              updatedAt: "2026-10-19T00:00:00Z",
              auth: { type: "bearer", token: "••••" },
            },
          ];
        return undefined;
      },
    );
    renderLiveTV("/livetv?channel=c1");
    fireEvent.click(await screen.findByRole("button", { name: "Login for source 2" }, WAIT));
    fireEvent.click(screen.getByRole("menuitemradio", { name: /Provider B/ }));
    fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));
    await waitFor(() => expect(sent(calls, "PUT", /\/livetv\/channels\/c1$/)).toHaveLength(1));
    const saved = body(sent(calls, "PUT", /\/livetv\/channels\/c1$/)[0]);
    expect(saved.streamSources[0].authSourceId).toBeUndefined();
    expect(saved.streamSources[1]).toMatchObject({ id: "bk", authSourceId: "b" });
  });
});

describe("Live TV Stremio plugins", () => {
  const kind = "plugin:addon.abc/channels";
  const addonDescriptor = {
    kind: "liveTVSource",
    id: kind,
    owner: "addon.abc",
    label: { resolved: "Sports TV" },
    hint: { source: "Live channels from a Stremio plugin." },
    meta: { editor: "addon", yieldsChannels: true, yieldsGuide: true },
  };

  it("shows a plugin source's plugin and Plugins link, never a link field", async () => {
    invalidateRegistry();
    backend([], (url, init) => {
      if (url.includes("/extend/registry")) return { version: 1, descriptors: [addonDescriptor] };
      if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET")
        return [
          {
            id: "a1",
            name: "My sports",
            kind,
            playlistUrl: kind,
            hasPlaylist: true,
            updatedAt: "2026-10-19T00:00:00Z",
          },
        ];
      return undefined;
    });
    renderLiveTV("/livetv?source=a1");
    expect(await screen.findByText("Channels come from Sports TV.", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Manage in Plugins" })).toHaveAttribute(
      "href",
      "/plugins/installed",
    );
    expect(screen.queryByLabelText("M3U playlist")).not.toBeInTheDocument();
    expect(screen.queryByText("Sign-in")).not.toBeInTheDocument();
  });

  it("adds a plugin through the shared drawer, then imports its channels", async () => {
    invalidateRegistry();
    let sources: unknown[] = [];
    const calls = backend([iptv("seed")], (url, init) => {
      const method = init?.method ?? "GET";
      if (url.includes("/extend/registry")) return { version: 1, descriptors: [addonDescriptor] };
      if (url.endsWith("/plugins/addons/preview"))
        return {
          id: "addon.abc",
          name: "Sports TV",
          host: "tv.example",
          roles: [{ role: "livetv_channels", on: false }],
        };
      if (url.endsWith("/plugins/addons") && method === "POST")
        return {
          id: "addon.abc",
          name: "Sports TV",
          version: "1.0.0",
          enabled: true,
          stremioOnly: true,
          roles: [{ role: "livetv_channels", on: true }],
        };
      if (url.endsWith("/livetv/sources") && method === "POST") {
        const saved = { ...body({ url, init }), id: "a1", updatedAt: "2026-10-19T00:00:00Z" };
        sources = [saved];
        return saved;
      }
      if (url.endsWith("/livetv/sources") && method === "GET") return sources;
      return undefined;
    });
    renderLiveTV("/livetv");
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Stremio plugin/ }));
    fireEvent.change(await screen.findByPlaceholderText("https://… /manifest.json"), {
      target: { value: "https://tv.example/cfg/manifest.json" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.click(await screen.findByRole("button", { name: "Add plugin" }, WAIT));
    await waitFor(() => expect(sent(calls, "POST", /\/livetv\/sources$/)).toHaveLength(1), WAIT);
    expect(body(sent(calls, "POST", /\/livetv\/sources$/)[0])).toMatchObject({
      name: "Sports TV",
      kind,
      playlistUrl: kind,
    });
    expect(body(sent(calls, "POST", /\/plugins\/addons$/)[0])).toEqual({
      url: "https://tv.example/cfg/manifest.json",
      roles: ["livetv_channels"],
    });
    expect(await screen.findByText("Channels come from Sports TV.", {}, WAIT)).toBeInTheDocument();
  });
});

describe("Live TV access on the viewer profile", () => {
  it(
    "only switches Live TV on or off for the viewer; channels are managed on each channel",
    { timeout: 30000 },
    async () => {
      let policy = {
        userId: 2,
        disabled: false,
        restricted: true,
        channels: ["c1"],
        favorites: [],
        maxRating: "",
      };
      const { fake } = renderAppWith("/viewers/2/limits", (method, path, body) => {
        if (path === "/livetv/channels")
          return [
            { id: "c1", number: "1", name: "Movie night", kind: "virtual", enabled: true },
            { id: "c2", number: "2", name: "Cartoons", kind: "virtual", enabled: true },
          ];
        if (path === "/livetv/policies/2") {
          if (method === "PUT") policy = body as typeof policy;
          return policy;
        }
        return undefined;
      });
      // First visit to the lazily loaded viewer profile route: allow a cold chunk load.
      fireEvent.click(
        await screen.findByLabelText("Disable Live TV for this viewer", {}, { timeout: 15000 }),
      );
      // Thousands of channels never list here; access lives on each channel.
      expect(screen.queryByLabelText("Allow Movie night")).not.toBeInTheDocument();
      expect(screen.queryByText("Allowed channels")).not.toBeInTheDocument();
      fireEvent.click(await screen.findByRole("button", { name: "Save changes" }));
      await waitFor(() =>
        expect(fakeCalls(fake, "PUT", "/livetv/policies/2").at(-1)?.body).toMatchObject({
          disabled: true,
          restricted: true,
          channels: ["c1"],
        }),
      );
    },
  );
});

it("bulk selects matching channels beyond the virtualised viewport without fetching full rows", async () => {
  const channels = Array.from({ length: 450 }, (_, i) =>
    iptv(`paged-${i}`, {
      number: String(i + 1),
      name: `Paged ${i + 1}`,
      enabled: i !== 449,
    }),
  );
  const calls = backend(channels);
  renderLiveTV();
  await screen.findByRole("button", { name: "1 Paged 1" }, WAIT);
  expect(screen.getByText(/1 disabled/)).toBeInTheDocument();
  const statusBar = screen.getByRole("radiogroup", { name: "Status" });
  fireEvent.click(within(statusBar).getByRole("radio", { name: /^Enabled/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Select all matches" }));
  fireEvent.click(await screen.findByRole("button", { name: /^Favourite$/ }));
  await waitFor(() => expect(sent(calls, "PATCH", /\/livetv\/channels\/bulk$/)).toHaveLength(1));
  const edit = body(sent(calls, "PATCH", /\/livetv\/channels\/bulk$/)[0]);
  expect(edit.ids).toHaveLength(449);
  expect(edit.ids).toContain("paged-448");
  expect(edit.ids).not.toContain("paged-449");
  expect(edit.favorite).toBe(true);
  expect(sent(calls, "GET", /\/livetv\/channels$/)).toHaveLength(0);
});

it("organises per-app visibility, status filters and header guide-mapping link without a Guides tab", async () => {
  const channels = [
    iptv("uk1", { number: "101", name: "BBC One", group: "UK", guideId: "bbc1", hiddenApps: [] }),
    iptv("uk2", { number: "102", name: "ITV1", group: "UK", guideId: "", hiddenApps: ["plex"] }),
    iptv("sp1", {
      number: "201",
      name: "Sky Sports",
      group: "Sports",
      enabled: false,
      hiddenApps: [],
    }),
  ];
  const calls = backend(channels, (url, init) => {
    if (url.endsWith("/livetv/sources") && (init?.method ?? "GET") === "GET") {
      return [
        { id: "src1", name: "Strong IPTV", hasPlaylist: true, updatedAt: "2026-10-19T00:00:00Z" },
      ];
    }
    if (url.endsWith("/livetv/guide/coverage")) {
      return [
        { channelId: "uk1", programmes: 24 },
        { channelId: "uk2", programmes: 0 },
        { channelId: "sp1", programmes: 0 },
      ];
    }
    if (url.includes("/livetv/guide?")) {
      const now = Date.now();
      return [
        {
          id: "prog-uk1",
          channelId: "uk1",
          start: new Date(now - 600_000).toISOString(),
          end: new Date(now + 1_800_000).toISOString(),
          item: { key: 1, durationMs: 2_400_000, title: "Six O'Clock News" },
        },
      ];
    }
    if (url.endsWith("/livetv/channels/apps") && init?.method === "POST") {
      return { updated: 1 };
    }
    return undefined;
  });
  renderLiveTV();
  const list = await screen.findByRole("region", { name: "Channels" }, WAIT);
  await within(list).findByRole("button", { name: "101 BBC One" }, WAIT);

  const topTabs = screen.getByRole("tablist", { name: "Live TV" });
  expect(within(topTabs).queryByRole("tab", { name: "Guides" })).toBeNull();
  expect(screen.queryByRole("region", { name: "On now" })).toBeNull();
  expect(within(topTabs).getByRole("tab", { name: "Channels" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(within(topTabs).getByRole("tab", { name: "Guide" })).toBeInTheDocument();

  const showingFor = screen.getByRole("toolbar", { name: "Showing for" });
  expect(within(showingFor).getByRole("button", { name: /Vela/ })).toBeInTheDocument();
  fireEvent.click(within(showingFor).getByRole("button", { name: /^Plex/ }));

  const statusBar = screen.getByRole("radiogroup", { name: "Status" });
  fireEvent.click(within(statusBar).getByRole("radio", { name: /^Hidden from Plex/ }));
  expect(within(list).getByRole("button", { name: "102 ITV1" })).toBeInTheDocument();
  expect(within(list).queryByRole("button", { name: "101 BBC One" })).toBeNull();

  const tree = screen.getByRole("navigation", { name: "Channel groups" });
  fireEvent.click(within(tree).getByRole("checkbox", { name: "Show UK in Plex" }));
  await waitFor(() => expect(sent(calls, "POST", /\/livetv\/channels\/apps$/)).toHaveLength(1));
  expect(body(sent(calls, "POST", /\/livetv\/channels\/apps$/)[0])).toEqual({
    app: "plex",
    show: true,
    source: "src1",
    group: "UK",
  });

  fireEvent.click(within(topTabs).getByRole("tab", { name: "Guide" }));
  const onNow = await screen.findByRole("region", { name: "On now" }, WAIT);
  expect(within(onNow).getByRole("button", { name: /BBC One on now:/ })).toBeInTheDocument();

  fireEvent.click(screen.getByRole("link", { name: "1 needs a guide" }));
  expect(
    await screen.findByRole("button", { name: /Map guide for ITV1/ }, WAIT),
  ).toBeInTheDocument();
});

it("uses saved source state when a different cluster node has no import progress", async () => {
  const source = {
    id: "src1",
    name: "Shared source",
    hasPlaylist: true,
    playlistUrl: "https://example.invalid/list.m3u",
    channelCount: 17,
  };
  let finish!: (v: unknown) => void;
  const importing = new Promise((resolve) => {
    finish = resolve;
  });
  const calls = backend([iptv("c1")], (url, init) => {
    if (url.endsWith("/livetv/sources")) return [source];
    if (url.includes("/livetv/sources/progress/")) return null;
    if (url.includes("/livetv/sources/src1?") && init?.method === "PUT") return importing;
    return undefined;
  });
  renderLiveTV("/livetv?source=src1");
  const save = await screen.findByRole("button", { name: "Save and re-import" }, WAIT);
  await waitFor(() => expect(save).toBeEnabled());
  fireEvent.click(save);
  await waitFor(() => expect(sent(calls, "PUT", /sources/)).toHaveLength(1));
  expect(
    await screen.findByText(
      "Import in progress. Detailed progress is unavailable on this server.",
      {},
      WAIT,
    ),
  ).toBeVisible();
  expect(await screen.findByText("17 channels in the last saved import", {}, WAIT)).toBeVisible();
  expect(sent(calls, "GET", /\/livetv\/sources$/).length).toBeGreaterThan(1);
  finish(source);
  await waitFor(() =>
    expect(
      screen.queryByText("Import in progress. Detailed progress is unavailable on this server."),
    ).toBeNull(),
  );
});

it("estimates AI cost before running and applies only reviewed proposals", async () => {
  const calls = backend(
    [iptv("c1"), iptv("c2", { name: "Cartoons", number: "102" })],
    (url, init) => {
      if (url.endsWith("/livetv/guide/coverage"))
        return [
          { channelId: "c1", programmes: 0 },
          { channelId: "c2", programmes: 0 },
        ];
      if (!url.endsWith("/livetv/guide/match")) return undefined;
      const b = JSON.parse(String(init?.body ?? "{}"));
      return {
        matched: 2,
        unmatched: 0,
        ai: {
          configured: true,
          estimatedCalls: 1,
          inputTokens: 1200,
          cached: 0,
          calls: b.ai === "propose" ? 1 : 0,
          deferred: 0,
        },
        changes:
          b.ai === "estimate"
            ? []
            : [
                {
                  channelId: "c1",
                  guideSourceId: "epg1",
                  guideId: "news",
                  guideName: "News 24",
                  method: "ai",
                  confidence: 0.98,
                  reason: "Same station and region",
                  proposalKey: "key-1",
                },
                {
                  channelId: "c2",
                  guideSourceId: "epg1",
                  guideId: "cartoons",
                  guideName: "Cartoons",
                  method: "ai",
                  confidence: 0.7,
                  reason: "Region uncertain",
                  proposalKey: "key-2",
                },
              ],
      };
    },
  );
  renderLiveTV("/livetv/guide-data");
  const matchWithAI = await screen.findByRole("button", { name: "Match with AI" }, WAIT);
  await waitFor(() => expect(matchWithAI).toBeEnabled());
  fireEvent.click(matchWithAI);
  expect(await screen.findByText(/Estimated calls: 1/)).toBeVisible();
  expect(calls.filter((c) => c.url.endsWith("/guide/match"))).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Find suggestions" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Accept match for News 24" })).toHaveAttribute(
      "aria-pressed",
      "true",
    ),
  );
  expect(screen.getByRole("button", { name: "Accept match for Cartoons" })).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  expect(screen.getByText("70% confidence")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Apply 1" }));
  await waitFor(() => expect(calls.filter((c) => c.url.endsWith("/guide/match"))).toHaveLength(3));
  const matchCalls = calls.filter((c) => c.url.endsWith("/guide/match"));
  expect(JSON.parse(String(matchCalls[2]?.init?.body))).toMatchObject({
    ai: "commit",
    selections: [{ channelId: "c1", guideSourceId: "epg1", guideId: "news", proposalKey: "key-1" }],
  });
});

describe("timeline snapshot boundary", () => {
  const range = { from: Date.UTC(2026, 10, 5), to: Date.UTC(2026, 10, 6) };
  it.each([
    null,
    {},
    [],
    {
      timezone: "UTC",
      dayparts: [],
      elements: [
        { id: "bad", title: "Bad", rule: "", kind: "pod", start: 1, end: 2, children: {} },
      ],
    },
  ])("renders a safe error for malformed snapshots %j", async (snapshot) => {
    backend([], (url) => (url.includes("/timeline?") ? snapshot : undefined));
    render(<ScheduleTimeline channelId="test" range={range} />);
    expect(await screen.findByText("Unable to load schedule")).toBeVisible();
  });
  it("retries a cold snapshot and renders it when ready", async () => {
    let attempts = 0;
    backend([], (url) => {
      if (!url.includes("/timeline?")) return undefined;
      if (attempts++ === 0)
        return new Response(JSON.stringify({ error: { code: "warming", message: "warming" } }), {
          status: 503,
          headers: { "Retry-After": "1" },
        });
      return { channelId: "test", timezone: "UTC", now: range.from, elements: [], dayparts: [] };
    });
    render(<ScheduleTimeline channelId="test" range={range} />);
    await waitFor(() => expect(attempts).toBe(2), { timeout: 3500 });
    expect(screen.queryByText("Unable to load schedule")).not.toBeInTheDocument();
  });
});

it("creates a rule-based owner group in a drawer with live preview and inline regex validation", async () => {
  const chans = [
    iptv("cbs", { number: "2", name: "CBS WCBS HD", group: "US Locals", guideId: "cbs.wcbs" }),
    iptv("espn", { number: "206", name: "ESPN HD", group: "Sports", guideId: "espn.us" }),
    iptv("espn-bak", { number: "207", name: "ESPN Backup", group: "Sports", guideId: "espn.us" }),
    iptv("bbc", { number: "501", name: "BBC News", group: "News", guideId: "bbc.news" }),
  ];
  let savedGroups: OwnerGroup[] = [];
  const calls = backend(chans, (url, init) => {
    const method = init?.method ?? "GET";
    if (url.endsWith("/livetv/sources")) {
      return [
        { id: "src1", name: "Tuliprox US", hasPlaylist: true, updatedAt: new Date().toISOString() },
      ];
    }
    if (url.endsWith("/livetv/groups") && method === "GET") return savedGroups;
    if (url.endsWith("/livetv/groups") && method === "PUT") {
      savedGroups = JSON.parse(String(init?.body ?? "[]")) as OwnerGroup[];
      return savedGroups;
    }
    if (url.endsWith("/livetv/groups/preview") && method === "POST") {
      const draft = JSON.parse(String(init?.body ?? "{}")) as OwnerGroup;
      for (const [i, r] of (draft.rules ?? []).entries()) {
        if ((r.op === "matches" || r.op === "notMatches") && r.value) {
          if (!compileOwnerGroupRegExp(r.value)) {
            return new Response(
              JSON.stringify({
                error: {
                  code: "livetv",
                  message: `invalid pattern "${r.value}"`,
                  fields: [
                    {
                      field: `rules.${i}.value`,
                      message: `error parsing regexp: missing closing ): \`${r.value}\``,
                    },
                  ],
                },
              }),
              { status: 400 },
            );
          }
        }
      }
      const match = compileOwnerGroupMatcher(draft, new Map([["src1", "Tuliprox US"]]));
      const matched = chans.filter((c) => match(c as any));
      return { count: matched.length, channels: matched };
    }
    return undefined;
  });

  renderLiveTV("/livetv");
  const nav = await screen.findByRole("navigation", { name: "Channel groups" }, WAIT);
  fireEvent.click(within(nav).getByRole("button", { name: "New" }));

  const dialog = await screen.findByRole("dialog", { name: "New group" }, WAIT);
  fireEvent.change(within(dialog).getByLabelText("Group name"), {
    target: { value: "US Sports" },
  });

  fireEvent.click(within(dialog).getByRole("button", { name: "Add rule" }));
  const fieldBtn = within(dialog).getByRole("button", { name: "Rule field" });
  const opBtn = within(dialog).getByRole("button", { name: "Rule condition" });
  expect(fieldBtn).toHaveTextContent("Channel name");
  expect(opBtn).toHaveTextContent("contains");

  fireEvent.click(fieldBtn);
  fireEvent.click(await screen.findByRole("option", { name: "Guide ID" }));
  fireEvent.click(opBtn);
  fireEvent.click(await screen.findByRole("option", { name: "matches pattern" }));
  fireEvent.change(within(dialog).getByLabelText("Rule value"), {
    target: { value: "(?i)^(cbs\\.wcbs|espn\\.us" },
  });

  const alert = await within(dialog).findByRole("alert", undefined, WAIT);
  expect(alert).toHaveTextContent("missing closing )");
  expect(within(dialog).getByRole("button", { name: "Create group" })).toBeDisabled();

  fireEvent.change(within(dialog).getByLabelText("Rule value"), {
    target: { value: "(?i)^(cbs\\.wcbs|espn\\.us)$" },
  });
  await waitFor(() => expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument(), WAIT);
  expect(await within(dialog).findByText("3 channels", undefined, WAIT)).toBeVisible();

  fireEvent.click(within(dialog).getByRole("button", { name: "Add rule" }));
  const opBtns = within(dialog).getAllByRole("button", { name: "Rule condition" });
  const valInputs = within(dialog).getAllByLabelText("Rule value");
  fireEvent.click(opBtns[1]!);
  fireEvent.click(await screen.findByRole("option", { name: "doesn't contain" }));
  fireEvent.change(valInputs[1]!, { target: { value: "Backup" } });

  expect(await within(dialog).findByText("2 channels", undefined, WAIT)).toBeVisible();
  expect(within(dialog).getByText("CBS WCBS HD")).toBeVisible();
  expect(within(dialog).getByText("ESPN HD")).toBeVisible();
  expect(within(dialog).queryByText("ESPN Backup")).not.toBeInTheDocument();

  fireEvent.click(within(dialog).getByRole("button", { name: "Create group" }));
  await waitFor(() => expect(sent(calls, "PUT", /\/livetv\/groups$/)).toHaveLength(1), WAIT);

  const putPayload = body(sent(calls, "PUT", /\/livetv\/groups$/)[0]);
  expect(putPayload).toMatchObject([
    {
      name: "US Sports",
      match: "any",
      rules: [
        { field: "guideId", op: "matches", value: "(?i)^(cbs\\.wcbs|espn\\.us)$" },
        { field: "name", op: "notContains", value: "Backup" },
      ],
    },
  ]);

  expect(await within(nav).findByRole("button", { name: "US Sports" }, WAIT)).toBeVisible();
  expect(
    await screen.findByRole("heading", { name: "US Sports · your group" }, WAIT),
  ).toBeVisible();
});

it("merges matching channel feeds with live preview, health, and provider priority reorder", async () => {
  let sourcesList = [
    {
      id: "src-a",
      name: "Alpha TV",
      hasPlaylist: true,
      priority: 1,
      updatedAt: new Date().toISOString(),
    },
    {
      id: "src-b",
      name: "Bravo TV",
      hasPlaylist: true,
      priority: 2,
      updatedAt: new Date().toISOString(),
    },
  ];
  const chans: any[] = [
    iptv("espn-a", {
      number: "206",
      name: "ESPN HD",
      group: "Sports",
      guideId: "espn.us",
      sourceId: "src-a",
      streamUrl: "https://alpha.example/espn-hd.m3u8",
    }),
    iptv("espn-b", {
      number: "207",
      name: "US: ESPN FHD",
      group: "Sports",
      guideId: "espn.us",
      sourceId: "src-b",
      streamUrl: "https://bravo.example/espn-fhd.m3u8",
    }),
    iptv("bbc", {
      number: "501",
      name: "BBC News",
      group: "News",
      guideId: "bbc.news",
      sourceId: "src-a",
      streamUrl: "https://alpha.example/bbc.m3u8",
    }),
  ];

  const calls = backend(chans, (url, init) => {
    const method = init?.method ?? "GET";
    if (url.endsWith("/livetv/sources") && method === "GET") return sourcesList;
    if (url.endsWith("/livetv/sources/order") && method === "PUT") {
      const { ids = [] } = JSON.parse(String(init?.body ?? "{}")) as { ids?: string[] };
      const byId = new Map(sourcesList.map((s) => [s.id, s]));
      sourcesList = ids
        .map((id, i) => {
          const found = byId.get(id);
          return found ? { ...found, priority: i + 1 } : null;
        })
        .filter((x): x is (typeof sourcesList)[number] => x !== null);
      return sourcesList;
    }
    if (url.endsWith("/livetv/channels/summary") && method === "GET") {
      return chans.filter((c) => !c.mergedInto);
    }
    if (url.endsWith("/livetv/channels/merge-preview") && method === "POST") {
      const req = JSON.parse(String(init?.body ?? "{}")) as {
        channelId?: string;
        rules?: OwnerGroupRule[];
        match?: "any" | "all";
      };
      const rules = req.rules ?? [];
      for (const [i, r] of rules.entries()) {
        if ((r.op === "matches" || r.op === "notMatches") && r.value) {
          if (!compileOwnerGroupRegExp(r.value)) {
            return new Response(
              JSON.stringify({
                error: {
                  code: "livetv",
                  message: `invalid pattern "${r.value}"`,
                  fields: [
                    {
                      field: `rules.${i}.value`,
                      message: `error parsing regexp: missing closing ): \`${r.value}\``,
                    },
                  ],
                },
              }),
              { status: 400 },
            );
          }
        }
      }
      const srcNames = new Map(sourcesList.map((s) => [s.id, s.name]));
      const srcRanks = new Map(sourcesList.map((s, idx) => [s.id, idx + 1]));
      const matcher = compileOwnerGroupMatcher(
        { groups: [], rules, match: req.match ?? "any" },
        srcNames,
      );
      const target = chans.find((c) => c.id === req.channelId);
      const donors = chans.filter((c) => c.id !== req.channelId && matcher(c));
      const ordered = [...(target ? [target] : []), ...donors].sort(
        (a, b) =>
          (srcRanks.get(a.sourceId) ?? 99) - (srcRanks.get(b.sourceId) ?? 99) ||
          streamQualityTier(a.name).rank - streamQualityTier(b.name).rank,
      );
      return {
        matchCount: donors.length,
        totalFeeds: ordered.length,
        maxInputs: 16,
        inputs: ordered.map((c, idx) => ({
          id: c.id,
          channelId: c.id,
          name: c.name,
          sourceId: c.sourceId,
          sourceName: srcNames.get(c.sourceId),
          sourcePriority: srcRanks.get(c.sourceId),
          quality: streamQualityTier(c.name).label || undefined,
          primary: c.id === req.channelId,
          included: idx < 16,
          state: c.id === "espn-b" ? "available" : "cooling_down",
        })),
      };
    }
    if (url.endsWith("/livetv/channels/espn-a") && method === "PUT") {
      const saved = JSON.parse(String(init?.body ?? "{}")) as Channel;
      const idx = chans.findIndex((c) => c.id === "espn-a");
      if (idx >= 0) chans[idx] = saved;
      for (const c of chans) {
        if (c.id === "espn-b" && saved.inputMerge?.rules?.length) {
          c.mergedInto = saved.inputMerge.hideDuplicates === false ? undefined : "espn-a";
        }
      }
      return saved;
    }
    return undefined;
  });

  renderLiveTV("/livetv");

  // 1. Reorder provider priority via sidebar drag-and-drop (Bravo TV above Alpha TV).
  const nav = await screen.findByRole("navigation", { name: "Channel groups" }, WAIT);
  const bravoHeader = await within(nav).findByLabelText("Provider Bravo TV", undefined, WAIT);
  const alphaHeader = within(nav).getByLabelText("Provider Alpha TV");
  fireEvent.dragStart(bravoHeader);
  fireEvent.dragOver(alphaHeader);
  fireEvent.drop(alphaHeader);

  await waitFor(
    () => expect(sent(calls, "PUT", /\/livetv\/sources\/order$/)).toHaveLength(1),
    WAIT,
  );
  expect(body(sent(calls, "PUT", /\/livetv\/sources\/order$/)[0])).toEqual({
    ids: ["src-b", "src-a"],
  });

  const lineup = screen.getByRole("region", { name: "Channels" });
  expect(
    await within(lineup).findByRole("button", { name: "207 US: ESPN FHD" }, WAIT),
  ).toBeVisible();
  fireEvent.click(within(lineup).getByRole("button", { name: "206 ESPN HD" }));

  // 2. Configure regex merge rule in the channel drawer with inline validation + live preview.
  const mergeSection = await screen.findByRole("region", { name: "Merge matching feeds" }, WAIT);
  fireEvent.click(within(mergeSection).getByRole("button", { name: "Add rule" }));

  const opBtn = within(mergeSection).getByRole("button", { name: "Rule condition" });
  fireEvent.click(opBtn);
  fireEvent.click(await screen.findByRole("option", { name: "matches pattern" }));

  const valInput = within(mergeSection).getByLabelText("Rule value");
  fireEvent.change(valInput, { target: { value: "(?i)^(?:us:\\s*)?espn\\b(" } });

  const alert = await within(mergeSection).findByRole("alert", undefined, WAIT);
  expect(alert).toHaveTextContent("missing closing )");
  expect(screen.getByRole("button", { name: /Save channel/ })).toBeDisabled();

  fireEvent.change(valInput, { target: { value: "(?i)^(?:us:\\s*)?espn\\b" } });
  await waitFor(
    () => expect(within(mergeSection).queryByRole("alert")).not.toBeInTheDocument(),
    WAIT,
  );

  expect(await within(mergeSection).findByText("1 matching feed", undefined, WAIT)).toBeVisible();
  const previewRows = within(mergeSection).getAllByRole("listitem");
  expect(previewRows).toHaveLength(2);
  expect(previewRows[0]).toHaveTextContent("Primary");
  expect(previewRows[0]).toHaveTextContent("US: ESPN FHD");
  expect(previewRows[0]).toHaveTextContent("FHD");
  expect(previewRows[0]).toHaveTextContent("Bravo TV");
  expect(previewRows[0]).toHaveTextContent("Healthy");
  expect(previewRows[1]).toHaveTextContent("Backup 1");
  expect(previewRows[1]).toHaveTextContent("ESPN HD");
  expect(previewRows[1]).toHaveTextContent("Alpha TV");
  expect(previewRows[1]).toHaveTextContent("Cooling down");

  // 3. Save channel and verify inputMerge payload + duplicate channel hidden from standalone lineup.
  expect(
    within(mergeSection).getByRole("switch", { name: "Hide matched duplicate channels" }),
  ).toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));
  await waitFor(
    () => expect(sent(calls, "PUT", /\/livetv\/channels\/espn-a$/)).toHaveLength(1),
    WAIT,
  );

  const savedBody = body(sent(calls, "PUT", /\/livetv\/channels\/espn-a$/)[0]);
  expect(savedBody.inputMerge).toEqual({
    match: "any",
    rules: [{ field: "name", op: "matches", value: "(?i)^(?:us:\\s*)?espn\\b" }],
  });

  fireEvent.keyDown(document, { key: "Escape" });
  await waitFor(
    () => expect(within(lineup).queryByRole("button", { name: "207 US: ESPN FHD" })).toBeNull(),
    WAIT,
  );
  expect(within(lineup).getByRole("button", { name: "501 BBC News" })).toBeInTheDocument();
});

describe("Live TV channel drawers and responsive workspace polish", () => {
  it("hides virtual timeline chrome on IPTV drawers and distinguishes mapped channels without listings", async () => {
    const mappedNoListings = iptv("c-mapped", {
      number: "101",
      name: "ESPN 1",
      group: "Sports",
      guideId: "strong-1",
      sourceId: "src1",
    });
    const unmapped = iptv("c-unmapped", {
      number: "102",
      name: "ESPN 2",
      group: "Sports",
      guideId: "",
      sourceId: "src1",
    });
    const calls = backend([mappedNoListings, unmapped], (url) => {
      if (url.endsWith("/livetv/sources")) {
        return [
          { id: "src1", name: "Strong IPTV", hasPlaylist: true, updatedAt: "2026-10-19T00:00:00Z" },
        ];
      }
      if (url.endsWith("/livetv/guide/coverage")) {
        return [
          { channelId: "c-mapped", programmes: 0 },
          { channelId: "c-unmapped", programmes: 0 },
        ];
      }
      return undefined;
    });

    const view = renderLiveTV("/livetv?channel=c-mapped");
    const dialog = await screen.findByRole("dialog", { name: "Edit channel" }, WAIT);
    expect(
      await within(dialog).findByText(
        "Mapped to a guide channel, but its guide has no upcoming listings.",
        undefined,
        WAIT,
      ),
    ).toBeVisible();
    expect(
      within(dialog).queryByText(
        "No listings for this channel yet. Map a guide channel on the Guide tab.",
      ),
    ).toBeNull();
    expect(within(dialog).queryByRole("region", { name: "Schedule timeline" })).toBeNull();
    expect(within(dialog).queryByText("Break pod")).toBeNull();
    expect(calls.filter((c) => c.url.includes("/timeline?"))).toHaveLength(0);

    view.unmount();
    renderLiveTV("/livetv?channel=c-unmapped");
    const dialog2 = await screen.findByRole("dialog", { name: "Edit channel" }, WAIT);
    expect(
      await within(dialog2).findByText(
        "No listings for this channel yet. Map a guide channel on the Guide tab.",
        undefined,
        WAIT,
      ),
    ).toBeVisible();
  });

  it("opens a new personal channel cleanly without draft 404s, dirty warning, duplicate subcaptions or raw {viewer} tokens", async () => {
    const calls = backend([iptv("seed")], (url) => {
      if (url.includes("/rundown") || url.includes("/timeline")) {
        return new Response(
          JSON.stringify({ error: { code: "not_found", message: "not found" } }),
          {
            status: 404,
          },
        );
      }
      return undefined;
    });

    renderLiveTV("/livetv");
    fireEvent.click(await screen.findByRole("button", { name: "Add" }, WAIT));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Personal channel/ }));

    const dialog = await screen.findByRole("dialog", { name: "New virtual channel" }, WAIT);
    expect(await within(dialog).findByText("Not saved yet", undefined, WAIT)).toBeVisible();
    expect(
      within(dialog).getByText("Create this channel to build each viewer's schedule."),
    ).toBeVisible();
    expect(within(dialog).queryByText("Unsaved changes")).toBeNull();
    expect(within(dialog).queryByText("not found")).toBeNull();
    expect(within(dialog).queryByText("Unable to load schedule")).toBeNull();
    expect(within(dialog).queryByRole("region", { name: "Schedule timeline" })).toBeNull();
    expect(
      calls.filter((c) => c.url.includes("/rundown") || c.url.includes("/timeline")),
    ).toHaveLength(0);

    // Ingredient labels appear once per row (never duplicated inside Artwork placeholders).
    expect(within(dialog).getAllByText("Picked for them")).toHaveLength(1);
    expect(within(dialog).getAllByText("Your picks (collections)")).toHaveLength(1);

    // Channel name picker uses AppKit SearchSelect with plain-English copy rather than raw {viewer} tokens or native <select>.
    expect(within(dialog).queryByText("{viewer} TV")).toBeNull();
    expect(within(dialog).getByText("Viewer's name + TV")).toBeVisible();
    expect(dialog.querySelector("select")).toBeNull();

    // Editing marks the draft as having unsaved changes.
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "For Everyone" } });
    expect(within(dialog).getByText("Unsaved changes")).toBeVisible();
  });

  it("renders a single selectable provider row per IPTV source under Providers", async () => {
    backend(
      [iptv("c1", { number: "101", name: "Sports 1", group: "Sports", sourceId: "src1" })],
      (url) => {
        if (url.endsWith("/livetv/sources")) {
          return [
            {
              id: "src1",
              name: "Strong IPTV",
              hasPlaylist: true,
              updatedAt: "2026-10-19T00:00:00Z",
            },
          ];
        }
        return undefined;
      },
    );

    renderLiveTV("/livetv");
    const nav = await screen.findByRole("navigation", { name: "Channel groups" }, WAIT);
    expect(await within(nav).findByText("Providers", undefined, WAIT)).toBeVisible();
    expect(within(nav).getAllByText("Strong IPTV")).toHaveLength(1);
    fireEvent.click(within(nav).getByRole("button", { name: "Strong IPTV" }));
    expect(screen.getByRole("heading", { name: "Strong IPTV" })).toBeVisible();
  });

  it("collapses Channel groups into a sheet on compact viewports so channel rows start above the fold", async () => {
    const originalMatchMedia = window.matchMedia;
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: query === "(max-width: 1023px)" || query === "(max-width: 639px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    try {
      backend(
        [
          iptv("c1", { number: "101", name: "Sports 1", group: "Sports", sourceId: "src1" }),
          {
            id: "v1",
            number: "1",
            name: "80s Movies",
            kind: "virtual",
            enabled: true,
            favorite: false,
            epoch: "2026-01-01T00:00:00Z",
            seed: 1,
            shuffle: false,
            items: [],
          },
        ],
        (url) => {
          if (url.endsWith("/livetv/sources")) {
            return [
              {
                id: "src1",
                name: "Strong IPTV",
                hasPlaylist: true,
                updatedAt: "2026-10-19T00:00:00Z",
              },
            ];
          }
          return undefined;
        },
      );

      renderLiveTV("/livetv");
      const list = await screen.findByRole("region", { name: "Channels" }, WAIT);
      expect(await within(list).findByRole("button", { name: "101 Sports 1" }, WAIT)).toBeVisible();
      // Inline groups rail is collapsed until requested.
      expect(screen.queryByRole("navigation", { name: "Channel groups" })).toBeNull();

      fireEvent.click(within(list).getByRole("button", { name: "Channel groups" }));
      const sheet = await screen.findByRole("dialog", { name: "Channel groups" }, WAIT);
      fireEvent.click(within(sheet).getByRole("button", { name: "Strong IPTV" }));

      await waitFor(
        () => expect(screen.queryByRole("dialog", { name: "Channel groups" })).toBeNull(),
        WAIT,
      );
      expect(within(list).getByRole("button", { name: "101 Sports 1" })).toBeVisible();
      expect(within(list).queryByRole("button", { name: "1 80s Movies" })).toBeNull();
    } finally {
      window.matchMedia = originalMatchMedia;
    }
  });

  it("marks automatic duplicate backups in Channel Studio and suppresses removed donors", async () => {
    const calls = backend([
      iptv("xtream-2113", {
        number: "2113",
        name: "FOX News",
        group: "US | News",
        sourceId: "src-a",
        streamUrl: "https://a.example/2113.ts",
        streamSources: [
          {
            id: "playlist-xtream-2113",
            name: "Imported stream",
            url: "https://a.example/2113.ts",
            enabled: true,
            priority: 0,
            authSourceId: "src-a",
          },
          {
            id: "auto-xtream-1873",
            name: "FOX News",
            url: "https://a.example/1873.ts",
            enabled: true,
            priority: 1,
            authSourceId: "src-a",
            autoFrom: "xtream-1873",
            autoNumber: "1873",
          },
        ],
      }),
    ]);

    renderLiveTV("/livetv?channel=xtream-2113");
    const section = await screen.findByRole("region", { name: "Stream sources" }, WAIT);
    expect(await within(section).findByText("Automatic · #1873", {}, WAIT)).toBeVisible();

    const removeBtn = within(section).getByRole("button", { name: "Remove source 2" });
    expect(removeBtn).toHaveTextContent("Remove");
    fireEvent.click(removeBtn);

    expect(within(section).queryByText("Automatic · #1873")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));

    await waitFor(() =>
      expect(sent(calls, "PUT", /\/livetv\/channels\/xtream-2113$/).length).toBeGreaterThan(0),
    );
    const saved = body(sent(calls, "PUT", /\/livetv\/channels\/xtream-2113$/)[0]);
    expect(saved.suppressedAutoBackups).toContain("xtream-1873");
    expect(saved.streamSources).toMatchObject([
      { id: "playlist-xtream-2113", url: "https://a.example/2113.ts", priority: 0 },
    ]);
  });
});
