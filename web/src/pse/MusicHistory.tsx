import { apiFetch } from "../lib/api";
import { useAsync, useInterval } from "../lib/hooks";
import { ErrorPanel } from "../components/feedback";
import type { EtherUser } from "../lib/types";
import { PlayHistory } from "../pages/activity/History";
import type { Play, Summary } from "../pages/activity/types";
import { L } from "./Listening";
export interface MusicPlay {
  id: number;
  viewerId: number;
  title: string;
  artist: string;
  album: string;
  duration: number;
  playedAt: number;
}
interface MusicPage {
  plays: MusicPlay[];
  total: number;
}
export function combineHistory(video: Play[], music: MusicPlay[], viewers: EtherUser[]): Play[] {
  return [
    ...video,
    ...music.map((p) => ({
      id: `music:${p.id}`,
      userId: p.viewerId,
      username:
        viewers.find((v) => v.id === p.viewerId)?.title ||
        viewers.find((v) => v.id === p.viewerId)?.username ||
        `#${p.viewerId}`,
      title: p.title,
      started: p.playedAt,
      ended: 0,
      device: "",
      platform: "",
      decision: "",
      bytes: 0,
      watchedMs: 0,
      completed: false,
      failovers: 0,
      error: "",
      music: { artist: p.artist, album: p.album, duration: p.duration },
    })),
  ]
    .sort((a, b) => b.started - a.started || a.id.localeCompare(b.id))
    .slice(0, 500);
}
export function MusicHistory({
  query,
  viewers,
  history,
  summary,
  error,
  onRetry,
}: {
  query: string;
  viewers: EtherUser[];
  history: Play[] | undefined;
  summary: Summary | undefined;
  error: unknown;
  onRetry: () => void;
}) {
  const music = useAsync(
    (signal) => apiFetch<MusicPage>(`/pse/music/history${query}&limit=500`, { signal }),
    [query],
  );
  useInterval(() => void music.reload(), 30_000);
  return (
    <>
      {music.error && history && music.data && (
        <ErrorPanel error={music.error} onRetry={() => void music.reload()} />
      )}
      <PlayHistory
        history={
          history && music.data ? combineHistory(history, music.data.plays, viewers) : undefined
        }
        summary={summary}
        musicTotal={music.data?.total}
        error={error || music.error}
        onRetry={() => {
          onRetry();
          void music.reload();
        }}
      />
      <p className="text-xs text-fg-muted">
        {L(
          "Les 500 lectures les plus récentes de la période, tous médias réunis. Pour la musique, la durée affichée est celle du morceau ; le temps d’écoute et l’appareil ne sont pas enregistrés.",
          "The latest 500 plays in the period, across all media. For music, duration is the track length; listening time and device are not recorded.",
        )}
      </p>
    </>
  );
}
