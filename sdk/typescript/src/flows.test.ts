import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import {
  createHostedHostClient,
  createMockHostClient,
  createRemoteHostClient,
  createTestHarness,
  HEADER_IDEMPOTENCY_KEY,
  plugin,
  type FlowActionRequest,
  type Manifest,
} from "./index.ts";

const smartLightsManifest: Manifest = {
  id: "community.example.lights",
  name: "Smart Lights",
  version: "1.0.0",
  ether: {
    protocol: 1,
    kind: "extension",
    summary: "Controls room lighting scenes",
    runtime: { kind: "remote" },
    capabilities: ["events", "flow_actions"],
    contributes: {
      events: {
        subscribe: ["playback.session.started"],
        publish: [
          {
            key: "scene.changed",
            label: "Scene changed",
            fields: [{ name: "scene", type: "string", label: "Scene" }],
          },
        ],
      },
      flowActions: [
        {
          key: "set_scene",
          label: "Set scene",
          route: "/ether/v1/flow-actions/set_scene",
          idempotent: true,
          sideEffect: true,
        },
        {
          key: "custom_dim",
          label: "Custom dim",
          route: "/ether/custom/dim",
        },
      ],
    },
  },
};

test("flowAction route dispatch, idempotency header, and mock events.publish", async () => {
  const mockHost = createMockHostClient();
  const instance = plugin({
    manifest: smartLightsManifest,
    host: mockHost,
    routes: {
      async flowAction(req: FlowActionRequest, ctx) {
        await ctx.ether.events.publish("scene.changed", { scene: req.params.scene }, { key: "room:den" });
        return {
          output: {
            key: req.key,
            scene: req.params.scene,
            idempotencyKey: req.idempotencyKey,
            dryRun: req.dryRun,
          },
        };
      },
    },
  });

  const harness = createTestHarness(instance);
  const res = await harness.flowAction("set_scene", {
    flowId: "flow-1",
    runId: "run-1",
    nodeId: "node-1",
    idempotencyKey: "run-1:node-1:0",
    params: { scene: "cinema" },
  });
  assert.equal(res.status, 200);
  const body = res.body as { output: Record<string, unknown> };
  assert.equal(body.output.key, "set_scene");
  assert.equal(body.output.scene, "cinema");
  assert.equal(body.output.idempotencyKey, "run-1:node-1:0");
  assert.equal(mockHost.state.publishedEvents.length, 1);
  assert.equal(mockHost.state.publishedEvents[0]?.key, "scene.changed");

  // Custom declared route maps to key "custom_dim" and picks up X-Ether-Idempotency-Key header.
  const res2 = await instance.dispatch({
    method: "POST",
    path: "/ether/custom/dim",
    headers: { [HEADER_IDEMPOTENCY_KEY]: "idem-header-9" },
    body: JSON.stringify({ dryRun: true, params: { scene: "warm" } }),
  });
  assert.equal(res2.status, 200);
  const body2 = res2.body as { output: Record<string, unknown> };
  assert.equal(body2.output.key, "custom_dim");
  assert.equal(body2.output.idempotencyKey, "idem-header-9");
  assert.equal(body2.output.dryRun, true);
});

test("hosted host client forwards events.publish via __ether_host", () => {
  const g = globalThis as { __ether_host?: (name: string, argsJson: string) => string };
  const prev = g.__ether_host;
  const calls: Array<{ name: string; args: unknown }> = [];
  g.__ether_host = (name: string, argsJson: string) => {
    calls.push({ name, args: JSON.parse(argsJson) });
    return "null";
  };
  try {
    const client = createHostedHostClient();
    client.events.publish("scene.changed", { scene: "bright" }, { key: "room:den" });
    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0], {
      name: "events.publish",
      args: ["scene.changed", { scene: "bright" }, { key: "room:den" }],
    });
  } finally {
    if (prev === undefined) delete g.__ether_host;
    else g.__ether_host = prev;
  }
});

test("remote host client POSTs /ether/plugin-host/events/publish", async () => {
  let receivedPath = "";
  let receivedCred = "";
  let receivedBody: Record<string, unknown> = {};

  const server = http.createServer(async (req, res) => {
    receivedPath = req.url ?? "";
    receivedCred = String(req.headers["x-ether-plugin-credential"] ?? "");
    const parts: string[] = [];
    for await (const chunk of req) {
      parts.push(typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk as Uint8Array));
    }
    receivedBody = JSON.parse(parts.join("")) as Record<string, unknown>;
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ ok: true }));
  });

  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  try {
    const addr = server.address();
    assert.ok(addr && typeof addr === "object");
    const port = (addr as { port: number }).port;
    const host = createRemoteHostClient({
      baseUrl: `http://127.0.0.1:${port}`,
      credential: "cred-ts-1",
    });
    await host.events.publish("scene.changed", { scene: "cinema" }, { key: "room:den" });
    assert.equal(receivedPath, "/ether/plugin-host/events/publish");
    assert.equal(receivedCred, "cred-ts-1");
    assert.equal(receivedBody.key, "scene.changed");
    assert.deepEqual(receivedBody.payload, { scene: "cinema" });
    assert.deepEqual(receivedBody.correlation, { key: "room:den" });
  } finally {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
});

test("mock host client records published events", () => {
  const mock = createMockHostClient();
  mock.events.publish("scene.changed", { scene: "off" });
  assert.equal(mock.state.publishedEvents.length, 1);
  assert.equal(mock.state.publishedEvents[0]?.key, "scene.changed");
});
