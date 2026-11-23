import { Toaster } from "@braedonsaunders/appkit-ui";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createFakeServer } from "../mock/server";
import LiveTV from "../pages/livetv/LiveTV";
import {
  ChannelsWorkspace,
  type ChannelFilter,
  type ChannelSort,
} from "../pages/livetv/ChannelsWorkspace";
import type { ChannelSummary } from "../pages/livetv/model";
import type { ReorderPlan } from "../pages/livetv/reorder";
import { calls as fakeCalls } from "./harness";

function makeChannel(id: string, number: string, name: string): ChannelSummary {
  return {
    id,
    number,
    name,
    kind: "iptv",
    enabled: true,
    favorite: false,
    group: "News",
    sourceId: "src-1",
  };
}

const SAMPLE: ChannelSummary[] = [
  makeChannel("ch-1", "1", "Movie Night"),
  makeChannel("ch-2", "2", "Family Animation"),
  makeChannel("ch-3", "3", "80s Action"),
  makeChannel("ch-4", "100", "News 24"),
  makeChannel("ch-5", "101", "Sports HD"),
];

function renderWorkspace(opts?: {
  channels?: ChannelSummary[];
  query?: string;
  filter?: ChannelFilter;
  sort?: ChannelSort;
  onRenumberChannels?: (plan: ReorderPlan) => Promise<boolean>;
}) {
  const onRenumberChannels = opts?.onRenumberChannels ?? vi.fn().mockResolvedValue(true);
  const res = render(
    <ChannelsWorkspace
      channels={opts?.channels ?? SAMPLE}
      sources={[]}
      coverage={new Map()}
      ownerGroups={[]}
      query={opts?.query ?? ""}
      filter={opts?.filter ?? "all"}
      onFilterChange={vi.fn()}
      sort={opts?.sort ?? "number"}
      onSortChange={vi.fn()}
      missingCount={0}
      selectedChannelId={null}
      busy={false}
      onOpenChannel={vi.fn()}
      onSaveOwnerGroups={vi.fn().mockResolvedValue(true)}
      onSetChannelsApp={vi.fn().mockResolvedValue(true)}
      onUpdateChannels={vi.fn().mockResolvedValue(true)}
      onRenumberChannels={onRenumberChannels}
    />,
  );
  return { ...res, onRenumberChannels };
}

describe("ChannelsWorkspace reordering", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("previews resulting numbers during pointer/touch drag and commits in one plan", () => {
    const onRenumberChannels = vi.fn().mockResolvedValue(true);
    const { container } = renderWorkspace({ onRenumberChannels });

    const scrollBox = container.querySelector(".app-scroll.flex-1") as HTMLDivElement;
    expect(scrollBox).not.toBeNull();
    vi.spyOn(scrollBox, "getBoundingClientRect").mockReturnValue({
      top: 100,
      left: 20,
      bottom: 600,
      right: 720,
      width: 700,
      height: 500,
      x: 20,
      y: 100,
      toJSON: () => ({}),
    });

    // Drag News 24 (#100, index 3) up to slot 1 (between #1 and #2) using touch pointer
    const handle = container.querySelector(
      '[data-channel-drag-handle="ch-4"]',
    ) as HTMLButtonElement;
    expect(handle).not.toBeNull();
    expect(handle.disabled).toBe(false);

    fireEvent.pointerDown(handle, { pointerId: 1, pointerType: "touch", button: 0, clientY: 296 });
    fireEvent.pointerMove(window, { pointerId: 1, pointerType: "touch", clientY: 156 });

    // Live preview banner, drop indicator, drag ghost, and inline shifted badges are visible
    const preview = screen.getByTestId("channel-reorder-preview");
    expect(preview.textContent).toContain("News 24");
    expect(preview.textContent).toContain("100");
    expect(preview.textContent).toContain("2");
    expect(screen.getByTestId("channel-drop-indicator")).toBeTruthy();
    expect(screen.getByTestId("channel-drag-ghost")).toBeTruthy();
    expect(screen.getByTestId("channel-number-preview-ch-2").textContent).toContain("3");
    expect(screen.getByTestId("channel-number-preview-ch-3").textContent).toContain("4");
    expect(screen.queryByTestId("channel-number-preview-ch-5")).toBeNull();

    // Release commits one batched plan with nextNumbers & prevNumbers for undo
    fireEvent.pointerUp(window, { pointerId: 1, pointerType: "touch", clientY: 156 });
    expect(onRenumberChannels).toHaveBeenCalledTimes(1);
    const plan = onRenumberChannels.mock.calls[0]![0] as ReorderPlan;
    expect(plan.nextNumbers).toEqual({ "ch-4": "2", "ch-2": "3", "ch-3": "4" });
    expect(plan.prevNumbers).toEqual({ "ch-4": "100", "ch-2": "2", "ch-3": "3" });
  });

  it("supports keyboard grab mode (Space + Arrows + Enter) and Escape cancel", () => {
    const onRenumberChannels = vi.fn().mockResolvedValue(true);
    const { container } = renderWorkspace({ onRenumberChannels });
    const handle = container.querySelector(
      '[data-channel-drag-handle="ch-4"]',
    ) as HTMLButtonElement;

    // Pick up with Space, move up once, then Escape to cancel
    fireEvent.keyDown(handle, { key: " " });
    expect(screen.getByTestId("channel-reorder-preview")).toBeTruthy();
    fireEvent.keyDown(window, { key: "ArrowUp" });
    expect(screen.getByTestId("channel-drop-indicator")).toBeTruthy();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("channel-reorder-preview")).toBeNull();
    expect(onRenumberChannels).not.toHaveBeenCalled();

    // Pick up again, move up once (between #2 and #3), commit with Enter
    fireEvent.keyDown(handle, { key: "Enter" });
    fireEvent.keyDown(window, { key: "ArrowUp" });
    fireEvent.keyDown(window, { key: "Enter" });

    expect(onRenumberChannels).toHaveBeenCalledTimes(1);
    const plan = onRenumberChannels.mock.calls[0]![0] as ReorderPlan;
    expect(plan.nextNumbers).toEqual({ "ch-4": "3", "ch-3": "4" });
  });

  it("supports Alt+ArrowUp and Alt+ArrowDown quick keyboard nudge", () => {
    const onRenumberChannels = vi.fn().mockResolvedValue(true);
    const { container } = renderWorkspace({ onRenumberChannels });
    const handle = container.querySelector(
      '[data-channel-drag-handle="ch-5"]',
    ) as HTMLButtonElement;

    // Move Sports HD (#101) up one slot (between #3 and #100 -> fits in gap as #4!)
    fireEvent.keyDown(handle, { key: "ArrowUp", altKey: true });
    expect(onRenumberChannels).toHaveBeenCalledTimes(1);
    const plan = onRenumberChannels.mock.calls[0]![0] as ReorderPlan;
    expect(plan.nextNumbers).toEqual({ "ch-5": "4" });
    expect(plan.shifted).toEqual([]);
  });

  it("disables drag when filtered or sorted differently, while keeping Move to position usable", async () => {
    const user = userEvent.setup();
    const onRenumberChannels = vi.fn().mockResolvedValue(true);
    const { container, rerender } = renderWorkspace({
      query: "News",
      onRenumberChannels,
    });

    // Only News 24 matches the search, drag handle is disabled with clear explanation
    const handle = container.querySelector(
      '[data-channel-drag-handle="ch-4"]',
    ) as HTMLButtonElement;
    expect(handle.disabled).toBe(true);
    expect(handle.title).toContain("Clear search and filters");

    // "Move to position" still lets the user jump the searched channel to position 2
    const moveBtn = screen.getByRole("button", { name: "Move News 24 to position" });
    await user.click(moveBtn);

    const dialog = screen.getByRole("dialog");
    const input = within(dialog).getByLabelText(/New position/);
    await user.clear(input);
    await user.type(input, "2");

    // Dialog previews the resulting channel numbers before applying
    expect(dialog.textContent).toContain("100");
    expect(dialog.textContent).toContain("2 following channels will shift");

    await user.click(within(dialog).getByRole("button", { name: "Move channel" }));
    expect(onRenumberChannels).toHaveBeenCalledTimes(1);
    const plan = onRenumberChannels.mock.calls[0]![0] as ReorderPlan;
    expect(plan.nextNumbers).toEqual({ "ch-4": "2", "ch-2": "3", "ch-3": "4" });

    // Non-number sort also disables drag with a clear hint
    rerender(
      <ChannelsWorkspace
        channels={SAMPLE}
        sources={[]}
        coverage={new Map()}
        ownerGroups={[]}
        query=""
        filter="all"
        onFilterChange={vi.fn()}
        sort="name"
        onSortChange={vi.fn()}
        missingCount={0}
        selectedChannelId={null}
        busy={false}
        onOpenChannel={vi.fn()}
        onSaveOwnerGroups={vi.fn().mockResolvedValue(true)}
        onSetChannelsApp={vi.fn().mockResolvedValue(true)}
        onUpdateChannels={vi.fn().mockResolvedValue(true)}
        onRenumberChannels={onRenumberChannels}
      />,
    );
    const sortedHandle = container.querySelector(
      '[data-channel-drag-handle="ch-4"]',
    ) as HTMLButtonElement;
    expect(sortedHandle.disabled).toBe(true);
    expect(sortedHandle.title).toContain("Sort by Channel #");
  });

  it("persists one batched PATCH /livetv/channels/bulk request and restores on Undo toast", async () => {
    const fake = createFakeServer();
    vi.stubGlobal("fetch", fake.fetch);
    const user = userEvent.setup();
    const { container } = render(
      <MemoryRouter initialEntries={["/livetv"]}>
        <Toaster />
        <Routes>
          <Route path="/livetv/:tab?" element={<LiveTV />} />
        </Routes>
      </MemoryRouter>,
    );

    // Wait for mock channels to load (virtual #1..#5 and IPTV #100 News 24)
    const newsHandle = await waitFor(() => {
      const el = container.querySelector(
        '[data-channel-drag-handle="iptv-0"]',
      ) as HTMLButtonElement | null;
      expect(el).not.toBeNull();
      return el!;
    });

    // Move News 24 (#100) up one slot above Personal TV (#5) -> News 24 becomes #5, Personal TV shifts #5 -> #6
    fireEvent.keyDown(newsHandle, { key: "ArrowUp", altKey: true });

    await waitFor(() => {
      const bulkCalls = fakeCalls(fake, "PATCH", "/livetv/channels/bulk");
      expect(bulkCalls.length).toBe(1);
      const body = bulkCalls[0]!.body as {
        ids: string[];
        numbers: Record<string, string>;
      };
      expect(body.numbers).toEqual({ "iptv-0": "5", "v-personal": "6" });
    });

    // Undo toast appears; clicking Undo sends one batched PATCH restoring original numbers
    const undoBtn = await screen.findByRole("button", { name: "Undo" });
    await user.click(undoBtn);

    await waitFor(() => {
      const bulkCalls = fakeCalls(fake, "PATCH", "/livetv/channels/bulk");
      expect(bulkCalls.length).toBe(2);
      const body = bulkCalls[1]!.body as {
        ids: string[];
        numbers: Record<string, string>;
      };
      expect(body.numbers).toEqual({ "iptv-0": "100", "v-personal": "5" });
    });
  });
});
