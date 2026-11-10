import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { deezerLinks, MusicPlaylists } from "./MusicPlaylists";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));

it("shows stored Deezer accounts as links", () => {
  expect(deezerLinks("user:42 playlist:9")).toBe(
    "https://www.deezer.com/profile/42 https://www.deezer.com/playlist/9",
  );
});

it("lists each viewer's accounts and playlists, then saves a new account", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    calls.push({ path, init });
    return {
      lastfmKey: false,
      hours: 6,
      viewers: [
        {
          id: 1,
          name: "Owner",
          accounts: { deezer: { service: "deezer", account: "user:42", syncedAt: 1800000000 } },
          playlists: [{ id: 5, name: "Road trip · Deezer", source: "deezer", tracks: 12 }],
          syncing: false,
        },
        { id: 2, name: "Kid", accounts: {}, playlists: [], syncing: false },
      ],
    };
  });
  render(<MusicPlaylists />);
  await screen.findByRole("button", { name: /My accounts|Mes comptes/ });
  fireEvent.click(screen.getByRole("button", { name: /My accounts|Mes comptes/ }));
  expect(screen.getByText("Owner", { selector: "span" })).toBeTruthy();
  expect(screen.getByText("Road trip · Deezer (12)")).toBeTruthy();
  expect(screen.getByText(/Last\.fm API key|clé API Last\.fm/)).toBeTruthy();
  const inputs = screen.getAllByRole("textbox") as HTMLInputElement[];
  expect(inputs[0]!.value).toBe("https://www.deezer.com/profile/42");
  expect(inputs[2]!.value).toBe("");

  // Trois champs par spectateur : Deezer, Last.fm, ListenBrainz.
  fireEvent.change(inputs[4]!, { target: { value: "kid_fm" } });
  fireEvent.change(inputs[5]!, { target: { value: "https://listenbrainz.org/user/kid_lb" } });
  const saves = screen.getAllByRole("button", { name: /Save|Enregistrer/ });
  expect((saves[0] as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(saves[1]!);
  await waitFor(() => expect(calls.some((c) => c.init?.method === "PUT")).toBe(true));
  const put = calls.find((c) => c.init?.method === "PUT")!;
  expect(put.path).toBe("/pse/music/playlists/");
  expect(JSON.parse(String(put.init!.body))).toEqual({
    viewer: 2,
    lastfm: "kid_fm",
    listenbrainz: "https://listenbrainz.org/user/kid_lb",
  });

  fireEvent.click(screen.getAllByRole("button", { name: /Refresh|Mettre à jour/ })[0]!);
  await waitFor(() => expect(calls.some((c) => c.path === "/pse/music/playlists/sync")).toBe(true));
  const sync = calls.find((c) => c.path === "/pse/music/playlists/sync")!;
  expect(JSON.parse(String(sync.init!.body))).toEqual({ viewer: 1 });
});
