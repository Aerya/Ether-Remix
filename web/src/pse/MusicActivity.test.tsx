import { fireEvent, render, screen } from "@testing-library/react";
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
  it("mixes music and video in one chronological table and opens music details", async () => {
    vi.mocked(apiFetch).mockResolvedValue({
      plays: [
        {
          id: 1,
          viewerId: 7,
          title: "One More Time",
          artist: "Daft Punk",
          album: "Discovery",
          duration: 201,
          playedAt: 20,
        },
      ],
      total: 1,
    });
    const video = {
      id: "video",
      userId: 7,
      username: "Alice",
      title: "A film",
      device: "TV",
      platform: "",
      decision: "",
      started: 10,
      ended: 11,
      bytes: 0,
      watchedMs: 1000,
      completed: true,
      failovers: 0,
      error: "",
    };
    render(
      <MemoryRouter>
        <MusicHistory
          query="?days=30"
          viewers={[]}
          history={[video]}
          summary={undefined}
          error={null}
          onRetry={vi.fn()}
        />
      </MemoryRouter>,
    );
    expect(await screen.findByText("One More Time")).toBeInTheDocument();
    expect(screen.getByText("A film")).toBeInTheDocument();
    expect(screen.getAllByRole("table")).toHaveLength(1);
    const tableRows = screen.getAllByRole("row");
    expect(tableRows[1]).toHaveTextContent("One More Time");
    expect(tableRows[2]).toHaveTextContent("A film");
    fireEvent.click(screen.getByText("One More Time"));
    expect(await screen.findByText("Discovery")).toBeInTheDocument();
    expect(
      vi.mocked(apiFetch).mock.calls.some(([path]) => String(path).includes("/stats/plays/music")),
    ).toBe(false);
  });
  it("reports history loading failures", async () => {
    vi.mocked(apiFetch).mockRejectedValue(new Error("Unavailable"));
    render(
      <MemoryRouter>
        <MusicHistory
          query="?days=30"
          viewers={[]}
          history={[]}
          summary={undefined}
          error={null}
          onRetry={vi.fn()}
        />
      </MemoryRouter>,
    );
    expect(await screen.findByText("Unavailable")).toBeInTheDocument();
  });
});
