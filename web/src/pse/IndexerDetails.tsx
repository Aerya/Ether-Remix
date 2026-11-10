import "./StatisticsCharts.css";
import { CoverageRing } from "./StatisticsCharts";
import { CatalogHistory } from "./CatalogHistory";
import { IndexerActivity } from "./IndexerActivity";
import { Button } from "@braedonsaunders/appkit-ui";
import { RefreshCw } from "lucide-react";
import { Link } from "react-router-dom";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

export interface Counts {
  titles: number;
  indexed: number;
  found: number;
  playable: number;
  recent: number;
  stale: number;
  failing: number;
}
interface Row extends Counts {
  id: string;
  name: string;
  type?: string;
}
interface Details {
  at: number;
  min: number;
  types: Record<string, Counts> | null;
  libraries: (Row & { rows: Row[] | null })[] | null;
  collections: Row[] | null;
  empty: number;
}
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const num = (n: number) => n.toLocaleString(getLocale());
const pct = (n: number, d: number) => (d > 0 ? `${Math.round((n * 100) / d)} %` : "—");
const TYPES = [
  ["movie", "Films", "Movies"],
  ["show", "Séries", "TV shows"],
  ["anime", "Animes", "Anime"],
] as const;

function Metric({
  label,
  value,
  hint,
  warning = false,
  color = "var(--viz-1)",
}: {
  label: string;
  value: number;
  hint: string;
  warning?: boolean;
  color?: string;
}) {
  return (
    <div
      className="remix-stat-metric rounded-xl border border-border bg-surface p-5"
      style={{ borderTopColor: color }}
    >
      <p className="text-sm text-fg-muted">{label}</p>
      <p
        className={`mt-2 text-3xl font-semibold tracking-tight tabular-nums ${warning && value > 0 ? "text-warning" : "text-fg"}`}
      >
        {num(value)}
      </p>
      <p className="mt-2 text-xs text-fg-muted">{hint}</p>
    </div>
  );
}
function Breakdown({ rows, label }: { rows: Row[]; label: string }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[680px] text-sm">
        <caption className="sr-only">{label}</caption>
        <thead>
          <tr className="border-b border-border bg-surface text-xs text-fg-muted">
            {[
              label,
              L("Titres", "Titles"),
              L("Cherchés", "Searched"),
              L("Avec release", "With release"),
              L("Affichés", "Shown"),
              L("24 h", "24 h"),
              L("À actualiser", "Stale"),
              L("En erreur", "Errors"),
            ].map((h, i) => (
              <th
                key={i}
                scope="col"
                className={`px-4 py-3 font-medium ${i ? "text-right" : "text-left"}`}
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr
              key={`${r.id}-${i}`}
              className="border-b border-border last:border-0 hover:bg-surface"
            >
              <th scope="row" className="px-4 py-3 text-left font-medium">
                {r.name || r.id}
              </th>
              {[
                r.titles,
                r.indexed,
                r.found,
                r.playable,
                r.recent,
                r.stale ?? 0,
                r.failing ?? 0,
              ].map((n, j) => (
                <td
                  key={j}
                  className={`px-4 py-3 text-right tabular-nums ${j >= 5 && n > 0 ? "text-warning" : ""}`}
                >
                  {num(n)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function IndexerDetails() {
  const [data, setData] = useState<Details | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = async (refresh: boolean) => {
    setBusy(true);
    setError("");
    try {
      setData(await apiFetch<Details>(`/pse/indexer/details${refresh ? "?refresh=1" : ""}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    void load(false);
  }, []);
  const types = data?.types ?? {};
  const total = TYPES.filter(([type]) => type !== "anime").reduce<Counts>(
    (acc, [t]) => {
      const c = types[t];
      if (c) for (const k of Object.keys(acc) as (keyof Counts)[]) acc[k] += c[k] ?? 0;
      return acc;
    },
    { titles: 0, indexed: 0, found: 0, playable: 0, recent: 0, stale: 0, failing: 0 },
  );

  return (
    <section
      className="space-y-6"
      aria-label={L("Statistiques du catalogue", "Catalogue statistics")}
      aria-busy={busy}
    >
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 className="text-xl font-semibold">
            {L("Le catalogue en chiffres", "Your catalogue at a glance")}
          </h2>
          <p className="mt-1 text-sm text-fg-muted">
            {L(
              "Couverture des recherches et disponibilité des films et séries.",
              "Search coverage and availability of movies and shows.",
            )}
          </p>
          {data ? (
            <p className="mt-2 text-xs text-fg-muted">
              {L("Calculé le ", "Calculated on ")}
              {new Date(data.at * 1000).toLocaleString(getLocale(), {
                dateStyle: "medium",
                timeStyle: "short",
              })}
            </p>
          ) : null}
        </div>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => void load(true)}>
          <RefreshCw className={`mr-2 h-4 w-4 ${busy ? "animate-spin" : ""}`} />
          {busy ? L("Calcul…", "Computing…") : L("Actualiser", "Refresh")}
        </Button>
      </header>
      {error ? (
        <p role="alert" className="rounded-xl border border-border p-4 text-sm text-warning">
          {error}
        </p>
      ) : null}
      {!data && busy ? (
        <p role="status" className="py-12 text-center text-sm text-fg-muted">
          {L("Chargement des statistiques…", "Loading statistics…")}
        </p>
      ) : null}
      {data ? (
        <>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <Metric
              label={L("Catalogue local", "Local catalogue")}
              value={total.titles}
              hint={L(
                "Titres gardés par Ether, où Remix cherche",
                "Titles kept by Ether, where Remix searches",
              )}
            />
            <Metric
              color="var(--viz-4)"
              label={L("Titres cherchés", "Searched titles")}
              value={total.indexed}
              hint={L(
                `${pct(total.indexed, total.titles)} du catalogue`,
                `${pct(total.indexed, total.titles)} of the catalogue`,
              )}
            />
            <Metric
              color="var(--viz-2)"
              label={L("Avec une release", "With a release")}
              value={total.found}
              hint={L("Au moins une release trouvée", "At least one release found")}
            />
            <Metric
              color="var(--viz-3)"
              label={L("Titres affichés", "Shown titles")}
              value={total.playable}
              hint={L(
                `${pct(total.playable, total.indexed)} des titres cherchés · seuil : ${data.min}`,
                `${pct(total.playable, total.indexed)} of searched titles · threshold: ${data.min}`,
              )}
            />
          </div>
          {total.titles === 0 ? (
            <p className="rounded-xl border border-dashed border-border p-8 text-center text-sm text-fg-muted">
              {L(
                "Aucun titre suivi pour le moment. Configurez l’indexation dans Sources → Indexation.",
                "No tracked titles yet. Configure indexing under Sources → Indexing.",
              )}
            </p>
          ) : null}
          <p className="text-sm text-fg-muted">
            {L(
              "Ces nombres portent sur le catalogue local, pas sur tout TMDB. C’est Ether, et non Remix, qui le limite aux titres assez connus et déjà sortis. « Cherchés » indique combien ont déjà été interrogés sur vos sources de releases.",
              "These counts cover the local catalogue, not all of TMDB. Ether, not Remix, limits it to titles popular enough and already released. “Searched” counts titles already queried on your release sources.",
            )}{" "}
            <Link className="underline" to="/catalog/settings">
              {L(
                "Réglage Ether : Masquer les titres obscurs",
                "Ether setting: Hide obscure titles",
              )}
            </Link>
          </p>
          <div className="grid gap-4 xl:grid-cols-3 md:grid-cols-2">
            {TYPES.map(([type, fr, en]) => {
              const c = types[type];
              if (!c) return null;
              return (
                <article
                  key={type}
                  className="remix-stat-card rounded-xl border border-border bg-surface p-5"
                >
                  <div className="mb-4 flex min-h-12 items-center justify-between gap-3">
                    <h3 className="font-semibold">{L(fr, en)}</h3>
                    <span className="text-sm text-fg-muted">
                      {num(c.titles)}{" "}
                      {type === "anime"
                        ? L(
                            "dont animes · inclus dans Films et Séries",
                            "of which anime · included in Movies and TV shows",
                          )
                        : L("titres du catalogue local", "titles in the local catalogue")}
                    </span>
                  </div>
                  <CoverageRing
                    value={c.indexed}
                    total={c.titles}
                    color={
                      type === "movie"
                        ? "var(--viz-1)"
                        : type === "show"
                          ? "var(--viz-2)"
                          : "var(--viz-3)"
                    }
                    label={L("Recherche effectuée", "Search completed")}
                  />
                  <dl className="mt-5 grid grid-cols-3 gap-3 text-sm">
                    {[
                      [L("Cherchés", "Searched"), c.indexed],
                      [L("Avec release", "With release"), c.found],
                      [L("Affichés", "Shown"), c.playable],
                    ].map(([label, value]) => (
                      <div key={label}>
                        <dt className="text-xs text-fg-muted">{label}</dt>
                        <dd className="mt-1 font-semibold tabular-nums">{num(value as number)}</dd>
                      </div>
                    ))}
                  </dl>
                </article>
              );
            })}
          </div>
        </>
      ) : null}
      <CatalogHistory
        localMovies={data ? (types.movie?.titles ?? 0) : null}
        localShows={data ? (types.show?.titles ?? 0) : null}
      />
      <IndexerActivity />
      {data ? (
        <>
          <section className="space-y-3">
            <h3 className="font-semibold">
              {L("Activité et fraîcheur", "Activity and freshness")}
            </h3>
            <div className="grid gap-3 md:grid-cols-3">
              <Metric
                label={L("Recherchés en 24 h", "Searched in 24 h")}
                value={total.recent}
                hint={L("Titres interrogés récemment", "Recently queried titles")}
              />
              <Metric
                label={L("Observations anciennes", "Old observations")}
                value={total.stale}
                warning
                hint={L(
                  "Titres affichés, non recherchés depuis 3 semaines",
                  "Shown titles not searched for 3 weeks",
                )}
              />
              <Metric
                label={L("Titres avec une source en erreur", "Titles with a source error")}
                value={total.failing}
                warning
                hint={L(
                  "Dernière recherche incomplète · nouvel essai sous 24 h",
                  "Last search incomplete · retry within 24 h",
                )}
              />
            </div>
          </section>
          {data.libraries?.length ? (
            <section className="space-y-3">
              <h3 className="font-semibold">{L("Bibliothèques à thème", "Theme libraries")}</h3>
              <p className="text-xs text-fg-muted">
                {L(
                  "Un titre peut appartenir à plusieurs bibliothèques : ces nombres ne s’additionnent pas.",
                  "A title can belong to several libraries: these counts are not additive.",
                )}
              </p>
              {data.libraries.map((lib, i) => (
                <details
                  key={`${lib.id}-${i}`}
                  className="overflow-hidden rounded-xl border border-border"
                >
                  <summary className="cursor-pointer px-5 py-4 text-sm marker:text-fg-muted">
                    <span className="font-medium">{lib.name}</span>
                    <span className="ml-3 text-xs text-fg-muted">
                      {num(lib.playable)} / {num(lib.titles)} {L("titres affichés", "titles shown")}
                    </span>
                  </summary>
                  <Breakdown
                    label={L("Bibliothèque / collection", "Library / collection")}
                    rows={[
                      { ...lib, name: L("Ensemble de la bibliothèque", "Whole library") },
                      ...(lib.rows ?? []),
                    ]}
                  />
                </details>
              ))}
            </section>
          ) : null}
          {data.collections?.length ? (
            <details className="overflow-hidden rounded-xl border border-border">
              <summary className="cursor-pointer px-5 py-4 text-sm font-medium">
                {L("Toutes les collections", "All collections")}{" "}
                <span className="ml-2 text-fg-muted">({num(data.collections.length)})</span>
              </summary>
              <Breakdown label={L("Collection", "Collection")} rows={data.collections} />
            </details>
          ) : null}
          {data.empty > 0 ? (
            <p className="text-xs text-fg-muted">
              {L(
                `${data.empty} collection(s) pas encore construite(s) ou réservée(s) à un spectateur, non détaillée(s).`,
                `${data.empty} collection(s) not built yet or private to a viewer, not detailed.`,
              )}
            </p>
          ) : null}
          <details className="rounded-xl border border-border px-5 py-4 text-sm">
            <summary className="cursor-pointer font-medium">
              {L("Comprendre les chiffres", "Understanding the numbers")}
            </summary>
            <div className="mt-4 space-y-3 text-fg-muted">
              <p>
                {L(
                  `Un titre affiché possède au moins ${data.min} releases correspondant à vos options. Son pourcentage se rapporte aux titres cherchés, pas à tout le catalogue.`,
                  `A shown title has at least ${data.min} releases matching your options. Its percentage is based on searched titles, not the entire catalogue.`,
                )}
              </p>
              <p>
                {L(
                  "Une observation ancienne date de plus de 3 semaines. Une release non revue depuis 60 jours ne compte plus. Si une source ne répond pas, ses releases précédentes sont conservées et une nouvelle recherche est tentée sous 24 h.",
                  "An old observation is over 3 weeks old. A release not seen for 60 days no longer counts. If a source fails, its previous releases are retained and a new search is attempted within 24 hours.",
                )}
              </p>
              <p>
                {L(
                  "Le marqueur « en cache » est annoncé par l’addon, sans vérification auprès du debrid. Les chiffres ne garantissent pas la lecture de chaque titre.",
                  "The cached marker is reported by the addon, without verification against the debrid service. These figures do not guarantee playback of every title.",
                )}
              </p>
            </div>
          </details>
        </>
      ) : null}
    </section>
  );
}
