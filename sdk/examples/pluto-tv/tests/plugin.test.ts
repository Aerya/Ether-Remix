import { test } from "node:test";
import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { createMockHostClient } from "../../../typescript/src/index.ts";
import { createPlugin } from "../plugin.ts";
const channels = readFileSync(
  new URL("./fixtures/channels.json", import.meta.url),
  "utf8",
);
const boot = readFileSync(
  new URL("./fixtures/boot.json", import.meta.url),
  "utf8",
);
const guide = readFileSync(
  new URL("./fixtures/guide.json", import.meta.url),
  "utf8",
);
test("maps recorded channels, guide, and streams without network", async () => {
  const host = createMockHostClient({
    httpHandler: (r) => ({
      status: 200,
      headers: {},
      body: r.url.includes("boot.pluto.tv")
        ? boot
        : r.url.includes("channels.json")
          ? channels
          : guide,
    }),
  });
  const p = createPlugin(host);
  const list = (await p.dispatch({ path: "/ether/channels.json" })).body as any;
  assert.ok(list.channels[0].inputs[0].url.startsWith("plugin://"));
  assert.equal(list.channels[0].number, 1030);
  assert.equal(list.channels[0].group, "News + Opinion");
  const id = list.channels[0].id;
  const g = (
    await p.dispatch({
      path: `/ether/guide/${id}.json`,
      query: { from: "2026-11-02T00:00:00Z", to: "2026-11-03T00:00:00Z" },
    })
  ).body as any;
  assert.equal(g.programmes[0].title, "Bloomberg TV+");
  const s = (await p.dispatch({ path: `/stream/tv/${id}.json` })).body as any;
  assert.ok(s.streams[0].url.includes(id));
  assert.ok(s.streams[0].url.includes("fixture-token"));
});

test("fetches small channel list first, slices 26h guide window, and skips Testing channels", async () => {
  const base = JSON.parse(channels);
  const lineupOnly = [
    base[0],
    {
      ...base[0],
      _id: "testing-chan",
      name: "Deal Zone",
      category: "Testing",
    },
  ];
  const withTimelines = [
    {
      ...base[0],
      timelines: [
        {
          _id: "tl-1",
          start: "2026-11-02T00:00:00Z",
          stop: "2026-11-02T01:00:00Z",
          title: "Market Open",
          episode: { description: "Live market coverage" },
        },
      ],
    },
  ];
  const urls: string[] = [];
  const host = createMockHostClient({
    httpHandler: (r) => {
      urls.push(r.url);
      const hasWindow = r.url.includes("start=") && r.url.includes("stop=");
      return {
        status: 200,
        headers: {},
        body: JSON.stringify(hasWindow ? withTimelines : lineupOnly),
      };
    },
  });
  const p = createPlugin(host);
  const list = (await p.dispatch({ path: "/ether/channels.json" })).body as any;
  assert.equal(list.channels.length, 1);
  assert.ok(!urls[0].includes("start=") && !urls[0].includes("stop="));
  assert.equal(urls.length, 8);
  const sliceWindows = urls.slice(1).map((raw) => {
    const u = new URL(raw);
    return {
      start: Date.parse(u.searchParams.get("start") || ""),
      stop: Date.parse(u.searchParams.get("stop") || ""),
    };
  });
  for (let i = 0; i < sliceWindows.length; i++) {
    const spanHours = (sliceWindows[i].stop - sliceWindows[i].start) / 3600000;
    assert.equal(spanHours, i < 6 ? 4 : 2);
    if (i > 0) {
      assert.equal(sliceWindows[i].start, sliceWindows[i - 1].stop);
    }
  }
  assert.equal(
    (sliceWindows[sliceWindows.length - 1].stop - sliceWindows[0].start) /
      3600000,
    26,
  );
  assert.equal(list.channels[0].programmes.length, 1);
  assert.equal(list.channels[0].programmes[0].title, "Market Open");
});

test("splits oversized guide slices down to 1h, merges deduplicated timelines sorted by start, and survives failed slices", async () => {
  const base = JSON.parse(channels);
  const chanId = base[0]._id as string;
  const requestedSpansHours: number[] = [];
  let windowOriginMs: number | undefined;

  const host = createMockHostClient({
    httpHandler: (r) => {
      const u = new URL(r.url);
      const startParam = u.searchParams.get("start");
      const stopParam = u.searchParams.get("stop");
      if (!startParam || !stopParam) {
        return {
          status: 200,
          headers: {},
          body: JSON.stringify(base),
        };
      }
      const startMs = Date.parse(startParam);
      const stopMs = Date.parse(stopParam);
      windowOriginMs ??= startMs;
      const offsetHours = Math.round((startMs - windowOriginMs) / 3600000);
      const spanHours = Math.round((stopMs - startMs) / 3600000);
      requestedSpansHours.push(spanHours);

      // First 4h window [0, 4h): 4h fails as too large, [0, 2h) also fails as too large
      // so it splits to [0, 1h) + [1h, 2h), while [2h, 4h) succeeds at 2h.
      if (offsetHours === 0 && spanHours > 1) {
        throw new Error(
          "__ETHER_FAULT__" +
            JSON.stringify({
              code: "host_error",
              message: "plugins: response larger than 10485760 bytes",
            }),
        );
      }

      // Second 4h window [4h, 8h): always exceeds the cap even at 1h;
      // must stop splitting at 1h and not fail the route.
      if (offsetHours >= 4 && offsetHours < 8) {
        throw new Error("plugins: response larger than 10485760 bytes");
      }

      // Third 4h window [8h, 12h): upstream HTTP 503; must not fail the route.
      if (offsetHours === 8) {
        return { status: 503, headers: {}, body: "unavailable" };
      }

      const timelines =
        offsetHours === 0 && spanHours === 1
          ? [
              {
                _id: "tl-boundary",
                start: new Date(windowOriginMs + 45 * 60000).toISOString(),
                stop: new Date(windowOriginMs + 75 * 60000).toISOString(),
                title: "Boundary Show",
              },
              {
                _id: "tl-earliest",
                start: new Date(windowOriginMs).toISOString(),
                stop: new Date(windowOriginMs + 45 * 60000).toISOString(),
                title: "Opening Bell",
              },
            ]
          : offsetHours === 1 && spanHours === 1
            ? [
                {
                  _id: "tl-boundary",
                  start: new Date(windowOriginMs + 45 * 60000).toISOString(),
                  stop: new Date(windowOriginMs + 75 * 60000).toISOString(),
                  title: "Boundary Show",
                },
                {
                  _id: "tl-second-hour",
                  start: new Date(windowOriginMs + 75 * 60000).toISOString(),
                  stop: new Date(windowOriginMs + 120 * 60000).toISOString(),
                  title: "Midday Brief",
                },
              ]
            : offsetHours === 2 && spanHours === 2
              ? [
                  {
                    _id: "tl-two-hour",
                    start: new Date(windowOriginMs + 120 * 60000).toISOString(),
                    stop: new Date(windowOriginMs + 240 * 60000).toISOString(),
                    title: "Afternoon Markets",
                  },
                ]
              : offsetHours === 12
                ? [
                    {
                      _id: "tl-later",
                      start: new Date(
                        windowOriginMs + 12 * 3600000,
                      ).toISOString(),
                      stop: new Date(
                        windowOriginMs + 13 * 3600000,
                      ).toISOString(),
                      title: "Late Edition",
                    },
                  ]
                : [];

      return {
        status: 200,
        headers: {},
        body: JSON.stringify([{ ...base[0], _id: chanId, timelines }]),
      };
    },
  });

  const p = createPlugin(host);
  const res = await p.dispatch({ path: "/ether/channels.json" });
  assert.equal(res.status, 200);
  const list = res.body as any;
  assert.equal(list.channels.length, 1);

  // Never splits below 1h, and did exercise 4h -> 2h -> 1h halving.
  assert.ok(requestedSpansHours.every((h) => h >= 1));
  assert.ok(requestedSpansHours.includes(4));
  assert.ok(requestedSpansHours.includes(2));
  assert.ok(requestedSpansHours.includes(1));

  const progs = list.channels[0].programmes;
  assert.deepEqual(
    progs.map((x: any) => x.id),
    ["tl-earliest", "tl-boundary", "tl-second-hour", "tl-two-hour", "tl-later"],
  );
  for (let i = 1; i < progs.length; i++) {
    assert.ok(Date.parse(progs[i - 1].start) <= Date.parse(progs[i].start));
  }
});
