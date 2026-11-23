import { Badge, Button, Input } from "@braedonsaunders/appkit-ui";
import { Plus, Puzzle, Search } from "lucide-react";
import { useState } from "react";
import { AddAddonDrawer } from "../../components/addons/AddAddonDrawer";
import { addonsApi, type AddonCatalog } from "../../components/addons/addons";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Segmented } from "../../components/fields";
import { t } from "../../i18n";
import { textOf, useRegistry } from "../../lib/extend";
import { useAsync } from "../../lib/hooks";
import type { CollectionSourceMeta } from "../../lib/rules";

/** One catalog an installed add-on offers as a collection source. */
export interface AddonCatalogChoice {
  /** The source kind: plugin:<add-on id>/<catalog key>. */
  kind: string;
  addon: string;
  label: string;
  /** The catalog's media type, when known. */
  mediaType?: "movie" | "show";
  /** Required filter names, when declared by the manifest. */
  requiredParams?: string[];
}

function mediaOf(type: string | undefined): "movie" | "show" | undefined {
  if (type === "movie") return "movie";
  if (type === "series") return "show";
  return undefined;
}

const listable = (c: AddonCatalog) => c.type === "movie" || c.type === "series";

/**
 * Pick a catalog of an installed Stremio add-on, or add an add-on first.
 * Catalogs come from the add-ons' collection source descriptors; an add-on
 * added here shows its catalogs straight away, before the registry reloads.
 */
export function AddonCatalogDrawer({
  open,
  onClose,
  onPick,
}: {
  open: boolean;
  onClose: () => void;
  onPick: (choice: AddonCatalogChoice) => void;
}) {
  const registry = useRegistry<CollectionSourceMeta & { catalog?: string }>("collectionSource");
  const [adding, setAdding] = useState(false);
  const [added, setAdded] = useState<AddonCatalogChoice[]>([]);
  const [filterText, setFilterText] = useState("");
  const [mediaFilter, setMediaFilter] = useState<"all" | "movie" | "show">("all");
  const addons = useAsync(() => (open ? addonsApi.list() : Promise.resolve([])), [open]);

  const installed = new Set((addons.data ?? []).map((a) => a.id));
  const choices: AddonCatalogChoice[] = [...added];
  for (const d of registry.descriptors) {
    if (!installed.has(d.owner) || choices.some((c) => c.kind === d.id)) continue;
    const reqParams = (d.meta?.params ?? []).filter((p) => p.required).map((p) => p.name);
    choices.push({
      kind: d.id,
      addon: d.group || d.owner,
      label: textOf(d.label),
      mediaType: mediaOf(d.meta?.catalog?.split("/")[0]),
      ...(reqParams.length ? { requiredParams: reqParams } : {}),
    });
  }
  const needle = filterText.trim().toLowerCase();
  const filtered = choices.filter((c) => {
    if (mediaFilter !== "all" && c.mediaType && c.mediaType !== mediaFilter) return false;
    if (!needle) return true;
    return `${c.label} ${c.addon}`.toLowerCase().includes(needle);
  });
  const groups = new Map<string, AddonCatalogChoice[]>();
  for (const c of filtered) groups.set(c.addon, [...(groups.get(c.addon) ?? []), c]);

  const pick = (c: AddonCatalogChoice) => {
    onPick({
      kind: c.kind,
      addon: c.addon,
      label: c.label,
      ...(c.mediaType ? { mediaType: c.mediaType } : {}),
    });
    onClose();
  };

  return (
    <>
      <Drawer
        open={open && !adding}
        onClose={onClose}
        title={t("Choose a Stremio plugin catalog")}
        size="md"
        footer={
          <div className="flex w-full justify-end">
            <Button variant="outline" onClick={() => setAdding(true)}>
              <Plus size={14} aria-hidden /> {t("Add a plugin from Stremio")}
            </Button>
          </div>
        }
      >
        <div className="space-y-4">
          <p className="text-sm text-fg-muted">
            {t(
              "Titles from the catalog are matched to your catalogue and refreshed in the background.",
            )}
          </p>
          {choices.length > 0 ? (
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
                onChange={setMediaFilter}
                options={[
                  { value: "all", label: t("All") },
                  { value: "movie", label: t("Movies") },
                  { value: "show", label: t("Series") },
                ]}
              />
            </div>
          ) : null}
          {addons.error ? (
            <ErrorPanel error={addons.error} onRetry={addons.reload} />
          ) : addons.loading || registry.loading ? (
            <LoadingRows rows={2} />
          ) : choices.length === 0 ? (
            <p className="rounded-lg border border-dashed border-border px-3 py-2.5 text-sm text-fg-muted">
              {t("No installed plugin offers a movie or series catalog yet.")}
            </p>
          ) : groups.size === 0 ? (
            <p className="rounded-lg border border-dashed border-border px-3 py-2.5 text-sm text-fg-muted">
              {t("No catalogues match that filter.")}
            </p>
          ) : (
            [...groups].map(([addon, list]) => (
              <section key={addon} className="space-y-1.5" aria-label={addon}>
                <h3 className="flex items-center gap-1.5 text-sm font-semibold">
                  <Puzzle size={14} aria-hidden className="text-fg-muted" />
                  {addon}
                </h3>
                <ul className="space-y-1">
                  {list.map((c) => (
                    <li key={c.kind}>
                      <button
                        type="button"
                        onClick={() => pick(c)}
                        className="flex w-full items-center justify-between gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-left text-sm hover:bg-surface-hover"
                      >
                        <span className="truncate">{c.label}</span>
                        {c.requiredParams?.length ? (
                          <Badge variant="warning" className="shrink-0">
                            {t("Requires {extras}", { extras: c.requiredParams.join(", ") })}
                          </Badge>
                        ) : null}
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            ))
          )}
        </div>
      </Drawer>
      <AddAddonDrawer
        open={open && adding}
        role="list_source"
        onClose={() => setAdding(false)}
        onAdded={(addon, preview) => {
          const fresh = (preview.catalogs ?? []).filter(listable).map((c) => ({
            kind: `plugin:${addon.id}/${c.key}`,
            addon: addon.name,
            label: `${c.name || c.id} · ${c.type}`,
            mediaType: mediaOf(c.type),
          }));
          setAdded((prev) => [
            ...fresh,
            ...prev.filter((p) => !fresh.some((f) => f.kind === p.kind)),
          ]);
          void addons.reload();
        }}
      />
    </>
  );
}
