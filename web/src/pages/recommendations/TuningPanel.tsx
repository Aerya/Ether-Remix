import { ConfigSection } from "../../components/ConfigSection";
import { PERSONAL_TOUCHES } from "../settings/schema";
import { Badge, Button, cn } from "@braedonsaunders/appkit-ui";
import { Brain, Compass, Layers3, RotateCcw, Sparkles } from "lucide-react";
import { useEffect, useState } from "react";
import { DivergingBars } from "../../components/charts";
import { api } from "../../lib/api";
import type { RankerInsight } from "../../lib/contract";
import { getLocale, localized, t } from "../../i18n";
import { formatNumber, relativeTime } from "../../lib/format";
import type { PageTuning, TuningDial } from "../../lib/pages";

/** The engine's middle setting for every dial. */
const DIAL_DEFAULT = 0.5;

const DIALS = localized<
  {
    dial: TuningDial;
    name: string;
    icon: React.ReactNode;
    low: string;
    high: string;
    words: [string, string, string, string, string];
    hint: string;
  }[]
>(() => [
  {
    dial: "discovery",
    name: t("Discovery"),
    icon: <Compass size={16} />,
    low: t("Proven favourites"),
    high: t("New territory"),
    words: [t("Safe bets"), t("Mostly familiar"), t("Balanced"), t("Adventurous"), t("Explorer")],
    hint: t("How far rows reach beyond what the viewer already loves."),
  },
  {
    dial: "freshness",
    name: t("Freshness"),
    icon: <Sparkles size={16} />,
    low: t("Timeless"),
    high: t("New & trending"),
    words: [
      t("Evergreen"),
      t("Classic-leaning"),
      t("Balanced"),
      t("Of the moment"),
      t("Hot off the press"),
    ],
    hint: t("Whether picks lean on all-time greats or what's new and buzzing."),
  },
  {
    dial: "variety",
    name: t("Variety"),
    icon: <Layers3 size={16} />,
    low: t("Focused"),
    high: t("Varied"),
    words: [
      t("Laser-focused"),
      t("Focused"),
      t("Balanced"),
      t("Varied"),
      t("Something for every mood"),
    ],
    hint: t(
      "How tightly rows stick to the strongest tastes versus spreading across genres, eras and moods.",
    ),
  },
]);

const word = (words: string[], v: number) =>
  words[Math.min(words.length - 1, Math.floor(v * words.length))] ?? "";

/**
 * Tuning (Personalized pages): the page's three dials, the server-wide
 * personal touches and what the learned model has picked up. With a viewer
 * picked in Editing for, the dials are "changes just for them".
 */
export function TuningPanel({
  tuning,
  inherited,
  onChange,
  pageName,
  viewerName,
  dials,
}: {
  tuning: PageTuning | undefined;
  /** Everyone's dials, when editing for a viewer. */
  inherited?: PageTuning;
  onChange: (t: PageTuning | undefined) => void;
  pageName: string;
  viewerName: string | null;
  dials: boolean;
}) {
  const page = pageName;
  const valueOf = (dial: TuningDial): { v: number; from: "override" | "global" | "default" } => {
    const own = tuning?.[dial];
    if (own !== undefined) return { v: own, from: viewerName ? "override" : "global" };
    const base = inherited?.[dial];
    if (base !== undefined) return { v: base, from: "global" };
    return { v: DIAL_DEFAULT, from: "default" };
  };
  const set = (dial: TuningDial, v: number | undefined) => {
    const next: PageTuning = { ...tuning, [dial]: v };
    if (v === undefined) delete next[dial];
    onChange(Object.keys(next).length ? next : undefined);
  };
  const layer = viewerName ? 1 : "global";
  return (
    <div className="space-y-5">
      {dials ? (
        <section aria-label={t("How {page} picks", { page })} className="space-y-3">
          <div className="flex items-end justify-between gap-2">
            <div>
              <h2 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">
                {t("How {page} picks", { page })}
              </h2>
              <p className="mt-0.5 text-xs text-fg-muted">
                {viewerName
                  ? t("Dials for {viewer}'s {page}; unset dials follow Everyone.", {
                      viewer: viewerName,
                      page,
                    })
                  : t("How Ether chooses the rows it adds and the titles in them.")}
              </p>
            </div>
            {tuning && Object.keys(tuning).length ? (
              <Button
                variant="ghost"
                size="sm"
                className="h-7 px-2 text-xs"
                onClick={() => onChange(undefined)}
              >
                <RotateCcw size={12} /> {t("Reset dials")}
              </Button>
            ) : null}
          </div>
          {DIALS.map((d) => (
            <Dial
              key={d.dial}
              spec={d}
              value={valueOf(d.dial)}
              layer={layer}
              onChange={(v) => set(d.dial, v)}
            />
          ))}
        </section>
      ) : null}
      {viewerName ? null : (
        <ConfigSection
          configKey="personalize"
          groups={PERSONAL_TOUCHES}
          label={t("Personal touches")}
        />
      )}
      <LearningCard />
    </div>
  );
}

function Dial({
  spec,
  value,
  layer,
  onChange,
}: {
  spec: (typeof DIALS)[number];
  value: { v: number; from: "override" | "global" | "default" };
  layer: number | "global";
  onChange: (v: number | undefined) => void;
}) {
  const pct = Math.round(value.v * 100);
  const own = value.from === (layer === "global" ? "global" : "override");
  return (
    <div
      className={cn(
        "rounded-xl border bg-surface p-3",
        value.from === "override" ? "border-info/40" : "border-border",
      )}
    >
      <div className="flex items-center gap-2">
        <span className="grid size-7 place-items-center rounded-lg bg-primary-subtle text-primary">
          {spec.icon}
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-fg">{spec.name}</p>
          <p className="truncate text-xs text-fg-muted" title={spec.hint}>
            {word(spec.words, value.v)}
          </p>
        </div>
        {value.from === "default" ? (
          <span className="text-[11px] text-fg-subtle">{t("Default")}</span>
        ) : value.from === "global" && layer !== "global" ? (
          <Badge variant="secondary">{t("From Everyone")}</Badge>
        ) : value.from === "override" ? (
          <Badge variant="info">{t("Just this viewer")}</Badge>
        ) : null}
        {own ? (
          <button
            type="button"
            aria-label={t("Reset {name}", { name: spec.name })}
            title={t("Reset")}
            onClick={() => onChange(undefined)}
            className="text-fg-subtle hover:text-fg"
          >
            <RotateCcw size={13} />
          </button>
        ) : null}
      </div>
      <div className="relative mt-3">
        <div className="pointer-events-none absolute inset-x-0 top-1/2 h-1.5 -translate-y-1/2 rounded-full bg-gradient-to-r from-primary/15 via-primary/40 to-primary" />
        <div
          className="pointer-events-none absolute top-1/2 h-3 w-px -translate-y-1/2 bg-fg-subtle/60"
          style={{ left: `${DIAL_DEFAULT * 100}%` }}
          aria-hidden
        />
        <input
          type="range"
          min={0}
          max={100}
          step={5}
          value={pct}
          aria-label={spec.name}
          aria-valuetext={word(spec.words, value.v)}
          onChange={(e) => onChange(Number(e.currentTarget.value) / 100)}
          className="relative z-10 h-5 w-full cursor-pointer appearance-none bg-transparent [&::-moz-range-thumb]:size-4 [&::-moz-range-thumb]:rounded-full [&::-moz-range-thumb]:border-2 [&::-moz-range-thumb]:border-primary [&::-moz-range-thumb]:bg-surface [&::-webkit-slider-thumb]:size-4 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-primary [&::-webkit-slider-thumb]:bg-surface [&::-webkit-slider-thumb]:shadow"
        />
      </div>
      <div className="mt-1 flex justify-between text-[11px] text-fg-subtle">
        <span>{spec.low}</span>
        <span>{spec.high}</span>
      </div>
    </div>
  );
}

/** What the learned ranker has picked up from real viewing. */
function LearningCard() {
  const [r, setR] = useState<RankerInsight | null | "none">(null);
  useEffect(() => {
    let live = true;
    api
      .rankerInsight()
      .then((x) => live && setR(x))
      .catch(() => live && setR("none"));
    return () => {
      live = false;
    };
  }, []);
  if (r === "none") return null;
  const lift =
    r && r.auc > 0 && r.baselineAuc > 0.5 ? (r.auc - 0.5) / (r.baselineAuc - 0.5) - 1 : null;
  const features = (r && r.features) ?? [];
  const maxW = Math.max(0.0001, ...features.map((f) => Math.abs(f.weight)));
  return (
    <section
      aria-label={t("How it learns")}
      className="relative overflow-hidden rounded-xl border border-border bg-surface p-3"
    >
      <div
        className="pointer-events-none absolute -right-10 -top-12 size-36 rounded-full bg-primary/10 blur-2xl"
        aria-hidden
      />
      <div className="relative flex items-center gap-2">
        <span className="grid size-7 place-items-center rounded-lg bg-primary-subtle text-primary">
          <Brain size={16} />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-fg">{t("How it learns")}</p>
          <p className="text-xs text-fg-muted">
            {r === null
              ? t("Checking the model…")
              : r.active
                ? r.trainedAt
                  ? t("Ranking from what viewers actually play · trained {when}", {
                      when: relativeTime(r.trainedAt),
                    })
                  : t("Ranking from what viewers actually play")
                : t(
                    "Watching what viewers play; it switches on once it beats the hand-tuned ranking.",
                  )}
          </p>
        </div>
        {r && r !== null ? (
          <Badge variant={r.active ? "success" : "secondary"}>
            {r.active ? t("Active") : t("Learning")}
          </Badge>
        ) : null}
      </div>
      {r ? (
        <div className="relative mt-3 space-y-3">
          <div className="grid grid-cols-3 gap-2 text-center">
            <Stat label={t("Seen")} value={formatNumber(r.examples)} />
            <Stat label={t("Played")} value={formatNumber(r.positives)} />
            <Stat
              label={t("vs hand-tuned")}
              value={
                lift === null
                  ? "—"
                  : new Intl.NumberFormat(getLocale(), {
                      style: "percent",
                      signDisplay: "always",
                      maximumFractionDigits: 0,
                    }).format(Math.round(lift * 100) / 100 + 0)
              }
              tone={lift === null ? undefined : lift >= 0 ? "good" : "bad"}
            />
          </div>
          {features.length ? (
            <div className="space-y-1.5">
              <p className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
                {t("What drives plays")}
              </p>
              <DivergingBars
                ariaLabel={t("What drives plays")}
                data={features.slice(0, 6).map((f) => ({
                  key: f.name,
                  label: f.label || f.name,
                  weight: f.weight / maxW,
                }))}
              />
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: "good" | "bad" }) {
  return (
    <div className="rounded-lg bg-bg-subtle px-2 py-1.5">
      <p
        className={cn(
          "text-base font-semibold tabular-nums",
          tone === "good" ? "text-success" : tone === "bad" ? "text-danger" : "text-fg",
        )}
      >
        {value}
      </p>
      <p className="text-[10.5px] uppercase tracking-wider text-fg-subtle">{label}</p>
    </div>
  );
}
