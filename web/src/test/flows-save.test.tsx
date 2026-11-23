import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import Flows, { UseTemplateDrawer } from "../pages/flows/Flows";
import { NodeInspector } from "../pages/flows/NodeInspector";
import {
  fillTemplateDefaults,
  initialActionParams,
  knownParams,
  pruneUnknownParams,
  validationFromError,
} from "../pages/flows/params";
import type { Catalogue } from "../pages/flows/shared";
import {
  FLOW_PICKER,
  type ActionSpec,
  type FlowNode,
  type FlowTemplate,
  type Graph,
} from "../pages/flows/types";

afterEach(() => {
  vi.unstubAllGlobals();
});

const UNPIN: ActionSpec = {
  type: "release.unpin",
  owner: "ether",
  group: "releases",
  label: "Unpin release",
  params: [{ key: "titleKey", type: "string", label: "Title key", required: true, template: true }],
};

const NOTIFY: ActionSpec = {
  type: "notify.send",
  owner: "ether",
  group: "notifications",
  label: "Send notification",
  params: [
    {
      key: "channelIds",
      type: "string_list",
      label: "Channels",
      required: true,
      picker: "channel",
    },
    { key: "title", type: "string", label: "Title", required: true, template: true },
    { key: "message", type: "string", label: "Message", required: true, template: true },
    { key: "fields", type: "object", label: "Embed fields", template: true },
    { key: "priority", type: "int", label: "Priority", default: 0 },
  ],
};

const STOP: ActionSpec = {
  type: "playback.stop_session",
  owner: "ether",
  group: "playback",
  label: "Stop playback session",
  params: [
    { key: "excludeViewerTags", type: "string_list", label: "Skip viewers tagged", template: true },
    { key: "minPausedMs", type: "duration_ms", label: "Paused at least" },
  ],
};

function catalogueOf(...actions: ActionSpec[]): Catalogue {
  return {
    events: [],
    actions,
    eventsByType: new Map(),
    actionsByType: new Map(actions.map((a) => [a.type, a])),
  };
}

describe("action params", () => {
  // Adding "Unpin release" used to seed notify.send's channelIds/title/message,
  // which the server rejects as unknown_param and the inspector never shows.
  it("seeds only params the action accepts", () => {
    expect(initialActionParams(UNPIN, "release.unpin")).toEqual({});
    expect(initialActionParams(NOTIFY, "notify.send")).toMatchObject({
      channelIds: ["*"],
      priority: 0,
    });
    expect(Object.keys(initialActionParams(NOTIFY, "notify.send"))).not.toContain("fields");
  });

  it("drops params from another action when the type changes", () => {
    expect(knownParams(UNPIN, { titleKey: "movie:1", channelIds: ["*"], title: "x" })).toEqual({
      titleKey: "movie:1",
    });
  });

  it("prunes hidden unknown params before saving, and leaves clean graphs alone", () => {
    const graph: Graph = {
      schemaVersion: 1,
      nodes: [
        {
          id: "a",
          kind: "action",
          position: { x: 0, y: 0 },
          config: { actionType: "release.unpin", params: { titleKey: "k", message: "m" } },
        },
      ],
      edges: [],
    };
    const cat = catalogueOf(UNPIN);
    const pruned = pruneUnknownParams(graph, cat.actionsByType);
    expect(pruned.nodes[0]!.config).toEqual({
      actionType: "release.unpin",
      params: { titleKey: "k" },
    });
    expect(pruneUnknownParams(pruned, cat.actionsByType)).toBe(pruned);
  });

  it("fills gallery placeholders for Preview on canvas", () => {
    const tpl: FlowTemplate = {
      id: "lights",
      name: "Lights",
      description: "",
      category: "smart-home",
      params: [
        { key: "brokerId", type: "string", label: "Broker", required: true, picker: "channel" },
        { key: "topic", type: "string", label: "Topic", default: "home/lights" },
      ],
      graph: {
        schemaVersion: 1,
        nodes: [
          {
            id: "a",
            kind: "action",
            position: { x: 0, y: 0 },
            config: {
              actionType: "mqtt.publish",
              params: {
                brokerId: "{{ param.brokerId }}",
                topic: "{{ param.topic }}",
                payload: '{"room":"{{param.topic}}"}',
              },
            },
          },
        ],
        edges: [],
      },
    };
    expect(fillTemplateDefaults(tpl).nodes[0]!.config).toEqual({
      actionType: "mqtt.publish",
      params: { brokerId: "", topic: "home/lights", payload: '{"room":"home/lights"}' },
    });
  });

  it("reads the validation report from a 400 save", () => {
    const err = new ApiError(400, "validation_failed", "flow definition is invalid");
    err.body = {
      validation: { valid: false, errors: [{ code: "x", message: "broken", nodeId: "a" }] },
    };
    expect(validationFromError(err)?.errors?.[0]?.message).toBe("broken");
    expect(validationFromError(new ApiError(500, "store_error", "nope"))).toBeNull();
  });
});

function stubFetch(routes: Record<string, unknown>) {
  const calls: { url: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      const key = Object.keys(routes).find((k) => url.includes(k));
      return new Response(JSON.stringify(key ? routes[key] : {}), {
        status: key && url.includes("from-template") ? 201 : 200,
      });
    }),
  );
  return calls;
}

describe("inspector param types", () => {
  const node = (actionType: string, params: Record<string, unknown>): FlowNode => ({
    id: "n1",
    kind: "action",
    position: { x: 0, y: 0 },
    config: { actionType, params },
  });

  // Typing into "Embed fields" used to save a string: "expects an object".
  it("edits object params as name/value rows", async () => {
    stubFetch({ "/notifications/": { channels: [] } });
    const onChange = vi.fn();
    render(
      <NodeInspector
        node={node("notify.send", { channelIds: ["*"], title: "t", message: "m" })}
        onChange={onChange}
        catalogue={catalogueOf(NOTIFY)}
        fieldGroups={[]}
      />,
    );
    fireEvent.change(screen.getByLabelText("Embed fields name 1"), {
      target: { value: "Viewer" },
    });
    const last = onChange.mock.calls.at(-1)![0] as FlowNode;
    expect((last.config as { params: Record<string, unknown> }).params.fields).toEqual({
      Viewer: "",
    });
  });

  it("edits string lists as lists and durations as milliseconds", () => {
    stubFetch({ "/notifications/": { channels: [] } });
    const onChange = vi.fn();
    render(
      <NodeInspector
        node={node("playback.stop_session", {})}
        onChange={onChange}
        catalogue={catalogueOf(STOP)}
        fieldGroups={[]}
      />,
    );
    fireEvent.change(screen.getByLabelText("Skip viewers tagged"), {
      target: { value: "vip, family" },
    });
    let params = (onChange.mock.calls.at(-1)![0] as FlowNode).config as {
      params: Record<string, unknown>;
    };
    expect(params.params.excludeViewerTags).toEqual(["vip", "family"]);
    fireEvent.change(screen.getByLabelText("Paused at least"), { target: { value: "90" } });
    params = (onChange.mock.calls.at(-1)![0] as FlowNode).config as {
      params: Record<string, unknown>;
    };
    expect(params.params.minPausedMs).toBe(90_000);
  });
});

describe("Use template drawer", () => {
  // The MQTT lighting template needs a broker; the drawer only knew channel lists.
  it("asks for a single broker channel and sends it", async () => {
    const calls = stubFetch({
      "/notifications/": {
        channels: [
          { id: "ch_mail", name: "Email", kind: "email" },
          { id: "ch_mqtt", name: "Home Assistant", kind: "mqtt" },
        ],
      },
      "/flows/from-template/": { flow: { id: "flow_1", name: "Lights", graph: {} } },
    });
    const tpl: FlowTemplate = {
      id: "home-assistant-cinema-lighting",
      name: "Lights",
      description: "",
      category: "smart-home",
      params: [
        {
          key: "brokerId",
          type: "string",
          label: "MQTT broker",
          required: true,
          picker: FLOW_PICKER.channel,
        },
        { key: "topic", type: "string", label: "MQTT topic", default: "home/theater" },
      ],
      graph: { schemaVersion: 1, nodes: [], edges: [] },
    };
    render(
      <MemoryRouter>
        <UseTemplateDrawer template={tpl} onClose={vi.fn()} />
      </MemoryRouter>,
    );
    const create = screen.getByRole("button", { name: "Create & open in editor" });
    expect(create).toBeDisabled();
    // AppKit's Select keeps a native <select> under its listbox button.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "MQTT broker" })).not.toBeDisabled(),
    );
    const broker = document.querySelector("select") as HTMLSelectElement;
    expect([...broker.options].map((o) => o.textContent)).toEqual(["Choose…", "Home Assistant"]);
    fireEvent.change(broker, { target: { value: "ch_mqtt" } });
    fireEvent.click(create);
    await waitFor(() => expect(calls.some((c) => c.url.includes("from-template"))).toBe(true));
    const body = calls.find((c) => c.url.includes("from-template"))!.body as {
      params: Record<string, unknown>;
    };
    expect(body.params).toEqual({ brokerId: "ch_mqtt", topic: "home/theater" });
  });
});

describe("Flows table", () => {
  // GET /api/flows returns summaries without graphs; reading flow.graph
  // blanked the whole page once any flow existed.
  it("renders list rows that carry no graph", async () => {
    stubFetch({
      "/flows/catalogue/events": {
        events: [{ type: "playback.session.started", label: "Playback started", fields: [] }],
      },
      "/flows/catalogue/actions": { actions: [] },
      "/flows/stats": { range: "7d", stats: null },
      "/flows": {
        flows: [
          {
            id: "flow_a",
            name: "Lights on pause",
            enabled: true,
            version: 1,
            trigger: {
              kinds: ["trigger.event", "trigger.manual"],
              events: ["playback.session.started"],
            },
            runs24h: 3,
            failures24h: 0,
            activeRuns: 0,
            createdAt: "2026-11-22T10:00:00Z",
            updatedAt: "2026-11-22T10:00:00Z",
          },
        ],
      },
    });
    render(
      <MemoryRouter initialEntries={["/flows/list"]}>
        <Routes>
          <Route path="/flows/*" element={<Flows />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(await screen.findByRole("button", { name: "Lights on pause" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Run now/ })).toBeInTheDocument();
    expect(
      (await screen.findAllByText(/Playback started · Manual trigger/)).length,
    ).toBeGreaterThan(0);
  });
});
