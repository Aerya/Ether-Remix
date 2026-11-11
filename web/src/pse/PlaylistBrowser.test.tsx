import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { PlaylistBrowser } from "./PlaylistBrowser";
const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
const viewers = [
  { id: 7, name: "Owner" },
  { id: 8, name: "Kid" },
];
it("previews then confirms for the chosen viewer, without importing during discovery", async () => {
  apiFetch.mockImplementation(async (path: string) => {
    if (path.includes("discover?"))
      return {
        items: [{ name: "Selection", url: "https://www.deezer.com/playlist/1" }],
        more: false,
      };
    if (path.endsWith("preview"))
      return {
        token: "preview-token",
        name: "Selection",
        source: "deezer",
        tracks: [{ title: "Song", artist: "Artist" }],
        total: 1,
        skipped: 0,
      };
    return { id: "job", done: true, processed: 1, total: 1, matched: 1, missing: [] };
  });
  render(<PlaylistBrowser viewers={viewers} mode="discover" refresh={() => {}} />);
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "8" } });
  fireEvent.click(
    screen.getByRole("button", { name: /Search \/ trending|Rechercher \/ tendances/ }),
  );
  fireEvent.click(await screen.findByRole("button", { name: /Preview|Aperçu/ }));
  await screen.findByText("Artist — Song");
  expect(apiFetch.mock.calls.some(([path]) => String(path).endsWith("/import"))).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: /Import for Kid|Importer pour Kid/ }));
  await waitFor(() =>
    expect(apiFetch.mock.calls.some(([path]) => String(path).endsWith("/import"))).toBe(true),
  );
  const call = apiFetch.mock.calls.find(([path]) => String(path).endsWith("/import"))!;
  expect(JSON.parse(call[1].body)).toEqual({ viewer: 8, token: "preview-token" });
});
it("displays provider failures without claiming an empty playlist", async () => {
  apiFetch.mockRejectedValue(new Error("Spotify : quota atteint"));
  render(
    <PlaylistBrowser
      viewers={[{ id: 7, name: "Owner", spotify: true }]}
      mode="discover"
      refresh={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Spotify" }));
  fireEvent.click(screen.getByRole("button", { name: /My playlists|Mes playlists/ }));
  expect(await screen.findByRole("alert")).toHaveTextContent("quota atteint");
  expect(screen.queryByText(/No playlists found|Aucune playlist trouvée/)).toBeNull();
});

it("rejects an OAuth callback not initiated in this browser", async () => {
  apiFetch.mockClear();
  window.history.replaceState(
    null,
    "",
    "/ether/remix/contenus?content=musique&code=foreign&state=foreign",
  );
  sessionStorage.removeItem("remix.spotify.pending");
  render(<PlaylistBrowser viewers={viewers} mode="discover" refresh={() => {}} />);
  expect(await screen.findByRole("alert")).toHaveTextContent(/expired|expiré/);
  expect(apiFetch).not.toHaveBeenCalled();
  expect(window.location.search).not.toContain("code=");
  window.history.replaceState(null, "", "/");
});
