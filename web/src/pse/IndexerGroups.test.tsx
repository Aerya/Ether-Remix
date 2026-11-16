import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { IndexerGroups } from "./IndexerGroups";
vi.mock("../lib/api", () => ({
  api: {
    getConfig: async () => ({
      indexers: [
        {
          id: "a",
          protocol: "torrent",
          url: "https://prowlarr.example/1/api?apikey=secret",
          prowlarrImport: {},
        },
        {
          id: "b",
          protocol: "torrent",
          url: "https://prowlarr.example/2/api?apikey=secret",
          prowlarrImport: {},
        },
        {
          id: "c",
          protocol: "torrent",
          url: "https://jackett.example/api/v2.0/indexers/site/results/torznab",
        },
        { id: "d", kind: "stremio", url: "https://addon.example/manifest.json" },
      ],
    }),
  },
}));
vi.mock("../pages/sources/Indexers", () => ({
  IndexerList: ({ originKey }: { originKey?: string }) => <output>{originKey || "all"}</output>,
}));
it("groups imported indexers by instance without exposing credentials or including addons", async () => {
  render(<IndexerGroups view="indexers" />);
  await waitFor(() => expect(screen.getByLabelText("Instance or origin")).toBeTruthy());
  fireEvent.click(screen.getByLabelText("Instance or origin"));
  const group = await screen.findByRole("option", { name: "Prowlarr · prowlarr.example (2)" });
  expect(screen.getByRole("option", { name: "Jackett · jackett.example (1)" })).toBeTruthy();
  expect(document.body.textContent).not.toContain("secret");
  expect(document.body.textContent).not.toContain("addon.example");
  fireEvent.click(group);
  expect(screen.getByText("Prowlarr:https://prowlarr.example")).toBeTruthy();
});
