import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
import { ReleaseLibrary } from "./ReleaseLibrary";
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
  const [page, setPage] = useState(0);
  const [tick, setTick] = useState(0);
  const [selected, setSelected] = useState("");
  const state = useAsync(
    (signal) =>
      apiFetch<Overview>(`/pse/release-library?overview=1&page=${compact ? 0 : page}`, { signal }),
    [page, tick, compact],
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
    <section className="space-y-4">
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
        <Link to="/remix/releases" className="text-accent underline">
          {L("Consulter les releases et suivre l’avancement", "Browse releases and track progress")}
        </Link>
      ) : (
        <>
          <IndexerActivity />
          <h3 className="font-semibold">
            {L(
              "Contenus indexés, les plus récents en premier",
              "Indexed content, most recent first",
            )}
          </h3>
          {d.items.length === 0 && (
            <p>
              {L(
                "Aucune release indexée. Les prochaines recherches et imports alimenteront cette liste.",
                "No indexed releases. Upcoming searches and imports will populate this list.",
              )}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            {d.items.map((item) => (
              <Button
                key={item.key}
                variant={selected === item.key ? "default" : "outline"}
                onClick={() => setSelected(item.key)}
              >
                {item.title}
              </Button>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Button disabled={page === 0} onClick={() => setPage((p) => p - 1)}>
              {L("Précédent", "Previous")}
            </Button>
            <span>
              {L("Page", "Page")} {page + 1} / {Math.max(1, Math.ceil(d.titles / 50))}
            </span>
            <Button disabled={(page + 1) * 50 >= d.titles} onClick={() => setPage((p) => p + 1)}>
              {L("Suivant", "Next")}
            </Button>
            <Button onClick={() => setTick((t) => t + 1)}>{L("Actualiser", "Refresh")}</Button>
          </div>
          {selected && <ReleaseLibrary key={selected} titleKey={selected} refresh={tick} />}
        </>
      )}
    </section>
  );
}
