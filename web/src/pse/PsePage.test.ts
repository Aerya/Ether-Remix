import { describe, expect, it } from "vitest";
import { featuresOfTab, formatCriteria, parseCriteria } from "./PsePage";

describe("critères du catalogue lisible", () => {
  it("relit et réécrit le filtre PSE_PLAYABLE_FILTER", () => {
    const c = parseCriteria("lang=fr,qc;min=1080p;codec=x265,h264;cached=1;foo=bar");
    expect(c).toEqual({
      lang: ["fr", "vfq"],
      min: "1080",
      max: "",
      codec: ["hevc", "h264"],
      cached: true,
      other: ["foo=bar"],
    });
    expect(formatCriteria(c)).toBe("lang=fr,vfq;min=1080;codec=hevc,h264;cached=1;foo=bar");
  });

  it("laisse un filtre vide vide", () => {
    expect(formatCriteria(parseCriteria(""))).toBe("");
  });
});

describe("onglets de la page Ether-Remix", () => {
  const f = (id: string) => ({
    id,
    title: { fr: id, en: id },
    description: { fr: "", en: "" },
    fields: [],
  });
  const all = [
    "notify",
    "french",
    "indexer",
    "playable",
    "scrob",
    "music",
    "posters",
    "nouveau",
  ].map(f);
  it("range chaque section dans son onglet", () => {
    expect(featuresOfTab("lecture", all).map((x) => x.id)).toEqual(["french", "playable"]);
    expect(featuresOfTab("contenus", all).map((x) => x.id)).toEqual(["music"]);
    expect(featuresOfTab("visuels", all).map((x) => x.id)).toEqual(["posters"]);
    expect(featuresOfTab("indexation", all).map((x) => x.id)).toEqual(["indexer"]);
    expect(featuresOfTab("sources", all)).toEqual([]);
    expect(featuresOfTab("apercu", all)).toEqual([]);
  });
  it("n’affiche une section inconnue dans aucun onglet", () => {
    for (const tab of [
      "apercu",
      "sources",
      "indexation",
      "lecture",
      "contenus",
      "visuels",
      "suivi",
    ] as const)
      expect(featuresOfTab(tab, all).map((x) => x.id)).not.toContain("nouveau");
    expect(featuresOfTab("suivi", all).map((x) => x.id)).toEqual(["notify", "scrob"]);
  });
});
