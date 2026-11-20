import { Button, Switch } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import type { EtherConfig } from "../lib/types";
import { moved } from "./SourceOrder";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export const QUICK_SOURCES = ["alko", "usenet", "torrent", "seedstrem"];
export interface QuickSourceSettings {
  enabled: boolean;
  order?: string[];
  /** Ordre d’affichage complet, sources cochées ou non. */
  rank?: string[];
  seedstremId?: string;
}

/** Ordre complet des quatre sources : rang mémorisé, sinon sélection puis reste. */
export function quickRows(value: QuickSourceSettings): string[] {
  const order = value.order ?? [];
  const rank = value.rank ?? [];
  const known = (list: string[]) =>
    list.filter((id, i) => QUICK_SOURCES.includes(id) && list.indexOf(id) === i);
  const base = rank.length ? [...known(rank), ...known(order)] : known(order);
  const head = known(base);
  return [...head, ...QUICK_SOURCES.filter((id) => !head.includes(id))];
}

export function QuickSources({
  value,
  onChange,
  config,
  values,
}: {
  value: QuickSourceSettings;
  onChange: (v: QuickSourceSettings) => void;
  config: EtherConfig;
  values: Record<string, string>;
}) {
  const order = value.order ?? [];
  const rows = quickRows(value);
  const [dragged, setDragged] = useState<number | null>(null);
  // Réordonne la liste complète ; l’ordre de lecture suit l’ordre affiché des sources cochées.
  const reorder = (next: string[], selected: string[] = order) =>
    onChange({ ...value, rank: next, order: next.filter((id) => selected.includes(id)) });
  const moveTo = (from: number, to: number) => {
    if (from === to || to < 0 || to >= rows.length) return;
    const next = [...rows];
    next.splice(to, 0, next.splice(from, 1)[0] as string);
    reorder(next);
  };
  const indexers = (config.indexers ?? []).filter((ix) => ix.enabled);
  const addons = indexers.filter((ix) => ix.kind === "stremio");
  const labels: Record<string, string> = {
    alko: L("StreamFusion Reborn — Alkodiques uniquement", "StreamFusion Reborn — Alkodiques only"),
    usenet: "Usenet",
    torrent: L("BitTorrent — client distant Ether-Remix", "BitTorrent — Ether-Remix remote client"),
    seedstrem: "BitTorrent — Seedstrem",
  };
  const on = (key: string) => ["on", "true", "1"].includes(values[key] ?? "");
  const ready: Record<string, boolean> = {
    alko:
      on("PSE_SFR") &&
      !!values.PSE_SFR_URL &&
      !!config.debrid?.providers?.some((p) => p.enabled && p.kind === "alldebrid"),
    usenet:
      !!config.usenet?.providers?.some((p) => p.enabled) &&
      indexers.some((ix) => ix.kind !== "stremio" && ix.protocol !== "torrent"),
    torrent:
      on("PSE_TORRENT") &&
      !!values.PSE_TORRENT_URL &&
      indexers.some((ix) => ix.kind !== "stremio" && ix.protocol === "torrent"),
    seedstrem: addons.some((ix) => ix.id === value.seedstremId),
  };
  const links: Record<string, string> = {
    alko: "stremio",
    usenet: "usenet",
    torrent: "torrent",
    seedstrem: "stremio",
  };
  return (
    <section className="space-y-3 rounded border border-border p-3">
      <h3 className="font-medium">
        {L(
          "Limiter le recours aux débrideurs, voire préférer le téléchargement & streaming BitTorrent/Usenet",
          "Limit debrid usage, or even prefer BitTorrent/Usenet download & streaming",
        )}
      </h3>
      <label className="flex items-center gap-2">
        <Switch
          checked={value.enabled}
          onChange={(e) => onChange({ ...value, enabled: e.currentTarget.checked })}
        />
        {L("Configuration rapide", "Quick setup")}
      </label>
      {value.enabled && (
        <>
          <p className="text-sm text-fg-muted">
            {L(
              "Après les filtres obligatoires, les sources cochées sont essayées de haut en bas. Si l’une ne peut pas lire le titre, Remix passe à la suivante. Les autres addons Stremio, dont Debrider, sont le dernier recours. Réordonnez avec ↑ / ↓ ou par glisser-déposer.",
              "After mandatory filters, checked sources are tried from top to bottom. If one cannot play the title, Remix moves to the next. Other Stremio addons, including Debrider, are the last resort. Reorder with ↑ / ↓ or drag and drop.",
            )}
          </p>
          <ol className="space-y-2">
            {rows.map((id, at) => (
              <li
                key={id}
                draggable
                onDragStart={(e) => {
                  setDragged(at);
                  e.dataTransfer.effectAllowed = "move";
                }}
                onDragOver={(e) => {
                  if (dragged !== null) e.preventDefault();
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  if (dragged !== null) moveTo(dragged, at);
                  setDragged(null);
                }}
                onDragEnd={() => setDragged(null)}
                className={`rounded border border-border p-2 ${dragged === at ? "opacity-50" : ""} ${order.includes(id) ? "" : "opacity-70"}`}
              >
                <div className="flex items-center gap-2">
                  <span className="cursor-grab select-none text-fg-muted" aria-hidden="true">
                    ⠿
                  </span>
                  <span className="w-5 text-sm text-fg-muted">
                    {order.includes(id) ? order.indexOf(id) + 1 : "–"}
                  </span>
                  <label className="flex flex-1 items-center gap-2">
                    <input
                      type="checkbox"
                      checked={order.includes(id)}
                      onChange={(e) =>
                        reorder(
                          rows,
                          e.target.checked ? [...order, id] : order.filter((x) => x !== id),
                        )
                      }
                    />
                    {labels[id]}
                  </label>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={L(`Monter ${labels[id]}`, `Move ${labels[id]} up`)}
                    disabled={at === 0}
                    onClick={() => reorder(moved(rows, at, -1))}
                  >
                    ↑
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={L(`Descendre ${labels[id]}`, `Move ${labels[id]} down`)}
                    disabled={at === rows.length - 1}
                    onClick={() => reorder(moved(rows, at, 1))}
                  >
                    ↓
                  </Button>
                </div>
                <p className="text-sm text-fg-muted">
                  {ready[id]
                    ? L("Configurée", "Configured")
                    : L(
                        "Non configurée — réglages requis",
                        "Not configured — settings required",
                      )}{" "}
                  ·{" "}
                  <Link className="underline" to={`/remix/sources?source=${links[id]}`}>
                    {L("Paramètres", "Settings")}
                  </Link>
                </p>
                {id === "seedstrem" && (
                  <label className="text-sm">
                    {L("Instance Seedstrem déjà installée", "Installed Seedstrem instance")}{" "}
                    <select
                      aria-label={L("Instance Seedstrem", "Seedstrem instance")}
                      className="rounded border border-border bg-surface p-1"
                      value={value.seedstremId ?? ""}
                      onChange={(e) => onChange({ ...value, seedstremId: e.target.value })}
                    >
                      <option value="">{L("Choisir l’instance", "Select instance")}</option>
                      {addons.map((ix) => (
                        <option key={ix.id} value={ix.id}>
                          {ix.name}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
              </li>
            ))}
          </ol>
          {!order.length && (
            <p className="text-sm">
              {L(
                "Aucune source sélectionnée : seuls les autres addons Stremio seront interrogés.",
                "No source selected: only other Stremio addons will be queried.",
              )}
            </p>
          )}
          <p className="text-sm text-fg-muted">
            {L(
              "Les réglages avancés sont conservés. Enregistrez pour appliquer ces choix.",
              "Advanced settings are preserved. Save to apply these choices.",
            )}
          </p>
        </>
      )}
    </section>
  );
}
