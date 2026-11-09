import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { MusicAddons } from "./MusicAddons";

const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));

it("refreshes public setup links without replacing the manifest input", async () => {
  const addon = {
    id: "tido",
    key: "TIDO",
    name: "Tido",
    setupUrl: "https://old.example.org/",
    account: false,
    listed: true,
    changed: false,
  };
  apiFetch.mockResolvedValueOnce({ addons: [addon], updatedAt: "2026-11-08T12:00:00Z" });
  render(
    <MusicAddons
      fields={[
        { key: "TIDO", control: <input aria-label="manifest" defaultValue="personal manifest" /> },
      ]}
    />,
  );
  expect((await screen.findByRole("link", { name: /Tido/ })).getAttribute("href")).toBe(
    addon.setupUrl,
  );
  apiFetch.mockResolvedValueOnce({
    addons: [{ ...addon, setupUrl: "https://new.example.org/", changed: true }],
    updatedAt: "2026-11-08T13:00:00Z",
  });
  fireEvent.click(screen.getByRole("button", { name: /Refresh setup links|Actualiser les liens/ }));
  await waitFor(() =>
    expect(screen.getByRole("link", { name: /Tido/ }).getAttribute("href")).toBe(
      "https://new.example.org/",
    ),
  );
  expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe("personal manifest");
  expect(screen.getByRole("status").textContent).toMatch(/Address differs|Adresse différente/);
  expect(apiFetch.mock.calls[1]).toEqual(["/pse/music/addons", { method: "POST" }]);
  apiFetch.mockRejectedValueOnce(new Error("unavailable"));
  fireEvent.click(screen.getByRole("button", { name: /Refresh setup links|Actualiser les liens/ }));
  await waitFor(() => expect(screen.getAllByRole("status").length).toBe(2));
  expect(screen.getByRole("link", { name: /Tido/ }).getAttribute("href")).toBe(
    "https://new.example.org/",
  );
});
