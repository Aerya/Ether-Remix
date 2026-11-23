import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  GUIDE_ZOOM_HOURS,
  GuideGrid,
  guideDayCount,
  guideDayOffset,
  guideHourTarget,
  guideNowScrollLeft,
  initialGuideZoom,
} from "../pages/livetv/GuideGrid";
import { ProgrammeInspector } from "../pages/livetv/ProgrammeInspector";
import type { Channel, Programme } from "../pages/livetv/model";

const HOUR = 60 * 60_000;
// Local times, so the day maths runs in whatever zone the suite uses.
const at = (day: number, hour: number, minute = 0) =>
  new Date(2026, 10, day, hour, minute).getTime();

const channel: Channel = {
  id: "ch1",
  number: "1.1",
  name: "News Channel",
  kind: "iptv",
  enabled: true,
  favorite: false,
  group: "News",
  streamUrl: "http://example.com/stream.m3u8",
  epoch: "2026-01-01T00:00:00Z",
  seed: 0,
  shuffle: false,
  items: [],
};

describe("guide day maths", () => {
  it("offers today through the last day with listings, at most a week", () => {
    const now = at(7, 10);
    expect(guideDayCount(now)).toBe(7);
    expect(guideDayCount(now, now - HOUR)).toBe(1);
    expect(guideDayCount(now, at(8, 16))).toBe(2);
    // Listings ending exactly at midnight do not open an empty day.
    expect(guideDayCount(now, at(9, 0))).toBe(2);
    expect(guideDayCount(now, at(30, 0))).toBe(7);
    expect(guideDayOffset(at(9, 23, 59), now)).toBe(2);
  });

  it("jumps to tonight's hour, or tomorrow's once it has aired", () => {
    expect(guideHourTarget(at(7, 12), 0, 20)).toBe(at(7, 20));
    expect(guideHourTarget(at(7, 20, 30), 0, 20)).toBe(at(7, 20));
    expect(guideHourTarget(at(7, 21, 30), 0, 20)).toBe(at(8, 20));
    expect(guideHourTarget(at(7, 21, 30), 0, 23)).toBe(at(7, 23));
    expect(guideHourTarget(at(7, 12), 2, 23)).toBe(at(9, 23));
    // A day before today (scrolled back) still jumps forward.
    expect(guideHourTarget(at(7, 12), -1, 20)).toBe(at(7, 20));
  });
});

describe("guide zoom", () => {
  it("defaults to 3h and keeps a stored choice", () => {
    expect(GUIDE_ZOOM_HOURS[initialGuideZoom(null)]).toBe(3);
    expect(GUIDE_ZOOM_HOURS[initialGuideZoom("nonsense")]).toBe(3);
    expect(initialGuideZoom("normal")).toBe("normal");
    expect(initialGuideZoom("tight")).toBe("tight");
  });
});

describe("guide horizontal alignment and phone column", () => {
  it("snaps the live scroll position to a 30-minute slot boundary on desktop and phone", () => {
    const start = at(7, 20, 0);
    const now = at(7, 21, 53);
    // Desktop timeline: 21:53 - 30m -> 21:00 slot (60m * 5px/m = 300px).
    expect(guideNowScrollLeft(now, start, 5, 1000)).toBe(300);
    // Narrow phone timeline (290px): advances to 21:30 slot (90m * 5px/m = 450px) so LIVE stays on screen.
    expect(guideNowScrollLeft(now, start, 5, 290)).toBe(450);
  });

  it("keeps partially scrolled programme titles clear of the sticky channel column and hides clipped ruler ticks", () => {
    const from = new Date(at(7, 20, 0));
    const now = at(7, 21, 53);
    const shortShow: Programme = {
      id: "short-1",
      channelId: "ch1",
      start: new Date(at(7, 21, 0)).toISOString(),
      end: new Date(at(7, 21, 22)).toISOString(),
      item: {
        key: 99,
        durationMs: 22 * 60_000,
        title: "The Last Show",
      },
    };
    render(
      <GuideGrid
        channels={[channel]}
        guide={new Map([["ch1", [shortShow]]])}
        from={from}
        until={new Date(from.getTime() + 6 * HOUR)}
        now={now}
        selected={null}
        onSelect={vi.fn()}
        onShift={vi.fn()}
        onNow={vi.fn()}
        loading={false}
      />,
    );

    const grid = screen.getByRole("grid", { name: "Programme guide" });
    // Scroll mid-slot to 21:08 (68m * 5px = 340px, 40px past the 21:00 card start).
    Object.defineProperty(grid, "scrollLeft", { value: 340, configurable: true });
    Object.defineProperty(grid, "clientWidth", { value: 1200, configurable: true });
    fireEvent.scroll(grid);

    const cell = screen.getByRole("gridcell", { name: /News Channel: The Last Show/ });
    const content = cell.querySelector("span.relative");
    expect(content).toHaveStyle({ paddingLeft: "40px" });
    // The 9:00 PM ruler label is scrolled behind the sticky column, so it is omitted rather than half-clipped.
    const ninePm = new Date(at(7, 21, 0)).toLocaleTimeString("en", {
      hour: "numeric",
      minute: "2-digit",
    });
    expect(within(grid).queryByText(ninePm)).toBeNull();
  });

  it("narrows the sticky channel column on phone viewports", () => {
    const originalMatchMedia = window.matchMedia;
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: query === "(max-width: 639px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    try {
      const from = new Date(at(7, 20, 0));
      render(
        <GuideGrid
          channels={[channel]}
          guide={new Map()}
          from={from}
          until={new Date(from.getTime() + 4 * HOUR)}
          now={at(7, 20, 30)}
          selected={null}
          onSelect={vi.fn()}
          onShift={vi.fn()}
          onNow={vi.fn()}
          loading={false}
        />,
      );
      const header = screen.getByRole("rowheader", { name: "Channel 1.1 News Channel" });
      expect(header).toHaveStyle({ width: "100px" });
      expect(within(header).getByText("1.1")).toBeInTheDocument();
      expect(within(header).getByText("News Channel")).toBeInTheDocument();
    } finally {
      window.matchMedia = originalMatchMedia;
    }
  });
});

describe("GuideGrid day picker", () => {
  const renderGrid = (props: Partial<Parameters<typeof GuideGrid>[0]> = {}) => {
    const now = at(7, 12);
    const from = new Date(at(7, 11));
    const onShift = vi.fn();
    render(
      <GuideGrid
        channels={[channel]}
        guide={new Map()}
        from={from}
        until={new Date(from.getTime() + 24 * HOUR)}
        now={now}
        selected={null}
        onSelect={vi.fn()}
        onShift={onShift}
        onNow={vi.fn()}
        loading={false}
        head={<div />}
        {...props}
      />,
    );
    const open = () => fireEvent.click(screen.getByRole("button", { name: "Day" }));
    return { onShift, open };
  };

  it("moves the window to the whole of the picked day", () => {
    const { onShift, open } = renderGrid();
    expect(screen.getByRole("button", { name: "Day" })).toHaveTextContent("Today");
    open();
    fireEvent.click(screen.getByRole("menuitemradio", { name: "Tomorrow" }));
    // 11:00 today to midnight tomorrow.
    expect(onShift).toHaveBeenCalledWith(13);
  });

  it("scrolls to prime time without moving a window that already holds it", () => {
    const { onShift, open } = renderGrid();
    open();
    fireEvent.click(screen.getByRole("menuitemradio", { name: /^Prime time/ }));
    expect(onShift).not.toHaveBeenCalled();
  });

  it("only offers days and jumps the guide has listings for", () => {
    const { open } = renderGrid({ guideUntil: at(7, 22) });
    open();
    const menu = screen.getByRole("menu", { name: "Day" });
    expect(
      within(menu)
        .getAllByRole("menuitemradio")
        .map((b) => b.textContent),
    ).toEqual(["Today", expect.stringMatching(/^Prime time/)]);
  });
});

describe("ProgrammeInspector", () => {
  const programme: Programme = {
    id: "p1",
    channelId: "ch1",
    start: new Date(at(7, 12)).toISOString(),
    end: new Date(at(7, 13)).toISOString(),
    item: {
      key: 1,
      durationMs: HOUR,
      title: "Evening News",
      summary: "Top stories of the day",
      season: 3,
      episode: 4,
    },
  };

  it("opens as a drawer with the channel's actions", () => {
    const onPlay = vi.fn();
    render(
      <ProgrammeInspector
        open
        inspected={{ channel, programme }}
        now={at(7, 12, 30)}
        onClose={vi.fn()}
        onPlay={onPlay}
      />,
    );
    const drawer = screen.getByRole("dialog", { name: "Evening News" });
    expect(within(drawer).getByText("Top stories of the day")).toBeInTheDocument();
    expect(within(drawer).getByText("S3 E4")).toBeInTheDocument();
    expect(within(drawer).getByText("On now")).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: /Watch/ }));
    expect(onPlay).toHaveBeenCalledWith(channel);
  });

  it("copies the stream link over plain HTTP, where the Clipboard API is missing", () => {
    const clipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    const exec = vi.fn().mockReturnValue(true);
    document.execCommand = exec;
    try {
      render(
        <ProgrammeInspector
          open
          inspected={{ channel, programme }}
          now={at(7, 12, 30)}
          onClose={vi.fn()}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: /Copy link/ }));
      expect(exec).toHaveBeenCalledWith("copy");
    } finally {
      delete (document as { execCommand?: unknown }).execCommand;
      if (clipboard) Object.defineProperty(navigator, "clipboard", clipboard);
      else delete (navigator as { clipboard?: unknown }).clipboard;
    }
  });
});
