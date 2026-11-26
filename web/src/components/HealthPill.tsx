import { Drawer, cn } from "@braedonsaunders/appkit-ui";
import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useApp } from "../app-context";
import { t, tn } from "../i18n";
import {
  countsOverall,
  hasExemptDiagnostic,
  type HealthItem,
  type HealthLevel,
} from "../lib/health";
import { Meter } from "./charts";
import { LEVEL_LABEL, LevelBadge, StatusDot } from "./status";

/** The one-line answer to "is everything working?", and how loud to say it. */
export interface HealthVerdict {
  level: HealthLevel;
  label: string;
  /** A sentence for the detail drawer when the label alone is not enough. */
  hint?: string;
}

/** Things that are failing or degraded now, as opposed to not set up yet. */
export function healthProblems(items: HealthItem[]): HealthItem[] {
  return items.filter((h) => !h.setupGap && (h.level === "error" || h.level === "warn"));
}

/**
 * One verdict for a set of health items. The console header and the Overview
 * both show it, so they can never disagree.
 */
export function healthVerdict(
  items: HealthItem[],
  state: { settingUp?: boolean; failed?: boolean; pending?: boolean } = {},
): HealthVerdict {
  // Before setup finishes, unconfigured sources and an unclaimed server are
  // expected, not problems: show progress instead of a red verdict.
  if (state.settingUp) {
    return {
      level: "unknown",
      label: t("Setting up"),
      hint: t("Finish setup to bring everything online. Items below fill in as you go."),
    };
  }
  if (state.failed) {
    return {
      level: "error",
      label: LEVEL_LABEL.error,
      hint: t("Status is unavailable right now."),
    };
  }
  if (state.pending) return { level: "unknown", label: t("Checking…") };
  const problems = healthProblems(items);
  if (problems.length) {
    return {
      level: problems.some((h) => h.level === "error") ? "error" : "warn",
      label: tn(problems.length, "{count} to fix", "{count} to fix"),
    };
  }
  if (items.some((h) => h.level === "unknown" && countsOverall(h))) {
    return { level: "unknown", label: t("Status incomplete") };
  }
  const setupGaps = items.filter((h) => h.setupGap);
  if (setupGaps.length) {
    return {
      level: "unknown",
      label: tn(setupGaps.length, "{count} thing to set up", "{count} things to set up"),
    };
  }
  // "Operational" means no participating failure. It does not mean TLS was verified.
  return { level: "ok", label: hasExemptDiagnostic(items) ? t("Operational") : t("All healthy") };
}

/** The verdict for this server, from the dashboard every page already holds. */
export function useServerVerdict(): HealthVerdict {
  const { health, dashboard, dashboardError, boot } = useApp();
  return healthVerdict(health, {
    settingUp: !boot.setupComplete,
    failed: Boolean(dashboardError),
    pending: !dashboard,
  });
}

/**
 * The health verdict as a pill. Pressing it opens the full health detail in
 * a drawer, wherever the pill sits.
 */
export function HealthPill({
  items,
  verdict,
  label,
  className,
  onNavigate,
}: {
  items: HealthItem[];
  verdict: HealthVerdict;
  /** Names the pill where nothing around it says what it reports on. */
  label?: (status: string) => string;
  className?: string;
  /** Runs when a health item is followed to its page. */
  onNavigate?: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
        aria-label={label?.(verdict.label)}
        className={cn(
          "inline-flex shrink-0 cursor-pointer items-center rounded-full whitespace-nowrap transition hover:brightness-110 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
          className,
        )}
      >
        <LevelBadge level={verdict.level} label={verdict.label} />
      </button>
      <HealthDrawer
        open={open}
        onClose={() => setOpen(false)}
        items={items}
        verdict={verdict}
        onNavigate={onNavigate}
      />
    </>
  );
}

/** Every health item in full: problems first, then what is left to set up. */
export function HealthDrawer({
  open,
  onClose,
  items,
  verdict,
  onNavigate,
}: {
  open: boolean;
  onClose: () => void;
  items: HealthItem[];
  verdict: HealthVerdict;
  onNavigate?: () => void;
}) {
  const problems = healthProblems(items);
  const setupGaps = items.filter((h) => h.setupGap);
  const rest = items.filter((h) => !problems.includes(h) && !h.setupGap);
  const follow = () => {
    onClose();
    onNavigate?.();
  };
  const group = (title: string | null, rows: HealthItem[], fix = false) =>
    rows.length ? (
      <section role={title ? "region" : undefined} aria-label={title ?? undefined}>
        {title ? (
          <h4 className="mb-1.5 text-xs font-semibold uppercase tracking-wider text-fg-subtle">
            {title}
          </h4>
        ) : null}
        <ul className="divide-y divide-border-subtle rounded-lg border border-border">
          {rows.map((h) => (
            <HealthDetail key={h.id} h={h} fix={fix} onFollow={follow} />
          ))}
        </ul>
      </section>
    ) : null;
  return (
    <Drawer open={open} onClose={onClose} title={t("Server health")} size="md" disableFullscreen>
      <div className="space-y-5">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <LevelBadge level={verdict.level} label={verdict.label} />
          {verdict.hint ? <p className="text-sm text-fg-muted">{verdict.hint}</p> : null}
        </div>
        {group(t("Needs attention"), problems, true)}
        {group(t("To set up"), setupGaps)}
        {group(problems.length || setupGaps.length ? t("Everything else") : null, rest)}
      </div>
    </Drawer>
  );
}

function HealthDetail({ h, fix, onFollow }: { h: HealthItem; fix: boolean; onFollow: () => void }) {
  // Some items repeat their summary or note as a detail line; say it once.
  const details = h.details.filter((line) => line !== h.summary && line !== h.note);
  return (
    <li>
      <Link
        to={h.href}
        onClick={onFollow}
        className="group flex items-start gap-2.5 px-3 py-2.5 transition-colors hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50"
      >
        <StatusDot level={h.level} pulse={fix} className="mt-1.5" />
        <span className="min-w-0 flex-1">
          <span className="flex items-center justify-between gap-2 text-sm font-medium text-fg">
            {h.label}
            <LevelBadge level={h.level} label={h.statusLabel} />
          </span>
          <span className="block text-pretty text-sm text-fg-muted">{h.summary}</span>
          {h.fill !== undefined ? (
            <span className="mt-1.5 block max-w-xs">
              <Meter
                value={h.fill}
                tone={h.fill > 0.95 && h.id !== "cache" ? "warning" : "primary"}
              />
            </span>
          ) : null}
          {details.length ? (
            <span className="mt-1.5 block space-y-0.5">
              {details.map((line, i) => (
                <span key={i} className="block text-pretty text-xs text-fg-muted">
                  {line}
                </span>
              ))}
            </span>
          ) : null}
          {h.note ? <span className="mt-1.5 block text-xs text-fg-subtle">{h.note}</span> : null}
        </span>
        <ChevronRight
          size={16}
          aria-hidden
          className="mt-0.5 shrink-0 text-fg-subtle transition-colors group-hover:text-fg"
        />
      </Link>
    </li>
  );
}
