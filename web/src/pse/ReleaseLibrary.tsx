import { titleScheduleApi } from "../pages/libraries/browser/titleSchedule";
import { useEffect, useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export interface ReleaseRow {
  name: string;
  source: string;
  protocol: string;
  size: number;
  height: number;
  videoCodec: string;
  audioCodec: string;
  languages: string[] | null;
  cached: string[] | null;
  published: number;
  firstSeen: number;
  lastSeen: number;
  state: string;
}
type SortKey = keyof ReleaseRow;
export function sortReleases(rows: ReleaseRow[], key: SortKey, descending: boolean) {
  return [...rows].sort((a, b) => {
    const av = a[key],
      bv = b[key];
    const unknown = (v: ReleaseRow[SortKey]) =>
      v == null || v === "" || v === 0 || (Array.isArray(v) && v.length === 0);
    if (unknown(av) !== unknown(bv)) return unknown(av) ? 1 : -1;
    if (typeof av === "number" && typeof bv === "number") {
      return (av - bv) * (descending ? -1 : 1) || a.name.localeCompare(b.name);
    }
    return (
      String(av ?? "").localeCompare(String(bv ?? ""), getLocale(), { numeric: true }) *
        (descending ? -1 : 1) || a.name.localeCompare(b.name)
    );
  });
}
const date = (n: number) => (n ? new Date(n * 1000).toLocaleString(getLocale()) : "—");
const stateLabel = (s: string) =>
  ({
    ready: L("Référence retenue", "Retained reference"),
    excluded: L("Hors critères · suppression en cours", "Outside criteria · removal pending"),
    metadata: L("Métadonnées seules", "Metadata only"),
    expired: L("À actualiser", "Needs refresh"),
    failed: L("Échec de lecture", "Playback failed"),
    source_changed: L("Source modifiée", "Source changed"),
  })[s] ?? s;
export function ReleaseLibrary({
  titleKey,
  seasons = [],
  refresh = 0,
}: {
  titleKey: string;
  refresh?: number;
  seasons?: { season: number; title: string }[];
}) {
  const [season, setSeason] = useState<number | null>(null);
  const episodeState = useAsync(
    (signal) =>
      season === null ? Promise.resolve([]) : titleScheduleApi.season(titleKey, season, signal),
    [titleKey, season],
  );
  const episodes = episodeState.data ?? [];
  const [episode, setEpisode] = useState("");
  const key = episode || titleKey;
  const state = useAsync(
    (signal) =>
      apiFetch<{ enabled: boolean; releases: ReleaseRow[] }>(
        `/pse/release-library?key=${encodeURIComponent(key)}`,
        { signal },
      ),
    [key, refresh],
  );
  const [page, setPage] = useState(0);
  const [sort, setSort] = useState<SortKey>("height");
  const [descending, setDescending] = useState(true);
  const [query, setQuery] = useState("");
  const rows = sortReleases(
    (state.data?.releases ?? []).filter((r) =>
      `${r.name} ${r.source} ${r.videoCodec} ${r.audioCodec} ${(r.languages ?? []).join(" ")}`
        .toLocaleLowerCase()
        .includes(query.toLocaleLowerCase()),
    ),
    sort,
    descending,
  );
  useEffect(() => setPage(0), [key, query, sort, descending]);
  const currentPage = Math.min(page, Math.max(0, Math.ceil(rows.length / 50) - 1));
  const columns: [SortKey, string][] = [
    ["name", "Release"],
    ["height", L("Résolution", "Resolution")],
    ["size", L("Taille", "Size")],
    ["videoCodec", L("Codec vidéo", "Video codec")],
    ["audioCodec", L("Codec audio", "Audio codec")],
    ["source", "Source"],
    ["protocol", L("Protocole", "Protocol")],
    ["languages", L("Langues", "Languages")],
    ["cached", L("Cache annoncé", "Reported cache")],
    ["state", L("Référence", "Reference")],
    ["published", L("Publication", "Published")],
    ["firstSeen", L("Première indexation", "First indexed")],
    ["lastSeen", L("Dernière observation", "Last observed")],
  ];
  return (
    <section className="min-w-0 max-w-full space-y-4 py-4">
      <h3 className="font-semibold">{L("Releases indexées", "Indexed releases")}</h3>
      <p className="text-sm text-fg-muted">
        {L(
          "Seules les références conformes aux critères Remix sont conservées. Les contrôles du profil de lecture et du client restent nécessaires ; un cache annoncé ne garantit pas la lecture. Les liens privés restent sur le serveur.",
          "Only references meeting Remix criteria are retained. Playback profile and client checks still apply; reported cache does not guarantee playback. Private links stay on the server.",
        )}
      </p>
      {state.data && (
        <p>
          {state.data.enabled
            ? L("Index disponible", "Index available")
            : L("Index indisponible", "Index unavailable")}{" "}
          · {rows.length} releases
        </p>
      )}
      {seasons.length > 0 && (
        <label>
          {L("Saison", "Season")}{" "}
          <select
            aria-label={L("Saison", "Season")}
            value={season ?? ""}
            onChange={(e) => {
              setSeason(e.target.value === "" ? null : Number(e.target.value));
              setEpisode("");
            }}
          >
            <option value="">{L("Premier épisode (S01E01)", "First episode (S01E01)")}</option>
            {seasons.map((s) => (
              <option key={s.season} value={s.season}>
                {s.title || `S${s.season}`}
              </option>
            ))}
          </select>
        </label>
      )}
      {episodeState.loading && (
        <p role="status">{L("Chargement des épisodes…", "Loading episodes…")}</p>
      )}
      {Boolean(episodeState.error) && (
        <p role="alert">
          {L("Impossible de charger les épisodes.", "Could not load episodes.")}{" "}
          <Button onClick={() => void episodeState.reload()}>{L("Réessayer", "Retry")}</Button>
        </p>
      )}
      {episodes.length > 0 && (
        <label className="block">
          {L("Épisode", "Episode")}{" "}
          <select
            aria-label={L("Épisode", "Episode")}
            value={episode}
            onChange={(e) => setEpisode(e.target.value)}
          >
            <option value="">
              {L(
                "Premier épisode (indexation initiale S01E01)",
                "First episode (initial S01E01 indexing)",
              )}
            </option>
            {episodes.map((e) => (
              <option key={e.key} value={e.key}>
                S{String(e.season).padStart(2, "0")}E{String(e.episode).padStart(2, "0")}
              </option>
            ))}
          </select>
        </label>
      )}
      <div className="flex flex-wrap gap-3">
        <input
          className="min-w-0 flex-1 rounded border p-2"
          aria-label={L("Filtrer les releases", "Filter releases")}
          placeholder={L("Nom, source, codec, langue…", "Name, source, codec, language…")}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <Button onClick={() => void state.reload()}>
          {L("Actualiser l’affichage", "Refresh display")}
        </Button>
      </div>
      {Boolean(state.error) && (
        <p role="alert">{L("Impossible de charger les releases.", "Could not load releases.")}</p>
      )}
      {state.loading ? (
        <p role="status">{L("Chargement…", "Loading…")}</p>
      ) : !rows.length ? (
        <p>
          {L(
            "Aucune release indexée pour ce contenu. L’indexation ou une recherche de lecture alimentera cette liste.",
            "No indexed release for this item. Indexing or a playback search will populate this list.",
          )}
        </p>
      ) : (
        <div className="space-y-2">
          <label className="flex flex-wrap items-center gap-2">
            {L("Trier par", "Sort by")}
            <select
              className="rounded border p-2"
              value={sort}
              onChange={(e) => setSort(e.target.value as SortKey)}
            >
              {columns.map(([key, label]) => (
                <option key={key} value={key}>
                  {label}
                </option>
              ))}
            </select>
            <Button onClick={() => setDescending(!descending)}>
              {descending ? L("Décroissant", "Descending") : L("Croissant", "Ascending")}
            </Button>
          </label>
          {rows.slice(currentPage * 50, (currentPage + 1) * 50).map((r, i) => (
            <ReleaseCard key={`${r.name}-${r.source}-${i}`} row={r} />
          ))}
        </div>
      )}
      {rows.length > 50 && (
        <div className="flex items-center gap-3">
          <Button disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>
            {L("Précédent", "Previous")}
          </Button>
          <span>
            {currentPage + 1} / {Math.ceil(rows.length / 50)}
          </span>
          <Button
            disabled={(currentPage + 1) * 50 >= rows.length}
            onClick={() => setPage(currentPage + 1)}
          >
            {L("Suivant", "Next")}
          </Button>
        </div>
      )}
      <p className="text-sm text-fg-muted">
        {L(
          "Publication : date fournie par la source, sinon inconnue. Première indexation et dernière observation : dates propres à Remix. Le tri du tableau ne change pas vos critères de lecture.",
          "Publication: source-provided date, otherwise unknown. First indexed and last observed: Remix dates. Table sorting does not change playback preferences.",
        )}
      </p>
    </section>
  );
}

export function ReleaseCard({ row: r, title }: { row: ReleaseRow; title?: string }) {
  const details = [
    [L("Codec vidéo", "Video codec"), r.videoCodec],
    [L("Codec audio", "Audio codec"), r.audioCodec],
    [
      L("Langues", "Languages"),
      (r.languages ?? []).join(", ") || L("Non indiquées", "Not reported"),
    ],
    [L("Cache annoncé", "Reported cache"), (r.cached ?? []).join(", ")],
    [L("Publication", "Published"), date(r.published)],
    [L("Première indexation", "First indexed"), date(r.firstSeen)],
    [L("Dernière observation", "Last observed"), date(r.lastSeen)],
  ];
  return (
    <details className="min-w-0 max-w-full rounded-lg border border-border p-3 text-sm">
      <summary className="cursor-pointer break-words [overflow-wrap:anywhere]">
        <span className="font-medium">{r.name || L("Release sans nom", "Unnamed release")}</span>
        {title && <span className="block text-fg-muted">{title}</span>}
        <span className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-fg-muted">
          <span>{r.source}</span>
          <span>{r.height ? `${r.height}p` : "—"}</span>
          <span>
            {r.size
              ? `${(r.size / 1024 ** 3).toLocaleString(getLocale(), { maximumFractionDigits: 2 })} GiB`
              : "—"}
          </span>
          <span>{r.protocol}</span>
          <span>{stateLabel(r.state)}</span>
        </span>
      </summary>
      <dl className="mt-3 grid min-w-0 gap-3 border-t border-border pt-3 sm:grid-cols-2 lg:grid-cols-3">
        {details.map(([label, value]) => (
          <div key={label} className="min-w-0 [overflow-wrap:anywhere]">
            <dt className="text-fg-muted">{label}</dt>
            <dd>{value || "—"}</dd>
          </div>
        ))}
      </dl>
    </details>
  );
}
