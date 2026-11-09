import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { MusicSources } from "./MusicSources";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));

it("tests each source and shows the real format", async () => {
  apiFetch.mockResolvedValueOnce({
    track: "Stromae — Papaoutai",
    catalog: true,
    plan: [
      { name: "Addon Eclipse · a.example", kind: "eclipse", active: true, order: 1 },
      { name: "AllDebrid", kind: "alldebrid", active: true, order: 2 },
      {
        name: "HiFi API · h.example",
        kind: "hifi",
        active: false,
        reason: "Utilisée seulement quand aucune autre source n'est active",
      },
    ],
    sources: [
      {
        name: "Addon Eclipse · a.example",
        kind: "eclipse",
        priority: 1,
        ok: true,
        quality: "FLAC 16/44",
        format: "FLAC",
        lossless: true,
        bitDepth: 24,
        sampleRate: 96000,
        hiRes: true,
        ms: 1200,
        stage: "bytes",
      },
      {
        name: "AllDebrid",
        kind: "alldebrid",
        priority: 2,
        ok: false,
        lossless: false,
        error: "Titre absent",
        ms: 300,
      },
    ],
  });
  render(<MusicSources />);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Stromae Papaoutai" } });
  fireEvent.click(screen.getByRole("button", { name: /Test sources|Tester les sources/ }));
  expect((await screen.findAllByText("1. Addon Eclipse · a.example")).length).toBe(2);
  expect(screen.getByText(/FLAC · (lossless|sans perte)/)).toBeTruthy();
  expect(screen.getByText("Titre absent")).toBeTruthy();
  expect(screen.getByText(/24 bits · 96 kHz/)).toBeTruthy();
  await waitFor(() => expect(apiFetch).toHaveBeenCalled());
  const [path, init] = apiFetch.mock.calls[0]!;
  expect(path).toBe("/pse/music/diagnose");
  expect(JSON.parse((init as RequestInit).body as string)).toEqual({ query: "Stromae Papaoutai" });
  expect(screen.getByText(/HiFi API · h.example — Utilisée seulement/)).toBeTruthy();
  expect(
    screen.getAllByText(/Full playback: not tested|Lecture complète : non testée/),
  ).toHaveLength(2);

  // Nouvel essai ciblé : seule la source en échec est relancée.
  apiFetch.mockResolvedValueOnce({
    track: "Stromae — Papaoutai",
    catalog: true,
    sources: [
      {
        name: "AllDebrid",
        kind: "alldebrid",
        priority: 2,
        ok: true,
        format: "MP3",
        lossless: false,
        ms: 900,
        stage: "bytes",
      },
    ],
  });
  fireEvent.click(screen.getByRole("button", { name: /Retry this source|Réessayer cette source/ }));
  await waitFor(() => expect(screen.queryByText("Titre absent")).toBeNull());
  expect(JSON.parse((apiFetch.mock.calls[1]![1] as RequestInit).body as string)).toEqual({
    query: "Stromae Papaoutai",
    source: "AllDebrid",
  });
  expect(screen.getByText("MP3")).toBeTruthy();
});
