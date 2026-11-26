import {
  Alert,
  AlertDescription,
  Badge,
  Button,
  Input,
  SearchSelect,
  Switch,
  cn,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  Folder,
  Link2,
  Loader2,
  Puzzle,
  RefreshCw,
  Search,
  Trash2,
} from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { Artwork } from "../../components/Artwork";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows, MutationError } from "../../components/feedback";
import { Field, NumberInput, Segmented } from "../../components/fields";
import { t, tn } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api, apiFetch, errorMessage } from "../../lib/api";
import type {
  AddonCatalogPreview,
  AddonCatalogPreviewItem,
  AddonCatalogSubscription,
  AddonImportNameMode,
  Refresh,
} from "../../lib/contract";
import { invalidateRegistry } from "../../lib/extend";
import { relativeTime } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { AddonSettingsPage } from "../plugins/drawer/AddonSettings";
import { LiveFields, useLiveForm } from "../plugins/drawer/liveForm";

type MediaFilter = "all" | "movie" | "show";

function previewCollectionName(
  cat: AddonCatalogPreviewItem,
  mode: AddonImportNameMode,
  affix: string,
): string {
  const base = (cat.name || cat.id).trim();
  const typeLabel = cat.mediaType === "show" ? t("Series") : t("Movies");
  const cleanAffix = affix.trim();
  switch (mode) {
    case "plain":
      return base;
    case "appendType":
      return `${base} (${typeLabel})`;
    case "prefix":
      return cleanAffix ? `${cleanAffix} ${base}` : base;
    case "suffix":
      return cleanAffix ? `${base} ${cleanAffix}` : base;
    default: {
      const lower = base.toLowerCase();
      const hasTypeWord = /\b(?:movie|movies|film|films|series|show|shows|tv)\)?$/i.test(lower);
      return cat.hasDuplicateName && !hasTypeWord ? `${base} (${typeLabel})` : base;
    }
  }
}

function missingRequiredExtras(
  cat: AddonCatalogPreviewItem,
  params: Record<string, string> | undefined,
): string[] {
  const req = cat.requiredExtras ?? [];
  if (!req.length) return [];
  return req.filter((name) => !params?.[name]?.trim());
}

/**
 * Bulk-import catalogues from a Stremio add-on manifest into Ether collections
 * (organised in a folder and optionally kept in sync as the add-on changes).
 */
export function StremioImportDrawer({
  open,
  initialAddonId,
  onClose,
  onImported,
}: {
  open: boolean;
  /** Optional installed add-on ID to inspect immediately when opened. */
  initialAddonId?: string;
  onClose: () => void;
  onImported: () => void;
}) {
  const linkId = useId();
  const folderId = useId();
  const affixId = useId();

  const imports = useAsync(
    () =>
      open ? api.addonCatalogImports() : Promise.resolve({ imports: [], availableAddons: [] }),
    [open],
  );

  const [url, setUrl] = useState("");
  const [preview, setPreview] = useState<AddonCatalogPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [syncingId, setSyncingId] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [settingUp, setSettingUp] = useState(false);
  const [touched, setTouched] = useState<Record<string, unknown>>({});
  const [pendingLink, setPendingLink] = useState("");

  // Import configuration state
  const [folder, setFolder] = useState("");
  const [nameMode, setNameMode] = useState<AddonImportNameMode>("autoType");
  const [nameAffix, setNameAffix] = useState("");
  const [followManifest, setFollowManifest] = useState(true);
  const [refresh, setRefresh] = useState<Exclude<Refresh, "live">>("daily");
  const [maxItems, setMaxItems] = useState(500);
  const [enabled, setEnabled] = useState(true);
  const [browseInApps, setBrowseInApps] = useState(true);

  // Catalogue selection & extra pinning
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [paramsByKey, setParamsByKey] = useState<Record<string, Record<string, string>>>({});
  const [filterText, setFilterText] = useState("");
  const [mediaFilter, setMediaFilter] = useState<MediaFilter>("all");

  const applyPreviewState = (p: AddonCatalogPreview) => {
    setPreview(p);
    setFilterText("");
    setMediaFilter("all");
    const sub = p.subscription;
    setFolder(sub?.folder ?? p.addonName);
    setNameMode(sub?.nameMode ?? "autoType");
    setNameAffix(sub?.nameAffix ?? "");
    setFollowManifest(sub ? sub.followManifest : true);
    setRefresh(
      sub?.refresh === "hourly" || sub?.refresh === "daily" || sub?.refresh === "weekly"
        ? sub.refresh
        : "daily",
    );
    setMaxItems(sub?.maxItems && sub.maxItems > 0 ? sub.maxItems : 500);
    setEnabled(sub ? sub.enabled : true);
    setBrowseInApps(sub?.browseInApps ?? true);

    const anyImported = p.catalogs.some((c) => c.imported);
    const nextSelected: Record<string, boolean> = {};
    const nextParams: Record<string, Record<string, string>> = {};
    for (const c of p.catalogs) {
      if (c.existingParams && Object.keys(c.existingParams).length > 0) {
        nextParams[c.key] = { ...c.existingParams };
      }
      if (anyImported) {
        nextSelected[c.key] = Boolean(c.imported);
      } else {
        nextSelected[c.key] = c.standalone;
      }
    }
    setSelected(nextSelected);
    setParamsByKey(nextParams);
  };

  const inspect = async (req: { url?: string; addonId?: string }) => {
    setBusy(true);
    setError(null);
    try {
      const p = await api.previewAddonCatalogImport(req);
      applyPreviewState(p);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    if (!open) {
      setUrl("");
      setPreview(null);
      setError(null);
      setSettingUp(false);
      setTouched({});
      setPendingLink("");
      return;
    }
    if (initialAddonId) {
      void inspect({ addonId: initialAddonId });
    }
  }, [open, initialAddonId]);

  useEffect(() => {
    if (!pendingLink) return;
    setPendingLink("");
    void inspect({ url: pendingLink });
  }, [pendingLink]);

  // Generic Stremio settings decoder/relay for add-ons requiring configuration first.
  const openConfigure = () =>
    apiFetch<{ page: string }>("/plugins/addons/configure-page", {
      method: "POST",
      body: JSON.stringify({ url: url.trim() }),
    }).then((r) => r.page);
  const live = useLiveForm(openConfigure, open && preview?.configureFirst ? 1 : 0);

  const useConfiguredLink = (link: string) => {
    setSettingUp(false);
    setTouched({});
    setUrl(link);
    setPreview(null);
    setPendingLink(link);
  };

  const continueSetup = async () => {
    setBusy(true);
    setError(null);
    try {
      const link = await live.commit();
      if (link) useConfiguredLink(link);
      else
        setError(
          new Error(
            t("{host} didn't give a link for these settings yet.", { host: preview?.host ?? "" }),
          ),
        );
    } finally {
      setBusy(false);
    }
  };

  const visibleCatalogs = useMemo(() => {
    if (!preview) return [];
    const needle = filterText.trim().toLowerCase();
    return preview.catalogs.filter((c) => {
      if (mediaFilter !== "all" && c.mediaType !== mediaFilter) return false;
      if (!needle) return true;
      return `${c.name} ${c.id} ${c.type}`.toLowerCase().includes(needle);
    });
  }, [preview, filterText, mediaFilter]);

  const selectedItems = useMemo(() => {
    if (!preview) return [];
    return preview.catalogs.filter(
      (c) => selected[c.key] && missingRequiredExtras(c, paramsByKey[c.key]).length === 0,
    );
  }, [preview, selected, paramsByKey]);

  const unpinnedSelectedCount = useMemo(() => {
    if (!preview) return 0;
    return preview.catalogs.filter(
      (c) => selected[c.key] && missingRequiredExtras(c, paramsByKey[c.key]).length > 0,
    ).length;
  }, [preview, selected, paramsByKey]);

  const toggleAllVisible = (on: boolean) => {
    setSelected((prev) => {
      const next = { ...prev };
      for (const c of visibleCatalogs) {
        if (on && missingRequiredExtras(c, paramsByKey[c.key]).length > 0) continue;
        next[c.key] = on;
      }
      return next;
    });
  };

  const setExtraParam = (catKey: string, extraName: string, value: string) => {
    setParamsByKey((prev) => {
      const cur = { ...(prev[catKey] ?? {}) };
      if (value.trim()) cur[extraName] = value;
      else delete cur[extraName];
      return { ...prev, [catKey]: cur };
    });
    if (value.trim()) {
      setSelected((prev) => ({ ...prev, [catKey]: true }));
    }
  };

  const submitImport = async () => {
    if (!preview || selectedItems.length === 0) return;
    setBusy(true);
    setError(null);
    try {
      const res = await api.applyAddonCatalogImport({
        ...(preview.installed ? { addonId: preview.addonId } : { url: url.trim() }),
        folder: folder.trim(),
        nameMode,
        nameAffix: nameAffix.trim(),
        followManifest,
        refresh,
        maxItems: Math.max(1, Math.min(2000, Math.round(maxItems || 500))),
        enabled,
        browseInApps,
        items: selectedItems.map((c) => {
          const p = paramsByKey[c.key];
          return {
            key: c.key,
            ...(p && Object.keys(p).length > 0 ? { params: p } : {}),
          };
        }),
      });
      invalidateRegistry();
      const parts: string[] = [];
      if (res.created > 0) {
        parts.push(tn(res.created, "Created {count} collection", "Created {count} collections"));
      }
      if (res.updated > 0) {
        parts.push(tn(res.updated, "updated {count}", "updated {count}"));
      }
      if (res.restored > 0) {
        parts.push(tn(res.restored, "restored {count}", "restored {count}"));
      }
      if ((res.skipped?.length ?? 0) > 0) {
        parts.push(tn(res.skipped!.length, "skipped {count}", "skipped {count}"));
      }
      toast.success(t("Imported from {name}", { name: res.addonName }), {
        description: parts.join(", ") || t("Collections are up to date."),
      });
      onImported();
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const syncNow = async (sub: AddonCatalogSubscription) => {
    if (syncingId) return;
    setSyncingId(sub.addonId);
    try {
      const res = await api.syncAddonCatalogImport(sub.addonId);
      invalidateRegistry();
      await imports.reload();
      onImported();
      const changed = res.created + res.updated + res.removed + res.restored;
      toast.success(t("Synced {name}", { name: res.addonName }), {
        description:
          changed > 0
            ? t("{created} added · {updated} updated · {removed} removed", {
                created: res.created,
                updated: res.updated + res.restored,
                removed: res.removed,
              })
            : t("Already matches the add-on manifest."),
      });
    } catch (e) {
      toast.error(t("Sync failed"), { description: errorMessage(e) });
    } finally {
      setSyncingId(null);
    }
  };

  const unlinkImport = async (sub: AddonCatalogSubscription) => {
    if (
      !(await confirmDialog({
        title: t("Stop following {name}?", { name: sub.addonName || sub.addonId }),
        message: t(
          "Your imported collections will stay in Ether, but future manifest changes will no longer sync automatically.",
        ),
        confirmLabel: t("Stop following"),
        tone: "danger",
      }))
    ) {
      return;
    }
    setSyncingId(sub.addonId);
    try {
      await api.deleteAddonCatalogImport(sub.addonId, false);
      await imports.reload();
      toast.success(t("Stopped following manifest"), { description: sub.addonName || sub.addonId });
    } catch (e) {
      toast.error(t("That didn't work"), { description: errorMessage(e) });
    } finally {
      setSyncingId(null);
    }
  };

  const settingsPage = preview ? (
    <AddonSettingsPage
      open={settingUp}
      name={preview.addonName}
      host={preview.host ?? ""}
      useLabel={t("Continue")}
      openPage={openConfigure}
      onUse={async (link) => useConfiguredLink(link)}
      onClose={() => setSettingUp(false)}
    />
  ) : null;

  const activeImports = imports.data?.imports ?? [];
  const availableAddons = (imports.data?.availableAddons ?? []).filter((a) => !a.hasImport);

  const footer = !preview ? (
    <div className="flex w-full items-center justify-end gap-2">
      <Button variant="ghost" onClick={onClose}>
        {t("Close")}
      </Button>
      <Button onClick={() => void inspect({ url: url.trim() })} disabled={!url.trim() || busy}>
        {busy ? t("Reading manifest…") : t("Inspect manifest")}
      </Button>
    </div>
  ) : preview.configureFirst ? (
    <div className="flex w-full items-center justify-between gap-2">
      <Button variant="ghost" onClick={() => setPreview(null)} disabled={busy}>
        <ArrowLeft size={14} aria-hidden /> {t("Back")}
      </Button>
      {live.status === "ready" ? (
        <Button onClick={() => void continueSetup()} disabled={busy}>
          {busy ? t("Reading manifest…") : t("Continue")}
        </Button>
      ) : (
        <Button onClick={() => setSettingUp(true)} disabled={busy}>
          {t("Set it up here")}
        </Button>
      )}
    </div>
  ) : (
    <div className="flex w-full items-center justify-between gap-3">
      <Button variant="ghost" onClick={() => setPreview(null)} disabled={busy}>
        <ArrowLeft size={14} aria-hidden /> {t("Back")}
      </Button>
      <div className="flex items-center gap-2">
        {unpinnedSelectedCount > 0 ? (
          <span className="text-xs text-warning">
            {tn(
              unpinnedSelectedCount,
              "{count} needs a required filter",
              "{count} need a required filter",
            )}
          </span>
        ) : null}
        <Button
          onClick={() => void submitImport()}
          disabled={busy || selectedItems.length === 0 || unpinnedSelectedCount > 0}
        >
          {busy
            ? t("Importing…")
            : preview.subscription
              ? tn(
                  selectedItems.length + unpinnedSelectedCount,
                  "Save & sync {count} collection",
                  "Save & sync {count} collections",
                )
              : tn(
                  selectedItems.length + unpinnedSelectedCount,
                  "Import {count} collection",
                  "Import {count} collections",
                )}
        </Button>
      </div>
    </div>
  );

  return (
    <>
      <Drawer
        open={open}
        onClose={onClose}
        title={t("Import from Stremio add-on")}
        size="lg"
        footer={footer}
      >
        {!preview ? (
          <div className="space-y-6">
            <div className="space-y-2.5">
              <Field
                label={t("Add-on manifest link")}
                htmlFor={linkId}
                hint={t(
                  "Paste a Stremio catalogue add-on's manifest link. Ether keeps the link sealed on the server and creates one collection per catalogue you pick.",
                )}
              >
                <div className="flex items-center gap-2">
                  <div className="relative min-w-0 flex-1">
                    <Link2
                      size={14}
                      aria-hidden
                      className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle"
                    />
                    <Input
                      id={linkId}
                      type="url"
                      value={url}
                      placeholder={t("https://…/manifest.json")}
                      autoFocus
                      className="pl-9"
                      onChange={(e) => {
                        setUrl(e.currentTarget.value);
                        setError(null);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" && url.trim() && !busy) {
                          e.preventDefault();
                          void inspect({ url: url.trim() });
                        }
                      }}
                    />
                  </div>
                </div>
              </Field>
              {error ? <MutationError error={error} /> : null}
            </div>

            {imports.error ? (
              <ErrorPanel error={imports.error} onRetry={imports.reload} />
            ) : imports.loading ? (
              <LoadingRows rows={2} />
            ) : (
              <>
                {activeImports.length > 0 ? (
                  <section aria-label={t("Synced add-on imports")} className="space-y-2">
                    <div className="flex items-center justify-between">
                      <h3 className="text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                        {t("Synced add-on imports")}
                      </h3>
                      <span className="text-xs text-fg-muted">
                        {tn(activeImports.length, "{count} add-on", "{count} add-ons")}
                      </span>
                    </div>
                    <ul className="space-y-2">
                      {activeImports.map((sub) => (
                        <li
                          key={sub.addonId}
                          className="rounded-xl border border-border bg-surface p-3"
                        >
                          <div className="flex items-start justify-between gap-3">
                            <div className="flex min-w-0 items-center gap-2.5">
                              <Artwork
                                src={sub.addonLogo}
                                alt=""
                                icon={<Puzzle size={16} />}
                                className="size-9 shrink-0 rounded-lg"
                              />
                              <div className="min-w-0">
                                <div className="flex flex-wrap items-center gap-1.5">
                                  <span className="truncate text-sm font-semibold text-fg">
                                    {sub.addonName || sub.addonId}
                                  </span>
                                  {sub.folder ? (
                                    <Badge variant="secondary">
                                      <Folder size={10} className="mr-1" aria-hidden />
                                      {sub.folder}
                                    </Badge>
                                  ) : null}
                                  {sub.followManifest ? (
                                    <Badge variant="success">{t("Follows manifest")}</Badge>
                                  ) : null}
                                  {sub.removedCount > 0 ? (
                                    <Badge variant="warning">
                                      {tn(sub.removedCount, "{count} removed", "{count} removed")}
                                    </Badge>
                                  ) : null}
                                </div>
                                <p className="mt-0.5 text-xs text-fg-muted">
                                  {tn(
                                    sub.managedCount,
                                    "{count} collection",
                                    "{count} collections",
                                  )}
                                  {sub.host ? ` · ${sub.host}` : ""}
                                  {sub.lastSyncedAt
                                    ? ` · ${t("Synced {when}", { when: relativeTime(sub.lastSyncedAt) })}`
                                    : ""}
                                </p>
                              </div>
                            </div>
                          </div>
                          <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={busy || syncingId === sub.addonId}
                              onClick={() => void inspect({ addonId: sub.addonId })}
                            >
                              {t("Choose catalogues")}
                            </Button>
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={busy || syncingId === sub.addonId}
                              onClick={() => void syncNow(sub)}
                            >
                              <RefreshCw
                                size={12}
                                className={cn(syncingId === sub.addonId && "animate-spin")}
                                aria-hidden
                              />{" "}
                              {t("Sync now")}
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              className="ml-auto text-fg-muted hover:text-danger"
                              disabled={busy || syncingId === sub.addonId}
                              onClick={() => void unlinkImport(sub)}
                            >
                              <Trash2 size={12} aria-hidden /> {t("Stop following")}
                            </Button>
                          </div>
                        </li>
                      ))}
                    </ul>
                  </section>
                ) : null}

                {availableAddons.length > 0 ? (
                  <section aria-label={t("Installed Stremio add-ons")} className="space-y-2">
                    <h3 className="text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                      {t("Installed Stremio add-ons")}
                    </h3>
                    <ul className="space-y-1.5">
                      {availableAddons.map((a) => (
                        <li key={a.addonId}>
                          <button
                            type="button"
                            disabled={busy}
                            onClick={() => void inspect({ addonId: a.addonId })}
                            className="flex w-full items-center gap-3 rounded-xl border border-border bg-surface px-3 py-2.5 text-left transition-colors hover:bg-surface-hover"
                          >
                            <Artwork
                              src={a.logo}
                              alt=""
                              icon={<Puzzle size={16} />}
                              className="size-9 shrink-0 rounded-lg"
                            />
                            <div className="min-w-0 flex-1">
                              <span className="block truncate text-sm font-medium text-fg">
                                {a.name}
                              </span>
                              <span className="block text-xs text-fg-muted">
                                {tn(a.catalogCount, "{count} catalogue", "{count} catalogues")}
                                {a.host ? ` · ${a.host}` : ""}
                              </span>
                            </div>
                            <Badge variant="secondary">{t("Import")}</Badge>
                          </button>
                        </li>
                      ))}
                    </ul>
                  </section>
                ) : null}
              </>
            )}
          </div>
        ) : (
          <div className="space-y-5">
            {/* Add-on header */}
            <div className="flex items-center gap-3 rounded-xl border border-border bg-bg-subtle/60 p-3">
              <Artwork
                src={preview.addonLogo}
                alt=""
                icon={<Puzzle size={18} />}
                className="size-11 shrink-0 rounded-lg"
              />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <p className="truncate text-sm font-semibold text-fg">{preview.addonName}</p>
                  {preview.installed ? <Badge variant="secondary">{t("Installed")}</Badge> : null}
                </div>
                <p className="truncate text-xs text-fg-muted">
                  {preview.host}
                  {preview.addonVersion ? ` · v${preview.addonVersion}` : ""}
                  {!preview.configureFirst
                    ? ` · ${tn(preview.catalogs.length, "{count} catalogue", "{count} catalogues")}`
                    : ""}
                </p>
              </div>
            </div>

            {preview.configureFirst ? (
              <div className="space-y-3">
                <p className="flex items-start gap-2 rounded-lg bg-warning-subtle p-3 text-sm">
                  <AlertTriangle size={15} className="mt-0.5 shrink-0 text-warning" aria-hidden />
                  {t("{name} needs setting up before Ether can read its catalogues.", {
                    name: preview.addonName,
                  })}
                </p>
                {live.status === "ready" ? (
                  <LiveFields
                    form={live}
                    title={t("Plugin settings")}
                    description={
                      live.partial
                        ? t(
                            "Read from {host}'s own settings page. A few of its settings are only on that page.",
                            { host: preview.host ?? "" },
                          )
                        : t("Read from {host}'s own settings page.", { host: preview.host ?? "" })
                    }
                    touched={touched}
                    onTouched={setTouched}
                  />
                ) : live.status === "loading" ? (
                  <p className="flex items-center gap-2 text-sm text-fg-muted" role="status">
                    <Loader2 size={14} className="animate-spin" aria-hidden />
                    {t("Reading {host}'s settings…", { host: preview.host ?? "" })}
                  </p>
                ) : null}
                <Button
                  size="sm"
                  variant={live.status === "ready" ? "ghost" : "default"}
                  onClick={() => setSettingUp(true)}
                >
                  {live.status === "ready"
                    ? t("Open {host}'s own page", { host: preview.host ?? "" })
                    : t("Set it up here")}
                </Button>
                {live.frame}
              </div>
            ) : (
              <>
                {error ? <MutationError error={error} /> : null}

                {/* Removed collections alert if any */}
                {(preview.removedCollections?.length ?? 0) > 0 ? (
                  <Alert variant="warning">
                    <AlertDescription>
                      {tn(
                        preview.removedCollections!.length,
                        "{count} previously imported collection is no longer in this add-on's manifest.",
                        "{count} previously imported collections are no longer in this add-on's manifest.",
                      )}
                    </AlertDescription>
                  </Alert>
                ) : null}

                {/* Import settings */}
                <section
                  aria-label={t("Import settings")}
                  className="space-y-4 rounded-xl border border-border bg-surface p-4"
                >
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field
                      label={t("Folder")}
                      htmlFor={folderId}
                      hint={t("Groups these collections together and keeps the add-on's order.")}
                    >
                      <Input
                        id={folderId}
                        value={folder}
                        maxLength={80}
                        placeholder={preview.addonName}
                        onChange={(e) => setFolder(e.currentTarget.value)}
                      />
                    </Field>
                    <Field
                      label={t("Naming")}
                      hint={t("How Ether names collections when catalogues share bare titles.")}
                    >
                      <SearchSelect
                        ariaLabel={t("Naming mode")}
                        value={nameMode}
                        onChange={(v) => setNameMode(v as AddonImportNameMode)}
                        searchable={false}
                        {...searchSelectLabels()}
                        options={[
                          { value: "autoType", label: t("Add type when names clash") },
                          { value: "plain", label: t("Catalogue name as-is") },
                          { value: "appendType", label: t("Always add Movies / Series") },
                          { value: "prefix", label: t("Add custom prefix") },
                          { value: "suffix", label: t("Add custom suffix") },
                        ]}
                      />
                    </Field>
                  </div>

                  {nameMode === "prefix" || nameMode === "suffix" ? (
                    <Field
                      label={nameMode === "prefix" ? t("Prefix") : t("Suffix")}
                      htmlFor={affixId}
                    >
                      <Input
                        id={affixId}
                        value={nameAffix}
                        maxLength={40}
                        placeholder={nameMode === "prefix" ? t("e.g. Kids ·") : t("e.g. (Kids)")}
                        onChange={(e) => setNameAffix(e.currentTarget.value)}
                      />
                    </Field>
                  ) : null}

                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label={t("Refresh schedule")}>
                      <Segmented
                        ariaLabel={t("Refresh schedule")}
                        value={refresh}
                        onChange={(v) => setRefresh(v)}
                        options={[
                          { value: "hourly", label: t("Hourly") },
                          { value: "daily", label: t("Daily") },
                          { value: "weekly", label: t("Weekly") },
                        ]}
                      />
                    </Field>
                    <Field
                      label={t("Max items per catalogue")}
                      hint={t("Bounds paging per refresh (1 to 2,000).")}
                    >
                      <NumberInput
                        aria-label={t("Max items per catalogue")}
                        value={maxItems}
                        min={1}
                        max={2000}
                        onChange={(n) => setMaxItems(Math.max(1, Math.min(2000, Math.round(n))))}
                      />
                    </Field>
                  </div>

                  <div className="space-y-2.5 border-t border-border-subtle pt-3">
                    <label className="flex items-center justify-between gap-3">
                      <span className="min-w-0">
                        <span className="block text-sm font-medium text-fg">
                          {t("Follow manifest updates")}
                        </span>
                        <span className="block text-xs text-fg-muted">
                          {t(
                            "Automatically add new catalogues, update unedited names and order, and flag removed ones without changing per-collection viewer targets.",
                          )}
                        </span>
                      </span>
                      <Switch
                        checked={followManifest}
                        onChange={(e) => setFollowManifest(e.currentTarget.checked)}
                        aria-label={t("Follow manifest updates")}
                      />
                    </label>
                    <label className="flex items-center justify-between gap-3">
                      <span className="text-sm font-medium text-fg">
                        {t("Publish new collections immediately")}
                      </span>
                      <Switch
                        checked={enabled}
                        onChange={(e) => setEnabled(e.currentTarget.checked)}
                        aria-label={t("Publish new collections immediately")}
                      />
                    </label>
                  </div>

                  <div className="space-y-1.5 border-t border-border-subtle pt-3">
                    <label className="inline-flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        checked={browseInApps}
                        onChange={(e) => {
                          setBrowseInApps(e.currentTarget.checked);
                        }}
                        className="accent-primary"
                      />
                      <span>{t("Browse in apps")}</span>
                    </label>
                    <p className="text-xs text-fg-muted">
                      {t(
                        "Each catalogue shows as a collection in its library's Collections view. Add them as rows in Recommendations.",
                      )}
                    </p>
                  </div>
                </section>

                {/* Catalogues checklist */}
                <section aria-label={t("Catalogues")} className="space-y-2.5">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h3 className="text-sm font-semibold text-fg">
                      {t("Catalogues")}{" "}
                      <span className="font-normal text-fg-muted">
                        ({selectedItems.length}/{preview.catalogs.length})
                      </span>
                    </h3>
                    <div className="flex items-center gap-1.5">
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-7 px-2 text-xs"
                        onClick={() => toggleAllVisible(true)}
                      >
                        {t("Select all")}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-7 px-2 text-xs"
                        onClick={() => toggleAllVisible(false)}
                      >
                        {t("Clear")}
                      </Button>
                    </div>
                  </div>

                  <div className="flex flex-wrap items-center gap-2">
                    <div className="relative min-w-0 flex-1">
                      <Search
                        size={13}
                        aria-hidden
                        className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-subtle"
                      />
                      <Input
                        type="search"
                        value={filterText}
                        onChange={(e) => setFilterText(e.currentTarget.value)}
                        placeholder={t("Filter catalogues…")}
                        aria-label={t("Filter catalogues")}
                        className="h-8 pl-7 text-sm"
                      />
                    </div>
                    <Segmented
                      ariaLabel={t("Catalogue media type")}
                      value={mediaFilter}
                      onChange={(v) => setMediaFilter(v)}
                      options={[
                        { value: "all", label: t("All") },
                        { value: "movie", label: t("Movies") },
                        { value: "show", label: t("Series") },
                      ]}
                    />
                  </div>

                  {preview.catalogs.length === 0 ? (
                    <p className="rounded-lg border border-dashed border-border px-3 py-4 text-center text-sm text-fg-muted">
                      {t("This add-on declares no movie or series catalogues.")}
                    </p>
                  ) : visibleCatalogs.length === 0 ? (
                    <p className="rounded-lg border border-dashed border-border px-3 py-4 text-center text-sm text-fg-muted">
                      {t("No catalogues match that filter.")}
                    </p>
                  ) : (
                    <ul className="max-h-96 divide-y divide-border overflow-y-auto rounded-xl border border-border bg-surface">
                      {visibleCatalogs.map((cat) => {
                        const checked = Boolean(selected[cat.key]);
                        const params = paramsByKey[cat.key];
                        const missing = missingRequiredExtras(cat, params);
                        const finalName = previewCollectionName(cat, nameMode, nameAffix);
                        const requiredExtras = (cat.extras ?? []).filter((x) => x.required);
                        return (
                          <li key={cat.key} className="space-y-2 px-3 py-2.5">
                            <div className="flex items-start gap-2.5">
                              <input
                                type="checkbox"
                                checked={checked}
                                aria-label={finalName}
                                onChange={(e) => {
                                  const next = e.currentTarget.checked;
                                  setSelected((prev) => ({
                                    ...prev,
                                    [cat.key]: next,
                                  }));
                                }}
                                className="mt-1 accent-primary"
                              />
                              <div className="min-w-0 flex-1">
                                <div className="flex flex-wrap items-center gap-1.5">
                                  <span className="truncate text-sm font-medium text-fg">
                                    {cat.name || cat.id}
                                  </span>
                                  <Badge variant="secondary">
                                    {cat.mediaType === "show" ? t("Series") : t("Movie")}
                                  </Badge>
                                  {cat.imported ? (
                                    <Badge variant="success">
                                      <Check size={10} className="mr-0.5" aria-hidden />
                                      {t("Imported")}
                                    </Badge>
                                  ) : null}
                                  {missing.length > 0 ? (
                                    <Badge variant="warning">
                                      {t("Requires {extras}", { extras: missing.join(", ") })}
                                    </Badge>
                                  ) : null}
                                </div>
                                {finalName !== (cat.name || cat.id) ? (
                                  <p className="mt-0.5 truncate text-xs text-fg-muted">
                                    {t("Will be named “{name}”", { name: finalName })}
                                  </p>
                                ) : null}
                                {cat.imported &&
                                cat.existingCollectionName &&
                                cat.existingCollectionName !== finalName ? (
                                  <p className="mt-0.5 truncate text-xs text-fg-subtle">
                                    {t("Keeping custom name “{name}”", {
                                      name: cat.existingCollectionName,
                                    })}
                                  </p>
                                ) : null}
                              </div>
                            </div>
                            {requiredExtras.length > 0 ? (
                              <div className="ml-6 flex flex-wrap items-center gap-2">
                                {requiredExtras.map((extra) => (
                                  <div
                                    key={extra.name}
                                    className="flex min-w-[180px] flex-1 items-center gap-1.5"
                                  >
                                    <span className="shrink-0 text-xs text-fg-muted">
                                      {extra.label}:
                                    </span>
                                    {extra.options && extra.options.length > 0 ? (
                                      <SearchSelect
                                        ariaLabel={t("{catalog} {extra}", {
                                          catalog: cat.name || cat.id,
                                          extra: extra.label,
                                        })}
                                        value={params?.[extra.name] ?? ""}
                                        onChange={(v) => setExtraParam(cat.key, extra.name, v)}
                                        {...searchSelectLabels()}
                                        placeholder={t("Pin a value…")}
                                        options={extra.options.map((o) => ({
                                          value: o,
                                          label: o,
                                        }))}
                                      />
                                    ) : (
                                      <Input
                                        aria-label={t("{catalog} {extra}", {
                                          catalog: cat.name || cat.id,
                                          extra: extra.label,
                                        })}
                                        value={params?.[extra.name] ?? ""}
                                        placeholder={t("Pin {extra}…", { extra: extra.name })}
                                        className="h-8 text-xs"
                                        onChange={(e) =>
                                          setExtraParam(cat.key, extra.name, e.currentTarget.value)
                                        }
                                      />
                                    )}
                                  </div>
                                ))}
                              </div>
                            ) : null}
                          </li>
                        );
                      })}
                    </ul>
                  )}
                </section>
              </>
            )}
          </div>
        )}
      </Drawer>
      {settingsPage}
    </>
  );
}
