import {
  ExtendedCondition,
  conditionValue,
  browseFields,
  conditionTitle,
  formatPluginCondition,
  newBrowseCondition,
} from "./RuleCondition";
import { PluginBrowse } from "../PluginBrowse";
import { librariesApi, type Library as PluginLibrary, type MediaKind } from "../api";
import { Link } from "react-router-dom";
import { CatalogBrowser } from "./Catalog";
import { Button, Input, Select } from "@braedonsaunders/appkit-ui";
import {
  CheckCircle2,
  Library,
  Eye,
  EyeOff,
  ArrowDown,
  ChevronDown,
  ChevronUp,
  GripVertical,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Drawer } from "../../../components/Drawer";
import { Artwork } from "../../../components/Artwork";
import { Segmented } from "../../../components/fields";
import { t, tn } from "../../../i18n";
import { apiFetch } from "../../../lib/api";
import { formatNumber } from "../../../lib/format";
import type { Rule, RuleField } from "../../../lib/contract";
import { useAsync, useDebouncedValue } from "../../../lib/hooks";
import { describeRule, fieldMeta, isRuleComplete, type FieldMeta } from "../../../lib/rules";
import { titlePosterURL } from "../../../lib/titles";
import { RuleRow } from "../../collections/RuleRow";

export type VisibilityRule = {
  name: string;
  action?: string;
  mediaType: string;
  match: string;
  rules: Rule[];
  /** "discovery" hides from browsing only; "library" hides from this library only; empty or "everywhere" hides everywhere. */
  scope?: string;
};

type Preview = {
  total: number;
  hidden: number;
  visible?: number;
  movies: number;
  shows: number;
  items?:
    { key: string; title: string; year?: number; poster?: string; posterPath?: string }[] | null;
  shown?:
    { key: string; title: string; year?: number; poster?: string; posterPath?: string }[] | null;
  rules?: { movies: number; shows: number }[] | null;
};

type OpenRuleState = {
  index: number;
  isNew: boolean;
  initial: VisibilityRule;
  before: VisibilityRule[];
  beforeFallback: string;
};

function pruneIncompleteCondition(rule: Rule): Rule | null {
  if (rule.field === "group") {
    if (!Array.isArray(rule.value)) return null;
    const children = (rule.value as Rule[])
      .map(pruneIncompleteCondition)
      .filter((c): c is Rule => c !== null);
    return children.length > 0 ? { ...rule, value: children } : null;
  }
  return isRuleComplete(rule) ? rule : null;
}

function previewableVisibilityRules(rules: VisibilityRule[]): {
  rules: VisibilityRule[];
  indexMap: Map<number, number>;
} {
  const out: VisibilityRule[] = [];
  const indexMap = new Map<number, number>();
  rules.forEach((g, i) => {
    const kept = g.rules.map(pruneIncompleteCondition).filter((c): c is Rule => c !== null);
    if (kept.length > 0) {
      indexMap.set(i, out.length);
      out.push({ ...g, rules: kept });
    }
  });
  return { rules: out, indexMap };
}

const FIELDS: RuleField[] = [
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

function metaFor(field: RuleField): FieldMeta | undefined {
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

const blank = (): Rule => ({ field: "genre", op: "in", value: [] });

function actionOf(rule: VisibilityRule) {
  return rule.action === "show" ? "show" : "hide";
}

export function isShowOnlyRules(rules: VisibilityRule[]): boolean {
  return rules.length > 0 && rules.every((rule) => actionOf(rule) === "show");
}

function hideScopeOf(rule: VisibilityRule): "everywhere" | "discovery" | "library" {
  if (actionOf(rule) !== "hide") return "everywhere";
  if (rule.scope === "discovery") return "discovery";
  if (rule.scope === "library") return "library";
  return "everywhere";
}

function conditionLine(rule: Rule, kind?: MediaKind): string {
  const formattedPlugin = formatPluginCondition(rule, kind);
  if (formattedPlugin) return formattedPlugin;
  if (rule.field === "group" && Array.isArray(rule.value) && rule.value.length === 0)
    return conditionTitle(rule, kind);
  if (rule.field === "group")
    return `${conditionTitle(rule, kind)} (${(rule.value as Rule[]).map((r) => conditionLine(r, kind)).join(" · ")})`;
  if (rule.field.startsWith("browse:") || rule.field === "viewer")
    return kind
      ? rule.op === "isNot"
        ? t("{field} is not {value}", {
            field: conditionTitle(rule, kind),
            value: conditionValue(rule.value),
          })
        : t("{field} is {value}", {
            field: conditionTitle(rule, kind),
            value: conditionValue(rule.value),
          })
      : `${conditionTitle(rule)}: ${conditionValue(rule.value, rule.field)}`;
  if (rule.op === "in") return describeRule({ ...rule, op: "is" });
  return describeRule(rule);
}

/** "Hide · Genre is Horror, Thriller". The stored name stays in the drawer. */
export function ruleLabel(
  rule: VisibilityRule,
  pinnedMediaType?: "movie" | "show",
  kind?: MediaKind,
) {
  const action = actionOf(rule) === "show" ? t("Show") : t("Hide");
  const bits = rule.rules.filter(isRuleComplete).map((r) => conditionLine(r, kind));
  const body =
    rule.match === "any" && bits.length > 1
      ? bits.reduce((left, right) => t("{left} or {right}", { left, right }))
      : bits.join(" · ");
  const media =
    pinnedMediaType && (rule.mediaType === pinnedMediaType || rule.mediaType === "mixed")
      ? ""
      : rule.mediaType === "movie"
        ? t("Movies")
        : rule.mediaType === "show"
          ? t("TV")
          : "";
  const hideScope = hideScopeOf(rule);
  const scope =
    hideScope === "discovery"
      ? t("Browsing only")
      : hideScope === "library"
        ? t("Only this library")
        : "";
  return [action, body, media, scope].filter(Boolean).join(" · ");
}

export function RulesPanel({
  rules,
  fallback,
  onRules,
  onFallback,
  region,
  libraryId,
  mediaType,
  pluginLibrary,
  sources,
}: {
  sources?: string[];
  pluginLibrary?: PluginLibrary;
  rules: VisibilityRule[];
  fallback: string;
  onRules: (rules: VisibilityRule[], nextFallback?: string) => void;
  onFallback: (value: string) => void;
  region: string;
  libraryId?: number;
  mediaType?: "movie" | "show";
}) {
  const canonicalId = mediaType === "show" ? 2 : 1;
  const inherited = useAsync(
    (signal) =>
      !pluginLibrary && (Boolean(sources) || Boolean(libraryId && libraryId > 2))
        ? librariesApi.get(canonicalId, signal)
        : Promise.resolve(null),
    [canonicalId, libraryId, pluginLibrary, sources],
  );
  const inheritedPolicy = inherited.data;
  const aliases: Record<string, string[]> = {
    genre: ["genre", "genres", "tags", "tag"],
    studio: ["studio", "studios"],
    country: ["country", "countries"],
    keyword: ["keyword", "keywords"],
    language: ["language"],
    runtime: ["runtime", "duration", "length"],
    votes: ["votes"],
    popularity: ["popularity"],
  };
  const pluginFields: Record<string, string> = {};
  for (const [field, keys] of Object.entries(aliases)) {
    const key = keys.find((k) =>
      pluginLibrary?.kind?.fields?.some(
        (f) =>
          f.key === k &&
          (f.indexed || f.type === "people") &&
          (["runtime", "votes", "popularity"].includes(field)
            ? ["int", "float", "duration"].includes(f.type)
            : ["string", "strings", "enum", "people"].includes(f.type)),
      ),
    );
    if (key) pluginFields[field] = key;
  }
  const allowedFields = pluginLibrary
    ? FIELDS.filter((f) => f === "year" || f === "rating" || !!pluginFields[f])
    : FIELDS;
  const newCondition = (): Rule =>
    pluginLibrary ? newBrowseCondition("year", pluginLibrary.kind) : blank();
  const [browse, setBrowse] = useState<"hidden" | "shown" | null>(null);
  const [browseParams, setBrowseParams] = useState(new URLSearchParams());
  // i18n: memo holds no copy
  const { rules: previewRules, indexMap: previewRuleIndex } = useMemo(
    () => previewableVisibilityRules(rules),
    [rules],
  );
  const [openState, setOpenState] = useState<OpenRuleState | null>(null);
  const open = openState?.index ?? null;
  const [drag, setDrag] = useState<number | null>(null);
  const [fresh, setFresh] = useState<number | null>(null);
  const showOnly = isShowOnlyRules(rules);
  const previewFallback =
    previewRules.length === 0 && rules.length > 0 && openState
      ? openState.beforeFallback
      : fallback;
  const ruleFilter = useMemo(
    () => ({
      ruleVisibility: browse ?? "shown",
      rules: JSON.stringify(previewRules),
      ruleFallback: previewFallback,
    }),
    [browse, previewRules, previewFallback],
  );
  const complete = rules.every((g) => g.rules.length > 0 && g.rules.every(isRuleComplete));
  const spec = useMemo(
    () => ({ rules: previewRules, fallback: previewFallback, libraryId, sources, mediaType }),
    [previewRules, previewFallback, libraryId, sources, mediaType],
  );
  const draft = useDebouncedValue(spec, 400);
  const preview = useAsync(
    async (signal) =>
      apiFetch<Preview>(
        sources && !libraryId ? "/libraries/preview" : "/catalog/visibility/preview",
        {
          method: "POST",
          body: JSON.stringify({
            ...(draft.libraryId ? { libraryId: draft.libraryId } : {}),
            ...(sources && !libraryId
              ? { rules: draft.rules, sources: draft.sources, mediaType: draft.mediaType }
              : { exclusions: draft.rules }),
            visibilityDefault: draft.fallback === "hide" ? "hide" : "show",
          }),
          signal,
        },
      ),
    [draft],
  );
  const commitRules = (next: VisibilityRule[]) => {
    if (!showOnly && isShowOnlyRules(next) && fallback !== "hide") {
      onRules(next, "hide");
      return;
    }
    onRules(next);
  };
  const openExistingRule = (i: number) => {
    const target = rules[i];
    if (!target) return;
    setFresh(null);
    setOpenState({
      index: i,
      isNew: false,
      initial: target,
      before: rules,
      beforeFallback: fallback,
    });
  };
  const move = (i: number, dir: number) => {
    const j = i + dir;
    if (j < 0 || j >= rules.length) return;
    const next = rules.slice();
    const [row] = next.splice(i, 1);
    next.splice(j, 0, row!);
    onRules(next);
    setOpenState((cur) =>
      cur ? { ...cur, index: cur.index === i ? j : cur.index === j ? i : cur.index } : null,
    );
  };
  const update = (i: number, patch: Partial<VisibilityRule>) =>
    commitRules(rules.map((g, n) => (n === i ? { ...g, ...patch } : g)));
  const editing = open != null ? rules[open] : undefined;
  const closeRuleDrawer = () => {
    if (
      openState?.isNew &&
      editing &&
      JSON.stringify(editing) === JSON.stringify(openState.initial)
    ) {
      onRules(openState.before, openState.beforeFallback);
    }
    setFresh(null);
    setOpenState(null);
  };
  const cancelRuleDrawer = () => {
    if (openState) onRules(openState.before, openState.beforeFallback);
    setFresh(null);
    setOpenState(null);
  };
  const hiddenItems = preview.data?.items ?? [];
  const shownItems = preview.data?.shown ?? [];
  const visible =
    preview.data?.visible ?? Math.max(0, (preview.data?.total ?? 0) - (preview.data?.hidden ?? 0));

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
        kind={pluginLibrary?.kind}
        libraryId={pluginLibrary?.id}
      />
    ) : (
      <RuleRow
        key={j}
        rule={rule}
        index={j}
        kind="include"
        mediaType={mediaType ?? editing?.mediaType ?? "mixed"}
        region={region}
        allowedFields={allowedFields}
        facetLibraryId={pluginLibrary?.id}
        facetFields={pluginFields}
        metaFor={metaFor}
        inline
        openValue={fresh === j}
        onChange={change}
        onRemove={remove}
      />
    );
  const inheritedRules = useMemo(
    () => (inheritedPolicy?.rules ?? []).filter((r) => hideScopeOf(r) !== "library"),
    [inheritedPolicy],
  );
  const allowLibraryScope =
    libraryId === 1 || libraryId === 2 || (editing ? hideScopeOf(editing) === "library" : false);
  return (
    <div className="@container">
      <div className="grid grid-cols-1 gap-6 @3xl:grid-cols-[minmax(0,1fr)_300px]">
        <div className="space-y-4">
          {inheritedPolicy &&
          (inheritedRules.length > 0 || inheritedPolicy.visibilityDefault === "hide") ? (
            <section className="space-y-2 rounded-xl border border-border bg-surface p-4">
              <div className="flex items-center justify-between">
                <p className="font-medium">
                  {inheritedPolicy.name} · {t("Rules")}
                </p>
                <Link to={`/libraries/${canonicalId}/rules`}>{t("Edit")}</Link>
              </div>
              <ul className="space-y-1 text-sm">
                {inheritedRules.map((rule, i) => (
                  <li key={i}>{ruleLabel(rule, mediaType, pluginLibrary?.kind)}</li>
                ))}
              </ul>
              <p className="text-sm text-fg-muted">
                {t("Titles not matched above")} ·{" "}
                {inheritedPolicy.visibilityDefault === "hide" ? t("Hide") : t("Show")}
              </p>
            </section>
          ) : null}
          <div className="flex items-center gap-3 rounded-xl border border-border bg-surface p-5 text-lg font-semibold">
            <Library className="shrink-0 text-primary" size={24} aria-hidden />
            {preview.loading && !preview.data
              ? t("Loading titles…")
              : libraryId
                ? tn(
                    preview.data?.total ?? 0,
                    "{count} title in this library",
                    "{count} titles in this library",
                  )
                : tn(
                    preview.data?.total ?? 0,
                    "{count} title in your catalog",
                    "{count} titles in your catalog",
                  )}
          </div>
          <div className="ml-6 border-l-2 border-border pl-5 space-y-4">
            <ArrowDown className="text-fg-muted" size={20} aria-hidden />
            {!pluginLibrary ? (
              <p className="text-sm text-fg-muted">
                {t("First match wins. Ratings match by age level.")}
              </p>
            ) : null}
            {rules.length === 0 ? (
              <p className="rounded-lg border border-dashed border-border px-4 py-6 text-sm text-fg-muted">
                {fallback === "hide"
                  ? t("No rules yet. Everything is hidden.")
                  : t("No rules yet. Everything is shown.")}
              </p>
            ) : null}
            <ol className="space-y-2">
              {rules.map((rule, i) => {
                const previewIdx = previewRuleIndex.get(i);
                const counts =
                  previewIdx !== undefined ? preview.data?.rules?.[previewIdx] : undefined;
                return (
                  <li
                    key={i}
                    draggable
                    onDragStart={() => setDrag(i)}
                    onDragOver={(e) => e.preventDefault()}
                    onDrop={() => {
                      if (drag == null || drag === i) return;
                      const next = rules.slice();
                      const [row] = next.splice(drag, 1);
                      next.splice(i, 0, row!);
                      onRules(next);
                      setDrag(null);
                    }}
                    className="grid grid-cols-[minmax(0,1fr)_repeat(4,auto)] items-center gap-1 rounded-lg border border-border bg-surface px-2 py-2 sm:flex sm:gap-2"
                  >
                    <button
                      type="button"
                      className="hidden cursor-grab px-1 text-fg-muted sm:block"
                      aria-label={t("Drag rule {n}", { n: i + 1 })}
                    >
                      <GripVertical size={16} />
                    </button>
                    <button
                      type="button"
                      className="col-span-5 min-w-0 flex-1 text-left"
                      onClick={() => openExistingRule(i)}
                    >
                      <span className="block text-sm font-medium sm:truncate">
                        {ruleLabel(rule, mediaType, pluginLibrary?.kind)}
                      </span>
                    </button>
                    {counts ? (
                      <span className="shrink-0 text-xs text-fg-muted">
                        {pluginLibrary ? (
                          tn(counts.movies + counts.shows, "{count} item", "{count} items")
                        ) : mediaType === "movie" ? (
                          tn(counts.movies, "{count} movie", "{count} movies")
                        ) : mediaType === "show" ? (
                          tn(counts.shows, "{count} show", "{count} shows")
                        ) : (
                          <>
                            {tn(counts.movies, "{count} movie", "{count} movies")}
                            {" · "}
                            {tn(counts.shows, "{count} show", "{count} shows")}
                          </>
                        )}
                      </span>
                    ) : null}
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("Move rule {n} up", { n: i + 1 })}
                      disabled={i === 0}
                      onClick={() => move(i, -1)}
                    >
                      <ChevronUp size={16} />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("Move rule {n} down", { n: i + 1 })}
                      disabled={i === rules.length - 1}
                      onClick={() => move(i, 1)}
                    >
                      <ChevronDown size={16} />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("Edit rule {n}", { n: i + 1 })}
                      onClick={() => openExistingRule(i)}
                    >
                      <Pencil size={16} />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("Delete rule {n}", { n: i + 1 })}
                      onClick={() => {
                        commitRules(rules.filter((_, n) => n !== i));
                        setFresh(null);
                        setOpenState(null);
                      }}
                    >
                      <Trash2 size={16} />
                    </Button>
                  </li>
                );
              })}
            </ol>
            <Button
              variant="outline"
              size="sm"
              disabled={rules.length >= 32}
              onClick={() => {
                const initial: VisibilityRule = {
                  name: "",
                  action: "hide",
                  mediaType: mediaType ?? "mixed",
                  match: "all",
                  rules: [newCondition()],
                };
                const before = rules;
                const beforeFallback = fallback;
                commitRules([...rules, initial]);
                setFresh(0);
                setOpenState({ index: rules.length, isNew: true, initial, before, beforeFallback });
              }}
            >
              <Plus size={14} />
              {t("Add rule")}
            </Button>
            <div className="space-y-1.5 rounded-xl border border-border bg-surface-alt px-3.5 py-3">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <span className="text-sm font-medium">{t("Titles not matched above")}</span>
                <Segmented
                  ariaLabel={t("Titles not matched above")}
                  size="sm"
                  value={fallback === "hide" ? "hide" : "show"}
                  onChange={onFallback}
                  options={[
                    { value: "show", label: t("Show") },
                    { value: "hide", label: t("Hide") },
                  ]}
                />
              </div>
              {showOnly ? (
                <p className="text-xs text-fg-muted">
                  {fallback === "hide"
                    ? t("Only Show rules are set above, so unmatched titles default to Hide.")
                    : t(
                        "Only Show rules are set above. Switch to Hide so only matching titles appear.",
                      )}
                </p>
              ) : null}
            </div>
            <ArrowDown className="text-fg-muted" size={20} aria-hidden />
          </div>
          <div
            className="flex items-center gap-3 rounded-xl border border-border bg-surface p-5 font-semibold"
            role="status"
          >
            <Eye className="shrink-0 text-success" size={24} aria-hidden />
            {t("{shown} shown · {hidden} hidden", {
              shown: formatNumber(visible),
              hidden: formatNumber(preview.data?.hidden ?? 0),
            })}
          </div>
        </div>
        <aside
          className="min-w-0 space-y-3 lg:sticky lg:top-0 lg:self-start"
          aria-label={t("Preview")}
        >
          <section className="min-h-fit rounded-xl border border-border bg-surface p-5 pb-6">
            <CheckCircle2 className="mb-4 text-success" size={32} aria-hidden />
            {preview.error ? (
              <p className="text-sm text-fg-muted">{t("Preview isn't available.")}</p>
            ) : (
              <>
                <p className="text-3xl font-semibold">
                  {preview.loading && !preview.data ? "…" : formatNumber(visible)}
                </p>
                <p className="text-sm text-fg-muted">
                  {(preview.data?.hidden ?? 0) > 0
                    ? t("visible of {total}", { total: formatNumber(preview.data?.total ?? 0) })
                    : t("titles visible")}
                </p>
                {(preview.data?.hidden ?? 0) > 0 ? (
                  <p className="mt-2 text-xs text-fg-muted">
                    {pluginLibrary
                      ? t("Hidden: {count}", { count: formatNumber(preview.data?.hidden ?? 0) })
                      : mediaType === "movie"
                        ? t("Hidden: {count}", {
                            count: tn(preview.data?.movies ?? 0, "{count} movie", "{count} movies"),
                          })
                        : mediaType === "show"
                          ? t("Hidden: {count}", {
                              count: tn(preview.data?.shows ?? 0, "{count} show", "{count} shows"),
                            })
                          : t("Hidden: {movies} · {shows}", {
                              movies: tn(
                                preview.data?.movies ?? 0,
                                "{count} movie",
                                "{count} movies",
                              ),
                              shows: tn(preview.data?.shows ?? 0, "{count} show", "{count} shows"),
                            })}
                  </p>
                ) : null}
                {!complete ? (
                  <p className="mt-2 text-xs text-fg-muted">
                    {t("Unfinished rules aren't included in the preview yet.")}
                  </p>
                ) : null}
              </>
            )}
          </section>
          <PosterRow
            title={t("Hidden")}
            count={preview.data?.hidden ?? 0}
            empty={t("Nothing hidden")}
            items={hiddenItems}
            onBrowse={() => {
              setBrowseParams(new URLSearchParams());
              setBrowse("hidden");
            }}
          />
          <PosterRow
            title={t("Shown")}
            count={visible}
            empty={t("Nothing shown")}
            items={shownItems}
            onBrowse={() => {
              setBrowseParams(new URLSearchParams());
              setBrowse("shown");
            }}
          />
        </aside>
        <Drawer
          open={browse !== null}
          onClose={() => setBrowse(null)}
          title={browse === "hidden" ? t("Hidden by rules") : t("Shown by rules")}
          size="2xl"
          // The browser is its own height-bounded scroller (.catalog-scroll);
          // a scrolling drawer body would let it grow to all its pages and
          // make its infinite scroll load the whole library.
          bodyClassName={
            browse && !pluginLibrary ? "flex min-h-0 flex-col overflow-hidden" : undefined
          }
        >
          {browse && pluginLibrary ? (
            <PluginBrowse
              library={pluginLibrary}
              ruleFilter={ruleFilter}
              controlledParams={browseParams}
              onParams={setBrowseParams}
            />
          ) : browse ? (
            <CatalogBrowser
              params={browseParams}
              setParams={setBrowseParams}
              refreshHealth={async () => {}}
              ruleFilter={ruleFilter}
              libraryScope={libraryId ? { id: libraryId, type: mediaType ?? "" } : undefined}
            />
          ) : null}
        </Drawer>
        <Drawer
          open={editing != null}
          onClose={closeRuleDrawer}
          title={
            editing
              ? editing.name.trim() || ruleLabel(editing, mediaType, pluginLibrary?.kind)
              : t("Rule")
          }
          size="lg"
          footer={
            <div className="flex w-full justify-end gap-2">
              <Button type="button" variant="outline" size="sm" onClick={cancelRuleDrawer}>
                {t("Cancel")}
              </Button>
              <Button type="button" size="sm" onClick={closeRuleDrawer}>
                {t("Done")}
              </Button>
            </div>
          }
        >
          {editing && open != null ? (
            <div className="space-y-4">
              <Input
                aria-label={t("Rule name")}
                placeholder={t("Name (optional)")}
                value={editing.name}
                onChange={(e) => update(open, { name: e.target.value })}
              />
              <div className="flex flex-wrap gap-3">
                <Segmented
                  ariaLabel={t("Rule action")}
                  value={actionOf(editing)}
                  onChange={(v) =>
                    update(open, v === "show" ? { action: v, scope: undefined } : { action: v })
                  }
                  options={[
                    { value: "show", label: t("Show") },
                    { value: "hide", label: t("Hide") },
                  ]}
                />
                {!mediaType && !pluginLibrary ? (
                  <Segmented
                    ariaLabel={t("Rule media")}
                    value={editing.mediaType}
                    onChange={(v) => update(open, { mediaType: v })}
                    options={[
                      { value: "mixed", label: t("Movies + TV") },
                      { value: "movie", label: t("Movies") },
                      { value: "show", label: t("TV") },
                    ]}
                  />
                ) : null}
                <Segmented
                  ariaLabel={t("Rule match")}
                  value={editing.match}
                  onChange={(v) => update(open, { match: v })}
                  options={[
                    { value: "all", label: t("All") },
                    { value: "any", label: t("Any") },
                  ]}
                />
              </div>
              {actionOf(editing) === "hide" ? (
                <div className="space-y-1">
                  <Select
                    aria-label={t("Where to hide")}
                    className="max-w-xs"
                    value={hideScopeOf(editing)}
                    onChange={(e) =>
                      update(open, {
                        scope:
                          e.target.value === "discovery" || e.target.value === "library"
                            ? e.target.value
                            : undefined,
                      })
                    }
                  >
                    <option value="everywhere">{t("Hide everywhere")}</option>
                    <option value="discovery">{t("Hide from browsing only")}</option>
                    {allowLibraryScope ? (
                      <option value="library">{t("Only this library")}</option>
                    ) : null}
                  </Select>
                  <p className="text-xs text-fg-muted">
                    {hideScopeOf(editing) === "discovery"
                      ? t(
                          "Browsing only keeps these titles out of Home, rows, recommendations, New Releases and Trending. Anyone can still search for them and play them.",
                        )
                      : hideScopeOf(editing) === "library"
                        ? t(
                            "Hides matching titles from this library only. They remain visible in custom libraries, collections, search and playback.",
                          )
                        : t(
                            "Hides matching titles everywhere, including Home, library browsing, recommendations and search.",
                          )}
                  </p>
                </div>
              ) : null}
              {editing.rules.some(
                (rule) => rule.field === "country" && (rule.op === "in" || rule.op === "is"),
              ) && (
                <p className="text-xs text-fg-muted">
                  {t(
                    "Hide matches only when every production country is listed. Show matches when any country is listed.",
                  )}
                </p>
              )}
              {editing.rules.map((rule, j) =>
                renderCondition(
                  rule,
                  j,
                  (v) => update(open, { rules: editing.rules.map((r, k) => (k === j ? v : r)) }),
                  () => update(open, { rules: editing.rules.filter((_, k) => k !== j) }),
                ),
              )}
              <Select
                aria-label={t("Add condition")}
                value=""
                onChange={(e) => {
                  if (e.target.value)
                    update(open, {
                      rules: [
                        ...editing.rules,
                        newBrowseCondition(e.target.value, pluginLibrary?.kind),
                      ],
                    });
                }}
              >
                <option value="">{t("More conditions")}</option>
                {browseFields(pluginLibrary?.kind).map(([key, label]) => (
                  <option key={key} value={key}>
                    {label}
                  </option>
                ))}
              </Select>
              <Button
                size="sm"
                variant="outline"
                disabled={editing.rules.length >= 20}
                onClick={() => {
                  setFresh(editing.rules.length);
                  update(open, { rules: [...editing.rules, newCondition()] });
                }}
              >
                <Plus size={14} />
                {t("Add condition")}
              </Button>
            </div>
          ) : null}
        </Drawer>
      </div>
    </div>
  );
}

function PosterRow({
  count,
  empty,
  title,
  items,
  onBrowse,
}: {
  title: string;
  count: number;
  empty: string;
  onBrowse: () => void;
  items: { key: string; title: string; poster?: string; posterPath?: string }[];
}) {
  return (
    <section className="min-w-0 rounded-xl border border-border bg-surface p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium">
          {t("{title} · {count}", { title, count: formatNumber(count) })}
        </h3>
        <Button variant="ghost" size="sm" onClick={onBrowse} disabled={count === 0}>
          {t("Browse")}
        </Button>
      </div>
      {count === 0 ? (
        <div className="flex items-center gap-2 rounded-lg border border-dashed border-border p-4 text-sm text-fg-muted">
          <EyeOff size={18} aria-hidden />
          {empty}
        </div>
      ) : null}
      <div className="flex gap-2 overflow-x-auto pb-1">
        {items.slice(0, 24).map((item) => (
          <Artwork
            key={item.key}
            src={item.poster ?? titlePosterURL(item, 185)}
            alt={item.title}
            className="aspect-[2/3] w-14 shrink-0 rounded-md"
          />
        ))}
      </div>
    </section>
  );
}
