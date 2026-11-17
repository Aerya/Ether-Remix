import { waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { calls, renderApp } from "./harness";
import { pluginCollectionHref } from "../pages/collections/links";

describe("plugin catalogue collection handoff", () => {
  it("previews the selected plugin source rather than the default catalogue", async () => {
    const source = "plugin:community.jay-classic-movies-copy/movie-classics";
    const { fake } = renderApp(
      pluginCollectionHref(source, "Jay Classics & favourites"),
      {},
      (fake) => {
        const fetch = fake.fetch;
        fake.fetch = async (...args) => {
          const response = await fetch(...args);
          if (String(args[0]).includes("/extend/registry")) {
            const data = await response.json();
            data.descriptors.push({
              kind: "collectionSource",
              id: source,
              owner: "xperience",
              label: { source: "Adventure" },
              meta: {
                editor: "plugin",
                mediaTypes: ["show"],
                defaults: { params: { genre: "None" } },
              },
            });
            return new Response(JSON.stringify(data), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            });
          }
          return response;
        };
      },
    );
    await waitFor(
      () => {
        const previews = calls(fake, "POST", "/collections/preview");
        expect(previews.length).toBeGreaterThan(0);
        expect(previews.at(-1)?.body).toMatchObject({
          name: "Jay Classics & favourites",
          mediaType: "show",
          sources: [{ kind: source, params: { genre: "None" } }],
        });
      },
      { timeout: 10000 },
    );
  });
});
