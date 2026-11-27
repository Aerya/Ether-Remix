import { Button, Select, Input, MultiSelect, Switch, cn, toast } from "@braedonsaunders/appkit-ui";
import {
  Check,
  Clapperboard,
  Grid2x2,
  Image as ImageIcon,
  Link2,
  RefreshCw,
  Sparkles,
  Wand2,
} from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { Artwork } from "../../components/Artwork";
import { Field, NumberInput, Segmented } from "../../components/fields";
import { TitleSearch, useViewers, viewerName } from "../../components/pickers";
import type { ArtworkMode, CollectionSpec, CollectionUse, TitleCard } from "../../lib/contract";
import type { AsyncState } from "../../lib/hooks";
import { getLocale, localized, t } from "../../i18n";
import { formatDateTime, relativeTime } from "../../lib/format";
import { PseRefreshField } from "../../pse/RefreshField";
import type { SpecUpdate } from "./BuildPanel";
import { artworkPreviewURL, artworkSource } from "./artwork";
import { UsedOn } from "./UsedOn";

// ---- Refresh ----------------------------------------------------------------------

export function SchedulePanel({
  spec,
  update,
  onRebuild,
  rebuilding,
}: {
  spec: CollectionSpec;
  update: SpecUpdate;
  onRebuild?: () => void;
  rebuilding?: boolean;
}) {
  const rotate = spec.rotate ?? null;
  return (
    <div className="space-y-7">
      {/* PSE-HOOK: cadence « Toutes les N h » — voir docs/pse/FORK.fr.md */}
      <PseRefreshField
        label={t("Refresh")}
        refresh={spec.refresh}
        onChange={(refresh) => update({ refresh })}
      />

      <div className="space-y-3">
        <ToggleRow
          title={t("Rotate members")}
          description={t("Show a fresh subset each period so the row never goes stale.")}
          checked={Boolean(rotate)}
          onChange={(on) =>
            update({ rotate: on ? { size: Math.min(20, spec.limit || 20), every: "daily" } : null })
          }
        />
        {rotate ? (
          <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border-subtle bg-bg-subtle p-3">
            <Field label={t("Titles shown")}>
              <NumberInput
                aria-label={t("Titles shown per rotation")}
                value={rotate.size}
                min={1}
                className="w-24"
                onChange={(n) =>
                  update({ rotate: { ...rotate, size: Math.max(1, Math.round(n)) } }, "rotate-size")
                }
              />
            </Field>
            <Field label={t("Rotate")}>
              <Segmented
                ariaLabel={t("Rotation period")}
                value={rotate.every}
                onChange={(v) => update({ rotate: { ...rotate, every: v } })}
                options={[
                  { value: "daily", label: t("Daily") },
                  { value: "weekly", label: t("Weekly") },
                ]}
              />
            </Field>
          </div>
        ) : null}
      </div>

      <div className="rounded-lg border border-border bg-surface p-4">
        <p className="text-sm font-medium text-fg">{t("Build status")}</p>
        {spec.id ? (
          <>
            <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
              <dt className="text-fg-muted">{t("Titles")}</dt>
              <dd className="tabular-nums text-fg">{spec.itemCount ?? "—"}</dd>
              <dt className="text-fg-muted">{t("Last built")}</dt>
              <dd className="text-fg" title={formatDateTime(spec.lastBuiltAt)}>
                {relativeTime(spec.lastBuiltAt)}
              </dd>
              <dt className="text-fg-muted">{t("Saved")}</dt>
              <dd className="text-fg" title={formatDateTime(spec.updatedAt)}>
                {relativeTime(spec.updatedAt)}
              </dd>
            </dl>
            {onRebuild ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={onRebuild}
                disabled={rebuilding}
              >
                <RefreshCw size={14} className={rebuilding ? "animate-spin" : ""} />{" "}
                {rebuilding ? t("Rebuilding…") : t("Rebuild now")}
              </Button>
            ) : null}
          </>
        ) : (
          <p className="mt-1 text-sm text-fg-muted">
            {t("Save the collection to build it for the first time.")}
          </p>
        )}
      </div>
    </div>
  );
}

/** A labelled switch on its own card row. */
export function ToggleRow({
  title,
  description,
  checked,
  onChange,
  disabled,
}: {
  title: ReactNode;
  description?: ReactNode;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-4 rounded-lg border border-border bg-surface p-3">
      <span className="min-w-0">
        <span className="block text-sm font-medium text-fg">{title}</span>
        {description ? (
          <span className="mt-0.5 block text-xs text-fg-muted">{description}</span>
        ) : null}
      </span>
      <Switch
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.currentTarget.checked)}
        aria-label={typeof title === "string" ? title : undefined}
      />
    </label>
  );
}

// ---- Artwork ------------------------------------------------------------------------

const MODES = localized<{ value: ArtworkMode; label: string; hint: string; icon: ReactNode }[]>(
  () => [
    {
      value: "auto",
      label: t("Automatic"),
      hint: t("Ether picks the best style for the titles inside."),
      icon: <Wand2 size={16} />,
    },
    {
      value: "collage",
      label: t("Poster collage"),
      hint: t("Four or six member posters, with the collection name."),
      icon: <Grid2x2 size={16} />,
    },
    {
      value: "backdrop",
      label: t("Backdrop"),
      hint: t("The lead title's backdrop with the name set over it."),
      icon: <Clapperboard size={16} />,
    },
    {
      value: "tmdb",
      label: t("TMDB collection"),
      hint: t("Official franchise artwork when the source is a TMDB collection."),
      icon: <ImageIcon size={16} />,
    },
    {
      value: "custom",
      label: t("Custom image"),
      hint: t("Any image URL you choose."),
      icon: <Link2 size={16} />,
    },
  ],
);

const ACCENTS = [
  "rose",
  "amber",
  "lime",
  "emerald",
  "teal",
  "cyan",
  "sky",
  "indigo",
  "violet",
] as const;
const ACCENT_BG: Record<(typeof ACCENTS)[number], string> = {
  rose: "bg-accent-rose",
  amber: "bg-accent-amber",
  lime: "bg-accent-lime",
  emerald: "bg-accent-emerald",
  teal: "bg-accent-teal",
  cyan: "bg-accent-cyan",
  sky: "bg-accent-sky",
  indigo: "bg-accent-indigo",
  violet: "bg-accent-violet",
};
const ACCENT_LABEL = localized<Record<(typeof ACCENTS)[number], string>>(() => ({
  rose: t("rose"),
  amber: t("amber"),
  lime: t("lime"),
  emerald: t("emerald"),
  teal: t("teal"),
  cyan: t("cyan"),
  sky: t("sky"),
  indigo: t("indigo"),
  violet: t("violet"),
}));

/** The accent palette is the categorical token set, resolved to hex for the server's renderer. */
function accentHex(name: string): string {
  if (typeof window === "undefined") return "";
  const raw = getComputedStyle(document.documentElement)
    .getPropertyValue(`--ch-accent-${name}`)
    .trim();
  const parts = raw.split(/\s+/).map(Number);
  if (parts.length !== 3 || parts.some((n) => !Number.isFinite(n))) return "";
  return `#${parts.map((n) => n.toString(16).padStart(2, "0")).join("")}`;
}

export function ArtworkPanel({
  spec,
  update,
  preview,
}: {
  spec: CollectionSpec;
  update: SpecUpdate;
  preview: TitleCard[];
}) {
  const swatches = useMemo(
    () => ACCENTS.map((a) => ({ name: a, hex: accentHex(a) })).filter((s) => s.hex),
    [],
  );
  const accent = spec.artwork.accent ?? "";
  return (
    <div className="space-y-6">
      <div>
        <h3 className="text-sm font-semibold text-fg">{t("Collection poster")}</h3>
        <p className="mt-1 text-xs leading-relaxed text-fg-muted">
          {t("Saved artwork is the collection poster. Collages follow the title order.")}
        </p>
      </div>
      <div role="radiogroup" aria-label={t("Artwork style")} className="grid grid-cols-2 gap-2">
        {MODES.map((m) => {
          const on = spec.artwork.mode === m.value;
          return (
            <button
              key={m.value}
              type="button"
              role="radio"
              aria-label={m.label}
              aria-checked={on}
              onClick={() => update({ artwork: { ...spec.artwork, mode: m.value } })}
              className={cn(
                "min-w-0 rounded-lg border p-2.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                on
                  ? "border-primary/50 bg-primary-subtle"
                  : "border-border bg-surface hover:border-border-strong",
              )}
            >
              <div className="mb-2 flex items-start justify-between gap-2">
                <ArtworkPreview
                  spec={{ ...spec, artwork: { ...spec.artwork, mode: m.value } }}
                  items={preview}
                  size="sm"
                  className="max-w-14"
                />
                {on ? (
                  <Check size={14} className="text-primary" />
                ) : (
                  <span className="text-fg-subtle">{m.icon}</span>
                )}
              </div>
              <span className="block text-xs font-semibold text-fg">{m.label}</span>
              <span className="mt-1 block text-[11px] leading-relaxed text-fg-muted">{m.hint}</span>
            </button>
          );
        })}
      </div>
      {spec.artwork.mode === "collage" || spec.artwork.mode === "auto" ? (
        <p className="text-xs leading-relaxed text-fg-muted">
          {t(
            "Uses up to six available member posters. To choose the films and their order, use hand-picked titles in Build and sort by Source order. Artwork falls back to a single image when fewer than four posters are available.",
          )}
        </p>
      ) : null}
      {spec.artwork.mode === "tmdb" ? (
        <p className="text-xs leading-relaxed text-fg-muted">
          {t(
            "The official franchise poster is fetched when you save. Collections without a TMDB franchise source use automatic artwork.",
          )}
        </p>
      ) : null}
      <section
        aria-label={t("Choose a title poster")}
        className="space-y-3 border-t border-border pt-4"
      >
        <div>
          <h3 className="text-sm font-semibold text-fg">{t("Use a title’s poster")}</h3>
          <p className="mt-1 text-xs text-fg-muted">
            {t("Choose an existing poster without changing the collection’s members.")}
          </p>
        </div>
        <TitleSearch
          placeholder={t("Find a film or show for its poster…")}
          onPick={(title) => {
            const source = artworkSource(title.poster);
            if (source) update({ artwork: { ...spec.artwork, mode: "custom", url: source } });
            else toast.error(t("This title has no usable poster"));
          }}
        />
        {preview.some((title) => artworkSource(title.poster)) ? (
          <div className="grid grid-cols-4 gap-2">
            {preview
              .filter((title) => artworkSource(title.poster))
              .slice(0, 8)
              .map((title) => {
                const source = artworkSource(title.poster)!;
                const on = spec.artwork.mode === "custom" && spec.artwork.url === source;
                return (
                  <button
                    key={title.key}
                    type="button"
                    aria-label={t("Use {title} poster", { title: title.title })}
                    aria-pressed={on}
                    onClick={() =>
                      update({ artwork: { ...spec.artwork, mode: "custom", url: source } })
                    }
                    className={cn(
                      "min-w-0 rounded-md p-1 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
                      on ? "bg-primary-subtle ring-1 ring-primary" : "hover:bg-surface-hover",
                    )}
                  >
                    <Artwork
                      src={title.poster}
                      alt=""
                      title={title.title}
                      className="aspect-[2/3] w-full rounded"
                    />
                    <span className="mt-1 block truncate text-[10px] text-fg-muted">
                      {title.title}
                    </span>
                  </button>
                );
              })}
          </div>
        ) : null}
      </section>

      {spec.artwork.mode === "custom" ? (
        <Field
          label={t("Image URL or TMDB path")}
          hint={t("A 2:3 poster works best. You can paste an image URL or pick a title above.")}
        >
          <Input
            type="text"
            value={spec.artwork.url ?? ""}
            placeholder="https://…"
            onChange={(e) =>
              update({ artwork: { ...spec.artwork, url: e.currentTarget.value } }, "artwork-url")
            }
            aria-label={t("Custom image URL")}
          />
        </Field>
      ) : null}

      {spec.artwork.mode !== "custom" && spec.artwork.mode !== "tmdb" ? (
        <Field
          label={t("Accent")}
          hint={t("Tints generated artwork. Automatic matches the lead poster.")}
        >
          <div
            className="flex flex-wrap items-center gap-2"
            role="radiogroup"
            aria-label={t("Accent colour")}
          >
            <button
              type="button"
              role="radio"
              aria-checked={!accent}
              onClick={() => update({ artwork: { ...spec.artwork, accent: undefined } })}
              className={cn(
                "h-8 rounded-full border px-3 text-xs font-medium",
                !accent
                  ? "border-primary bg-primary-subtle text-primary"
                  : "border-border text-fg-muted hover:text-fg",
              )}
            >
              <Sparkles size={12} className="mr-1 inline" />
              {t("Automatic")}
            </button>
            {swatches.map((s) => (
              <button
                key={s.name}
                type="button"
                role="radio"
                aria-checked={accent.toLowerCase() === s.hex}
                aria-label={ACCENT_LABEL[s.name]}
                title={ACCENT_LABEL[s.name]}
                onClick={() => update({ artwork: { ...spec.artwork, accent: s.hex } })}
                className={cn(
                  "size-8 rounded-full ring-offset-2 ring-offset-bg transition",
                  ACCENT_BG[s.name],
                  accent.toLowerCase() === s.hex ? "ring-2 ring-fg" : "hover:scale-110",
                )}
              />
            ))}
            {swatches.length ? (
              <label className="flex items-center gap-1.5 text-xs text-fg-muted">
                <input
                  type="color"
                  value={/^#[0-9a-f]{6}$/i.test(accent) ? accent : swatches[0]!.hex}
                  onChange={(e) =>
                    update(
                      { artwork: { ...spec.artwork, accent: e.currentTarget.value } },
                      "accent",
                    )
                  }
                  className="size-8 cursor-pointer rounded-full border border-border bg-transparent p-0"
                  aria-label={t("Custom accent colour")}
                />
                {t("Custom")}
              </label>
            ) : null}
          </div>
        </Field>
      ) : null}
    </div>
  );
}

/** Client-side approximation of the server's generated poster, from the preview titles. */
export function ArtworkPreview({
  spec,
  items,
  className,
  size = "md",
}: {
  spec: CollectionSpec;
  items: TitleCard[];
  className?: string;
  size?: "sm" | "md";
}) {
  const posters = items.filter((i) => i.poster);
  const mode =
    spec.artwork.mode === "auto" || (spec.artwork.mode === "collage" && posters.length < 4)
      ? posters.length >= 4
        ? "collage"
        : "backdrop"
      : spec.artwork.mode;
  const accentStyle = spec.artwork.accent ? { backgroundColor: spec.artwork.accent } : undefined;
  const name = spec.name.trim() || t("Untitled collection");
  return (
    <div
      className={cn(
        "relative aspect-[2/3] w-full overflow-hidden rounded-md bg-bg-subtle shadow-md ring-1 ring-border",
        className,
      )}
      aria-label={t("Artwork preview")}
      role="img"
    >
      {mode === "collage" ? (
        <div
          className={cn(
            "grid size-full grid-rows-2 gap-px bg-border",
            posters.length >= 6 ? "grid-cols-3" : "grid-cols-2",
          )}
        >
          {posters.slice(0, posters.length >= 6 ? 6 : 4).map((p) => (
            <Artwork key={p.key} src={p.poster} alt="" title="" className="size-full" eager />
          ))}
        </div>
      ) : mode === "backdrop" ? (
        <Artwork
          src={items[0]?.backdrop ?? items[0]?.poster}
          alt=""
          title=""
          className="size-full"
          imgClassName="object-center"
          eager
        />
      ) : mode === "tmdb" ? (
        <div className="grid size-full place-items-center p-2 text-center text-fg-subtle">
          <ImageIcon size={size === "sm" ? 16 : 28} aria-hidden />
          <span className="sr-only">
            {t("Official TMDB collection artwork appears after saving")}
          </span>
        </div>
      ) : (
        <Artwork
          src={artworkPreviewURL(spec.artwork.url)}
          alt=""
          title={size === "sm" ? "" : t("Add an image URL")}
          icon={<Link2 size={size === "sm" ? 16 : 22} aria-hidden />}
          className="size-full"
          eager
        />
      )}
      {mode !== "tmdb" && mode !== "custom" ? (
        <div
          className={cn(
            "scrim-bottom absolute inset-x-0 bottom-0",
            size === "sm" ? "px-1.5 pb-1.5 pt-6" : "px-3 pb-3 pt-10",
          )}
        >
          <div
            className={cn(
              "rounded-full",
              size === "sm" ? "mb-1 h-0.5 w-4" : "mb-2 h-1 w-8",
              !accentStyle && "bg-primary",
            )}
            style={accentStyle}
          />
          <p
            className={cn(
              "text-on-scrim line-clamp-3 font-bold leading-tight",
              size === "sm" ? "text-[10px]" : "text-base",
            )}
          >
            {name}
          </p>
          {items.length && size !== "sm" ? (
            <p className="text-on-scrim mt-0.5 text-[10px] opacity-75">
              {spec.mediaType === "show" ? t("Series") : t("Collection")}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

// ---- Used on ------------------------------------------------------------------------

/**
 * Where the collection shows. Collections are content only: rows that show
 * one are placed in the Recommendations designer, listed here as "Used on".
 */
export function UsedOnPanel({
  spec,
  update,
  usage,
}: {
  spec: CollectionSpec;
  update: SpecUpdate;
  usage: AsyncState<CollectionUse[]>;
}) {
  const { viewers } = useViewers();
  const users = spec.users ?? "all";
  const audienceLabel = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
  return (
    <div className="space-y-7">
      <section className="space-y-2" aria-labelledby="used-on-h">
        <h3 id="used-on-h" className="text-sm font-semibold text-fg">
          {t("Used on")}
        </h3>
        <p className="text-sm text-fg-muted">
          {t("Rows that show this collection. Open one to change where, when and for whom.")}
        </p>
        <label className="block space-y-2 text-sm">
          <span className="font-medium">
            {getLocale().startsWith("fr") ? "Carte de navigation Remix" : "Remix navigation card"}
          </span>
          <Select
            aria-label={getLocale().startsWith("fr") ? "Groupe de cartes" : "Card group"}
            value={spec.navigationGroup ?? ""}
            onChange={(e) => {
              const navigationGroup = e.target.value as CollectionSpec["navigationGroup"];
              update({
                navigationGroup: navigationGroup || undefined,
                browseInApps: navigationGroup ? true : spec.browseInApps,
              });
            }}
          >
            <option value="">{getLocale().startsWith("fr") ? "Aucune carte" : "No card"}</option>
            <option value="studios">Studios &amp; labels</option>
            <option value="services">Services</option>
            {spec.navigationGroup?.startsWith("services-") && (
              <option value={spec.navigationGroup}>
                Services · {spec.navigationGroup.slice(9)}
              </option>
            )}
            <option value="genres">Genres</option>
          </Select>
          <span className="block text-fg-muted">
            {getLocale().startsWith("fr")
              ? "Utilisez aussi vos collections Xperience ou personnalisées dans le préréglage Cartes inspirées de Nuvio. Le contenu et les sources de cette collection restent inchangés."
              : "Use Xperience or custom collections in the Nuvio-inspired cards preset too. This collection's content and sources stay unchanged."}
          </span>
        </label>
        <UsedOn collectionId={spec.id} usage={usage} />
      </section>
      <section className="space-y-2" aria-labelledby="who-h">
        <h3 id="who-h" className="text-sm font-semibold text-fg">
          {audienceLabel("Qui peut voir cette collection", "Who sees it")}
        </h3>
        <Segmented
          ariaLabel={audienceLabel("Accès à la collection", "Audience")}
          value={users === "all" ? "all" : "some"}
          onChange={(v) => update({ users: v === "all" ? "all" : [] })}
          options={[
            { value: "all", label: audienceLabel("Tout le monde", "Everyone") },
            { value: "some", label: audienceLabel("Certains spectateurs", "Specific viewers") },
          ]}
        />
        {users !== "all" ? (
          <MultiSelect
            value={users.map(String)}
            onChange={(v) => update({ users: v.map(Number) })}
            ariaLabel={audienceLabel("Spectateurs", "Viewers")}
            placeholder={
              viewers
                ? audienceLabel("Choisir des spectateurs…", "Choose viewers…")
                : audienceLabel("Chargement des spectateurs…", "Loading viewers…")
            }
            searchPlaceholder={audienceLabel("Rechercher…", "Search…")}
            noMatchesLabel={audienceLabel("Aucun résultat", "No matches")}
            clearLabel={audienceLabel("Effacer", "Clear")}
            selectAllLabel={audienceLabel("Tout sélectionner", "Select all")}
            options={(viewers ?? []).map((v) => ({
              value: String(v.id),
              label: viewerName(v),
              description: v.isOwner ? audienceLabel("Propriétaire", "Owner") : v.username,
            }))}
          />
        ) : null}
        {users !== "all" && users.length === 0 ? (
          <p className="text-xs text-warning">
            {audienceLabel("Aucun spectateur sélectionné.", "No viewers chosen yet.")}
          </p>
        ) : null}
      </section>

      <section className="space-y-2" aria-labelledby="browse-h">
        <h3 id="browse-h" className="text-sm font-semibold text-fg">
          {t("Browse")}
        </h3>
        <ToggleRow
          title={t("Browse in apps")}
          description={t("Show it as a collection in its library's Collections view.")}
          checked={spec.browseInApps}
          onChange={(browseInApps) => update({ browseInApps })}
        />
      </section>
    </div>
  );
}
