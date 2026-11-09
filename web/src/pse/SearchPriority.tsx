import { Badge, Button, Input, Select, toast } from "@braedonsaunders/appkit-ui";
import { useEffect, useMemo, useState } from "react";
import { getLocale } from "../i18n";
import { api, apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import type { ThemeData } from "./ThemeLibraries";

/**
 * Priorité de recherche de releases, par collection ou bibliothèque
 * (internal/pse/themes/priority.go). Voir la section « Délais » du README.
 */

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const ENDPOINT = "/pse/themes/";
const VISIBLE = 50;
/** Prévient la liste qu'un import vient de changer les priorités. */
export const PRIORITY_CHANGED = "pse-search-priority-changed";

export type TargetKind = "collection" | "library";

interface PriorityRow {
  kind: TargetKind;
  id: string;
  level: number;
  titles: number;
}

interface PriorityData {
  priorities?: PriorityRow[];
  total?: number;
  max?: number;
}

export interface Target {
  kind: TargetKind;
  id: string;
  name: string;
  collections: string[];
  level: number;
  titles?: number;
}

export const levelOptions = () => [
  { value: 0, label: L("Normale", "Normal") },
  {
    value: 1,
    label: L(
      "Prioritaire (sans release : retente après 1 jour)",
      "High (no release: retry after 1 day)",
    ),
  },
  {
    value: 2,
    label: L(
      "Maximale (sans release : retente après 6 h)",
      "Maximum (no release: retry after 6 h)",
    ),
  },
];

/** Applique un niveau à une collection ou une bibliothèque. */
export function setSearchPriority(kind: TargetKind, id: string, level: number) {
  return apiFetch<PriorityData>(ENDPOINT + "priority", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ kind, id, level }),
  });
}

/** Cibles à afficher : celles priorisées d'abord, puis par nom. */
export function buildTargets(themes: ThemeData, prio: PriorityData, query: string): Target[] {
  const rows = new Map((prio.priorities ?? []).map((p) => [`${p.kind}:${p.id}`, p]));
  const targets: Target[] = [
    ...themes.libraries.map((l) => ({
      kind: "library" as const,
      id: String(l.id),
      name: l.name,
      collections: l.collections,
    })),
    ...themes.collections.map((c) => ({
      kind: "collection" as const,
      id: c.id,
      name: c.name,
      collections: [c.id],
    })),
  ].map((t) => {
    const row = rows.get(`${t.kind}:${t.id}`);
    return { ...t, level: row?.level ?? 0, titles: row?.titles };
  });
  const q = query.trim().toLowerCase();
  return targets
    .filter((t) => !q || t.name.toLowerCase().includes(q))
    .sort((a, b) => b.level - a.level || a.name.localeCompare(b.name));
}

export function SearchPriority() {
  const themes = useAsync(() => apiFetch<ThemeData>(ENDPOINT), []);
  const prio = useAsync(() => apiFetch<PriorityData>(ENDPOINT + "priority"), []);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const reloadThemes = themes.reload;
  const reloadPrio = prio.reload;

  useEffect(() => {
    const again = () => {
      reloadThemes();
      reloadPrio();
    };
    window.addEventListener(PRIORITY_CHANGED, again);
    return () => window.removeEventListener(PRIORITY_CHANGED, again);
  }, [reloadThemes, reloadPrio]);

  const targets = useMemo(
    () => (themes.data && prio.data ? buildTargets(themes.data, prio.data, query) : []),
    [themes.data, prio.data, query],
  );
  if (!themes.data || !prio.data) return null;

  const run = async (t: Target, action: () => Promise<string>) => {
    setBusy(`${t.kind}:${t.id}`);
    try {
      toast.success(await action());
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const changeLevel = (t: Target, level: number) =>
    run(t, async () => {
      await setSearchPriority(t.kind, t.id, level);
      prio.reload();
      return L(`Priorité de « ${t.name} » enregistrée`, `“${t.name}” priority saved`);
    });

  const searchNow = (t: Target) =>
    run(t, async () => {
      const res = await apiFetch<{ queued: number }>(ENDPOINT + "priority/search", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ kind: t.kind, id: t.id }),
      });
      return L(`${res.queued} titre(s) en tête de file`, `${res.queued} title(s) queued first`);
    });

  const refresh = (t: Target) =>
    run(t, async () => {
      for (const id of t.collections) await api.rebuildCollection(id);
      return L(`Actualisation de « ${t.name} » lancée`, `“${t.name}” refresh started`);
    });

  const total = prio.data.total ?? 0;
  const max = prio.data.max ?? 0;
  return (
    <details className="rounded-xl border border-border bg-surface">
      <summary className="cursor-pointer px-4 py-3 font-medium">
        {L("Priorité de recherche", "Search priority")}
      </summary>
      <div className="border-t border-border p-4">
        <p className="mb-4 text-sm text-fg-muted">
          {L(
            "Cherche les releases de certaines collections ou bibliothèques avant le reste. Les limites de chaque source restent appliquées.",
            "Searches releases for chosen collections or libraries before the rest. Each source’s limits still apply.",
          )}
        </p>
        <div className="space-y-3">
          <p className="text-sm">
            {L(`${total} titre(s) prioritaire(s)`, `${total} priority title(s)`)}
            {max > 0 && total > max
              ? L(
                  ` : seuls les ${max} premiers sont traités en priorité.`,
                  `: only the first ${max} are prioritized.`,
                )
              : ""}
          </p>
          <Input
            className="h-9 w-full max-w-md"
            aria-label={L("Filtrer", "Filter")}
            placeholder={L("Filtrer par nom…", "Filter by name…")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {targets.slice(0, VISIBLE).map((t) => {
            const key = `${t.kind}:${t.id}`;
            return (
              <div
                key={key}
                data-priority-target={key}
                className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border p-3"
              >
                <span className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium">
                  {t.name}
                  <Badge variant="secondary">
                    {t.kind === "library"
                      ? L("Bibliothèque", "Library")
                      : L("Collection", "Collection")}
                  </Badge>
                  {t.titles !== undefined ? (
                    <span className="text-xs font-normal text-fg-muted">
                      {L(`${t.titles} titre(s)`, `${t.titles} title(s)`)}
                    </span>
                  ) : null}
                </span>
                <span className="flex flex-wrap items-center gap-2">
                  <Select
                    className="h-9 w-72 max-w-full text-sm"
                    aria-label={L(`Priorité de ${t.name}`, `${t.name} priority`)}
                    value={String(t.level)}
                    disabled={busy !== null}
                    onChange={(e) => void changeLevel(t, Number(e.target.value))}
                  >
                    {levelOptions().map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </Select>
                  <Button size="sm" disabled={busy !== null} onClick={() => void searchNow(t)}>
                    {L("Chercher maintenant", "Search now")}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy !== null}
                    onClick={() => void refresh(t)}
                  >
                    {L("Actualiser", "Refresh")}
                  </Button>
                </span>
              </div>
            );
          })}
          {targets.length > VISIBLE ? (
            <p className="text-xs text-fg-muted">
              {L(
                `${VISIBLE} affichées sur ${targets.length} : filtrez pour trouver les autres.`,
                `Showing ${VISIBLE} of ${targets.length}: filter to find the others.`,
              )}
            </p>
          ) : null}
        </div>
      </div>
    </details>
  );
}
