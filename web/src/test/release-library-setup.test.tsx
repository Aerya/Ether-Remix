import { fireEvent, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { renderAppWith } from "./routes";
it("saves the release library from quick start and exposes its view only after saving", async () => {
  let enabled = false;
  const doc = () => ({
    features: [
      {
        id: "indexer",
        title: { fr: "Indexation", en: "Indexing" },
        description: { fr: "", en: "" },
        fields: [
          {
            key: "PSE_RELEASE_LIBRARY",
            kind: "toggle",
            value: enabled ? "on" : "off",
            default: "off",
            label: { fr: "Bibliothèque de releases (Bêta)", en: "Release library (Beta)" },
            help: { fr: "", en: "" },
          },
        ],
      },
    ],
  });
  renderAppWith("/remix/demarrage", (method, path, body) => {
    if (path === "/pse/") {
      if (method === "PUT") {
        expect(body).toEqual({ values: { PSE_RELEASE_LIBRARY: "on" } });
        enabled = true;
      }
      return doc();
    }
  });
  const toggle = await screen.findByRole("switch", { name: "Release library (Beta)" });
  fireEvent.click(toggle);
  fireEvent.click(screen.getByRole("button", { name: /Enregistrer|Save/ }));
  await waitFor(() => expect(enabled).toBe(true));
  fireEvent.click(screen.getByRole("tab", { name: "Indexing" }));
  expect(await screen.findByRole("button", { name: "Releases" })).toBeVisible();
});
