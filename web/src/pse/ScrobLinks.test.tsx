import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ScrobLinks } from "./ScrobLinks";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));

const masked = "https://scrob.lan/api/proxy/webhooks/jellyfin/1?api_key=••••••";

it("lists every viewer with its masked Scrob link and saves a new one", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    calls.push({ path, init });
    if (path === "/pse/scrob/test") return { ok: true, message: "Scrob accepted" };
    return {
      enabled: true,
      viewers: [
        {
          id: 1,
          name: "Owner",
          url: masked,
          configured: true,
          lastStatus: "ok",
          lastAt: 1800000000,
        },
        { id: 2, name: "Kid", url: "", configured: false },
      ],
    };
  });
  render(<ScrobLinks />);
  expect(await screen.findByText("Owner")).toBeTruthy();
  expect(screen.getByText("Kid")).toBeTruthy();
  const inputs = screen.getAllByRole("textbox") as HTMLInputElement[];
  expect(inputs[0]!.value).toBe(masked);
  expect(inputs[0]!.value).not.toContain("SECRET");
  expect(inputs[1]!.value).toBe("");

  fireEvent.change(inputs[1]!, {
    target: { value: "https://scrob.lan/api/proxy/webhooks/jellyfin/1?api_key=k2" },
  });
  const saves = screen.getAllByRole("button", { name: /Save|Enregistrer/ });
  fireEvent.click(saves[1]!);
  await waitFor(() => expect(calls.some((c) => c.init?.method === "PUT")).toBe(true));
  const put = calls.find((c) => c.init?.method === "PUT")!;
  expect(JSON.parse(String(put.init!.body))).toEqual({
    links: { "2": "https://scrob.lan/api/proxy/webhooks/jellyfin/1?api_key=k2" },
  });

  fireEvent.click(screen.getAllByRole("button", { name: /Test|Tester/ })[0]!);
  await waitFor(() => expect(calls.some((c) => c.path === "/pse/scrob/test")).toBe(true));
  const test = calls.find((c) => c.path === "/pse/scrob/test")!;
  expect(JSON.parse(String(test.init!.body))).toEqual({ viewer: 1, url: masked });
});

it("creates a viewer-scoped Jellyfin connection only on request and masks its token", async () => {
  apiFetch.mockReset();
  apiFetch.mockImplementation(async (path: string) => {
    if (path === "/viewers/7/apps/jellyfin")
      return {
        url: "https://remix.example/jellyfin",
        password: "private-grant",
        userId: "jellyfin-user-seven",
      };
    return { enabled: true, viewers: [{ id: 7, name: "Alice", url: "", configured: false }] };
  });
  render(<ScrobLinks />);
  const button = await screen.findByRole("button", {
    name: /Create Jellyfin credentials|Créer les identifiants Jellyfin/,
  });
  expect(apiFetch).not.toHaveBeenCalledWith("/viewers/7/apps/jellyfin", expect.anything());
  fireEvent.click(button);
  await screen.findByDisplayValue("https://remix.example/jellyfin");
  expect(apiFetch).toHaveBeenCalledWith("/viewers/7/apps/jellyfin", { method: "POST" });
  expect(screen.getByLabelText("Token").getAttribute("type")).toBe("password");
  expect((screen.getByLabelText("User ID") as HTMLInputElement).value).toBe("jellyfin-user-seven");
  expect((button as HTMLButtonElement).disabled).toBe(true);
});

it("refreshes Jellyfin activity without reloading or losing an unsaved webhook", async () => {
  vi.useFakeTimers();
  let seen = false;
  apiFetch.mockReset();
  apiFetch.mockImplementation(async () => ({
    enabled: true,
    viewers: [
      {
        id: 7,
        name: "Alice",
        url: "",
        configured: false,
        jellyfinLastAt: seen ? 1800000000 : undefined,
      },
    ],
  }));
  const mounted = render(<ScrobLinks />);
  try {
    await act(async () => {
      await Promise.resolve();
    });
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "https://scrob.example/private-draft" } });
    seen = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(screen.getByText(/Last Jellyfin access|Dernier accès Jellyfin/)).toBeTruthy();
    expect((input as HTMLInputElement).value).toBe("https://scrob.example/private-draft");
    expect(screen.getByText(/Webhook non configuré|Webhook not configured/)).toBeTruthy();
  } finally {
    mounted.unmount();
    vi.useRealTimers();
  }
});
