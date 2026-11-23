import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { UseTemplateDrawer } from "../pages/flows/Flows";
import { NodeInspector } from "../pages/flows/NodeInspector";
import type { Catalogue } from "../pages/flows/shared";
import {
  FLOW_PICKER,
  type ActionSpec,
  type FlowNode,
  type FlowTemplate,
} from "../pages/flows/types";

afterEach(() => {
  vi.unstubAllGlobals();
});

const TEMPLATE: FlowTemplate = {
  id: "notify-critical-alerts",
  name: "Critical server alerts",
  description: "Notify immediately on critical failures.",
  category: "notifications",
  params: [
    {
      key: "channelIds",
      type: "string_list",
      label: "Notification channels",
      default: ["*"],
      picker: FLOW_PICKER.channel,
    },
  ],
  graph: {
    schemaVersion: 1,
    nodes: [],
    edges: [],
  },
};

function stubFetch(
  channels = [
    {
      id: "ch_discord",
      name: "Ops Discord",
      kind: "discord",
      enabled: true,
      events: [],
      perMinute: 5,
      digestMinutes: 0,
    },
  ],
) {
  const createdBodies: unknown[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/notifications/")) {
      return new Response(JSON.stringify({ channels, deliveries: [] }), { status: 200 });
    }
    if (url.includes("/flows/from-template/")) {
      createdBodies.push(init?.body ? JSON.parse(String(init.body)) : null);
      return new Response(
        JSON.stringify({
          flow: {
            id: "flow_1",
            name: TEMPLATE.name,
            enabled: true,
            version: 1,
            graph: TEMPLATE.graph,
          },
        }),
        { status: 201 },
      );
    }
    return new Response("{}", { status: 200 });
  });
  vi.stubGlobal("fetch", fn);
  return { fn, createdBodies };
}

describe("Flows channel picker", () => {
  it("renders the channel picker in UseTemplateDrawer for canonical picker 'channel' and defaults empty selection to ['*']", async () => {
    const { createdBodies } = stubFetch();
    const onClose = vi.fn();

    render(
      <MemoryRouter>
        <UseTemplateDrawer template={TEMPLATE} onClose={onClose} />
      </MemoryRouter>,
    );

    expect(await screen.findByRole("button", { name: "Ops Discord" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Create & open in editor" }));

    await waitFor(() => expect(createdBodies).toHaveLength(1));
    expect(createdBodies[0]).toMatchObject({
      name: "Critical server alerts",
      enabled: true,
      params: { channelIds: ["*"] },
    });
    expect(onClose).toHaveBeenCalled();
  });

  it("submits selected channel IDs from UseTemplateDrawer", async () => {
    const { createdBodies } = stubFetch();

    render(
      <MemoryRouter>
        <UseTemplateDrawer template={TEMPLATE} onClose={vi.fn()} />
      </MemoryRouter>,
    );

    const chBtn = await screen.findByRole("button", { name: "Ops Discord" });
    fireEvent.click(chBtn);
    expect(chBtn).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(screen.getByRole("button", { name: "Create & open in editor" }));
    await waitFor(() => expect(createdBodies).toHaveLength(1));
    expect(createdBodies[0]).toMatchObject({
      params: { channelIds: ["ch_discord"] },
    });
  });

  it("renders the channel picker in NodeInspector for notify.send and strips wildcard '*' on toggle", async () => {
    stubFetch();
    const notifySpec: ActionSpec = {
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
          picker: FLOW_PICKER.channel,
        },
      ],
    };
    const catalogue: Catalogue = {
      events: [],
      actions: [notifySpec],
      eventsByType: new Map(),
      actionsByType: new Map([[notifySpec.type, notifySpec]]),
    };
    const node: FlowNode = {
      id: "notify_1",
      kind: "action",
      label: "Send alert",
      position: { x: 0, y: 0 },
      config: {
        actionType: "notify.send",
        params: { channelIds: ["*"] },
      },
    };
    const onChange = vi.fn();

    const { rerender } = render(
      <NodeInspector node={node} onChange={onChange} catalogue={catalogue} fieldGroups={[]} />,
    );

    const chBtn = await screen.findByRole("button", { name: "Ops Discord" });
    expect(chBtn).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(chBtn);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        config: expect.objectContaining({
          params: { channelIds: ["ch_discord"] },
        }),
      }),
    );

    onChange.mockClear();
    const selectedNode: FlowNode = {
      ...node,
      config: { actionType: "notify.send", params: { channelIds: ["ch_discord"] } },
    };
    rerender(
      <NodeInspector
        node={selectedNode}
        onChange={onChange}
        catalogue={catalogue}
        fieldGroups={[]}
      />,
    );
    const pressed = screen.getByRole("button", { name: "Ops Discord" });
    expect(pressed).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(pressed);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        config: expect.objectContaining({
          params: { channelIds: ["*"] },
        }),
      }),
    );
  });
});
