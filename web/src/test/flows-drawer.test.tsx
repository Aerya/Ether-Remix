import { ConfirmRoot } from "@braedonsaunders/appkit-ui";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  DocumentTitleProvider,
  useDocumentBrand,
  useRouteDocumentTitle,
} from "../lib/document-title";
import Flows from "../pages/flows/Flows";
import type { Flow, FlowSummary, Graph } from "../pages/flows/types";

afterEach(() => {
  vi.unstubAllGlobals();
  document.title = "";
});

const SAMPLE_GRAPH: Graph = {
  schemaVersion: 1,
  nodes: [
    {
      id: "t1",
      kind: "trigger.event",
      label: "Backup failed",
      position: { x: 60, y: 120 },
      config: { events: ["system.backup.failed"] },
    },
    {
      id: "a1",
      kind: "action",
      label: "Send notification",
      position: { x: 360, y: 120 },
      config: {
        actionType: "notify.send",
        params: { channelIds: ["*"], title: "Failed", message: "Oops" },
      },
    },
  ],
  edges: [{ id: "e1", source: "t1", sourceHandle: "next", target: "a1" }],
};

function stubFlowsApi(initialFlows: Flow[] = []) {
  const store = new Map<string, Flow>(initialFlows.map((f) => [f.id, f]));
  const created: Flow[] = [];
  let seq = 1;

  const toSummary = (f: Flow): FlowSummary => ({
    id: f.id,
    name: f.name,
    description: f.description,
    enabled: f.enabled,
    version: f.version,
    trigger: { kinds: ["trigger.event"], events: ["system.backup.failed"] },
    runs24h: 0,
    failures24h: 0,
    activeRuns: 0,
    createdAt: f.createdAt,
    updatedAt: f.updatedAt,
  });

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      if (url.includes("/flows/catalogue/events")) {
        return new Response(
          JSON.stringify({
            events: [
              {
                type: "system.backup.failed",
                domain: "system",
                label: "Backup failed",
                fields: [],
              },
            ],
          }),
          { status: 200 },
        );
      }
      if (url.includes("/flows/catalogue/actions")) {
        return new Response(
          JSON.stringify({
            actions: [
              {
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
                  {
                    key: "message",
                    type: "string",
                    label: "Message",
                    required: true,
                    template: true,
                  },
                ],
              },
            ],
          }),
          { status: 200 },
        );
      }
      if (url.includes("/flows/validate")) {
        return new Response(JSON.stringify({ valid: true, errors: [], warnings: [] }), {
          status: 200,
        });
      }
      if (url.includes("/flows/stats")) {
        return new Response(JSON.stringify({ range: "7d", stats: null }), { status: 200 });
      }
      if (url.includes("/notifications/")) {
        return new Response(JSON.stringify({ channels: [], deliveries: [] }), { status: 200 });
      }
      if (url.includes("/flows/test") || /\/flows\/[^/]+\/test/.test(url)) {
        return new Response(
          JSON.stringify({
            run: {
              id: "run_test",
              flowId: "draft",
              triggerKind: "trigger.event",
              eventType: "system.backup.failed",
              status: "completed",
              dryRun: true,
              stepCount: 1,
              startedAt: "2026-11-22T10:00:00Z",
              finishedAt: "2026-11-22T10:00:01Z",
            },
            steps: [],
          }),
          { status: 200 },
        );
      }
      if (method === "POST" && /\/flows$/.test(url)) {
        const body = JSON.parse(String(init?.body ?? "{}"));
        const saved: Flow = {
          id: `flow_created_${seq++}`,
          name: body.name,
          description: body.description,
          enabled: body.enabled ?? true,
          version: 1,
          graph: body.graph,
          limits: body.limits,
          createdAt: "2026-11-22T10:00:00Z",
          updatedAt: "2026-11-22T10:00:00Z",
        };
        store.set(saved.id, saved);
        created.push(saved);
        return new Response(JSON.stringify({ flow: saved }), { status: 201 });
      }
      const single = url.match(/\/flows\/([^/?]+)$/);
      if (single && method === "GET") {
        const flow = store.get(decodeURIComponent(single[1]!));
        if (!flow) return new Response(JSON.stringify({ error: "not found" }), { status: 404 });
        return new Response(JSON.stringify({ flow }), { status: 200 });
      }
      if (single && method === "PUT") {
        const id = decodeURIComponent(single[1]!);
        const body = JSON.parse(String(init?.body ?? "{}"));
        const prev = store.get(id);
        const updated: Flow = {
          ...(prev ?? { id, version: 1, createdAt: "2026-11-22T10:00:00Z" }),
          ...body,
          id,
          updatedAt: "2026-11-22T10:01:00Z",
        };
        store.set(id, updated);
        return new Response(JSON.stringify({ flow: updated }), { status: 200 });
      }
      if (/\/flows(\?.*)?$/.test(url) && method === "GET") {
        return new Response(JSON.stringify({ flows: [...store.values()].map(toSummary) }), {
          status: 200,
        });
      }
      return new Response("{}", { status: 200 });
    }),
  );

  return { store, created };
}

function LocationProbe() {
  const loc = useLocation();
  useDocumentBrand("Home");
  useRouteDocumentTitle({ matched: true, parts: ["Flows"] });
  return <output data-testid="location">{loc.pathname}</output>;
}

function renderFlows(initialEntry: string | { pathname: string; state?: unknown }) {
  return render(
    <DocumentTitleProvider>
      <MemoryRouter initialEntries={[initialEntry]}>
        <LocationProbe />
        <Routes>
          <Route path="/flows/*" element={<Flows />} />
        </Routes>
        <ConfirmRoot />
      </MemoryRouter>
    </DocumentTitleProvider>,
  );
}

describe("Flow editor drawer", () => {
  it("opens New flow as a drawer over the Flows list, asks before discarding unsaved changes, and supports Esc", async () => {
    stubFlowsApi([
      {
        id: "flow_existing",
        name: "Backup alerts",
        enabled: true,
        version: 1,
        graph: SAMPLE_GRAPH,
        createdAt: "2026-11-22T10:00:00Z",
        updatedAt: "2026-11-22T10:00:00Z",
      },
    ]);

    renderFlows("/flows/list");
    expect(await screen.findByRole("button", { name: "Backup alerts" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "New flow" }));

    const dialog = await screen.findByRole("dialog", { name: "New flow" });
    expect(dialog).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("/flows/list/new");
    expect(document.title).toBe("Flows · New flow — Home");
    // The Flows list stays mounted underneath (hidden/inert).
    expect(screen.getByText("Backup alerts")).toBeInTheDocument();

    // Closing untouched closes immediately without confirmation.
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByTestId("location")).toHaveTextContent("/flows/list");
    expect(document.title).toBe("Flows — Home");

    // Reopen, edit name, then try to close via Esc -> unsaved-changes prompt.
    fireEvent.click(screen.getByRole("button", { name: "New flow" }));
    const nameInput = await screen.findByLabelText("Flow name");
    fireEvent.change(nameInput, { target: { value: "Nightly check" } });
    expect(document.title).toBe("Flows · Nightly check — Home");

    fireEvent.keyDown(document, { key: "Escape" });
    expect(await screen.findByText("Discard unsaved changes?")).toBeInTheDocument();

    // Pressing Escape cancels only the confirmation dialog; the editor drawer stays open.
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByText("Discard unsaved changes?")).toBeNull());
    expect(screen.getByRole("dialog", { name: "Nightly check" })).toBeInTheDocument();
    expect(screen.getByLabelText("Flow name")).toHaveValue("Nightly check");

    // Click Close -> Confirm Discard -> returns to Flows list.
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(await screen.findByText("Discard unsaved changes?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByTestId("location")).toHaveTextContent("/flows/list");
  });

  it("switches the URL to the new flow id on first save without closing the drawer", async () => {
    const { created } = stubFlowsApi();
    renderFlows("/flows/list/new");

    const dialog = await screen.findByRole("dialog", { name: "New flow" });
    const nameInput = await screen.findByLabelText("Flow name");
    fireEvent.change(nameInput, { target: { value: "First saved flow" } });
    fireEvent.click(screen.getByRole("button", { name: "Save flow" }));

    await waitFor(() => expect(created).toHaveLength(1));
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(`/flows/list/${created[0]!.id}`),
    );
    // The same dialog element stayed mounted without closing.
    expect(screen.getByRole("dialog", { name: "First saved flow" })).toBe(dialog);
    expect(document.title).toBe("Flows · First saved flow — Home");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("opens Describe-it drafts pre-filled, stacks Test run drawer cleanly, and handles node deletion shortcuts", async () => {
    stubFlowsApi();
    renderFlows({
      pathname: "/flows/list/new",
      state: {
        draft: {
          name: "Backup alert draft",
          description: "Alert when backups fail",
          graph: SAMPLE_GRAPH,
        },
      },
    });

    expect(await screen.findByRole("dialog", { name: "Backup alert draft" })).toBeInTheDocument();
    expect(await screen.findByLabelText("Flow name")).toHaveValue("Backup alert draft");
    expect(document.title).toBe("Flows · Backup alert draft — Home");
    expect(screen.getByLabelText("Backup failed")).toBeInTheDocument();
    expect(screen.getByLabelText("Send notification")).toBeInTheDocument();

    // Test run drawer opens nested above the editor drawer; Esc closes only Test run.
    fireEvent.click(screen.getByRole("button", { name: "Test run" }));
    expect(await screen.findByRole("dialog", { name: "Test run" })).toBeInTheDocument();
    expect(document.querySelector('[data-drawer-layer="nested"]')).not.toBeNull();

    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Test run" })).toBeNull());
    // Editor drawer stays open and no discard prompt was triggered.
    expect(screen.getByRole("dialog", { name: "Backup alert draft" })).toBeInTheDocument();
    expect(screen.queryByText("Discard unsaved changes?")).toBeNull();

    // Select a node -> Node inspector opens; Delete removes the selected node.
    const actionNode = screen.getByLabelText("Send notification");
    fireEvent.pointerDown(actionNode);
    expect(screen.getByLabelText("Step settings")).toHaveTextContent("Send notification");
    fireEvent.keyDown(window, { key: "Delete" });
    await waitFor(() => expect(screen.queryByLabelText("Send notification")).toBeNull());
  });

  it("deep-links to editing an existing flow in the drawer and removes standalone /flows/new and /flows/edit routes", async () => {
    stubFlowsApi([
      {
        id: "flow_alpha",
        name: "Stop 4K transcodes",
        enabled: true,
        version: 1,
        graph: SAMPLE_GRAPH,
        createdAt: "2026-11-22T10:00:00Z",
        updatedAt: "2026-11-22T10:00:00Z",
      },
    ]);

    const { unmount } = renderFlows("/flows/list/flow_alpha");
    expect(await screen.findByRole("dialog", { name: "Stop 4K transcodes" })).toBeInTheDocument();
    expect(document.title).toBe("Flows · Stop 4K transcodes — Home");
    unmount();

    const oldNew = renderFlows("/flows/new");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/flows"));
    expect(screen.queryByRole("dialog")).toBeNull();
    oldNew.unmount();

    const oldEdit = renderFlows("/flows/edit/flow_alpha");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/flows"));
    expect(screen.queryByRole("dialog")).toBeNull();
    oldEdit.unmount();
  });
});
