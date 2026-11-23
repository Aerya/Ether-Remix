import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { LinkImport } from "./LinkImport";
import type { DownloaderStatus } from "./model";

describe("LinkImport", () => {
  it("opens in a drawer, hides downloader status once ready, and supports link clips and imports", async () => {
    let status: DownloaderStatus = {
      available: false,
      source: "none",
      pinnedVersion: "2026.10.26",
      canInstall: true,
      canUpdate: false,
    };
    const calls: { url: string; method: string; body?: unknown }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? "GET";
        const body = init?.body ? JSON.parse(String(init.body)) : undefined;
        calls.push({ url, method, body });
        if (url.endsWith("/onair/downloader") && method === "GET") {
          return new Response(JSON.stringify(status), { status: 200 });
        }
        if (url.endsWith("/onair/downloader/install") && method === "POST") {
          status = {
            available: true,
            source: "managed",
            version: "2026.10.26",
            pinnedVersion: "2026.10.26",
            canInstall: true,
            canUpdate: true,
          };
          return new Response(JSON.stringify(status), { status: 200 });
        }
        if (url.endsWith("/onair/reels/saturday/clips/import") && method === "POST") {
          return new Response(JSON.stringify([{ id: "c1", status: "ready", link: true }]), {
            status: 201,
          });
        }
        return new Response("{}", { status: 200 });
      }),
    );

    const onImported = vi.fn();
    render(<LinkImport reelId="saturday" onImported={onImported} />);

    fireEvent.click(screen.getByRole("button", { name: /Add from link/i }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    expect(screen.getByText(/Video downloader isn’t installed yet/i)).toBeInTheDocument();
    const installBtn = await screen.findByRole("button", { name: /^Install downloader$/i });
    expect(installBtn).toBeInTheDocument();

    fireEvent.click(installBtn);

    await waitFor(() => {
      expect(screen.queryByText(/Video downloader isn’t installed yet/i)).not.toBeInTheDocument();
    });
    expect(screen.queryByText(/yt-dlp/i)).not.toBeInTheDocument();

    const textarea = screen.getByLabelText("Media links");
    fireEvent.change(textarea, {
      target: { value: "https://archive.org/details/retro_ad_1988" },
    });

    fireEvent.click(screen.getByRole("button", { name: /^Add as link$/i }));

    await waitFor(() => {
      expect(onImported).toHaveBeenCalledTimes(1);
    });

    const linkCall = calls.find((c) => c.url.endsWith("/onair/reels/saturday/clips/import"));
    expect(linkCall?.body).toEqual({
      urls: ["https://archive.org/details/retro_ad_1988"],
      kind: "video",
      mode: "link",
    });

    fireEvent.click(screen.getByRole("button", { name: /Add from link/i }));
    fireEvent.change(screen.getByLabelText("Media links"), {
      target: { value: "https://www.youtube.com/watch?v=dQw4w9WgXcQ" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^Import$/i }));

    await waitFor(() => {
      expect(onImported).toHaveBeenCalledTimes(2);
    });

    const importCalls = calls.filter((c) => c.url.endsWith("/onair/reels/saturday/clips/import"));
    expect(importCalls[1]?.body).toEqual({
      urls: ["https://www.youtube.com/watch?v=dQw4w9WgXcQ"],
      kind: "video",
    });
  });
});
