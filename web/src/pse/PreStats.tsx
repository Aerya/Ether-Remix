import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const num = (n: number) => n.toLocaleString(getLocale());

export interface PreStatus {
  active?: boolean;
  import?: { state?: string; last?: number; added?: number; pending?: number; error?: string };
}

/**
 * Compteurs des pre, affichés dans Statistiques seulement quand l’option est
 * activée : releases indexées au total, releases venues des pre et pre en attente.
 */
export function PreStats({ status }: { status: PreStatus | undefined }) {
  const [total, setTotal] = useState<number | null>(null);
  useEffect(() => {
    if (!status?.active) return;
    let live = true;
    apiFetch<{ references?: number }>("/pse/release-library?overview=1&summary=1")
      .then((d) => {
        if (live && typeof d.references === "number") setTotal(d.references);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [status?.active]);
  if (!status?.active) return null;
  const added = status.import?.added ?? 0;
  const share = total && total > 0 ? Math.round((added * 1000) / total) / 10 : null;
  const last = status.import?.last
    ? new Date(status.import.last * 1000).toLocaleString(getLocale(), {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : null;
  const cards: [string, string, string][] = [
    [
      L("Releases indexées (global)", "Indexed releases (overall)"),
      total === null ? "—" : num(total),
      L("Toutes sources confondues", "All sources combined"),
    ],
    [
      L("Releases issues des pre", "Releases from pre"),
      num(added),
      share === null ? "" : L(`${share} % du total`, `${share}% of the total`),
    ],
    [
      L("Pre en attente", "Pending pre"),
      num(status.import?.pending ?? 0),
      L("Délai ou titre pas encore trouvé", "Waiting for the delay or a title"),
    ],
  ];
  return (
    <section className="space-y-3" aria-label={L("Pre (bêta)", "Pre (beta)")}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="font-semibold">{L("Pre (bêta)", "Pre (beta)")}</h3>
        <p className="text-xs text-fg-muted">
          {last
            ? L(`Dernière lecture : ${last}`, `Last read: ${last}`)
            : L("Aucune lecture pour le moment", "Nothing read yet")}
          {" · "}
          <Link className="underline" to="/remix/indexation?view=pre">
            {L("Réglages", "Settings")}
          </Link>
        </p>
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        {cards.map(([label, value, hint]) => (
          <div key={label} className="rounded-xl border border-border p-4">
            <p className="text-xs text-fg-muted">{label}</p>
            <p className="mt-1 text-xl font-semibold tabular-nums">{value}</p>
            {hint ? <p className="mt-1 text-xs text-fg-muted">{hint}</p> : null}
          </div>
        ))}
      </div>
      {status.import?.error ? (
        <p role="alert" className="text-sm text-warning">
          {status.import.error}
        </p>
      ) : null}
      <p className="text-xs text-fg-muted">
        {L(
          "Le détail des releases par source, dont les pre, se trouve dans Recherches.",
          "The per-source release detail, including pre, is under Searches.",
        )}{" "}
        <Link className="underline" to="/remix/apercu?view=recherches">
          {L("Voir Recherches", "View Searches")}
        </Link>
      </p>
    </section>
  );
}
