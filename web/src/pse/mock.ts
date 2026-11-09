import type { Descriptor } from "../lib/extend";
const text = (fr: string, en = fr) => ({ fr, en });
const demoField = (key: string, fr: string, en: string, kind = "toggle", extra = {}) => ({
  key,
  kind,
  label: text(fr, en),
  help: text(""),
  default: kind === "toggle" ? "off" : "",
  value: kind === "toggle" ? "off" : "",
  source: "console",
  ...extra,
});
const demoFeatures = [
  {
    id: "music",
    title: text("Musique", "Music"),
    description: text(""),
    status: { active: false },
    fields: [
      demoField(
        "PSE_MUSIC_ECLIPSE_URL",
        "Manifest Eclipse personnel",
        "Personal Eclipse manifest",
        "url",
      ),
      demoField("PSE_MUSIC_TIDO_URL", "Tido", "Tido", "url", { community: true }),
      demoField("PSE_MUSIC_INTERNETARCHIVE_URL", "Internet Archive", "Internet Archive", "url", {
        community: true,
      }),
      demoField("PSE_MUSIC_ULTRAMAX_URL", "UltraMax", "UltraMax", "url", { community: true }),
      demoField("PSE_MUSIC_DEEZER_RICKY_URL", "Deezer · Ricky", "Deezer · Ricky", "url", {
        community: true,
      }),
      demoField("PSE_MUSIC_SOUNDCLOUD_URL", "SoundCloud · Eclipse", "SoundCloud · Eclipse", "url", {
        community: true,
      }),
      demoField(
        "PSE_MUSIC_SOUNDCLOUD_RICKY_URL",
        "SoundCloud · Ricky",
        "SoundCloud · Ricky",
        "url",
        { community: true },
      ),
      demoField(
        "PSE_MUSIC_ADDON_REGISTRY",
        "Actualiser les liens des fournisseurs",
        "Refresh provider setup links",
        "toggle",
        { advanced: true },
      ),
      demoField("PSE_MUSIC_DMM", "DMM · Real-Debrid", "DMM · Real-Debrid"),
      demoField(
        "PSE_MUSIC_REALDEBRID",
        "Real-Debrid par indexeurs",
        "Real-Debrid through indexers",
      ),
      demoField("PSE_MUSIC_ALLDEBRID", "AllDebrid", "AllDebrid"),
      demoField("PSE_MUSIC_TORBOX", "TorBox", "TorBox"),
      demoField("PSE_MUSIC_AUDIUS", "Audius", "Audius", "select", {
        options: [
          { value: "off", label: text("Désactivé", "Off") },
          { value: "fallback", label: text("En repli", "Fallback") },
        ],
      }),
      demoField("PSE_MUSIC_HIFI_URL", "HiFi API", "HiFi API", "url", { advanced: true }),
    ],
  },
  {
    id: "indexer",
    title: text("Indexation en arrière-plan", "Background indexing"),
    description: text(""),
    fields: [
      demoField("PSE_INDEXER_SOURCES", "Sources importées", "Imported sources", "multi", {
        options: [],
      }),
    ],
  },
];
/** Réponses minimales du fork pour le serveur de démonstration Ether. */
export function remixMock(route: string): Response | undefined {
  if (route === "GET /pse" || route === "PUT /pse")
    return Response.json({ features: demoFeatures });
  if (route.startsWith("POST /debrid/providers/") && route.endsWith("/key"))
    return Response.json({ key: "demo-api-key" });
  if (route === "GET /pse/music/addons" || route === "POST /pse/music/addons")
    return Response.json({ addons: [], updatedAt: "" });
  if (route === "GET /pse/themes" || route === "GET /pse/themes/")
    return Response.json({ libraries: [], collections: [], presets: [], max: 99 });
  if (route === "GET /pse/themes/priorities" || route === "GET /pse/themes/priority")
    return Response.json({ priorities: [], total: 0, max: 99 });
  if (route === "GET /pse/themes/vostfr") return Response.json({ keys: [], max: 99 });
  return undefined;
}

export const remixDescriptors: Descriptor[] = [
  {
    kind: "page",
    id: "pse",
    owner: "builtin",
    label: { source: "Ether-Remix" },
    icon: "Languages",
    group: "",
    order: 15,
    meta: { route: "/accent", navGroup: "", inNav: true },
  },
  {
    kind: "navItem",
    id: "pse",
    owner: "builtin",
    label: { source: "Ether-Remix" },
    icon: "Languages",
    group: "",
    order: 15,
    meta: { route: "/accent", page: "pse" },
  },
];
