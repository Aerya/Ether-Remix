import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { NowPlaying } from "../pages/activity/NowPlaying";
import { MusicHistory } from "./MusicHistory";
import { normalizeSessions } from "../lib/health";
import { apiFetch } from "../lib/api";
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: vi.fn(),
}));
vi.mock("../lib/titles", () => ({ useTitle: vi.fn(() => null) }));

const rows = normalizeSessions([
  {
    id: "music-one",
    title: "One More Time",
    isMusic: true,
    artist: "Daft Punk",
    album: "Discovery",
    canStop: false,
    viewerState: "paused",
    username: "Alice",
    client: { protocol: "subsonic", product: "Music client" },
    info: { duration: 201000000000 },
    viewOffset: 12000,
  },
]);

describe("Music activity", () => {
  it.each(["cards", "list"] as const)(
    "shows music metadata without a video stop action (%s)",
    (view) => {
      render(
        <MemoryRouter>
          <NowPlaying
            state={{
              data: rows,
              loading: false,
              error: undefined,
              reload: vi.fn(async () => {}),
              setData: vi.fn(),
            }}
            protocol=""
            view={view}
          />
        </MemoryRouter>,
      );
      expect(screen.getByText("One More Time")).toBeInTheDocument();
      expect(screen.getByText("Daft Punk · Discovery")).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /Stop stream/ })).toBeNull();
      expect(rows[0]?.progress).toBeCloseTo(12 / 201);
    },
  );
  it("paginates music history and resets when the viewer filter changes", async () => {
    vi.mocked(apiFetch).mockImplementation(async (path) => ({
      plays: [
        {
          id: 1,
          viewerId: 7,
          title: String(path).includes("offset=100") ? "Second page" : "One More Time",
          artist: "Daft Punk",
          album: "Discovery",
          duration: 201,
          playedAt: 1794300000,
        },
      ],
      total: 101,
    }));
    const { rerender } = render(<MusicHistory query="?days=30&user=7" viewers={[]} />);
    expect(await screen.findByText("One More Time")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("Second page")).toBeInTheDocument();
    rerender(<MusicHistory query="?days=30&user=8" viewers={[]} />);
    await waitFor(() =>
      expect(apiFetch).toHaveBeenLastCalledWith("/pse/music/history?days=30&user=8&offset=0"),
    );
  });
  it("reports history loading failures", async () => {
    vi.mocked(apiFetch).mockRejectedValue(new Error("Unavailable"));
    render(<MusicHistory query="?days=30" viewers={[]} />);
    expect(await screen.findByText("Unavailable")).toBeInTheDocument();
  });
});
