import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { apiFetch } from "../lib/api";
import { PeerStatus } from "./PeerStatus";
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
it("searches without downloading and queues only an explicitly selected result", async () => {
  vi.mocked(apiFetch)
    .mockResolvedValueOnce({
      ok: true,
      data: [{ id: "fixture-id", name: "Example.2024.mkv", size: 100 }],
    })
    .mockResolvedValue({ ok: true });
  render(<PeerStatus kind="amule" />);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Example" } });
  fireEvent.click(screen.getByRole("button", { name: /^(Rechercher|Search)$/ }));
  expect(await screen.findByText(/Example.2024.mkv/)).toBeTruthy();
  expect(apiFetch).toHaveBeenCalledTimes(1);
  expect(vi.mocked(apiFetch).mock.calls[0]?.[0]).toBe("/pse/peer/amule/search");
  fireEvent.click(screen.getByRole("button", { name: /^(Télécharger|Download)$/ }));
  expect(await screen.findByText(/Demande prise en compte|Request accepted/)).toBeTruthy();
  expect(vi.mocked(apiFetch).mock.calls[1]?.[1]?.body).toBe(JSON.stringify({ id: "fixture-id" }));
});
it("shows connection errors without announcing success", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ ok: false, message: "service P2P : HTTP 401" });
  render(<PeerStatus kind="slskd" />);
  fireEvent.click(screen.getByRole("button", { name: /Tester la connexion|Test connection/ }));
  expect(await screen.findByText("service P2P : HTTP 401")).toBeTruthy();
  expect(screen.queryByText(/Connexion réussie|Connection successful/)).toBeNull();
});
