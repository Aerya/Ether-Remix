import test from "node:test";
import assert from "node:assert/strict";
import { plugin, type LiveEventsRequest } from "./index.ts";

test("live feed routes dispatch events, detail, entities, and standings", async () => {
  let lastEvents: LiveEventsRequest | undefined;
  const p = plugin({
    manifest: {
      id: "community.acme.live",
      version: "1.0.0",
      name: "Live feed",
      ether: {
        protocol: 1,
        kind: "extension",
        capabilities: ["live_events"],
        summary: "Live feed test",
        runtime: { kind: "hosted", entry: "index.ts" },
        contributes: {
          liveFeeds: [{ id: "main", title: "Main feed", minIntervalSec: 15, entityKinds: ["team"] }],
        },
      },
    },
    routes: {},
  });

  p.onLiveFeed("main", {
    events(req) {
      lastEvents = req;
      return {
        events: [
          {
            id: "e1",
            feed: req.feed,
            league: { id: "l1", name: "League One" },
            start: "2026-11-22T18:00:00Z",
            status: { state: "in", detail: "P2", period: 2, clock: "08:00", close: true },
          },
        ],
        nextPollSec: 15,
      };
    },
    eventDetail(req) {
      return {
        id: req.id,
        feed: req.feed,
        league: { id: "l1", name: "League One" },
        start: "2026-11-22T18:00:00Z",
        status: { state: "in" },
        tables: [{ title: "Line", columns: ["Side", "T"], rows: [["A", "1"]] }],
      };
    },
    entities(req) {
      return {
        entities: [{ id: "t1", kind: req.kind ?? "team", name: "Alpha", league: req.league }],
      };
    },
    standings() {
      return {
        tables: [{ title: "Standings", columns: ["Team", "W"], rows: [["Alpha", "10"]] }],
      };
    },
  });

  const ev = await p.dispatch({
    method: "GET",
    path: "/ether/v1/live/main/events",
    query: { from: "2026-11-22T00:00:00Z", league: "l1", ids: "e1, e2" },
  });
  assert.equal(ev.status, 200);
  assert.deepEqual(lastEvents, {
    feed: "main",
    from: "2026-11-22T00:00:00Z",
    to: undefined,
    league: "l1",
    ids: ["e1", "e2"],
  });

  const detail = await p.dispatch({ method: "GET", path: "/ether/v1/live/main/events/e1" });
  assert.equal(detail.status, 200);
  assert.equal((detail.body as { id: string }).id, "e1");

  const entities = await p.dispatch({ method: "GET", path: "/ether/v1/live/main/entities?kind=team&league=l1&q=alp" });
  assert.equal(entities.status, 200);
  assert.equal((entities.body as { entities: unknown[] }).entities.length, 1);

  const standings = await p.dispatch({ method: "GET", path: "/ether/v1/live/main/standings?league=l1" });
  assert.equal(standings.status, 200);
  assert.equal((standings.body as { tables: unknown[] }).tables.length, 1);

  const missing = await p.dispatch({ method: "GET", path: "/ether/v1/live/unknown/events" });
  assert.equal(missing.status, 404);
});
