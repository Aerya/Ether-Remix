import type { StyleTarget } from "./DiscoveryCardStyles";

export interface PackImage {
  name: string;
  url: string;
  group?: string;
}
export interface PackSource {
  id: string;
  name: string;
  page: string;
  url: string;
  repo?: string;
  style?: string;
}
const github = (id: string, name: string, repo: string, style?: string): PackSource => ({
  id,
  name,
  repo,
  style,
  page: `https://github.com/${repo}`,
  url: `https://api.github.com/repos/${repo}/git/trees/HEAD?recursive=1`,
});
export const PACK_SOURCES: PackSource[] = [
  github("nuvio", "Nuvio Assets · paysage", "rrevanth/nuvio-assets"),
  ...[
    "streaming/color",
    "streaming/white",
    "streaming/logos",
    "network/color",
    "network/white",
    "network/logos",
    "genre",
    "studio",
  ].map((path) => ({
    id: "kometa-" + path,
    name: "Kometa · " + path,
    style: path,
    page: "https://github.com/Kometa-Team/Default-Images/tree/master/" + path,
    url: "https://api.github.com/repos/Kometa-Team/Default-Images/contents/" + path + "?ref=master",
  })),
  github("legacy", "nichtlegacy · logos", "nichtlegacy/kometa-config-images"),
  github("kaptain", "Kaptain · genres", "ImKaptain/nuvio-assets"),
  ...[
    "",
    "-neon",
    "-frost",
    "-minimal",
    "-solid",
    "-light",
    "-black-white",
    "-white-black",
    "-icon-dark",
    "-icon-light",
    "-neon-icon",
    "-split",
    "-text-black",
    "-text-white",
  ].map((style) => ({
    id: "fr" + style,
    name: "FR-Badges · " + (style.slice(1) || "couleur"),
    page: "https://farizmohd70.github.io/FR-Badges/",
    url: `https://raw.githubusercontent.com/farizmohd70/FR-Badges/main/badges${style}.json`,
  })),
  {
    id: "ultramax",
    name: "UltraMax · collections",
    page: "https://github.com/PaRaN01a-hash/UltraMax",
    url: "https://raw.githubusercontent.com/PaRaN01a-hash/UltraMax/main/addon/collections.json",
  },
  {
    id: "tvflix",
    name: "TVFlix / Nuvio · collections",
    page: "https://github.com/ParticularCatch449/Nuvio",
    url: "https://raw.githubusercontent.com/ParticularCatch449/Nuvio/main/src/data/nuvioCollections.json",
  },
];

export function safeImageURL(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length > 8192) return;
  try {
    const u = new URL(value);
    if (u.protocol === "https:" && !u.username && !u.password && !u.hash) return u.href;
  } catch {
    /* Ignore invalid entries. */
  }
}
const groupName = (name: string): string | undefined => {
  if (/genre/i.test(name)) return "genres";
  if (/studio/i.test(name)) return "studios";
  if (/streaming|services|networks|platforms|channels/i.test(name)) return "services";
};

// Read image metadata only. Never import addons, credentials, filters or executable regexes.
export function parsePack(value: unknown, source?: PackSource): PackImage[] {
  const out: PackImage[] = [];
  const add = (name: unknown, raw: unknown, group?: string) => {
    const url = safeImageURL(raw);
    if (url && typeof name === "string" && name.trim() && name.length <= 200) {
      if (out.length >= 2000)
        throw new Error("Pack limité à 2000 visuels / pack limited to 2000 images");
      out.push({ name: name.trim(), url, group });
    }
  };
  if (source?.id.startsWith("kometa-") && Array.isArray(value)) {
    const group = source.style?.startsWith("genre")
      ? "genres"
      : source.style?.startsWith("studio")
        ? "studios"
        : "services";
    for (const item of value) {
      if (
        !item ||
        typeof item.name !== "string" ||
        item.type !== "file" ||
        item.name.startsWith("!") ||
        /_(movies|shows|originals)\./.test(item.name)
      )
        continue;
      add(item.name.replace(/\.[^.]+$/, ""), item.download_url, group);
    }
  } else if (source?.repo && value && typeof value === "object" && "tree" in value) {
    const tree = value as { tree: { path: string; type: string }[]; truncated?: boolean };
    if (tree.truncated || !Array.isArray(tree.tree))
      throw new Error("Catalogue incomplet / incomplete catalogue");
    for (const item of tree.tree) {
      if (item.type !== "blob" || typeof item.path !== "string") continue;
      const path = item.path;
      if (!/\.(png|jpg|jpeg|webp|gif)$/i.test(path)) continue;
      let name = path
        .split("/")
        .pop()!
        .replace(/\.[^.]+$/, "");
      let group: string | undefined;
      if (source.id === "nuvio") {
        group = (
          { streaming: "services", studios: "studios", genres: "genres" } as Record<string, string>
        )[path.split("/")[0]!];
        if (!group || !/-landscape\.(png|jpg|jpeg|webp|gif)$/.test(path)) continue;
        name = name.replace(/-landscape$/, "");
      } else if (source.id === "kometa") {
        group = (
          {
            streaming: "services",
            studio: "studios",
            genre: "genres",
            network: "services",
          } as Record<string, string>
        )[path.split("/")[0]!];
        if (!group || path.split("/").length !== 2) continue;
      } else if (source.id === "legacy") {
        if (!path.startsWith("overlays/images/streaming_clean/")) continue;
        group = "services";
      } else if (source.id === "kaptain") {
        if (!/^Genres\/[^/]+\/[^/]+_Base\.png$/.test(path)) continue;
        group = "genres";
        name = path.split("/")[1]!;
      }
      add(
        name.replace(/[_-]/g, " "),
        `https://raw.githubusercontent.com/${source.repo}/HEAD/${path.split("/").map(encodeURIComponent).join("/")}`,
        group,
      );
    }
  } else {
    let nodes = 0;
    const visit = (node: unknown, group?: string, depth = 0) => {
      if (depth > 12 || ++nodes > 12000) throw new Error("Pack trop complexe / pack too complex");
      if (Array.isArray(node)) {
        for (const child of node) visit(child, group, depth + 1);
        return;
      }
      if (!node || typeof node !== "object") return;
      const o = node as Record<string, unknown>;
      const title = typeof o.title === "string" ? o.title : o.name;
      const g = ["services", "genres", "studios"].includes(String(o.group))
        ? String(o.group)
        : group;
      if (o.imageURL && o.groupId !== "streaming") return; // FR-Badges: keep service logos only.
      add(title, o.coverImageUrl ?? o.imageURL ?? o.url, o.imageURL ? "services" : g);
      for (const key of ["collections", "folders", "images", "filters", "items", "data", "pack"]) {
        if (o[key])
          visit(
            o[key],
            (key === "folders" && typeof title === "string" ? groupName(title) : undefined) ?? g,
            depth + 1,
          );
      }
    };
    visit(value);
  }
  return out.filter(
    (x, i, a) =>
      a.findIndex((y) => y.url === x.url && y.name === x.name && y.group === x.group) === i,
  );
}

const aliases: Record<string, string> = {
  amazonprimevideo: "primevideo",
  amazonprime: "primevideo",
  prime: "primevideo",
  appletvplus: "appletv",
  hbomax: "max",
  maxhbomax: "max",
  disneyplus: "disney",
  paramountplus: "paramount",
  sciencefiction: "scifi",
  comedie: "comedy",
  drame: "drama",
  fantastique: "fantasy",
  policier: "crime",
  mystere: "mystery",
  aventure: "adventure",
  documentaire: "documentary",
  famille: "family",
  histoire: "history",
  horreur: "horror",
  musique: "music",
  guerre: "war",
  marvelstudios: "marvel",
  studioghibli: "ghibli",
  warnerbros: "warner",
  universalpictures: "universal",
};
export function packName(name: string): string {
  const key = name
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "")
    .toLowerCase()
    .replace(/[^a-z0-9]/g, "");
  return aliases[key] ?? key;
}
export function packMatches(images: PackImage[], target: StyleTarget): PackImage[] {
  return images.filter(
    (i) => (!i.group || i.group === target.group) && packName(i.name) === packName(target.name),
  );
}
