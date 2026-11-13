import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { TorrentStatus, formatETA, formatSize } from "./TorrentStatus";
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));

it("shows clients, free space and downloads in progress first", () => {
  render(
    <TorrentStatus
      status={{
        active: true,
        state: {
          clients: [
            { name: "qBittorrent", version: "v5.2.0", free: 5 * 2 ** 30, lowSpace: true },
            { name: "Client 2 (rTorrent)", error: "injoignable", freeNote: "dossier invisible" },
          ],
          downloads: [
            {
              client: "qBittorrent",
              name: "Film.2024",
              progress: 0.42,
              size: 2 ** 30,
              eta: 720,
              done: false,
              active: true,
              ratio: 0,
            },
          ],
          seeding: 3,
          minFree: 20 * 2 ** 30,
          addedToday: 2,
          maxPerDay: 10,
        },
      }}
    />,
  );
  expect(screen.getByText("Film.2024")).toBeTruthy();
  expect(screen.getByText(/42 %/)).toBeTruthy();
  expect(screen.getByText(/2 \/ 10/)).toBeTruthy();
  expect(screen.getByText(/(sous le seuil|below the)/)).toBeTruthy();
  expect(screen.getByText(/(Injoignable|Unreachable)/)).toBeTruthy();
});

it("hides when the torrent client is off", () => {
  const { container } = render(<TorrentStatus status={{ active: false }} />);
  expect(container.innerHTML).toBe("");
});

it("formats sizes and remaining time", () => {
  expect(formatETA(30)).toBe("< 1 min");
  expect(formatETA(720)).toBe("12 min");
  expect(formatETA(5400)).toBe("1 h 30");
  expect(formatSize(1536)).toMatch(/^1[.,]5 (Ko|KB)$/);
});
