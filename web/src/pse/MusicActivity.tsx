import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Music } from "lucide-react";
import { useState } from "react";
import { TimeBars } from "../components/charts";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { formatNumber } from "../lib/format";
import { useAsync } from "../lib/hooks";

/**
 * Bloc Musique de la Vue d'ensemble (internal/pse/music/plays.go) :
 * écoutes des 30 derniers jours, tous spectateurs. Masqué tant que la
 * musique n'est pas activée ou qu'aucune écoute n'existe.
 */

interface Entry {
  name: string;
  artist?: string;
  plays: number;
}

export interface MusicStats {
  days: { date: string; plays: number; hours: number; listeners: number }[];
  plays: number;
  previous: number;
  hours: number;
  listeners: number;
  topTracks: Entry[];
  topArtists: Entry[];
}

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

function Readout({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="min-w-0 border-l border-border-subtle pl-3 first:border-l-0 first:pl-0">
      <dt className="truncate text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
        {label}
      </dt>
      <dd className="truncate text-xl font-semibold text-fg">{value}</dd>
      <dd className="truncate text-xs text-fg-muted">{detail}</dd>
    </div>
  );
}

function Top({ title, items }: { title: string; items: Entry[] }) {
  if (!items.length) return null;
  return (
    <div className="min-w-0">
      <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
        {title}
      </div>
      <ol className="space-y-0.5 text-sm">
        {items.map((e, i) => (
          <li key={`${e.name}-${e.artist ?? ""}`} className="flex min-w-0 gap-2">
            <span className="w-4 shrink-0 text-right text-fg-subtle tabular-nums">{i + 1}</span>
            <span className="min-w-0 flex-1 truncate">
              {e.name}
              {e.artist ? <span className="text-fg-muted"> · {e.artist}</span> : null}
            </span>
            <span className="shrink-0 text-fg-muted tabular-nums">{e.plays}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

export function MusicActivity({ className }: { className?: string }) {
  const state = useAsync(() => apiFetch<MusicStats>("/pse/music/stats?days=30"), []);
  const [metric, setMetric] = useState<"plays" | "hours">("plays");
  const s = state.data;
  if (!s || s.plays + s.previous === 0) return null;

  const change = s.previous > 0 ? (s.plays - s.previous) / s.previous : null;
  const data = s.days.map((d) => ({
    key: d.date,
    label: new Date(`${d.date}T12:00:00`).toLocaleDateString(getLocale(), {
      month: "short",
      day: "numeric",
    }),
    value: metric === "plays" ? d.plays : d.hours,
    detail: L(
      `${d.plays} écoute(s) · ${formatNumber(d.hours, 1)} h · ${d.listeners} auditeur(s)`,
      `${d.plays} play(s) · ${formatNumber(d.hours, 1)} h · ${d.listeners} listener(s)`,
    ),
  }));

  return (
    <DashboardPanel
      title={L("Musique · 30 derniers jours", "Music · last 30 days")}
      icon={<Music size={16} />}
      className={className}
      bodyClassName="flex min-h-0 flex-col gap-3"
      actions={
        <div
          role="radiogroup"
          aria-label={L("Mesure", "Metric")}
          className="inline-flex rounded-md border border-border bg-bg-subtle p-0.5"
        >
          {(["plays", "hours"] as const).map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={metric === m}
              onClick={() => setMetric(m)}
              className={`rounded px-2 py-0.5 text-xs font-medium ${metric === m ? "bg-surface text-fg shadow-sm" : "text-fg-muted hover:text-fg"}`}
            >
              {m === "plays" ? L("Écoutes", "Plays") : L("Heures", "Hours")}
            </button>
          ))}
        </div>
      }
    >
      <dl className="grid shrink-0 grid-cols-3 gap-3">
        <Readout
          label={L("Écoutes", "Plays")}
          value={formatNumber(s.plays, 0)}
          detail={
            change === null
              ? L("Tous auditeurs", "All listeners")
              : `${change >= 0 ? "+" : "−"}${Math.round(Math.abs(change) * 100)} % ${L("vs. avant", "vs. prior")}`
          }
        />
        <Readout
          label={L("Heures écoutées", "Hours listened")}
          value={formatNumber(s.hours, 0)}
          detail={L("30 derniers jours", "Last 30 days")}
        />
        <Readout
          label={L("Auditeurs", "Listeners")}
          value={formatNumber(s.listeners, 0)}
          detail={L("Au moins une écoute", "At least one play")}
        />
      </dl>
      <TimeBars
        data={data}
        height={120}
        ariaLabel={L("Écoutes de musique par jour", "Music plays per day")}
        valueFormat={(v) => formatNumber(v, metric === "plays" ? 0 : 1)}
      />
      <div className="grid gap-3 sm:grid-cols-2">
        <Top title={L("Titres", "Tracks")} items={s.topTracks} />
        <Top title={L("Artistes", "Artists")} items={s.topArtists} />
      </div>
    </DashboardPanel>
  );
}
