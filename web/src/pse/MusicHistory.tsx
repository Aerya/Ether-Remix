import { useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync, useInterval } from "../lib/hooks";
import { ErrorPanel, LoadingRows } from "../components/feedback";
import type { EtherUser } from "../lib/types";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface MusicPlay {
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

export function MusicHistory({ query, viewers }: { query: string; viewers: EtherUser[] }) {
  // Changer de période ou de spectateur repart de la première page.
  return <MusicHistoryPage key={query} query={query} viewers={viewers} />;
}
function MusicHistoryPage({ query, viewers }: { query: string; viewers: EtherUser[] }) {
  const [offset, setOffset] = useState(0);
  const state = useAsync(
    () => apiFetch<MusicPage>(`/pse/music/history${query}&offset=${offset}`),
    [query, offset],
  );
  useInterval(() => void state.reload(), 30_000);
  return (
    <section className="space-y-3 rounded-xl border border-border p-4">
      <h2 className="text-lg font-semibold">{L("Historique musical", "Music history")}</h2>
      <p className="text-sm text-fg-muted">
        {L(
          "Écoutes signalées par les clients. La durée indiquée est celle du morceau. Les statistiques ci-dessous concernent les films et séries.",
          "Listens reported by clients. Duration is the track length. The statistics below cover movies and shows.",
        )}
      </p>
      {state.error ? (
        <ErrorPanel error={state.error} onRetry={() => void state.reload()} />
      ) : !state.data ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          {state.data.plays.length === 0 ? (
            <p>{L("Aucune écoute sur cette période.", "No listens in this period.")}</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr>
                    {[
                      L("Morceau", "Track"),
                      L("Artiste · Album", "Artist · Album"),
                      L("Spectateur", "Viewer"),
                      L("Date", "Date"),
                      L("Durée", "Duration"),
                    ].map((x) => (
                      <th key={x} className="p-2">
                        {x}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {state.data.plays.map((p) => (
                    <tr key={p.id} className="border-t border-border">
                      <td className="p-2">{p.title}</td>
                      <td className="p-2">{[p.artist, p.album].filter(Boolean).join(" · ")}</td>
                      <td className="p-2">
                        {(() => {
                          const v = viewers.find((v) => v.id === p.viewerId);
                          return v?.title || v?.username || `#${p.viewerId}`;
                        })()}
                      </td>
                      <td className="p-2">
                        {new Date(p.playedAt * 1000).toLocaleString(getLocale())}
                      </td>
                      <td className="p-2">
                        {Math.floor(p.duration / 60)}:{String(p.duration % 60).padStart(2, "0")}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className="flex items-center gap-3">
            <Button
              size="sm"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - 100))}
            >
              {L("Précédent", "Previous")}
            </Button>
            <span>
              {state.data.total
                ? `${offset + 1}–${Math.min(offset + 100, state.data.total)} / ${state.data.total}`
                : "0"}
            </span>
            <Button
              size="sm"
              disabled={offset + 100 >= state.data.total}
              onClick={() => setOffset(offset + 100)}
            >
              {L("Suivant", "Next")}
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
