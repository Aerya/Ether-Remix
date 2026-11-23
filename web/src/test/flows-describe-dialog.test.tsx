import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DescribeFlowDialog } from "../pages/flows/DescribeFlowDialog";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("DescribeFlowDialog", () => {
  it("shows Configure AI in Integrations with a link on 409 ai_unavailable", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              error: {
                code: "ai_unavailable",
                message: "Configure AI in Integrations to describe flows in plain language.",
              },
            }),
            { status: 409 },
          ),
      ),
    );

    render(
      <MemoryRouter>
        <DescribeFlowDialog open onClose={vi.fn()} onDraft={vi.fn()} />
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByLabelText("What should happen?"), {
      target: { value: "Notify me when a backup fails" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Draft my flow" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Configure AI in Integrations");
    const link = screen.getByRole("link", { name: "Integrations" });
    expect(link).toHaveAttribute("href", "/services");
  });

  it("shows readable validation errors on 422 responses", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              error: {
                code: "validation_failed",
                message: "Could not produce a valid flow: a1: unknown action",
                fields: [
                  { field: "a1 (nodes[1].config.actionType)", message: 'unknown action "bad.act"' },
                ],
              },
            }),
            { status: 422 },
          ),
      ),
    );

    // Also verifies router-free rendering works cleanly.
    render(<DescribeFlowDialog open onClose={vi.fn()} onDraft={vi.fn()} />);

    fireEvent.change(screen.getByLabelText("What should happen?"), {
      target: { value: "Do something unsupported" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Draft my flow" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn't draft that flow");
    expect(alert).toHaveTextContent('unknown action "bad.act"');
  });

  it("renders a valid draft and passes it to onDraft", async () => {
    const draft = {
      name: "Backup alert",
      description: "Alert on failed backup.",
      explanation: "Listens for failed backups and notifies you.",
      graph: {
        schemaVersion: 1,
        nodes: [
          {
            id: "t1",
            kind: "trigger.event" as const,
            label: "Backup failed",
            position: { x: 80, y: 120 },
            config: { events: ["system.backup.failed"] },
          },
          {
            id: "a1",
            kind: "action" as const,
            label: "Notify",
            position: { x: 380, y: 120 },
            config: {
              actionType: "notify.send",
              params: { channelIds: ["*"], title: "Failed", message: "Oops" },
            },
          },
        ],
        edges: [{ id: "e1", source: "t1", sourceHandle: "next", target: "a1" }],
      },
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify(draft), { status: 200 })),
    );

    const onDraft = vi.fn();
    const onClose = vi.fn();
    render(
      <MemoryRouter>
        <DescribeFlowDialog open onClose={onClose} onDraft={onDraft} />
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByLabelText("What should happen?"), {
      target: { value: "Notify me when backups fail" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Draft my flow" }));

    expect(await screen.findByText("Backup alert")).toBeInTheDocument();
    expect(screen.getByText("Listens for failed backups and notifies you.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open in editor" }));
    await waitFor(() => expect(onDraft).toHaveBeenCalledWith(draft));
    expect(onClose).toHaveBeenCalled();
  });
});
