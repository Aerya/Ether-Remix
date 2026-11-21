import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { PreStats, type PreStatus } from "./PreStats";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const num = (n: number) => n.toLocaleString(getLocale());

interface SourceRow {
  id: string;
  name: string;
  enabled: boolean;
  titles: number;
  releases: number;
}

/**
 * Releases indexées dans Statistiques → Résumé : le total, puis le détail par source,
 * puis les pre (détail par flux) quand l’option est activée ou à découvrir sinon.
 */
export function IndexedReleases({ pre }: { pre: PreStatus | undefined }) {
  const [rows, setRows] = useState<SourceRow[] | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    apiFetch<{ sources?: SourceRow[] }>("/pse/indexer/sources")
      .then((d) => {
        if (live) setRows(d.sources ?? []);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, []);
  const shown = (rows ?? [])
    .filter((r) => r.releases > 0 || r.enabled)
    .sort((a, b) => b.releases - a.releases || (a.name || a.id).localeCompare(b.name || b.id));
  const total = (rows ?? []).reduce((n, r) => n + (r.releases || 0), 0);
  return (
    <>
      <section className="space-y-3" aria-label={L("Releases indexées", "Indexed releases")}>
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h3 className="font-semibold">{L("Releases indexées", "Indexed releases")}</h3>
          <p className="text-xs text-fg-muted">
            <Link className="underline" to="/remix/apercu?view=recherches">
              {L("Détail et historique dans Recherches", "Detail and history under Searches")}
            </Link>
          </p>
        </div>
        {failed ? (
          <p role="alert" className="text-sm text-warning">
            {L("Compteurs momentanément indisponibles.", "Counters temporarily unavailable.")}
          </p>
        ) : rows === null ? (
          <p role="status" className="text-sm text-fg-muted">
            {L("Chargement…", "Loading…")}
          </p>
        ) : (
          <>
            <div className="rounded-xl border border-border p-4">
              <p className="text-xs text-fg-muted">
                {L("Releases indexées (global)", "Indexed releases (overall)")}
              </p>
              <p className="mt-1 text-xl font-semibold tabular-nums">{num(total)}</p>
              <p className="mt-1 text-xs text-fg-muted">
                {L("Toutes sources confondues", "All sources combined")}
              </p>
            </div>
            {shown.length ? (
              <div className="overflow-x-auto rounded-xl border border-border">
                <table className="w-full min-w-[420px] text-sm">
                  <caption className="sr-only">
                    {L("Releases indexées par source", "Indexed releases by source")}
                  </caption>
                  <thead>
                    <tr className="border-b border-border text-xs text-fg-muted">
                      <th scope="col" className="px-4 py-3 text-left font-medium">
                        {L("Source", "Source")}
                      </th>
                      <th scope="col" className="px-4 py-3 text-right font-medium">
                        {L("Titres", "Titles")}
                      </th>
                      <th scope="col" className="px-4 py-3 text-right font-medium">
                        {L("Releases indexées", "Indexed releases")}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {shown.map((r) => (
                      <tr key={r.id} className="border-b border-border last:border-0">
                        <th scope="row" className="px-4 py-3 text-left font-medium">
                          {r.name || r.id}
                        </th>
                        <td className="px-4 py-3 text-right tabular-nums">{num(r.titles)}</td>
                        <td className="px-4 py-3 text-right tabular-nums">{num(r.releases)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="text-sm text-fg-muted">
                {L("Aucune release indexée pour le moment.", "No indexed release yet.")}
              </p>
            )}
          </>
        )}
      </section>
      <PreStats status={pre} total={rows === null ? null : total} />
    </>
  );
}
