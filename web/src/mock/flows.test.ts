import { describe, expect, it } from "vitest";
import {
  createFlowsMock,
  describeFlow,
  flowTemplates,
  validateFlowGraph,
  _simulateForTests,
} from "./flows";
import type { Flow, Graph, TestRunResult } from "../pages/flows/types";

const q = new URLSearchParams();

describe("flows mock", () => {
  it("serves the catalogue and templates", async () => {
    const mock = createFlowsMock();
    const events = mock.handle("GET", ["flows", "catalogue", "events"], q, null);
    expect(events?.status).toBe(200);
    expect((events?.body as { events: unknown[] }).events.length).toBeGreaterThan(20);
    expect(flowTemplates().length).toBeGreaterThanOrEqual(7);
    for (const t of flowTemplates()) expect(validateFlowGraph(t.graph).valid).toBe(true);
  });

  it("rejects a graph with no trigger and accepts the 4K recipe", () => {
    const bad: Graph = {
      schemaVersion: 1,
      nodes: [
        {
          id: "a",
          kind: "action",
          position: { x: 0, y: 0 },
          config: { actionType: "notify.send", params: {} },
        },
      ],
      edges: [],
    };
    const result = validateFlowGraph(bad);
    expect(result.valid).toBe(false);
    expect(result.errors?.some((e) => e.code === "no_trigger")).toBe(true);
  });

  it("test run walks the 4K recipe to the stop action for the sample payload", () => {
    const mock = createFlowsMock();
    const list = mock.handle("GET", ["flows"], q, null)?.body as { flows: Flow[] };
    const flow = list.flows.find((f) => f.templateId === "stop-remote-4k-transcodes")!;
    const res = mock.handle("POST", ["flows", flow.id, "test"], q, {
      dryRun: true,
      event: { type: "playback.session.started" },
    });
    const { steps } = res?.body as TestRunResult;
    const kinds = steps.map((s) => s.nodeKind);
    expect(kinds[0]).toBe("trigger.event");
    expect(kinds).toContain("condition");
    expect(kinds).toContain("action");
    const cond = steps.find((s) => s.nodeKind === "condition")!;
    expect(cond.branch).toBe("true");
    const trace = (cond.output as { trace: { rules: { matched: boolean }[] } }).trace;
    expect(trace.rules.every((r) => r.matched)).toBe(true);
  });

  it("describe turns the owner's example into a stop-transcode draft", () => {
    const draft = describeFlow("stop video transcodes for viewers away from home");
    expect(draft.graph.nodes.some((n) => n.config?.actionType === "playback.stop_session")).toBe(
      true,
    );
    expect(draft.explanation.length).toBeGreaterThan(20);
  });

  it("simulator respects a false branch", () => {
    const t = flowTemplates().find((x) => x.id === "stop-remote-4k-transcodes")!;
    const steps = _simulateForTests(
      t.graph,
      {
        stream: { videoDecision: "copy" },
        source: { videoHeight: 1080 },
        network: { location: "lan" },
        viewer: { tags: [] },
      },
      "playback.session.started",
      "run_x",
    );
    expect(steps.some((s) => s.nodeKind === "action")).toBe(false);
  });

  it("tests an unsaved draft graph via POST /flows/test without polluting runs", () => {
    const mock = createFlowsMock();
    const beforeRuns = (mock.handle("GET", ["flows", "runs"], q, null)?.body as { runs: unknown[] })
      .runs.length;
    const missing = mock.handle("POST", ["flows", "test"], q, { dryRun: true });
    expect(missing?.status).toBe(400);

    const t = flowTemplates().find((x) => x.id === "stop-remote-4k-transcodes")!;
    const res = mock.handle("POST", ["flows", "test"], q, {
      graph: t.graph,
      event: { type: "playback.session.started" },
    });
    expect(res?.status).toBe(200);
    const body = res?.body as TestRunResult;
    expect(body.run.dryRun).toBe(true);
    expect(body.run.flowId).toBe("");
    expect(body.steps.length).toBeGreaterThanOrEqual(3);
    const afterRuns = (mock.handle("GET", ["flows", "runs"], q, null)?.body as { runs: unknown[] })
      .runs.length;
    expect(afterRuns).toBe(beforeRuns);
  });

  it("returns 409 ai_unavailable from POST /flows/describe when LLM is disabled", () => {
    const mock = createFlowsMock({ llmEnabled: () => false });
    const res = mock.handle("POST", ["flows", "describe"], q, {
      prompt: "stop 4K transcodes away from home",
    });
    expect(res?.status).toBe(409);
    expect((res?.body as { error: { code: string } }).error.code).toBe("ai_unavailable");
  });

  it("atomically customizes a notification channel via POST /flows/customize-notification", () => {
    const channels = [
      {
        id: "ch-1",
        name: "Ops Discord",
        kind: "discord",
        enabled: true,
        events: ["server.crash", "playback.error", "backup.failed"],
      },
      {
        id: "ch-crash-only",
        name: "Crash only",
        kind: "discord",
        enabled: true,
        events: ["server.crash"],
      },
    ];
    const mock = createFlowsMock({ channels });

    const conflict = mock.handle("POST", ["flows", "customize-notification"], q, {
      channelId: "ch-crash-only",
    });
    expect(conflict?.status).toBe(409);
    expect((conflict?.body as { error: { code: string } }).error.code).toBe("cannot_customize");

    const ok = mock.handle("POST", ["flows", "customize-notification"], q, {
      channelId: "ch-1",
      removeLegacyRule: true,
    });
    expect(ok?.status).toBe(201);
    const body = ok?.body as { flow: Flow; migrated: string[]; kept: string[]; notes: string[] };
    expect(body.flow.id).toBeTruthy();
    expect(body.migrated).toEqual(["playback.error", "backup.failed"]);
    expect(body.kept).toEqual(["server.crash"]);
    expect(body.notes.length).toBe(1);
    expect(channels[0]!.events).toEqual(["server.crash"]);
  });
});
