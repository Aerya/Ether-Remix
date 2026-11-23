import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Badge } from "@braedonsaunders/appkit-ui";
import { Clock3, Sparkles, Clapperboard } from "lucide-react";
import { Artwork } from "../../components/Artwork";
import { HourStrip, DivergingBars, Meter } from "../../components/charts";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import type { TasteProfile, ViewerDetail } from "../../lib/contract";
import { formatNumber, formatPercent } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { affinityLabel, extractTopPicks } from "./TasteView";
import { Kpi } from "../../components/Kpi";

export function ViewerDashboard({
  viewer,
  taste,
}: {
  viewer: ViewerDetail;
  taste?: TasteProfile | null;
}) {
  const home = useAsync(
    (signal) =>
      api.viewerHome(
        viewer.id,
        { device: "tv", section: "home", at: new Date().toISOString() },
        signal,
      ),
    [viewer.id],
  );
  const picks = extractTopPicks(home.data, 6).map((pick) => pick.item);
  return (
    <div aria-label={t("Viewer dashboard")} className="flex flex-col gap-4">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        {[
          [t("Plays in 30 days"), formatNumber(viewer.plays30d)],
          [t("Hours in 30 days"), formatNumber(viewer.hoursWatched30d, 1)],
          [
            t("Recent plays finished"),
            viewer.recent?.length
              ? formatPercent(
                  viewer.recent.filter((play) => play.completed).length / viewer.recent.length,
                )
              : "—",
          ],
        ].map(([label, value]) => (
          <Kpi key={label} label={label!} value={value} />
        ))}
      </div>
      <div className="grid grid-cols-1 items-start gap-4 md:grid-cols-2">
        <DashboardPanel title={t("Next in their feed")} icon={<Sparkles size={16} />}>
          {home.error ? (
            <ErrorPanel error={home.error} onRetry={() => void home.reload()} />
          ) : !home.data ? (
            <LoadingRows rows={3} />
          ) : (
            <div className="space-y-3">
              {picks.map((pick, i) => (
                <div
                  key={pick.key}
                  className="flex items-center gap-3 rounded-lg border border-border p-3"
                >
                  <Artwork
                    src={pick.poster}
                    alt=""
                    title={pick.title}
                    className="aspect-[2/3] w-14 shrink-0 rounded"
                  />
                  <div className="min-w-0">
                    <Badge variant={i === 0 ? "info" : "secondary"}>
                      {t("Pick {rank}", { rank: i + 1 })}
                    </Badge>
                    <p className="mt-2 truncate text-sm font-semibold">{pick.title}</p>
                    <p className="text-xs text-fg-muted">{pick.year}</p>
                  </div>
                </div>
              ))}
              {!picks.length && (
                <p className="text-sm text-fg-muted">
                  {t("Building recommendations for this viewer…")}
                </p>
              )}
            </div>
          )}
        </DashboardPanel>
        <div className="flex flex-col gap-4">
          <DashboardPanel title={t("When they watch")} icon={<Clock3 size={16} />}>
            {taste ? (
              <div className="space-y-4">
                <HourStrip
                  peaks={taste.habits.peakHours ?? []}
                  ariaLabel={t("Peak viewing hours")}
                />
                <Meter
                  value={taste.habits.bingeScore}
                  label={t("Binge watching")}
                  valueLabel={formatPercent(taste.habits.bingeScore)}
                />
                <Meter value={taste.habits.rewatchScore} label={t("Rewatching")} />
              </div>
            ) : (
              <LoadingRows rows={2} />
            )}
          </DashboardPanel>
          <DashboardPanel title={t("Top genres")} icon={<Clapperboard size={16} />}>
            {taste ? (
              <DivergingBars
                ariaLabel={t("Genre affinity from dislike to like")}
                valueFormat={affinityLabel}
                data={[...taste.genres]
                  .sort((a, b) => b.weight - a.weight)
                  .slice(0, 5)
                  .map((g) => ({ key: g.name, label: g.name, weight: g.weight }))}
              />
            ) : (
              <LoadingRows rows={2} />
            )}
          </DashboardPanel>
        </div>
      </div>
    </div>
  );
}
