import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetShellHorizontalScroll } from "../../../components/Drawer";
import { LooksGallery, blankStyle } from "../onair/LooksGallery";
import { CreateTab } from "../onair/editor/CreateTab";
import { RemixPanel } from "../onair/editor/RemixPanel";
import { friendlyMediaError } from "../onair/mediaErrors";
import Library, { LibraryAside } from "./Library";

describe("On Air Library & Styles UI fixes", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("maps raw backend and downloader errors to friendly copy", () => {
    expect(friendlyMediaError("Get https://archive.org/foo: context deadline exceeded")).toBe(
      "Timed out while downloading. Check the link and try again.",
    );
    expect(friendlyMediaError("unexpected HTTP 404")).toBe("Media wasn’t found at that link.");
  });

  it("keeps Library counts consistent, renders sort in the header, and gives failed imports Retry and Delete", async () => {
    const calls: { url: string; method: string; body?: unknown }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? "GET";
        const body = init?.body ? JSON.parse(String(init.body)) : undefined;
        calls.push({ url, method, body });
        if (url.endsWith("/onair/assets") && method === "GET") {
          return new Response(
            JSON.stringify([
              {
                digest: "a".repeat(64),
                kind: "video",
                name: "Bumper 1",
                mime: "video/mp4",
                size: 1024,
                durationMs: 5000,
                license: "owned",
                usedBy: [],
                createdAt: 10,
              },
              {
                digest: "job-failed-1",
                kind: "video",
                name: "https://archive.org/details/slow_clip",
                mime: "video/mp4",
                size: 0,
                durationMs: 0,
                license: "owned",
                source: "https://archive.org/details/slow_clip",
                status: "error",
                error: "context deadline exceeded",
                usedBy: [],
                createdAt: 20,
              },
            ]),
            { status: 200 },
          );
        }
        if (url.endsWith("/onair/reels") && method === "GET") {
          return new Response(
            JSON.stringify([
              {
                id: "reel-80s",
                name: "80s Breaks",
                order: "shuffle",
                repeatHours: 1,
                loudnessMatch: true,
                trimSilence: false,
                channelIds: [],
                clips: [
                  {
                    id: "clip-1",
                    name: "Toy Commercial",
                    durationMs: 30000,
                    status: "ready",
                    link: true,
                    cached: false,
                    source: "https://archive.org/details/toy_ad",
                    thumbnail: "/onair/reels/reel-80s/clips/clip-1/thumb",
                  },
                ],
              },
            ]),
            { status: 200 },
          );
        }
        if (url.endsWith("/onair/downloader") && method === "GET") {
          return new Response(
            JSON.stringify({
              available: true,
              source: "managed",
              pinnedVersion: "2026.10.26",
              canInstall: true,
              canUpdate: true,
            }),
            { status: 200 },
          );
        }
        if (url.endsWith("/onair/assets/import") && method === "POST") {
          return new Response(JSON.stringify([{ digest: "job-retried", status: "queued" }]), {
            status: 202,
          });
        }
        if (url.includes("/onair/assets/job-failed-1") && method === "DELETE") {
          return new Response(null, { status: 204 });
        }
        return new Response("{}", { status: 200 });
      }),
    );

    render(
      <MemoryRouter initialEntries={["/livetv/library"]}>
        <LibraryAside />
        <Library />
      </MemoryRouter>,
    );

    // Everything count equals Video count (2 library videos + 1 reel clip = 3).
    const everythingBtn = await screen.findByRole("button", { name: /Everything/i });
    expect(everythingBtn).toHaveTextContent("3");
    const videoButtons = screen.getAllByRole("button", { name: /^Video\s*3$/i });
    expect(videoButtons.length).toBeGreaterThan(0);

    // Sort picker lives in the Library grid header.
    expect(screen.getByRole("button", { name: /Sort assets/i })).toBeInTheDocument();

    // Friendly failure reason replaces raw "context deadline exceeded".
    expect(
      await screen.findByText("Timed out while downloading. Check the link and try again."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/context deadline exceeded/i)).not.toBeInTheDocument();

    // Clicking the failed item opens a drawer with Retry and Delete.
    fireEvent.click(screen.getByText("https://archive.org/details/slow_clip"));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    const retryBtn = screen.getByRole("button", { name: /^Retry$/i });
    expect(screen.getByRole("button", { name: /^Delete$/i })).toBeInTheDocument();

    fireEvent.click(retryBtn);
    await waitFor(() => {
      expect(calls.some((c) => c.url.endsWith("/onair/assets/import") && c.method === "POST")).toBe(
        true,
      );
    });
  });

  it("puts Brand kit inside a padded section and shows a style-specific Create empty state", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/agent/status")) {
          return new Response(JSON.stringify({ enabled: true }), { status: 200 });
        }
        if (url.endsWith("/onair/assets")) {
          return new Response("[]", { status: 200 });
        }
        return new Response("{}", { status: 200 });
      }),
    );
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    );

    const style = blankStyle();
    const { container } = render(
      <MemoryRouter>
        <RemixPanel style={style} onChange={vi.fn()} />
        <CreateTab style={style} onChange={vi.fn()} />
      </MemoryRouter>,
    );

    expect(container.querySelector("legend")).toBeNull();
    expect(screen.getByRole("region", { name: "Brand kit" })).toBeInTheDocument();
    expect(await screen.findByText("Design your channel’s on-air look")).toBeInTheDocument();
    expect(screen.queryByText(/Ask about this server/i)).not.toBeInTheDocument();
  });

  it("omits duplicate content tabs and Hover to play text in main LooksGallery", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/onair/templates")) {
          return new Response(
            JSON.stringify([
              {
                id: "retro",
                name: "1984 Synth",
                blurb: "Neon nights",
                tags: ["80s"],
                style: blankStyle(),
              },
            ]),
            { status: 200 },
          );
        }
        if (url.includes("/livetv/channels")) {
          return new Response("[]", { status: 200 });
        }
        return new Response("{}", { status: 200 });
      }),
    );
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    );

    render(
      <MemoryRouter initialEntries={["/livetv/looks?view=make"]}>
        <LooksGallery looks={[]} onNew={vi.fn()} onSelect={vi.fn()} />
      </MemoryRouter>,
    );

    expect(await screen.findByText("1984 Synth")).toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.queryByText(/Hover to play/i)).not.toBeInTheDocument();
  });

  it("resets horizontal scroll across document and shell containers", () => {
    const shell = document.createElement("div");
    shell.className = "ether-shell";
    const inner = document.createElement("div");
    const ws = document.createElement("div");
    ws.setAttribute("data-workspace-scroll", "");
    inner.appendChild(ws);
    shell.appendChild(inner);
    document.body.appendChild(shell);

    document.documentElement.scrollLeft = 180;
    document.body.scrollLeft = 120;
    inner.scrollLeft = 240;
    ws.scrollLeft = 95;

    resetShellHorizontalScroll();

    expect(document.documentElement.scrollLeft).toBe(0);
    expect(document.body.scrollLeft).toBe(0);
    expect(inner.scrollLeft).toBe(0);
    expect(ws.scrollLeft).toBe(0);
    shell.remove();
  });
});
