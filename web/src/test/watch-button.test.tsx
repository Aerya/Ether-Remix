import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WatchButton } from "../components/WatchButton";

const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch,
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("WatchButton", () => {
  it("opens a same-origin player path on the origin the console was loaded from", async () => {
    // Behind a reverse proxy or tunnel Ether answers with a path, never the
    // loopback host it saw.
    apiFetch.mockImplementation(async (_path: string, options?: RequestInit) => ({
      available: true,
      players: [{ id: "vela", url: options?.method === "POST" ? "/vela/#code=abc" : "/vela/" }],
    }));
    const replace = vi.fn();
    const tab = { opener: {}, location: { replace }, close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    render(<WatchButton />);
    fireEvent.click(await screen.findByRole("button", { name: "Open the web player" }));
    await waitFor(() => expect(replace).toHaveBeenCalled());
    const target = new URL(replace.mock.calls[0]?.[0] as string);
    expect(target.origin).toBe(window.location.origin);
    expect(target.pathname).toBe("/vela/");
    const args = new URLSearchParams(target.hash.slice(1));
    expect(args.get("code")).toBe("abc");
    expect(args.get("console")).toBe(window.location.origin + "/ether/");
  });
});
