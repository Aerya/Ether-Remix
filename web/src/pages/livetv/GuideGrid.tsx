import { programmeArtwork } from "./model";
import { Badge, Button, Checkbox, Spinner, cn } from "@braedonsaunders/appkit-ui";
import {
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  Link2,
  Maximize2,
  Minimize2,
  Play,
  Star,
  Tv,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Segmented } from "../../components/fields";
import { ToolbarPicker, type ToolbarOption } from "../../components/ToolbarPicker";
import { Artwork } from "../../components/Artwork";
import { getLocale, t, tn } from "../../i18n";
import { unit } from "../../lib/format";
import { useMediaQuery } from "../../lib/hooks";
import type { ChannelSelection } from "./ChannelBulkBar";
import { ChannelLogo } from "./ChannelLogo";
import {
  MINUTE,
  clock,
  dayClock,
  hueOf,
  onNow,
  progress,
  type Channel,
  type Programme,
} from "./model";

const ROW = 60;
const HEADER = 36;
const CH_COL = 208;
const CH_COL_PHONE = 100;
/** The Live TV page's lineup column: a third of the guide, never cramped. */
const LINEUP_MIN = 260;
const ZOOM = { wide: 5, normal: 3.5, tight: 2.2 } as const;
export type Zoom = keyof typeof ZOOM;
/** Visible span each zoom aims for, as shown on the zoom control. */
export const GUIDE_ZOOM_HOURS: Record<Zoom, number> = { tight: 6, normal: 4, wide: 3 };
const GUIDE_ZOOM_KEY = "ether:guide-zoom";
/** The stored zoom when valid; otherwise the 3h default ("wide"). */
export function initialGuideZoom(stored: string | null): Zoom {
  return stored === "tight" || stored === "normal" || stored === "wide" ? stored : "wide";
}
function storedGuideZoom(): Zoom {
  try {
    return initialGuideZoom(
      typeof localStorage !== "undefined" ? localStorage.getItem(GUIDE_ZOOM_KEY) : null,
    );
  } catch {
    return "wide";
  }
}
const HOUR = 60 * MINUTE;
/** The furthest ahead the day picker reaches, even for virtual channels. */
const MAX_DAYS = 7;
/** How much of the past a jump leaves visible before its target. */
const LEAD = 30 * MINUTE;
const STEP = 30 * MINUTE;

/**
 * Snap the initial/Now scroll target to a 30-minute ruler boundary so the
 * left-most time label and slot start sit cleanly beside the sticky channel
 * column, advancing one half-hour on narrow viewports so the live marker stays
 * on screen.
 */
export function guideNowScrollLeft(
  now: number,
  start: number,
  ppm: number,
  timelineWidth: number,
): number {
  const toX = (ms: number) => ((ms - start) / MINUTE) * ppm;
  let slot = Math.max(start, Math.floor((now - LEAD) / STEP) * STEP);
  const currentSlot = Math.max(start, Math.floor(now / STEP) * STEP);
  if (timelineWidth > 0 && toX(now) - toX(slot) > timelineWidth - 56 && slot < currentSlot) {
    slot = currentSlot;
  }
  return Math.max(0, toX(slot));
}

/** Local midnight `days` after the day containing `ms` (DST-safe). */
function midnight(ms: number, days = 0): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() + days);
  return d.getTime();
}

/** Calendar days from the day containing `now` to the day containing `ms`. */
export function guideDayOffset(ms: number, now: number): number {
  return Math.round((midnight(ms) - midnight(now)) / (24 * HOUR));
}

/** Days the picker offers: today through the last day with listings. */
export function guideDayCount(now: number, guideUntil?: number): number {
  if (guideUntil === undefined) return MAX_DAYS;
  if (guideUntil <= now) return 1;
  return Math.min(MAX_DAYS, Math.max(1, guideDayOffset(guideUntil - 1, now) + 1));
}

/**
 * `hour`:00 on day `offset` (never before today), or the day after when that
 * hour has already finished airing.
 */
export function guideHourTarget(now: number, offset: number, hour: number): number {
  const at = (days: number) => {
    const d = new Date(midnight(now, days));
    d.setHours(hour);
    return d.getTime();
  };
  const day = Math.max(0, offset);
  const target = at(day);
  return target + HOUR <= now ? at(day + 1) : target;
}

const JUMPS = [
  { value: "prime", hour: 20, label: () => t("Prime time") },
  { value: "late", hour: 23, label: () => t("Late night") },
] as const;

/**
 * The classic EPG grid, done properly: channels down the left, time across
 * the top, the live line running through it. Rows are virtualised so an
 * IPTV lineup of thousands still scrolls smoothly.
 */
export function GuideGrid({
  channels,
  guide,
  from,
  until,
  now,
  selected,
  onSelect,
  onShift,
  onNow: jumpNow,
  onRange,
  loading,
  head,
  covered,
  onToggleFavorite,
  onInspect,
  onPlay,
  onMapGuide,
  guideUntil,
  emptyHint,
  selection,
  bulkActions,
  toolbarStart,
  toolbarFilters,
}: {
  /** Sparse by design: only rows near the viewport have been fetched. */
  channels: (Channel | undefined)[];
  guide: Map<string, Programme[]>;
  from: Date;
  until: Date;
  now: number;
  selected: string | null;
  onSelect: (id: string) => void;
  onShift: (hours: number) => void;
  onNow: () => void;
  onRange?: (first: number, last: number) => void;
  loading: boolean;
  /**
   * The lineup view: the channel column becomes the page's lineup (a third
   * of the width, with favourites, what's on and guide gaps) and head sits
   * above it, beside the guide's toolbar. One scroller keeps every channel
   * row level with its listings.
   */
  head?: ReactNode;
  covered?: (c: Channel) => boolean | undefined;
  onToggleFavorite?: (c: Channel) => void;
  /** Opens a programme's details; without it a programme selects its channel. */
  onInspect?: (c: Channel, p: Programme) => void;
  /** Plays a channel in the preview player. */
  onPlay?: (c: Channel) => void;
  /** Opens guide matching for an IPTV channel without listings. */
  onMapGuide?: (c: Channel) => void;
  /** When listings run out (ms); undefined when some channel never runs out. */
  guideUntil?: number;
  /** Shown instead of the default when there are no rows. */
  emptyHint?: ReactNode;
  selection?: ChannelSelection;
  bulkActions?: ReactNode;
  /** Extra controls at the start of the toolbar row (not in the lineup view). */
  toolbarStart?: ReactNode;
  /** Filters shown before the time controls in the full-width toolbar. */
  toolbarFilters?: ReactNode;
}) {
  const lineup = head !== undefined;
  const compactMedia = useMediaQuery("(max-width: 639px)");
  const loadedChannels = useMemo(
    () => channels.filter((c): c is Channel => c !== undefined),
    [channels],
  );
  const [zoom, setZoomState] = useState<Zoom>(storedGuideZoom);
  const setZoom = useCallback((z: Zoom) => {
    setZoomState(z);
    try {
      localStorage.setItem(GUIDE_ZOOM_KEY, z);
    } catch {
      // Private mode: keep the choice for this visit only.
    }
  }, []);
  const ppm = ZOOM[zoom];
  const start = from.getTime();
  const minutes = (until.getTime() - start) / MINUTE;
  const width = minutes * ppm;
  const scroller = useRef<HTMLDivElement>(null);
  const [view, setView] = useState({ top: 0, height: 800, left: 0, width: 1200 });
  const compactChannelCol = !lineup && (compactMedia || (view.width > 0 && view.width < 640));
  const chCol = lineup
    ? Math.max(LINEUP_MIN, Math.round(view.width / 3))
    : compactChannelCol
      ? CH_COL_PHONE
      : CH_COL;
  const timelineWidth = Math.max(0, view.width - chCol);
  const x = (ms: number) => ((ms - start) / MINUTE) * ppm;
  const nowX = x(now);
  const nowVisible = now >= start && now <= until.getTime();

  const scrollTo = useCallback((left: number, smooth = false) => {
    const el = scroller.current;
    const target = Math.max(0, left);
    if (!el) return;
    if (typeof el.scrollTo === "function")
      el.scrollTo({ left: target, behavior: smooth ? "smooth" : "auto" });
    else el.scrollLeft = target;
    if (!smooth) {
      setView((prev) => (prev.left === target ? prev : { ...prev, left: target }));
    }
  }, []);
  const scrollToNow = useCallback(
    (smooth = false) => scrollTo(guideNowScrollLeft(now, start, ppm, timelineWidth), smooth),
    [now, start, ppm, timelineWidth, scrollTo],
  );
  // A day or time jump that needs a new window lands once that window renders.
  const pendingFocus = useRef<number | null>(null);
  // The time at the listings' left edge, so zooming keeps the viewer's place.
  const leftEdge = useRef<number | null>(null);
  const lastZoom = useRef(zoom);
  const hasChannels = channels.length > 0;
  // Land on "now" when the window moves, keep the place when zooming, and
  // land on a jump's target once its window renders.
  useLayoutEffect(() => {
    if (!hasChannels) return;
    const focus = pendingFocus.current;
    const zoomed = lastZoom.current !== zoom;
    pendingFocus.current = null;
    lastZoom.current = zoom;
    if (focus !== null) scrollTo(x(focus - LEAD));
    else if (zoomed && leftEdge.current !== null) scrollTo(x(leftEdge.current));
    else scrollToNow();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [start, zoom, hasChannels]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const measure = () =>
      setView((prev) => ({
        top: el.scrollTop,
        height: el.clientHeight || prev.height,
        left: el.scrollLeft,
        width:
          el.clientWidth ||
          (typeof window !== "undefined" && window.innerWidth ? window.innerWidth : prev.width),
      }));
    measure();
    const ro = typeof ResizeObserver !== "undefined" ? new ResizeObserver(measure) : null;
    ro?.observe(el);
    return () => ro?.disconnect();
  }, []);

  const first = Math.max(0, Math.floor((view.top - HEADER) / ROW) - 6);
  const last = Math.min(channels.length, Math.ceil((view.top + view.height) / ROW) + 6);
  useEffect(() => {
    onRange?.(first, last);
  }, [first, last, onRange]);

  const ticks = useMemo(() => {
    const out: number[] = [];
    for (let tick = Math.ceil(start / STEP) * STEP; tick <= until.getTime(); tick += STEP)
      out.push(tick);
    return out;
  }, [start, until]);

  const dayLabel = new Date(Math.max(start, now)).toLocaleDateString(getLocale(), {
    weekday: "long",
    month: "short",
    day: "numeric",
  });

  // The day being looked at follows the scroll position, not the window.
  const focusedDay = guideDayOffset(start + (view.left / ppm) * MINUTE + LEAD, now);
  // Whether the live now-line is on screen: "Now" only shows once away from it.
  const nowInView = x(now) >= view.left && x(now) <= view.left + timelineWidth;
  const days = guideDayCount(now, guideUntil);
  const today = midnight(now);
  // Show `focus`: scroll there when the window holds it, otherwise move the
  // window to start at `windowStart` and land on `focus` once it renders.
  const show = (focus: number, windowStart: number) => {
    const hours = Math.round((windowStart - start) / HOUR);
    if ((focus >= start && focus <= until.getTime() - 3 * HOUR) || hours === 0) {
      scrollTo(x(focus - LEAD), true);
      return;
    }
    pendingFocus.current = focus;
    onShift(hours);
  };
  const pickDay = (offset: number) => {
    if (offset === 0) {
      jumpNow();
      scrollToNow(true);
      return;
    }
    // The same time of day on that day, with the whole day in the window.
    const at = new Date(now);
    at.setDate(at.getDate() + offset);
    show(at.getTime(), midnight(now, offset));
  };
  const jump = (hour: number) => {
    const target = guideHourTarget(now, focusedDay, hour);
    show(target, target - 2 * HOUR);
  };
  const locale = getLocale();
  const jumpTargets = JUMPS.map((j) => guideHourTarget(now, focusedDay, j.hour));
  const jumpKey = jumpTargets.join(",");
  const dayMenu = useMemo(() => {
    const date = new Intl.DateTimeFormat(locale, {
      weekday: "short",
      month: "short",
      day: "numeric",
    });
    const label = (offset: number) =>
      offset === 0
        ? t("Today")
        : offset === 1
          ? t("Tomorrow")
          : offset === -1
            ? t("Yesterday")
            : date.format(midnight(today, offset));
    const options: ToolbarOption<string>[] = Array.from({ length: days }, (_, i) => ({
      value: `day:${i}`,
      label: label(i),
      group: t("Day"),
    }));
    JUMPS.forEach((j, i) => {
      const target = jumpTargets[i]!;
      if (guideUntil !== undefined && target >= guideUntil) return;
      options.push({
        value: `jump:${j.value}`,
        label: j.label(),
        hint:
          guideDayOffset(target, today) === Math.max(0, focusedDay)
            ? clock(new Date(target))
            : dayClock(new Date(target)),
        group: t("Go to"),
      });
    });
    return { options, label };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [locale, today, days, focusedDay, guideUntil, jumpKey]);

  // Full screen shows only the guide: the browser's Fullscreen API on this
  // element, or a window-filling overlay where that API is missing.
  const root = useRef<HTMLDivElement>(null);
  const [nativeFull, setNativeFull] = useState(false);
  const [pseudoFull, setPseudoFull] = useState(false);
  const full = nativeFull || pseudoFull;
  useEffect(() => {
    const sync = () => setNativeFull(document.fullscreenElement === root.current);
    document.addEventListener("fullscreenchange", sync);
    return () => document.removeEventListener("fullscreenchange", sync);
  }, []);
  useEffect(() => {
    if (!pseudoFull) return;
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setPseudoFull(false);
    window.addEventListener("keydown", esc);
    return () => window.removeEventListener("keydown", esc);
  }, [pseudoFull]);
  const toggleFull = async () => {
    if (pseudoFull) return setPseudoFull(false);
    if (document.fullscreenElement) return void (await document.exitFullscreen());
    if (root.current?.requestFullscreen) {
      try {
        await root.current.requestFullscreen();
        return;
      } catch {
        // Refused (e.g. an iframe without allowfullscreen): fill the window.
      }
    }
    setPseudoFull(true);
  };

  return (
    <div
      ref={root}
      className={cn(
        "relative flex h-full min-h-[28rem] flex-col bg-bg",
        // Without the Fullscreen API (iOS Safari), fill the window instead.
        pseudoFull && "fixed inset-0 z-[100] h-dvh",
      )}
    >
      <div
        className={cn(
          "flex shrink-0",
          lineup && channels.length === 0 && "max-sm:flex-col",
          lineup && "border-b border-border",
        )}
      >
        {lineup ? (
          <div
            className="shrink-0 border-r border-border bg-bg-subtle"
            style={{ width: channels.length === 0 && view.width < 640 ? "100%" : chCol }}
          >
            {head}
          </div>
        ) : null}
        <div className={cn("flex min-w-0 flex-1", lineup && "h-[104px]")}>
          {lineup ? (
            <OnNowStrip channels={loadedChannels} guide={guide} now={now} onSelect={onSelect} />
          ) : null}
          {lineup ? (
            <div
              className={cn(
                "flex min-w-0 flex-wrap items-center gap-1 bg-surface px-2 py-1.5",
                channels.length === 0 && !toolbarStart && "max-sm:hidden",
                lineup
                  ? "ml-auto w-44 shrink-0 content-center justify-center border-l border-border"
                  : "flex-1 border-b border-border sm:px-4",
              )}
            >
              {!lineup ? toolbarStart : null}
              <div
                className={cn(
                  "flex min-w-0 items-center gap-2",
                  lineup ? "w-full justify-center" : "mr-auto",
                )}
              >
                <h2
                  className={cn(
                    "text-sm font-semibold text-fg",
                    (lineup || toolbarStart) && "sr-only",
                  )}
                >
                  {t("Guide")}
                </h2>
                <span className={cn("text-xs text-fg-muted", lineup && "sr-only")}>
                  {tn(channels.length, "{day} · {count} channel", "{day} · {count} channels", {
                    day: dayLabel,
                  })}
                </span>
                <ToolbarPicker
                  ariaLabel={t("Day")}
                  value={`day:${focusedDay}`}
                  trigger={<span className="min-w-0 truncate">{dayMenu.label(focusedDay)}</span>}
                  options={dayMenu.options}
                  searchable={false}
                  // Sized like the heading it replaces, so the toolbar keeps its height.
                  className="h-5 max-w-[9rem] border-transparent bg-transparent px-1 text-sm font-semibold shadow-none"
                  menuClassName="w-56"
                  onChange={(v) => {
                    if (v.startsWith("day:")) return pickDay(Number(v.slice(4)));
                    const j = JUMPS.find((o) => `jump:${o.value}` === v);
                    if (j) jump(j.hour);
                  }}
                />
                {loading ? (
                  lineup ? (
                    <Spinner size={12} label={t("Updating…")} />
                  ) : (
                    <span className="text-xs text-fg-subtle">{t("Updating…")}</span>
                  )
                ) : null}
              </div>
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("Earlier")}
                  onClick={() => onShift(-3)}
                >
                  <ChevronLeft size={15} />
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    jumpNow();
                    scrollToNow(true);
                  }}
                >
                  <span className="relative mr-1 flex size-2">
                    <span className="absolute inline-flex size-full animate-ping rounded-full bg-danger/60" />
                    <span className="relative inline-flex size-2 rounded-full bg-danger" />
                  </span>
                  {t("Now")}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("Later")}
                  onClick={() => onShift(3)}
                >
                  <ChevronRight size={15} />
                </Button>
              </div>
              <Segmented
                ariaLabel={t("Zoom")}
                size="sm"
                value={zoom}
                onChange={setZoom}
                options={[
                  { value: "tight", label: unit(GUIDE_ZOOM_HOURS.tight, "hour") },
                  { value: "normal", label: unit(GUIDE_ZOOM_HOURS.normal, "hour") },
                  { value: "wide", label: unit(GUIDE_ZOOM_HOURS.wide, "hour") },
                ]}
              />
              <Button
                variant="ghost"
                size="sm"
                aria-label={full ? t("Exit full screen") : t("Full screen guide")}
                aria-pressed={full}
                onClick={() => void toggleFull()}
              >
                {full ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
              </Button>
            </div>
          ) : (
            // One bar: views on the left; filters, then one time cluster
            // (earlier · day · later, and "Now" only once away from now),
            // then zoom and full screen on the right.
            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-2 border-b border-border bg-surface px-3 py-2 sm:px-4">
              {toolbarStart}
              <div className="ml-auto flex min-w-0 flex-wrap items-center gap-2">
                {toolbarFilters}
                {toolbarFilters ? (
                  <span className="hidden h-5 w-px bg-border sm:block" aria-hidden />
                ) : null}
                {/* Every control in this bar is 32px tall (h-8), so the time
                    cluster's frame is 1px with 28px children. */}
                <div className="flex h-8 items-center gap-0.5 rounded-lg border border-border bg-bg p-px">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2"
                    aria-label={t("Earlier")}
                    onClick={() => onShift(-3)}
                  >
                    <ChevronLeft size={15} />
                  </Button>
                  <ToolbarPicker
                    ariaLabel={t("Day")}
                    value={`day:${focusedDay}`}
                    trigger={
                      <span className="flex min-w-0 items-center gap-1.5">
                        {nowInView ? (
                          <span className="relative flex size-2 shrink-0">
                            <span className="absolute inline-flex size-full animate-ping rounded-full bg-danger/60" />
                            <span className="relative inline-flex size-2 rounded-full bg-danger" />
                          </span>
                        ) : null}
                        <span className="min-w-0 truncate">{dayMenu.label(focusedDay)}</span>
                      </span>
                    }
                    options={dayMenu.options}
                    searchable={false}
                    className="h-7 max-w-[11rem] border-transparent bg-transparent px-2 text-sm font-medium shadow-none"
                    menuClassName="w-56"
                    onChange={(v) => {
                      if (v.startsWith("day:")) return pickDay(Number(v.slice(4)));
                      const j = JUMPS.find((o) => `jump:${o.value}` === v);
                      if (j) jump(j.hour);
                    }}
                  />
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2"
                    aria-label={t("Later")}
                    onClick={() => onShift(3)}
                  >
                    <ChevronRight size={15} />
                  </Button>
                  {nowInView ? null : (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2"
                      onClick={() => {
                        jumpNow();
                        scrollToNow(true);
                      }}
                    >
                      {t("Now")}
                    </Button>
                  )}
                </div>
                {loading ? <Spinner size={14} label={t("Updating…")} /> : null}
                <Segmented
                  ariaLabel={t("Zoom")}
                  size="sm"
                  className="p-px"
                  value={zoom}
                  onChange={setZoom}
                  options={[
                    { value: "wide", label: unit(GUIDE_ZOOM_HOURS.wide, "hour") },
                    { value: "normal", label: unit(GUIDE_ZOOM_HOURS.normal, "hour") },
                    { value: "tight", label: unit(GUIDE_ZOOM_HOURS.tight, "hour") },
                  ]}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  className="w-8 px-0"
                  aria-label={full ? t("Exit full screen") : t("Full screen guide")}
                  aria-pressed={full}
                  onClick={() => void toggleFull()}
                >
                  {full ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
                </Button>
              </div>
            </div>
          )}
        </div>
      </div>

      {!lineup && !full ? (
        <OnNowStrip channels={loadedChannels} guide={guide} now={now} onSelect={onSelect} compact />
      ) : null}

      <div
        ref={scroller}
        role="grid"
        aria-label={t("Programme guide")}
        aria-rowcount={channels.length}
        onScroll={(e) => {
          const { scrollTop, clientHeight, scrollLeft, clientWidth } = e.currentTarget;
          leftEdge.current = start + (scrollLeft / ppm) * MINUTE;
          setView((prev) => ({
            top: scrollTop,
            height: clientHeight || prev.height,
            left: scrollLeft,
            width: clientWidth || prev.width,
          }));
        }}
        className="app-scroll relative min-h-0 flex-1 overflow-auto bg-bg"
      >
        {channels.length > 0 ? (
          <div
            className="relative"
            style={{
              width: chCol + width,
              height: HEADER + channels.length * ROW + (selection?.ids.size ? 100 : 0),
            }}
          >
            {/* time ruler */}
            <div
              className="sticky top-0 z-30 flex border-b border-border bg-surface/95 backdrop-blur"
              style={{ height: HEADER }}
            >
              <div
                className={cn(
                  "sticky left-0 z-40 flex shrink-0 items-center border-r border-border bg-surface text-[11px] font-semibold uppercase tracking-wider text-fg-subtle",
                  compactChannelCol ? "px-2" : "px-3",
                )}
                style={{ width: chCol }}
              >
                {selection ? (
                  <Checkbox
                    className={compactChannelCol ? "mr-2" : "mr-3"}
                    checked={selection.all}
                    ref={(el) => {
                      if (el) el.indeterminate = !selection.all && selection.ids.size > 0;
                    }}
                    disabled={selection.busy || !channels.length}
                    aria-label={t("Select all matches")}
                    onChange={selection.toggleAll}
                  />
                ) : null}
                <span className="truncate">{t("Channel")}</span>
              </div>
              <div className="relative overflow-hidden" style={{ width }}>
                {ticks.map((tick) => {
                  const hour = new Date(tick).getMinutes() === 0;
                  const tickX = x(tick);
                  const clipped = tickX < view.left - 2;
                  return (
                    <div
                      key={tick}
                      className="absolute inset-y-0 flex items-center border-l border-border/70 pl-2"
                      style={{ left: tickX }}
                    >
                      {clipped ? null : (
                        <span
                          className={cn(
                            "whitespace-nowrap text-xs tabular-nums",
                            hour ? "font-semibold text-fg" : "text-fg-subtle",
                          )}
                        >
                          {clock(new Date(tick))}
                        </span>
                      )}
                    </div>
                  );
                })}
                {nowVisible && nowX >= view.left ? (
                  <div
                    className="absolute bottom-0 z-10 -translate-x-1/2 rounded-t-md bg-danger px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-white"
                    style={{ left: nowX }}
                  >
                    {t("Live · {time}", { time: clock(new Date(now)) })}
                  </div>
                ) : null}
              </div>
            </div>

            {/* half-hour gridlines */}
            <div
              className="pointer-events-none absolute bottom-0 z-0"
              style={{ left: chCol, top: HEADER, width }}
              aria-hidden
            >
              {ticks.map((tick) => (
                <div
                  key={tick}
                  className="absolute inset-y-0 border-l border-border/40"
                  style={{ left: x(tick) }}
                />
              ))}
            </div>

            {channels.slice(first, last).map((c, k) => {
              const i = first + k;
              if (!c)
                return (
                  <div
                    key={`pending-${i}`}
                    role="row"
                    aria-busy="true"
                    aria-rowindex={i + 1}
                    className="absolute left-0 flex border-b border-border/60 bg-bg-subtle/40"
                    style={{ top: HEADER + i * ROW, height: ROW, width: chCol + width }}
                  >
                    <div
                      className={cn(
                        "sticky left-0 z-20 flex shrink-0 items-center border-r border-border bg-surface",
                        compactChannelCol ? "px-2" : "px-4",
                      )}
                      style={{ width: chCol }}
                      aria-hidden
                    >
                      <div className="h-3 w-2/3 animate-pulse rounded bg-fg-subtle/15" />
                    </div>
                  </div>
                );
              const ps = guide.get(c.id) ?? [];
              const on = c.id === selected;
              const live = onNow(ps, now);
              return (
                <div
                  key={c.id}
                  role="row"
                  aria-selected={selection ? selection.ids.has(c.id) : undefined}
                  aria-rowindex={i + 1}
                  className={cn(
                    "absolute left-0 flex border-b border-border/60",
                    on ? "bg-primary-subtle/40" : i % 2 ? "bg-bg" : "bg-bg-subtle/40",
                  )}
                  style={{ top: HEADER + i * ROW, height: ROW, width: chCol + width }}
                >
                  {lineup ? (
                    <LineupCell
                      channel={c}
                      live={live}
                      hasGuide={covered?.(c)}
                      on={on}
                      now={now}
                      width={chCol}
                      onSelect={onSelect}
                      onPlay={onPlay}
                      onToggleFavorite={onToggleFavorite}
                      selection={selection}
                    />
                  ) : (
                    <button
                      type="button"
                      role="rowheader"
                      onClick={() => onSelect(c.id)}
                      className={cn(
                        "sticky left-0 z-20 flex shrink-0 items-center border-r border-border text-left transition-colors hover:bg-surface-hover",
                        compactChannelCol ? "gap-1.5 px-2" : "gap-2.5 px-3",
                        on ? "bg-primary-subtle" : "bg-surface",
                      )}
                      style={{ width: chCol }}
                      aria-label={t("Channel {number} {name}", { number: c.number, name: c.name })}
                    >
                      {compactChannelCol ? (
                        <>
                          <ChannelLogo
                            name={c.name}
                            logo={c.logo}
                            seed={c.id}
                            channelId={c.id}
                            size={24}
                          />
                          <span className="min-w-0 flex-1">
                            <span className="flex items-center gap-1">
                              <span className="font-mono text-[10px] tabular-nums text-fg-subtle">
                                {c.number}
                              </span>
                              {c.favorite ? (
                                <Star size={9} className="shrink-0 fill-warning text-warning" />
                              ) : null}
                            </span>
                            <span className="block truncate text-xs font-medium text-fg">
                              {c.name}
                            </span>
                          </span>
                        </>
                      ) : (
                        <>
                          <span className="w-7 shrink-0 text-right font-mono text-[11px] tabular-nums text-fg-subtle">
                            {c.number}
                          </span>
                          <ChannelLogo
                            name={c.name}
                            logo={c.logo}
                            seed={c.id}
                            channelId={c.id}
                            size={32}
                          />
                          <span className="min-w-0 flex-1">
                            <span className="flex items-center gap-1">
                              <span className="truncate text-[13px] font-medium text-fg">
                                {c.name}
                              </span>
                              {c.favorite ? (
                                <Star size={10} className="shrink-0 fill-warning text-warning" />
                              ) : null}
                            </span>
                            <span className="block truncate text-[11px] text-fg-subtle">
                              {live
                                ? t("{time} left", {
                                    time: unit(
                                      Math.max(
                                        1,
                                        Math.round((Date.parse(live.end) - now) / MINUTE),
                                      ),
                                      "minute",
                                    ),
                                  })
                                : c.group || (c.kind === "virtual" ? t("Virtual") : t("Live"))}
                            </span>
                          </span>
                        </>
                      )}
                    </button>
                  )}
                  <div className="relative overflow-hidden" style={{ width }}>
                    {ps.length === 0 ? (
                      <div
                        className="absolute inset-1 flex items-center rounded-md border border-dashed border-border px-3 text-xs text-fg-subtle"
                        style={{ paddingLeft: Math.max(12, view.left + 12) }}
                      >
                        {c.kind === "iptv" && onMapGuide ? (
                          <>
                            <span>{t("No listings")}</span>
                            <button
                              type="button"
                              onClick={() => onMapGuide(c)}
                              className="ml-2 inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium text-primary hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                            >
                              <Link2 size={11} aria-hidden /> {t("Map guide")}
                            </button>
                          </>
                        ) : c.kind === "iptv" ? (
                          t("No guide data — map a guide channel in Guide data")
                        ) : (
                          t("No guide data — no programming")
                        )}
                      </div>
                    ) : null}
                    {ps.map((p) => {
                      const s = Math.max(Date.parse(p.start), start);
                      const e = Math.min(Date.parse(p.end), until.getTime());
                      // The guide is virtual in both directions. Rendering a
                      // whole day's cards for every visible row blocks drawer
                      // transitions even though most cards are offscreen.
                      const overscan = 60 * ppm;
                      if (
                        e <= s ||
                        x(e) < view.left - overscan ||
                        x(s) > view.left + timelineWidth + overscan
                      )
                        return null;
                      const left = x(s);
                      const w = Math.max(2, x(e) - left);
                      const airing = live?.id === p.id;
                      const past = Date.parse(p.end) <= now;
                      // Slide the label right so it stays readable beside the
                      // sticky channel column when the programme began earlier.
                      const hiddenLeft = Math.max(0, view.left - left);
                      const visibleW = w - hiddenLeft;
                      const inset = hiddenLeft;
                      return (
                        <button
                          key={p.id}
                          type="button"
                          role="gridcell"
                          onClick={() => (onInspect ? onInspect(c, p) : onSelect(c.id))}
                          aria-label={t("{channel}: {title}, {start} to {end}", {
                            channel: c.name,
                            title: p.item.title,
                            start: clock(p.start),
                            end: clock(p.end),
                          })}
                          title={`${p.item.title}\n${clock(p.start)} – ${clock(p.end)}${p.item.summary ? `\n\n${p.item.summary}` : ""}`}
                          className={cn(
                            "group absolute inset-y-1 overflow-hidden rounded-md border px-2 py-1 text-left transition-all hover:z-10 hover:shadow-md focus-visible:z-10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                            airing
                              ? "border-primary/40 bg-surface shadow-sm"
                              : past
                                ? "border-transparent bg-surface/50 opacity-60"
                                : "border-border/70 bg-surface hover:border-border-strong",
                          )}
                          style={{ left: left + 1, width: Math.max(2, w - 2) }}
                        >
                          {airing ? (
                            <span
                              className="absolute inset-y-0 left-0 bg-primary/10"
                              style={{ width: `${progress(p, now) * 100}%` }}
                              aria-hidden
                            />
                          ) : null}
                          <span
                            className="absolute inset-y-1 left-0 w-0.5 rounded-full"
                            style={{ background: `hsl(${hueOf(c.id)} 65% 55%)` }}
                            aria-hidden
                          />
                          {visibleW >= 28 ? (
                            <span
                              className="relative flex h-full items-center gap-2"
                              style={inset ? { paddingLeft: inset } : undefined}
                            >
                              {p.item.thumb && visibleW > 220 ? (
                                <Artwork
                                  src={programmeArtwork(p.item.thumb)}
                                  alt=""
                                  title=""
                                  className="h-10 w-7 shrink-0 rounded"
                                />
                              ) : null}
                              <span className="min-w-0">
                                <span
                                  className={cn(
                                    "block truncate text-[13px] font-medium",
                                    airing ? "text-fg" : "text-fg",
                                  )}
                                >
                                  {p.item.title || t("Untitled")}
                                  {p.item.season && p.item.episode ? (
                                    <span className="ml-1.5 text-[11px] font-normal text-fg-subtle">
                                      {t("S{season} E{episode}", {
                                        season: p.item.season,
                                        episode: p.item.episode,
                                      })}
                                    </span>
                                  ) : null}
                                </span>
                                {visibleW > 70 ? (
                                  <span className="block truncate text-[11px] tabular-nums text-fg-muted">
                                    {clock(p.start)} – {clock(p.end)}
                                    {p.item.rating ? ` · ${p.item.rating}` : ""}
                                  </span>
                                ) : null}
                              </span>
                            </span>
                          ) : null}
                        </button>
                      );
                    })}
                  </div>
                </div>
              );
            })}

            {nowVisible && nowX >= view.left ? (
              // Above programme cards (z-10 on hover) but below the sticky
              // channel column (z-20), so scrolling never draws it over names.
              <div
                className="pointer-events-none absolute bottom-0 z-[15] w-0.5 bg-danger shadow-[0_0_8px] shadow-danger/60"
                style={{ left: chCol + nowX - 1, top: HEADER }}
                aria-hidden
              />
            ) : null}
          </div>
        ) : null}
        {channels.length === 0 && emptyHint ? (
          <div className="absolute inset-0 grid place-items-center px-6 py-10 text-center">
            <div className="max-w-sm">{emptyHint}</div>
          </div>
        ) : channels.length === 0 ? (
          <div className="absolute inset-0 grid place-items-center px-6 py-10 text-center">
            <div className="max-w-sm space-y-2">
              <Tv className="mx-auto text-fg-subtle" size={28} />
              <p className="text-sm font-medium text-fg">{t("No channels match")}</p>
              <p className="text-xs text-fg-muted">
                {t(
                  "Clear the search or filter on the left, create a virtual channel, or add an IPTV source.",
                )}
              </p>
            </div>
          </div>
        ) : null}
      </div>
      {bulkActions ? (
        <div className="pointer-events-none absolute inset-x-3 bottom-3 z-40 flex justify-center">
          {bulkActions}
        </div>
      ) : null}
    </div>
  );
}

/** A lineup row's channel cell: what the Live TV lineup shows for a channel. */
function LineupCell({
  channel: c,
  live,
  hasGuide,
  on,
  now,
  width,
  onSelect,
  onPlay,
  onToggleFavorite,
  selection,
}: {
  channel: Channel;
  live: Programme | undefined;
  hasGuide: boolean | undefined;
  on: boolean;
  now: number;
  width: number;
  onSelect: (id: string) => void;
  onPlay?: (c: Channel) => void;
  onToggleFavorite?: (c: Channel) => void;
  selection?: ChannelSelection;
}) {
  const checked = selection?.ids.has(c.id) ?? false;
  return (
    <div
      role="rowheader"
      className={cn(
        "group sticky left-0 z-20 flex shrink-0 items-center border-r border-border",
        on || checked ? "bg-primary-subtle" : "bg-surface",
      )}
      style={{ width }}
    >
      {selection ? (
        <div className="flex w-8 shrink-0 justify-end">
          <Checkbox
            checked={checked}
            disabled={selection.busy}
            aria-label={t("Select {channel}", { channel: c.name })}
            onChange={(e) => selection.toggle(c.id, (e.nativeEvent as MouseEvent).shiftKey)}
          />
        </div>
      ) : null}
      <button
        type="button"
        onClick={() => onSelect(c.id)}
        aria-current={on ? "true" : undefined}
        aria-label={`${c.number} ${c.name}`}
        className={cn(
          "flex h-full min-w-0 flex-1 items-center gap-2.5 px-2 text-left transition-colors hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50",
          !c.enabled && "opacity-55",
        )}
      >
        <span
          className={cn(
            "w-7 shrink-0 text-right font-mono text-xs tabular-nums",
            on ? "text-primary" : "text-fg-subtle",
          )}
        >
          {c.number}
        </span>
        <ChannelLogo name={c.name} logo={c.logo} seed={c.id} channelId={c.id} size={34} />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1">
            <span className={cn("truncate text-sm font-medium", on ? "text-primary" : "text-fg")}>
              {c.name}
            </span>
            {c.health === "unavailable" ? (
              <Badge variant="secondary">{t("Unavailable")}</Badge>
            ) : null}
            {c.favorite ? (
              <Star
                size={11}
                className="shrink-0 fill-warning text-warning"
                aria-label={t("Favourite")}
              />
            ) : null}
          </span>
          {live ? (
            <span className="block">
              <span className="block truncate text-xs text-fg-muted">{live.item.title}</span>
              <span className="mt-1 block h-0.5 overflow-hidden rounded-full bg-border">
                <span
                  className="block h-full rounded-full bg-primary/70"
                  style={{ width: `${progress(live, now) * 100}%` }}
                />
              </span>
            </span>
          ) : (
            <span className="block truncate text-xs text-fg-subtle">
              {!c.enabled
                ? t("Off air")
                : hasGuide === false
                  ? t("No guide data")
                  : c.group || (c.kind === "virtual" ? t("Virtual") : t("Live"))}
            </span>
          )}
        </span>
        {hasGuide === false && c.enabled ? (
          <AlertTriangle
            size={13}
            className="shrink-0 text-warning"
            aria-label={t("No guide data")}
          />
        ) : null}
      </button>
      {(onPlay && c.enabled) || onToggleFavorite ? (
        // Hover actions stay in the tab order (opacity, not display), so a
        // keyboard reaches them from the channel button.
        <div className="pointer-events-none absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1 opacity-0 transition-opacity group-focus-within:pointer-events-auto group-focus-within:opacity-100 group-hover:pointer-events-auto group-hover:opacity-100">
          {onPlay && c.enabled ? (
            <button
              type="button"
              onClick={() => onPlay(c)}
              aria-label={t("Play {name}", { name: c.name })}
              title={t("Play")}
              className="rounded-md bg-surface p-1 text-fg-subtle shadow-sm hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
            >
              <Play size={13} className="fill-current" aria-hidden />
            </button>
          ) : null}
          {onToggleFavorite ? (
            <button
              type="button"
              onClick={() => onToggleFavorite(c)}
              aria-label={
                c.favorite
                  ? t("Unfavourite {channel}", { channel: c.name })
                  : t("Favourite {channel}", { channel: c.name })
              }
              aria-pressed={c.favorite}
              className="rounded-md bg-surface p-1 text-fg-subtle shadow-sm hover:text-warning focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
            >
              <Star size={13} className={cn(c.favorite && "fill-warning text-warning")} />
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/** "On now" spotlight: big cards for favourites (or the first channels). */
export function OnNowStrip({
  channels,
  guide,
  now,
  onSelect,
  compact = false,
}: {
  channels: Channel[];
  guide: Map<string, Programme[]>;
  now: number;
  onSelect: (id: string) => void;
  compact?: boolean;
}) {
  const picks = useMemo(() => {
    const live = channels
      .filter((c) => c.enabled)
      .map((c) => ({ c, p: onNow(guide.get(c.id), now) }))
      .filter((x): x is { c: Channel; p: Programme } => Boolean(x.p));
    live.sort(
      (a, b) =>
        Number(b.c.favorite) - Number(a.c.favorite) ||
        Number(Boolean(b.p.item.thumb)) - Number(Boolean(a.p.item.thumb)),
    );
    const seen = new Set<string>();
    return live
      .filter(({ p }) => {
        const title = p.item.title.trim().toLowerCase();
        if (seen.has(title)) return false;
        seen.add(title);
        return true;
      })
      .slice(0, 10);
  }, [channels, guide, now]);
  if (!picks.length) {
    if (compact) return null;
    return (
      <section
        aria-label={t("On now")}
        className="relative isolate flex min-w-0 flex-1 items-center justify-center overflow-hidden bg-bg-subtle px-5 py-3"
      >
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 bg-gradient-to-br from-primary-subtle via-surface to-bg"
        />
        <div
          aria-hidden
          className="pointer-events-none absolute -right-6 -top-12 size-44 rounded-full border border-primary/15 bg-primary/5 motion-safe:animate-pulse"
        />
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 top-1/2 h-px bg-gradient-to-r from-transparent via-primary/25 to-transparent"
        />
        <div className="relative flex min-w-0 flex-col items-center gap-2 text-center sm:flex-row sm:gap-4 sm:text-left">
          <span
            className="hidden size-9 shrink-0 items-center justify-center rounded-full border border-primary/20 bg-surface/60 text-primary sm:inline-flex"
            aria-hidden
          >
            <Tv size={17} />
          </span>
          <p className="text-sm font-medium text-fg">{t("Your next channel starts here.")}</p>
        </div>
      </section>
    );
  }
  if (compact) {
    return (
      <section
        aria-label={t("On now")}
        className="flex shrink-0 items-center gap-2.5 border-b border-border bg-bg-subtle/60 px-3 py-1.5 sm:px-4"
      >
        <div className="hidden shrink-0 items-center gap-1.5 sm:flex">
          <span className="relative flex size-2">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-danger/60" />
            <span className="relative inline-flex size-2 rounded-full bg-danger" />
          </span>
          <h2 className="text-[11px] font-semibold uppercase tracking-wider text-fg-muted">
            {t("On now")}
          </h2>
        </div>
        <div className="app-scroll flex min-w-0 flex-1 gap-2 overflow-x-auto py-0.5 [scrollbar-gutter:auto]">
          {picks.map(({ c, p }) => {
            const hue = hueOf(c.id);
            const hasArt = Boolean(p.item.thumb || c.logo);
            return (
              <button
                key={c.id}
                type="button"
                onClick={() => onSelect(c.id)}
                aria-label={t("{channel} on now: {title}", {
                  channel: c.name,
                  title: p.item.title,
                })}
                className="group relative flex h-14 w-52 shrink-0 overflow-hidden rounded-lg border border-border text-left shadow-xs transition-all hover:-translate-y-0.5 hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                style={{
                  background: `linear-gradient(120deg, hsl(${hue} 60% 22%), hsl(${(hue + 50) % 360} 55% 12%))`,
                }}
              >
                {hasArt ? (
                  <Artwork
                    src={programmeArtwork(p.item.thumb) || c.logo}
                    alt=""
                    title=""
                    className="absolute inset-y-0 right-0 w-16 opacity-85 [mask-image:linear-gradient(to_left,black_35%,transparent)]"
                    imgClassName="object-cover"
                  />
                ) : null}
                <span className="relative flex min-w-0 flex-1 flex-col justify-between px-2.5 py-1.5">
                  <span className="flex min-w-0 items-center gap-1.5 pr-10">
                    <ChannelLogo
                      name={c.name}
                      logo={c.logo}
                      seed={c.id}
                      channelId={c.id}
                      size={16}
                      className="rounded"
                    />
                    <span className="truncate text-[10px] font-medium text-white/80">
                      {c.number} · {c.name}
                    </span>
                  </span>
                  <span className="flex min-w-0 items-baseline justify-between gap-2 pr-8">
                    <span className="min-w-0 truncate text-xs font-semibold text-white">
                      {p.item.title}
                    </span>
                    <span className="shrink-0 text-[10px] tabular-nums text-white/70">
                      {clock(p.end)}
                    </span>
                  </span>
                </span>
                <span className="absolute inset-x-0 bottom-0 h-0.5 bg-white/15">
                  <span
                    className="block h-full bg-white/80"
                    style={{ width: `${progress(p, now) * 100}%` }}
                  />
                </span>
              </button>
            );
          })}
        </div>
      </section>
    );
  }
  return (
    <section aria-label={t("On now")} className="flex min-w-0 flex-1 items-center bg-surface px-2">
      <div className="sr-only">
        <span className="relative flex size-2">
          <span className="absolute inline-flex size-full animate-ping rounded-full bg-danger/60" />
          <span className="relative inline-flex size-2 rounded-full bg-danger" />
        </span>
        <h2 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">
          {t("On now")}
        </h2>
      </div>
      <div className="app-scroll flex w-full gap-2 overflow-x-auto py-1 [scrollbar-gutter:auto]">
        {picks.map(({ c, p }) => {
          const hue = hueOf(c.id);
          return (
            <button
              key={c.id}
              type="button"
              onClick={() => onSelect(c.id)}
              aria-label={t("{channel} on now: {title}", { channel: c.name, title: p.item.title })}
              className="group relative flex h-[5.5rem] w-64 shrink-0 overflow-hidden rounded-xl border border-border text-left shadow-xs transition-all hover:-translate-y-0.5 hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
              style={{
                background: `linear-gradient(120deg, hsl(${hue} 60% 22%), hsl(${(hue + 50) % 360} 55% 12%))`,
              }}
            >
              {p.item.thumb || c.logo ? (
                <Artwork
                  src={programmeArtwork(p.item.thumb) || c.logo}
                  alt=""
                  title=""
                  className="absolute inset-y-0 right-0 w-24 opacity-90 [mask-image:linear-gradient(to_left,black_40%,transparent)]"
                  imgClassName="object-cover"
                />
              ) : null}
              <span className="relative flex min-w-0 flex-1 flex-col justify-between p-2.5">
                <span className="flex items-center gap-2">
                  <ChannelLogo
                    name={c.name}
                    logo={c.logo}
                    seed={c.id}
                    channelId={c.id}
                    size={22}
                    className="rounded-md"
                  />
                  <span className="truncate text-[11px] font-medium text-white/80">
                    {c.number} · {c.name}
                  </span>
                </span>
                <span className="min-w-0 pr-16">
                  <span className="block truncate text-sm font-semibold text-white">
                    {p.item.title}
                  </span>
                  <span className="block text-[11px] tabular-nums text-white/70">
                    {t("until {time}", { time: clock(p.end) })}
                  </span>
                </span>
              </span>
              <span className="absolute inset-x-0 bottom-0 h-1 bg-white/15">
                <span
                  className="block h-full bg-white/80"
                  style={{ width: `${progress(p, now) * 100}%` }}
                />
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
}
