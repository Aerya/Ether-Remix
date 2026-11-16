import { useState } from "react";
import { Select } from "@braedonsaunders/appkit-ui";
import { IndexerList } from "../pages/sources/Indexers";
import { api } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
import { indexerOrigin } from "./indexerOrigin";
import type { ComponentProps } from "react";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export function IndexerGroups(props: ComponentProps<typeof IndexerList>) {
  const cfg = useAsync(() => api.getConfig(), [props.version]);
  const [selected, setSelected] = useState("");
  const groups = new Map<string, { label: string; count: number }>();
  for (const ix of cfg.data?.indexers ?? []) {
    if (ix.kind === "stremio") continue;
    if (props.view === "bittorrent" && ix.protocol !== "torrent") continue;
    if (props.view === "usenet-indexers" && ix.protocol === "torrent") continue;
    const g = indexerOrigin(ix);
    const entry = groups.get(g.key) ?? { label: g.label, count: 0 };
    entry.count++;
    groups.set(g.key, entry);
  }
  const active = groups.has(selected) ? selected : "";
  return (
    <div className="space-y-3">
      <p className="text-sm text-fg-muted">
        {L(
          "Prowlarr et Jackett regroupent des indexeurs : chaque indexeur conserve son activation et ses limites. Filtrez par instance pour les retrouver. Aucun indexeur n’est supprimé ni fusionné automatiquement.",
          "Prowlarr and Jackett manage indexers: each indexer keeps its own activation and limits. Filter by instance to find them. No indexer is automatically deleted or merged.",
        )}
      </p>
      <Select
        aria-label={L("Instance ou provenance", "Instance or origin")}
        value={active}
        onChange={(e) => setSelected(e.target.value)}
      >
        <option value="">
          {L("Toutes les instances et sources manuelles", "All instances and manual sources")}
        </option>
        {[...groups].map(([id, g]) => (
          <option key={id} value={id}>
            {g.label} ({g.count})
          </option>
        ))}
      </Select>
      <p className="text-xs text-fg-muted">
        {L(
          "Prowlarr : importez ses indexeurs avec le bouton ci-dessus. Jackett : ajoutez l’URL Torznab et la clé de chaque indexeur via Ajouter une source → Torznab.",
          "Prowlarr: import its indexers using the button above. Jackett: add each indexer’s Torznab URL and key via Add a source → Torznab.",
        )}
      </p>
      <IndexerList
        {...props}
        originKey={active || undefined}
        onSaved={(name) => {
          void cfg.reload();
          props.onSaved?.(name);
        }}
      />
    </div>
  );
}
