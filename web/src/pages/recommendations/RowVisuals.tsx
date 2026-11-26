// How a previewed row draws in the console: the same shapes apps draw
// (standard shelf, hero, wide, tiles, track list, round and square cards),
// plus the chips every row header and source entry carries.
import { Badge, cn } from "@braedonsaunders/appkit-ui";
import { Eye, EyeOff, Pin, Puzzle, Sparkles, User, Users } from "lucide-react";
import { Artwork } from "../../components/Artwork";
import { PosterCard, PosterGrid } from "../../components/Poster";
import { t } from "../../i18n";
import type { TitleCard } from "../../lib/contract";
import type { PlacementMode, PreviewRow, RowCard, RowStyle } from "../../lib/pages";

export function placementLabel(mode: PlacementMode, curated: boolean): string {
  // Curated: pinned rows keep their number; the others sit in your order.
  if (curated && mode !== "pinned") return mode === "hide" ? t("Hidden") : t("On the page");
  switch (mode) {
    case "pinned":
      return t("Pinned");
    case "show":
      return t("Show");
    case "auto":
      return t("Auto");
    default:
      return t("Hide");
  }
}

export function PlacementChip({
  mode,
  position,
  curated,
}: {
  mode: PlacementMode;
  position?: number;
  curated: boolean;
}) {
  const variant =
    mode === "pinned"
      ? "success"
      : mode === "show"
        ? "info"
        : mode === "hide"
          ? "warning"
          : "secondary";
  return (
    <Badge variant={variant} className="gap-1">
      {mode === "pinned" ? (
        <Pin size={11} />
      ) : mode === "auto" ? (
        <Sparkles size={11} />
      ) : mode === "hide" ? (
        <EyeOff size={11} />
      ) : (
        <Eye size={11} />
      )}
      {placementLabel(mode, curated)}
      {mode === "pinned" && position ? ` #${position}` : ""}
    </Badge>
  );
}

export function KindChip({ personal }: { personal: boolean }) {
  return personal ? (
    <Badge variant="outline" className="gap-1 border-primary/40 font-normal text-primary">
      <User size={11} /> {t("Personal")}
    </Badge>
  ) : (
    <Badge variant="outline" className="gap-1 font-normal">
      <Users size={11} /> {t("Shared")}
    </Badge>
  );
}

export function VelaOnlyChip() {
  return (
    <Badge variant="outline" className="gap-1 font-normal">
      <Puzzle size={11} /> {t("Only in Vela")}
    </Badge>
  );
}

/** A title's age rating on its card, "Unrated" when it has none. */
export function RatingBadge({ rating }: { rating?: string }) {
  return (
    <span
      className={cn(
        "rounded px-1 py-0.5 text-[10px] font-semibold",
        rating ? "bg-overlay/80 text-on-scrim" : "bg-warning/90 text-on-scrim",
      )}
    >
      {rating || t("Unrated")}
    </span>
  );
}

function SquareCard({
  item,
  round,
  showRating,
}: {
  item: TitleCard;
  round?: boolean;
  showRating?: boolean;
}) {
  return (
    <div className="w-28 shrink-0 sm:w-32">
      <div
        className={cn(
          "relative aspect-square overflow-hidden shadow-sm ring-1 ring-border-subtle",
          round ? "rounded-full" : "rounded-md",
        )}
      >
        <Artwork src={item.poster} alt={item.title} title={item.title} className="size-full" />
        {showRating ? (
          <span
            className={cn(
              "absolute",
              round ? "inset-x-0 bottom-2 text-center" : "left-1.5 top-1.5",
            )}
          >
            <RatingBadge rating={item.contentRating} />
          </span>
        ) : null}
      </div>
      <p className={cn("mt-1.5 truncate text-xs font-medium text-fg", round && "text-center")}>
        {item.title}
      </p>
    </div>
  );
}

function WideCard({
  item,
  hero,
  showRating,
}: {
  item: TitleCard;
  hero?: boolean;
  showRating?: boolean;
}) {
  return (
    <div className={cn("shrink-0", hero ? "w-80 sm:w-[26rem]" : "w-56 sm:w-64")}>
      <div className="relative aspect-video overflow-hidden rounded-lg shadow-sm ring-1 ring-border-subtle">
        <Artwork
          src={item.backdrop ?? item.poster}
          alt={item.title}
          title={item.title}
          className="size-full"
        />
        {showRating ? (
          <span className="absolute left-1.5 top-1.5">
            <RatingBadge rating={item.contentRating} />
          </span>
        ) : null}
        {hero ? (
          <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-overlay/80 to-transparent px-3 pb-2 pt-8">
            <p className="truncate text-sm font-semibold text-on-scrim">{item.title}</p>
          </div>
        ) : null}
      </div>
      {hero ? null : <p className="mt-1.5 truncate text-xs font-medium text-fg">{item.title}</p>}
    </div>
  );
}

function RatedPoster({
  item,
  showRating,
  rank,
}: {
  item: TitleCard;
  showRating: boolean;
  rank?: number;
}) {
  return (
    <div className="relative w-28 shrink-0 sm:w-32">
      {rank ? (
        <span className="absolute -left-1 -top-1 z-10 grid size-6 place-items-center rounded-full bg-primary text-xs font-bold text-primary-fg shadow">
          {rank}
        </span>
      ) : null}
      <PosterCard
        item={item}
        badge={showRating ? <RatingBadge rating={item.contentRating} /> : undefined}
      />
    </div>
  );
}

/** The titles of one previewed row, drawn in the row's look. */
export function RowBody({
  row,
  showRating,
  style,
  card,
  numbered,
  pluginName,
}: {
  row: Pick<PreviewRow, "items" | "present" | "ghost">;
  showRating: boolean;
  style?: RowStyle;
  card?: RowCard;
  numbered?: boolean;
  pluginName?: string;
}) {
  const items = row.items ?? [];
  const layout = style || row.present.layout || "standard";
  const shape = card || row.present.card || "";
  const ranked = numbered ?? row.present.numbered ?? false;
  if (row.present.surface) {
    return (
      <div className="flex items-center gap-3 rounded-xl border border-dashed border-border bg-bg-subtle/60 px-4 py-5 text-sm text-fg-muted">
        <Puzzle size={18} />
        <span>
          {t(
            "Drawn by {plugin} in Vela. Vela hides it when there's nothing to show, and it doesn't count toward the page length.",
            { plugin: pluginName || row.present.surface.pluginName || row.present.surface.plugin },
          )}
        </span>
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <p className="rounded-xl border border-dashed border-border px-4 py-4 text-sm text-fg-muted">
        {row.ghost
          ? t("Not showing at this moment.")
          : t("Nothing to show for this viewer right now.")}
      </p>
    );
  }
  if (layout === "tracks") {
    return (
      <div className="overflow-hidden rounded-xl border border-border">
        {items.slice(0, 5).map((it, i) => (
          <div
            key={it.key}
            className="flex items-center gap-3 border-b border-border-subtle px-3 py-2 text-sm last:border-0"
          >
            <span className="w-4 text-right tabular-nums text-fg-subtle">{i + 1}</span>
            <Artwork src={it.poster} alt="" title={it.title} className="size-8 shrink-0 rounded" />
            <span className="min-w-0 flex-1 truncate font-medium text-fg">{it.title}</span>
            {showRating ? <RatingBadge rating={it.contentRating} /> : null}
            {it.year ? <span className="tabular-nums text-fg-muted">{it.year}</span> : null}
          </div>
        ))}
      </div>
    );
  }
  if (layout === "tiles") {
    return (
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {items.slice(0, 6).map((it) => (
          <div
            key={it.key}
            className="flex items-center gap-2 overflow-hidden rounded-md bg-surface ring-1 ring-border-subtle"
          >
            <Artwork src={it.poster} alt="" title={it.title} className="size-11 shrink-0" />
            <span className="min-w-0 flex-1 truncate text-xs font-semibold text-fg">
              {it.title}
            </span>
            {showRating ? (
              <span className="mr-1.5 shrink-0">
                <RatingBadge rating={it.contentRating} />
              </span>
            ) : null}
          </div>
        ))}
      </div>
    );
  }
  return (
    <ul className="shelf flex gap-3 overflow-x-auto pb-1" aria-label={t("Titles")}>
      {items.map((it, i) => (
        <li key={it.key}>
          {layout === "hero" ? (
            <WideCard item={it} hero showRating={showRating} />
          ) : layout === "wide" || shape === "landscape" ? (
            <WideCard item={it} showRating={showRating} />
          ) : shape === "square" || shape === "round" ? (
            <SquareCard item={it} round={shape === "round"} showRating={showRating} />
          ) : (
            <RatedPoster
              item={it}
              showRating={showRating}
              rank={ranked && i < 10 ? i + 1 : undefined}
            />
          )}
        </li>
      ))}
    </ul>
  );
}

function RankBadge({ rank }: { rank: number }) {
  return (
    <span className="grid size-6 place-items-center rounded-full bg-primary text-xs font-bold text-primary-fg shadow">
      {rank}
    </span>
  );
}

/**
 * Every title of one previewed row as a grid, in the row's card shape: the
 * row drawer's Content tab. Shelves scroll sideways on the page; here the
 * whole row is visible at once.
 */
export function RowGrid({
  row,
  style,
  card,
  numbered,
  pluginName,
  showRating = false,
}: {
  row: Pick<PreviewRow, "items" | "present" | "ghost">;
  /** Every card shows its age rating ("Unrated" when it has none). */
  showRating?: boolean;
  style?: RowStyle;
  card?: RowCard;
  numbered?: boolean;
  pluginName?: string;
}) {
  const items = row.items ?? [];
  const layout = style || row.present.layout || "standard";
  const shape = card || row.present.card || "";
  const ranked = numbered ?? row.present.numbered ?? false;
  if (row.present.surface || items.length === 0) {
    return <RowBody row={row} showRating={false} pluginName={pluginName} />;
  }
  if (layout === "tracks" || layout === "tiles") {
    return (
      <div className="overflow-hidden rounded-xl border border-border">
        {items.map((it, i) => (
          <div
            key={`${it.key}-${i}`}
            className="flex items-center gap-3 border-b border-border-subtle px-3 py-2 text-sm last:border-0"
          >
            <span className="w-5 text-right tabular-nums text-fg-subtle">{i + 1}</span>
            <Artwork src={it.poster} alt="" title={it.title} className="size-9 shrink-0 rounded" />
            <span className="min-w-0 flex-1 truncate font-medium text-fg">{it.title}</span>
            {showRating ? <RatingBadge rating={it.contentRating} /> : null}
            {it.year ? <span className="tabular-nums text-fg-muted">{it.year}</span> : null}
          </div>
        ))}
      </div>
    );
  }
  if (layout === "hero" || layout === "wide" || shape === "landscape") {
    return (
      <ul className="grid grid-cols-2 gap-x-2 gap-y-4 sm:grid-cols-[repeat(auto-fill,minmax(11rem,1fr))] sm:gap-x-3">
        {items.map((it, i) => (
          <li key={`${it.key}-${i}`} className="min-w-0">
            <div className="relative aspect-video overflow-hidden rounded-lg shadow-sm ring-1 ring-border-subtle">
              <Artwork
                src={it.backdrop ?? it.poster}
                alt={it.title}
                title={it.title}
                className="size-full"
              />
              {ranked && i < 10 ? (
                <span className="absolute left-1.5 top-1.5">
                  <RankBadge rank={i + 1} />
                </span>
              ) : null}
              {showRating ? (
                <span className="absolute right-1.5 top-1.5">
                  <RatingBadge rating={it.contentRating} />
                </span>
              ) : null}
            </div>
            <p className="mt-1.5 truncate text-xs font-medium text-fg">{it.title}</p>
          </li>
        ))}
      </ul>
    );
  }
  if (shape === "square" || shape === "round") {
    return (
      <ul className="grid grid-cols-3 gap-x-2 gap-y-4 sm:grid-cols-[repeat(auto-fill,minmax(7rem,1fr))] sm:gap-x-3">
        {items.map((it, i) => (
          <li key={`${it.key}-${i}`} className="min-w-0">
            <div
              className={cn(
                "aspect-square overflow-hidden shadow-sm ring-1 ring-border-subtle",
                shape === "round" ? "rounded-full" : "rounded-md",
              )}
            >
              <Artwork src={it.poster} alt={it.title} title={it.title} className="size-full" />
            </div>
            {showRating ? (
              <p className={cn("mt-1", shape === "round" && "text-center")}>
                <RatingBadge rating={it.contentRating} />
              </p>
            ) : null}
            <p
              className={cn(
                "mt-1.5 truncate text-xs font-medium text-fg",
                shape === "round" && "text-center",
              )}
            >
              {it.title}
            </p>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <PosterGrid
      className="grid-cols-3 gap-x-2 sm:grid-cols-[repeat(auto-fill,minmax(7rem,1fr))] sm:gap-x-3"
      items={items}
      badge={
        ranked || showRating
          ? (it, i) => (
              <span className="flex items-center gap-1">
                {ranked && i < 10 ? <RankBadge rank={i + 1} /> : null}
                {showRating ? <RatingBadge rating={it.contentRating} /> : null}
              </span>
            )
          : undefined
      }
    />
  );
}
