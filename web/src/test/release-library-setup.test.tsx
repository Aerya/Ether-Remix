import { fireEvent, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { renderAppWith } from "./routes";

it("exposes stored releases with automatic search off and no beta switch", async () => {
  renderAppWith("/remix/indexation", (_method, path) => {
    if (path === "/pse/release-library")
      return {
        enabled: true,
        references: 0,
        ready: 0,
        expired: 0,
        failed: 0,
        sourceChanged: 0,
        metadata: 0,
        titles: 0,
        lastSeen: 0,
        items: [],
        total: 0,
        page: 0,
      };
    if (path === "/pse/")
      return {
        features: [
          {
            id: "indexer",
            title: { fr: "Indexation", en: "Indexing" },
            description: { fr: "", en: "" },
            fields: [
              {
                key: "PSE_INDEXER",
                kind: "toggle",
                value: "off",
                default: "off",
                label: {
                  fr: "Rechercher automatiquement des releases",
                  en: "Automatically search for releases",
                },
                help: { fr: "", en: "" },
              },
            ],
          },
        ],
      };
  });
  expect(
    await screen.findByRole("switch", { name: "Automatically search for releases" }),
  ).not.toBeChecked();
  expect(
    screen.queryByRole("switch", { name: /Release library|Bibliothèque de releases/ }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Releases" }));
  expect(
    await screen.findByRole("heading", { name: /Release index|Index des releases/ }),
  ).toBeVisible();
});
