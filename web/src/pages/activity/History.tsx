import { L } from "../../pse/Listening";
import { Badge, EmptyState, Input, PagedTable, Select } from "@braedonsaunders/appkit-ui";
import { CheckCircle2, Clock, History as HistoryIcon, PlayCircle, Headphones } from "lucide-react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Kpi, KpiRow } from "../../components/Kpi";
import { LiveChannelArtwork } from "../../components/SessionArtwork";
import { formatDateTime, fixed, formatNumber, formatPercent } from "../../lib/format";
import { channelLabel } from "../../lib/health";
import { DASH, formatSeconds } from "./experience-format";
import { getLocale, t, tn } from "../../i18n";
import { pagedTableLabels } from "../../i18n/appkit-labels";
import type { EtherUser } from "../../lib/types";
import { PlayDrawer, usePlayParam } from "./PlayDrawer";
import { viewerLabel, type Play, type Summary, type ViewingFilter } from "./types";

/** Period, viewer and year-in-review filters on History's and Stats' tab row. */
export function ViewingFilters({
  value,
  onChange,
  viewers,
}: {
  value: ViewingFilter;
  onChange: (next: ViewingFilter) => void;
  viewers: EtherUser[];
}) {
  return (
    <div className="flex flex-wrap items-center gap-2" role="group" aria-label={t("Filters")}>
      <Select
        aria-label={t("Period")}
        className="h-8 w-40 text-sm"
        value={value.days}
        onChange={(e) => onChange({ ...value, days: e.target.value, year: "" })}
      >
        {[7, 30, 90, 365].map((n) => (
          <option key={n} value={String(n)}>
            {tn(n, "Last {count} day", "Last {count} days")}
          </option>
        ))}
      </Select>
      <Select
        aria-label={t("Viewer")}
        className="h-8 w-44 text-sm"
        value={value.user}
        onChange={(e) => onChange({ ...value, user: e.target.value })}
      >
        <option value="">{t("Everyone")}</option>
        {viewers.map((v) => (
          <option key={v.id} value={String(v.id)}>
            {viewerLabel(v)}
          </option>
        ))}
      </Select>
      <Input
        aria-label={t("Year in review")}
        className="h-8 w-28 text-sm"
        type="number"
        min={2000}
        max={new Date().getFullYear()}
        placeholder={t("Year")}
        value={value.year}
        onChange={(e) => onChange({ ...value, year: e.target.value })}
      />
    </div>
  );
}

const isLivePlay = (p: Play) => Boolean(p.channelId || p.channelName);

function playChannelTitle(p: Play): string {
  return (
    channelLabel({
      channelName: p.channelName ?? "",
      channelNumber: p.channelNumber ?? "",
    }) || p.title
  );
}

function statusCell(p: Play) {
  if (p.music) return <Badge variant="secondary">{L("Écoute déclarée", "Reported listen")}</Badge>;
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap gap-1">
        {isLivePlay(p) ? <Badge variant="info">{t("Live TV")}</Badge> : null}
        <Badge variant={p.completed ? "success" : "secondary"}>
          {p.completed ? t("Completed") : p.ended ? t("Ended") : t("Last observed")}
        </Badge>
        {p.failovers ? (
          <Badge variant="warning">
            {tn(p.failovers, "{count} failover", "{count} failovers")}
          </Badge>
        ) : null}
      </div>
      {p.error ? <p className="text-xs text-danger">{p.error}</p> : null}
    </div>
  );
}

/** Recorded plays for the selected period and viewer. */
export function PlayHistory({
  history,
  summary,
  error,
  onRetry,
  musicTotal,
}: {
  musicTotal?: number;
  history: Play[] | undefined;
  summary: Summary | undefined;
  error: unknown;
  onRetry: () => void;
}) {
  const [playId, openPlay] = usePlayParam();
  const musicPlay = history?.find((p) => p.id === playId && p.music);
  const drawer = playId?.startsWith("music:") ? (
    <Drawer open onClose={() => openPlay(null)} title={musicPlay?.title || L("Écoute", "Listen")}>
      {musicPlay ? (
        <dl className="divide-y divide-border">
          <Detail label={L("Artiste", "Artist")}>{musicPlay.music?.artist || DASH}</Detail>
          <Detail label={L("Album", "Album")}>{musicPlay.music?.album || DASH}</Detail>
          <Detail label={t("Viewer")}>{musicPlay.username}</Detail>
          <Detail label={t("Started")}>{formatDateTime(new Date(musicPlay.started * 1000))}</Detail>
          <Detail label={L("Durée du morceau", "Track length")}>
            {formatSeconds((musicPlay.music?.duration || 0) * 1000)}
          </Detail>
        </dl>
      ) : (
        <p>
          {L("Écoute absente de la sélection actuelle.", "Listen not in the current selection.")}
        </p>
      )}
    </Drawer>
  ) : (
    <PlayDrawer id={playId} onClose={() => openPlay(null)} />
  );
  if (error && !history)
    return (
      <>
        <ErrorPanel error={error} onRetry={onRetry} />
        {drawer}
      </>
    );
  return (
    <>
      <KpiRow>
        <Kpi
          label={t("Plays")}
          value={summary ? formatNumber(summary.plays + (musicTotal ?? 0), 0) : DASH}
          icon={<PlayCircle size={18} />}
        />
        <Kpi
          label={L("Écoutes musicales", "Music listens")}
          value={musicTotal != null ? formatNumber(musicTotal, 0) : DASH}
          icon={<Headphones size={18} />}
          tone="info"
        />
        <Kpi
          label={L("Session vidéo moyenne", "Average video session")}
          value={
            summary && summary.plays
              ? t("{minutes} min", { minutes: fixed(summary.watchedMs / summary.plays / 60000, 0) })
              : DASH
          }
          icon={<Clock size={18} />}
          tone="info"
        />
        <Kpi
          label={L("Complétion vidéo", "Video completion")}
          value={summary && summary.plays ? formatPercent(summary.completed / summary.plays) : DASH}
          icon={<CheckCircle2 size={18} />}
          tone="success"
        />
      </KpiRow>
      {!history ? (
        <LoadingRows rows={4} />
      ) : history.length ? (
        <PagedTable
          rows={history}
          rowKey={(p) => p.id}
          pageSize={20}
          onRowClick={(p) => openPlay(p.id)}
          empty={t("No recorded plays in this period.")}
          defaultSort={{ key: "started", dir: "desc" }}
          labels={pagedTableLabels({
            searchLabel: t("Search play history"),
            searchPlaceholder: t("Find a title, viewer or device…"),
          })}
          columns={[
            {
              key: "title",
              header: t("Title / viewer"),
              search: (p) =>
                [
                  p.channelNumber,
                  p.channelName,
                  p.title,
                  p.username,
                  p.music?.artist,
                  p.music?.album,
                ]
                  .filter(Boolean)
                  .join(" "),
              sortValue: (p) => (isLivePlay(p) ? p.channelName || p.title : p.title),
              cell: (p) => {
                if (isLivePlay(p)) {
                  const channel = playChannelTitle(p);
                  const programme =
                    p.title && p.title !== p.channelName && p.title !== channel ? p.title : "";
                  const sub = [programme, p.username || t("Unknown viewer")]
                    .filter(Boolean)
                    .join(" · ");
                  return (
                    <div className="flex items-center gap-2.5">
                      <LiveChannelArtwork
                        channelId={p.channelId}
                        name={p.channelName || p.channelNumber || p.title}
                        className="size-8 shrink-0 rounded-md"
                        imgClassName="p-1"
                      />
                      <div className="min-w-0">
                        <p className="truncate font-medium text-fg">{channel}</p>
                        <p className="truncate text-xs text-fg-muted">{sub}</p>
                      </div>
                    </div>
                  );
                }
                return (
                  <>
                    <p className="font-medium text-fg">
                      {p.title}{" "}
                      {p.music && <Badge variant="secondary">{L("Musique", "Music")}</Badge>}
                    </p>
                    {p.music && (
                      <p className="text-xs text-fg-muted">
                        {[p.music.artist, p.music.album].filter(Boolean).join(" · ")}
                      </p>
                    )}
                    <p className="text-xs text-fg-muted">{p.username || t("Unknown viewer")}</p>
                  </>
                );
              },
            },
            {
              key: "started",
              header: t("Started"),
              sortValue: (p) => p.started,
              cell: (p) => new Date(p.started * 1000).toLocaleString(getLocale()),
            },
            {
              key: "device",
              header: t("Device"),
              search: (p) => (p.device || p.platform) + " " + p.decision,
              cell: (p) => (
                <>
                  {p.music ? DASH : p.device || p.platform || t("Unknown")}
                  <span className="block text-xs text-fg-muted">{p.decision}</span>
                </>
              ),
            },
            {
              key: "duration",
              header: L("Durée", "Duration"),
              align: "right",
              sortValue: (p) => (p.music ? p.music.duration * 1000 : p.watchedMs),
              cell: (p) =>
                p.music ? (
                  <>
                    {formatSeconds(p.music.duration * 1000)}
                    <span className="block text-xs text-fg-muted">
                      {L("Durée du morceau", "Track length")}
                    </span>
                  </>
                ) : (
                  t("{minutes} min", { minutes: fixed(p.watchedMs / 60000, 0) })
                ),
            },
            { key: "status", header: t("Status"), cell: statusCell },
          ]}
        />
      ) : (
        <EmptyState
          icon={<HistoryIcon />}
          title={t("No plays yet")}
          description={t(
            "History starts when Ether observes playback. There are no recorded plays in this period.",
          )}
        />
      )}
      {drawer}
    </>
  );
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[9rem_1fr] gap-3 py-2 text-sm">
      <dt className="text-fg-muted">{label}</dt>
      <dd className="min-w-0 break-words text-fg">{children}</dd>
    </div>
  );
}
