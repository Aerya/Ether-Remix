import { Badge, Button, Select, toast } from "@braedonsaunders/appkit-ui";
import { Layers, Plus, Puzzle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { AddAddonDrawer } from "../components/addons/AddAddonDrawer";
import { addonsApi, type AddonCatalog } from "../components/addons/addons";
import { Drawer } from "../components/Drawer";
import { ErrorPanel, LoadingRows } from "../components/feedback";
import { Field, NumberInput } from "../components/fields";
import { getLocale } from "../i18n";
import { useLocale } from "../i18n/react";
import { api } from "../lib/api";
import type { CollectionSpec } from "../lib/contract";
import { textOf, useRegistry } from "../lib/extend";
import { useAsync } from "../lib/hooks";
import {
  blankSpec,
  newSourceFromDescriptor,
  sourceDescriptor,
  type CollectionSourceMeta,
} from "../lib/rules";
import { PseRefreshField } from "./RefreshField";
import { levelOptions, PRIORITY_CHANGED, setSearchPriority } from "./SearchPriority";

/**
 * Import en une fois des catalogues d'un addon Stremio : une collection par
 * catalogue (films ou séries), rafraîchie en arrière-plan à la cadence
 * choisie. Bouton dans Collections ; voir docs/pse/FORK.fr.md.
 */

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

interface Catalog {
  kind: string; // plugin:<addon>/<catalogue>
  name: string;
  mediaType: "movie" | "show";
}

interface Picked {
  addon: string;
  catalogs: Catalog[];
}

export interface AddonPreset {
  refresh: CollectionSpec["refresh"];
  level: number;
  note: () => string;
}

/**
 * Réglages proposés pour les addons reconnus. L'identifiant vient du lien
 * plugin:<id>/<catalogue> ; il peut porter un suffixe « |hôte ».
 */
export function addonPreset(name: string, kinds: string[]): AddonPreset | null {
  const owner = kinds[0]?.match(/^plugin:([^/]+)\//)?.[1] ?? "";
  if (owner.startsWith("community.seerrcatalog") || /seerr\s*catalog/i.test(name))
    return {
      refresh: "hourly",
      level: 2,
      note: () =>
        L(
          "SeerrCatalog reconnu : mise à jour toutes les heures et priorité maximale proposées, pour que vos demandes soient cherchées vite.",
          "SeerrCatalog recognized: hourly refresh and maximum priority suggested, so your requests are searched quickly.",
        ),
    };
  if (owner.startsWith("community.useflowfr.catalog") || /rss\s*catalog/i.test(name))
    return {
      refresh: "3h",
      level: 1,
      note: () =>
        L(
          "RSS Catalog reconnu : mise à jour toutes les 3 h et priorité « Prioritaire » proposées.",
          "RSS Catalog recognized: 3-hour refresh and high priority suggested.",
        ),
    };
  return null;
}

const mediaOf = (type: string | undefined) =>
  type === "movie" ? "movie" : type === "series" ? "show" : undefined;

const typeLabel = (m: "movie" | "show") =>
  m === "movie" ? L("Films", "Movies") : L("Séries", "Series");

/**
 * Mode « bibliothèque à thème » (ThemeLibraries.tsx) : les collections
 * créées restent hors de l'onglet Collections, celles déjà importées sont
 * réutilisées, et onCreated reçoit leurs identifiants dans l'ordre.
 */
export interface PseAddonThemeMode {
  label: string;
  onCreated: (addon: string, ids: string[], types: ("movie" | "show")[]) => void;
}

export function PseAddonCatalogImport({
  onImported,
  theme,
}: {
  onImported: () => void;
  theme?: PseAddonThemeMode;
}) {
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [picked, setPicked] = useState<Picked | null>(null);
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <Layers size={14} aria-hidden />{" "}
        {theme?.label ?? L("Importer un addon", "Import an add-on")}
      </Button>
      <AddonPicker
        open={open && !adding && !picked}
        onClose={() => setOpen(false)}
        onAdd={() => setAdding(true)}
        onPick={setPicked}
      />
      <AddAddonDrawer
        open={open && adding}
        role="list_source"
        onClose={() => setAdding(false)}
        onAdded={(addon, preview) => {
          setAdding(false);
          setPicked({
            addon: addon.name,
            catalogs: (preview.catalogs ?? []).flatMap((c: AddonCatalog) => {
              const m = mediaOf(c.type);
              return m
                ? [{ kind: `plugin:${addon.id}/${c.key}`, name: c.name || c.id, mediaType: m }]
                : [];
            }),
          });
        }}
      />
      <CatalogChooser
        picked={open ? picked : null}
        theme={theme}
        onBack={() => setPicked(null)}
        onDone={(n) => {
          setPicked(null);
          setOpen(false);
          if (n > 0) onImported();
        }}
      />
    </>
  );
}

/** Addons déjà installés qui offrent des catalogues, ou ajout d'un addon. */
function AddonPicker({
  open,
  onClose,
  onAdd,
  onPick,
}: {
  open: boolean;
  onClose: () => void;
  onAdd: () => void;
  onPick: (p: Picked) => void;
}) {
  const registry = useRegistry<CollectionSourceMeta & { catalog?: string }>("collectionSource");
  const addons = useAsync(() => (open ? addonsApi.list() : Promise.resolve([])), [open]);
  const { locale } = useLocale();
  const groups = useMemo(() => {
    const installed = new Map((addons.data ?? []).map((a) => [a.id, a.name]));
    const out = new Map<string, Catalog[]>();
    for (const d of registry.descriptors) {
      const name = installed.get(d.owner);
      const m = mediaOf(d.meta?.catalog?.split("/")[0]);
      if (!name || !m) continue;
      const label = textOf(d.label).replace(/\s·\s(movie|series)$/i, "");
      out.set(d.group || name, [
        ...(out.get(d.group || name) ?? []),
        { kind: d.id, name: label, mediaType: m },
      ]);
    }
    return out;
  }, [addons.data, registry.descriptors, locale]);

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={L("Importer les catalogues d'un addon", "Import an add-on's catalogs")}
      description={L(
        "Chaque catalogue devient une collection, visible dans Plex et Jellyfin et mise à jour automatiquement.",
        "Each catalog becomes a collection, shown in Plex and Jellyfin and kept up to date.",
      )}
      size="md"
      footer={
        <div className="flex w-full justify-end">
          <Button variant="outline" onClick={onAdd}>
            <Plus size={14} aria-hidden /> {L("Ajouter un addon Stremio", "Add a Stremio add-on")}
          </Button>
        </div>
      }
    >
      {addons.error ? (
        <ErrorPanel error={addons.error} onRetry={addons.reload} />
      ) : addons.loading || registry.loading ? (
        <LoadingRows rows={2} />
      ) : groups.size === 0 ? (
        <p className="rounded-lg border border-dashed border-border px-3 py-2.5 text-sm text-fg-muted">
          {L(
            "Aucun addon installé ne propose encore de catalogue : ajoutez-en un avec son lien manifest.",
            "No installed add-on offers a catalog yet: add one with its manifest link.",
          )}
        </p>
      ) : (
        <ul className="space-y-2">
          {[...groups].map(([addon, catalogs]) => (
            <li key={addon}>
              <button
                type="button"
                onClick={() => onPick({ addon, catalogs })}
                className="flex w-full items-center justify-between gap-3 rounded-lg border border-border bg-surface px-3 py-2.5 text-left text-sm hover:bg-surface-hover"
              >
                <span className="flex items-center gap-2 font-medium">
                  <Puzzle size={14} aria-hidden className="text-fg-muted" />
                  {addon}
                </span>
                <Badge variant="secondary">
                  {L(`${catalogs.length} catalogue(s)`, `${catalogs.length} catalog(s)`)}
                </Badge>
              </button>
            </li>
          ))}
        </ul>
      )}
    </Drawer>
  );
}

/** Choix des catalogues, de la cadence et du nombre de titres, puis création. */
function CatalogChooser({
  picked,
  theme,
  onBack,
  onDone,
}: {
  picked: Picked | null;
  theme?: PseAddonThemeMode;
  onBack: () => void;
  onDone: (created: number) => void;
}) {
  const registry = useRegistry<CollectionSourceMeta>("collectionSource");
  const existing = useAsync(
    () => (picked ? api.collections() : Promise.resolve([] as CollectionSpec[])),
    [picked],
  );
  // Catalogue déjà importé → sa collection (réutilisée en mode thème).
  const usedBy = useMemo(() => {
    const m = new Map<string, string>();
    for (const c of existing.data ?? [])
      for (const s of c.sources) if (c.id && !m.has(String(s.kind))) m.set(String(s.kind), c.id);
    return m;
  }, [existing.data]);
  const used = useMemo(() => (theme ? new Set<string>() : new Set(usedBy.keys())), [theme, usedBy]);
  const [off, setOff] = useState<Set<string>>(new Set());
  const [refresh, setRefresh] = useState<CollectionSpec["refresh"]>("6h");
  const [limit, setLimit] = useState(100);
  const [level, setLevel] = useState(0);
  const [busy, setBusy] = useState(false);

  const preset = picked
    ? addonPreset(
        picked.addon,
        picked.catalogs.map((c) => c.kind),
      )
    : null;
  useEffect(() => {
    if (!picked) return;
    const initial = addonPreset(
      picked.addon,
      picked.catalogs.map((c) => c.kind),
    );
    setRefresh(initial?.refresh ?? "6h");
    setLevel(initial?.level ?? 0);
  }, [picked]);

  const catalogs = picked?.catalogs ?? [];
  const chosen = catalogs.filter((c) => !used.has(c.kind) && !off.has(c.kind));

  const create = async () => {
    if (!picked) return;
    setBusy(true);
    let created = 0;
    const failed: string[] = [];
    const ids: string[] = [];
    const types = new Set<"movie" | "show">();
    for (const c of chosen) {
      const reuse = theme ? usedBy.get(c.kind) : undefined;
      if (reuse) {
        ids.push(reuse);
        types.add(c.mediaType);
        continue;
      }
      const d = sourceDescriptor(registry.descriptors, c.kind);
      const source = d ? newSourceFromDescriptor(d, c.mediaType) : ({ kind: c.kind } as never);
      const spec: CollectionSpec = {
        ...blankSpec(c.mediaType),
        name: `${c.name} · ${typeLabel(c.mediaType)}`,
        description: L(
          `Catalogue ${c.name} de l'addon ${picked.addon}`,
          `${c.name} catalog from ${picked.addon}`,
        ),
        sources: [source],
        sort: "source",
        sortDesc: false,
        limit,
        refresh,
        localOnly: false,
        enabled: true, // publiée : visible tout de suite dans Plex et Jellyfin
        ...(theme
          ? {
              // Rangée de bibliothèque à thème : hors de l'onglet Collections.
              targets: {
                collectionsTab: false,
                homeRow: false,
                sectionRow: false,
                users: "all",
                schedule: null,
              },
            }
          : {}),
      };
      try {
        const saved = await api.createCollection(spec);
        if (saved?.id) ids.push(saved.id);
        types.add(c.mediaType);
        created++;
      } catch (e) {
        failed.push(`${spec.name} : ${e instanceof Error ? e.message : String(e)}`);
      }
    }
    if (level > 0 && ids.length > 0) {
      const results = await Promise.allSettled(
        ids.map((id) => setSearchPriority("collection", id, level)),
      );
      if (results.some((r) => r.status === "rejected"))
        toast.error(
          L(
            "Certaines priorités n'ont pas été enregistrées : réglez-les dans Bibliothèques.",
            "Some priorities were not saved: set them in Libraries.",
          ),
        );
      window.dispatchEvent(new Event(PRIORITY_CHANGED));
    }
    setBusy(false);
    if (created > 0)
      toast.success(
        L(
          `${created} collection(s) créée(s) ; elles se remplissent en arrière-plan.`,
          `${created} collection(s) created; they fill in the background.`,
        ),
      );
    if (failed.length > 0) toast.error(failed.join("\n"));
    if (theme && ids.length > 0) theme.onCreated(picked.addon, ids, [...types]);
    onDone(theme ? ids.length : created);
  };

  return (
    <Drawer
      open={picked !== null}
      onClose={() => !busy && onBack()}
      title={picked ? L(`Catalogues de ${picked.addon}`, `${picked.addon} catalogs`) : ""}
      description={L(
        "Une collection publiée par catalogue coché, visible tout de suite dans Plex et Jellyfin. Les catalogues déjà importés sont ignorés.",
        "One published collection per ticked catalog, shown right away in Plex and Jellyfin. Catalogs already imported are skipped.",
      )}
      size="md"
      footer={
        <div className="flex w-full justify-start gap-2">
          {/* À gauche : les notifications s'affichent en bas à droite. */}
          <Button size="sm" disabled={busy || chosen.length === 0} onClick={() => void create()}>
            {chosen.length > 1
              ? L(`Créer ${chosen.length} collections`, `Create ${chosen.length} collections`)
              : L("Créer la collection", "Create the collection")}
          </Button>
          <Button variant="ghost" size="sm" disabled={busy} onClick={onBack}>
            {L("Retour", "Back")}
          </Button>
        </div>
      }
    >
      <div className="space-y-5">
        {existing.error ? <ErrorPanel error={existing.error} onRetry={existing.reload} /> : null}
        {catalogs.length === 0 ? (
          <p className="text-sm text-fg-muted">
            {L(
              "Cet addon ne propose aucun catalogue de films ou de séries.",
              "This add-on offers no movie or series catalog.",
            )}
          </p>
        ) : (
          <fieldset className="space-y-1.5">
            <div className="flex items-center justify-between">
              <legend className="text-sm font-medium">{L("Catalogues", "Catalogs")}</legend>
              <div className="flex gap-1">
                <Button variant="ghost" size="sm" onClick={() => setOff(new Set())}>
                  {L("Tout", "All")}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setOff(new Set(catalogs.map((c) => c.kind)))}
                >
                  {L("Aucun", "None")}
                </Button>
              </div>
            </div>
            {catalogs.map((c) => {
              const done = used.has(c.kind);
              return (
                <label
                  key={c.kind}
                  className="flex items-center gap-3 rounded-lg border border-border bg-surface px-3 py-2 text-sm"
                >
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    disabled={done}
                    checked={!done && !off.has(c.kind)}
                    onChange={(e) =>
                      setOff((prev) => {
                        const next = new Set(prev);
                        if (e.currentTarget.checked) next.delete(c.kind);
                        else next.add(c.kind);
                        return next;
                      })
                    }
                  />
                  <span className="flex-1">{c.name}</span>
                  <Badge variant="outline">{typeLabel(c.mediaType)}</Badge>
                  {done ? <Badge variant="secondary">{L("Déjà importé", "Imported")}</Badge> : null}
                  {theme && usedBy.has(c.kind) ? (
                    <Badge variant="secondary">
                      {L("Déjà importé, réutilisé", "Imported, reused")}
                    </Badge>
                  ) : null}
                </label>
              );
            })}
          </fieldset>
        )}
        <PseRefreshField
          label={L("Mise à jour", "Refresh")}
          refresh={refresh}
          onChange={setRefresh}
          allowLive={false}
        />
        <Field label={L("Priorité de recherche", "Search priority")} hint={preset?.note()}>
          <Select
            className="h-9 w-full text-sm"
            aria-label={L("Priorité de recherche", "Search priority")}
            value={String(level)}
            onChange={(e) => setLevel(Number(e.target.value))}
          >
            {levelOptions().map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </Select>
        </Field>
        <Field
          label={L("Titres par collection", "Titles per collection")}
          hint={L("De 1 à 2 000.", "From 1 to 2,000.")}
        >
          <NumberInput
            aria-label={L("Titres par collection", "Titles per collection")}
            value={limit}
            min={1}
            max={2000}
            className="w-28"
            onChange={(n) => setLimit(Math.min(2000, Math.max(1, Math.round(n || 1))))}
          />
        </Field>
      </div>
    </Drawer>
  );
}
