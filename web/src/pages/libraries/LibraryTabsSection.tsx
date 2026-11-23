import {
  Badge,
  Button,
  EmptyState,
  SettingsSection,
  Spinner,
  Switch,
  cn,
  toast,
} from "@braedonsaunders/appkit-ui";
import { LayoutList, Pencil, Plus, RotateCcw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ErrorPanel } from "../../components/feedback";
import { DragHandle, SortableList } from "../../components/Sortable";
import { t, tn } from "../../i18n";
import { api, errorMessage } from "../../lib/api";
import type { HomeLayout, LibraryLayoutSection } from "../../lib/contract";
import { useAsync } from "../../lib/hooks";
import { EMPTY_LAYOUT, collectionScheduleChanges, pageOf, prune } from "../../lib/layout";
import {
  librariesApi,
  type Library,
  type LibraryAvailableFacets,
  type LibraryTab,
  type LibraryTabKind,
} from "./api";
import {
  AddTabPickerDrawer,
  TabEditDrawer,
  createSeedTab,
  facetOptionLabel,
  sortFieldLabel,
  supportsLibraryRecommendedRows,
  tabKindIcon,
} from "./TabEditDrawer";

const VALID_NEW_KINDS = new Set<LibraryTabKind>([
  "recommended",
  "library",
  "collections",
  "people",
  "view",
  "live",
]);

const MAX_LIBRARY_TABS = 50;

const EMPTY_FACETS: LibraryAvailableFacets = { collections: [], people: [] };

function canonicalTab(tab: LibraryTab): LibraryTab {
  return {
    key: tab.key,
    label: tab.label,
    kind: tab.kind,
    ...(tab.facet ? { facet: tab.facet } : {}),
    ...(tab.order ? { order: tab.order } : {}),
    ...(tab.sort ? { sort: tab.sort } : {}),
    ...(tab.direction ? { direction: tab.direction } : {}),
    ...(tab.kind === "live" ? { card: tab.card ?? "landscape" } : {}),
    ...(tab.kind === "view" ? { layout: tab.layout ?? "grid" } : {}),
    ...(tab.kind !== "recommended" ? { search: tab.search !== false } : {}),
    ...(tab.kind !== "recommended" && tab.search !== false && tab.searchPlaceholder
      ? { searchPlaceholder: tab.searchPlaceholder }
      : {}),
    ...(tab.filters ? { filters: tab.filters } : {}),
    ...(tab.hidden ? { hidden: true } : {}),
  };
}

function summarizeTab(
  tab: LibraryTab,
  availableFacets: LibraryAvailableFacets,
  recommendedCustomised: boolean,
): string {
  if (tab.kind === "recommended") {
    return recommendedCustomised ? t("Personalized rows · Custom rows") : t("Personalized rows");
  }
  const parts: string[] = [];
  if (tab.kind === "live") {
    parts.push(t("Live streams"));
    const card = tab.card ?? "landscape";
    parts.push(
      card === "poster"
        ? t("Poster cards")
        : card === "square"
          ? t("Square cards")
          : t("Landscape cards"),
    );
  } else if (tab.kind === "library") {
    parts.push(t("Everything"));
    const ruleCount = tab.filters?.rules.length ?? 0;
    if (ruleCount > 0) {
      parts.push(tn(ruleCount, "{count} filter rule", "{count} filter rules"));
    }
  } else if (tab.kind === "collections") {
    if (!tab.facet) {
      parts.push(t("Owner collections"));
    } else {
      const opt = availableFacets.collections.find((f) => f.id === tab.facet);
      const facetLabel = opt ? facetOptionLabel(opt) : tab.facet;
      parts.push(
        facetLabel ? t("Collections by {facet}", { facet: facetLabel }) : t("Collections"),
      );
      parts.push(tab.order === "name" ? t("Name A–Z") : t("Most items first"));
    }
  } else if (tab.kind === "people") {
    const opt = availableFacets.people.find((f) => f.id === tab.facet);
    const facetLabel = opt ? facetOptionLabel(opt) : (tab.facet ?? "");
    parts.push(facetLabel ? t("People in {facet}", { facet: facetLabel }) : t("People"));
    parts.push(tab.order === "count" ? t("Most titles") : t("Name A–Z"));
  } else if (tab.kind === "view") {
    parts.push(t("Saved view"));
    const ruleCount = tab.filters?.rules.length ?? 0;
    parts.push(tn(ruleCount, "{count} rule", "{count} rules"));
    if (tab.layout === "list") {
      parts.push(t("List"));
    } else if (tab.layout === "timeline") {
      parts.push(t("Timeline"));
    }
  }

  if (tab.kind !== "people" && (tab.kind !== "collections" || Boolean(tab.facet)) && tab.sort) {
    parts.push(sortFieldLabel(tab.sort));
  }
  if (tab.search === false) {
    parts.push(t("No search"));
  }
  return parts.join(" · ");
}

type DrawerMode =
  | { type: "closed" }
  | { type: "pick" }
  | { type: "new"; kind: LibraryTabKind }
  | { type: "edit"; key: string };

export function LibraryTabsSection({
  library,
  onChanged,
}: {
  library: Library;
  onChanged?: () => void;
}) {
  const [searchParams, setSearchParams] = useSearchParams();
  const state = useAsync((signal) => librariesApi.tabs(library.id, signal), [library.id]);
  const supportsRecommendedRows = supportsLibraryRecommendedRows(library);
  const libraryLayoutDoc = useAsync(
    (signal) =>
      supportsRecommendedRows ? api.libraryLayout(library.id, signal) : Promise.resolve(null),
    [library.id, supportsRecommendedRows],
  );
  const [saving, setSaving] = useState(false);
  const [localDrawer, setLocalDrawer] = useState<DrawerMode>({ type: "closed" });

  const librarySection: LibraryLayoutSection = `library:${library.id}`;
  const recommendedCustomised = Boolean(libraryLayoutDoc.data?.customized);

  const clearTabParams = () => {
    if (
      !searchParams.has("addTab") &&
      !searchParams.has("newTab") &&
      !searchParams.has("editTab")
    ) {
      return;
    }
    const next = new URLSearchParams(searchParams);
    next.delete("addTab");
    next.delete("newTab");
    next.delete("editTab");
    setSearchParams(next, { replace: true });
  };

  const closeDrawers = () => {
    setLocalDrawer({ type: "closed" });
    clearTabParams();
  };

  const urlEditKey = searchParams.get("editTab");
  const urlNewKind = searchParams.get("newTab") as LibraryTabKind | null;
  const urlAdd = searchParams.get("addTab") === "1";

  const activeDrawer: DrawerMode =
    localDrawer.type !== "closed"
      ? localDrawer
      : urlEditKey
        ? { type: "edit", key: urlEditKey }
        : urlNewKind &&
            VALID_NEW_KINDS.has(urlNewKind) &&
            (urlNewKind !== "live" || library.source === "plugin")
          ? { type: "new", kind: urlNewKind }
          : urlAdd
            ? { type: "pick" }
            : { type: "closed" };

  const tabs = state.data?.effectiveTabs ?? state.data?.tabs ?? [];
  const customized = state.data?.customized ?? false;
  const availableFacets = state.data?.availableFacets ?? EMPTY_FACETS;
  const availableSorts = state.data?.availableSorts ?? [];
  const visibleTabCount = tabs.filter((tab) => !tab.hidden).length;
  const resetLabel =
    library.source === "plugin" ? t("Reset to plugin tabs") : t("Reset to default");

  const persistTabs = async (nextTabs: LibraryTab[], message: string) => {
    const previous = state.data;
    if (previous) {
      state.setData({
        ...previous,
        customized: true,
        tabs: nextTabs,
        effectiveTabs: nextTabs,
      });
    }
    setSaving(true);
    try {
      const updated = await librariesApi.putTabs(library.id, nextTabs);
      state.setData(updated);
      onChanged?.();
      toast.success(message);
      return true;
    } catch (e) {
      if (previous) state.setData(previous);
      toast.error(errorMessage(e));
      return false;
    } finally {
      setSaving(false);
    }
  };

  const handleSaveDrawer = async (savedTab: LibraryTab, nextLayout?: HomeLayout) => {
    const isNew = activeDrawer.type === "new";
    const tabChanged =
      isNew ||
      !editingExisting ||
      JSON.stringify(canonicalTab(savedTab)) !== JSON.stringify(canonicalTab(editingExisting));
    const nextTabs = isNew
      ? [...tabs, savedTab]
      : tabs.map((item) => (item.key === savedTab.key ? savedTab : item));

    const previousTabs = state.data;
    setSaving(true);
    try {
      if (nextLayout) {
        const savedLayout = libraryLayoutDoc.data
          ? prune(libraryLayoutDoc.data.layout)
          : EMPTY_LAYOUT;
        const schedules = collectionScheduleChanges(nextLayout, savedLayout);
        const nextPage = pageOf(nextLayout, "global", librarySection);
        const updatedLayout =
          !nextPage && Object.keys(schedules).length === 0
            ? await api.deleteLibraryLayout(library.id)
            : await api.putLibraryLayout(library.id, {
                page: nextPage ?? { section: librarySection, rows: [] },
                ...(nextPage ? {} : { viewerPages: [] }),
                ...(Object.keys(schedules).length ? { collectionSchedules: schedules } : {}),
              });
        libraryLayoutDoc.setData(updatedLayout);
      }

      if (tabChanged || !nextLayout) {
        if (previousTabs) {
          state.setData({
            ...previousTabs,
            customized: true,
            tabs: nextTabs,
            effectiveTabs: nextTabs,
          });
        }
        const updatedTabs = await librariesApi.putTabs(library.id, nextTabs);
        state.setData(updatedTabs);
        onChanged?.();
      }

      toast.success(
        isNew
          ? t("Added {label} tab", { label: savedTab.label })
          : t("Saved {label} tab", { label: savedTab.label }),
      );
      closeDrawers();
    } catch (e) {
      if (previousTabs && (tabChanged || !nextLayout)) {
        state.setData(previousTabs);
      }
      toast.error(errorMessage(e));
      return;
    } finally {
      setSaving(false);
    }
  };

  const handleReset = async () => {
    setSaving(true);
    try {
      const updated = await librariesApi.resetTabs(library.id);
      state.setData(updated);
      onChanged?.();
      closeDrawers();
      toast.success(
        library.source === "plugin" ? t("Restored plugin tabs") : t("Restored default tabs"),
      );
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const existingKeys = new Set(tabs.map((tab) => tab.key));
  const editingExisting =
    activeDrawer.type === "edit"
      ? (tabs.find((tab) => tab.key === activeDrawer.key) ?? null)
      : null;
  const seededNew =
    activeDrawer.type === "new" && state.data
      ? createSeedTab(activeDrawer.kind, library, availableFacets, availableSorts, existingKeys)
      : null;

  const editingIsOnlyVisible = Boolean(
    editingExisting && !editingExisting.hidden && visibleTabCount <= 1,
  );

  return (
    <SettingsSection title={t("Tabs")}>
      <div className="space-y-4 p-4 sm:p-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 flex-1 space-y-1">
            <p className="text-sm text-fg-muted">
              {t("Tabs show in Vela. Plex, Jellyfin and Emby keep their own library tabs.")}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={customized ? "info" : "secondary"}>
              {customized
                ? t("Customized")
                : library.source === "plugin"
                  ? t("Plugin default")
                  : t("Default")}
            </Badge>
            {tabs.length >= 12 ? (
              <Badge variant={tabs.length >= MAX_LIBRARY_TABS ? "warning" : "secondary"}>
                {t("{count} / {max} tabs", { count: tabs.length, max: MAX_LIBRARY_TABS })}
              </Badge>
            ) : null}
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={!customized || saving || state.loading}
              onClick={() => void handleReset()}
            >
              <RotateCcw size={13} />
              {resetLabel}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={saving || !state.data || tabs.length >= MAX_LIBRARY_TABS}
              onClick={() => {
                clearTabParams();
                setLocalDrawer({ type: "pick" });
              }}
            >
              <Plus size={14} />
              {t("Add tab")}
            </Button>
          </div>
        </div>

        {state.error ? (
          <ErrorPanel error={state.error} onRetry={() => void state.reload()} />
        ) : state.loading && !state.data ? (
          <div className="flex items-center gap-2 py-6 text-sm text-fg-muted">
            <Spinner size={16} label={t("Loading tabs")} />
            <span>{t("Loading tabs…")}</span>
          </div>
        ) : tabs.length === 0 ? (
          <EmptyState
            icon={<LayoutList />}
            title={t("No tabs configured")}
            description={t(
              "Add tabs to choose how viewers browse this library in Vela, or reset to restore the standard tabs.",
            )}
            action={
              <div className="flex flex-wrap items-center justify-center gap-2">
                <Button
                  type="button"
                  size="sm"
                  onClick={() => {
                    clearTabParams();
                    setLocalDrawer({ type: "pick" });
                  }}
                >
                  <Plus size={14} />
                  {t("Add tab")}
                </Button>
                {customized ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={saving}
                    onClick={() => void handleReset()}
                  >
                    <RotateCcw size={13} />
                    {resetLabel}
                  </Button>
                ) : null}
              </div>
            }
          />
        ) : (
          <div className={cn(tabs.length > 10 && "max-h-[34rem] overflow-y-auto pr-1")}>
            <SortableList
              items={tabs}
              getId={(tab) => tab.key}
              onReorder={(next) => {
                void persistTabs(next, t("Tab order saved"));
              }}
              className="space-y-2"
              renderItem={(tab, handle) => {
                const isOnlyVisible = !tab.hidden && visibleTabCount <= 1;
                return (
                  <div
                    className={cn(
                      "flex flex-wrap items-center gap-2 rounded-xl border border-border bg-surface px-2.5 py-2 transition-colors sm:flex-nowrap sm:gap-3 sm:px-3 sm:py-2.5",
                      tab.hidden && "bg-bg-subtle/50 opacity-75",
                    )}
                  >
                    <DragHandle
                      handle={handle}
                      label={t("Reorder {label} tab", { label: tab.label })}
                    />
                    <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-primary-subtle text-primary">
                      {tabKindIcon(tab.kind, 16)}
                    </span>
                    <div className="min-w-0 flex-1">
                      <button
                        type="button"
                        onClick={() => {
                          clearTabParams();
                          setLocalDrawer({ type: "edit", key: tab.key });
                        }}
                        className="flex max-w-full flex-wrap items-center gap-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                      >
                        <span className="truncate text-sm font-medium text-fg hover:underline">
                          {tab.label}
                        </span>
                        {tab.hidden ? <Badge variant="outline">{t("Hidden")}</Badge> : null}
                      </button>
                      <p className="truncate text-xs text-fg-muted">
                        {summarizeTab(tab, availableFacets, recommendedCustomised)}
                      </p>
                    </div>
                    <div className="ml-auto flex shrink-0 items-center gap-1.5">
                      <Switch
                        aria-label={t("Display {label} tab", { label: tab.label })}
                        checked={!tab.hidden}
                        disabled={saving || isOnlyVisible}
                        onChange={(e) => {
                          const visible = e.target.checked;
                          const next = tabs.map((item) =>
                            item.key === tab.key
                              ? { ...item, hidden: visible ? undefined : true }
                              : item,
                          );
                          void persistTabs(
                            next,
                            visible
                              ? t("{label} tab shown", { label: tab.label })
                              : t("{label} tab hidden", { label: tab.label }),
                          );
                        }}
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="size-8 text-fg-muted hover:text-fg"
                        aria-label={t("Edit {label} tab", { label: tab.label })}
                        onClick={() => {
                          clearTabParams();
                          setLocalDrawer({ type: "edit", key: tab.key });
                        }}
                      >
                        <Pencil size={15} />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="size-8 text-fg-muted hover:text-danger"
                        aria-label={t("Delete {label} tab", { label: tab.label })}
                        disabled={saving || tabs.length <= 1 || isOnlyVisible}
                        onClick={() => {
                          const next = tabs.filter((item) => item.key !== tab.key);
                          void persistTabs(next, t("Removed {label} tab", { label: tab.label }));
                        }}
                      >
                        <Trash2 size={15} />
                      </Button>
                    </div>
                  </div>
                );
              }}
            />
          </div>
        )}
      </div>

      <AddTabPickerDrawer
        open={activeDrawer.type === "pick"}
        library={library}
        availableFacets={availableFacets}
        existingTabs={tabs}
        onPick={(kind) => {
          clearTabParams();
          setLocalDrawer({ type: "new", kind });
        }}
        onClose={closeDrawers}
      />

      <TabEditDrawer
        open={
          (activeDrawer.type === "new" && seededNew !== null) ||
          (activeDrawer.type === "edit" && editingExisting !== null)
        }
        isNew={activeDrawer.type === "new"}
        initialTab={activeDrawer.type === "new" ? seededNew : editingExisting}
        library={library}
        availableFacets={availableFacets}
        availableSorts={availableSorts}
        existingKeys={
          activeDrawer.type === "edit" && editingExisting
            ? new Set([...existingKeys].filter((k) => k !== editingExisting.key))
            : existingKeys
        }
        canHide={activeDrawer.type === "new" ? visibleTabCount >= 1 : !editingIsOnlyVisible}
        canDelete={
          activeDrawer.type === "edit" &&
          Boolean(editingExisting) &&
          tabs.length > 1 &&
          !editingIsOnlyVisible
        }
        libraryLayoutDoc={libraryLayoutDoc.data ?? null}
        layoutLoading={libraryLayoutDoc.loading}
        saving={saving}
        onClose={closeDrawers}
        onDelete={
          activeDrawer.type === "edit" && editingExisting
            ? () => {
                const next = tabs.filter((item) => item.key !== editingExisting.key);
                void persistTabs(
                  next,
                  t("Removed {label} tab", { label: editingExisting.label }),
                ).then((ok) => {
                  if (ok) closeDrawers();
                });
              }
            : undefined
        }
        onSave={(savedTab, nextLayout) => {
          void handleSaveDrawer(savedTab, nextLayout);
        }}
      />
    </SettingsSection>
  );
}
