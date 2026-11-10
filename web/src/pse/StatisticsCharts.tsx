import { getLocale } from "../i18n";

/** A subset and its complement, never overlapping catalogue metrics. */
export function CoverageRing({
  value,
  total,
  color,
  label,
}: {
  value: number;
  total: number;
  color: string;
  label: string;
}) {
  const safeTotal = Math.max(0, total);
  const searched = Math.max(0, Math.min(safeTotal, value));
  const ratio = safeTotal ? searched / safeTotal : 0;
  const fr = getLocale().startsWith("fr");
  return (
    <div className="flex flex-wrap items-center gap-5">
      <div className="relative h-32 w-32 shrink-0">
        <svg
          viewBox="0 0 120 120"
          className="h-full w-full -rotate-90"
          role="img"
          aria-label={`${label} : ${searched.toLocaleString(getLocale())} / ${safeTotal.toLocaleString(getLocale())}`}
        >
          <circle
            cx="60"
            cy="60"
            r="49"
            fill="none"
            stroke="var(--color-border)"
            strokeWidth="9"
            opacity="0.5"
          />
          {ratio > 0 ? (
            <circle
              cx="60"
              cy="60"
              r="49"
              fill="none"
              stroke={color}
              strokeWidth="9"
              strokeLinecap={ratio === 1 ? "butt" : "round"}
              pathLength="100"
              strokeDasharray={`${ratio * 100} 100`}
              className="remix-stat-ring"
            />
          ) : null}
        </svg>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
          <strong className="text-2xl font-semibold tabular-nums">
            {safeTotal ? `${Math.round(ratio * 100)} %` : "—"}
          </strong>
          <span className="text-[10px] uppercase tracking-widest text-fg-muted">
            {fr ? "cherchés" : "searched"}
          </span>
        </div>
      </div>
      <div className="min-w-0 flex-1 space-y-3 text-sm">
        <p className="font-medium">{label}</p>
        <p className="flex items-center justify-between gap-2">
          <span className="flex items-center gap-2 text-fg-muted">
            <i className="h-2 w-2 rounded-full" style={{ background: color }} />
            {fr ? "Cherchés" : "Searched"}
          </span>
          <strong className="tabular-nums">{searched.toLocaleString(getLocale())}</strong>
        </p>
        <p className="flex items-center justify-between gap-2">
          <span className="text-fg-muted">{fr ? "À explorer" : "To explore"}</span>
          <span className="tabular-nums">{(safeTotal - searched).toLocaleString(getLocale())}</span>
        </p>
      </div>
    </div>
  );
}
