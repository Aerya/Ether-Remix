import type { Descriptor } from "../lib/extend";
/** Réponses minimales du fork pour le serveur de démonstration Ether. */
export function remixMock(route: string): Response | undefined {
  if (route === "GET /pse" || route === "PUT /pse") return Response.json({ features: [] });
  if (route === "GET /pse/themes")
    return Response.json({ libraries: [], collections: [], presets: [], max: 99 });
  if (route === "GET /pse/themes/priorities") return Response.json({ priorities: [], max: 99 });
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
