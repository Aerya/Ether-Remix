import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
import { ReleaseBrowser } from "./ReleaseBrowser";
import { IndexerActivity } from "./IndexerActivity";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface Overview {
  enabled: boolean;
  references: number;
  ready: number;
  expired: number;
  failed: number;
  sourceChanged: number;
  metadata: number;
  titles: number;
  lastSeen: number;
  items: { key: string; title: string; lastSeen: number }[];
}
export function ReleaseLibraryDashboard({ compact = false }: { compact?: boolean }) {
  const [tick, setTick] = useState(0);
  const state = useAsync(
    (signal) => apiFetch<Overview>(`/pse/release-library?overview=1&page=0`, { signal }),
    [tick, compact],
  );
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!document.hidden) setTick((t) => t + 1);
    }, 15000);
    return () => window.clearInterval(timer);
  }, []);
  const d = state.data;
  if (state.error)
    return (
      <p role="alert">
        {L(
          "Impossible de charger les releases. Nouvelle tentative toutes les 15 secondes.",
          "Unable to load releases. Retrying every 15 seconds.",
        )}
      </p>
    );
  if (!d) return <p role="status">{L("Chargement des releases…", "Loading releases…")}</p>;
  if (!d.enabled)
    return (
      <p>{L("La bibliothèque de releases est désactivée.", "Release library is disabled.")}</p>
    );
  return (
    <section className="min-w-0 max-w-full space-y-4">
      <h2 className="text-lg font-semibold">
        {L("Bibliothèque de releases · Bêta", "Release library · Beta")}
      </h2>
      <p className="text-sm text-fg-muted">
        {L(
          "Collecte continue lors des recherches et imports. Actualisation toutes les 15 secondes. Métadonnées et références se recoupent : leurs compteurs ne s’additionnent pas. Une référence disponible ne garantit pas la lecture.",
          "Collected continuously during searches and imports. Refreshed every 15 seconds. Metadata and references overlap: their counts must not be added. An available reference does not guarantee playback.",
        )}
      </p>
      <dl className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {(
          [
            [L("Contenus / épisodes", "Titles / episodes"), d.titles],
            [L("Métadonnées indexées", "Indexed metadata"), d.metadata],
            [L("Références conservées", "Stored references"), d.references],
            [L("Références disponibles", "Available references"), d.ready],
            [L("À actualiser", "Needs refresh"), d.expired],
            [L("Échecs de lecture", "Playback failures"), d.failed],
            [L("Source modifiée", "Source changed"), d.sourceChanged],
          ] as [string, number][]
        ).map(([label, value]) => (
          <div key={label} className="rounded-lg border border-border p-3">
            <dt className="text-sm text-fg-muted">{label}</dt>
            <dd className="text-xl tabular-nums">{value.toLocaleString(getLocale())}</dd>
          </div>
        ))}
      </dl>
      <p className="text-sm">
        {L("Dernière référence enregistrée : ", "Last reference saved: ")}
        {d.lastSeen ? new Date(d.lastSeen * 1000).toLocaleString(getLocale()) : L("aucune", "none")}
      </p>
      {compact ? (
        <Link to="/remix/indexation?view=releases" className="text-accent underline">
          {L("Consulter les releases et suivre l’avancement", "Browse releases and track progress")}
        </Link>
      ) : (
        <>
          <IndexerActivity />
          <ReleaseBrowser />
        </>
      )}
    </section>
  );
}
