import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { IBroadcastSource, LocalMedia } from "./OwnedMedia";
const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  apiFetch.mockReset();
});
it("updates iBroadcast authorization and library without reloading", async () => {
  vi.useFakeTimers();
  let connected = false;
  apiFetch.mockImplementation(async (path: string) => {
    if (path.endsWith("/poll")) {
      connected = true;
      return { connected: true, tracks: 4, syncing: false };
    }
    return connected
      ? { connected: true, tracks: 4, syncing: false }
      : {
          connected: false,
          tracks: 0,
          userCode: "ABCD",
          url: "https://oauth.ibroadcast.com/device",
        };
  });
  await act(async () => {
    render(<IBroadcastSource />);
  });
  expect(screen.getByText(/4 (tracks|titres)/)).toBeTruthy();
  expect(screen.queryByText("ABCD")).toBeNull();
  expect(apiFetch).toHaveBeenCalledWith("/pse/owned/ibroadcast/poll", { method: "POST" });
  const calls = apiFetch.mock.calls.length;
  cleanup();
  await act(async () => {
    vi.advanceTimersByTime(10000);
  });
  expect(apiFetch.mock.calls.length).toBe(calls);
});
it("retains configured folders while showing scan errors", async () => {
  apiFetch.mockResolvedValue({
    roots: [{ id: "1", name: "Family", path: "/media/family", kind: "video" }],
    status: {
      running: false,
      scanned: 3,
      imported: 2,
      errors: 1,
      lastError: "Mount unavailable",
      finished: 0,
    },
  });
  render(<LocalMedia />);
  expect(await screen.findByText("Family")).toBeTruthy();
  expect(screen.getByText("Mount unavailable")).toBeTruthy();
  expect(screen.getByText(/\/media\/family/)).toBeTruthy();
});
