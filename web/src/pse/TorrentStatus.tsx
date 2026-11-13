import { useState } from "react";
import { Badge, Button } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

interface ClientState {
  name: string;
  version?: string;
  error?: string;
  free?: number;
  freeNote?: string;
  lowSpace?: boolean;
}
interface DownloadState {
  client: string;
  name: string;
  progress: number;
  size: number;
  eta?: number;
  done: boolean;
  active: boolean;
  ratio: number;
}
export interface TorrentState {
  clients: ClientState[];
  downloads: DownloadState[] | null;
  seeding: number;
  minFree: number;
  addedToday: number;
  maxPerDay: number;
}

export function formatSize(bytes: number): string {
  const units = getLocale().startsWith("fr")
    ? ["o", "Ko", "Mo", "Go", "To"]
    : ["B", "KB", "MB", "GB", "TB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toLocaleString(getLocale(), { maximumFractionDigits: v >= 100 || i === 0 ? 0 : 1 })} ${units[i]}`;
}

export function formatETA(seconds: number): string {
  if (seconds < 60) return "< 1 min";
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`;
  const h = Math.floor(seconds / 3600);
  return `${h} h ${String(Math.round((seconds % 3600) / 60)).padStart(2, "0")}`;
}

// État des clients torrent et des téléchargements de Remix (catégorie
// Remix), avec un bouton pour l'actualiser sans recharger la page.
export function TorrentStatus({ status }: { status?: Record<string, unknown> }) {
  const [fresh, setFresh] = useState<TorrentState>();
  const [busy, setBusy] = useState(false);
  const state = fresh ?? (status?.state as TorrentState | undefined);
  if (!status?.active || !state) return null;

  async function refresh() {
    setBusy(true);
    try {
      const doc = await apiFetch<{ features: { id: string; status?: Record<string, unknown> }[] }>(
        "/pse/",
      );
      const next = doc.features.find((f) => f.id === "torrent")?.status?.state as
        TorrentState | undefined;
      if (next) setFresh(next);
    } finally {
      setBusy(false);
    }
  }

  const downloads = state.downloads ?? [];
  const running = downloads.filter((d) => !d.done);
  return (
    <div className="space-y-3 border-b border-border px-4 py-3 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-fg-muted">
          {L(
            `Ajouts aujourd’hui : ${state.addedToday} / ${state.maxPerDay} · ${running.length} en cours · ${state.seeding} terminés`,
            `Added today: ${state.addedToday} / ${state.maxPerDay} · ${running.length} downloading · ${state.seeding} complete`,
          )}
        </span>
        <Button size="sm" variant="secondary" disabled={busy} onClick={() => void refresh()}>
          {busy ? L("Actualisation…", "Refreshing…") : L("Actualiser", "Refresh")}
        </Button>
      </div>
      <ul className="space-y-1">
        {state.clients.map((c) => (
          <li key={c.name} className="flex flex-wrap items-center gap-2">
            <Badge variant={c.error ? "destructive" : c.lowSpace ? "warning" : "success"}>
              {c.name}
            </Badge>
            <span className="text-fg-muted">
              {c.error ? L(`Injoignable : ${c.error}`, `Unreachable: ${c.error}`) : c.version}
              {" · "}
              {c.free !== undefined
                ? c.lowSpace
                  ? L(
                      `${formatSize(c.free)} libres, sous le seuil de ${formatSize(state.minFree)} : plus aucun ajout`,
                      `${formatSize(c.free)} free, below the ${formatSize(state.minFree)} threshold: no new additions`,
                    )
                  : L(`${formatSize(c.free)} libres`, `${formatSize(c.free)} free`)
                : L(
                    `place libre inconnue (${c.freeNote || "dossier invisible"})`,
                    `free space unknown (${c.freeNote || "folder not visible"})`,
                  )}
            </span>
          </li>
        ))}
      </ul>
      {downloads.length ? (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="text-fg-muted">
              <tr>
                <th className="py-1 pr-3 font-medium">{L("Torrent", "Torrent")}</th>
                <th className="py-1 pr-3 font-medium">{L("Avancement", "Progress")}</th>
                <th className="py-1 pr-3 font-medium">{L("Taille", "Size")}</th>
                <th className="py-1 pr-3 font-medium">{L("Ratio", "Ratio")}</th>
                {state.clients.length > 1 ? (
                  <th className="py-1 font-medium">{L("Client", "Client")}</th>
                ) : null}
              </tr>
            </thead>
            <tbody>
              {downloads.map((d, i) => (
                <tr key={`${d.client}-${d.name}-${i}`} className="border-t border-border">
                  <td className="max-w-[28rem] truncate py-1 pr-3" title={d.name}>
                    {d.name}
                  </td>
                  <td className="whitespace-nowrap py-1 pr-3">
                    {d.done
                      ? d.active
                        ? L("Terminé, en partage", "Complete, seeding")
                        : L("Terminé, arrêté", "Complete, stopped")
                      : `${Math.floor(d.progress * 100)} %${d.eta ? ` · ${formatETA(d.eta)}` : ""}`}
                  </td>
                  <td className="whitespace-nowrap py-1 pr-3">
                    {d.size ? formatSize(d.size) : "—"}
                  </td>
                  <td className="py-1 pr-3">
                    {d.ratio.toLocaleString(getLocale(), { maximumFractionDigits: 2 })}
                  </td>
                  {state.clients.length > 1 ? (
                    <td className="whitespace-nowrap py-1">{d.client}</td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-fg-muted">
          {L("Aucun torrent de Remix pour l’instant.", "No Remix torrent yet.")}
        </p>
      )}
    </div>
  );
}
