import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { MusicDigarr } from "./MusicDigarr";
const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
beforeEach(() => {
  apiFetch.mockReset();
});
const viewers = [
  { id: 7, name: "Alice" },
  { id: 8, name: "Bob" },
];
const page = {
  configured: true,
  total: 1,
  items: [
    { id: 12, kind: "album", artist: { name: "Example" }, recommendedReleaseGroupTitle: "Album" },
  ],
};
it("resolves an album and imports it only for the selected viewer", async () => {
  apiFetch.mockImplementation((path: string) => {
    if (path.endsWith("/import")) return Promise.resolve({ tracks: 5 });
    if (path.includes("/albums?"))
      return Promise.resolve([{ id: 31, title: "Album", artist: { name: "Example" } }]);
    return Promise.resolve(page);
  });
  render(<MusicDigarr viewers={viewers} />);
  fireEvent.click(
    await screen.findByRole("button", { name: /Retrouver dans Remix|Find in Remix/ }),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: /Créer une playlist de cet album|Create a playlist from this album/,
    }),
  );
  await screen.findByText(/Playlist créée : 5|Playlist created: 5/);
  expect(apiFetch).toHaveBeenCalledWith(
    "/pse/music/playlists/digarr/import",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ viewer: 7, recommendation: 12, album: 31 }),
    }),
  );
  expect(
    screen.getByRole("button", { name: /^Playlist créée$|^Playlist created$/ }),
  ).toBeDisabled();
});
it("discards a late response when switching viewers", async () => {
  let resolveAlice!: (p: typeof page) => void;
  apiFetch.mockImplementation((path: string) =>
    path.includes("viewer=7")
      ? new Promise((resolve) => {
          resolveAlice = resolve;
        })
      : Promise.resolve({ configured: false, total: 0, items: [] }),
  );
  render(<MusicDigarr viewers={viewers} />);
  fireEvent.change(screen.getByLabelText(/Spectateur|Viewer/), { target: { value: "8" } });
  await screen.findByText(/Connecteur facultatif non configuré|Optional connector not configured/);
  resolveAlice(page);
  await waitFor(() => expect(screen.queryByText("Album — Example")).not.toBeInTheDocument());
  expect(apiFetch).toHaveBeenCalledWith("/pse/music/playlists/digarr?viewer=8&offset=0");
});
it("reports failure and retries without treating it as an empty result", async () => {
  apiFetch
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValue({ configured: true, total: 0, items: [] });
  render(<MusicDigarr viewers={viewers} />);
  await screen.findByRole("alert");
  expect(screen.queryByText(/Aucune recommandation|No recommendations/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /Actualiser|Refresh/ }));
  await screen.findByText(/Aucune recommandation|No recommendations/);
});
