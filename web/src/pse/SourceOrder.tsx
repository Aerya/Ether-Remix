import { Button } from "@braedonsaunders/appkit-ui";
import { SettingsSection } from "../components/CollapsibleSections";
import { useEffect, useMemo, useState } from "react";
import { getLocale } from "../i18n";
import { api } from "../lib/api";
import { useAsync } from "../lib/hooks";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const FAMILIES = ["stremio", "usenet", "torrent"];
const CONTENTS = ["movie", "show", "anime"] as const;
type Content = (typeof CONTENTS)[number];
interface Row {
  id: string;
  label: string;
}

const familyLabel = (id: string) =>
  id === "stremio"
    ? "Stremio"
    : id === "usenet"
      ? L("Indexeurs usenet", "Usenet indexers")
      : L("Indexeurs bittorrent", "BitTorrent indexers");
const contentLabel = (id: Content) =>
  id === "movie"
    ? L("Films", "Movies")
    : id === "show"
      ? L("Séries", "Shows")
      : L("Animés", "Anime");

export function moved<T>(list: T[], at: number, delta: -1 | 1): T[] {
  const to = at + delta;
  if (to < 0 || to >= list.length) return list;
  const next = [...list];
  next.splice(to, 0, next.splice(at, 1)[0] as T);
  return next;
}

/** Écrit l'ordre global dans les priorités (la plus petite passe d'abord). */
export function applyPriorities<T extends { id: string; priority: number }>(
  list: T[],
  ids: string[],
): T[] {
  const at = new Map(ids.map((id, index) => [id, index + 1]));
  return list.map((item) => {
    const priority = at.get(item.id);
    return priority === undefined ? item : { ...item, priority };
  });
}

/** Déplace une source dans sa famille, sans changer la place des autres familles. */
export function movedInFamily(
  ids: string[],
  familyOf: (id: string) => string,
  family: string,
  at: number,
  delta: -1 | 1,
): string[] {
  const next = moved(
    ids.filter((id) => familyOf(id) === family),
    at,
    delta,
  );
  let index = 0;
  return ids.map((id) => (familyOf(id) === family ? (next[index++] as string) : id));
}

function OrderList({ rows, onMove }: { rows: Row[]; onMove: (at: number, delta: -1 | 1) => void }) {
  return (
    <ol className="divide-y divide-border rounded border border-border">
      {rows.map((row, at) => (
        <li className="flex items-center gap-2 p-2 text-sm" key={row.id}>
          <span className="w-6 text-fg-muted">{at + 1}</span>
          <span className="min-w-0 flex-1 truncate">{row.label}</span>
          <Button
            aria-label={L(`Monter ${row.label}`, `Move ${row.label} up`)}
            disabled={at === 0}
            onClick={() => onMove(at, -1)}
            size="sm"
            variant="ghost"
          >
            ↑
          </Button>
          <Button
            aria-label={L(`Descendre ${row.label}`, `Move ${row.label} down`)}
            disabled={at === rows.length - 1}
            onClick={() => onMove(at, 1)}
            size="sm"
            variant="ghost"
          >
            ↓
          </Button>
        </li>
      ))}
    </ol>
  );
}

function FamilyLists({
  families,
  ids,
  familyOf,
  rowOf,
  onChange,
}: {
  families: string[];
  ids: string[];
  familyOf: (id: string) => string;
  rowOf: (id: string) => Row;
  onChange: (ids: string[]) => void;
}) {
  return (
    <div className="space-y-3">
      {families.map((family) => {
        const group = ids.filter((id) => familyOf(id) === family);
        return group.length ? (
          <div className="space-y-1" key={family}>
            <h4 className="text-xs font-medium text-fg-muted">{familyLabel(family)}</h4>
            <OrderList
              rows={group.map(rowOf)}
              onMove={(at, delta) => onChange(movedInFamily(ids, familyOf, family, at, delta))}
            />
          </div>
        ) : null;
      })}
    </div>
  );
}

export function SourceOrder() {
  const config = useAsync(() => api.getConfig(), []);
  const [families, setFamilies] = useState<string[]>([]);
  const [content, setContent] = useState<Record<string, string[]>>({});
  const [globalIds, setGlobalIds] = useState<string[] | null>(null);
  const [selected, setSelected] = useState<Content>("movie");
  const [state, setState] = useState<"" | "saving" | "saved" | "error">("");

  useEffect(() => {
    const order = config.data?.resolver?.sourceOrder;
    setFamilies(order?.families ?? []);
    setContent(order?.content ?? {});
  }, [config.data]);

  const indexers = useMemo(
    () =>
      (config.data?.indexers ?? [])
        .filter((ix) => ix.enabled)
        .sort((a, b) => a.priority - b.priority),
    [config.data],
  );
  const labels = new Map(indexers.map((ix) => [ix.id, ix]));
  const custom = (content[selected] ?? []).filter((id) => labels.has(id));
  const orderedIds = globalIds ?? indexers.map((ix) => ix.id);
  const contentIds = [...custom, ...orderedIds.filter((id) => !custom.includes(id))];
  const sourceRow = (id: string): Row => ({ id, label: labels.get(id)?.name ?? id });
  const familyOfId = (id: string) => {
    const ix = labels.get(id);
    return ix?.kind === "stremio" ? "stremio" : ix?.protocol === "torrent" ? "torrent" : "usenet";
  };
  const familyIds = families.length ? families : FAMILIES;

  async function save() {
    setState("saving");
    try {
      const latest = await api.getConfig();
      await api.putConfig({
        ...latest,
        indexers:
          globalIds && latest.indexers
            ? applyPriorities(latest.indexers, globalIds)
            : latest.indexers,
        resolver: { ...latest.resolver, sourceOrder: { families, content } },
      } as typeof latest);
      setState("saved");
    } catch {
      setState("error");
    }
  }

  return (
    <SettingsSection
      title={L("Sources à privilégier", "Sources to prefer")}
      description={L(
        "À qualité équivalente, Remix essaie les sources dans cet ordre. La langue et la qualité restent prioritaires.",
        "Among equivalent releases, Remix tries sources in this order. Language and quality still come first.",
      )}
    >
      {config.error ? (
        <p className="p-4 text-sm text-fg-muted">
          {L("Réglages indisponibles.", "Settings unavailable.")}
        </p>
      ) : !config.data ? (
        <p className="p-4 text-sm">{L("Chargement…", "Loading…")}</p>
      ) : (
        <div className="space-y-5 p-4">
          <section className="space-y-2">
            <h3 className="text-sm font-medium">
              {L("Par famille de sources", "By source family")}
            </h3>
            <OrderList
              rows={familyIds.map((id) => ({ id, label: familyLabel(id) }))}
              onMove={(at, delta) => setFamilies(moved(familyIds, at, delta))}
            />
            {families.length ? (
              <Button onClick={() => setFamilies([])} size="sm" variant="ghost">
                {L("Revenir au choix automatique", "Back to automatic")}
              </Button>
            ) : (
              <p className="text-sm text-fg-muted">
                {L(
                  "Automatique tant que tu n'as rien déplacé.",
                  "Automatic until you move something.",
                )}
              </p>
            )}
          </section>
          <section className="space-y-2">
            <h3 className="text-sm font-medium">
              {L("Ordre global des sources", "Global source order")}
            </h3>
            <FamilyLists
              families={familyIds}
              ids={orderedIds}
              familyOf={familyOfId}
              rowOf={sourceRow}
              onChange={setGlobalIds}
            />
          </section>
          <section className="space-y-2">
            <h3 className="text-sm font-medium">
              {L("Sources par type de contenu", "Sources by content type")}
            </h3>
            <nav className="flex flex-wrap gap-2">
              {CONTENTS.map((id) => (
                <Button
                  key={id}
                  onClick={() => setSelected(id)}
                  size="sm"
                  variant={selected === id ? "default" : "outline"}
                >
                  {contentLabel(id)}
                </Button>
              ))}
            </nav>
            <FamilyLists
              families={familyIds}
              ids={contentIds}
              familyOf={familyOfId}
              rowOf={sourceRow}
              onChange={(ids) => setContent({ ...content, [selected]: ids })}
            />
            {custom.length ? (
              <Button
                onClick={() => {
                  const { [selected]: _removed, ...rest } = content;
                  setContent(rest);
                }}
                size="sm"
                variant="ghost"
              >
                {L("Reprendre l'ordre global", "Use the global order")}
              </Button>
            ) : (
              <p className="text-sm text-fg-muted">
                {L(
                  "Ordre global tant que tu n'as rien déplacé.",
                  "Global order until you move something.",
                )}
              </p>
            )}
          </section>
          <div className="flex items-center gap-3">
            <Button disabled={state === "saving"} onClick={() => void save()}>
              {L("Enregistrer", "Save")}
            </Button>
            {state === "saved" ? <span className="text-sm">{L("Enregistré", "Saved")}</span> : null}
            {state === "error" ? (
              <span className="text-sm">{L("Enregistrement impossible", "Could not save")}</span>
            ) : null}
          </div>
        </div>
      )}
    </SettingsSection>
  );
}
