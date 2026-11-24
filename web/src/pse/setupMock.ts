// In-browser demo only; excluded from production with the existing mock server.
const fields = [
  ["PSE_FRENCH", "Catalogue français", "French catalogue", "off"],
  ["PSE_FRENCH_RELEASES", "Marqueurs de releases", "Release tags", "french,multi"],
  ["PSE_PLAYABLE", "Catalogue lisible", "Playable catalogue", "off"],
  ["PSE_PLAYABLE_MIN", "Minimum de releases", "Minimum releases", "1"],
  ["PSE_PLAYABLE_SINGLE", "Afficher les titres à une release", "Show single-release titles", "off"],
  ["PSE_PLAYABLE_PENDING", "Inclure les titres en attente", "Include pending titles", "hide"],
  ["PSE_INDEXER", "Recherches de fond", "Background search", "off"],
  ["PSE_PREDB", "Annonces pre", "Pre announcements", "off"],
  ["PSE_LIBRARY_SORT", "Tri des bibliothèques", "Library sort", "title"],
  ["PSE_POSTER_REFRESH_HOURS", "Cache des affiches", "Poster cache", "24"],
].map(([key, fr, en, value]) => ({
  key: key!,
  label: { fr: fr!, en: en! },
  value: value!,
  source: "default",
}));
let revision = 1;
export function setupMock(route: string, raw: unknown): Response | undefined {
  if (route === "GET /pse/setup")
    return Response.json({ revision: String(revision), fields, warnings: [] });
  if (!["POST /pse/setup/preview", "POST /pse/setup/apply"].includes(route)) return undefined;
  const body = raw as { revision?: string; values?: Record<string, string | null>; plan?: string };
  if (!body || body.revision !== String(revision))
    return Response.json({ error: "État modifié" }, { status: 409 });
  const values = body.values ?? {};
  if (Object.keys(values).some((key) => !fields.some((field) => field.key === key)))
    return Response.json({ error: "Réglage inconnu" }, { status: 422 });
  const changes = Object.entries(values).flatMap(([key, value]) => {
    const field = fields.find((f) => f.key === key)!;
    return value !== null && value !== field.value
      ? [{ key, label: field.label, before: field.value, after: value, reset: false }]
      : [];
  });
  const plan = JSON.stringify([revision, values]);
  if (route.endsWith("/preview"))
    return Response.json({ revision: String(revision), plan, values, changes, warnings: [] });
  if (body.plan !== plan) return Response.json({ error: "Aperçu différent" }, { status: 409 });
  const undo: Record<string, string> = {};
  for (const change of changes) {
    const field = fields.find((f) => f.key === change.key)!;
    undo[field.key] = field.value;
    field.value = change.after;
  }
  revision++;
  return Response.json({ revision: String(revision), undo, changes });
}
