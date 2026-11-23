import {
  Badge,
  Button,
  Checkbox,
  SearchSelect,
  Spinner,
  Switch,
  cn,
  toast,
} from "@braedonsaunders/appkit-ui";
import { Check, ImageIcon, Pin, RefreshCw, RotateCcw, SlidersHorizontal } from "lucide-react";
import { Fragment, useEffect, useMemo, useState } from "react";
import { Artwork } from "../../components/Artwork";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, MutationError } from "../../components/feedback";
import { ListInput } from "../../components/fields";
import { Section } from "../../components/layout";
import { TitleSearch } from "../../components/pickers";
import { titleSubtitle } from "../../components/Poster";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { localized, t } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { useLocale } from "../../i18n/react";
import { api, apiFetch } from "../../lib/api";
import type { TitleCard } from "../../lib/contract";
import { isPluginDescriptor, textOf, useRegistry } from "../../lib/extend";
import { useAsync, useDebouncedValue } from "../../lib/hooks";
import { blankSpec } from "../../lib/rules";
import { LANGUAGE_TAGS } from "../libraries/browser/CatalogPickers";
import { MetadataWorkspace } from "./MetadataWorkspace";
import {
  normalizeOverlays,
  PRESETS,
  explanationURL,
  type ArtworkExplanation,
  type Overlays,
} from "./model";
import { PosterProviders } from "./PosterProviders";
import { PosterCanvas } from "./PosterCanvas";

function displayLanguage(locale: string, code: string) {
  try {
    return new Intl.DisplayNames([locale], { type: "language" }).of(code) || code;
  } catch {
    return code;
  }
}

type PosterBadgeMeta = {
  key?: string;
  layerKey?: string;
  pluginName?: string;
};

function badgeSetAliases(pluginId: string, setKey: string): string[] {
  return [
    `${pluginId}:${setKey}`,
    `plugin:${pluginId}:${setKey}`,
    `${pluginId}/${setKey}`,
    `plugin:${pluginId}/${setKey}`,
  ];
}

function isBadgeSetDisabled(
  disabled: readonly string[] | undefined,
  pluginId: string,
  setKey: string,
): boolean {
  if (!disabled?.length) return false;
  const aliases = badgeSetAliases(pluginId, setKey);
  return disabled.some((entry) => aliases.includes(entry));
}

type Variant = { path: string; language?: string; tags?: string[] };
type Art = {
  posters: Variant[] | null;
  backdrops: Variant[] | null;
  pinnedPoster?: string;
  pinnedBackdrop?: string;
};
const OPTIONS = localized(
  () =>
    [
      {
        key: "status",
        label: t("Release status badge"),
        hint: t("Recent releases, unwatched new episodes and seasons."),
      },
      {
        key: "quality",
        label: t("Quality badge"),
        hint: t("Last verified playback: resolution, HDR, Dolby Vision and Atmos."),
      },
      {
        key: "ratings",
        label: t("Rating badges"),
        hint: t("TMDB by default; cached provider scores when configured."),
      },
      {
        key: "episodeBadges",
        label: t("Badges on episodes"),
        hint: t("Show badges on individual episode artwork."),
      },
      {
        key: "details",
        label: t("Title details"),
        hint: t("Year, runtime and content rating from the catalogue."),
      },
      {
        key: "personalize",
        label: t("Match posters to taste"),
        hint: t("Choose tagged artwork for each viewer."),
      },
      {
        key: "localize",
        label: t("Localized artwork"),
        hint: t("Follow the language requested by Plex."),
      },
    ] as const,
);

export function ArtworkSettings() {
  const { locale } = useLocale();
  const doc = useAsync(
    async () => normalizeOverlays(await apiFetch<Overlays>("/discovery/overlays/settings")),
    [],
  );
  const users = useAsync(() => api.users(), []);
  const posterBadgesRegistry = useRegistry<PosterBadgeMeta>("posterBadge");
  const { draft, setDraft, dirty, reset } = useDraft(doc.data);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  // Use the same catalogue and image pipeline as the Collection Studio.
  // A bounded pool keeps refresh instant and avoids refetching the catalogue.
  const samples = useAsync(async (signal) => {
    const result = await api.previewCollection(
      { ...blankSpec("mixed"), name: "Artwork preview", limit: 60 },
      signal,
    );
    const titles = [
      ...new Map(
        (result.items ?? [])
          .filter((card) => card.poster && (card.type === "movie" || card.type === "show"))
          .map((card) => [card.key, card]),
      ).values(),
    ];
    for (let i = titles.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [titles[i], titles[j]] = [titles[j]!, titles[i]!];
    }
    return titles;
  }, []);
  const [pickedTitle, setTitle] = useState<TitleCard | null>(null);
  const title = pickedTitle ?? samples.data?.[0] ?? null;
  const alternatives = (samples.data ?? []).filter((card) => card.key !== title?.key);
  const refreshTitle = () => {
    if (alternatives.length)
      setTitle(alternatives[Math.floor(Math.random() * alternatives.length)]!);
  };
  const [lang, setLang] = useState("en");
  const languages = useMemo(() => {
    const options = [...new Set(LANGUAGE_TAGS.map((tag) => tag.split("-")[0]!))]
      .map((value) => ({ value, label: displayLanguage(locale, value) }))
      .sort((a, b) => a.label.localeCompare(b.label, locale));
    if (lang && !options.some((o) => o.value === lang)) {
      options.unshift({ value: lang, label: displayLanguage(locale, lang) });
    }
    return options;
  }, [locale, lang]);
  const [viewer, setViewer] = useState<number | null>(null);
  const [nonce, setNonce] = useState(0);
  const [pinning, setPinning] = useState<TitleCard | null>(null);
  const preview = useDebouncedValue(draft, 180);
  const previewUser = viewer ?? (users.data?.find((u) => u.isOwner) ?? users.data?.[0])?.id ?? null;
  const [mode, setMode] = useState<"poster" | "library" | "compare">("poster");
  const explanationRequest =
    title && preview && previewUser != null
      ? explanationURL(preview, title.key, previewUser, lang, nonce)
      : null;
  const explanation = useAsync(
    async (signal) => {
      if (!explanationRequest) return null;
      const report = await apiFetch<ArtworkExplanation>(explanationRequest, { signal });
      return { request: explanationRequest, report };
    },
    [explanationRequest],
  );
  // useAsync keeps old data during reloads. Never attribute a previous title's
  // or viewer's evidence to the newly selected preview, even before effects run.
  const explanationData =
    !explanation.loading && !explanation.error && explanation.data?.request === explanationRequest
      ? explanation.data?.report
      : null;
  if (doc.error)
    return (
      <MetadataWorkspace
        aside={<ErrorPanel error={doc.error} onRetry={() => void doc.reload()} />}
        main={null}
      />
    );
  if (!draft || !preview)
    return (
      <MetadataWorkspace
        aside={<Spinner size={20} label={t("Loading artwork settings")} />}
        main={null}
      />
    );
  const set = (patch: Partial<Overlays>) => setDraft({ ...draft, ...patch });
  const pluginBadgeSets = posterBadgesRegistry.descriptors.filter(isPluginDescriptor).map((d) => {
    const pluginId = d.owner;
    const setKey = d.meta?.key || (d.id.includes("/") ? d.id.slice(d.id.indexOf("/") + 1) : d.id);
    const entryKey = `${pluginId}:${setKey}`;
    const layerKey = d.meta?.layerKey || `plugin:${pluginId}:${setKey}`;
    const setLabel = textOf(d.label) || setKey;
    const pluginName = d.meta?.pluginName || d.group || pluginId;
    return {
      pluginId,
      setKey,
      entryKey,
      layerKey,
      fullLabel: `${setLabel} · ${pluginName}`,
      hint: textOf(d.hint),
      enabled: !isBadgeSetDisabled(draft.disabledBadgeSets, pluginId, setKey),
    };
  });
  const toggleBadgeSet = (pluginId: string, setKey: string, checked: boolean) => {
    const aliases = new Set(badgeSetAliases(pluginId, setKey));
    const kept = (draft.disabledBadgeSets ?? []).filter((entry) => !aliases.has(entry));
    const nextDisabled = checked ? kept : [...kept, `${pluginId}:${setKey}`];
    set({ disabledBadgeSets: nextDisabled });
  };
  const appearanceDisabled = !draft.enabled || saving;
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const next = await apiFetch<Overlays>("/discovery/overlays/settings", {
        method: "PUT",
        body: JSON.stringify(draft),
      });
      doc.setData(normalizeOverlays(next));
      setNonce((n) => n + 1);
      toast.success(t("Artwork settings saved"), {
        description: t("Plex picks up the new artwork when it refreshes."),
      });
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <MetadataWorkspace
        fitPreview
        actions={
          <Badge variant={dirty ? "warning" : "secondary"}>
            {dirty ? t("Unsaved design") : t("Saved design")}
          </Badge>
        }
        aside={
          <>
            <div className="metadata-enable">
              <div>
                <label htmlFor="overlay-enabled">{t("Ether posters")}</label>
                <p>{t("Serve customized posters")}</p>
              </div>
              <Switch
                id="overlay-enabled"
                aria-label={t("Ether posters")}
                checked={draft.enabled}
                disabled={saving}
                onChange={(e) => set({ enabled: e.currentTarget.checked })}
              />
            </div>
            <PosterProviders onSaved={() => setNonce((n) => n + 1)} />
            <section className="metadata-control-section" aria-label={t("Design presets")}>
              <div className="metadata-section-title">
                <h3>{t("Preset")}</h3>
              </div>
              <div className="metadata-presets">
                {PRESETS.map((p) => {
                  const selected = Object.entries(p.settings).every(
                    ([k, v]) => draft[k as keyof Overlays] === v,
                  );
                  return (
                    <button
                      key={p.key}
                      type="button"
                      className={cn("metadata-preset", selected && "selected")}
                      aria-pressed={selected}
                      disabled={saving}
                      onClick={() => set(p.settings)}
                    >
                      <span className={`metadata-preset-art preset-${p.key}`}>
                        <span style={{ background: p.settings.accent }} />
                        {selected && <Check size={13} />}
                      </span>
                      <strong>{p.name}</strong>
                      <small>{p.description}</small>
                    </button>
                  );
                })}
              </div>
            </section>
            <fieldset disabled={appearanceDisabled} className="metadata-control-section">
              <div className="metadata-section-title">
                <h3>{t("Badge appearance")}</h3>
              </div>
              <div className="metadata-field">
                <label htmlFor="overlay-style">{t("Badge finish")}</label>
                <SearchSelect
                  id="overlay-style"
                  ariaLabel={t("Badge finish")}
                  disabled={appearanceDisabled}
                  value={draft.style}
                  options={[
                    { value: "glass", label: t("Glass") },
                    { value: "solid", label: t("Solid") },
                    { value: "minimal", label: t("Minimal") },
                  ]}
                  onChange={(style) => set({ style: style as Overlays["style"] })}
                  {...searchSelectLabels()}
                />
              </div>
              <div className="metadata-field-row">
                <div className="metadata-field">
                  <label htmlFor="overlay-position">{t("Placement")}</label>
                  <SearchSelect
                    id="overlay-position"
                    ariaLabel={t("Placement")}
                    disabled={appearanceDisabled}
                    value={draft.position}
                    options={[
                      { value: "bottom", label: t("Bottom") },
                      { value: "top", label: t("Top") },
                    ]}
                    onChange={(position) => set({ position: position as Overlays["position"] })}
                    {...searchSelectLabels()}
                  />
                </div>
                <div className="metadata-field">
                  <label htmlFor="overlay-size">{t("Badge size")}</label>
                  <SearchSelect
                    id="overlay-size"
                    ariaLabel={t("Badge size")}
                    disabled={appearanceDisabled}
                    value={draft.size}
                    options={[
                      { value: "small", label: t("Small") },
                      { value: "medium", label: t("Medium") },
                      { value: "large", label: t("Large") },
                      { value: "extra-large", label: t("Extra large") },
                    ]}
                    onChange={(size) => set({ size: size as Overlays["size"] })}
                    {...searchSelectLabels()}
                  />
                </div>
              </div>
              <label className="mt-2.5 flex items-center gap-2 text-sm">
                <Checkbox
                  disabled={appearanceDisabled}
                  checked={!draft.hideDenominator}
                  onChange={(e) => set({ hideDenominator: !e.currentTarget.checked })}
                />
                {t("Show rating denominator")}
              </label>
              <label className="mt-2 flex items-center gap-2 text-sm">
                <Checkbox
                  disabled={appearanceDisabled}
                  checked={!draft.hideDot}
                  onChange={(e) => set({ hideDot: !e.currentTarget.checked })}
                />
                {t("Show leading dot")}
              </label>
              <div className="metadata-field">
                <span>{t("Accent colour")}</span>
                <div className="metadata-swatches">
                  {[
                    ["#d4a574", t("Sand")],
                    ["#a7c4b5", t("Sage")],
                    ["#9ab6d4", t("Glacier")],
                    ["#c0a6d8", t("Lavender")],
                    ["#e8e4dc", t("Pearl")],
                  ].map(([color, name]) => (
                    <button
                      type="button"
                      key={color}
                      aria-label={name}
                      aria-pressed={draft.accent === color}
                      onClick={() => set({ accent: color })}
                      style={{ background: color }}
                    >
                      {draft.accent === color && <Check size={16} />}
                    </button>
                  ))}
                  <label className="metadata-custom-color" title={t("Custom accent")}>
                    <input
                      type="color"
                      aria-label={t("Custom accent")}
                      value={draft.accent}
                      onChange={(e) => set({ accent: e.currentTarget.value })}
                    />
                    <SlidersHorizontal size={15} />
                  </label>
                  <code>{draft.accent}</code>
                </div>
              </div>
            </fieldset>
            <section className="metadata-control-section" aria-label={t("Poster layers")}>
              <div className="metadata-section-title">
                <h3>{t("Poster layers")}</h3>
                <span className="text-[10px] text-fg-muted">
                  {explanation.loading || draft !== preview
                    ? t("Checking title…")
                    : t("Live title data")}
                </span>
              </div>
              {explanation.error && !explanation.loading ? (
                <p className="text-xs text-fg-muted">
                  {t("Preview data unavailable.")}{" "}
                  <button className="underline" onClick={() => void explanation.reload()}>
                    {t("Retry title data")}
                  </button>
                </p>
              ) : null}
              {OPTIONS.map((o) => (
                <Fragment key={o.key}>
                  <div className="metadata-toggle">
                    <div>
                      <label htmlFor={`overlay-${o.key}`}>{o.label}</label>
                      <p>{o.hint}</p>
                      {explanationData?.layers.find((l) => l.key === o.key && l.state !== "off") &&
                        (() => {
                          const layer = explanationData!.layers.find((l) => l.key === o.key)!;
                          return (
                            <p
                              className={`metadata-layer-state is-${layer.state}`}
                              title={layer.reason}
                            >
                              {layer.state === "ready" ? layer.labels.join(" · ") : layer.reason}
                            </p>
                          );
                        })()}
                    </div>
                    <Switch
                      id={`overlay-${o.key}`}
                      aria-label={o.label}
                      checked={draft[o.key]}
                      disabled={appearanceDisabled}
                      onChange={(e) => set({ [o.key]: e.currentTarget.checked })}
                    />
                  </div>
                  {o.key === "ratings" && draft.ratings ? (
                    <div className="metadata-field mt-1 mb-2">
                      <label htmlFor="overlay-rating-style">{t("Rating style")}</label>
                      <SearchSelect
                        id="overlay-rating-style"
                        ariaLabel={t("Rating style")}
                        disabled={appearanceDisabled}
                        value={draft.ratingStyle}
                        options={[
                          { value: "sources", label: t("Each source") },
                          { value: "combined", label: t("One combined score") },
                        ]}
                        onChange={(ratingStyle) =>
                          set({ ratingStyle: ratingStyle as Overlays["ratingStyle"] })
                        }
                        {...searchSelectLabels()}
                      />
                    </div>
                  ) : null}
                </Fragment>
              ))}
              {pluginBadgeSets.length > 0 ? (
                <div
                  role="group"
                  aria-label={t("From plugins")}
                  className="mt-2.5 border-t border-border/60 pt-2.5"
                >
                  <div className="metadata-section-title">
                    <h4 className="text-[11px] font-semibold text-fg-muted">{t("From plugins")}</h4>
                  </div>
                  {pluginBadgeSets.map((b) => {
                    const layer = explanationData?.layers.find(
                      (l) => l.key === b.layerKey && l.state !== "off",
                    );
                    const inputId = `overlay-plugin-${b.pluginId}-${b.setKey}`;
                    return (
                      <div className="metadata-toggle" key={b.entryKey}>
                        <div>
                          <label htmlFor={inputId}>{b.fullLabel}</label>
                          {b.hint ? <p>{b.hint}</p> : null}
                          {layer ? (
                            <p
                              className={`metadata-layer-state is-${layer.state}`}
                              title={layer.reason}
                            >
                              {layer.state === "ready" ? layer.labels.join(" · ") : layer.reason}
                            </p>
                          ) : null}
                        </div>
                        <Switch
                          id={inputId}
                          aria-label={b.fullLabel}
                          checked={b.enabled}
                          disabled={appearanceDisabled}
                          onChange={(e) =>
                            toggleBadgeSet(b.pluginId, b.setKey, e.currentTarget.checked)
                          }
                        />
                      </div>
                    );
                  })}
                </div>
              ) : null}
            </section>
            <Section
              title={t("Preview title")}
              description={t("Try your design on artwork from your catalogue.")}
            >
              <TitleSearch placeholder={t("Search movies and shows…")} onPick={setTitle} />
              {title && (
                <Button size="sm" variant="ghost" onClick={() => setPinning(title)}>
                  <Pin size={13} /> {t("Edit this title’s artwork")}
                </Button>
              )}
              <div className="metadata-field-row">
                <div className="metadata-field">
                  <label htmlFor="overlay-preview-viewer">{t("Preview as")}</label>
                  <SearchSelect
                    id="overlay-preview-viewer"
                    ariaLabel={t("Preview as")}
                    value={previewUser != null ? String(previewUser) : ""}
                    options={(users.data ?? []).map((u) => ({
                      value: String(u.id),
                      label: u.title || u.username,
                    }))}
                    onChange={(v) => setViewer(Number(v))}
                    {...searchSelectLabels()}
                    placeholder={t("Select a viewer")}
                  />
                </div>
                <div className="metadata-field">
                  <label htmlFor="overlay-preview-language">{t("Preview language")}</label>
                  <SearchSelect
                    id="overlay-preview-language"
                    ariaLabel={t("Preview language")}
                    value={lang}
                    options={languages}
                    onChange={setLang}
                    searchable
                    {...searchSelectLabels()}
                  />
                </div>
              </div>
              {users.error ? (
                <ErrorPanel error={users.error} onRetry={() => void users.reload()} />
              ) : null}
            </Section>
            <Section
              title={t("Title artwork")}
              description={t("Give an individual title its own poster or backdrop.")}
            >
              <TitleSearch placeholder={t("Find a title to customise…")} onPick={setPinning} />
            </Section>
          </>
        }
        main={
          <>
            <div className="metadata-preview-toolbar">
              <div className="metadata-preview-title" aria-live="polite">
                <h2 title={title?.title}>{title?.title ?? t("Artwork preview")}</h2>
                <p>{title ? titleSubtitle(title) : t("Catalogue artwork")}</p>
              </div>
              <Button
                size="sm"
                variant="ghost"
                aria-label={t("Try another artwork")}
                title={t("Try another artwork")}
                disabled={!alternatives.length || samples.loading}
                onClick={refreshTitle}
              >
                <RefreshCw size={15} />
              </Button>
              <div className="metadata-view-switch" aria-label={t("Preview view")}>
                {(
                  [
                    ["poster", t("Poster")],
                    ["library", t("Library")],
                    ["compare", t("Compare")],
                  ] as const
                ).map(([key, label]) => (
                  <button
                    type="button"
                    aria-pressed={mode === key}
                    key={key}
                    onClick={() => setMode(key)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>
            {title ? (
              <PosterCanvas
                title={title}
                neighbours={alternatives.slice(0, 2)}
                mode={mode}
                settings={preview}
                user={previewUser}
                lang={lang}
                nonce={nonce}
              />
            ) : (
              <div className="metadata-stage metadata-artwork-empty">
                {samples.loading ? (
                  <Spinner size={22} label={t("Loading catalogue artwork")} />
                ) : samples.error ? (
                  <ErrorPanel error={samples.error} onRetry={() => void samples.reload()} />
                ) : (
                  <>
                    <ImageIcon size={28} />
                    <h2>{t("No catalogue artwork yet")}</h2>
                    <p>
                      {t(
                        "Search for a movie or show in Preview title, or refresh after your catalogue has synced.",
                      )}
                    </p>
                    <Button size="sm" variant="outline" onClick={() => void samples.reload()}>
                      {t("Reload catalogue artwork")}
                    </Button>
                  </>
                )}
              </div>
            )}
            <div className="metadata-preview-caption">
              <span className="metadata-live-dot" />
              <span>
                {mode === "library"
                  ? t("Library preview · Plex layouts vary")
                  : !explanation.loading &&
                      explanationData &&
                      draft.enabled &&
                      explanationData.labels.length === 0
                    ? t("No selected badges available · see Poster layers")
                    : t("Originals preserved · available badges only")}
              </span>
              <Badge variant="outline">
                {draft.enabled ? t("Ether design") : t("Original artwork")}
              </Badge>
            </div>
          </>
        }
      />
      <TitleArtworkDrawer
        title={pinning}
        lang={lang}
        onClose={() => setPinning(null)}
        onSaved={() => setNonce((n) => n + 1)}
      />
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          reset();
          setError(null);
        }}
      />
    </>
  );
}

/** One title's artwork choices: a record, so it's edited in a drawer. */
function TitleArtworkDrawer({
  title,
  lang,
  onClose,
  onSaved,
}: {
  title: TitleCard | null;
  lang: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { locale } = useLocale();
  const [art, setArt] = useState<Art>({ posters: [], backdrops: [] });
  const [choices, setChoices] = useState<Art | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!title) return;
    const ctl = new AbortController();
    setChoices(null);
    setLoadError(null);
    setSaveError(null);
    void Promise.all([
      apiFetch<Art>(`/discovery/overlays/artwork/${title.key}`, { signal: ctl.signal }),
      apiFetch<Art>(`/discovery/overlays/variants/${title.key}?lang=${encodeURIComponent(lang)}`, {
        signal: ctl.signal,
      }),
    ])
      .then(([current, loaded]) => {
        if (ctl.signal.aborted) return;
        setChoices(loaded);
        setArt({
          ...current,
          posters: (loaded.posters ?? []).map((v) => ({
            ...v,
            tags: current.posters?.find((p) => p.path === v.path)?.tags ?? [],
          })),
          backdrops: loaded.backdrops,
        });
      })
      .catch((e) => {
        if (!ctl.signal.aborted) setLoadError(e);
      });
    return () => ctl.abort();
    // Reload only for a different title; the language is read when it opens.
  }, [title?.key]);

  const save = async () => {
    if (!title) return;
    setSaving(true);
    setSaveError(null);
    try {
      await apiFetch(`/discovery/overlays/artwork/${title.key}`, {
        method: "PUT",
        body: JSON.stringify(art),
      });
      toast.success(t("Artwork saved"), { description: title.title });
      onSaved();
      onClose();
    } catch (e) {
      setSaveError(e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer
      open={title !== null}
      onClose={onClose}
      title={title?.title ?? t("Title artwork")}
      description={t("Pick the poster and backdrop for this title.")}
      size="xl"
      footer={
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <Button
            variant="ghost"
            disabled={!choices}
            onClick={() => setArt({ ...art, pinnedPoster: "", pinnedBackdrop: "" })}
          >
            <RotateCcw size={14} /> {t("Use automatic choices")}
          </Button>
          <div className="flex gap-2">
            <Button variant="outline" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button disabled={!choices || saving} onClick={() => void save()}>
              {saving ? t("Saving…") : t("Save title artwork")}
            </Button>
          </div>
        </div>
      }
    >
      {loadError ? (
        <ErrorPanel error={loadError} />
      ) : !choices ? (
        <div className="py-10 text-center">
          <Spinner size={20} label={t("Loading artwork choices")} />
        </div>
      ) : (
        <div className="space-y-6">
          {saveError ? <MutationError error={saveError} /> : null}
          <section className="space-y-3" aria-label={t("Posters")}>
            <div>
              <h3 className="text-sm font-semibold text-fg">{t("Poster")}</h3>
              <p className="text-xs text-fg-muted">
                {t(
                  "Click a poster to pin it for everyone. Tag posters with the genres or people they feature so taste matching can pick one per viewer.",
                )}
              </p>
            </div>
            {choices.posters?.length ? (
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                {choices.posters.map((variant) => {
                  const pinned = art.pinnedPoster === variant.path;
                  return (
                    <div key={variant.path} className="space-y-2">
                      <button
                        type="button"
                        aria-pressed={pinned}
                        aria-label={
                          variant.language
                            ? t("Pin poster ({language})", { language: variant.language })
                            : t("Pin poster (textless)")
                        }
                        className={cn(
                          "relative block w-full overflow-hidden rounded-lg border-2",
                          pinned ? "border-primary" : "border-transparent",
                        )}
                        onClick={() => setArt({ ...art, pinnedPoster: pinned ? "" : variant.path })}
                      >
                        <Artwork
                          src={`https://image.tmdb.org/t/p/w185${variant.path}`}
                          alt={
                            variant.language
                              ? t("Poster ({language})", { language: variant.language })
                              : t("Poster (textless)")
                          }
                          className="aspect-[2/3]"
                        />
                        {pinned ? (
                          <span className="absolute left-1.5 top-1.5 inline-flex items-center gap-1 rounded bg-primary px-1.5 py-0.5 text-[11px] font-medium text-primary-fg">
                            <Pin size={11} /> {t("Pinned")}
                          </span>
                        ) : null}
                      </button>
                      <ListInput
                        ariaLabel={t("Focus tags for {path}", { path: variant.path })}
                        placeholder={t("Drama, actor name")}
                        value={art.posters?.find((p) => p.path === variant.path)?.tags ?? []}
                        onChange={(tags) =>
                          setArt((current) => ({
                            ...current,
                            posters:
                              current.posters?.map((p) =>
                                p.path === variant.path ? { ...p, tags: tags.map(String) } : p,
                              ) ?? [],
                          }))
                        }
                      />
                    </div>
                  );
                })}
              </div>
            ) : (
              <p className="flex items-center gap-2 text-sm text-fg-muted">
                <ImageIcon size={14} /> {t("TMDB has no alternate posters for this title.")}
              </p>
            )}
          </section>
          <section className="space-y-2">
            <label htmlFor="pinned-backdrop" className="block text-sm font-semibold text-fg">
              {t("Backdrop")}
            </label>
            <div className="metadata-backdrops">
              {choices.backdrops?.map((v, index) => (
                <button
                  type="button"
                  key={v.path}
                  aria-label={
                    v.language
                      ? t("Pin backdrop {number} ({language})", {
                          number: index + 1,
                          language: v.language,
                        })
                      : t("Pin backdrop {number} (textless)", { number: index + 1 })
                  }
                  aria-pressed={art.pinnedBackdrop === v.path}
                  onClick={() =>
                    setArt({ ...art, pinnedBackdrop: art.pinnedBackdrop === v.path ? "" : v.path })
                  }
                  className={cn(
                    "overflow-hidden rounded-lg border-2",
                    art.pinnedBackdrop === v.path ? "border-primary" : "border-transparent",
                  )}
                >
                  <Artwork
                    src={`https://image.tmdb.org/t/p/w300${v.path}`}
                    alt={t("Backdrop {number}", { number: index + 1 })}
                    className="aspect-video"
                  />
                </button>
              ))}
            </div>
            <SearchSelect
              id="pinned-backdrop"
              ariaLabel={t("Backdrop")}
              value={art.pinnedBackdrop || ""}
              options={[
                { value: "", label: t("Automatic textless backdrop") },
                ...(art.pinnedBackdrop &&
                !(choices.backdrops ?? []).some((v) => v.path === art.pinnedBackdrop)
                  ? [{ value: art.pinnedBackdrop, label: t("Pinned") }]
                  : []),
                ...(choices.backdrops ?? []).map((v, index) => ({
                  value: v.path,
                  label: t("Backdrop {number}", { number: index + 1 }),
                  hint: v.language ? displayLanguage(locale, v.language) : t("Textless"),
                })),
              ]}
              onChange={(pinnedBackdrop) => setArt({ ...art, pinnedBackdrop })}
              {...searchSelectLabels()}
            />
          </section>
        </div>
      )}
    </Drawer>
  );
}
