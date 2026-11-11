import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Disc3, Headphones, Music2, Users } from "lucide-react";
import { RankedBars, TimeBars, WeekHeatmap } from "../components/charts";
import { Kpi, KpiRow } from "../components/Kpi";
import { ErrorPanel, LoadingRows } from "../components/feedback";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync, useInterval } from "../lib/hooks";
import type { EtherUser } from "../lib/types";
export const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export interface ListeningBucket {
  key: string;
  label: string;
  plays: number;
}
export interface ListeningSummary {
  plays: number;
  tracks: number;
  artists: number;
  listeners: number;
  daily: ListeningBucket[];
  titles: ListeningBucket[];
  artistsRanked: ListeningBucket[];
  albums: ListeningBucket[];
  users: ListeningBucket[];
  heatmap: { weekday: number; hour: number; plays: number }[];
}
export function Listening({ query, viewers }: { query: string; viewers: EtherUser[] }) {
  const state = useAsync(
    (signal) => apiFetch<ListeningSummary>(`/pse/music/listening${query}`, { signal }),
    [query],
  );
  useInterval(() => void state.reload(), 30_000);
  if (state.error) return <ErrorPanel error={state.error} onRetry={() => void state.reload()} />;
  if (!state.data) return <LoadingRows rows={4} />;
  const s = state.data;
  const count = (v: number) => v.toLocaleString(getLocale());
  const ranks = (items: ListeningBucket[], users = false) =>
    items.map((b) => {
      const user = users ? viewers.find((v) => String(v.id) === b.key) : undefined;
      return {
        key: b.key,
        label: users ? user?.title || user?.username || `#${b.key}` : b.label,
        value: b.plays,
      };
    });
  return (
    <>
      <KpiRow>
        <Kpi
          label={L("Écoutes", "Listens")}
          value={count(s.plays)}
          icon={<Headphones size={18} />}
        />
        <Kpi
          label={L("Morceaux", "Tracks")}
          value={count(s.tracks)}
          icon={<Music2 size={18} />}
          tone="info"
        />
        <Kpi
          label={L("Artistes", "Artists")}
          value={count(s.artists)}
          icon={<Disc3 size={18} />}
          tone="success"
        />
        <Kpi
          label={L("Auditeurs", "Listeners")}
          value={count(s.listeners)}
          icon={<Users size={18} />}
          tone="info"
        />
      </KpiRow>
      <DashboardPanel title={L("Écoutes par jour", "Daily listens")}>
        <TimeBars
          ariaLabel={L("Écoutes par jour", "Daily listens")}
          height={180}
          valueFormat={count}
          data={s.daily.map((b) => ({ key: b.key, label: b.label, value: b.plays }))}
        />
      </DashboardPanel>
      <DashboardPanel title={L("Quand écoutez-vous ?", "When people listen")}>
        <WeekHeatmap
          ariaLabel={L("Écoutes par jour et heure", "Listens by weekday and hour")}
          cells={s.heatmap.map((c) => ({ ...c, value: c.plays }))}
          valueFormat={count}
        />
      </DashboardPanel>
      <div className="grid gap-6 lg:grid-cols-2">
        {[
          {
            title: L("Morceaux les plus écoutés", "Top tracks"),
            items: s.titles,
            color: "viz-1" as const,
          },
          {
            title: L("Artistes les plus écoutés", "Top artists"),
            items: s.artistsRanked,
            color: "viz-2" as const,
          },
          {
            title: L("Albums les plus écoutés", "Top albums"),
            items: s.albums,
            color: "viz-3" as const,
          },
          {
            title: L("Auditeurs les plus actifs", "Top listeners"),
            items: s.users,
            color: "viz-5" as const,
            users: true,
          },
        ].map((p) => (
          <DashboardPanel key={p.title} title={p.title}>
            <RankedBars
              ariaLabel={p.title}
              color={p.color}
              data={ranks(p.items, p.users)}
              valueFormat={count}
              empty={
                <p className="py-4 text-fg-muted">
                  {L("Aucune écoute sur cette période.", "No listens in this period.")}
                </p>
              }
            />
          </DashboardPanel>
        ))}
      </div>
      <p className="text-xs text-fg-muted">
        {L(
          "Écoutes déclarées par les clients, sur toute la période sélectionnée. Les jours et heures utilisent le fuseau du navigateur. La durée réelle d’écoute n’est pas mesurée.",
          "Client-reported listens across the entire selected period. Days and hours use the browser time zone. Actual listening time is not measured.",
        )}
      </p>
    </>
  );
}
