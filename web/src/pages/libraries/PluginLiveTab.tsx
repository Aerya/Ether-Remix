import { Badge, Button, EmptyState, cn } from "@braedonsaunders/appkit-ui";
import { Play, Radio, RefreshCw, Search, Users } from "lucide-react";
import { useState } from "react";
import { Artwork } from "../../components/Artwork";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { t, tn } from "../../i18n";
import { formatNumber } from "../../lib/format";
import { useAsync, useDebouncedValue } from "../../lib/hooks";
import { librariesApi, type Library, type LibraryLiveItem, type LibraryTab } from "./api";
import { PluginLivePreview } from "./PluginLivePreview";

export function PluginLiveTab({ library, tab }: { library: Library; tab: LibraryTab }) {
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebouncedValue(query, 300);
  const [filter, setFilter] = useState("");
  const [extraPages, setExtraPages] = useState<{
    key: string;
    items: LibraryLiveItem[];
    cursor: string;
  }>({ key: "", items: [], cursor: "" });
  const [loadingMore, setLoadingMore] = useState(false);
  const [playing, setPlaying] = useState<LibraryLiveItem | null>(null);

  const requestKey = JSON.stringify([library.id, tab.key, debouncedQuery, filter]);
  const page = useAsync(
    (signal) => {
      const p: Record<string, string> = { tab: tab.key, limit: "48" };
      if (debouncedQuery.trim()) p.query = debouncedQuery.trim();
      if (filter) p.filter = filter;
      return librariesApi.live(library.id, p, signal);
    },
    [requestKey],
  );

  const items: LibraryLiveItem[] = [
    ...(page.data?.items ?? []),
    ...(extraPages.key === requestKey ? extraPages.items : []),
  ];
  const nextCursor =
    extraPages.key === requestKey && extraPages.cursor
      ? extraPages.cursor
      : (page.data?.nextCursor ?? "");
  const filters = page.data?.filters ?? [];
  const cardShape = tab.card ?? "landscape";

  const loadMore = async () => {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const p: Record<string, string> = { tab: tab.key, limit: "48", cursor: nextCursor };
      if (debouncedQuery.trim()) p.query = debouncedQuery.trim();
      if (filter) p.filter = filter;
      const more = await librariesApi.live(library.id, p);
      setExtraPages((prev) => ({
        key: requestKey,
        items: [...(prev.key === requestKey ? prev.items : []), ...more.items],
        cursor: more.nextCursor ?? "",
      }));
    } finally {
      setLoadingMore(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="plugin-browse-bar">
        {tab.search !== false ? (
          <label className="plugin-search">
            <Search size={14} aria-hidden />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={
                tab.searchPlaceholder || t("Search {name}", { name: tab.label || library.name })
              }
              aria-label={
                tab.searchPlaceholder || t("Search {name}", { name: tab.label || library.name })
              }
            />
          </label>
        ) : null}
        {filters.length > 0 ? (
          <div
            className="flex flex-wrap items-center gap-1.5"
            role="group"
            aria-label={t("Filter streams")}
          >
            <button
              type="button"
              onClick={() => setFilter("")}
              className={cn(
                "rounded-full border px-3 py-1 text-xs font-medium transition-colors",
                filter === ""
                  ? "border-primary bg-primary-subtle text-primary"
                  : "border-border bg-surface text-fg-muted hover:text-fg",
              )}
            >
              {t("All")}
            </button>
            {filters.map((f) => (
              <button
                key={f.key}
                type="button"
                onClick={() => setFilter(filter === f.key ? "" : f.key)}
                className={cn(
                  "rounded-full border px-3 py-1 text-xs font-medium transition-colors",
                  filter === f.key
                    ? "border-primary bg-primary-subtle text-primary"
                    : "border-border bg-surface text-fg-muted hover:text-fg",
                )}
              >
                {f.label}
              </button>
            ))}
          </div>
        ) : null}
        <span className="flex-1" />
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={page.loading}
          onClick={() => {
            setExtraPages({ key: "", items: [], cursor: "" });
            void page.reload();
          }}
        >
          <RefreshCw size={13} className={page.loading ? "animate-spin" : undefined} aria-hidden />
          {t("Refresh")}
        </Button>
      </div>

      <p role="status" className="text-sm text-fg-muted">
        {page.loading && !page.data
          ? t("Loading live streams…")
          : page.data
            ? tn(items.length, "{count} live stream", "{count} live streams", {
                count: formatNumber(items.length, 0),
              })
            : ""}
      </p>

      {page.error ? <ErrorPanel error={page.error} onRetry={() => void page.reload()} /> : null}
      {page.loading && !page.data ? <LoadingRows /> : null}
      {!page.loading && page.data && items.length === 0 ? (
        <EmptyState
          icon={<Radio size={22} aria-hidden />}
          title={
            debouncedQuery || filter ? t("No matching live streams") : t("Nothing live right now")
          }
          description={
            debouncedQuery || filter
              ? t("Try clearing your search or filter.")
              : t("Live streams reported by this plugin appear here.")
          }
        />
      ) : (
        <ul
          className={cn(
            "plugin-live-grid",
            cardShape === "poster" && "plugin-live-grid-poster",
            cardShape === "square" && "plugin-live-grid-square",
          )}
        >
          {items.map((item) => (
            <li key={item.id}>
              <button
                type="button"
                className={cn(
                  "plugin-live-card group",
                  playing?.id === item.id && "border-primary ring-1 ring-primary/40",
                )}
                onClick={() => setPlaying(item)}
              >
                <div
                  className={cn(
                    "plugin-live-thumb",
                    cardShape === "poster"
                      ? "aspect-[2/3]"
                      : cardShape === "square"
                        ? "aspect-square"
                        : "aspect-video",
                  )}
                >
                  {item.thumb ? (
                    <Artwork src={item.thumb} alt="" className="size-full object-cover" />
                  ) : (
                    <span className="grid size-full place-items-center bg-surface-hover text-fg-muted">
                      <Radio size={24} aria-hidden />
                    </span>
                  )}
                  <div className="pointer-events-none absolute inset-x-2 top-2 flex items-center justify-between gap-1.5">
                    {item.live ? (
                      <span className="inline-flex items-center gap-1 rounded bg-danger px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-white shadow">
                        <span className="size-1.5 rounded-full bg-white" /> {t("Live")}
                      </span>
                    ) : (
                      <span />
                    )}
                    {item.badge ? (
                      <span className="rounded bg-black/75 px-1.5 py-0.5 text-[10px] font-medium text-white">
                        {item.badge}
                      </span>
                    ) : null}
                  </div>
                  {typeof item.viewers === "number" ? (
                    <span className="pointer-events-none absolute bottom-2 left-2 inline-flex items-center gap-1 rounded bg-black/75 px-1.5 py-0.5 text-[11px] font-medium tabular-nums text-white">
                      <Users size={11} aria-hidden />
                      {formatNumber(item.viewers, 0)}
                    </span>
                  ) : null}
                  <span className="pointer-events-none absolute inset-0 grid place-items-center bg-black/0 opacity-0 transition-all group-hover:bg-black/35 group-hover:opacity-100">
                    <span className="grid size-10 place-items-center rounded-full bg-black/75 text-white shadow">
                      <Play size={18} className="translate-x-0.5" aria-hidden />
                    </span>
                  </span>
                </div>
                <div className="flex flex-col gap-1 p-2.5">
                  <span className="line-clamp-2 text-sm font-medium leading-snug text-fg">
                    {item.title}
                  </span>
                  {item.subtitle ? (
                    <span className="truncate text-xs text-fg-muted">{item.subtitle}</span>
                  ) : null}
                  {item.tags?.length ? (
                    <div className="mt-0.5 flex flex-wrap gap-1">
                      {item.tags.slice(0, 3).map((tag) => (
                        <Badge key={tag} variant="secondary" className="text-[10px]">
                          {tag}
                        </Badge>
                      ))}
                    </div>
                  ) : null}
                </div>
              </button>
            </li>
          ))}
        </ul>
      )}

      {nextCursor ? (
        <div className="mt-4">
          <Button variant="outline" disabled={loadingMore} onClick={() => void loadMore()}>
            {t("Show more")}
          </Button>
        </div>
      ) : null}

      {playing ? (
        <PluginLivePreview
          libraryId={library.id}
          tabKey={tab.key}
          item={playing}
          onClose={() => setPlaying(null)}
        />
      ) : null}
    </div>
  );
}
