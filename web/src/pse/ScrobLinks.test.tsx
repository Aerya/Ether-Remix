import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
