import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
import { ReleaseBrowser } from "./ReleaseBrowser";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface Overview {
  auditPending?: boolean;
  auditFailed?: boolean;
  quarantined?: number;
  referenceBackfill?: number;
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
function ReleaseOverview({ compact = false }: { compact?: boolean }) {
  const [tick, setTick] = useState(0);
  const state = useAsync(
    (signal) => apiFetch<Overview>(`/pse/release-library?overview=1&summary=1`, { signal }),
    [tick, compact],
  );
  useEffect(() => {
    if (state.loading) return;
    const timer = window.setInterval(() => {
      if (!document.hidden) setTick((t) => t + 1);
    }, 60000);
    return () => window.clearInterval(timer);
  }, [state.loading, tick]);
  const d = state.data;
  if (state.error && !d)
    return (
      <p role="alert">
        {L(
          "Compteurs momentanément indisponibles. Nouvelle tentative dans 60 secondes.",
          "Counters temporarily unavailable. Retrying in 60 seconds.",
        )}
      </p>
    );
  if (!d) return <p role="status">{L("Chargement des releases…", "Loading releases…")}</p>;
  if (d.auditFailed)
    return (
      <p role="alert">
        {L(
          "La vérification des associations a échoué. La bibliothèque reste isolée ; consulter les journaux du serveur.",
          "Association verification failed. The library remains isolated; check server logs.",
        )}
      </p>
    );
  if (d.auditPending)
    return (
      <p role="status">
        {L(
          "Vérification des associations média/release en cours. Les anciennes entrées non confirmées sont mises à l’écart.",
          "Media/release associations are being checked. Unconfirmed old entries are being quarantined.",
        )}
      </p>
    );
  if (!d.enabled)
    return <p>{L("L’index des releases est indisponible.", "Release index is unavailable.")}</p>;
  return (
    <section className="min-w-0 max-w-full space-y-4">
      {Boolean(state.error) && (
        <p role="alert">
          {L(
            "Compteurs momentanément indisponibles ; dernières valeurs conservées.",
            "Counters temporarily unavailable; keeping previous values.",
          )}
        </p>
      )}
      <h2 className="text-lg font-semibold">{L("Index des releases", "Release index")}</h2>
      <p className="text-sm text-fg-muted">
        {L(
          "Collecte continue lors des recherches et imports. Actualisation toutes les 60 secondes. Métadonnées et références se recoupent : leurs compteurs ne s’additionnent pas. Ces compteurs décrivent les références, pas leur conformité au profil de lecture.",
          "Collected continuously during searches and imports. Refreshed every 60 seconds. Metadata and references overlap: their counts must not be added. These counters describe references, not compliance with the playback profile.",
        )}
      </p>
      {Boolean(d.quarantined) && (
        <p className="text-sm text-fg-muted">
          {d.quarantined?.toLocaleString(getLocale())}{" "}
          {L(
            "associations anciennes non confirmées mises en quarantaine, exclues des résultats et de la réutilisation.",
            "unconfirmed old associations quarantined, excluded from results and reuse.",
          )}
        </p>
      )}
      <dl className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {(
          [
            [L("Contenus / épisodes", "Titles / episodes"), d.titles],
            [L("Métadonnées indexées", "Indexed metadata"), d.metadata],
            [L("Références conservées", "Stored references"), d.references],
            [L("Références sans échec enregistré", "References without recorded failure"), d.ready],
            [L("À actualiser", "Needs refresh"), d.expired],
            [L("Échecs de lecture", "Playback failures"), d.failed],
            [L("Source modifiée", "Source changed"), d.sourceChanged],
            [L("Contenus à reprendre", "Titles pending recovery"), d.referenceBackfill ?? 0],
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
      ) : null}
    </section>
  );
}

export function ReleaseLibraryDashboard({ compact = false }: { compact?: boolean }) {
  return (
    <section className="min-w-0 max-w-full space-y-4">
      <ReleaseOverview compact={compact} />
      {!compact && <ReleaseBrowser />}
    </section>
  );
}
