import { expect, it } from "vitest";
import { packMatches, parsePack, PACK_SOURCES } from "./artworkPacks";
const target = (name: string, group = "services") => ({ name, group, key: name, entity: 8 });
it("imports only covers from nested Nuvio exports, never addon credentials or GIF focus URLs", () => {
  const images = parsePack({
    collections: [
      {
        title: "Streaming Services",
        folders: [
          {
            title: "Netflix",
            coverImageUrl: "https://example.com/n.png",
            focusGifUrl: "https://example.com/f.gif",
            catalogSources: [{ url: "https://private.example/token" }],
          },
        ],
      },
    ],
  });
  expect(images).toEqual([
    { name: "Netflix", url: "https://example.com/n.png", group: "services" },
  ]);
});
it("rejects malformed URLs and keeps ambiguous variants for review", () => {
  const images = parsePack({
    images: [
      { name: "Netflix", url: "javascript:alert(1)" },
      { name: "Netflix", url: "https://user:pass@example.com/x" },
      { name: "Netflix", url: "https://example.com/a" },
      { name: "Netflix", url: "https://example.com/b" },
    ],
  });
  expect(packMatches(images, target("Netflix"))).toHaveLength(2);
  expect(packMatches(images, target("Netflix Standard with Ads"))).toHaveLength(0);
});
it("matches translated genres and aliases without mixing studios and services", () => {
  expect(
    packMatches(
      [{ name: "Comedy", url: "https://example.com/x", group: "genres" }],
      target("Comédie", "genres"),
    ),
  ).toHaveLength(1);
  expect(
    packMatches(
      [{ name: "Amazon Prime Video", url: "https://example.com/x", group: "services" }],
      target("Prime Video"),
    ),
  ).toHaveLength(1);
  expect(
    packMatches(
      [{ name: "Paramount", url: "https://example.com/x", group: "studios" }],
      target("Paramount+"),
    ),
  ).toHaveLength(0);
});
it("reads only streaming logos from FR-Badges, without evaluating its regexes", () => {
  expect(
    parsePack({
      filters: [
        {
          name: "Netflix",
          groupId: "streaming",
          imageURL: "https://example.com/n.png",
          pattern: "(?i)danger",
        },
        { name: "4K", groupId: "resolution", imageURL: "https://example.com/4.png" },
      ],
    }),
  ).toHaveLength(1);
});
it("selects landscape covers and ignores animated focus variants in GitHub trees", () => {
  const images = parsePack(
    {
      tree: [
        { type: "blob", path: "streaming/netflix/netflix-landscape.gif" },
        { type: "blob", path: "streaming/netflix/netflix.gif" },
        { type: "blob", path: "genres/comedy/comedy-landscape.png" },
      ],
    },
    PACK_SOURCES[0],
  );
  expect(images).toHaveLength(2);
  expect(images[0]?.name).toBe("netflix");
  expect(() => parsePack({ tree: [], truncated: true }, PACK_SOURCES[0])).toThrow();
});
