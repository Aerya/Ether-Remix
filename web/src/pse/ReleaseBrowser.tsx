import { useEffect, useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
import { ReleaseCard, type ReleaseRow } from "./ReleaseLibrary";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
type Item = {
  key: string;
  title: string;
  category: string;
  count: number;
  firstSeen: number;
  lastSeen: number;
  release?: ReleaseRow;
};
interface Result {
  enabled: boolean;
  preparing?: boolean;
  auditPending?: boolean;
  auditFailed?: boolean;
  items: Item[];
  total: number;
  page: number;
  hasMore?: boolean;
  requestParams?: string;
}
export function periodBounds(
  period: string,
  from: string,
  to: string,
  now = new Date(),
): { from?: string; to?: string } {
  let start: Date | undefined;
  let end: Date | undefined;
  if (period === "custom") {
    if (from) start = new Date(`${from}T00:00:00`);
    if (to) {
      end = new Date(`${to}T00:00:00`);
      end.setDate(end.getDate() + 1);
    }
  } else if (period !== "all") {
    start = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    if (period === "week") start.setDate(start.getDate() - ((start.getDay() + 6) % 7));
    if (period === "month") start.setDate(1);
    end = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1);
  }
  return {
    ...(start ? { from: String(Math.floor(start.getTime() / 1000)) } : {}),
    ...(end ? { to: String(Math.floor(end.getTime() / 1000)) } : {}),
  };
}
export function ReleaseBrowser() {
  const [mode, setMode] = useState("media");
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");
  const [period, setPeriod] = useState("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [date, setDate] = useState("firstSeen");
  const [sort, setSort] = useState("indexed");
  const [page, setPage] = useState(0);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(query);
      setPage(0);
    }, 350);
    return () => window.clearTimeout(timer);
  }, [query]);
  const invalid = period === "custom" && Boolean(from && to && from > to);
  const params = new URLSearchParams({
    browse: "1",
    mode,
    q: search,
    category,
    date,
    sort,
    page: String(page),
    ...periodBounds(period, from, to),
  }).toString();
  const state = useAsync(
    (signal) =>
      invalid
        ? Promise.resolve(null)
        : apiFetch<Result>(`/pse/release-library?${params}`, { signal }).then((result) => ({
            ...result,
            requestParams: params,
          })),
    [params, tick, invalid],
  );
  const d = state.data;
  useEffect(() => {
    if (!d?.preparing || d.auditFailed || state.loading || state.error) return;
    const timer = window.setTimeout(() => setTick((t) => t + 1), d.auditPending ? 5000 : 500);
    return () => window.clearTimeout(timer);
  }, [d, state.loading, state.error]);
  function change(fn: () => void) {
    fn();
    setPage(0);
  }
  const categories: Record<string, string> = {
    movie: L("Films", "Movies"),
    show: L("Séries", "Series"),
    anime: L("Animes", "Anime"),
  };
  const selectClass = "min-w-0 max-w-full rounded border border-border p-2";
  return (
    <section
      className="min-w-0 max-w-full space-y-4"
      aria-label={L("Explorer l’index", "Browse the index")}
    >
      <h3 className="font-semibold">{L("Explorer l’index", "Browse the index")}</h3>
      <div className="flex flex-wrap gap-2">
        {[
          ["media", L("Médias", "Media")],
          ["releases", "Releases"],
        ].map(([key, label]) => (
          <Button
            key={key}
            aria-pressed={mode === key}
            variant={mode === key ? "default" : "outline"}
            onClick={() =>
              change(() => {
                setMode(key!);
                setQuery("");
                setSearch("");
              })
            }
          >
            {label}
          </Button>
        ))}
      </div>
      <label className="block">
        {mode === "media"
          ? L("Chercher un média", "Search media")
          : L("Chercher une release", "Search releases")}
        <input
          type="search"
          maxLength={300}
          className="mt-1 block w-full min-w-0 rounded border border-border p-2"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={
            mode === "media"
              ? L("Titre, titre original ou identifiant…", "Title, original title or identifier…")
              : L("Nom de release ou source…", "Release name or source…")
          }
        />
      </label>
      <div className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <label className="grid min-w-0 gap-1">
          {L("Catégorie", "Category")}
          <select
            className={selectClass}
            value={category}
            onChange={(e) =>
              change(() => {
                setCategory(e.target.value);
              })
            }
          >
            <option value="">{L("Toutes", "All")}</option>
            {Object.entries(categories).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
        </label>
        <label className="grid min-w-0 gap-1">
          {L("Période", "Period")}
          <select
            className={selectClass}
            value={period}
            onChange={(e) => change(() => setPeriod(e.target.value))}
          >
            {[
              ["all", L("Tout l’historique", "All time")],
              ["day", L("Aujourd’hui", "Today")],
              ["week", L("Cette semaine", "This week")],
              ["month", L("Ce mois", "This month")],
              ["custom", L("Dates personnalisées", "Custom dates")],
            ].map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
        </label>
        <label className="grid min-w-0 gap-1">
          {L("Date utilisée", "Date field")}
          <select
            className={selectClass}
            value={date}
            onChange={(e) => change(() => setDate(e.target.value))}
          >
            <option value="firstSeen">{L("Première indexation", "First indexed")}</option>
            <option value="lastSeen">{L("Dernière observation", "Last observed")}</option>
          </select>
        </label>
        <label className="grid min-w-0 gap-1">
          {L("Ordre", "Order")}
          <select
            className={selectClass}
            value={sort}
            onChange={(e) => change(() => setSort(e.target.value))}
          >
            <option value="indexed">{L("Indexations récentes", "Recently indexed")}</option>
            <option value="recent">{L("Observations récentes", "Recently observed")}</option>
            <option value="oldest">{L("Plus anciennes indexations", "Oldest indexed")}</option>
          </select>
        </label>
      </div>
      {period === "custom" && (
        <div className="flex flex-wrap gap-3">
          <label>
            {L("Du", "From")}{" "}
            <input
              className={selectClass}
              type="date"
              value={from}
              onChange={(e) => change(() => setFrom(e.target.value))}
            />
          </label>
          <label>
            {L("Au inclus", "Through")}{" "}
            <input
              className={selectClass}
              type="date"
              value={to}
              onChange={(e) => change(() => setTo(e.target.value))}
            />
          </label>
        </div>
      )}
      <p className="text-sm text-fg-muted">
        {L(
          "Dates dans votre fuseau horaire. Les séries regroupent les épisodes présents dans l’index, sans garantir une saison complète. Animes : classification du catalogue, lorsque disponible. Cette recherche consulte uniquement l’index enregistré.",
          "Dates use your time zone. Series group episodes present in the index, without implying a complete season. Anime uses catalogue classification when available. This search only reads the stored index.",
        )}
      </p>
      {invalid ? (
        <p role="alert">
          {L(
            "La date de fin doit suivre la date de début.",
            "End date must not precede start date.",
          )}
        </p>
      ) : state.error ? (
        <p role="alert">
          {L("Impossible de charger l’index.", "Unable to load the index.")}{" "}
          <Button onClick={() => setTick((t) => t + 1)}>{L("Réessayer", "Retry")}</Button>
        </p>
      ) : state.loading ? (
        <p role="status">{L("Chargement…", "Loading…")}</p>
      ) : d?.auditFailed ? (
        <p role="alert">
          {L(
            "La vérification des associations a échoué. La bibliothèque reste isolée ; consulter les journaux du serveur.",
            "Association verification failed. The library remains isolated; check server logs.",
          )}
        </p>
      ) : d?.auditPending ? (
        <p role="status">
          {L(
            "Vérification des associations média/release en cours…",
            "Verifying media/release associations…",
          )}
        </p>
      ) : d?.preparing ? (
        <p role="status">
          {L(
            "Préparation de la recherche dans les anciennes références… Cette étape reprend automatiquement sans relancer les sources.",
            "Preparing search for existing references… This step resumes automatically without querying sources.",
          )}
        </p>
      ) : null}
      {!invalid && d?.enabled && !d.preparing && (
        <>
          <p aria-live="polite">
            {d.total >= 0
              ? `${d.total.toLocaleString(getLocale())} ${mode === "media" ? L("médias", "media") : "releases"}`
              : L("Releases indexées", "Indexed releases")}
          </p>
          {!d.items.length && (
            <p>{L("Aucun résultat pour ces filtres.", "No results match these filters.")}</p>
          )}
          <div className="grid min-w-0 gap-2">
            {d.items.map((item, i) =>
              item.release ? (
                <ReleaseCard key={`${item.key}-${i}`} row={item.release} title={item.title} />
              ) : (
                <MediaReleases
                  key={item.key}
                  item={item}
                  category={categories[item.category] ?? ""}
                  params={d.requestParams ?? params}
                />
              ),
            )}
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Button disabled={d.page === 0} onClick={() => setPage(d.page - 1)}>
              {L("Précédent", "Previous")}
            </Button>
            <span>
              Page {d.page + 1}
              {d.total >= 0 ? ` / ${Math.max(1, Math.ceil(d.total / 50))}` : ""}
            </span>
            <Button
              disabled={d.hasMore !== undefined ? !d.hasMore : (d.page + 1) * 50 >= d.total}
              onClick={() => setPage(d.page + 1)}
            >
              {L("Suivant", "Next")}
            </Button>
            <Button onClick={() => setTick((t) => t + 1)}>{L("Actualiser", "Refresh")}</Button>
          </div>
        </>
      )}
    </section>
  );
}

function MediaReleases({
  item,
  category,
  params,
}: {
  item: Item;
  category: string;
  params: string;
}) {
  const [opened, setOpened] = useState(false);
  const [isOpen, setIsOpen] = useState(false);
  const [loadedParams, setLoadedParams] = useState(params);
  useEffect(() => {
    if (isOpen) setLoadedParams(params);
  }, [params, isOpen]);
  return (
    <details
      className="min-w-0 rounded-lg border border-border p-3"
      onToggle={(event) => {
        setIsOpen(event.currentTarget.open);
        if (event.currentTarget.open) {
          setOpened(true);
          setLoadedParams(params);
        }
      }}
    >
      <summary className="cursor-pointer [overflow-wrap:anywhere]">
        <span className="font-medium">{item.title}</span>
        <span className="ml-2 text-sm text-fg-muted">
          {category} · {L("Observé le ", "Observed ")}
          {new Date(item.lastSeen * 1000).toLocaleDateString(getLocale())}
        </span>
      </summary>
      {opened && <MediaReleaseResults item={item} params={loadedParams} />}
    </details>
  );
}
function MediaReleaseResults({ item, params }: { item: Item; params: string }) {
  const [page, setPage] = useState(0);
  const query = new URLSearchParams(params);
  query.set("mode", "releases");
  query.set("media", item.key);
  query.delete("q");
  query.set("page", String(page));
  const url = query.toString();
  const state = useAsync(
    (signal) => apiFetch<Result>(`/pse/release-library?${url}`, { signal }),
    [url],
  );
  const d = state.data;
  useEffect(() => {
    if (!d?.preparing || d.auditFailed || state.loading || state.error) return;
    const timer = window.setTimeout(() => void state.reload(), 500);
    return () => window.clearTimeout(timer);
  }, [d, state.loading, state.error]);
  return (
    <div
      className="mt-3 space-y-3"
      aria-label={L("Releases de ", "Releases for ") + item.title}
      aria-busy={state.loading}
    >
      {state.loading && (
        <p role="status">
          {L("Chargement des releases de ce média…", "Loading releases for this media…")}
        </p>
      )}
      {Boolean(state.error) && (
        <p role="alert">
          {L(
            "Chargement impossible. La liste des médias reste disponible.",
            "Unable to load releases. The media list remains available.",
          )}{" "}
          <Button onClick={() => void state.reload()}>{L("Réessayer", "Retry")}</Button>
        </p>
      )}
      {d?.preparing && (
        <p role="status">
          {L("Préparation des anciennes références…", "Preparing existing references…")}
        </p>
      )}
      {d && !d.preparing && (
        <>
          {!d.items.length && (
            <p>{L("Aucune release pour ces filtres.", "No releases match these filters.")}</p>
          )}
          {d.items.map(
            (entry, i) =>
              entry.release && <ReleaseCard key={entry.key + "-" + i} row={entry.release} />,
          )}
          {d.total > 50 && (
            <div className="flex flex-wrap items-center gap-2">
              <Button disabled={d.page === 0 || state.loading} onClick={() => setPage(d.page - 1)}>
                {L("Précédent", "Previous")}
              </Button>
              <span>
                Page {d.page + 1} / {Math.ceil(d.total / 50)}
              </span>
              <Button disabled={!d.hasMore || state.loading} onClick={() => setPage(d.page + 1)}>
                {L("Suivant", "Next")}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
