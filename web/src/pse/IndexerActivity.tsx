import { Button } from "@braedonsaunders/appkit-ui";
import { Activity, ArrowDown, ArrowUp, ArrowUpDown, Pause, Play, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const num = (n: number) => n.toLocaleString(getLocale());
const compact = (n: number) =>
  n.toLocaleString(getLocale(), { notation: "compact", maximumFractionDigits: 1 });
interface Event {
  id: number;
  sourceId: string;
  source: string;
  title: string;
  type: string;
  anime?: boolean;
  started: number;
  finished?: number;
  status: string;
  results: number;
  durationMs: number;
}
interface Live {
  at: number;
  state: string;
  active: Event[];
  recent: Event[];
}
interface Source {
  id: string;
  name: string;
  enabled: boolean;
  selected: boolean;
  limit: number;
  hits: number;
  titles: number;
  releases: number;
  searches: number;
  errors: number;
  partial: number;
  denied: number;
  results: number;
  durationMs: number;
}
type SourceSort = "name" | "hits" | "searches" | "errors" | "denied" | "duration" | "titles";
interface Overview {
  at: number;
  sources: Source[];
  history: { day: string; sourceId: string; hits: number; searches: number; errors: number }[];
}
const resultLabel = (status: string) =>
  ({
    success: L("Résultats reçus", "Results received"),
    empty: L("Aucun résultat", "No results"),
    partial: L("Réponse partielle", "Partial response"),
    error: L("Source en erreur", "Source error"),
    budget: L("Quota atteint", "Quota reached"),
    cancelled: L("Annulée", "Cancelled"),
    searching: L("Recherche en cours", "Searching"),
  })[status] ?? status;
const stateLabel = (state: string) =>
  ({
    searching: L("Recherche en cours", "Searching"),
    waiting: L("En attente du prochain passage", "Waiting for the next run"),
    disabled: L("Indexation désactivée", "Indexing disabled"),
    paused: L("En pause pendant une lecture", "Paused during playback"),
    no_sources: L("Aucune source sélectionnée", "No selected sources"),
  })[state] ?? state;

export function IndexerActivity() {
  const [live, setLive] = useState<Live | null>(null);
  const [overview, setOverview] = useState<Overview | null>(null);
  const [automatic, setAutomatic] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState("");
  const [source, setSource] = useState("");
  const [busy, setBusy] = useState(false);
  const [sort, setSort] = useState<{ key: SourceSort; descending: boolean }>({
    key: "name",
    descending: false,
  });
  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let lastSources = 0;
    let running = false;
    const controller = new AbortController();
    async function poll() {
      if (disposed || running) return;
      if (document.hidden) {
        if (automatic) timer = setTimeout(poll, 3000);
        return;
      }
      running = true;
      setBusy(true);
      const tasks: Promise<unknown>[] = [
        apiFetch<Live>("/pse/indexer/activity", { signal: controller.signal }).then((value) => {
          if (!disposed) setLive(value);
        }),
      ];
      if (Date.now() - lastSources >= 30000)
        tasks.push(
          apiFetch<Overview>("/pse/indexer/sources", { signal: controller.signal }).then(
            (value) => {
              if (!disposed) {
                setOverview(value);
                lastSources = Date.now();
              }
            },
          ),
        );
      const results = await Promise.allSettled(tasks);
      if (!disposed) {
        setError(
          results.some((r) => r.status === "rejected")
            ? L(
                "Actualisation interrompue. Les dernières données reçues restent affichées.",
                "Refresh interrupted. Last received data remains visible.",
              )
            : "",
        );
        setBusy(false);
      }
      running = false;
      if (!disposed && automatic) timer = setTimeout(poll, 3000);
    }
    const visible = () => {
      if (!document.hidden && automatic) {
        clearTimeout(timer);
        void poll();
      }
    };
    document.addEventListener("visibilitychange", visible);
    void poll();
    return () => {
      disposed = true;
      controller.abort();
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [automatic, refresh]);
  const sources = overview?.sources ?? [];
  const selectedSources = sources.filter((s) => !source || s.id === source);
  const sourceName = (s: Source) => s.name || s.id;
  const compareNames = (a: Source, b: Source) =>
    sourceName(a).localeCompare(sourceName(b), getLocale(), {
      numeric: true,
      sensitivity: "base",
    }) || a.id.localeCompare(b.id);
  const sortedSources = [...selectedSources].sort((a, b) => {
    // Une durée absente reste en fin de tableau dans les deux sens.
    if (sort.key === "duration" && (!a.searches || !b.searches))
      return Number(!a.searches) - Number(!b.searches) || compareNames(a, b);
    const value = (s: Source) => {
      switch (sort.key) {
        case "hits":
          return s.hits;
        case "searches":
          return s.searches;
        case "errors":
          return s.errors + s.partial;
        case "denied":
          return s.denied;
        case "duration":
          return s.durationMs / s.searches;
        case "titles":
          return s.titles;
        default:
          return 0;
      }
    };
    const comparison = sort.key === "name" ? compareNames(a, b) : value(a) - value(b);
    return (sort.descending ? -comparison : comparison) || compareNames(a, b);
  });
  const columns: { key: SourceSort; label: string; title?: string }[] = [
    { key: "name", label: L("Source", "Source") },
    {
      key: "hits",
      label: L("Requêtes / quota", "Requests / quota"),
      title: L("Trier par nombre de requêtes", "Sort by request count"),
    },
    { key: "searches", label: L("Recherches", "Searches") },
    {
      key: "errors",
      label: L("Erreurs / partielles", "Errors / partial"),
      title: L(
        "Trier par total des erreurs et réponses partielles",
        "Sort by total errors and partial responses",
      ),
    },
    { key: "denied", label: L("Quotas refusés", "Quota denials") },
    { key: "duration", label: L("Durée moyenne", "Average duration") },
    { key: "titles", label: L("Titres en stock", "Stored titles") },
  ];
  const days = [...new Set(overview?.history.map((h) => h.day) ?? [])];
  const history = days.map((day) => ({
    day,
    hits: (overview?.history ?? [])
      .filter((h) => h.day === day && (!source || source === h.sourceId))
      .reduce((sum, h) => sum + h.hits, 0),
  }));
  const maxHits = Math.max(1, ...history.map((h) => h.hits));
  const maxReleases = Math.max(1, ...selectedSources.map((s) => s.releases));
  const active = (live?.active ?? []).filter((e) => !source || source === e.sourceId);
  const recent = (live?.recent ?? []).filter((e) => !source || source === e.sourceId);

  return (
    <section
      className="space-y-5"
      aria-label={L("Sources et activité en direct", "Sources and live activity")}
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Activity size={18} className="text-primary" />
          <h3 className="font-semibold">{L("Sources et activité", "Sources and activity")}</h3>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <select
            aria-label={L("Filtrer par source", "Filter by source")}
            value={source}
            onChange={(e) => setSource(e.target.value)}
            className="min-w-0 rounded-lg border border-border bg-surface px-3 py-2 text-sm text-fg"
          >
            <option value="">{L("Toutes les sources", "All sources")}</option>
            {sources.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name || s.id}
              </option>
            ))}
          </select>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setAutomatic(!automatic)}
            aria-pressed={automatic}
          >
            {automatic ? <Pause size={14} className="mr-2" /> : <Play size={14} className="mr-2" />}
            {automatic
              ? L("Suspendre le suivi", "Pause updates")
              : L("Reprendre le suivi", "Resume updates")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => setRefresh((n) => n + 1)}
            aria-label={L("Actualiser l’activité", "Refresh activity")}
          >
            <RefreshCw size={14} />
          </Button>
        </div>
      </div>
      <div className="rounded-xl border border-border bg-surface p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="flex items-center gap-2 text-sm font-medium">
            <span
              className={`h-2 w-2 rounded-full ${error ? "bg-warning" : automatic ? "bg-success" : "bg-fg-muted"}`}
            />
            {live ? stateLabel(live.state) : L("Connexion…", "Connecting…")}
          </p>
          <span className="text-xs text-fg-muted">
            {automatic
              ? L("Suivi toutes les 3 s", "Updates every 3s")
              : L("Suivi suspendu", "Updates paused")}
            {live ? ` · ${new Date(live.at).toLocaleTimeString(getLocale())}` : ""}
          </span>
        </div>
        {error ? (
          <p role="alert" className="mt-3 text-sm text-warning">
            {error}
          </p>
        ) : null}
        {active.length ? (
          <div className="mt-4 space-y-3">
            {active.map((e) => (
              <div
                key={e.id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border p-4"
              >
                <div>
                  <p className="font-medium">
                    {e.title}
                    {e.anime ? (
                      <span className="ml-2 rounded bg-primary/10 px-2 py-0.5 text-xs text-primary">
                        {L("Anime", "Anime")}
                      </span>
                    ) : null}
                  </p>
                  <p className="mt-1 text-xs text-fg-muted">
                    {e.source || e.sourceId} ·{" "}
                    {e.type === "show"
                      ? L("Série · épisode 1", "Show · episode 1")
                      : L("Film", "Movie")}
                  </p>
                </div>
                <span className="text-sm tabular-nums text-primary">
                  {Math.max(0, Math.round(((live?.at ?? e.started) - e.started) / 1000))} s ·{" "}
                  {L("en cours", "in progress")}
                </span>
              </div>
            ))}
          </div>
        ) : (
          <p className="mt-3 text-sm text-fg-muted">
            {source && live?.active.length
              ? L("Une autre source est interrogée.", "Another source is being queried.")
              : L("Aucune recherche de titre en cours.", "No title search in progress.")}
          </p>
        )}
        <p className="mt-3 text-xs text-fg-muted">
          {L(
            "Le suivi concerne l’indexation en arrière-plan. Le suspendre ici n’arrête pas les recherches.",
            "Tracking covers background indexing. Pausing updates here does not stop searches.",
          )}
        </p>
      </div>
      <div className="grid gap-5 lg:grid-cols-2">
        <article className="rounded-xl border border-border p-5">
          <h4 className="text-sm font-semibold">
            {L("Requêtes sur 7 jours", "Requests over 7 days")}
          </h4>
          <p className="mt-1 text-xs text-fg-muted">
            {L("Budget consommé par les sources · jours UTC", "Source budget consumed · UTC days")}
          </p>
          {history.length ? (
            <div
              className="mt-5 flex h-44 items-end gap-3"
              role="img"
              aria-label={
                L("Requêtes quotidiennes", "Daily requests") +
                ": " +
                history.map((h) => h.day + " : " + num(h.hits)).join(", ")
              }
            >
              {history.map((h) => (
                <div
                  key={h.day}
                  className="flex h-full min-w-0 flex-1 flex-col justify-end items-center gap-2"
                >
                  <span className="text-xs tabular-nums" title={num(h.hits)}>
                    {compact(h.hits)}
                  </span>
                  <div
                    className="w-full max-w-10 rounded-t-md bg-viz-1"
                    style={{ height: `${Math.max(h.hits ? 3 : 0, (h.hits / maxHits) * 110)}px` }}
                  />
                  <span className="text-[11px] text-fg-muted">
                    {new Date(h.day + "T12:00:00Z").toLocaleDateString(getLocale(), {
                      day: "2-digit",
                      month: "2-digit",
                      timeZone: "UTC",
                    })}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <p className="py-12 text-center text-sm text-fg-muted">
              {L("Pas encore d’historique.", "No history yet.")}
            </p>
          )}
        </article>
        <article className="rounded-xl border border-border p-5">
          <h4 className="text-sm font-semibold">
            {L("Releases conservées par source", "Stored releases by source")}
          </h4>
          <p className="mt-1 text-xs text-fg-muted">
            {L(
              "État de l’index · un titre peut apparaître dans plusieurs sources",
              "Index snapshot · a title can appear in several sources",
            )}
          </p>
          <div className="mt-5 max-h-48 space-y-4 overflow-y-auto">
            {selectedSources.map((s, i) => (
              <div key={s.id}>
                <div className="mb-1.5 flex justify-between gap-3 text-xs">
                  <span className="truncate">{s.name || s.id}</span>
                  <span className="tabular-nums">{num(s.releases)}</span>
                </div>
                <div className="h-2 rounded-full bg-border">
                  <div
                    className="h-full rounded-full"
                    style={{
                      width: `${(s.releases / maxReleases) * 100}%`,
                      backgroundColor: `var(--viz-${(i % 5) + 1})`,
                    }}
                  />
                </div>
              </div>
            ))}
            {!selectedSources.length ? (
              <p className="py-8 text-center text-sm text-fg-muted">
                {L("Aucune source à afficher.", "No sources to display.")}
              </p>
            ) : null}
          </div>
        </article>
      </div>
      <div className="overflow-hidden rounded-xl border border-border">
        <div className="px-5 py-4">
          <h4 className="text-sm font-semibold">{L("Détail par source", "Source breakdown")}</h4>
          <p className="mt-1 text-xs text-fg-muted">
            {L(
              "Aujourd’hui (UTC) · chiffres actualisés toutes les 30 s. Les erreurs et durées sont mesurées depuis cette mise à jour d’Ether-Remix.",
              "Today (UTC) · figures refreshed every 30s. Errors and durations are measured since this Ether-Remix update.",
            )}
          </p>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead>
              <tr className="border-y border-border bg-surface text-xs text-fg-muted">
                {columns.map((column, i) => {
                  const active = sort.key === column.key;
                  const Icon = active ? (sort.descending ? ArrowDown : ArrowUp) : ArrowUpDown;
                  return (
                    <th
                      key={column.key}
                      scope="col"
                      aria-sort={active ? (sort.descending ? "descending" : "ascending") : "none"}
                      className={`px-4 py-3 font-medium ${i ? "text-right" : "text-left"}`}
                    >
                      <button
                        type="button"
                        title={column.title}
                        className={`inline-flex items-center gap-1.5 rounded-sm hover:text-fg focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary ${active ? "text-fg" : ""}`}
                        onClick={() =>
                          setSort({
                            key: column.key,
                            descending: active ? !sort.descending : column.key !== "name",
                          })
                        }
                      >
                        {column.label}
                        <Icon size={13} aria-hidden="true" className="shrink-0" />
                      </button>
                    </th>
                  );
                })}
              </tr>
            </thead>
            <tbody>
              {sortedSources.map((s) => (
                <tr key={s.id} className="border-b border-border last:border-0">
                  <th scope="row" className="px-4 py-4 text-left font-medium">
                    <span>{s.name || s.id}</span>
                    <span className="mt-1 block text-xs font-normal text-fg-muted">
                      {s.selected
                        ? L("Sélectionnée", "Selected")
                        : s.enabled
                          ? L("Hors indexation", "Not selected")
                          : L("Désactivée ou retirée", "Disabled or removed")}
                    </span>
                  </th>
                  <td className="px-4 py-4 text-right tabular-nums">
                    {num(s.hits)} / {s.selected ? num(s.limit) : "—"}
                  </td>
                  <td className="px-4 py-4 text-right tabular-nums">{num(s.searches)}</td>
                  <td
                    className={`px-4 py-4 text-right tabular-nums ${s.errors + s.partial > 0 ? "text-warning" : ""}`}
                  >
                    {num(s.errors)} / {num(s.partial)}
                  </td>
                  <td className="px-4 py-4 text-right tabular-nums">{num(s.denied)}</td>
                  <td className="px-4 py-4 text-right tabular-nums">
                    {s.searches
                      ? (s.durationMs / s.searches / 1000).toLocaleString(getLocale(), {
                          maximumFractionDigits: 1,
                        }) + " s"
                      : "—"}
                  </td>
                  <td className="px-4 py-4 text-right tabular-nums">{num(s.titles)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="px-5 py-3 text-xs text-fg-muted">
          {L(
            "Une recherche de titre peut consommer plusieurs requêtes. Les releases conservées ne garantissent pas la lecture.",
            "A title search can consume several requests. Stored releases do not guarantee playback.",
          )}
        </p>
      </div>
      <details className="rounded-xl border border-border" open>
        <summary className="cursor-pointer px-5 py-4 text-sm font-semibold">
          {L("Dernières recherches", "Recent searches")}{" "}
          <span className="ml-2 text-xs font-normal text-fg-muted">
            {L(
              "50 dernières · depuis le démarrage de l’indexeur",
              "Last 50 · since indexer startup",
            )}
          </span>
        </summary>
        <ol className="max-h-72 overflow-y-auto px-5 pb-3">
          {recent.map((e) => (
            <li
              key={e.id}
              className="flex flex-wrap items-center justify-between gap-2 border-t border-border py-3 text-sm"
            >
              <div className="min-w-0">
                <p className="font-medium">
                  {e.title}
                  {e.anime ? (
                    <span className="ml-2 rounded bg-primary/10 px-2 py-0.5 text-xs text-primary">
                      {L("Anime", "Anime")}
                    </span>
                  ) : null}
                </p>
                <p className="mt-1 text-xs text-fg-muted">
                  {e.source || e.sourceId} ·{" "}
                  {new Date(e.finished ?? e.started).toLocaleTimeString(getLocale())} ·{" "}
                  {(e.durationMs / 1000).toLocaleString(getLocale(), { maximumFractionDigits: 1 })}{" "}
                  s
                </p>
              </div>
              <div className="text-right">
                <span
                  className={
                    ["error", "partial", "budget"].includes(e.status)
                      ? "text-warning"
                      : "text-fg-muted"
                  }
                >
                  {resultLabel(e.status)}
                </span>
                {e.results > 0 ? (
                  <p className="text-xs text-fg-muted">
                    {num(e.results)} {L("résultats", "results")}
                  </p>
                ) : null}
              </div>
            </li>
          ))}
        </ol>
        {!recent.length ? (
          <p className="px-5 pb-5 text-sm text-fg-muted">
            {L(
              "Les recherches apparaîtront ici dès leur exécution.",
              "Searches will appear here as they run.",
            )}
          </p>
        ) : null}
      </details>
    </section>
  );
}
