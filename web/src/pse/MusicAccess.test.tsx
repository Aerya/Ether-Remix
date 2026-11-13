import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { MusicAccess } from "./MusicAccess";
import { apiFetch } from "../lib/api";
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));
vi.mock("../i18n", () => ({ getLocale: () => "fr" }));
let enabled = false;
beforeEach(() => {
  enabled = false;
  vi.mocked(apiFetch).mockReset();
  vi.mocked(apiFetch).mockImplementation(async (_path, options) => {
    if (options?.method === "POST") {
      enabled = true;
      return { password: "generated-music-secret" };
    }
    if (options?.method === "DELETE") {
      enabled = false;
      return { ok: true };
    }
    return [{ id: 7, name: "Alice", username: "music-7", enabled }];
  });
});
it("crée un accès replié par défaut, masque son secret et le retire après révocation", async () => {
  const { container } = render(<MusicAccess />);
  expect(container.querySelector("details")?.open).toBe(false);
  fireEvent.click(screen.getByText("Clients musicaux · Arpeggi / Subsonic"));
  fireEvent.click(await screen.findByRole("button", { name: "Créer l’accès" }));
  const password = await screen.findByDisplayValue("generated-music-secret");
  expect(password.getAttribute("type")).toBe("password");
  expect(apiFetch).toHaveBeenCalledWith("/pse/music/access", {
    method: "POST",
    body: '{"viewer":7}',
  });
  fireEvent.click(await screen.findByRole("button", { name: "Révoquer" }));
  await waitFor(() => expect(screen.queryByDisplayValue("generated-music-secret")).toBeNull());
});
