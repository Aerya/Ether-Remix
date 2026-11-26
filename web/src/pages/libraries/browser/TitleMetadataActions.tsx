import { Badge, Button, Input, Popover, Select, toast } from "@braedonsaunders/appkit-ui";
import { ArrowUpRight, Ellipsis, ListOrdered, RefreshCw, Search } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { Artwork } from "../../../components/Artwork";
import { Drawer } from "../../../components/Drawer";
import { t } from "../../../i18n";
import { apiFetch } from "../../../lib/api";
import { useAsync } from "../../../lib/hooks";
import { metadataSources, supportsMetadata } from "../../../lib/metadataSources";
import type { TitleCard, TitleDetail } from "../../../lib/contract";

type MatchCandidate = TitleCard & { source: string; providerId: string };

export function TitleMetadataActions({
  title,
  onRefresh,
  onEpisodeOrder,
  onMatch,
}: {
  title: TitleDetail;
  onRefresh: () => Promise<unknown>;
  onEpisodeOrder?: () => void;
  onMatch: (key: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const path = `/catalog/title/${encodeURIComponent(title.key)}`;
  async function refresh() {
    setRefreshing(true);
    setMenuOpen(false);
    try {
      await apiFetch(path + "/refresh", { method: "POST" });
      await onRefresh();
      toast.success(t("Metadata refreshed"));
    } catch {
      toast.error(t("Could not refresh metadata"));
    } finally {
      setRefreshing(false);
    }
  }
  return (
    <>
      <Popover
        open={menuOpen}
        onOpenChange={setMenuOpen}
        align="end"
        trigger={
          <Button
            variant="ghost"
            size="sm"
            aria-expanded={menuOpen}
            aria-busy={refreshing}
            aria-label={refreshing ? t("Refreshing metadata") : t("Metadata")}
            disabled={refreshing}
            onClick={() => setMenuOpen(!menuOpen)}
          >
            {refreshing ? <RefreshCw size={16} className="animate-spin" /> : <Ellipsis size={16} />}
          </Button>
        }
      >
        <div className="grid min-w-44 gap-0.5 p-1">
          <Button
            variant="ghost"
            size="sm"
            className="justify-start"
            onClick={() => void refresh()}
          >
            <RefreshCw size={15} />
            {t("Refresh metadata")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="justify-start"
            onClick={() => {
              setMenuOpen(false);
              setOpen(true);
            }}
          >
            <Search size={15} />
            {t("Fix match")}
          </Button>
          {onEpisodeOrder ? (
            <Button
              variant="ghost"
              size="sm"
              className="justify-start"
              onClick={() => {
                setMenuOpen(false);
                onEpisodeOrder();
              }}
            >
              <ListOrdered size={15} />
              {t("Episode order")}
            </Button>
          ) : null}
        </div>
      </Popover>
      {open ? (
        <FixMatchDrawer
          title={title}
          path={path}
          onClose={() => setOpen(false)}
          onMatch={onMatch}
        />
      ) : null}
    </>
  );
}

function FixMatchDrawer({
  title,
  path,
  onClose,
  onMatch,
}: {
  title: TitleDetail;
  path: string;
  onClose: () => void;
  onMatch: (key: string) => void;
}) {
  const [query, setQuery] = useState(title.title);
  const [year, setYear] = useState(title.year ? String(title.year) : "");
  const [results, setResults] = useState<MatchCandidate[] | null>(null);
  const [selected, setSelected] = useState<MatchCandidate>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<"search" | "apply" | null>(null);
  const radioGroup = useId();
  const registry = useAsync(metadataSources, []);
  const [source, setSource] = useState("");
  const available =
    registry.data?.filter((entry) => entry.configured && supportsMetadata(entry, title.type)) ?? [];
  const activeSource = available.find((entry) => entry.id === source)?.id ?? available[0]?.id ?? "";
  const candidates = results?.filter((item) => item.key !== title.key) ?? [];

  async function search(event: FormEvent) {
    event.preventDefault();
    if (busy || !query.trim() || !activeSource) return;
    setBusy(true);
    setError(null);
    setSelected(undefined);
    setResults(null);
    try {
      const params = new URLSearchParams({ q: query.trim(), year, source: activeSource });
      setResults(await apiFetch(`${path}/matches?${params}`));
    } catch {
      setError("search");
    } finally {
      setBusy(false);
    }
  }

  async function apply() {
    if (!selected || busy) return;
    setBusy(true);
    setError(null);
    try {
      const next = await apiFetch<{ key: string }>(path + "/match", {
        method: "PUT",
        body: JSON.stringify({ source: selected.source, providerId: selected.providerId }),
      });
      onClose();
      onMatch(next.key);
    } catch {
      setError("apply");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer
      open
      onClose={busy ? () => {} : onClose}
      title={t("Fix match")}
      size="md"
      footer={
        <div className="grid w-full justify-items-end gap-2">
          {selected ? (
            <p aria-live="polite" className="w-full text-sm text-fg-muted">
              {t("Watch history, favourites, collections and saved releases move to {title}.", {
                title: selected.year ? `${selected.title} (${selected.year})` : selected.title,
              })}
            </p>
          ) : null}
          <Button disabled={busy || !selected} onClick={() => void apply()}>
            {t("Apply match")}
          </Button>
        </div>
      }
    >
      <div className="space-y-4">
        <form className="space-y-3" onSubmit={(event) => void search(event)}>
          <div className="grid grid-cols-[minmax(0,1fr)_6rem] gap-3">
            <label className="grid gap-1 text-sm">
              {t("Title")}
              <Input
                className="min-w-0"
                value={query}
                disabled={busy}
                maxLength={300}
                onChange={(event) => setQuery(event.target.value)}
                required
              />
            </label>
            <label className="grid gap-1 text-sm">
              {t("Year")}
              <Input
                className="min-w-0"
                value={year}
                disabled={busy}
                type="number"
                min={1800}
                max={2200}
                onChange={(event) => setYear(event.target.value)}
              />
            </label>
          </div>
          <label className="grid gap-1 text-sm">
            {t("Metadata source")}
            <Select
              aria-label={t("Metadata source")}
              sheetTitle={t("Metadata source")}
              searchable={false}
              value={activeSource}
              disabled={busy || available.length === 0}
              onChange={(event) => {
                setSource(event.currentTarget.value);
                setResults(null);
                setSelected(undefined);
                setError(null);
              }}
            >
              {available.length === 0 ? (
                <option value="">
                  {registry.error
                    ? t("Could not load metadata sources")
                    : registry.loading
                      ? t("Loading…")
                      : t("No source configured")}
                </option>
              ) : null}
              {available.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.name}
                </option>
              ))}
            </Select>
          </label>
          <Button type="submit" variant="outline" disabled={busy || !query.trim() || !activeSource}>
            <Search size={15} />
            {t("Search")}
          </Button>
        </form>
        {error ? (
          <p role="alert" className="text-sm text-fg-muted">
            {error === "search" ? t("Could not find matches") : t("Could not apply match")}
          </p>
        ) : null}
        {results && candidates.length === 0 ? <p role="status">{t("No matches found")}</p> : null}
        <fieldset aria-label={t("Fix match")} className="space-y-2" disabled={busy}>
          <MatchCard
            title={
              title.tmdbId ? { ...title, source: "tmdb", providerId: String(title.tmdbId) } : title
            }
            group={radioGroup}
            checked={!selected}
            current
            onSelect={() => setSelected(undefined)}
          />
          {candidates.map((match) => (
            <MatchCard
              key={match.providerId}
              title={match}
              group={radioGroup}
              checked={selected?.providerId === match.providerId}
              onSelect={() => setSelected(match)}
            />
          ))}
        </fieldset>
      </div>
    </Drawer>
  );
}

function MatchCard({
  title,
  group,
  checked,
  current,
  onSelect,
}: {
  title: TitleCard & Partial<Pick<MatchCandidate, "source" | "providerId">>;
  group: string;
  checked: boolean;
  current?: boolean;
  onSelect: () => void;
}) {
  const isShow = title.type === "show";
  return (
    <div className="relative rounded-lg border border-border bg-surface has-checked:border-primary has-checked:bg-bg-subtle">
      <label className="flex cursor-pointer items-center gap-3 rounded-lg p-3 has-focus-visible:ring-2 has-focus-visible:ring-ring">
        <input
          type="radio"
          name={group}
          checked={checked}
          onChange={onSelect}
          className="peer sr-only"
        />
        <span
          aria-hidden="true"
          className="grid size-4 shrink-0 place-items-center rounded-full border border-fg-muted peer-checked:border-primary peer-checked:after:size-2 peer-checked:after:rounded-full peer-checked:after:bg-primary"
        />
        <Artwork src={title.poster} alt="" className="aspect-[2/3] w-10 shrink-0 rounded" />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1 pr-12">
            <span className="font-medium">{title.title}</span>
            {current ? <Badge variant="secondary">{t("Current")}</Badge> : null}
          </span>
          <span className="block truncate text-xs text-fg-muted">
            {[isShow ? t("TV show") : t("Movie"), title.year].filter(Boolean).join(" · ")}
          </span>
          <span className="block truncate text-sm text-fg-muted">
            {title.summary || t("No overview is available for this title yet.")}
          </span>
        </span>
      </label>
      {title.source === "tmdb" && title.providerId ? (
        <a
          href={`https://www.themoviedb.org/${isShow ? "tv" : "movie"}/${encodeURIComponent(title.providerId)}`}
          target="_blank"
          rel="noreferrer"
          className="absolute right-3 top-3 inline-flex items-center gap-0.5 rounded text-xs text-fg-muted hover:text-fg hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        >
          {t("TMDB")}
          <ArrowUpRight size={12} aria-hidden="true" />
        </a>
      ) : null}
    </div>
  );
}
