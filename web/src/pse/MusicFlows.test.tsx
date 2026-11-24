import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { MusicFlows } from "./MusicFlows";
const apiFetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));
beforeEach(() => {
  apiFetch.mockReset();
});
it("saves a schedule for the selected viewer and exposes optional AudioMuse only when configured", async () => {
  apiFetch.mockResolvedValue({ flows: [], audioMuse: false, busy: false });
  render(
    <MusicFlows
      viewers={[
        { id: 7, name: "Alice" },
        { id: 8, name: "Bob" },
      ]}
    />,
  );
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: /Save schedule|Enregistrer la programmation/ }),
    ).not.toBeDisabled(),
  );
  expect(screen.getByRole("option", { name: "AudioMuse" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText(/Viewer|Spectateur/), { target: { value: "8" } });
  await waitFor(() => expect(apiFetch).toHaveBeenCalledWith("/pse/music/playlists/flows?viewer=8"));
  fireEvent.change(screen.getByLabelText(/^Name$|^Nom$/), { target: { value: "Bob mix" } });
  fireEvent.click(
    screen.getByRole("button", { name: /Save schedule|Enregistrer la programmation/ }),
  );
  await waitFor(() => expect(apiFetch.mock.calls.some((c) => c[1]?.method === "PUT")).toBe(true));
  const request = apiFetch.mock.calls.find((c) => c[1]?.method === "PUT")!;
  expect(JSON.parse(request[1].body)).toMatchObject({
    viewer: 8,
    name: "Bob mix",
    mode: "mix",
    hours: 24,
    size: 30,
    enabled: true,
  });
});
it("shows failures and missing tracks, keeps the last success, and submits a manual refresh", async () => {
  const flow = {
    viewer: 7,
    ref: "daily",
    revision: 2,
    name: "Daily",
    mode: "playlist",
    hours: 24,
    size: 30,
    enabled: true,
    succeeded: 1800000000,
    matched: 12,
    missing: 3,
    error: "Actualisation impossible",
  };
  apiFetch.mockResolvedValue({ flows: [flow], audioMuse: true, busy: false });
  render(<MusicFlows viewers={[{ id: 7, name: "Alice" }]} />);
  await screen.findByText(/Daily/);
  expect(screen.getByRole("alert")).toBeTruthy();
  expect(screen.getByText(/12.*3/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /^Refresh$|^Actualiser$/ }));
  await waitFor(() =>
    expect(apiFetch).toHaveBeenCalledWith(
      "/pse/music/playlists/flows/run",
      expect.objectContaining({ method: "POST" }),
    ),
  );
});
it("loads accessible seed choices only for AudioMuse", async () => {
  apiFetch.mockImplementation(async (path: string) =>
    path.includes("/seeds?")
      ? [{ id: 4000000000001, name: "Artist — Seed" }]
      : { flows: [], audioMuse: true, busy: false },
  );
  render(<MusicFlows viewers={[{ id: 7, name: "Alice" }]} />);
  await waitFor(() => expect(screen.getByRole("option", { name: "AudioMuse" })).not.toBeDisabled());
  expect(apiFetch.mock.calls.some((c) => c[0].includes("/seeds?"))).toBe(false);
  fireEvent.change(screen.getByLabelText(/^Type$/), { target: { value: "audiomuse" } });
  await screen.findByRole("option", { name: "Artist — Seed" });
  expect(apiFetch).toHaveBeenCalledWith("/pse/music/playlists/flows/seeds?viewer=7");
});
