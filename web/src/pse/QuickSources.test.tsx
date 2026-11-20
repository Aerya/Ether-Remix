import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { SourceOrder } from "./SourceOrder";
import { api } from "../lib/api";
import type { EtherConfig } from "../lib/types";

vi.mock("../i18n", () => ({ getLocale: () => "fr" }));

describe("Limiter le recours aux débrideurs, voire préférer le téléchargement & streaming BitTorrent/Usenet", () => {
  it("reste désactivée par défaut et sauvegarde sélection, ordre et instance sans effacer les priorités", async () => {
    const cfg = {
      indexers: [
        { id: "seed", name: "Mon Seedstrem", kind: "stremio", enabled: true, priority: 1 },
      ],
      resolver: { sourceOrder: { families: ["torrent", "usenet"], content: { movie: ["seed"] } } },
      usenet: { providers: [] },
      debrid: { providers: [] },
    } as unknown as EtherConfig;
    vi.spyOn(api, "getConfig").mockResolvedValue(cfg);
    const save = vi.spyOn(api, "putConfig").mockResolvedValue(cfg);
    render(
      <MemoryRouter>
        <SourceOrder />
      </MemoryRouter>,
    );
    const mode = await screen.findByRole("switch", { name: "Configuration rapide" });
    expect(mode).not.toBeChecked();
    expect(screen.queryByLabelText("Usenet")).not.toBeInTheDocument();
    fireEvent.click(mode);
    fireEvent.click(screen.getByLabelText("Usenet"));
    fireEvent.click(screen.getByLabelText("BitTorrent — Seedstrem"));
    fireEvent.change(screen.getByLabelText("Instance Seedstrem"), { target: { value: "seed" } });
    fireEvent.click(screen.getByRole("button", { name: "Monter BitTorrent — Seedstrem" }));
    fireEvent.click(screen.getByRole("button", { name: "Monter BitTorrent — Seedstrem" }));
    expect(screen.getAllByText(/Non configurée — réglages requis/)).toHaveLength(3);
    expect(screen.getAllByRole("link", { name: "Paramètres" })).toHaveLength(4);
    fireEvent.click(screen.getByRole("button", { name: "Enregistrer" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0]?.[0].resolver?.sourceOrder).toEqual({
      families: ["torrent", "usenet"],
      content: { movie: ["seed"] },
      quick: {
        enabled: true,
        order: ["seedstrem", "usenet"],
        rank: ["alko", "seedstrem", "usenet", "torrent"],
        seedstremId: "seed",
      },
    });
    fireEvent.click(mode);
    fireEvent.click(screen.getByRole("button", { name: "Enregistrer" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
    expect(save.mock.calls[1]?.[0].resolver?.sourceOrder?.quick).toEqual({
      enabled: false,
      order: ["seedstrem", "usenet"],
      rank: ["alko", "seedstrem", "usenet", "torrent"],
      seedstremId: "seed",
    });
  });

  it("permet de réordonner librement toutes les sources, cochées ou non", async () => {
    const cfg = {
      indexers: [],
      resolver: { sourceOrder: { quick: { enabled: true, order: ["usenet", "alko"] } } },
      usenet: { providers: [] },
      debrid: { providers: [] },
    } as unknown as EtherConfig;
    vi.spyOn(api, "getConfig").mockResolvedValue(cfg);
    const save = vi.spyOn(api, "putConfig").mockResolvedValue(cfg);
    render(
      <MemoryRouter>
        <SourceOrder />
      </MemoryRouter>,
    );
    const torrent = "BitTorrent — client distant Ether-Remix";
    await screen.findByLabelText(torrent);
    // Une source non cochée peut monter tout en haut, puis être cochée à cette place.
    fireEvent.click(screen.getByRole("button", { name: `Monter ${torrent}` }));
    fireEvent.click(screen.getByRole("button", { name: `Monter ${torrent}` }));
    fireEvent.click(screen.getByLabelText(torrent));
    // Intervertir les deux premières sources cochées.
    fireEvent.click(screen.getByRole("button", { name: "Descendre Usenet" }));
    expect(screen.getByRole("button", { name: `Monter ${torrent}` })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Enregistrer" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0]?.[0].resolver?.sourceOrder?.quick).toEqual({
      enabled: true,
      order: ["torrent", "alko", "usenet"],
      rank: ["torrent", "alko", "usenet", "seedstrem"],
    });
  });
});
