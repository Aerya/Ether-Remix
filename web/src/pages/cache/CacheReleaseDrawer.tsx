import { Badge, Button, cn } from "@braedonsaunders/appkit-ui";
import { Film, Pin, Sparkles } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Artwork } from "../../components/Artwork";
import { CopyButton } from "../../components/CopyButton";
import { Drawer } from "../../components/Drawer";
import { Kpi } from "../../components/Kpi";
import { t, tn } from "../../i18n";
import { apiFetch } from "../../lib/api";
import {
  formatBytes,
  formatDateTime,
  formatNumber,
  formatPercent,
  parseTime,
  relativeTime,
} from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { qualityLabel, rangeLabel, type ReleaseInfo } from "../../lib/releases";
import { useTitle } from "../../lib/titles";
import { searchLink } from "../releases/Pinned";
import type { CacheSnapshot, ReleaseDetail } from "./Cache";
import { isOpaqueId } from "./CacheExplorer";
import {
  causeDescription,
  causeLabel,
  pinLabel,
  retentionLabel,
  selectionReason,
  shelfStateLabel,
  statusPills,
} from "./reasons";

type CachedRelease = CacheSnapshot["releases"][number];

const SOURCE_NAMES: Record<string, string> = {
  remux: "Remux",
  bluray: "Blu-ray",
  webdl: "WEB-DL",
  webrip: "WEBRip",
  hdtv: "HDTV",
  dvd: "DVD",
  screener: "Screener",
  cam: "CAM",
};

function compactKey(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length <= 20) return trimmed;
  return `${trimmed.slice(0, 8)}…${trimmed.slice(-6)}`;
}

/** Fallback media-tag parser when the server has only a raw release or file name. */
export function inferReleaseInfo(r: CachedRelease): ReleaseInfo | undefined {
  if (r.reason?.info) return r.reason.info;
  const raw = [r.reason?.source, r.reason?.file, r.Title].find(
    (s) => s && s.trim() && !isOpaqueId(s),
  );
  if (!raw) return undefined;
  const text = ` ${raw.replace(/[._[\]()]+/g, " ")} `;
  const info: ReleaseInfo = {};
  if (/\b(2160p|4k|uhd)\b/i.test(text)) info.height = 2160;
  else if (/\b1080[pi]\b/i.test(text)) info.height = 1080;
  else if (/\b720p\b/i.test(text)) info.height = 720;
  else if (/\b(480p|576p)\b/i.test(text)) info.height = 480;

  if (/\bremux\b/i.test(text)) info.source = "remux";
  else if (/\b(blu-?ray|bdrip|brrip)\b/i.test(text)) info.source = "bluray";
  else if (/\bweb-?dl\b/i.test(text)) info.source = "webdl";
  else if (/\bwebrip\b/i.test(text)) info.source = "webrip";
  else if (/\bhdtv\b/i.test(text)) info.source = "hdtv";
  else if (/\bdvdrip\b/i.test(text)) info.source = "dvd";

  if (/\b(hevc|x265|h\.?265)\b/i.test(text)) info.videoCodec = "hevc";
  else if (/\bav1\b/i.test(text)) info.videoCodec = "av1";
  else if (/\b(x264|h\.?264|avc)\b/i.test(text)) info.videoCodec = "h264";

  if (/\b(dv|dovi|dolby\s*vision)\b/i.test(text)) info.dolbyVision = true;
  if (/\bhdr(10(\+|plus)?)?\b/i.test(text)) info.hdr10 = true;

  if (/\btruehd\b/i.test(text)) info.audioCodec = "truehd";
  else if (/\bdts-?hd\b/i.test(text)) info.audioCodec = "dtshd";
  else if (/\bdts\b/i.test(text)) info.audioCodec = "dts";
  else if (/\b(eac3|e-ac-3|ddp|dd\+)\b/i.test(text)) info.audioCodec = "eac3";
  else if (/\b(ac3|dd5)\b/i.test(text)) info.audioCodec = "ac3";
  else if (/\bflac\b/i.test(text)) info.audioCodec = "flac";
  else if (/\baac\b/i.test(text)) info.audioCodec = "aac";

  if (/\batmos\b/i.test(text)) info.atmos = true;
  const ch = raw.match(/\b(7\.1|5\.1|2\.0)\b/);
  if (ch) info.channels = ch[1];
  const grp = raw.replace(/\.(mkv|mp4|avi|ts|m4v)$/i, "").match(/-([A-Za-z0-9]{2,16})$/);
  if (grp) info.group = grp[1];

  const hasAny =
    Boolean(info.height) ||
    Boolean(info.source) ||
    Boolean(info.videoCodec) ||
    Boolean(info.audioCodec) ||
    Boolean(info.dolbyVision) ||
    Boolean(info.hdr10);
  return hasAny ? info : undefined;
}

function originLabel(origin?: string): string {
  switch (origin) {
    case "Usenet":
      return t("Usenet");
    case "HTTP":
      return t("Direct HTTP stream");
    case "Debrid":
      return t("Debrid");
    default:
      return origin || "—";
  }
}

function warmingRuleLabel(
  primaryCode: string,
  policyName: string,
  policy?: CacheSnapshot["policy"],
): string {
  let rule = "";
  switch (primaryCode) {
    case "shelf":
      rule =
        policy?.preloadSource === "popular"
          ? t("Managed preload shelf · Catalogue popularity")
          : policy?.preloadSource === "recent"
            ? t("Managed preload shelf · Newest releases")
            : t("Managed preload shelf · Smart household blend");
      break;
    case "detail-warm":
      rule = t("Detail-page warming");
      break;
    case "next-episode":
      rule = t("Next-episode prefetch");
      break;
    case "resume-warm":
    case "unfinished":
      rule = t("Continue Watching protection");
      break;
    case "post-play":
      rule = t("Post-play recommendation warming");
      break;
    case "trailer-warm":
      rule = t("Trailer warming");
      break;
    case "browse-warm":
      rule = t("Browse row warming");
      break;
    case "watchlist":
      rule = t("Watchlist arrival warming");
      break;
    case "owner-pin":
      rule = t("Manual owner pin");
      break;
    case "quality-check":
      rule = t("Release health check");
      break;
    case "failover":
      rule = t("Playback source failover");
      break;
    case "repair":
      rule = t("Usenet article repair");
      break;
    default:
      rule =
        policy?.mode === "adaptive"
          ? t("Adaptive repeat-watch retention")
          : policy?.mode === "off"
            ? t("Pass-through stream")
            : t("On-demand stream retention");
      break;
  }
  return policyName ? `${policyName} · ${rule}` : rule;
}

function earliestCachedTime(r: CachedRelease, detail?: ReleaseDetail | null): Date | null {
  const candidates = [
    parseTime(r.reason?.cachedAt),
    parseTime(detail?.firstCached),
    ...(r.reason?.causes ?? []).map((c) => parseTime(c.firstSeen)),
  ].filter((d): d is Date => d !== null);
  if (!candidates.length) return null;
  return candidates.reduce((min, d) => (d.getTime() < min.getTime() ? d : min));
}

function formatSpanRange(start: number, end: number): string {
  return start === end ? formatNumber(start) : `${formatNumber(start)}–${formatNumber(end)}`;
}

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[8.5rem_1fr] gap-3 py-2 text-sm">
      <dt className="text-fg-muted">{label}</dt>
      <dd className="min-w-0 break-words text-fg">{children}</dd>
    </div>
  );
}

function ReleasePoster({ ratingKey, title }: { ratingKey?: number; title: string }) {
  const detail = useTitle(ratingKey ? String(ratingKey) : undefined);
  return (
    <Artwork
      src={detail?.poster}
      alt={title}
      title={title}
      className="aspect-[2/3] w-16 shrink-0 rounded-lg border border-border shadow-xs"
    />
  );
}

function SegmentMap({
  release,
  detail,
}: {
  release: CachedRelease;
  detail?: ReleaseDetail | null;
}) {
  const slots = 48;
  const ratio = release.Size > 0 ? Math.min(1, release.Bytes / release.Size) : 0;
  const complete =
    (release.Size > 0 && release.Bytes >= release.Size * 0.995) ||
    Boolean(
      detail &&
      detail.estimatedTotal &&
      detail.estimatedTotal > 0 &&
      detail.segments >= detail.estimatedTotal &&
      detail.spanCount === 1,
    );

  const cells: number[] =
    detail?.coverage && detail.coverage.length > 0
      ? detail.coverage
      : Array.from({ length: slots }, (_, i) => {
          if (complete) return 1;
          if (release.Size > 0) return (i + 0.5) / slots <= ratio ? 1 : 0;
          return release.Segments > 0 && i < Math.min(slots, Math.max(2, release.Segments))
            ? 0.7
            : 0;
        });

  const spans = detail?.spans ?? [];
  const spanSummary = complete
    ? t("Complete source · all {count} segments", { count: formatNumber(release.Segments) })
    : spans.length > 0
      ? (() => {
          const shown = spans
            .slice(0, 4)
            .map((s) => formatSpanRange(s.start, s.end))
            .join(", ");
          const more = Math.max(0, (detail?.spanCount ?? spans.length) - 4);
          return more > 0
            ? t("Segments {ranges} (+{more} more)", {
                ranges: shown,
                more: formatNumber(more),
              })
            : t("Segments {ranges}", { ranges: shown });
        })()
      : tn(release.Segments, "{count} cached segment", "{count} cached segments");

  return (
    <div className="rounded-xl border border-border bg-bg-subtle/50 p-3">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="font-medium text-fg">{t("Segment map")}</span>
        <span className="text-fg-muted">
          {release.Size > 0
            ? t("{cached} of {total} ({percent})", {
                cached: formatBytes(release.Bytes),
                total: formatBytes(release.Size),
                percent: formatPercent(ratio),
              })
            : formatBytes(release.Bytes)}
        </span>
      </div>
      <div
        role="img"
        aria-label={spanSummary}
        className="mt-2 grid h-3 gap-0.5 overflow-hidden rounded bg-border/40 p-0.5"
        style={{ gridTemplateColumns: `repeat(${cells.length}, minmax(0, 1fr))` }}
      >
        {cells.map((v, idx) => (
          <span
            key={idx}
            className={cn(
              "rounded-[1px] transition-colors",
              v >= 0.75
                ? "bg-primary"
                : v >= 0.25
                  ? "bg-primary/60"
                  : v > 0
                    ? "bg-primary/35"
                    : "bg-surface/60",
            )}
          />
        ))}
      </div>
      <div className="mt-1.5 flex flex-wrap items-center justify-between gap-2 text-[11px] text-fg-subtle">
        <span>{spanSummary}</span>
        {detail && detail.spanCount > 1 && !complete ? (
          <span>
            {tn(detail.spanCount, "{count} contiguous range", "{count} contiguous ranges")}
          </span>
        ) : release.Segments > 0 && release.Bytes > 0 ? (
          <span>
            {t("~{size} / segment", {
              size: formatBytes(Math.round(release.Bytes / release.Segments)),
            })}
          </span>
        ) : null}
      </div>
    </div>
  );
}

export function CacheReleaseDrawer({
  releaseId,
  release,
  friendlyTitle,
  friendlySubtitle,
  snapshot,
  policyName,
  busy,
  onClose,
  onPin,
  onClear,
}: {
  releaseId: string | null;
  release?: CachedRelease;
  friendlyTitle?: string;
  friendlySubtitle?: string;
  snapshot?: CacheSnapshot;
  policyName: string;
  busy: boolean;
  onClose: () => void;
  onPin: (id: string, pinned: boolean) => void;
  onClear: (id: string, title: string, block: boolean) => Promise<boolean> | void;
}) {
  const detailState = useAsync(
    (signal) =>
      releaseId
        ? apiFetch<ReleaseDetail>(`/cache/releases/${encodeURIComponent(releaseId)}`, {
            signal,
          }).catch(() => null)
        : Promise.resolve(null),
    [releaseId, release?.Segments],
  );
  const detail =
    detailState.data && detailState.data.releaseId === releaseId ? detailState.data : null;

  const preloadItem = release
    ? snapshot?.preload.items?.find((it) => it.releaseId === release.ReleaseID)
    : undefined;
  const ratingKey = release?.reason?.ratingKey || preloadItem?.key || undefined;
  const preloadSelection = ratingKey
    ? snapshot?.preload.selection?.find((s) => s.key === ratingKey)
    : undefined;

  const causes = release?.reason?.causes ?? [];
  const primaryCode = causes[0]?.code ?? "unrecorded";
  const shelfCause = causes.find((c) => c.code === "shelf" && c.shelf);
  const shelfScore = preloadSelection?.score ?? shelfCause?.shelf?.score;
  const shelfReasonCodes = preloadSelection?.reasonCodes ?? shelfCause?.shelf?.reasonCodes;
  const shelfState = preloadItem?.state ?? preloadSelection?.state ?? shelfCause?.shelf?.state;

  const info = release ? inferReleaseInfo(release) : undefined;
  const range = rangeLabel(info);
  const resBadge =
    info?.height && info.height >= 2160
      ? "4K"
      : info?.height && info.height > 0
        ? `${info.height}p`
        : "";
  const sourceBadge = info?.source ? (SOURCE_NAMES[info.source] ?? info.source) : "";
  const isOwnerPinned = Boolean(release?.Pins?.includes("owner"));
  const title = friendlyTitle || t("Cached release");
  const cachedAt = release ? earliestCachedTime(release, detail) : null;
  const lastUsed = release ? parseTime(release.LastUsed) : null;
  const lastPlayed = release ? parseTime(release.LastPlayed) : null;
  const retention = release?.reason?.retention;
  const expiryUntil = parseTime(retention?.until);
  const pills = release ? statusPills(primaryCode, release.Pins) : [];

  const storageKind = snapshot?.shared
    ? snapshot.clustered
      ? t("Shared cluster pool")
      : t("NAS storage")
    : t("Local disk");

  return (
    <Drawer
      open={releaseId != null}
      onClose={onClose}
      size="md"
      title={title}
      documentTitle={title}
      description={release ? friendlySubtitle : undefined}
      footer={
        release ? (
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Button
              variant={isOwnerPinned ? "secondary" : "outline"}
              size="sm"
              disabled={busy}
              onClick={() => onPin(release.ReleaseID, !isOwnerPinned)}
            >
              <Pin size={14} className={isOwnerPinned ? "fill-primary text-primary" : ""} />
              {isOwnerPinned ? t("Unpin") : t("Keep in cache")}
            </Button>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => {
                  void Promise.resolve(onClear(release.ReleaseID, title, false)).then((cleared) => {
                    if (cleared) onClose();
                  });
                }}
              >
                {t("Clear")}
              </Button>
              <Button
                variant="destructive"
                size="sm"
                disabled={busy}
                onClick={() => {
                  void Promise.resolve(onClear(release.ReleaseID, title, true)).then((cleared) => {
                    if (cleared) onClose();
                  });
                }}
              >
                {t("Clear & block")}
              </Button>
            </div>
          </div>
        ) : undefined
      }
    >
      {!snapshot ? (
        <p className="py-8 text-center text-sm text-fg-muted">{t("Loading cache…")}</p>
      ) : !release ? (
        <div className="py-10 text-center">
          <Film size={24} className="mx-auto mb-3 text-fg-subtle" />
          <p className="text-sm font-medium text-fg">
            {t("This release is no longer in the cache.")}
          </p>
          <p className="mt-1 text-xs text-fg-muted">
            {t("It may have been cleared or evicted to make room for newer media.")}
          </p>
        </div>
      ) : (
        <div className="space-y-6">
          <div className="flex items-start gap-3.5">
            <ReleasePoster ratingKey={ratingKey} title={title} />
            <div className="min-w-0 flex-1 space-y-2">
              <div className="flex flex-wrap gap-1.5">
                {pills.map((pill) => (
                  <Badge key={pill.label} variant={pill.active ? "default" : "secondary"}>
                    {pill.label}
                  </Badge>
                ))}
                {resBadge ? <Badge variant="secondary">{resBadge}</Badge> : null}
                {range ? <Badge variant="secondary">{range}</Badge> : null}
                {sourceBadge ? <Badge variant="secondary">{sourceBadge}</Badge> : null}
              </div>
              {info ? (
                <p className="text-xs text-fg-muted">{qualityLabel(info)}</p>
              ) : release.reason?.source && !isOpaqueId(release.reason.source) ? (
                <p className="truncate text-xs text-fg-muted">{release.reason.source}</p>
              ) : null}
              {ratingKey ? (
                <Link
                  to={searchLink(String(ratingKey), title)}
                  className="inline-block text-xs font-medium text-primary hover:underline"
                >
                  {t("Manage title releases")}
                </Link>
              ) : null}
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <Kpi
              label={t("Cached on disk")}
              value={formatBytes(release.Bytes)}
              detail={
                release.Size > 0
                  ? t("{percent} of {total}", {
                      percent: formatPercent(Math.min(1, release.Bytes / release.Size)),
                      total: formatBytes(release.Size),
                    })
                  : t("Source size unrecorded")
              }
            />
            <Kpi
              label={t("Segments")}
              value={formatNumber(release.Segments)}
              detail={
                release.Plays > 0
                  ? tn(release.Plays, "{count} play recorded", "{count} plays recorded")
                  : t("Not played yet")
              }
            />
          </div>

          <section aria-label={t("What's cached")} className="space-y-3">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">
              {t("What's cached")}
            </h3>
            <SegmentMap release={release} detail={detail} />
            <dl className="divide-y divide-border">
              <DetailRow label={t("Release")}>
                {release.reason?.source && !isOpaqueId(release.reason.source) ? (
                  <span className="font-mono text-xs">{release.reason.source}</span>
                ) : (
                  "—"
                )}
              </DetailRow>
              <DetailRow label={t("File")}>
                {release.reason?.file && !isOpaqueId(release.reason.file) ? (
                  <span className="font-mono text-xs">{release.reason.file}</span>
                ) : (
                  "—"
                )}
              </DetailRow>
              <DetailRow label={t("Format")}>
                {info ? (
                  <span>
                    {qualityLabel(info)}
                    {range ? ` · ${range}` : ""}
                    {info.group ? (
                      <span className="block text-xs text-fg-muted">
                        {t("Release group: {group}", { group: info.group })}
                      </span>
                    ) : null}
                  </span>
                ) : (
                  "—"
                )}
              </DetailRow>
              <DetailRow label={t("Bytes")}>
                {formatBytes(release.Bytes)}
                {release.Size > 0 ? ` / ${formatBytes(release.Size)}` : ""}
              </DetailRow>
              <DetailRow label={t("Cache key")}>
                <span className="flex min-w-0 items-center gap-2">
                  <code
                    className="min-w-0 truncate rounded bg-bg-subtle px-2 py-1 font-mono text-xs text-fg-muted"
                    title={release.ReleaseID}
                  >
                    {compactKey(release.ReleaseID)}
                  </code>
                  <CopyButton value={release.ReleaseID} label={t("Copy cache key")} />
                </span>
              </DetailRow>
            </dl>
          </section>

          <section aria-label={t("Where")}>
            <h3 className="mb-1 text-xs font-semibold uppercase tracking-wider text-fg-subtle">
              {t("Where")}
            </h3>
            <dl className="divide-y divide-border">
              <DetailRow label={t("Origin")}>{originLabel(release.reason?.origin)}</DetailRow>
              <DetailRow label={t("Source")}>
                {release.reason?.indexer ||
                  (release.reason?.source && !isOpaqueId(release.reason.source)
                    ? release.reason.source
                    : "—")}
              </DetailRow>
              <DetailRow label={t("Storage")}>
                {storageKind}
                {snapshot?.directory ? (
                  <span className="block truncate font-mono text-xs text-fg-muted">
                    {snapshot.directory}
                  </span>
                ) : null}
              </DetailRow>
            </dl>
          </section>

          <section aria-label={t("Why it's cached")}>
            <h3 className="mb-1 text-xs font-semibold uppercase tracking-wider text-fg-subtle">
              {t("Why it's cached")}
            </h3>
            <dl className="divide-y divide-border">
              <DetailRow label={t("Fetched because")}>
                <ul className="space-y-1.5">
                  {causes.length ? (
                    causes.map((c) => {
                      const seen = parseTime(c.firstSeen);
                      return (
                        <li key={c.code}>
                          <span className="font-medium text-fg">{causeLabel(c.code)}</span>
                          <span className="block text-xs text-fg-muted">
                            {causeDescription(c.code)}
                            {seen ? ` · ${relativeTime(seen)}` : ""}
                          </span>
                        </li>
                      );
                    })
                  ) : (
                    <li>
                      <span className="font-medium text-fg">{t("Unexplained")}</span>
                      <span className="block text-xs text-fg-muted">
                        {causeDescription("unrecorded")}
                      </span>
                    </li>
                  )}
                </ul>
              </DetailRow>
              {shelfScore !== undefined || shelfReasonCodes?.length ? (
                <DetailRow label={t("Shelf score")}>
                  <div className="space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      {shelfScore !== undefined ? (
                        <span className="inline-flex items-center gap-1 rounded-md bg-primary/10 px-2 py-0.5 font-mono text-xs font-medium text-primary">
                          <Sparkles size={12} />
                          {t("{score}/100", { score: Math.round(shelfScore) })}
                        </span>
                      ) : null}
                      {shelfState ? (
                        <Badge variant={shelfState === "cached" ? "success" : "secondary"}>
                          {shelfStateLabel(shelfState)}
                        </Badge>
                      ) : null}
                    </div>
                    <p className="text-xs text-fg-muted">
                      {selectionReason(shelfReasonCodes, preloadSelection?.reasonArgs)}
                    </p>
                    {preloadSelection ? (
                      <p className="text-[11px] text-fg-subtle">
                        {t("Catalogue {score}/100", {
                          score: Math.round(preloadSelection.catalogue),
                        })}{" "}
                        ·{" "}
                        {t("Household {score}/100", {
                          score: Math.round(preloadSelection.household),
                        })}
                      </p>
                    ) : null}
                  </div>
                </DetailRow>
              ) : null}
              <DetailRow label={t("Selected by")}>
                {warmingRuleLabel(primaryCode, policyName, snapshot?.policy)}
              </DetailRow>
              <DetailRow label={t("Kept because")}>{retentionLabel(retention)}</DetailRow>
            </dl>
          </section>

          <section aria-label={t("When")}>
            <h3 className="mb-1 text-xs font-semibold uppercase tracking-wider text-fg-subtle">
              {t("When")}
            </h3>
            <dl className="divide-y divide-border">
              <DetailRow label={t("Cached at")}>
                {cachedAt ? (
                  <>
                    {formatDateTime(cachedAt)}
                    <span className="block text-xs text-fg-muted">{relativeTime(cachedAt)}</span>
                  </>
                ) : (
                  "—"
                )}
              </DetailRow>
              <DetailRow label={t("Last used")}>
                {lastUsed ? (
                  <>
                    {formatDateTime(lastUsed)}
                    <span className="block text-xs text-fg-muted">{relativeTime(lastUsed)}</span>
                  </>
                ) : (
                  "—"
                )}
              </DetailRow>
              <DetailRow label={t("Play history")}>
                {tn(release.Plays, "{count} play", "{count} plays")}
                <span className="block text-xs text-fg-muted">
                  {lastPlayed
                    ? `${formatDateTime(lastPlayed)} · ${relativeTime(lastPlayed)}`
                    : t("Never played")}
                </span>
              </DetailRow>
              <DetailRow label={t("Eviction")}>
                {retention?.kind === "pinned" ? (
                  t("Protected while pinned ({pins})", {
                    pins:
                      Array.from(
                        new Set((retention.pins ?? release.Pins ?? []).map(pinLabel)),
                      ).join(" · ") || t("Kept by you"),
                  })
                ) : retention?.kind === "expiry" && expiryUntil ? (
                  <>
                    {formatDateTime(expiryUntil)}
                    <span className="block text-xs text-fg-muted">
                      {relativeTime(expiryUntil)} ·{" "}
                      {tn(retention.days ?? 0, "{count} inactive day", "{count} inactive days")}
                    </span>
                  </>
                ) : retention?.kind === "replay" ? (
                  t("+{hours} h replay credit before space eviction", {
                    hours: retention.hours ?? 0,
                  })
                ) : snapshot?.budget ? (
                  t("Evicted oldest-first when the {budget} budget needs room", {
                    budget: formatBytes(snapshot.budget),
                  })
                ) : (
                  t("Reclaimable anytime")
                )}
              </DetailRow>
            </dl>
          </section>
        </div>
      )}
    </Drawer>
  );
}
