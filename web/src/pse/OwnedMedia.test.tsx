import { act, cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
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
  vi.unstubAllGlobals();
});
it("updates iBroadcast authorization and library without reloading", async () => {
  vi.useFakeTimers();
  let connected = false;
  apiFetch.mockImplementation(async (path: string) => {
    if (path.endsWith("/accounts"))
      return [
        {
          id: 2,
          name: "Personal",
          access: { ownerId: 7, sharedAll: false, viewers: [], group: "" },
          importPlaylists: false,
        },
      ];
    if (path.endsWith("/viewers")) return [];
    if (path.includes("/poll?")) {
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
  expect(apiFetch).toHaveBeenCalledWith("/pse/owned/ibroadcast/poll?account=2", { method: "POST" });
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

it("creates accounts without requesting global sharing", async () => {
  apiFetch.mockImplementation(async (path: string) => (path.endsWith("accounts") ? [] : []));
  render(<IBroadcastSource />);
  fireEvent.change(screen.getByRole("textbox", { name: /Nom du compte|Account name/ }), {
    target: { value: "Personal" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Ajouter un compte|Add account/ }));
  await waitFor(() =>
    expect(apiFetch).toHaveBeenCalledWith("/pse/owned/accounts", {
      method: "POST",
      body: JSON.stringify({ name: "Personal" }),
    }),
  );
});
it("uses the viewer portal identity and CSRF header for personal accounts", async () => {
  const fetcher = vi.fn(async () => ({ ok: true, json: async () => [] }));
  vi.stubGlobal("fetch", fetcher);
  render(<IBroadcastSource portalToken="fixture-viewer" />);
  fireEvent.change(screen.getByRole("textbox", { name: /Nom du compte|Account name/ }), {
    target: { value: "Mine" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Ajouter un compte|Add account/ }));
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      "/ether/api/me/media/accounts",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          Authorization: "Bearer fixture-viewer",
          "X-Ether-Request": "1",
        }),
      }),
    ),
  );
  expect(apiFetch).not.toHaveBeenCalled();
});
