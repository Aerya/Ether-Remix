import { Button, Switch } from "@braedonsaunders/appkit-ui";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import type { EtherConfig } from "../lib/types";
import { moved } from "./SourceOrder";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export const QUICK_SOURCES = ["alko", "usenet", "torrent", "seedstrem"];
export interface QuickSourceSettings {
  enabled: boolean;
  order?: string[];
  seedstremId?: string;
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
  const rows = [
    ...order.filter((id) => QUICK_SOURCES.includes(id)),
    ...QUICK_SOURCES.filter((id) => !order.includes(id)),
  ];
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
      <h3 className="font-medium">{L("Configuration rapide des sources", "Quick source setup")}</h3>
      <label className="flex items-center gap-2">
        <Switch
          checked={value.enabled}
          onChange={(e) => onChange({ ...value, enabled: e.currentTarget.checked })}
        />
        {L("Mode rapide", "Quick mode")}
      </label>
      {value.enabled && (
        <>
          <p className="text-sm text-fg-muted">
            {L(
              "Après les filtres obligatoires, chaque source est essayée dans cet ordre. Si elle ne peut pas lire le titre, Remix passe à la suivante. Les autres addons Stremio, dont Debrider, sont le dernier recours.",
              "After mandatory filters, each source is tried in this order. If it cannot play the title, Remix moves to the next. Other Stremio addons, including Debrider, are the last resort.",
            )}
          </p>
          <ol className="space-y-2">
            {rows.map((id) => (
              <li key={id} className="rounded border border-border p-2">
                <div className="flex items-center gap-2">
                  <label className="flex flex-1 items-center gap-2">
                    <input
                      type="checkbox"
                      checked={order.includes(id)}
                      onChange={(e) =>
                        onChange({
                          ...value,
                          order: e.target.checked ? [...order, id] : order.filter((x) => x !== id),
                        })
                      }
                    />
                    {labels[id]}
                  </label>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={L(`Monter ${labels[id]}`, `Move ${labels[id]} up`)}
                    disabled={!order.includes(id) || order.indexOf(id) === 0}
                    onClick={() =>
                      onChange({ ...value, order: moved(order, order.indexOf(id), -1) })
                    }
                  >
                    ↑
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={L(`Descendre ${labels[id]}`, `Move ${labels[id]} down`)}
                    disabled={!order.includes(id) || order.indexOf(id) === order.length - 1}
                    onClick={() =>
                      onChange({ ...value, order: moved(order, order.indexOf(id), 1) })
                    }
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
