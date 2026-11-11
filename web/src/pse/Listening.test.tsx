import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Listening } from "./Listening";
import { combineHistory } from "./MusicHistory";
import { apiFetch } from "../lib/api";
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: vi.fn(),
}));

describe("Listening", () => {
  it("requests the full filtered summary, shows rankings and no invented listening hours", async () => {
    vi.mocked(apiFetch).mockResolvedValue({
      plays: 105,
      tracks: 1,
      artists: 1,
      listeners: 1,
      daily: [{ key: "2026-11-11", label: "2026-11-11", plays: 105 }],
      titles: [{ key: "track", label: "Song · Artist", plays: 105 }],
      artistsRanked: [],
      albums: [],
      users: [],
      heatmap: [],
    });
    render(<Listening query="?days=30&user=7&year=2026&tz=60" viewers={[]} />);
    expect(await screen.findByText("Song · Artist")).toBeInTheDocument();
    expect(apiFetch).toHaveBeenCalledWith(
      "/pse/music/listening?days=30&user=7&year=2026&tz=60",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(screen.getByText(/Actual listening time is not measured/)).toBeInTheDocument();
    expect(screen.queryByText("Hours watched")).toBeNull();
  });
  it("selects latest mixed rows before pagination and preserves unique IDs", () => {
    const video = {
      id: "1",
      userId: 7,
      username: "Alice",
      title: "Film",
      device: "",
      platform: "",
      decision: "",
      started: 900,
      ended: 0,
      bytes: 0,
      watchedMs: 0,
      completed: false,
      failovers: 0,
      error: "",
    };
    const music = Array.from({ length: 500 }, (_, i) => ({
      id: i + 1,
      viewerId: 7,
      title: `Song ${i}`,
      artist: "Artist",
      album: "Album",
      duration: 120,
      playedAt: i,
    }));
    const result = combineHistory([video], music, []);
    expect(result).toHaveLength(500);
    expect(result[0]?.id).toBe("1");
    expect(result[1]?.id).toBe("music:500");
    expect(result.some((p) => p.id === "music:1")).toBe(false);
    expect(music[0]?.id).toBe(1);
  });
});
