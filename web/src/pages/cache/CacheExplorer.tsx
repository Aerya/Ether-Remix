import {
  Badge,
  Button,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  cn,
} from "@braedonsaunders/appkit-ui";
import { ChevronLeft, ChevronRight, Film, Pin, Search } from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ToolbarPicker } from "../../components/ToolbarPicker";
import { getLocale, localized, t, tn } from "../../i18n";
import { formatBytes, formatPercent } from "../../lib/format";
import { useMediaQuery } from "../../lib/hooks";
import type { CacheSnapshot } from "./Cache";
import { CacheReleaseDrawer } from "./CacheReleaseDrawer";
import { causeDescription, causeLabel, pinLabel, statusPills } from "./reasons";

const FILTERS = localized(
  () =>
    [
      ["all", t("All releases")],
      ["watching", t("Playing now")],
      ["paused", t("Paused")],
      ["unfinished", t("Unfinished")],
      ["shelf", t("Shelf")],
      ["prefetched", t("Prefetched")],
      ["checked", t("Checked")],
      ["owner", t("Kept by you")],
      ["unexplained", t("Unexplained")],
      ["reclaimable", t("Reclaimable")],
    ] as const,
);

const PREFETCHED = [
  "next-episode",
  "post-play",
  "resume-warm",
  "detail-warm",
  "trailer-warm",
  "browse-warm",
];

function matchesFilter(r: CacheSnapshot["releases"][number], filter: string): boolean {
  const codes = new Set((r.reason?.causes ?? []).map((c) => c.code));
  const pins = new Set(r.Pins ?? []);
  switch (filter) {
    case "watching":
      return codes.has("playing") || pins.has("playback:live");
    case "paused":
      return codes.has("paused");
    case "unfinished":
      return codes.has("unfinished") || pins.has("playback:inprogress");
    case "shelf":
      return codes.has("shelf") || pins.has("preloaded");
    case "prefetched":
      return PREFETCHED.some((code) => codes.has(code));
    case "checked":
      return codes.has("quality-check");
    case "owner":
      return pins.has("owner");
    case "unexplained":
      return codes.size === 0 || codes.has("unrecorded");
    case "reclaimable":
      return (
        pins.size === 0 &&
        !codes.has("playing") &&
        !codes.has("paused") &&
        r.reason?.retention.kind !== "pinned"
      );
    default:
      return true;
  }
}

function dateLabel(value: string): string {
  return value && !value.startsWith("0001")
    ? new Date(value).toLocaleDateString(getLocale(), { month: "short", day: "numeric" })
    : "—";
}

// A release id that reached the title or file field is a hash, not something a person names.
export function isOpaqueId(value: string): boolean {
  return /^(?:[a-z0-9_-]+:)?[0-9a-f]{24,}$/i.test(value.trim().replace(/\.[a-z0-9]{2,5}$/i, ""));
}

export function friendlyName(r: CacheSnapshot["releases"][number]): string {
  const title = r.Title?.trim();
  if (title && !isOpaqueId(title)) return title;
  const file = r.reason?.file?.trim();
  if (file && !isOpaqueId(file)) return file;
  const source = r.reason?.source?.trim();
  if (source && !isOpaqueId(source)) return source;
  const codes = new Set((r.reason?.causes ?? []).map((c) => c.code));
  if (codes.has("quality-check") || codes.has("repair")) return t("Cached check data");
  if (codes.has("detail-warm")) return t("Detail-page cache");
  return t("Cached release data");
}

export function friendlySubtitle(r: CacheSnapshot["releases"][number]): string {
  const primary = friendlyName(r);
  const file = r.reason?.file?.trim();
  if (file && !isOpaqueId(file) && file !== primary) return file;
  const source = r.reason?.source?.trim();
  if (source && !isOpaqueId(source) && source !== primary) return source;
  return r.reason?.ratingKey ? t("Cached stream") : t("Unmatched release");
}

export function CacheExplorer({
  snapshot,
  policyName = "",
  busy,
  onPin,
  onClear,
}: {
  snapshot?: CacheSnapshot;
  policyName?: string;
  busy: boolean;
  onPin: (id: string, pinned: boolean) => void;
  onClear: (id: string, title: string, block: boolean) => Promise<boolean> | void;
}) {
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const [page, setPage] = useState(0);
  const [params, setParams] = useSearchParams();
  const compact = useMediaQuery("(max-width: 639px)");
  const selectedId = params.get("release");

  const openRelease = (id: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (id) next.set("release", id);
        else next.delete("release");
        return next;
      },
      { preventScrollReset: true },
    );

  const allReleases = snapshot?.releases ?? [];
  const selectedRelease = selectedId
    ? allReleases.find((r) => r.ReleaseID === selectedId)
    : undefined;

  const rows = allReleases.filter(
    (r) =>
      matchesFilter(r, filter) &&
      `${r.Title} ${r.ReleaseID} ${r.reason?.file ?? ""} ${r.reason?.source ?? ""}`
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  const pages = Math.max(1, Math.ceil(rows.length / 20)),
    current = Math.min(page, pages - 1);
  const pageRows = rows.slice(current * 20, (current + 1) * 20);

  return (
    <>
      <section
        aria-label={t("Cache explorer")}
        className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-border bg-surface"
      >
        <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border p-4">
          <div className="relative min-w-48 flex-1">
            <Search
              size={14}
              className="pointer-events-none absolute left-3 top-3 text-fg-subtle"
            />
            <Input
              aria-label={t("Search cached releases")}
              placeholder={t("Search titles, files, or release IDs…")}
              className="pl-9"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setPage(0);
              }}
            />
          </div>
          <ToolbarPicker
            ariaLabel={t("Filter cached releases")}
            value={filter}
            onChange={(next) => {
              setFilter(next);
              setPage(0);
            }}
            options={FILTERS.map(([value, label]) => ({ value, label }))}
            searchable={false}
            className="h-10 px-3"
            menuClassName="w-52"
          />
          <span className="text-xs text-fg-muted">
            {tn(rows.length, "{count} release", "{count} releases")}
          </span>
        </div>
        <div className="app-scroll min-h-0 flex-1 overflow-auto">
          {compact ? (
            pageRows.length ? (
              <div role="list" className="divide-y divide-border">
                {pageRows.map((r) => {
                  const selected = selectedId === r.ReleaseID;
                  const name = friendlyName(r);
                  const subtitle = friendlySubtitle(r);
                  const causes = r.reason?.causes ?? [];
                  const primaryCode = causes[0]?.code ?? "unrecorded";
                  const pills = statusPills(primaryCode, r.Pins);
                  return (
                    <div
                      key={r.ReleaseID}
                      role="listitem"
                      tabIndex={0}
                      aria-selected={selected || undefined}
                      className={cn(
                        "cursor-pointer space-y-2.5 p-4 transition-colors hover:bg-bg-subtle/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                        selected && "bg-primary/5",
                      )}
                      onClick={() => openRelease(r.ReleaseID)}
                      onKeyDown={(e) => {
                        if (e.target !== e.currentTarget) return;
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          openRelease(r.ReleaseID);
                        }
                      }}
                    >
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium text-fg" title={name}>
                          {name}
                        </p>
                        <p className="mt-0.5 truncate text-xs text-fg-subtle" title={subtitle}>
                          {subtitle}
                        </p>
                      </div>
                      <div className="space-y-1.5">
                        <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                          <span className="font-medium tabular-nums text-fg">
                            {formatBytes(r.Bytes)}
                            {r.Size > 0
                              ? ` · ${t("{percent} retained", {
                                  percent: formatPercent(
                                    Math.min(100, Math.round((r.Bytes / r.Size) * 100)) / 100,
                                  ),
                                })}`
                              : ""}
                          </span>
                          <span className="text-fg-muted">{dateLabel(r.LastUsed)}</span>
                        </div>
                        {r.Size > 0 && (
                          <div className="h-1 overflow-hidden rounded-full bg-primary/10">
                            <div
                              className="h-full bg-primary/60"
                              style={{ width: `${Math.min(100, (r.Bytes / r.Size) * 100)}%` }}
                            />
                          </div>
                        )}
                      </div>
                      <div className="flex flex-wrap items-center justify-between gap-2 pt-0.5">
                        <div className="flex flex-wrap gap-1">
                          {pills.map((pill) => (
                            <Badge key={pill.label} variant={pill.active ? "default" : "secondary"}>
                              {pill.label}
                            </Badge>
                          ))}
                        </div>
                        <div
                          className="ml-auto flex items-center gap-1 whitespace-nowrap"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <Button
                            variant="ghost"
                            size="sm"
                            aria-label={
                              r.Pins?.includes("owner")
                                ? t("Unpin {title}", { title: name })
                                : t("Pin {title}", { title: name })
                            }
                            disabled={busy}
                            onClick={() => onPin(r.ReleaseID, !r.Pins?.includes("owner"))}
                          >
                            <Pin
                              size={14}
                              className={
                                r.Pins?.includes("owner") ? "fill-primary text-primary" : ""
                              }
                            />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={busy}
                            onClick={() => void onClear(r.ReleaseID, name, false)}
                          >
                            {t("Clear")}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={busy}
                            onClick={() => void onClear(r.ReleaseID, name, true)}
                          >
                            {t("Clear & block")}
                          </Button>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            ) : null
          ) : (
            <Table
              containerClassName="rounded-none border-0 shadow-none"
              className="w-full table-fixed"
            >
              <TableHeader>
                <TableRow>
                  <TableHead>{t("Title / release")}</TableHead>
                  <TableHead className="w-32 lg:w-36">{t("Retained")}</TableHead>
                  <TableHead className="hidden md:table-cell md:w-[24%]">{t("Reason")}</TableHead>
                  <TableHead className="hidden lg:table-cell lg:w-24">{t("Last used")}</TableHead>
                  <TableHead className="w-56">
                    <span className="sr-only">{t("Owner pin")}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {pageRows.map((r) => {
                  const selected = selectedId === r.ReleaseID;
                  const name = friendlyName(r);
                  const subtitle = friendlySubtitle(r);
                  const causes = r.reason?.causes ?? [];
                  const primaryCode = causes[0]?.code ?? "unrecorded";
                  const extra = Math.max(0, causes.length - 1);
                  const pinLabels = Array.from(new Set((r.Pins ?? []).map(pinLabel)));
                  return (
                    <TableRow
                      key={r.ReleaseID}
                      tabIndex={0}
                      aria-selected={selected || undefined}
                      className={cn(
                        "cursor-pointer transition-colors hover:bg-bg-subtle/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                        selected && "bg-primary/5",
                      )}
                      onClick={() => openRelease(r.ReleaseID)}
                      onKeyDown={(e) => {
                        if (e.target !== e.currentTarget) return;
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          openRelease(r.ReleaseID);
                        }
                      }}
                    >
                      <TableCell className="max-w-0">
                        <div className="flex items-center gap-3">
                          <div className="hidden shrink-0 rounded-lg bg-primary/10 p-2 text-primary sm:block">
                            <Film size={16} />
                          </div>
                          <div className="min-w-0 flex-1">
                            <p className="truncate text-sm font-medium" title={name}>
                              {name}
                            </p>
                            <p
                              className="mt-0.5 truncate text-[10px] text-fg-subtle"
                              title={subtitle}
                            >
                              {subtitle}
                            </p>
                            {pinLabels.length > 0 && (
                              <div className="mt-1 flex flex-wrap gap-1">
                                {pinLabels.map((label) => (
                                  <Badge key={label} variant="secondary">
                                    {label}
                                  </Badge>
                                ))}
                              </div>
                            )}
                          </div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <p className="text-xs font-medium tabular-nums">{formatBytes(r.Bytes)}</p>
                        {r.Size > 0 && (
                          <>
                            <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-primary/10">
                              <div
                                className="h-full bg-primary/60"
                                style={{ width: `${Math.min(100, (r.Bytes / r.Size) * 100)}%` }}
                              />
                            </div>
                            <p className="mt-1 text-[10px] text-fg-subtle">
                              {t("{percent} retained", {
                                percent: formatPercent(
                                  Math.min(100, Math.round((r.Bytes / r.Size) * 100)) / 100,
                                ),
                              })}
                            </p>
                          </>
                        )}
                      </TableCell>
                      <TableCell className="hidden text-xs md:table-cell">
                        <p
                          className="truncate font-medium text-fg"
                          title={causes
                            .map((c) => `${causeLabel(c.code)}: ${causeDescription(c.code)}`)
                            .join("\n")}
                        >
                          {causeLabel(primaryCode)}
                          {extra > 0 ? ` +${extra}` : ""}
                        </p>
                        <p className="mt-0.5 line-clamp-2 text-[10px] text-fg-subtle">
                          {causeDescription(primaryCode)}
                        </p>
                      </TableCell>
                      <TableCell className="hidden text-xs text-fg-muted lg:table-cell">
                        {dateLabel(r.LastUsed)}
                      </TableCell>
                      <TableCell className="w-56">
                        <div
                          className="flex items-center justify-end gap-1 whitespace-nowrap"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <Button
                            variant="ghost"
                            size="sm"
                            aria-label={
                              r.Pins?.includes("owner")
                                ? t("Unpin {title}", { title: name })
                                : t("Pin {title}", { title: name })
                            }
                            disabled={busy}
                            onClick={() => onPin(r.ReleaseID, !r.Pins?.includes("owner"))}
                          >
                            <Pin
                              size={14}
                              className={
                                r.Pins?.includes("owner") ? "fill-primary text-primary" : ""
                              }
                            />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={busy}
                            onClick={() => void onClear(r.ReleaseID, name, false)}
                          >
                            {t("Clear")}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={busy}
                            onClick={() => void onClear(r.ReleaseID, name, true)}
                          >
                            {t("Clear & block")}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
          {!rows.length && (
            <div className="px-5 py-16 text-center">
              <Film size={24} className="mx-auto mb-3 text-fg-subtle" />
              <p className="text-sm font-medium">
                {!snapshot
                  ? t("Loading cache…")
                  : search || filter !== "all"
                    ? t("No matching releases")
                    : t("No retained media yet")}
              </p>
              <p className="mt-2 text-xs text-fg-muted">
                {search || filter !== "all"
                  ? t("Try a different search or filter.")
                  : t("Cached movies and episodes appear here as viewers watch.")}
              </p>
            </div>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-border px-4 py-2.5">
          <p className="text-[11px] text-fg-muted">
            {rows.length
              ? t("{start}–{end} of {total}", {
                  start: current * 20 + 1,
                  end: Math.min((current + 1) * 20, rows.length),
                  total: rows.length,
                })
              : tn(0, "{count} release", "{count} releases")}{" "}
            · {t("Pins protect data from eviction")}
          </p>
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("Previous cache page")}
              disabled={current === 0}
              onClick={() => setPage(current - 1)}
            >
              <ChevronLeft size={15} />
            </Button>
            <span className="px-1 text-xs">
              {current + 1} / {pages}
            </span>
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("Next cache page")}
              disabled={current + 1 >= pages}
              onClick={() => setPage(current + 1)}
            >
              <ChevronRight size={15} />
            </Button>
          </div>
        </div>
      </section>
      <CacheReleaseDrawer
        releaseId={selectedId}
        release={selectedRelease}
        friendlyTitle={selectedRelease ? friendlyName(selectedRelease) : undefined}
        friendlySubtitle={selectedRelease ? friendlySubtitle(selectedRelease) : undefined}
        snapshot={snapshot}
        policyName={policyName}
        busy={busy}
        onClose={() => openRelease(null)}
        onPin={onPin}
        onClear={onClear}
      />
    </>
  );
}
