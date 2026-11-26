import {
  Badge,
  Button,
  EmptyState,
  Input,
  Skeleton,
  confirmDialog,
  toast,
  cn,
} from "@braedonsaunders/appkit-ui";
import {
  ArrowRight,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  EyeOff,
  Folder,
  Globe2,
  Layers,
  LayoutGrid,
  PanelLeft,
  LayoutTemplate,
  Pencil,
  Plus,
  Puzzle,
  RefreshCw,
  Search,
  Sparkles,
  Trash2,
  Undo2,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { PluginActionMenu } from "../../viewspec/slots/Slots";
import { Artwork } from "../../components/Artwork";
import { ErrorPanel } from "../../components/feedback";
import { ToolbarPicker } from "../../components/ToolbarPicker";
import { PanelHead, Workspace } from "../../components/workspace";
import { api, errorMessage } from "../../lib/api";
import type { CollectionSpec, CollectionTemplateBundlePreview } from "../../lib/contract";
import { t, tn } from "../../i18n";
import { relativeTime } from "../../lib/format";
import { clearDraft, draftKey, listDrafts } from "../../lib/history";
import { useAsync } from "../../lib/hooks";
import { addToPageHref } from "../../lib/recommendations";
import { normalizeSpec } from "../../lib/rules";
import { CollectionDetail } from "./CollectionDetail";
import { DescribeDialog } from "./DescribeDialog";
import { StremioImportDrawer } from "./StremioImportDrawer";
import { isBlankCollectionSpec, newCollectionHref } from "./links";
import { bundleDescription, bundleTitle } from "../setup/StarterBundlesCard";

type Tab = "mine" | "templates";
type Filter = "all" | "live" | "draft";

function loadUnsavedNewDrafts() {
  return listDrafts<CollectionSpec>("collection").filter((d) => {
    if (!d.id.startsWith("new-")) return false;
    if (isBlankCollectionSpec(d.draft.value)) {
      clearDraft(draftKey("collection", d.id));
      return false;
    }
    return true;
  });
}

export default function CollectionsGallery({
  browseSearch,
  active = true,
}: {
  browseSearch?: string;
  active?: boolean;
}) {
  const [routeParams, setParams] = useSearchParams();
  const params = browseSearch === undefined ? routeParams : new URLSearchParams(browseSearch);
  const rawTab = params.get("tab");
  const tab: Tab = rawTab === "templates" ? rawTab : "mine";
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [category, setCategory] = useState("all");
  const [describe, setDescribe] = useState(false);
  const [importDrawer, setImportDrawer] = useState<{ open: boolean; addonId?: string }>({
    open: false,
  });
  const [collapsedFolders, setCollapsedFolders] = useState<Set<string>>(() => new Set());
  const [busy, setBusy] = useState(false);
  const [bundleBusy, setBundleBusy] = useState<string | null>(null);
  const [bundlePreview, setBundlePreview] = useState<CollectionTemplateBundlePreview | null>(null);
  const navigate = useNavigate();
  const collections = useAsync(() => api.collections(), []);
  const templates = useAsync(() => api.templates(), []);
  const bundles = useAsync(() => api.templateBundles(), []);
  const [drafts, setDrafts] = useState(loadUnsavedNewDrafts);
  const importAddonParam = params.get("importAddon");
  useEffect(() => {
    if (importAddonParam) {
      setImportDrawer({ open: true, addonId: importAddonParam });
    }
  }, [importAddonParam]);
  const closeImportDrawer = () => {
    setImportDrawer({ open: false });
    if (params.has("importAddon")) {
      const p = new URLSearchParams(params);
      p.delete("importAddon");
      setParams(p, { replace: true });
    }
  };
  const wasActive = useRef(active);
  useEffect(() => {
    if (active && !wasActive.current) {
      void collections.reload();
      setDrafts(loadUnsavedNewDrafts());
    }
    wasActive.current = active;
  }, [active, collections.reload]);
  const mine = collections.data ?? [];
  const needle = q.trim().toLowerCase();
  const match = (s: { name: string; description?: string; folder?: string }) =>
    !needle || `${s.name} ${s.description ?? ""} ${s.folder ?? ""}`.toLowerCase().includes(needle);
  const items = mine.filter(
    (c) => match(c) && (filter === "all" || c.enabled === (filter === "live")),
  );
  const categories = [...new Set((templates.data ?? []).map((tpl) => tpl.category))];
  const shownTemplates = (templates.data ?? []).filter(
    (tpl) =>
      match({ name: t(tpl.name), description: t(tpl.description) }) &&
      (category === "all" || tpl.category === category),
  );
  const template =
    tab === "templates"
      ? (shownTemplates.find((tpl) => tpl.id === params.get("template")) ?? shownTemplates[0])
      : undefined;
  const selected =
    tab !== "templates"
      ? (items.find((c) => c.id === params.get("collection")) ?? items[0])
      : undefined;
  const state = tab === "templates" ? templates : collections;
  const posterView = tab !== "templates" && params.get("view") === "posters";
  const previewLabel = t("Collection preview");
  const setView = (posters: boolean) => {
    const p = new URLSearchParams(params);
    if (posters) p.set("view", "posters");
    else p.delete("view");
    setParams(p);
  };
  const setTab = (next: Tab) => {
    const p = new URLSearchParams(params);
    p.set("tab", next);
    p.delete("collection");
    p.delete("template");
    setParams(p);
    setQ("");
  };
  const select = (key: "collection" | "template", id: string, openPreview = false) => {
    const p = new URLSearchParams(params);
    p.set(key, id);
    if (openPreview) p.delete("view");
    setParams(p);
    if (window.matchMedia("(max-width: 1023px)").matches)
      document
        .querySelector(`[aria-label="${previewLabel.replace(/"/g, '\\"')}"]`)
        ?.scrollIntoView({ block: "start", behavior: "smooth" });
  };
  const edit = (c: CollectionSpec, section?: "artwork") =>
    navigate(`/collections/${encodeURIComponent(c.id!)}${section ? `?section=${section}` : ""}`);
  const duplicate = (c: CollectionSpec) =>
    navigate(newCollectionHref(), {
      state: {
        spec: {
          ...c,
          id: undefined,
          addonSync: undefined,
          name: t("{name} (copy)", { name: c.name }),
          enabled: false,
        },
      },
    });
  const previewBundle = async (bundleId: string) => {
    if (bundleBusy) return;
    if (bundlePreview?.bundle.id === bundleId) {
      setBundlePreview(null);
      return;
    }
    setBundleBusy(bundleId);
    try {
      const res = await api.previewTemplateBundle(bundleId);
      setBundlePreview(res);
    } catch (e) {
      toast.error(t("That didn't work"), { description: errorMessage(e) });
    } finally {
      setBundleBusy(null);
    }
  };
  const applyBundle = async (bundleId: string) => {
    if (bundleBusy) return;
    setBundleBusy(bundleId);
    try {
      const res = await api.applyTemplateBundle(bundleId);
      await collections.reload();
      if (bundlePreview?.bundle.id === bundleId) {
        const nextPreview = await api.previewTemplateBundle(bundleId);
        setBundlePreview(nextPreview);
      }
      toast.success(t("Template bundle applied"), {
        description: tn(
          res.createdCount,
          "Created {count} collection ({skipped} already present).",
          "Created {count} collections ({skipped} already present).",
          { skipped: res.skippedCount },
        ),
      });
    } catch (e) {
      toast.error(t("That didn't work"), { description: errorMessage(e) });
    } finally {
      setBundleBusy(null);
    }
  };
  const act = async (c: CollectionSpec, action: "publish" | "unpublish" | "rebuild" | "delete") => {
    if (!c.id || busy) return;
    if (
      action === "delete" &&
      !(await confirmDialog({
        title: t("Delete collection?"),
        message: t("“{name}” disappears from Plex for everyone.", { name: c.name }),
        confirmLabel: t("Delete"),
        tone: "danger",
      }))
    )
      return;
    setBusy(true);
    try {
      if (action === "delete") {
        await api.deleteCollection(c.id);
        collections.setData((list) => (list ?? []).filter((x) => x.id !== c.id));
        toast.success(t("Collection deleted"));
      } else if (action === "rebuild") {
        const r = await api.rebuildCollection(c.id);
        collections.setData((list) =>
          (list ?? []).map((x) =>
            x.id === c.id ? { ...x, itemCount: r.itemCount, lastBuiltAt: r.lastBuiltAt } : x,
          ),
        );
        toast.success(t("Rebuilt"), {
          description: tn(r.itemCount, "{name}: {count} title.", "{name}: {count} titles.", {
            name: c.name,
          }),
        });
      } else {
        const saved = await api.updateCollection(c.id, {
          ...normalizeSpec(c),
          enabled: action === "publish",
        });
        collections.setData((list) => (list ?? []).map((x) => (x.id === c.id ? saved : x)));
        toast.success(action === "publish" ? t("Published") : t("Unpublished"), {
          description: c.name,
        });
      }
    } catch (e) {
      toast.error(t("That didn't work"), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Workspace
        title={t("Collections")}
        actions={
          <>
            {tab !== "templates" ? (
              <div
                role="group"
                aria-label={t("Collection layout")}
                className="flex rounded-lg border border-border bg-bg-subtle p-0.5"
              >
                <Button
                  variant={posterView ? "ghost" : "secondary"}
                  size="icon"
                  className="size-7"
                  aria-label={t("Show collection preview")}
                  aria-pressed={!posterView}
                  onClick={() => setView(false)}
                >
                  <PanelLeft size={14} />
                </Button>
                <Button
                  variant={posterView ? "secondary" : "ghost"}
                  size="icon"
                  className="size-7"
                  aria-label={t("Show poster gallery")}
                  aria-pressed={posterView}
                  onClick={() => setView(true)}
                >
                  <LayoutGrid size={14} />
                </Button>
              </div>
            ) : null}
            <Button variant="outline" size="sm" onClick={() => setImportDrawer({ open: true })}>
              <Puzzle size={14} /> {t("Import from Stremio")}
            </Button>
            <Button variant="outline" size="sm" onClick={() => setDescribe(true)}>
              <Sparkles size={14} /> {t("Describe it")}
            </Button>
            <Button size="sm" onClick={() => navigate(newCollectionHref())}>
              <Plus size={14} /> {t("New collection")}
            </Button>
          </>
        }
        asideLabel={t("Collection library")}
        mainLabel={previewLabel}
        asideClassName="max-lg:max-h-[45vh]"
        aside={
          <>
            <PanelHead>
              <ToolbarPicker
                ariaLabel={t("Collection views")}
                value={tab}
                onChange={(key) => setTab(key)}
                className="mb-2 h-10 w-full px-3 [&>span:nth-child(2)]:flex-1 [&>span:nth-child(2)]:text-left"
                matchWidth
                options={[
                  {
                    value: "mine",
                    label: t("Your collections"),
                    icon: <Layers size={15} />,
                    meta: <ViewCount n={collections.data ? mine.length : undefined} />,
                  },
                  {
                    value: "templates",
                    label: t("Templates"),
                    icon: <LayoutTemplate size={15} />,
                    meta: <ViewCount n={templates.data?.length} />,
                  },
                ]}
              />
              <div className="flex items-center gap-1.5">
                <div className="relative min-w-0 flex-1">
                  <Search
                    size={13}
                    className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-subtle"
                    aria-hidden
                  />
                  <Input
                    type="search"
                    value={q}
                    onChange={(e) => setQ(e.currentTarget.value)}
                    placeholder={
                      tab === "templates" ? t("Find a template…") : t("Find a collection…")
                    }
                    aria-label={t("Search collections")}
                    className="h-8 pl-7 text-sm"
                  />
                </div>
                {tab === "mine" ? (
                  <ToolbarPicker
                    ariaLabel={t("Collection status")}
                    value={filter}
                    onChange={setFilter}
                    options={[
                      { value: "all", label: t("All statuses") },
                      { value: "live", label: t("Published") },
                      { value: "draft", label: t("Drafts") },
                    ]}
                  />
                ) : null}
                {tab === "templates" ? (
                  <ToolbarPicker
                    ariaLabel={t("Template category")}
                    value={category}
                    onChange={setCategory}
                    options={[
                      { value: "all", label: t("All categories") },
                      ...categories.map((c) => ({ value: c, label: t(c) })),
                    ]}
                  />
                ) : null}
              </div>
            </PanelHead>
            <div className="app-scroll min-h-0 flex-1 space-y-4 overflow-y-auto p-2 sm:p-3">
              {tab === "mine" && drafts.length ? (
                <section
                  aria-label={t("Unsaved drafts")}
                  className="rounded-lg border border-dashed border-border-strong p-2.5"
                >
                  <h2 className="mb-2 text-[11px] font-medium uppercase tracking-wide text-fg-subtle">
                    {t("On this device")}
                  </h2>
                  {drafts
                    .filter((d) => match({ name: d.draft.value.name || t("Untitled") }))
                    .map((d) => (
                      <div key={d.id} className="flex items-center gap-2 py-1">
                        <Link
                          to={`/collections/new?draft=${encodeURIComponent(d.id)}`}
                          className="min-w-0 flex-1 truncate text-xs font-medium text-fg hover:underline"
                        >
                          {d.draft.value.name || t("Untitled collection")}
                          <span className="block font-normal text-fg-subtle">
                            {relativeTime(d.draft.savedAt)}
                          </span>
                        </Link>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          aria-label={t("Discard draft {name}", {
                            name: d.draft.value.name || t("Untitled"),
                          })}
                          onClick={() => {
                            clearDraft(draftKey("collection", d.id));
                            setDrafts((ds) => ds.filter((x) => x.id !== d.id));
                          }}
                        >
                          <Undo2 size={13} />
                        </Button>
                      </div>
                    ))}
                </section>
              ) : null}
              {state.error ? (
                <ErrorPanel error={state.error} onRetry={() => void state.reload()} />
              ) : !state.data ? (
                <div role="status" aria-label={t("Loading collections")} className="space-y-2">
                  {Array.from({ length: 5 }, (_, i) => (
                    <Skeleton key={i} className="h-20 w-full rounded-lg" />
                  ))}
                </div>
              ) : tab === "templates" ? (
                <>
                  {bundles.data?.length ? (
                    <section
                      aria-label={t("Template bundles")}
                      className="space-y-2 rounded-xl border border-border bg-surface p-2.5"
                    >
                      <div className="flex items-center justify-between px-1">
                        <h2 className="text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
                          {t("One-click template bundles")}
                        </h2>
                        <span className="text-[11px] text-fg-muted">
                          {tn(bundles.data.length, "{count} bundle", "{count} bundles")}
                        </span>
                      </div>
                      <ul className="space-y-1.5">
                        {bundles.data.map((b) => {
                          const isPreviewing = bundlePreview?.bundle.id === b.id;
                          return (
                            <li
                              key={b.id}
                              className="rounded-lg border border-border bg-bg-subtle/60 p-2"
                            >
                              <div className="flex items-start justify-between gap-2">
                                <div className="min-w-0 flex-1">
                                  <span className="block text-xs font-semibold text-fg">
                                    {bundleTitle(b)}
                                  </span>
                                  <span className="mt-0.5 block text-[11px] text-fg-muted">
                                    {bundleDescription(b)}
                                  </span>
                                </div>
                                <Badge variant="secondary">
                                  {tn(
                                    b.templateIds.length,
                                    "{count} template",
                                    "{count} templates",
                                  )}
                                </Badge>
                              </div>
                              <div className="mt-2 flex flex-wrap items-center gap-1.5">
                                <Button
                                  variant="outline"
                                  size="sm"
                                  className="h-7 px-2.5 text-xs"
                                  disabled={bundleBusy === b.id}
                                  onClick={() => void previewBundle(b.id)}
                                >
                                  {isPreviewing ? t("Hide preview") : t("Preview bundle")}
                                </Button>
                                <Button
                                  size="sm"
                                  className="h-7 px-2.5 text-xs"
                                  disabled={bundleBusy === b.id}
                                  onClick={() => void applyBundle(b.id)}
                                >
                                  <Plus size={12} /> {t("Apply bundle")}
                                </Button>
                              </div>
                              {isPreviewing && bundlePreview ? (
                                <div className="mt-2 rounded-md border border-border bg-surface p-2 text-[11px]">
                                  <p className="mb-1 font-medium text-fg">
                                    {tn(
                                      bundlePreview.willCreate,
                                      "{count} to create ({skipped} already present)",
                                      "{count} to create ({skipped} already present)",
                                      { skipped: bundlePreview.willSkip },
                                    )}
                                  </p>
                                  <ul className="max-h-32 space-y-1 overflow-y-auto">
                                    {bundlePreview.items.map((item) => (
                                      <li
                                        key={item.templateId}
                                        className="flex items-center justify-between gap-2 text-fg-muted"
                                      >
                                        <span className="truncate">{t(item.name)}</span>
                                        <span className="shrink-0">
                                          {item.action === "skip_existing" ? (
                                            <span className="inline-flex items-center gap-1 text-success">
                                              <Check size={11} /> {t("Added")}
                                            </span>
                                          ) : (
                                            t("Will create")
                                          )}
                                        </span>
                                      </li>
                                    ))}
                                  </ul>
                                </div>
                              ) : null}
                            </li>
                          );
                        })}
                      </ul>
                    </section>
                  ) : null}
                  <p className="px-2 text-xs text-fg-muted">
                    {t("A starting point for your next collection.")}
                  </p>
                  <ul className="space-y-1">
                    {shownTemplates.map((tpl) => (
                      <li key={tpl.id}>
                        <button
                          type="button"
                          aria-label={t("Preview template {name}", { name: t(tpl.name) })}
                          aria-current={template?.id === tpl.id ? "true" : undefined}
                          onClick={() => select("template", tpl.id)}
                          className={cn(ROW, template?.id === tpl.id ? ACTIVE : INACTIVE)}
                        >
                          <span className="grid size-10 shrink-0 place-items-center rounded-lg border border-border bg-surface text-primary">
                            <LayoutTemplate size={18} />
                          </span>
                          <span className="min-w-0 flex-1">
                            <span className="block truncate text-sm font-medium">
                              {t(tpl.name)}
                            </span>
                            <span className="mt-0.5 block text-[11px] text-fg-muted">
                              {t(tpl.category)} · {mediaLabel(tpl.spec)}
                            </span>
                          </span>
                          <ArrowRight size={14} className="shrink-0 text-fg-subtle" />
                        </button>
                      </li>
                    ))}
                  </ul>
                  {!shownTemplates.length ? <ListEmpty /> : null}
                </>
              ) : (
                <>
                  <div className="flex items-center justify-between px-2 text-[11px] text-fg-subtle">
                    <span>{t("Your library")}</span>
                    <span>{tn(items.length, "{count} collection", "{count} collections")}</span>
                  </div>
                  <CollectionList
                    items={items}
                    selectedId={selected?.id}
                    collapsedFolders={collapsedFolders}
                    onToggleFolder={(folder) =>
                      setCollapsedFolders((prev) => {
                        const next = new Set(prev);
                        if (next.has(folder)) next.delete(folder);
                        else next.add(folder);
                        return next;
                      })
                    }
                    onSelect={(id) => select("collection", id)}
                    onManageAddon={(addonId) => setImportDrawer({ open: true, addonId })}
                  />
                  {!items.length ? <ListEmpty /> : null}
                </>
              )}
            </div>
            <div className="mt-auto hidden shrink-0 border-t border-border px-4 py-3 lg:block">
              <Link
                to="/recommendations"
                className="flex items-center justify-between gap-2 text-xs font-medium text-fg-muted hover:text-primary"
              >
                <span>{t("Place rows in Recommendations")}</span>
                <ArrowRight size={13} />
              </Link>
            </div>
          </>
        }
        main={
          state.error ? (
            <div className="p-6">
              <ErrorPanel error={state.error} onRetry={() => void state.reload()} />
            </div>
          ) : !state.data ? (
            <div className="space-y-5 p-6">
              <Skeleton className="h-56 w-full rounded-xl" />
              <Skeleton className="h-60 w-full rounded-xl" />
            </div>
          ) : posterView && items.length ? (
            <section className="p-5 sm:p-6" aria-label={t("Collection posters")}>
              <div className="mb-5 flex flex-wrap items-baseline justify-between gap-2">
                <h2 className="text-sm font-semibold text-fg">{t("Your collections")}</h2>
                <span className="text-xs text-fg-muted">
                  {tn(items.length, "{count} collection", "{count} collections")}
                </span>
              </div>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-x-4 gap-y-6">
                {items.map((c) => (
                  <article key={c.id} className="group min-w-0">
                    <button
                      type="button"
                      aria-label={t("Open {name} preview", { name: c.name })}
                      onClick={() => select("collection", c.id!, true)}
                      className="block w-full rounded-lg text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
                    >
                      <div className="relative aspect-[2/3] overflow-hidden rounded-lg ring-1 ring-border transition group-hover:ring-primary/50">
                        <Artwork
                          src={api.artworkUrl(
                            c.id!,
                            450,
                            [c.updatedAt, c.lastBuiltAt].filter(Boolean).join("-"),
                          )}
                          alt={t("{name} collection poster", { name: c.name })}
                          title={c.name}
                          className="size-full"
                        />
                        <span className="absolute left-2 top-2">
                          <Badge variant={c.enabled ? "success" : "secondary"}>
                            {c.enabled ? t("Published") : t("Draft")}
                          </Badge>
                        </span>
                      </div>
                      <span className="mt-2 block truncate text-sm font-medium text-fg">
                        {c.name}
                      </span>
                      <span className="mt-0.5 block text-xs text-fg-muted">
                        {c.itemCount === undefined
                          ? t("Not built yet")
                          : tn(c.itemCount, "{count} title", "{count} titles")}
                      </span>
                    </button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="mt-1 h-7 px-0 text-xs text-fg-muted"
                      aria-label={t("Customise {name} artwork", { name: c.name })}
                      onClick={() => edit(c, "artwork")}
                    >
                      <Pencil size={12} /> {t("Customise artwork")}
                    </Button>
                  </article>
                ))}
              </div>
            </section>
          ) : selected || template ? (
            <CollectionDetail
              key={`${tab}-${template?.id ?? selected?.id}`}
              spec={
                template
                  ? normalizeSpec({
                      ...template.spec,
                      name: t(template.name),
                      description: t(template.description),
                    })
                  : selected!
              }
              template={template}
              actions={
                template ? (
                  <Button
                    size="sm"
                    aria-label={t("Use template {name}", { name: t(template.name) })}
                    onClick={() => navigate(newCollectionHref({ template: template.id }))}
                  >
                    <Plus size={14} /> {t("Use template")}
                  </Button>
                ) : (
                  <>
                    <Button size="sm" onClick={() => edit(selected!)}>
                      <Pencil size={14} /> {t("Edit collection")}
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => edit(selected!, "artwork")}>
                      <LayoutGrid size={14} /> {t("Artwork")}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        void act(selected!, selected!.enabled ? "unpublish" : "publish")
                      }
                    >
                      {selected!.enabled ? <EyeOff size={14} /> : <Globe2 size={14} />}
                      {selected!.enabled ? t("Unpublish") : t("Publish")}
                    </Button>
                    <Button variant="outline" size="sm" asChild>
                      <Link to={addToPageHref(selected!.id!)}>
                        <Plus size={14} /> {t("Add to a page…")}
                      </Link>
                    </Button>
                    <PluginActionMenu
                      slot="actionMenu.collection"
                      recordId={String(selected!.id)}
                      onRefresh={() => void collections.reload()}
                      label={t("Collection actions")}
                      items={[
                        {
                          key: "duplicate",
                          label: t("Duplicate"),
                          icon: Copy,
                          onSelect: () => duplicate(selected!),
                        },
                        {
                          key: "rebuild",
                          label: t("Rebuild now"),
                          icon: RefreshCw,
                          disabled: busy,
                          onSelect: () => void act(selected!, "rebuild"),
                        },
                        { key: "sep", separator: true as const },
                        {
                          key: "delete",
                          label: t("Delete"),
                          icon: Trash2,
                          danger: true,
                          disabled: busy,
                          onSelect: () => void act(selected!, "delete"),
                        },
                      ]}
                    />
                  </>
                )
              }
            />
          ) : (
            <EmptyState
              icon={<Layers />}
              title={
                needle || filter !== "all" || category !== "all"
                  ? t("No matches")
                  : tab === "mine"
                    ? t("Make room for a great collection")
                    : t("No collections yet")
              }
              description={
                needle || filter !== "all" || category !== "all"
                  ? t("Try another search or filter.")
                  : t(
                      "Start with a template, describe an idea, or build a collection from scratch.",
                    )
              }
              className="px-6 py-20"
              action={
                <Button variant="outline" onClick={() => setTab("templates")}>
                  <LayoutTemplate size={14} /> {t("Browse templates")}
                </Button>
              }
            />
          )
        }
      />
      <DescribeDialog
        open={describe}
        onClose={() => setDescribe(false)}
        onApply={(spec) => navigate(newCollectionHref(), { state: { spec } })}
        onSaved={(saved) => navigate(`/collections/${encodeURIComponent(saved.id!)}`)}
      />
      <StremioImportDrawer
        open={importDrawer.open}
        initialAddonId={importDrawer.addonId}
        onClose={closeImportDrawer}
        onImported={() => void collections.reload()}
      />
    </>
  );
}

const ROW =
  "flex w-full items-center gap-3 rounded-lg px-2.5 py-2.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";
const ACTIVE = "bg-primary-subtle text-fg ring-1 ring-inset ring-primary/30";
const INACTIVE = "text-fg hover:bg-surface-hover";

function CollectionList({
  items,
  selectedId,
  collapsedFolders,
  onToggleFolder,
  onSelect,
  onManageAddon,
}: {
  items: CollectionSpec[];
  selectedId?: string;
  collapsedFolders: Set<string>;
  onToggleFolder: (folder: string) => void;
  onSelect: (id: string) => void;
  onManageAddon: (addonId: string) => void;
}) {
  const hasFolders = items.some((c) => Boolean(c.folder?.trim()));
  if (!hasFolders) {
    return (
      <ul className="space-y-1">
        {items.map((c) => (
          <CollectionRow
            key={c.id}
            collection={c}
            selected={selectedId === c.id}
            onSelect={onSelect}
          />
        ))}
      </ul>
    );
  }

  // Group items by folder in first-appearance order while keeping ungrouped items inline.
  type Group =
    | { kind: "item"; item: CollectionSpec }
    | { kind: "folder"; name: string; items: CollectionSpec[]; addonId?: string };
  const groups: Group[] = [];
  const folderMap = new Map<string, Extract<Group, { kind: "folder" }>>();
  for (const c of items) {
    const folder = c.folder?.trim();
    if (!folder) {
      groups.push({ kind: "item", item: c });
      continue;
    }
    const key = folder.toLowerCase();
    let group = folderMap.get(key);
    if (!group) {
      group = { kind: "folder", name: folder, items: [] };
      folderMap.set(key, group);
      groups.push(group);
    }
    group.items.push(c);
    if (!group.addonId && c.addonSync?.addonId) group.addonId = c.addonSync.addonId;
  }

  return (
    <div className="space-y-2">
      {groups.map((g, idx) => {
        if (g.kind === "item") {
          return (
            <ul key={g.item.id ?? `item-${idx}`} className="space-y-1">
              <CollectionRow
                collection={g.item}
                selected={selectedId === g.item.id}
                onSelect={onSelect}
              />
            </ul>
          );
        }
        const collapsed = collapsedFolders.has(g.name);
        const removedCount = g.items.filter((x) => x.addonSync?.removed).length;
        return (
          <section
            key={`folder-${g.name}`}
            aria-label={g.name}
            className="rounded-lg border border-border/70 bg-bg-subtle/40 p-1"
          >
            <div className="flex items-center gap-1 px-1 py-1">
              <button
                type="button"
                onClick={() => onToggleFolder(g.name)}
                aria-expanded={!collapsed}
                className="flex min-w-0 flex-1 items-center gap-1.5 rounded px-1 py-0.5 text-left text-xs font-semibold text-fg hover:bg-surface-hover"
              >
                {collapsed ? (
                  <ChevronRight size={13} aria-hidden className="shrink-0 text-fg-subtle" />
                ) : (
                  <ChevronDown size={13} aria-hidden className="shrink-0 text-fg-subtle" />
                )}
                <Folder size={13} aria-hidden className="shrink-0 text-fg-muted" />
                <span className="truncate">{g.name}</span>
                <span className="shrink-0 tabular-nums text-[11px] font-normal text-fg-subtle">
                  {g.items.length}
                </span>
                {removedCount > 0 ? (
                  <Badge variant="warning" className="ml-auto shrink-0 text-[10px]">
                    {tn(removedCount, "{count} removed", "{count} removed")}
                  </Badge>
                ) : null}
              </button>
              {g.addonId ? (
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-6 shrink-0"
                  aria-label={t("Manage {folder} import", { folder: g.name })}
                  onClick={() => onManageAddon(g.addonId!)}
                >
                  <RefreshCw size={12} />
                </Button>
              ) : null}
            </div>
            {!collapsed ? (
              <ul className="mt-1 space-y-1">
                {g.items.map((c) => (
                  <CollectionRow
                    key={c.id}
                    collection={c}
                    selected={selectedId === c.id}
                    onSelect={onSelect}
                  />
                ))}
              </ul>
            ) : null}
          </section>
        );
      })}
    </div>
  );
}

function CollectionRow({
  collection: c,
  selected,
  onSelect,
}: {
  collection: CollectionSpec;
  selected: boolean;
  onSelect: (id: string) => void;
}) {
  const removed = Boolean(c.addonSync?.removed);
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(c.id!)}
        aria-label={t("Preview {name}", { name: c.name })}
        aria-current={selected ? "true" : undefined}
        className={cn(ROW, selected ? ACTIVE : INACTIVE)}
      >
        <Artwork
          src={c.id ? api.artworkUrl(c.id, 120, c.updatedAt) : undefined}
          alt=""
          title={c.name}
          className="aspect-[2/3] w-10 shrink-0 rounded shadow-sm ring-1 ring-border"
        />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{c.name}</span>
          <span className="mt-1 block text-[11px] text-fg-muted">
            {c.itemCount === undefined
              ? t("Not built yet")
              : tn(c.itemCount, "{count} title", "{count} titles")}{" "}
            · {mediaLabel(c)}
          </span>
        </span>
        <span
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            removed ? "bg-warning" : c.enabled ? "bg-success" : "bg-fg-subtle",
          )}
          aria-hidden
        />
        <span className="sr-only">
          {removed ? t("Removed from add-on") : c.enabled ? t("Published") : t("Draft")}
        </span>
      </button>
    </li>
  );
}
function mediaLabel(c: CollectionSpec) {
  return c.mediaType === "show"
    ? t("TV shows")
    : c.mediaType === "mixed"
      ? t("Movies & TV")
      : t("Movies");
}
function ListEmpty() {
  return (
    <p className="px-3 py-8 text-center text-xs text-fg-muted">{t("No collections to show.")}</p>
  );
}

function ViewCount({ n }: { n?: number }) {
  return n === undefined ? null : (
    <span className="shrink-0 tabular-nums text-xs text-fg-subtle">{n}</span>
  );
}
