import { sourceLabel } from "./CatalogueSources";
import {
  Badge,
  Button,
  Input,
  SearchSelect,
  SettingsSection,
  Switch,
  buttonVariants,
  cn,
  toast,
} from "@braedonsaunders/appkit-ui";
import { Film, Plug, RefreshCw, Tv } from "lucide-react";
import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useApp } from "../../app-context";
import { SettingsRow } from "../../components/SettingsRow";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { LevelBadge } from "../../components/status";
import { ErrorPanel } from "../../components/feedback";
import { LoadingRows } from "../../components/feedback";
import { t, tn } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { useQuality } from "../releases/useQuality";
import {
  librariesApi,
  publishedTo,
  type AppSwitchPatch,
  type Library,
  type LibraryCardShape,
  type LibrarySyncRequest,
  type PresentationAppKey,
  type Presentations,
  type ResolvedAppPresentation,
  type ResolvedPresentations,
} from "./api";
import { LibraryCacheCard } from "./LibraryCache";
import { TitlesComeFrom } from "./TitlesComeFrom";
import { NarrowingEditor } from "./NarrowingEditor";
import {
  DeleteGuardChoice,
  formatItemStorageSummary,
  formatStoredCapSummary,
  formatSyncStatusLine,
  notifySyncError,
  notifySyncResponse,
  resolveDeleteGuard,
} from "./syncStatus";

function presentationHint(value: string): string {
  const hints: Record<string, string> = {
    movie: t("Shown as a movie library"),
    movies: t("Shown as a movie library"),
    Movie: t("Shown as a movie library"),
    show: t("Shown as a show library with seasons"),
    shows: t("Shown as a show library with seasons"),
    series: t("Shown as a show library with seasons"),
    tvshows: t("Shown as a show library with seasons"),
    Series: t("Shown as a show library with seasons"),
    Season: t("Shown as a show library with seasons"),
    Episode: t("Shown as a show library with seasons"),
    artist: t("Shown as a music library"),
    music: t("Shown as a music library"),
    MusicAlbum: t("Shown as a music library"),
    Audio: t("Shown as a music library"),
    photo: t("Shown as a photo library"),
    photos: t("Shown as a photo library"),
    PhotoAlbum: t("Shown as a photo library"),
    Photo: t("Shown as a photo library"),
    audiobooks: t("Shown as an audiobook library"),
    books: t("Shown as a book library"),
    Book: t("Shown as a book library"),
    homevideos: t("Shown as a home video library"),
    channel: t("Shown as a channel library"),
    live: t("Shown as a live TV library"),
    tv: t("Shown as a live TV library"),
  };
  return hints[value] ?? value;
}

function unavailableReason(reason?: string): string {
  if (!reason) return t("Not available for this library type.");
  const localized: Record<string, string> = {
    "Plex does not have a book library type.": t("Plex does not have a book library type."),
    "Plex does not have an audiobook library type.": t(
      "Plex does not have an audiobook library type.",
    ),
    "Plex does not have a podcast library type.": t("Plex does not have a podcast library type."),
    "Stremio only supports movies, shows, and channels.": t(
      "Stremio only supports movies, shows, and channels.",
    ),
    "Xtream only supports movies, series, and live TV.": t(
      "Xtream only supports movies, series, and live TV.",
    ),
    "Silo only supports movie and TV show libraries.": t(
      "Silo only supports movie and TV show libraries.",
    ),
  };
  return localized[reason] ?? reason;
}

const APP_ORDER: PresentationAppKey[] = [
  "vela",
  "plex",
  "mediaBrowser",
  "stremio",
  "xtream",
  "silo",
];

function fallbackResolved(lib: Library): ResolvedPresentations {
  const isShow = lib.mediaType === "show";
  const p = lib.present ?? {};
  const specs: { app: PresentationAppKey; hint: string }[] = [
    { app: "vela", hint: isShow ? "shows" : "movies" },
    { app: "plex", hint: isShow ? "show" : "movie" },
    { app: "mediaBrowser", hint: isShow ? "tvshows" : "movies" },
    { app: "stremio", hint: isShow ? "series" : "movie" },
    { app: "xtream", hint: isShow ? "series" : "movie" },
    { app: "silo", hint: isShow ? "tvshows" : "movies" },
  ];
  return {
    mediaClass: "video",
    adapterOnly: Boolean(p.adapterOnly),
    effective: p,
    available: {
      vela: { type: isShow ? "shows" : "movies" },
      plex: { base: isShow ? "show" : "movie" },
      mediaBrowser: { collectionType: isShow ? "tvshows" : "movies" },
      stremio: { type: isShow ? "series" : "movie" },
      xtream: { category: isShow ? "series" : "movie" },
      silo: { type: isShow ? "tvshows" : "movies" },
    },
    apps: specs.map(({ app, hint }) => ({
      app,
      available: true,
      enabled: Boolean(!p.adapterOnly && p[app]),
      defaultOn: lib.source === "builtin",
      source: "inferred",
      hintValue: hint,
    })),
  };
}

function initialAppSwitches(resolved: ResolvedPresentations): AppSwitchPatch {
  const out: AppSwitchPatch = { adapterOnly: Boolean(resolved.adapterOnly) };
  for (const app of resolved.apps) {
    out[app.app] = app.available ? app.enabled : false;
  }
  return out;
}

function previewPresent(resolved: ResolvedPresentations, switches: AppSwitchPatch): Presentations {
  if (switches.adapterOnly) return { adapterOnly: true };
  const next: Presentations = {};
  for (const app of resolved.apps) {
    if (app.available && switches[app.app]) {
      const avail = resolved.available[app.app];
      if (avail) (next as Record<string, unknown>)[app.app] = avail;
    }
  }
  return next;
}

function appTitle(app: PresentationAppKey): string {
  switch (app) {
    case "vela":
      return "Vela";
    case "plex":
      return "Plex";
    case "mediaBrowser":
      return t("Jellyfin & Emby");
    case "stremio":
      return "Stremio";
    case "xtream":
      return "Xtream";
    case "silo":
      return "Silo";
  }
}

function libraryKindValue(lib: Library): string {
  if (lib.source === "plugin") {
    const kind = lib.kind?.label?.trim() || lib.kindKey || lib.name;
    const plugin = lib.pluginName?.trim() || lib.pluginId?.trim() || "";
    return plugin ? t("{kind} from {plugin}", { kind, plugin }) : kind;
  }
  return lib.mediaType === "show" ? t("TV shows") : t("Movies");
}

function cardShapeLabel(shape?: string): string {
  switch (shape) {
    case "landscape":
      return t("Wide");
    case "square":
      return t("Square");
    default:
      return t("Poster");
  }
}

function CardShapeRow({
  library,
  value,
  onChange,
}: {
  library: Library;
  value: "" | LibraryCardShape;
  onChange: (value: "" | LibraryCardShape) => void;
}) {
  const isPlugin = library.source === "plugin";
  const pluginDefault = library.kind?.library?.card ?? "poster";
  const choices: { value: "" | LibraryCardShape; label: string }[] = [
    ...(isPlugin
      ? [
          {
            value: "" as const,
            label: t("Plugin default ({shape})", { shape: cardShapeLabel(pluginDefault) }),
          },
        ]
      : []),
    { value: "poster", label: t("Poster") },
    { value: "landscape", label: t("Wide") },
    { value: "square", label: t("Square") },
  ];
  const current: "" | LibraryCardShape =
    value === "poster" || value === "landscape" || value === "square"
      ? value
      : isPlugin
        ? ""
        : "poster";

  return (
    <SettingsRow
      title={t("Card shape")}
      description={t("How thumbnails are shaped when viewers browse this library.")}
      control={
        <div className="w-56">
          <SearchSelect
            value={current}
            onChange={(next) => onChange(next as "" | LibraryCardShape)}
            options={choices}
            ariaLabel={t("Card shape")}
            searchable={false}
          />
        </div>
      }
    />
  );
}

/** "Show in apps", per protocol (spec §6.2, §15.3). Builtins are synthesised. */
function PresentationsEditor({
  library,
  name,
  resolved,
  switches,
  onChange,
}: {
  library: Library;
  name: string;
  resolved: ResolvedPresentations;
  switches: AppSwitchPatch;
  onChange: (value: AppSwitchPatch) => void;
}) {
  const adapterOnly = Boolean(switches.adapterOnly);
  const byApp = new Map<PresentationAppKey, ResolvedAppPresentation>(
    resolved.apps.map((a) => [a.app, a]),
  );

  const appDescription = (info: ResolvedAppPresentation): string => {
    if (!info.available) return unavailableReason(info.reason);
    if (adapterOnly) return t("Off while “Only in its own app” is on.");
    const hint = presentationHint(info.hintValue ?? "");
    if (library.source === "plugin") {
      if (info.source === "plugin") return t("{hint} (from plugin)", { hint });
      if (info.source === "inferred") return t("{hint} (inferred)", { hint });
    }
    return hint;
  };

  return (
    <SettingsSection
      title={t("Show in apps")}
      description={t("Each app shows {name} as its own library. Pick which apps show it.", {
        name: name.trim() || library.name,
      })}
    >
      <SettingsRow
        title={t("Kind")}
        description={t("What this library holds.")}
        control={<span className="text-sm font-medium text-fg">{libraryKindValue(library)}</span>}
      />
      {APP_ORDER.map((key) => {
        const info = byApp.get(key);
        if (!info) return null;
        const title = appTitle(key);
        const disabled = !info.available || adapterOnly;
        const checked = !disabled && Boolean(switches[key]);
        return (
          <SettingsRow
            key={key}
            title={title}
            description={appDescription(info)}
            control={
              <Switch
                aria-label={t("Show in {app}", { app: title })}
                checked={checked}
                disabled={disabled}
                onChange={(e) => onChange({ ...switches, [key]: e.target.checked })}
              />
            }
          />
        );
      })}
      {library.source === "plugin" ? (
        <SettingsRow
          title={t("Only in its own app")}
          description={t(
            "The plugin serves this library itself instead of through the apps above.",
          )}
          control={
            <Switch
              aria-label={t("Only in its own app")}
              checked={adapterOnly}
              onChange={(e) => onChange({ ...switches, adapterOnly: e.target.checked })}
            />
          }
        />
      ) : null}
    </SettingsSection>
  );
}

/**
 * "Quality profile" (spec #419): Default, or a profile from Releases ›
 * Profiles filtered to this library's media type. Titles in this library
 * rank with it wherever they appear.
 */
function QualityProfileRow({
  library,
  value,
  onChange,
}: {
  library: Library;
  value: string;
  onChange: (value: string) => void;
}) {
  const { quality } = useQuality();
  const media = library.mediaType === "show" ? "show" : "movie";
  const profileMedia = media === "show" ? "tv" : "movie";
  const names = (quality?.profiles ?? [])
    .filter((p) => !p.mediaType || p.mediaType === profileMedia)
    .map((p) => p.name);
  const current = value && names.includes(value) ? value : "";

  return (
    <SettingsRow
      title={t("Quality profile")}
      description={t(
        "Titles in this library rank with this profile. Rules and the Away profile still take precedence.",
      )}
      control={
        <div className="w-56">
          <SearchSelect
            value={current}
            onChange={onChange}
            options={[
              { value: "", label: t("Default") },
              ...names.map((name) => ({ value: name, label: name })),
            ]}
            ariaLabel={t("Quality profile")}
            searchable={false}
          />
        </div>
      }
    />
  );
}

export function LibrarySettings({ library, onSaved }: { library: Library; onSaved: () => void }) {
  const navigate = useNavigate();
  const { health } = useApp();
  const collectionSources = useAsync(
    (signal) =>
      library.source === "collection"
        ? librariesApi.listCollectionSources(signal)
        : Promise.resolve([]),
    [library.source],
  );
  const registry = useAsync((signal) => librariesApi.catalogueSources(signal), []);
  const cache = useAsync(
    (signal) =>
      library.source === "plugin" ? librariesApi.cache(library.id, signal) : Promise.resolve(null),
    [library.id, library.source],
  );
  const storedCardShape: "" | LibraryCardShape =
    library.source === "plugin"
      ? library.cardShape || ""
      : library.cardShape === "landscape" || library.cardShape === "square"
        ? library.cardShape
        : "poster";
  const resolvedPresentations = useMemo(
    () => library.presentations ?? fallbackResolved(library),
    [library],
  );
  const stored = useMemo(
    () => ({
      name: library.name,
      hidden: library.hidden,
      cardShape: storedCardShape,
      presentApps: initialAppSwitches(resolvedPresentations),
      qualityProfile: library.qualityProfile ?? "",
      sources: library.sources ?? [],
      collectionId: library.collectionId,
      cacheMaxBytes: cache.data?.maxBytes ?? 0,
      cacheTTL: cache.data?.ttlSeconds ?? 0,
    }),
    [library, resolvedPresentations, storedCardShape, cache.data],
  );
  const { draft, setDraft, dirty, reset } = useDraft(stored);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [editing, setEditing] = useState(false);
  if (library.source === "plugin" && cache.loading && !cache.data) return <LoadingRows />;
  if (!draft) return <LoadingRows />;
  const sourceNames =
    registry.data?.filter((source) => draft.sources.includes(source.id)).map(sourceLabel) ?? [];
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const body: Record<string, unknown> = {
        name: draft.name.trim(),
        hidden: draft.hidden,
      };
      if (draft.cardShape !== stored.cardShape) {
        body.cardShape =
          library.source !== "plugin" && draft.cardShape === "poster" ? "" : draft.cardShape;
      }
      if (library.source !== "plugin" && draft.qualityProfile !== stored.qualityProfile)
        body.qualityProfile = draft.qualityProfile;
      if (
        library.source !== "builtin" &&
        JSON.stringify(draft.presentApps) !== JSON.stringify(stored.presentApps)
      ) {
        const patch: AppSwitchPatch = {};
        if (draft.presentApps.adapterOnly !== stored.presentApps.adapterOnly)
          patch.adapterOnly = draft.presentApps.adapterOnly;
        for (const app of resolvedPresentations.apps) {
          if (app.available && draft.presentApps[app.app] !== stored.presentApps[app.app]) {
            patch[app.app] = draft.presentApps[app.app];
          }
        }
        body.presentApps = patch;
      }
      if (JSON.stringify(draft.sources) !== JSON.stringify(stored.sources))
        body.sources = draft.sources;
      if (draft.collectionId !== stored.collectionId) body.collectionId = draft.collectionId;
      await librariesApi.patch(library.id, body);
      if (
        cache.data &&
        (draft.cacheMaxBytes !== stored.cacheMaxBytes || draft.cacheTTL !== stored.cacheTTL)
      )
        await librariesApi.setCache(library.id, draft.cacheMaxBytes, draft.cacheTTL);
      onSaved();
      void cache.reload();
      toast.success(t("Settings saved"));
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  const summaryLibrary = {
    ...library,
    hidden: draft.hidden,
    present: previewPresent(resolvedPresentations, draft.presentApps),
  };
  return (
    <>
      <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_300px]">
        <div className="min-w-0 space-y-5">
          <TitlesComeFrom
            library={library}
            sources={draft.sources}
            onChange={(sources) => setDraft({ ...draft, sources })}
          >
            {library.source === "collection" ? (
              <SettingsRow
                title={t("Collection")}
                description={
                  collectionSources.data?.find((c) => c.id === draft.collectionId)?.name ??
                  t("Collection")
                }
                control={
                  <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
                    {t("Change")}
                  </Button>
                }
              />
            ) : null}
          </TitlesComeFrom>
          <SettingsSection title={t("Basics")}>
            <SettingsRow
              title={t("Name")}
              description={t("The name viewers see in their apps.")}
              control={
                <Input
                  aria-label={t("Library name")}
                  value={draft.name}
                  onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                />
              }
            />
            <CardShapeRow
              library={library}
              value={draft.cardShape}
              onChange={(cardShape) => setDraft({ ...draft, cardShape })}
            />
            <SettingsRow
              title={t("Hide from apps")}
              description={t("The library stays in this console but disappears from every app.")}
              control={
                <Switch
                  aria-label={t("Hide from apps")}
                  checked={draft.hidden}
                  onChange={(e) => setDraft({ ...draft, hidden: e.target.checked })}
                />
              }
            />
            <SettingsRow
              title={t("Sharing")}
              description={t("Choose who can open each library on the Viewers page.")}
              control={
                <Link className={buttonVariants({ variant: "outline", size: "sm" })} to="/viewers">
                  {t("Open Viewers")}
                </Link>
              }
            />
          </SettingsSection>
          {library.source === "builtin" ? (
            <SettingsSection title={t("Show in apps")}>
              <SettingsRow
                title={t("Show in apps")}
                description={t(
                  "Movies and TV Shows appear in every app. Use “Hide from apps” above to hide one.",
                )}
                control={
                  <Link className={buttonVariants({ variant: "outline", size: "sm" })} to="/apps">
                    {t("Apps")}
                  </Link>
                }
              />
            </SettingsSection>
          ) : (
            <PresentationsEditor
              library={library}
              name={draft.name}
              resolved={resolvedPresentations}
              switches={draft.presentApps}
              onChange={(presentApps) => setDraft({ ...draft, presentApps })}
            />
          )}
          {library.source !== "plugin" && (
            <SettingsSection title={t("Quality")}>
              <QualityProfileRow
                library={library}
                value={draft.qualityProfile}
                onChange={(qualityProfile) => setDraft({ ...draft, qualityProfile })}
              />
            </SettingsSection>
          )}
          {library.source === "plugin" && library.kind?.sync?.mode !== "push" ? (
            <PluginLibrarySyncSection library={library} onChanged={onSaved} />
          ) : null}
          {cache.data ? (
            <LibraryCacheCard
              library={library}
              data={cache.data}
              maxBytes={draft.cacheMaxBytes}
              ttlSeconds={draft.cacheTTL}
              onChange={(cacheMaxBytes, cacheTTL) =>
                setDraft({ ...draft, cacheMaxBytes, cacheTTL })
              }
              onReload={() => void cache.reload()}
            />
          ) : null}
          {cache.error ? (
            <ErrorPanel error={cache.error} onRetry={() => void cache.reload()} />
          ) : null}
          {library.source === "catalogue" ||
          library.source === "collection" ||
          library.source === "plugin" ? (
            <SettingsSection title={t("Remove")}>
              <SettingsRow
                title={t("Delete this library")}
                description={t("Media and watch history are kept.")}
                control={
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={saving}
                    onClick={() => {
                      if (
                        window.confirm(
                          t("Delete the library “{name}”? Media and watch history are kept.", {
                            name: library.name,
                          }),
                        )
                      )
                        void librariesApi
                          .remove(library.id)
                          .then(() => navigate("/libraries"))
                          .catch((e) => toast.error(errorMessage(e)));
                    }}
                  >
                    {t("Delete")}
                  </Button>
                }
              />
            </SettingsSection>
          ) : null}
        </div>
        <aside className="rounded-xl border border-border bg-surface p-5 xl:sticky xl:top-5">
          <div className="flex items-center gap-3">
            {library.source === "plugin" ? (
              <Plug size={24} />
            ) : library.mediaType === "show" ? (
              <Tv size={24} />
            ) : (
              <Film size={24} />
            )}
            <h2 className="font-semibold">{draft.name}</h2>
          </div>
          {library.itemCount !== undefined ? (
            <p className="mt-4 text-2xl font-semibold">
              {tn(library.itemCount, "{count} title", "{count} titles")}
            </p>
          ) : null}
          {library.source === "plugin" &&
          typeof (library.storedItems ?? library.itemCount) === "number" &&
          typeof library.maxPluginItems === "number" &&
          library.maxPluginItems > 0 ? (
            <p className="mt-1 text-xs text-fg-muted">
              {formatStoredCapSummary(
                library.storedItems ?? library.itemCount ?? 0,
                library.maxPluginItems,
              )}
            </p>
          ) : null}
          <div className="mt-3">
            {library.source === "plugin" && library.sync?.running ? (
              <Badge variant="secondary" className="gap-1">
                <RefreshCw size={11} className="animate-spin" aria-hidden />
                {library.sync.phase === "reindexing" ? t("Reindexing") : t("Syncing")}
              </Badge>
            ) : (
              <LevelBadge
                label={
                  library.source === "plugin" && resolveDeleteGuard(library.sync)
                    ? t("Needs review")
                    : sourceNames.length > 0 &&
                        registry.data
                          ?.filter((s) => draft.sources.includes(s.id))
                          .every((s) => s.health === "unknown")
                      ? t("Not loaded yet")
                      : undefined
                }
                level={
                  library.source === "plugin" && resolveDeleteGuard(library.sync)
                    ? "warn"
                    : library.sync?.lastError
                      ? "error"
                      : sourceNames.length > 0 &&
                          registry.data
                            ?.filter((s) => draft.sources.includes(s.id))
                            .every((s) => s.health === "unknown")
                        ? "unknown"
                        : registry.data?.some(
                              (source) =>
                                draft.sources.includes(source.id) &&
                                ["error", "attention"].includes(source.health),
                            )
                          ? "warn"
                          : (health.find((item) => item.id === "catalog")?.level ?? "unknown")
                }
              />
            )}
          </div>
          <p className="mt-4 text-sm text-fg-muted">
            {t("Titles from: {sources}", { sources: sourceNames.join(", ") })}
          </p>
          <p className="mt-3 text-sm text-fg-muted">
            {draft.hidden
              ? t("Hidden from apps")
              : publishedTo(summaryLibrary).length
                ? t("In apps: {apps}", {
                    apps: publishedTo(summaryLibrary)
                      .map(
                        (app) =>
                          ({
                            vela: "Vela",
                            mediaBrowser: t("Jellyfin & Emby"),
                            plex: "Plex",
                            stremio: "Stremio",
                            xtream: "Xtream",
                            silo: "Silo",
                            adapter: t("Its own app"),
                          })[app] ?? app,
                      )
                      .join(", "),
                  })
                : t("Not shown in any app yet")}
          </p>
        </aside>
      </div>
      <SaveBar
        dirty={dirty}
        saving={saving}
        disabled={!draft.name.trim()}
        error={error}
        onSave={() => void save()}
        onDiscard={() => {
          reset();
          setError(null);
        }}
      />
      {editing ? (
        <NarrowingEditor
          library={{ ...library, collectionId: draft.collectionId }}
          onClose={() => setEditing(false)}
          onChange={(collectionId) => {
            setDraft({
              ...draft,
              ...(collectionId ? { collectionId } : {}),
            });
            setEditing(false);
          }}
        />
      ) : null}
    </>
  );
}

function PluginLibrarySyncSection({
  library,
  onChanged,
}: {
  library: Library;
  onChanged: () => void;
}) {
  const [busyAction, setBusyAction] = useState<"sync" | "full" | "keep" | "force" | null>(null);
  const running = Boolean(library.sync?.running);
  const guard = running ? null : resolveDeleteGuard(library.sync);
  const capSummary = formatItemStorageSummary(library);
  const status = guard
    ? ""
    : formatSyncStatusLine(library.sync, {
        count: library.storedItems ?? library.itemCount ?? 0,
      });
  const summaryLine = capSummary && status ? `${capSummary} · ${status}` : capSummary || status;
  const isBusy = running || Boolean(busyAction);

  const runSync = async (action: "sync" | "full" | "keep" | "force", opts: LibrarySyncRequest) => {
    setBusyAction(action);
    try {
      const res = await librariesApi.sync(library.id, opts);
      notifySyncResponse(res);
    } catch (e) {
      notifySyncError(e);
    } finally {
      onChanged();
      setBusyAction(null);
    }
  };

  return (
    <SettingsSection title={t("Library sync")}>
      <SettingsRow
        title={library.kind?.label ?? library.name}
        description={
          <div className="space-y-1">
            {summaryLine ? <span>{summaryLine}</span> : null}
            {guard ? (
              <DeleteGuardChoice
                guard={guard}
                busyAction={busyAction}
                onKeep={() => void runSync("keep", { keepExisting: true })}
                onRemoveMissing={() =>
                  void runSync("force", {
                    force: true,
                    fullWalk: guard.source === "walk",
                  })
                }
              />
            ) : null}
          </div>
        }
        control={
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={isBusy}
              onClick={() => void runSync("full", { fullWalk: true })}
            >
              {busyAction === "full" ? (
                <RefreshCw size={13} className="animate-spin" aria-hidden />
              ) : null}
              {t("Full re-sync")}
            </Button>
            <Button
              size="sm"
              disabled={isBusy}
              onClick={() => void runSync("sync", { fullWalk: false })}
            >
              <RefreshCw
                size={13}
                className={cn((busyAction === "sync" || running) && "animate-spin")}
                aria-hidden
              />{" "}
              {running ? t("Syncing…") : t("Sync now")}
            </Button>
          </div>
        }
      />
    </SettingsSection>
  );
}
