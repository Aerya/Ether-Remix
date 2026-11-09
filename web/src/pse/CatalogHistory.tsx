import { Button } from "@braedonsaunders/appkit-ui";
import { RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const num = (n: number) => n.toLocaleString(getLocale());
interface Bucket {
  start: string;
  movies: number;
  shows: number;
  movieSearches: number;
  showSearches: number;
  cumulativeMovies: number;
  cumulativeShows: number;
  cumulativeMovieSearches: number;
  cumulativeShowSearches: number;
}
interface History {
  at: number;
  period: string;
  tmdb: { movies: number; shows: number; exportDate: string; updatedAt: number; error?: string };
  history: Bucket[];
  sources: { id: string; name: string }[];
  searchTrackingSince: string;
  undated: number;
}
type Period = "day" | "week" | "month" | "year";

export function CatalogHistory({
  localMovies,
  localShows,
}: {
  localMovies: number | null;
  localShows: number | null;
}) {
  const [period, setPeriod] = useState<Period>("day");
  const [source, setSource] = useState("");
  const [metric, setMetric] = useState("catalogue");
  const [media, setMedia] = useState("both");
  const [cumulative, setCumulative] = useState(false);
  const [data, setData] = useState<History | null>(null);
  const [dataKey, setDataKey] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const requestedSource = metric === "searches" ? source : "";
  const key = period + "|" + requestedSource;
  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let first = true;
    const controller = new AbortController();
    async function load() {
      if (document.hidden) {
        timer = setTimeout(load, 60000);
        return;
      }
      setBusy(true);
      const params = new URLSearchParams({ period, source: requestedSource });
      if (first && refresh) params.set("refresh", "1");
      first = false;
      try {
        const result = await apiFetch<History>("/pse/indexer/history?" + params, {
          signal: controller.signal,
        });
        if (!disposed) {
          setData(result);
          setDataKey(key);
          setError("");
        }
      } catch {
        if (!disposed)
          setError(
            L(
              "Historique indisponible. Les dernières données reçues restent affichées.",
              "History unavailable. Last received data remains visible.",
            ),
          );
      } finally {
        if (!disposed) {
          setBusy(false);
          timer = setTimeout(load, 60000);
        }
      }
    }
    void load();
    return () => {
      disposed = true;
      controller.abort();
      clearTimeout(timer);
    };
  }, [period, requestedSource, refresh, key]);
  const buckets = dataKey === key ? (data?.history ?? []) : [];
  const measured = (start: string) => {
    if (metric === "catalogue") return true;
    if (!data?.searchTrackingSince) return false;
    const end = new Date(start + "T00:00:00Z");
    if (period === "year") end.setUTCFullYear(end.getUTCFullYear() + 1);
    else if (period === "month") end.setUTCMonth(end.getUTCMonth() + 1);
    else end.setUTCDate(end.getUTCDate() + (period === "week" ? 7 : 1));
    return end.toISOString().slice(0, 10) > data.searchTrackingSince;
  };
  const values = (b: Bucket, cum = cumulative) => ({
    movies:
      media === "show"
        ? 0
        : metric === "catalogue"
          ? cum
            ? b.cumulativeMovies
            : b.movies
          : cum
            ? b.cumulativeMovieSearches
            : b.movieSearches,
    shows:
      media === "movie"
        ? 0
        : metric === "catalogue"
          ? cum
            ? b.cumulativeShows
            : b.shows
          : cum
            ? b.cumulativeShowSearches
            : b.showSearches,
  });
  const series = buckets.map((b) => ({ ...values(b), start: b.start }));
  const max = Math.max(1, ...series.map((b) => b.movies + b.shows));
  const total = cumulative
    ? (series.at(-1) ?? { movies: 0, shows: 0 })
    : series.reduce((a, b) => ({ movies: a.movies + b.movies, shows: a.shows + b.shows }), {
        movies: 0,
        shows: 0,
      });
  const dateLabel = (day: string) =>
    new Date(day + "T00:00:00Z").toLocaleDateString(
      getLocale(),
      period === "year"
        ? { year: "numeric", timeZone: "UTC" }
        : period === "month"
          ? { month: "short", year: "numeric", timeZone: "UTC" }
          : { day: "2-digit", month: "2-digit", timeZone: "UTC" },
    );
  const selectClass = "rounded-lg border border-border bg-surface px-3 py-2 text-sm text-fg";
  return (
    <section
      className="space-y-5"
      aria-label={L("Repères TMDB et historique", "TMDB reference and history")}
    >
      <div className="rounded-xl border border-border p-5">
        <h3 className="font-semibold">
          {L("Ether-Remix et le catalogue TMDB", "Ether-Remix and the TMDB catalogue")}
        </h3>
        <p className="mt-1 text-xs text-fg-muted">
          {L(
            "Repère global · listes standard TMDB, hors exports adultes · relevé quotidien",
            "Global reference · standard TMDB lists, excluding adult exports · daily snapshot",
          )}
        </p>
        {data?.tmdb.updatedAt ? (
          <>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              {(
                [
                  [L("Films", "Movies"), data.tmdb.movies, localMovies],
                  [L("Séries", "Shows"), data.tmdb.shows, localShows],
                ] as const
              ).map(([name, worldwide, local]) => (
                <div key={name} className="rounded-lg bg-surface p-4">
                  <div className="text-sm text-fg-muted">{name}</div>
                  <div className="mt-1 text-2xl font-semibold tabular-nums">
                    {num(worldwide)}{" "}
                    <span className="text-xs font-normal text-fg-muted">
                      {L("sur TMDB", "on TMDB")}
                    </span>
                  </div>
                  <p className="mt-2 text-sm">
                    {local === null ? "—" : num(local)} {L("dans Ether-Remix", "in Ether-Remix")}
                    {local !== null && worldwide > 0 ? (
                      <span className="text-fg-muted">
                        {" "}
                        ·{" "}
                        {((100 * local) / worldwide).toLocaleString(getLocale(), {
                          maximumFractionDigits: 2,
                        })}{" "}
                        %
                      </span>
                    ) : null}
                  </p>
                </div>
              ))}
            </div>
            <p className="mt-3 text-xs text-fg-muted">
              {L("Export du", "Export dated")} {data.tmdb.exportDate} ·{" "}
              {L(
                "Ce pourcentage situe la sélection locale ; il ne mesure pas les titres lisibles ni un objectif de complétude.",
                "This percentage puts the local selection in context; it measures neither playable titles nor a completeness target.",
              )}{" "}
              <a
                className="underline"
                href="https://developer.themoviedb.org/docs/daily-id-exports"
                target="_blank"
                rel="noreferrer"
              >
                {L("Source TMDB", "TMDB source")}
              </a>
            </p>
          </>
        ) : (
          <p className="mt-4 text-sm text-fg-muted">
            {L(
              "Premier relevé TMDB en attente. Il est téléchargé en arrière-plan puis conservé en cache.",
              "Waiting for the first TMDB snapshot. It is downloaded in the background and cached.",
            )}
          </p>
        )}
        {data?.tmdb.error ? <p className="mt-3 text-sm text-warning">{data.tmdb.error}</p> : null}
      </div>
      <div className="rounded-xl border border-border p-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="font-semibold">{L("Historique de l’indexation", "Indexing history")}</h3>
            <p className="mt-1 text-xs text-fg-muted">
              {L(
                "Périodes UTC · 30 jours, 12 semaines, 12 mois ou 10 années",
                "UTC periods · 30 days, 12 weeks, 12 months or 10 years",
              )}
            </p>
          </div>
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => setRefresh((v) => v + 1)}
          >
            <RefreshCw size={14} className="mr-2" />
            {L("Actualiser", "Refresh")}
          </Button>
        </div>
        <div className="mt-5 flex flex-wrap items-center gap-2">
          <select
            aria-label={L("Mesure historique", "History metric")}
            className={selectClass}
            value={metric}
            onChange={(e) => setMetric(e.target.value)}
          >
            <option value="catalogue">{L("Ajouts au catalogue", "Catalogue additions")}</option>
            <option value="searches">{L("Recherches par source", "Source searches")}</option>
          </select>
          <select
            aria-label={L("Période historique", "History period")}
            className={selectClass}
            value={period}
            onChange={(e) => setPeriod(e.target.value as Period)}
          >
            <option value="day">{L("Jour", "Day")}</option>
            <option value="week">{L("Semaine", "Week")}</option>
            <option value="month">{L("Mois", "Month")}</option>
            <option value="year">{L("Année", "Year")}</option>
          </select>
          <select
            aria-label={L("Type de média historique", "History media type")}
            className={selectClass}
            value={media}
            onChange={(e) => setMedia(e.target.value)}
          >
            <option value="both">{L("Films et séries", "Movies and shows")}</option>
            <option value="movie">{L("Films", "Movies")}</option>
            <option value="show">{L("Séries", "Shows")}</option>
          </select>
          {metric === "searches" ? (
            <select
              aria-label={L("Source historique", "History source")}
              className={selectClass}
              value={source}
              onChange={(e) => setSource(e.target.value)}
            >
              <option value="">{L("Toutes les sources", "All sources")}</option>
              {(data?.sources ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          ) : null}
          <div className="flex gap-1 rounded-lg border border-border p-1">
            <Button
              size="sm"
              variant={cumulative ? "ghost" : "outline"}
              aria-pressed={!cumulative}
              onClick={() => setCumulative(false)}
            >
              {L("Par période", "Per period")}
            </Button>
            <Button
              size="sm"
              variant={cumulative ? "outline" : "ghost"}
              aria-pressed={cumulative}
              onClick={() => setCumulative(true)}
            >
              {L("Cumulé", "Cumulative")}
            </Button>
          </div>
        </div>
        {error ? (
          <p role="alert" className="mt-3 text-sm text-warning">
            {error}
          </p>
        ) : null}
        <div className="mt-5 flex flex-wrap items-center gap-5 text-sm">
          <span className="font-medium">
            {cumulative
              ? L("Cumul à date", "Cumulative to date")
              : L("Total sur la période", "Period total")}{" "}
            :{" "}
            <span className="tabular-nums">
              {metric === "searches" && !data?.searchTrackingSince
                ? "—"
                : num(total.movies + total.shows)}
            </span>
          </span>
          {media !== "show" ? (
            <span className="flex items-center gap-2">
              <i className="h-2.5 w-2.5 rounded-sm" style={{ background: "var(--viz-1)" }} />
              {L("Films", "Movies")} :{" "}
              {metric === "searches" && !data?.searchTrackingSince ? "—" : num(total.movies)}
            </span>
          ) : null}
          {media !== "movie" ? (
            <span className="flex items-center gap-2">
              <i className="h-2.5 w-2.5 rounded-sm" style={{ background: "var(--viz-2)" }} />
              {L("Séries", "Shows")} :{" "}
              {metric === "searches" && !data?.searchTrackingSince ? "—" : num(total.shows)}
            </span>
          ) : null}
        </div>
        {buckets.length ? (
          <div className="mt-5 overflow-x-auto">
            <div
              className="flex h-56 min-w-[640px] items-end gap-2"
              role="img"
              aria-label={series
                .map((b) =>
                  measured(b.start)
                    ? dateLabel(b.start) +
                      ": " +
                      b.movies +
                      " " +
                      L("films", "movies") +
                      ", " +
                      b.shows +
                      " " +
                      L("séries", "shows")
                    : dateLabel(b.start) + ": " + L("non mesuré", "not measured"),
                )
                .join("; ")}
            >
              {series.map((b, i) => (
                <div
                  className="flex h-full min-w-0 flex-1 flex-col justify-end"
                  key={b.start}
                  title={
                    dateLabel(b.start) +
                    " · " +
                    (measured(b.start) ? num(b.movies + b.shows) : L("Non mesuré", "Not measured"))
                  }
                >
                  <div
                    className={
                      measured(b.start)
                        ? "flex min-h-0 flex-col justify-end"
                        : "flex min-h-0 flex-col justify-end rounded-sm border border-dashed border-border bg-surface/40"
                    }
                    style={{ height: measured(b.start) ? ((b.movies + b.shows) / max) * 185 : 185 }}
                  >
                    {measured(b.start) && b.shows ? (
                      <div
                        className="rounded-t-sm"
                        style={{
                          height: (b.shows / (b.movies + b.shows)) * 100 + "%",
                          background: "var(--viz-2)",
                        }}
                      />
                    ) : null}
                    {measured(b.start) && b.movies ? (
                      <div
                        className={b.shows ? "" : "rounded-t-sm"}
                        style={{
                          height: (b.movies / (b.movies + b.shows)) * 100 + "%",
                          background: "var(--viz-1)",
                        }}
                      />
                    ) : null}
                  </div>
                  <span className="mt-2 h-7 whitespace-nowrap text-center text-[10px] text-fg-muted">
                    {i % (period === "day" ? 3 : 1) === 0 ? dateLabel(b.start) : ""}
                  </span>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <p className="py-12 text-center text-sm text-fg-muted">
            {busy
              ? L("Chargement de l’historique…", "Loading history…")
              : L("Aucune donnée pour cette sélection.", "No data for this selection.")}
          </p>
        )}
        <p className="mt-4 text-xs text-fg-muted">
          {metric === "catalogue"
            ? L(
                "Ajouts d’après les dates d’entrée des médias conservés dans le catalogue local. Le cumul inclut les ajouts datés antérieurs à la fenêtre affichée.",
                "Additions use the entry dates of media retained in the local catalogue. Cumulative values include dated additions before the displayed window.",
              )
            : data?.searchTrackingSince
              ? L(
                  `Recherches conservées depuis le ${data.searchTrackingSince}. Une recherche = un titre interrogé auprès d’une source ; un titre peut être recherché plusieurs fois. Les périodes antérieures n’ont pas été mesurées.`,
                  `Searches retained since ${data.searchTrackingSince}. One search = one title queried on one source; a title may be searched several times. Earlier periods were not measured.`,
                )
              : L(
                  "Le suivi par source commencera au prochain passage de l’indexeur ; les périodes précédentes ne sont pas reconstituées.",
                  "Source tracking starts with the next indexer run; earlier periods are not reconstructed.",
                )}
        </p>
        {metric === "catalogue" && data?.undated ? (
          <p className="mt-2 text-xs text-fg-muted">
            {num(data.undated)}{" "}
            {L(
              "titres sans date d’ajout, exclus de cet historique.",
              "titles without an added date, excluded from this history.",
            )}
          </p>
        ) : null}
        <details className="mt-5 border-t border-border pt-4">
          <summary className="cursor-pointer text-sm font-medium">
            {L("Valeurs détaillées", "Detailed values")}
          </summary>
          <div className="mt-3 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-fg-muted">
                  <th className="py-2">{L("Période", "Period")}</th>
                  {media !== "show" ? <th className="text-right">{L("Films", "Movies")}</th> : null}
                  {media !== "movie" ? (
                    <th className="text-right">{L("Séries", "Shows")}</th>
                  ) : null}
                  <th className="text-right">{L("Total période", "Period total")}</th>
                  <th className="text-right">{L("Total cumulé", "Cumulative total")}</th>
                </tr>
              </thead>
              <tbody>
                {buckets.map((b) => {
                  const v = values(b, false),
                    c = values(b, true);
                  return (
                    <tr key={b.start} className="border-b border-border last:border-0">
                      <th scope="row" className="py-2 text-left font-normal">
                        {dateLabel(b.start)}
                      </th>
                      {media !== "show" ? (
                        <td className="text-right tabular-nums">
                          {measured(b.start) ? num(v.movies) : "—"}
                        </td>
                      ) : null}
                      {media !== "movie" ? (
                        <td className="text-right tabular-nums">
                          {measured(b.start) ? num(v.shows) : "—"}
                        </td>
                      ) : null}
                      <td className="text-right tabular-nums">
                        {measured(b.start) ? num(v.movies + v.shows) : "—"}
                      </td>
                      <td className="text-right tabular-nums">
                        {measured(b.start) ? num(c.movies + c.shows) : "—"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </details>
      </div>
    </section>
  );
}
