import {
  Avatar,
  Badge,
  Button,
  Input,
  SearchSelect,
  Select,
  Spinner,
  Switch,
  cn,
} from "@braedonsaunders/appkit-ui";
import {
  CheckCircle2,
  Filter,
  Folders,
  Library as LibraryIcon,
  Plus,
  Radio,
  RotateCcw,
  Search,
  Sparkles,
  Users,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Artwork } from "../../components/Artwork";
import { Drawer } from "../../components/Drawer";
import { Segmented } from "../../components/fields";
import { useViewers, viewerName } from "../../components/pickers";
import { t, tn } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { api } from "../../lib/api";
import { asObj } from "../settings/SchemaForm";
import type {
  HomeLayout,
  LibraryLayoutDoc,
  LibraryLayoutSection,
  Rule,
  RuleField,
} from "../../lib/contract";
import { formatNumber } from "../../lib/format";
import { useAsync, useDebouncedValue } from "../../lib/hooks";
import {
  EMPTY_LAYOUT,
  clearLibraryLayout,
  effectiveDedupe,
  hasLibraryLayout,
  mediaSectionForLibrary,
  prune,
  sameLayout,
  setDedupe,
} from "../../lib/layout";
import { fieldMeta, isRuleComplete, type FieldMeta } from "../../lib/rules";
import { titlePosterURL } from "../../lib/titles";
import { RuleRow } from "../collections/RuleRow";
import { AnchoredSlots } from "../designer/AnchoredSlots";
import { PageSettings } from "../designer/PageSettings";
import { RowPool } from "../designer/RowPool";
import {
  librariesApi,
  type Library,
  type LibraryAvailableFacets,
  type LibraryFacetCategory,
  type LibraryFacetOption,
  type LibraryTab,
  type LibraryTabKind,
  type LibraryTabPreviewItem,
} from "./api";
import { ExtendedCondition, browseFields, newBrowseCondition } from "./browser/RuleCondition";

const RULE_FIELDS: RuleField[] = [
  "title",
  "genre",
  "keyword",
  "studio",
  "country",
  "language",
  "contentRating",
  "year",
  "rating",
  "votes",
  "popularity",
  "runtime",
];

const FALLBACK_CATALOG_SORTS = [
  "popularity",
  "added",
  "released",
  "year",
  "rating",
  "duration",
  "title",
  "lastViewed",
  "server",
] as const;

export function supportsLibraryRecommendedRows(library: Library): boolean {
  if (library.source === "plugin") return true;
  return !library.mediaType || library.mediaType === "movie" || library.mediaType === "show";
}

function metaForField(field: RuleField): FieldMeta | undefined {
  const base = fieldMeta(field);
  if (!base) return undefined;
  if (field === "title") return { ...base, label: t("Specific titles") };
  const extra = [...base.ops, "unknown" as const, "known" as const];
  if (field === "keyword")
    return { ...base, kind: "facet", facet: "keyword", ops: extra, defaultOp: "in" };
  if (field === "studio")
    return {
      ...base,
      label: t("Studio or network"),
      kind: "facet",
      facet: "studio",
      ops: extra,
      defaultOp: "in",
    };
  if (field === "contentRating") return base;
  if (base.kind === "facet" || base.kind === "number") return { ...base, ops: extra };
  return base;
}

function pruneIncompleteRule(rule: Rule): Rule | null {
  if (rule.field === "group") {
    if (!Array.isArray(rule.value)) return null;
    const children = (rule.value as Rule[])
      .map(pruneIncompleteRule)
      .filter((c): c is Rule => c !== null);
    return children.length > 0 ? { ...rule, value: children } : null;
  }
  return isRuleComplete(rule) ? rule : null;
}

export function sortFieldLabel(sort: string): string {
  switch (sort) {
    case "title":
      return t("Title");
    case "added":
      return t("Date added");
    case "released":
      return t("Release date");
    case "year":
      return t("Year");
    case "rating":
      return t("Rating");
    case "popularity":
      return t("Popularity");
    case "duration":
      return t("Runtime");
    case "index":
      return t("Order");
    case "lastViewed":
      return t("Recently watched");
    case "server":
      return t("Default order");
    default:
      return sort;
  }
}

const FACET_CATEGORY_ORDER: LibraryFacetCategory[] = [
  "essentials",
  "people",
  "technical",
  "plugin",
];

function inferFacetCategory(facet: LibraryFacetOption, isPlugin: boolean): LibraryFacetCategory {
  if (
    facet.category === "essentials" ||
    facet.category === "people" ||
    facet.category === "technical" ||
    facet.category === "plugin"
  ) {
    return facet.category;
  }
  switch (facet.id) {
    case "cast":
    case "director":
    case "writer":
    case "author":
    case "narrator":
      return "people";
    case "resolution":
    case "hdr":
    case "audioLanguage":
      return "technical";
    default:
      if (isPlugin) {
        return facet.id === "year" ||
          facet.id === "decade" ||
          facet.id === "released" ||
          facet.id === "rating"
          ? "essentials"
          : "plugin";
      }
      return "essentials";
  }
}

function facetCategoryTitle(category: LibraryFacetCategory): string {
  switch (category) {
    case "essentials":
      return t("Essentials");
    case "people":
      return t("People");
    case "technical":
      return t("Technical");
    case "plugin":
      return t("Plugin fields");
  }
}

export function facetOptionLabel(facet: LibraryFacetOption): string {
  switch (facet.id) {
    case "":
      return t("Owner collections");
    case "genre":
      return t("Genres");
    case "studio":
      return t("Studios");
    case "network":
      return t("Networks");
    case "country":
      return t("Countries");
    case "language":
      return facet.category === "plugin" ? facet.label : t("Languages");
    case "contentRating":
      return t("Content ratings");
    case "decade":
      return facet.label === "Decade" ? t("Decade") : t("Decades");
    case "year":
      return facet.label === "Year" ? t("Year") : t("Years");
    case "released":
      return t("Released");
    case "franchise":
      return t("Franchises");
    case "tag":
      return t("Tags");
    case "keyword":
      return t("Keywords");
    case "rating":
      return facet.label === "Rating" ? t("Rating") : t("Rating bands");
    case "runtime":
      return t("Runtime bands");
    case "addedMonth":
      return t("Added month");
    case "resolution":
      return t("Resolutions");
    case "hdr":
      return t("HDR");
    case "audioLanguage":
      return t("Audio languages");
    case "subject":
      return t("Subjects");
    case "series":
      return t("Series");
    case "publisher":
      return t("Publishers");
    case "cast":
      return t("Actors");
    case "director":
      return t("Directors");
    case "writer":
      return t("Writers");
    case "author":
      return t("Authors");
    case "narrator":
      return t("Narrators");
    default:
      return facet.label;
  }
}

function defaultDirectionForSort(sort: string): "asc" | "desc" {
  return sort === "title" || sort === "index" || sort === "server" ? "asc" : "desc";
}

function directionOptions(sort: string): { value: "asc" | "desc"; label: string }[] {
  if (sort === "title") {
    return [
      { value: "asc", label: t("A–Z") },
      { value: "desc", label: t("Z–A") },
    ];
  }
  if (sort === "index" || sort === "server") {
    return [
      { value: "asc", label: t("First to last") },
      { value: "desc", label: t("Last to first") },
    ];
  }
  if (sort === "lastViewed") {
    return [
      { value: "desc", label: t("Recent first") },
      { value: "asc", label: t("Oldest first") },
    ];
  }
  if (sort === "added" || sort === "released" || sort === "year") {
    return [
      { value: "desc", label: t("Newest first") },
      { value: "asc", label: t("Oldest first") },
    ];
  }
  if (sort === "duration") {
    return [
      { value: "desc", label: t("Longest first") },
      { value: "asc", label: t("Shortest first") },
    ];
  }
  return [
    { value: "desc", label: t("Highest first") },
    { value: "asc", label: t("Lowest first") },
  ];
}

export function tabKindIcon(kind: LibraryTabKind, size = 16) {
  switch (kind) {
    case "recommended":
      return <Sparkles size={size} />;
    case "library":
      return <LibraryIcon size={size} />;
    case "collections":
      return <Folders size={size} />;
    case "people":
      return <Users size={size} />;
    case "view":
      return <Filter size={size} />;
    case "live":
      return <Radio size={size} />;
  }
}

export function tabKindTitle(kind: LibraryTabKind): string {
  switch (kind) {
    case "recommended":
      return t("Recommended");
    case "library":
      return t("Everything");
    case "collections":
      return t("Collections by…");
    case "people":
      return t("People in…");
    case "view":
      return t("Saved view");
    case "live":
      return t("Live");
  }
}

export function slugTabKey(label: string, kind: LibraryTabKind, existingKeys: Set<string>): string {
  const raw = label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  let base = raw && /^[a-z0-9]/.test(raw) ? raw : kind;
  base = base.slice(0, 56).replace(/-+$/g, "") || kind;
  if (!existingKeys.has(base)) return base;
  let n = 2;
  while (existingKeys.has(`${base}-${n}`)) n++;
  return `${base}-${n}`;
}

export function defaultLabelForFacet(
  facet: LibraryFacetOption | undefined,
  kind: LibraryTabKind,
): string {
  if (!facet) return kind === "people" ? t("People") : t("Collections");
  switch (facet.id) {
    case "":
      return t("Collections");
    case "genre":
      return t("Genres");
    case "studio":
      return t("Studios");
    case "network":
      return t("Networks");
    case "country":
      return t("Countries");
    case "decade":
      return t("Decades");
    case "year":
      return t("Years");
    case "franchise":
      return t("Franchises");
    case "subject":
      return t("Subjects");
    case "series":
      return t("Series");
    case "publisher":
      return t("Publishers");
    case "language":
      return facet.category === "plugin" ? facet.label : t("Languages");
    case "contentRating":
      return t("Content ratings");
    case "tag":
      return t("Tags");
    case "keyword":
      return t("Keywords");
    case "rating":
      return facet.label === "Rating" ? t("Rating") : t("Ratings");
    case "runtime":
      return t("Runtime");
    case "addedMonth":
      return t("Added month");
    case "released":
      return t("Released");
    case "resolution":
      return t("Resolutions");
    case "hdr":
      return t("HDR");
    case "audioLanguage":
      return t("Audio languages");
    case "cast":
      return t("Cast");
    case "director":
      return t("Directors");
    case "writer":
      return t("Writers");
    case "author":
      return t("Authors");
    case "narrator":
      return t("Narrators");
    default:
      return facet.label;
  }
}

export function createSeedTab(
  kind: LibraryTabKind,
  library: Library,
  availableFacets: LibraryAvailableFacets,
  availableSorts: string[],
  existingKeys: Set<string>,
): LibraryTab {
  const defaultSort =
    library.source === "plugin"
      ? (availableSorts[0] ?? library.kind?.browse.sorts?.[0] ?? "server")
      : availableSorts.includes("added")
        ? "added"
        : (availableSorts[0] ?? "popularity");
  const defaultDir = defaultDirectionForSort(defaultSort);

  if (kind === "recommended") {
    const label = t("Recommended");
    return {
      key: slugTabKey("recommended", kind, existingKeys),
      label,
      kind: "recommended",
    };
  }
  if (kind === "library") {
    const label = library.source === "plugin" ? library.name : t("Library");
    return {
      key: slugTabKey("library", kind, existingKeys),
      label,
      kind: "library",
      sort: defaultSort,
      direction: defaultDir,
      search: true,
    };
  }
  if (kind === "collections") {
    const facet =
      availableFacets.collections.find((f) => f.id !== "") ?? availableFacets.collections[0];
    const facetId = facet?.id ?? "";
    const label = defaultLabelForFacet(facet, "collections");
    return {
      key: slugTabKey(facetId || "collections", kind, existingKeys),
      label,
      kind: "collections",
      facet: facetId,
      ...(facetId
        ? {
            order: "count" as const,
            sort: defaultSort,
            direction: defaultDir,
          }
        : {}),
      search: true,
    };
  }
  if (kind === "people") {
    const facet = availableFacets.people[0];
    const label = defaultLabelForFacet(facet, "people");
    return {
      key: slugTabKey(facet?.id ?? "people", kind, existingKeys),
      label,
      kind: "people",
      facet: facet?.id ?? "",
      order: "name",
      search: true,
    };
  }
  if (kind === "live") {
    return {
      key: slugTabKey("live", kind, existingKeys),
      label: t("Live"),
      kind: "live",
      card: "landscape",
      search: true,
    };
  }
  const label = t("Unwatched");
  const seedRule: Rule = { field: "viewer", op: "is", value: { unwatched: true } };
  return {
    key: slugTabKey("unwatched", kind, existingKeys),
    label,
    kind: "view",
    layout: "grid",
    sort: defaultSort,
    direction: defaultDir,
    search: true,
    filters: { match: "all", rules: [seedRule] },
  };
}

export function AddTabPickerDrawer({
  open,
  library,
  availableFacets,
  existingTabs,
  onPick,
  onClose,
}: {
  open: boolean;
  library: Library;
  availableFacets: LibraryAvailableFacets;
  existingTabs: LibraryTab[];
  onPick: (kind: LibraryTabKind) => void;
  onClose: () => void;
}) {
  const { locale } = useLocale();
  const hasCollectionsFacet = availableFacets.collections.length > 0;
  const hasPeopleFacet = availableFacets.people.length > 0;
  const hasRecommended = existingTabs.some((tab) => tab.kind === "recommended");

  const cards = useMemo(
    () => [
      {
        kind: "recommended" as const,
        title: t("Recommended"),
        description: t("Personalized rows and collections arranged for this library."),
        badge: hasRecommended ? t("Already in tabs") : undefined,
        disabled: false,
      },
      {
        kind: "library" as const,
        title: t("Everything"),
        description: t("Every item in {name}, with a default sort and optional filter.", {
          name: library.name,
        }),
        disabled: false,
      },
      {
        kind: "collections" as const,
        title: t("Collections by…"),
        description: hasCollectionsFacet
          ? t("Group items into browsable buckets like genre, studio, decade or subject.")
          : t("No collection facets are indexed for this library."),
        disabled: !hasCollectionsFacet,
      },
      {
        kind: "people" as const,
        title: t("People in…"),
        description: hasPeopleFacet
          ? t("Browse actors, directors, authors or narrators in this library.")
          : t("No people facets are indexed for this library."),
        disabled: !hasPeopleFacet,
      },
      {
        kind: "view" as const,
        title: t("Saved view"),
        description: t("A filtered slice of this library — such as Unwatched, 4K or 90s Classics."),
        disabled: false,
      },
      ...(library.source === "plugin"
        ? [
            {
              kind: "live" as const,
              title: t("Live"),
              description: t(
                "Currently live streams supplied by this plugin with search and direct playback.",
              ),
              disabled: false,
            },
          ]
        : []),
    ],
    [hasCollectionsFacet, hasPeopleFacet, hasRecommended, library.name, library.source, locale],
  );

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={t("Add tab")}
      description={t("Choose what this tab shows when viewers open {name} in Vela.", {
        name: library.name,
      })}
      size="md"
    >
      <div className="space-y-2.5">
        {cards.map((card) => (
          <button
            key={card.kind}
            type="button"
            disabled={card.disabled}
            onClick={() => onPick(card.kind)}
            className={cn(
              "flex w-full items-start gap-3.5 rounded-xl border border-border bg-surface p-3.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
              card.disabled
                ? "cursor-not-allowed opacity-50"
                : "hover:border-primary/40 hover:bg-surface-hover",
            )}
          >
            <span className="mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg bg-primary-subtle text-primary">
              {tabKindIcon(card.kind, 18)}
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-semibold text-fg">{card.title}</span>
                {card.badge ? <Badge variant="secondary">{card.badge}</Badge> : null}
              </span>
              <span className="mt-0.5 block text-xs text-fg-muted">{card.description}</span>
            </span>
          </button>
        ))}
      </div>
    </Drawer>
  );
}

function previewPosterSrc(item: LibraryTabPreviewItem): string | undefined {
  if (item.poster) return item.poster;
  const pluginArt = item.art?.find((a) => a.role === "poster")?.url ?? item.art?.[0]?.url;
  if (pluginArt) return pluginArt;
  return titlePosterURL(item, 185);
}

export function TabEditDrawer({
  open,
  isNew,
  initialTab,
  library,
  availableFacets,
  availableSorts,
  existingKeys,
  canHide = true,
  canDelete = true,
  libraryLayoutDoc,
  layoutLoading,
  saving,
  onSave,
  onDelete,
  onClose,
}: {
  open: boolean;
  isNew: boolean;
  initialTab: LibraryTab | null;
  library: Library;
  availableFacets: LibraryAvailableFacets;
  availableSorts: string[];
  existingKeys: Set<string>;
  canHide?: boolean;
  canDelete?: boolean;
  libraryLayoutDoc?: LibraryLayoutDoc | null;
  layoutLoading?: boolean;
  saving: boolean;
  onSave: (tab: LibraryTab, nextLayout?: HomeLayout) => void;
  onDelete?: () => void;
  onClose: () => void;
}) {
  if (!open || !initialTab) return null;
  return (
    <TabEditDrawerInner
      key={`${isNew ? "new" : "edit"}:${initialTab.key}:${initialTab.kind}`}
      isNew={isNew}
      initialTab={initialTab}
      library={library}
      availableFacets={availableFacets}
      availableSorts={availableSorts}
      existingKeys={existingKeys}
      canHide={canHide}
      canDelete={canDelete}
      libraryLayoutDoc={libraryLayoutDoc ?? null}
      layoutLoading={layoutLoading ?? false}
      saving={saving}
      onSave={onSave}
      onDelete={onDelete}
      onClose={onClose}
    />
  );
}

function TabEditDrawerInner({
  isNew,
  initialTab,
  library,
  availableFacets,
  availableSorts: serverSorts,
  existingKeys,
  canHide,
  canDelete,
  libraryLayoutDoc,
  layoutLoading,
  saving,
  onSave,
  onDelete,
  onClose,
}: {
  isNew: boolean;
  initialTab: LibraryTab;
  library: Library;
  availableFacets: LibraryAvailableFacets;
  availableSorts: string[];
  existingKeys: Set<string>;
  canHide: boolean;
  canDelete: boolean;
  libraryLayoutDoc: LibraryLayoutDoc | null;
  layoutLoading: boolean;
  saving: boolean;
  onSave: (tab: LibraryTab, nextLayout?: HomeLayout) => void;
  onDelete?: () => void;
  onClose: () => void;
}) {
  const config = useAsync(() => api.getConfig(), []);
  const region = String(asObj(config.data?.catalog).region ?? "");
  const [draft, setDraft] = useState<LibraryTab>(() => structuredClone(initialTab));
  const [labelTouched, setLabelTouched] = useState(!isNew);
  const [freshRuleIndex, setFreshRuleIndex] = useState<number | null>(null);

  const isPlugin = library.source === "plugin";
  const section: LibraryLayoutSection = `library:${library.id}`;
  const mediaSection = isPlugin ? null : mediaSectionForLibrary(library);
  const supportsRecommendedRows = supportsLibraryRecommendedRows(library);
  const { viewers, error: viewersError } = useViewers();
  const [selectedViewerId, setSelectedViewerId] = useState<number | null | undefined>(undefined);
  const [previewAt] = useState(() => new Date().toISOString());

  const defaultViewerId = useMemo(
    () => viewers?.find((v) => v.isOwner)?.id ?? viewers?.[0]?.id ?? null,
    [viewers],
  );
  const viewersReady = viewers !== null || Boolean(viewersError);
  const previewViewerId = selectedViewerId !== undefined ? selectedViewerId : defaultViewerId;
  const previewViewer = (viewers ?? []).find((v) => v.id === previewViewerId);
  const previewWho = previewViewer ? viewerName(previewViewer) : t("Typical viewer");

  // i18n: memo holds no copy
  const savedLayout = useMemo(
    () => (libraryLayoutDoc ? prune(libraryLayoutDoc.layout) : EMPTY_LAYOUT),
    [libraryLayoutDoc],
  );
  const [adoptedLayout, setAdoptedLayout] = useState<LibraryLayoutDoc | null>(
    () => libraryLayoutDoc,
  );
  const [layoutDraft, setLayoutDraft] = useState<HomeLayout>(() =>
    libraryLayoutDoc ? prune(libraryLayoutDoc.layout) : EMPTY_LAYOUT,
  );
  const [focusRow, setFocusRow] = useState<string | null>(null);
  if (libraryLayoutDoc && libraryLayoutDoc !== adoptedLayout) {
    setAdoptedLayout(libraryLayoutDoc);
    setLayoutDraft(prune(libraryLayoutDoc.layout));
  }
  const availableRows = (libraryLayoutDoc?.availableRows ?? []).map((row) =>
    row.kind === "favorites"
      ? {
          ...row,
          title: t("Favorites"),
          description: t("Titles this viewer marked as favorites."),
        }
      : row,
  );
  const rowsCustomised = supportsRecommendedRows && hasLibraryLayout(layoutDraft, section);
  const layoutDirty = Boolean(libraryLayoutDoc) && !sameLayout(layoutDraft, savedLayout);
  const debouncedLayoutDraft = useDebouncedValue(layoutDraft, 200);
  const hasLoadedLibraryLayout = Boolean(libraryLayoutDoc);
  const rowPreview = useAsync(
    (signal) => {
      if (
        draft.kind !== "recommended" ||
        !supportsRecommendedRows ||
        !hasLoadedLibraryLayout ||
        !viewersReady
      ) {
        return Promise.resolve(null);
      }
      const dirty = !sameLayout(debouncedLayoutDraft, savedLayout);
      return api.previewLayout(
        {
          viewerId: previewViewerId,
          device: "tv",
          at: previewAt,
          section,
          ...(dirty ? { layout: debouncedLayoutDraft } : {}),
        },
        signal,
      );
    },
    [
      draft.kind,
      supportsRecommendedRows,
      hasLoadedLibraryLayout,
      viewersReady,
      previewViewerId,
      previewAt,
      section,
      debouncedLayoutDraft,
      savedLayout,
    ],
  );
  const previewRequest = useMemo(
    () =>
      draft.kind === "recommended" &&
      supportsRecommendedRows &&
      hasLoadedLibraryLayout &&
      viewersReady
        ? {
            viewerId: previewViewerId,
            device: "tv" as const,
            at: previewAt,
          }
        : null,
    [
      draft.kind,
      supportsRecommendedRows,
      hasLoadedLibraryLayout,
      viewersReady,
      previewViewerId,
      previewAt,
    ],
  );
  const mediaType: "movie" | "show" | "mixed" =
    library.mediaType === "movie" || library.mediaType === "show" ? library.mediaType : "mixed";

  const availableSorts: string[] = serverSorts.length
    ? serverSorts
    : isPlugin
      ? library.kind?.browse.sorts?.length
        ? library.kind.browse.sorts
        : ["server"]
      : [...FALLBACK_CATALOG_SORTS];

  const [facetQuery, setFacetQuery] = useState("");
  const matchingFacets =
    draft.kind === "people" ? availableFacets.people : availableFacets.collections;
  const showFacetSearch = matchingFacets.length > 8;
  const normalizedFacetQuery = facetQuery.trim().toLowerCase();
  const visibleFacets = normalizedFacetQuery
    ? matchingFacets.filter((facet) => {
        const label = facetOptionLabel(facet).toLowerCase();
        const catTitle = facetCategoryTitle(inferFacetCategory(facet, isPlugin)).toLowerCase();
        return (
          label.includes(normalizedFacetQuery) ||
          facet.label.toLowerCase().includes(normalizedFacetQuery) ||
          facet.id.toLowerCase().includes(normalizedFacetQuery) ||
          catTitle.includes(normalizedFacetQuery)
        );
      })
    : matchingFacets;
  const facetSections = FACET_CATEGORY_ORDER.map((category) => ({
    category,
    items: visibleFacets.filter((f) => inferFacetCategory(f, isPlugin) === category),
  })).filter((sec) => sec.items.length > 0);
  const showFacetCategoryHeaders =
    draft.kind === "collections" &&
    (matchingFacets.some((f) => Boolean(f.category)) || facetSections.length > 1);

  const supportsFacet = draft.kind === "collections" || draft.kind === "people";
  const supportsOrder =
    draft.kind === "people" || (draft.kind === "collections" && Boolean(draft.facet));
  const supportsRules = draft.kind === "view" || draft.kind === "library";
  const supportsSort =
    draft.kind === "library" ||
    draft.kind === "view" ||
    (draft.kind === "collections" && Boolean(draft.facet));
  const supportsSearch = draft.kind !== "recommended";

  const rules = draft.filters?.rules ?? [];
  const matchMode = draft.filters?.match === "any" ? "any" : "all";
  // i18n: memo holds no copy
  const completeRules = useMemo(
    () => rules.map(pruneIncompleteRule).filter((r): r is Rule => r !== null),
    [rules],
  );
  const allRulesComplete = rules.every(isRuleComplete);

  const currentSort =
    draft.sort && availableSorts.includes(draft.sort)
      ? draft.sort
      : (availableSorts[0] ?? (isPlugin ? "server" : "added"));
  const dirOpts = directionOptions(currentSort);
  const currentDirection: "asc" | "desc" = draft.direction ?? defaultDirectionForSort(currentSort);
  const searchEnabled = draft.search !== false;

  const facetValid =
    !supportsFacet ||
    (draft.kind === "collections" && !isPlugin
      ? matchingFacets.some((f) => f.id === (draft.facet ?? ""))
      : Boolean(draft.facet));

  const canSave =
    Boolean(draft.label.trim()) &&
    facetValid &&
    (draft.kind !== "view" || completeRules.length > 0);

  // i18n: memo holds no copy
  const previewTabPayload = useMemo<LibraryTab | null>(() => {
    if (draft.kind === "recommended" || draft.kind === "live") return null;
    if (supportsFacet && !facetValid) return null;
    const cleaned: LibraryTab = {
      key: draft.key || "preview",
      label: draft.label.trim() || "Preview",
      kind: draft.kind,
    };
    if (supportsFacet) {
      cleaned.facet = draft.facet ?? "";
    }
    if (supportsOrder) {
      cleaned.order = draft.order ?? (draft.kind === "people" ? "name" : "count");
    }
    if (supportsSort) {
      cleaned.sort = currentSort;
      cleaned.direction = currentDirection;
    }
    if (supportsRules && completeRules.length > 0) {
      cleaned.filters = {
        match: matchMode,
        rules: completeRules,
      };
    }
    return cleaned;
  }, [
    completeRules,
    currentDirection,
    currentSort,
    draft.facet,
    draft.key,
    draft.kind,
    draft.label,
    draft.order,
    facetValid,
    matchMode,
    supportsFacet,
    supportsOrder,
    supportsRules,
    supportsSort,
  ]);

  const debouncedPreviewTab = useDebouncedValue(previewTabPayload, 250);

  const livePreview = useAsync(
    (signal) =>
      debouncedPreviewTab
        ? librariesApi.previewTab(library.id, { tab: debouncedPreviewTab, limit: 12 }, signal)
        : Promise.resolve(null),
    [library.id, debouncedPreviewTab],
  );

  const baselineLibraryPreview = useAsync(
    (signal) =>
      supportsRules
        ? librariesApi.previewTab(
            library.id,
            { tab: { key: "library", label: "Library", kind: "library" }, limit: 1 },
            signal,
          )
        : Promise.resolve(null),
    [supportsRules, library.id],
  );

  // Plugin field mapping for RuleRow
  // i18n: memo holds no copy
  const { allowedFields, pluginFields } = useMemo(() => {
    if (!isPlugin) return { allowedFields: RULE_FIELDS, pluginFields: undefined };
    const aliases: Record<string, string[]> = {
      genre: ["genre", "genres", "tags", "tag", "subject"],
      studio: ["studio", "studios", "publisher"],
      country: ["country", "countries"],
      keyword: ["keyword", "keywords", "series"],
      language: ["language"],
      runtime: ["runtime", "duration", "length"],
      votes: ["votes"],
      popularity: ["popularity"],
    };
    const mapped: Record<string, string> = {};
    for (const [field, keys] of Object.entries(aliases)) {
      const key = keys.find((k) =>
        library.kind?.fields?.some((f) => f.key === k && f.indexed !== false),
      );
      if (key) mapped[field] = key;
    }
    return {
      allowedFields: RULE_FIELDS.filter(
        (f) => f === "year" || f === "rating" || Boolean(mapped[f]),
      ),
      pluginFields: mapped,
    };
  }, [isPlugin, library.kind]);

  const setRules = (nextRules: Rule[], nextMatch: "all" | "any" = matchMode) => {
    setDraft((cur) => ({
      ...cur,
      filters: nextRules.length ? { match: nextMatch, rules: nextRules } : undefined,
    }));
  };

  const renderCondition = (
    rule: Rule,
    j: number,
    change: (r: Rule) => void,
    remove: () => void,
  ): React.ReactNode =>
    rule.field.startsWith("browse:") || rule.field === "group" || rule.field === "viewer" ? (
      <ExtendedCondition
        key={j}
        rule={rule}
        onChange={change}
        onRemove={remove}
        renderChild={renderCondition}
        kind={isPlugin ? library.kind : undefined}
        libraryId={isPlugin ? library.id : undefined}
        level={isPlugin ? draft.level : undefined}
      />
    ) : (
      <RuleRow
        key={j}
        rule={rule}
        index={j}
        kind="include"
        mediaType={mediaType}
        region={region}
        allowedFields={allowedFields}
        facetLibraryId={isPlugin ? library.id : undefined}
        facetFields={pluginFields}
        metaFor={metaForField}
        inline
        openValue={freshRuleIndex === j}
        onChange={change}
        onRemove={remove}
      />
    );

  // i18n: memo holds no copy
  const previewBuckets = useMemo(() => {
    if (!livePreview.data) return [];
    if (livePreview.data.kind === "collections") {
      return livePreview.data.collections.map((c) => ({
        id: c.id,
        name: c.name,
        count: c.itemCount,
      }));
    }
    if (livePreview.data.kind === "people") {
      return livePreview.data.people.map((p) => ({
        id: String(p.id),
        name: p.name,
        count: p.itemCount,
      }));
    }
    return [];
  }, [livePreview.data]);

  const commit = () => {
    if (!canSave) return;
    const key = isNew ? slugTabKey(draft.label.trim(), draft.kind, existingKeys) : draft.key;
    const cleaned: LibraryTab = {
      key,
      label: draft.label.trim(),
      kind: draft.kind,
    };
    if (supportsFacet && draft.facet) {
      cleaned.facet = draft.facet;
    }
    if (supportsOrder) {
      cleaned.order = draft.order ?? (draft.kind === "people" ? "name" : "count");
    }
    if (supportsSort) {
      cleaned.sort = currentSort;
      cleaned.direction = currentDirection;
    }
    if (draft.kind === "live") {
      cleaned.card = draft.card ?? "landscape";
    }
    if (draft.kind === "view") {
      cleaned.layout = draft.layout ?? "grid";
    }
    if (supportsSearch) {
      cleaned.search = searchEnabled;
      if (searchEnabled && draft.searchPlaceholder?.trim()) {
        cleaned.searchPlaceholder = draft.searchPlaceholder.trim();
      }
    }
    if (supportsRules && completeRules.length > 0) {
      cleaned.filters = {
        match: matchMode,
        rules: completeRules,
      };
    }
    if (draft.hidden && canHide) {
      cleaned.hidden = true;
    }
    onSave(
      cleaned,
      draft.kind === "recommended" && supportsRecommendedRows && layoutDirty
        ? layoutDraft
        : undefined,
    );
  };

  const itemsPreviewData =
    livePreview.data && (livePreview.data.kind === "library" || livePreview.data.kind === "view")
      ? livePreview.data
      : null;
  const baselineItemsTotal =
    baselineLibraryPreview.data &&
    (baselineLibraryPreview.data.kind === "library" || baselineLibraryPreview.data.kind === "view")
      ? baselineLibraryPreview.data.total
      : (library.itemCount ?? 0);
  const previewCount = itemsPreviewData?.total ?? baselineItemsTotal;
  const previewTotal = Math.max(previewCount, baselineItemsTotal);
  const previewItems = itemsPreviewData?.items ?? [];

  const defaultSearchPlaceholder =
    draft.kind === "people"
      ? t("Search people…")
      : draft.kind === "collections"
        ? t("Search collections…")
        : t("Search {name}…", { name: draft.label.trim() || library.name });

  return (
    <Drawer
      open
      onClose={onClose}
      title={
        isNew
          ? t("New {kind} tab", { kind: tabKindTitle(draft.kind) })
          : t("Edit {label}", { label: initialTab.label })
      }
      description={tabKindTitle(draft.kind)}
      size={draft.kind === "recommended" && supportsRecommendedRows ? "xl" : "lg"}
      footer={
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          <div>
            {!isNew && onDelete ? (
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={saving || !canDelete}
                onClick={onDelete}
              >
                {t("Delete tab")}
              </Button>
            ) : null}
          </div>
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button type="button" size="sm" disabled={!canSave || saving} onClick={commit}>
              {isNew ? t("Add tab") : t("Save tab")}
            </Button>
          </div>
        </div>
      }
    >
      <div className="space-y-5">
        {/* Label */}
        <div className="space-y-1.5">
          <label className="block text-sm font-medium text-fg" htmlFor="tab-label-input">
            {t("Label")}
          </label>
          <Input
            id="tab-label-input"
            aria-label={t("Tab label")}
            value={draft.label}
            placeholder={tabKindTitle(draft.kind)}
            onChange={(e) => {
              setLabelTouched(true);
              setDraft({ ...draft, label: e.target.value });
            }}
          />
          <p className="text-xs text-fg-muted">
            {t("Shown in the tab bar at the top of {name} in Vela.", { name: library.name })}
          </p>
        </div>

        {/* Rows on this tab (embedded Recommended rows editor) */}
        {draft.kind === "recommended" && supportsRecommendedRows ? (
          <section
            aria-label={t("Rows on this tab")}
            className="space-y-4 rounded-xl border border-border bg-surface p-4"
          >
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="flex min-w-0 flex-1 items-start gap-3">
                <span className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg bg-primary-subtle text-primary">
                  <Sparkles size={16} />
                </span>
                <div className="min-w-0 flex-1 space-y-1">
                  <h3 className="text-sm font-medium text-fg">{t("Rows on this tab")}</h3>
                  <p className="text-xs text-fg-muted">
                    {isPlugin
                      ? t("Rows arranged for {name}'s Recommended tab.", { name: library.name })
                      : t(
                          "These rows also show on this library's Recommended page in Plex, Jellyfin and Emby.",
                        )}
                  </p>
                </div>
              </div>
              {rowsCustomised ? (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => setLayoutDraft(clearLibraryLayout(layoutDraft, section))}
                >
                  <RotateCcw size={13} />
                  {t("Reset to default rows")}
                </Button>
              ) : null}
            </div>

            {layoutLoading && !libraryLayoutDoc ? (
              <div className="flex items-center gap-2 py-3 text-xs text-fg-muted">
                <Spinner size={14} label={t("Loading rows")} />
                <span>{t("Loading rows…")}</span>
              </div>
            ) : (
              <div className="space-y-5 border-t border-border-subtle pt-4">
                {(viewers ?? []).length > 0 ? (
                  <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border-subtle bg-bg-subtle/40 px-3 py-2">
                    <div className="flex min-w-0 items-center gap-2">
                      {previewViewer ? (
                        <Avatar src={previewViewer.thumb} name={previewWho} size={24} />
                      ) : (
                        <span className="grid size-6 place-items-center rounded-full bg-primary-subtle text-primary">
                          <Sparkles size={13} />
                        </span>
                      )}
                      <span className="text-xs font-medium text-fg-muted">{t("Preview as")}</span>
                    </div>
                    <div className="w-56">
                      <SearchSelect
                        value={previewViewerId === null ? "" : String(previewViewerId)}
                        onChange={(v) => setSelectedViewerId(v ? Number(v) : null)}
                        ariaLabel={t("Preview as")}
                        placeholder={t("Select…")}
                        searchPlaceholder={t("Search…")}
                        noMatchesLabel={t("No matches")}
                        selectLabel={t("Select")}
                        closeLabel={t("Close")}
                        options={[
                          {
                            value: "",
                            label: t("Typical viewer"),
                            hint: t("The Home a new viewer gets"),
                          },
                          ...(viewers ?? []).map((v) => ({
                            value: String(v.id),
                            label: viewerName(v),
                            hint: t("Spot-check, with their overrides"),
                          })),
                        ]}
                      />
                    </div>
                  </div>
                ) : null}
                <AnchoredSlots
                  draft={layoutDraft}
                  onChange={setLayoutDraft}
                  layer="global"
                  section={section}
                  viewers={viewers ?? []}
                  available={availableRows}
                  mediaSection={mediaSection}
                />
                <div className="border-t border-border-subtle pt-4">
                  <RowPool
                    draft={layoutDraft}
                    onChange={setLayoutDraft}
                    layer="global"
                    section={section}
                    viewers={viewers ?? []}
                    available={availableRows}
                    focusRow={focusRow}
                    onFocusRow={setFocusRow}
                    mediaSection={mediaSection}
                    preview={rowPreview.data}
                    previewWho={previewWho}
                    previewRequest={previewRequest}
                  />
                </div>
                <div className="space-y-3 border-t border-border-subtle pt-4">
                  <PageSettings
                    draft={layoutDraft}
                    onChange={setLayoutDraft}
                    layer="global"
                    section={section}
                    viewers={viewers ?? []}
                    defaults={{
                      home: 14,
                      library: libraryLayoutDoc?.defaults.maxRows ?? 16,
                    }}
                    label={t("Recommended")}
                  />
                  {!isPlugin ? (
                    <div className="flex items-start gap-3 rounded-lg border border-border bg-bg-subtle/40 p-3">
                      <Switch
                        aria-label={t("Don't repeat what Home shows")}
                        checked={effectiveDedupe(layoutDraft, "global", section).on}
                        onChange={(e) =>
                          setLayoutDraft(
                            setDedupe(layoutDraft, "global", section, e.currentTarget.checked),
                          )
                        }
                      />
                      <div className="min-w-0 flex-1">
                        <p className="text-sm font-medium text-fg">
                          {t("Don't repeat what Home shows")}
                        </p>
                        <p className="text-xs text-fg-muted">
                          {t(
                            "Titles the viewer's Home leads with stay off {page}, so switching tabs always shows something new.",
                            { page: library.name },
                          )}
                        </p>
                      </div>
                    </div>
                  ) : null}
                </div>
              </div>
            )}
          </section>
        ) : null}

        {/* Facet picker + bucket order + live bucket preview for collections & people */}
        {supportsFacet ? (
          <section className="space-y-4 rounded-xl border border-border bg-surface p-4">
            <div className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm font-medium text-fg">
                  {draft.kind === "people" ? t("Group people by") : t("Group collections by")}
                </p>
                {showFacetSearch ? (
                  <div className="relative w-full sm:w-56">
                    <Search
                      size={14}
                      aria-hidden
                      className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-muted"
                    />
                    <Input
                      aria-label={t("Filter fields")}
                      value={facetQuery}
                      onChange={(e) => setFacetQuery(e.target.value)}
                      placeholder={t("Filter fields…")}
                      className="h-8 pl-8 text-xs"
                    />
                  </div>
                ) : null}
              </div>
              <div
                className="space-y-3"
                role="radiogroup"
                aria-label={
                  draft.kind === "people" ? t("Group people by") : t("Group collections by")
                }
              >
                {facetSections.length === 0 ? (
                  <p className="rounded-lg border border-dashed border-border px-3 py-3 text-xs text-fg-muted">
                    {t("No matching fields.")}
                  </p>
                ) : showFacetCategoryHeaders ? (
                  facetSections.map((sec) => (
                    <div key={sec.category} className="space-y-1.5">
                      <p className="text-[11px] font-semibold uppercase tracking-wider text-fg-muted">
                        {facetCategoryTitle(sec.category)}
                      </p>
                      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                        {sec.items.map((facet) => {
                          const active = (draft.facet ?? "") === facet.id;
                          return (
                            <button
                              key={facet.id || "__owner_collections"}
                              type="button"
                              role="radio"
                              aria-checked={active}
                              onClick={() => {
                                const nextLabel = labelTouched
                                  ? draft.label
                                  : defaultLabelForFacet(facet, draft.kind);
                                setDraft({ ...draft, facet: facet.id, label: nextLabel });
                              }}
                              className={cn(
                                "rounded-lg border px-3 py-2 text-left text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                                active
                                  ? "border-primary bg-primary-subtle text-primary"
                                  : "border-border bg-bg-subtle/50 text-fg hover:bg-surface-hover",
                              )}
                            >
                              {facetOptionLabel(facet)}
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  ))
                ) : (
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                    {visibleFacets.map((facet) => {
                      const active = (draft.facet ?? "") === facet.id;
                      return (
                        <button
                          key={facet.id || "__owner_collections"}
                          type="button"
                          role="radio"
                          aria-checked={active}
                          onClick={() => {
                            const nextLabel = labelTouched
                              ? draft.label
                              : defaultLabelForFacet(facet, draft.kind);
                            setDraft({ ...draft, facet: facet.id, label: nextLabel });
                          }}
                          className={cn(
                            "rounded-lg border px-3 py-2 text-left text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                            active
                              ? "border-primary bg-primary-subtle text-primary"
                              : "border-border bg-bg-subtle/50 text-fg hover:bg-surface-hover",
                          )}
                        >
                          {facetOptionLabel(facet)}
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            {supportsOrder ? (
              <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border-subtle pt-3">
                <div>
                  <p className="text-sm font-medium text-fg">
                    {draft.kind === "people" ? t("People order") : t("Bucket order")}
                  </p>
                  <p className="text-xs text-fg-muted">
                    {draft.kind === "people"
                      ? t("How people are ordered in the directory.")
                      : t("How buckets are ordered on the tab.")}
                  </p>
                </div>
                <Segmented
                  ariaLabel={draft.kind === "people" ? t("People order") : t("Bucket order")}
                  size="sm"
                  value={draft.order ?? (draft.kind === "people" ? "name" : "count")}
                  onChange={(order) => setDraft({ ...draft, order })}
                  options={
                    draft.kind === "people"
                      ? [
                          { value: "name", label: t("Name A–Z") },
                          { value: "count", label: t("Most titles") },
                        ]
                      : [
                          { value: "count", label: t("Most items first") },
                          { value: "name", label: t("Name A–Z") },
                        ]
                  }
                />
              </div>
            ) : null}

            {/* Live facet preview */}
            <div
              className="rounded-lg border border-border-subtle bg-bg-subtle/60 p-3"
              role="status"
              aria-label={t("Live preview")}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="flex items-center gap-1.5 text-xs font-medium text-fg">
                  <CheckCircle2 size={14} className="text-success" aria-hidden />
                  {livePreview.loading && !livePreview.data
                    ? t("Loading preview…")
                    : draft.kind === "people"
                      ? tn(
                          previewBuckets.length,
                          "{count} person in {name}",
                          "{count} people in {name}",
                          { name: library.name },
                        )
                      : tn(
                          previewBuckets.length,
                          "{count} collection in {name}",
                          "{count} collections in {name}",
                          { name: library.name },
                        )}
                </span>
              </div>
              {previewBuckets.length > 0 ? (
                <div className="mt-2 flex flex-wrap gap-1.5">
                  {previewBuckets.slice(0, 8).map((b) => (
                    <Badge key={b.id} variant="secondary" className="font-normal">
                      {b.name}
                      <span className="ml-1 tabular-nums text-fg-muted">
                        · {formatNumber(b.count)}
                      </span>
                    </Badge>
                  ))}
                </div>
              ) : null}
            </div>
          </section>
        ) : null}

        {/* Rules editor + live title preview for view & library */}
        {supportsRules ? (
          <section className="space-y-3 rounded-xl border border-border bg-surface p-4">
            <div className="flex flex-wrap items-start justify-between gap-2">
              <div>
                <p className="text-sm font-medium text-fg">
                  {draft.kind === "view" ? t("Matching titles") : t("Default filter")}
                </p>
                <p className="text-xs text-fg-muted">
                  {draft.kind === "view"
                    ? t("Choose which titles from {name} appear in this saved view.", {
                        name: library.name,
                      })
                    : t(
                        "Optional. Leave empty to show every item in {name}, or add rules to narrow this tab.",
                        { name: library.name },
                      )}
                </p>
              </div>
              {rules.length > 1 ? (
                <Segmented
                  ariaLabel={t("Rule matching")}
                  size="sm"
                  value={matchMode}
                  onChange={(m) => setRules(rules, m)}
                  options={[
                    { value: "all", label: t("Match all") },
                    { value: "any", label: t("Match any") },
                  ]}
                />
              ) : null}
            </div>

            {rules.length === 0 ? (
              <p className="rounded-lg border border-dashed border-border px-3 py-3 text-xs text-fg-muted">
                {draft.kind === "view"
                  ? t("Add at least one rule to define this saved view.")
                  : t("No filter rules — every item in {name} is shown.", { name: library.name })}
              </p>
            ) : (
              <div className="space-y-2">
                {rules.map((rule, i) =>
                  renderCondition(
                    rule,
                    i,
                    (next) => setRules(rules.map((r, idx) => (idx === i ? next : r))),
                    () => {
                      setFreshRuleIndex(null);
                      setRules(rules.filter((_, idx) => idx !== i));
                    },
                  ),
                )}
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => {
                  const seed: Rule = isPlugin
                    ? newBrowseCondition("year", library.kind)
                    : { field: "genre", op: "in", value: [] };
                  setFreshRuleIndex(rules.length);
                  setRules([...rules, seed]);
                }}
              >
                <Plus size={14} />
                {t("Add rule")}
              </Button>
              <div className="w-48">
                <Select
                  aria-label={t("More conditions")}
                  value=""
                  onChange={(e) => {
                    if (!e.target.value) return;
                    setFreshRuleIndex(null);
                    setRules([
                      ...rules,
                      newBrowseCondition(e.target.value, isPlugin ? library.kind : undefined),
                    ]);
                  }}
                >
                  <option value="">{t("More conditions…")}</option>
                  {browseFields(isPlugin ? library.kind : undefined).map(([key, label]) => (
                    <option key={key} value={key}>
                      {label}
                    </option>
                  ))}
                </Select>
              </div>
            </div>

            {/* Live matching count & sample posters */}
            <div
              className="space-y-2 rounded-lg border border-border-subtle bg-bg-subtle/60 p-3"
              role="status"
              aria-label={t("Matching titles preview")}
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="flex items-center gap-1.5 text-xs font-medium text-fg">
                  <CheckCircle2 size={14} className="text-success" aria-hidden />
                  {livePreview.loading && !itemsPreviewData
                    ? t("Counting matching titles…")
                    : completeRules.length > 0
                      ? t("{matched} of {total} titles match", {
                          matched: formatNumber(previewCount),
                          total: formatNumber(previewTotal),
                        })
                      : tn(
                          previewCount,
                          "{count} item in this library",
                          "{count} items in this library",
                        )}
                </span>
                {!allRulesComplete ? (
                  <span className="text-[11px] text-fg-muted">
                    {t("Finish incomplete rules to include them in the count.")}
                  </span>
                ) : null}
              </div>
              {previewItems.length > 0 ? (
                <div className="flex gap-2 overflow-x-auto pb-1">
                  {previewItems.slice(0, 10).map((item, idx) => (
                    <Artwork
                      key={String(item.key ?? idx)}
                      src={previewPosterSrc(item)}
                      alt={item.title}
                      className="aspect-[2/3] w-12 shrink-0 rounded-md"
                    />
                  ))}
                </div>
              ) : null}
            </div>
          </section>
        ) : null}

        {/* View tab layout */}
        {draft.kind === "view" ? (
          <section className="space-y-3 rounded-xl border border-border bg-surface p-4">
            <div className="space-y-1.5">
              <span className="block text-sm font-medium text-fg">{t("Layout")}</span>
              <p className="text-xs text-fg-muted">
                {t("Choose how matching titles are arranged in Vela.")}
              </p>
              <Segmented
                ariaLabel={t("Layout")}
                size="sm"
                value={draft.layout ?? "grid"}
                onChange={(layout) => setDraft({ ...draft, layout })}
                options={[
                  { value: "grid", label: t("Grid") },
                  { value: "list", label: t("List") },
                  { value: "timeline", label: t("Timeline") },
                ]}
              />
            </div>
          </section>
        ) : null}

        {/* Live tab card shape */}
        {draft.kind === "live" ? (
          <section className="space-y-3 rounded-xl border border-border bg-surface p-4">
            <div className="space-y-1.5">
              <span className="block text-sm font-medium text-fg">{t("Card shape")}</span>
              <p className="text-xs text-fg-muted">
                {t("Choose how live stream thumbnails appear in this tab.")}
              </p>
              <Segmented
                ariaLabel={t("Card shape")}
                size="sm"
                value={draft.card ?? "landscape"}
                onChange={(card) => setDraft({ ...draft, card })}
                options={[
                  { value: "landscape", label: t("Landscape") },
                  { value: "poster", label: t("Poster") },
                  { value: "square", label: t("Square") },
                ]}
              />
            </div>
          </section>
        ) : null}

        {/* Sort & Direction */}
        {supportsSort ? (
          <section className="space-y-3 rounded-xl border border-border bg-surface p-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <label className="block text-sm font-medium text-fg" htmlFor="tab-sort-select">
                  {draft.kind === "collections"
                    ? t("Sort items inside each bucket")
                    : t("Default sort")}
                </label>
                <Select
                  id="tab-sort-select"
                  aria-label={
                    draft.kind === "collections"
                      ? t("Sort items inside each bucket")
                      : t("Default sort")
                  }
                  value={currentSort}
                  onChange={(e) => {
                    const nextSort = e.target.value;
                    setDraft({
                      ...draft,
                      sort: nextSort,
                      direction: defaultDirectionForSort(nextSort),
                    });
                  }}
                >
                  {availableSorts.map((s) => (
                    <option key={s} value={s}>
                      {sortFieldLabel(s)}
                    </option>
                  ))}
                </Select>
              </div>
              <div className="space-y-1.5">
                <span className="block text-sm font-medium text-fg">{t("Direction")}</span>
                <Segmented
                  ariaLabel={t("Sort direction")}
                  size="sm"
                  value={currentDirection}
                  onChange={(direction) => setDraft({ ...draft, direction })}
                  options={dirOpts}
                />
              </div>
            </div>
          </section>
        ) : null}

        {/* Search & Visibility */}
        <section className="divide-y divide-border-subtle rounded-xl border border-border bg-surface">
          {supportsSearch ? (
            <div className="space-y-3 p-4">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-fg">{t("Search box")}</p>
                  <p className="text-xs text-fg-muted">
                    {t("Let viewers filter this tab by typing.")}
                  </p>
                </div>
                <Switch
                  aria-label={t("Search box")}
                  checked={searchEnabled}
                  onChange={(e) => setDraft({ ...draft, search: e.target.checked })}
                />
              </div>
              {searchEnabled ? (
                <div className="space-y-1">
                  <label
                    className="block text-xs font-medium text-fg-muted"
                    htmlFor="tab-search-placeholder"
                  >
                    {t("Search placeholder")}
                  </label>
                  <Input
                    id="tab-search-placeholder"
                    aria-label={t("Search placeholder")}
                    placeholder={defaultSearchPlaceholder}
                    value={draft.searchPlaceholder ?? ""}
                    onChange={(e) => setDraft({ ...draft, searchPlaceholder: e.target.value })}
                  />
                </div>
              ) : null}
            </div>
          ) : null}

          <div className="flex items-center justify-between gap-3 p-4">
            <div>
              <p className="text-sm font-medium text-fg">{t("Visible in Vela")}</p>
              <p className="text-xs text-fg-muted">
                {!canHide
                  ? t("At least one tab must remain visible in Vela.")
                  : t("Turn off to keep this tab configured here without showing it to viewers.")}
              </p>
            </div>
            <Switch
              aria-label={t("Visible in Vela")}
              checked={!draft.hidden}
              disabled={!canHide && !draft.hidden}
              onChange={(e) => setDraft({ ...draft, hidden: !e.target.checked })}
            />
          </div>
        </section>
      </div>
    </Drawer>
  );
}
