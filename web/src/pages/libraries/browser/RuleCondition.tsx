import { Button, Input, Select, cn } from "@braedonsaunders/appkit-ui";
import { Check, Plus, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { catalogValueLabel } from "./catalogValueLabel";
import { t } from "../../../i18n";
import { formatNumber } from "../../../lib/format";
import { useDebouncedValue } from "../../../lib/hooks";
import { librariesApi, type MediaKind } from "../api";
import type { Rule, RuleField } from "../../../lib/contract";
import {
  SHORT_VALUE_LIST_LIMIT,
  buildNumericCondition,
  buildTextCondition,
  parseNumericCondition,
  parseTextCondition,
  pluginFieldDeclaredValues,
  pluginFieldKind,
  pluginFieldLabel,
  pluginFieldMultiValued,
  type ChipCombineMode,
  type NumericCompareOp,
  type ParsedNumericCondition,
  type ParsedTextCondition,
} from "./pluginRuleValue";

export { formatPluginCondition } from "./pluginRuleValue";

const VIEWER_BROWSE_FIELDS = (): [string, string][] => [
  ["__history", t("Watch history")],
  ["__watched", t("Watched")],
  ["__unwatched", t("Unwatched")],
  ["__progress", t("In progress")],
  ["__favorite", t("Favorite")],
  ["__watchlist", t("On watchlist")],
  ["__lastViewedAfter", t("Watched after")],
  ["__lastViewedBefore", t("Watched before")],
];

// The extended rules use the catalogue's existing indexed predicates. These
// are individual rule conditions, never a second saved library filter.
export function browseFields(kind?: MediaKind): [string, string][] {
  if (kind)
    return [
      ["__group", t("Rules")],
      ...VIEWER_BROWSE_FIELDS(),
      ...["year", "rating", "released"].map((key): [string, string] => [
        key,
        pluginFieldLabel(key, kind),
      ]),
      ...(kind.fields ?? [])
        .filter(
          (f) =>
            (f.indexed || f.type === "people") && !["year", "rating", "released"].includes(f.key),
        )
        .map((f): [string, string] => [f.key, f.label]),
    ];
  return [
    ["__group", t("Rules")],
    ...VIEWER_BROWSE_FIELDS(),
    ["__rated", t("Rated")],
    ["type", t("Type")],
    ["releaseGate", t("New-release gate")],
    ["playable", t("Playable now")],
    ["debridCached", t("Cached on debrid")],
    ["qualities", t("Quality")],
    ["anime", t("Anime keyword")],
    ["kids", t("Kids genre")],
    ["tags", t("Tags")],
    ["keywords", t("Keyword")],
    ["yearFrom", t("Minimum") + " · " + t("Year")],
    ["yearTo", t("Maximum") + " · " + t("Year")],
    ["years", t("Year")],
    ["excludeYears", t("Exclude") + " · " + t("Year")],
    ["minVotes", t("Minimum votes")],
    ["maxVotes", t("Maximum") + " · " + t("Votes")],
    ["minPopularity", t("Minimum popularity")],
    ["maxPopularity", t("Maximum") + " · " + t("Popularity")],
    ["dateAddedFrom", t("Minimum") + " · " + t("Release / air date")],
    ["dateAddedTo", t("Maximum") + " · " + t("Release / air date")],
    ["etherAddedFrom", t("Minimum") + " · " + t("Added to Ether")],
    ["etherAddedTo", t("Maximum") + " · " + t("Added to Ether")],
    ["statuses", t("Show status")],
    ["minSeasons", t("Minimum") + " · " + t("Seasons")],
    ["maxSeasons", t("Maximum") + " · " + t("Seasons")],
    ["minEpisodes", t("Minimum") + " · " + t("Episodes")],
    ["maxEpisodes", t("Maximum") + " · " + t("Episodes")],
    ["minRating", t("Minimum") + " · " + t("Rating")],
    ["maxRating", t("Maximum") + " · " + t("Rating")],
    ["excludeRatings", t("Exclude") + " · " + t("Rating")],
    ["maxRuntime", t("Maximum") + " · " + t("Runtime (minutes)")],
    ["minRuntime", t("Minimum") + " · " + t("Runtime (minutes)")],
    ["excludeRuntimes", t("Exclude") + " · " + t("Runtime (minutes)")],
    ["languages", t("Original language")],
    ["countries", t("Country")],
    ["excludeCountries", t("Exclude") + " · " + t("Country")],
    ["contentRatings", t("Certification")],
    ["excludeContentRatings", t("Exclude") + " · " + t("Certification")],
    ["maxContentRating", t("Maximum") + " · " + t("Certification")],
    ["collection", t("Collection / franchise")],
    ["collections", t("Collections")],
    ["excludeCollections", t("Exclude") + " · " + t("Collections")],
    ["person", t("Person")],
    ["credits", t("People & stories")],
    ["studio", t("Studio / network")],
    ["studioFilters", t("Studio / network")],
    ["firstCharacter", t("Title") + " · " + t("Letter")],
    ["nameStartsWith", t("Title") + " · " + t("Prefix")],
    ["nameStartsWithOrGreater", t("Title") + " · " + t("Minimum")],
    ["nameLessThan", t("Title") + " · " + t("Maximum")],
    ["query", t("Title")],
    ["titleFilters", t("Title") + " · " + t("Rules")],
    ["releasedFilters", t("Release / air date") + " · " + t("Rules")],
  ];
}

export function conditionTitle(rule: Rule, kind?: MediaKind): string {
  const parsedText = parseTextCondition(rule, kind);
  if (parsedText) return pluginFieldLabel(parsedText.fieldKey, kind);
  if (rule.field === "group")
    return rule.op === "in" ? t("Any") : rule.op === "isNot" ? t("Not") : t("All");
  if (rule.field === "viewer") return t("Viewer activity");
  return browseFields(kind).find(([key]) => `browse:${key}` === rule.field)?.[1] ?? t("Rule");
}

function valueLabel(key: string): string {
  const labels: Record<string, string> = {
    userId: t("Viewer"),
    history: t("Watch history"),
    watched: t("Watched"),
    unwatched: t("Unwatched"),
    inProgress: t("In progress"),
    favorite: t("Favorite"),
    watchlist: t("On watchlist"),
    onWatchlist: t("On watchlist"),
    stateField: t("Activity"),
    stateOperator: t("Comparison"),
    stateValue: t("Value"),
    lastViewedAfter: t("Watched after"),
    lastViewedBefore: t("Watched before"),
    people: t("Person"),
    names: t("Name"),
    kind: t("Role"),
    op: t("Comparison"),
    value: t("Value"),
    unix: t("Date"),
    spanDay: t("Day"),
    name: t("Name"),
  };
  return labels[key] ?? browseFields().find(([field]) => field === key)?.[1] ?? t("Value");
}

// Wire values are separate from translated labels for every finite browse enum.
function enumValues(field: string): string[] | undefined {
  return (
    {
      type: ["movie", "show"],
      releaseGate: ["admitted", "held", "unknown"],
      playable: ["yes", "unknown"],
      debridCached: ["yes", "unknown"],
      qualities: ["4k", "hdr", "dv", "atmos"],
      statuses: ["Returning Series", "Ended", "Canceled", "In Production"],
    } as Record<string, string[]>
  )[field];
}

function enumLabel(field: string, value: string): string {
  const labelField =
    (
      {
        releaseGate: "gate",
        debridCached: "cached",
        qualities: "quality",
        statuses: "status",
      } as Record<string, string>
    )[field] ?? field;
  return catalogValueLabel(labelField, value);
}

function ValueEditor({
  value,
  onChange,
  field = "",
  choices,
}: {
  value: unknown;
  onChange: (v: unknown) => void;
  field?: string;
  choices?: string[];
}) {
  if (
    typeof value === "number" &&
    /^(dateAddedFrom|dateAddedTo|etherAddedFrom|etherAddedTo|unix|lastViewedAfter|lastViewedBefore)$/.test(
      field,
    )
  )
    return (
      <Input
        type="date"
        value={value > 0 ? new Date(value * 1000).toISOString().slice(0, 10) : ""}
        onChange={(e) => onChange(e.target.value ? Date.parse(e.target.value) / 1000 : 0)}
      />
    );

  if (Array.isArray(value))
    return (
      <div className="space-y-2">
        {value.map((v, i) => (
          <div key={i} className="flex items-start gap-2">
            <ValueEditor
              value={v}
              field={field}
              choices={choices}
              onChange={(next) => onChange(value.map((old, j) => (j === i ? next : old)))}
            />
            <Button
              size="sm"
              variant="ghost"
              onClick={() => onChange(value.filter((_, j) => j !== i))}
            >
              {t("Remove")}
            </Button>
          </div>
        ))}
        <Button
          size="sm"
          variant="outline"
          onClick={() => onChange([...value, value.length ? structuredClone(value[0]) : ""])}
        >
          {t("Add")}
        </Button>
      </div>
    );
  if (value && typeof value === "object")
    return (
      <div className="space-y-2">
        {Object.entries(value).map(([k, v]) => (
          <label key={k} className="block space-y-1 text-sm">
            <span>{valueLabel(k)}</span>
            <ValueEditor
              field={k}
              value={v}
              onChange={(next) => onChange({ ...value, [k]: next })}
            />
          </label>
        ))}
      </div>
    );
  const options = choices ?? enumValues(field);
  if (options)
    return (
      <Select value={String(value ?? "")} onChange={(e) => onChange(e.target.value)}>
        {!options.includes(String(value ?? "")) ? (
          <option value={String(value ?? "")}>{value ? String(value) : t("Choose")}</option>
        ) : null}
        {options.map((option) => (
          <option key={option} value={option}>
            {choices ? option : enumLabel(field, option)}
          </option>
        ))}
      </Select>
    );
  if (typeof value === "boolean")
    return (
      <Select value={String(value)} onChange={(e) => onChange(e.target.value === "true")}>
        <option value="true">{t("Yes")}</option>
        <option value="false">{t("No")}</option>
      </Select>
    );
  return (
    <Input
      type={typeof value === "number" ? "number" : "text"}
      value={String(value ?? "")}
      onChange={(e) =>
        onChange(typeof value === "number" ? Number(e.target.value) : e.target.value)
      }
    />
  );
}

// ---- Plugin text field value picker (short list chips / long list search) ---

type TallyItem = { value: string; count: number };

const LONG_LIST_PAGE_SIZE = 20;

function PluginTextValuePicker({
  parsed,
  kind,
  libraryId,
  level,
  onChange,
}: {
  parsed: ParsedTextCondition;
  kind: MediaKind;
  libraryId?: number;
  level?: string;
  onChange: (r: Rule) => void;
}) {
  const multiValued = pluginFieldMultiValued(parsed.fieldKey, kind);
  const declaredValues = pluginFieldDeclaredValues(parsed.fieldKey, kind);

  const [preferredMode, setPreferredMode] = useState<ChipCombineMode>(parsed.mode);
  const prevFieldRef = useRef(parsed.fieldKey);
  useEffect(() => {
    if (parsed.modeExplicit || prevFieldRef.current !== parsed.fieldKey) {
      prevFieldRef.current = parsed.fieldKey;
      setPreferredMode(parsed.mode);
    }
  }, [parsed.mode, parsed.modeExplicit, parsed.fieldKey]);

  const effectiveMode: ChipCombineMode = parsed.modeExplicit
    ? parsed.mode
    : parsed.mode === "none"
      ? "none"
      : preferredMode;

  const [initialValues, setInitialValues] = useState<TallyItem[]>([]);
  const [isLongList, setIsLongList] = useState(false);
  const [loadingInitial, setLoadingInitial] = useState(Boolean(libraryId));

  // Long-list search & pagination state
  const [searchText, setSearchText] = useState("");
  const debouncedSearch = useDebouncedValue(searchText.trim(), 180);
  const [suggestions, setSuggestions] = useState<TallyItem[]>([]);
  const [hasMoreSuggestions, setHasMoreSuggestions] = useState(false);
  const [loadingSuggestions, setLoadingSuggestions] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const searchBoxRef = useRef<HTMLDivElement>(null);

  // Short-list custom value draft
  const [customDraft, setCustomDraft] = useState("");

  useEffect(() => {
    setSearchText("");
    setCustomDraft("");
    setSearchOpen(false);
    if (!libraryId) {
      setInitialValues(declaredValues.map((value) => ({ value, count: 0 })));
      setIsLongList(false);
      setLoadingInitial(false);
      return;
    }
    const ac = new AbortController();
    setLoadingInitial(true);
    librariesApi
      .fieldValues(
        libraryId,
        parsed.fieldKey,
        {
          limit: SHORT_VALUE_LIST_LIMIT,
          offset: 0,
          order: "count",
          declared: true,
          level,
        },
        ac.signal,
      )
      .then((res) => {
        if (ac.signal.aborted) return;
        const rows = res.values ?? [];
        const more = Boolean(res.more || res.truncated);
        setInitialValues(rows);
        setIsLongList(more);
        setSuggestions(rows.slice(0, LONG_LIST_PAGE_SIZE));
        setHasMoreSuggestions(more || rows.length > LONG_LIST_PAGE_SIZE);
        setLoadingInitial(false);
      })
      .catch(() => {
        if (ac.signal.aborted) return;
        setInitialValues(declaredValues.map((value) => ({ value, count: 0 })));
        setIsLongList(false);
        setLoadingInitial(false);
      });
    return () => ac.abort();
  }, [libraryId, parsed.fieldKey, level]);

  useEffect(() => {
    if (!isLongList || !libraryId) return;
    if (!debouncedSearch) {
      setSuggestions(initialValues.slice(0, LONG_LIST_PAGE_SIZE));
      setHasMoreSuggestions(true);
      setLoadingSuggestions(false);
      return;
    }
    const ac = new AbortController();
    setLoadingSuggestions(true);
    librariesApi
      .fieldValues(
        libraryId,
        parsed.fieldKey,
        {
          q: debouncedSearch,
          limit: LONG_LIST_PAGE_SIZE,
          offset: 0,
          order: "count",
          declared: true,
          level,
        },
        ac.signal,
      )
      .then((res) => {
        if (ac.signal.aborted) return;
        setSuggestions(res.values ?? []);
        setHasMoreSuggestions(Boolean(res.more || res.truncated));
        setLoadingSuggestions(false);
      })
      .catch(() => {
        if (ac.signal.aborted) return;
        setSuggestions([]);
        setHasMoreSuggestions(false);
        setLoadingSuggestions(false);
      });
    return () => ac.abort();
  }, [isLongList, libraryId, parsed.fieldKey, level, debouncedSearch, initialValues]);

  useEffect(() => {
    if (!searchOpen) return;
    const onDown = (e: MouseEvent) => {
      if (searchBoxRef.current && !searchBoxRef.current.contains(e.target as Node)) {
        setSearchOpen(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [searchOpen]);

  const emit = (nextMode: ChipCombineMode, nextValues: string[]) => {
    setPreferredMode(nextMode);
    onChange(buildTextCondition(parsed.fieldKey, nextMode, nextValues));
  };

  const toggleValue = (val: string) => {
    const exists = parsed.values.includes(val);
    const next = exists ? parsed.values.filter((v) => v !== val) : [...parsed.values, val];
    emit(effectiveMode, next);
  };

  const addValue = (raw: string) => {
    const clean = raw.trim();
    if (!clean) return;
    if (!parsed.values.includes(clean)) {
      emit(effectiveMode, [...parsed.values, clean]);
    }
  };

  const removeValue = (val: string) => {
    emit(
      effectiveMode,
      parsed.values.filter((v) => v !== val),
    );
  };

  const loadMoreSuggestions = () => {
    if (!libraryId || loadingMore || !hasMoreSuggestions) return;
    setLoadingMore(true);
    librariesApi
      .fieldValues(libraryId, parsed.fieldKey, {
        q: debouncedSearch || undefined,
        limit: LONG_LIST_PAGE_SIZE,
        offset: suggestions.length,
        order: "count",
        declared: true,
        level,
      })
      .then((res) => {
        const nextRows = res.values ?? [];
        setSuggestions((prev) => {
          const seen = new Set(prev.map((x) => x.value));
          const merged = [...prev];
          for (const row of nextRows) {
            if (!seen.has(row.value)) {
              seen.add(row.value);
              merged.push(row);
            }
          }
          return merged;
        });
        setHasMoreSuggestions(Boolean(res.more || res.truncated));
      })
      .finally(() => setLoadingMore(false));
  };

  // i18n: memo holds no copy
  const shortListChips = useMemo(() => {
    const seen = new Set<string>();
    const out: { value: string; count: number | null }[] = [];
    for (const item of initialValues) {
      if (seen.has(item.value)) continue;
      seen.add(item.value);
      out.push({ value: item.value, count: item.count });
    }
    for (const v of parsed.values) {
      if (seen.has(v)) continue;
      seen.add(v);
      out.push({ value: v, count: null });
    }
    return out;
  }, [initialValues, parsed.values]);

  const showAllOption = multiValued || effectiveMode === "all";
  const trimmedSearch = searchText.trim();
  const hasExactSuggestion = suggestions.some((s) => s.value === trimmedSearch);
  const canUseCustomSearch =
    trimmedSearch.length > 0 && !parsed.values.includes(trimmedSearch) && !hasExactSuggestion;

  const commitCustomDraft = () => {
    const clean = customDraft.trim();
    if (!clean) return;
    addValue(clean);
    setCustomDraft("");
  };

  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <div className="w-36 shrink-0">
          <Select
            aria-label={t("Comparison")}
            value={effectiveMode}
            onChange={(e) => emit(e.target.value as ChipCombineMode, parsed.values)}
          >
            <option value="any">{t("Any of")}</option>
            {showAllOption ? <option value="all">{t("All of")}</option> : null}
            <option value="none">{t("None of")}</option>
          </Select>
        </div>
        {parsed.values.length > 0 ? (
          <Button type="button" size="sm" variant="ghost" onClick={() => emit(effectiveMode, [])}>
            {t("Clear")}
          </Button>
        ) : null}
      </div>

      {loadingInitial ? (
        <p className="text-xs text-fg-muted">{t("Loading…")}</p>
      ) : isLongList ? (
        <div className="space-y-2">
          {parsed.values.length > 0 ? (
            <div
              className="flex flex-wrap gap-1.5"
              role="group"
              aria-label={pluginFieldLabel(parsed.fieldKey, kind)}
            >
              {parsed.values.map((val) => (
                <span
                  key={val}
                  className="inline-flex items-center gap-1 rounded-full border border-primary/50 bg-primary-subtle py-0.5 pl-2.5 pr-1.5 text-xs font-medium text-primary"
                >
                  <span>{val}</span>
                  <button
                    type="button"
                    aria-label={t("Remove {value}", { value: val })}
                    className="rounded-full p-0.5 hover:bg-primary/20"
                    onClick={() => removeValue(val)}
                  >
                    <X size={12} aria-hidden />
                  </button>
                </span>
              ))}
            </div>
          ) : null}

          <div ref={searchBoxRef} className="relative">
            <div className="relative">
              <Search
                size={14}
                aria-hidden
                className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-muted"
              />
              <Input
                value={searchText}
                className="pl-8"
                aria-label={pluginFieldLabel(parsed.fieldKey, kind)}
                placeholder={t("Search values or type a new one…")}
                onFocus={() => setSearchOpen(true)}
                onChange={(e) => {
                  setSearchText(e.target.value);
                  setSearchOpen(true);
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && trimmedSearch) {
                    e.preventDefault();
                    const exact = suggestions.find(
                      (s) => s.value.toLowerCase() === trimmedSearch.toLowerCase(),
                    );
                    addValue(exact ? exact.value : trimmedSearch);
                    setSearchText("");
                  } else if (e.key === "Escape") {
                    setSearchOpen(false);
                  }
                }}
              />
            </div>

            {searchOpen ? (
              <div className="mt-1.5 max-h-56 overflow-y-auto rounded-lg border border-border bg-surface shadow-sm">
                {loadingSuggestions ? (
                  <p className="px-3 py-2 text-xs text-fg-muted">{t("Loading…")}</p>
                ) : (
                  <div className="divide-y divide-border-subtle">
                    {suggestions.map((item) => {
                      const selected = parsed.values.includes(item.value);
                      return (
                        <button
                          key={item.value}
                          type="button"
                          onClick={() => toggleValue(item.value)}
                          className={cn(
                            "flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-xs transition-colors hover:bg-surface-hover",
                            selected && "bg-primary-subtle font-medium text-primary",
                          )}
                        >
                          <span className="flex min-w-0 items-center gap-1.5 truncate">
                            {selected ? <Check size={12} className="shrink-0" aria-hidden /> : null}
                            <span className="truncate">{item.value}</span>
                          </span>
                          <span className="shrink-0 tabular-nums text-[11px] text-fg-muted">
                            {formatNumber(item.count)}
                          </span>
                        </button>
                      );
                    })}

                    {suggestions.length === 0 && !canUseCustomSearch ? (
                      <p className="px-3 py-2 text-xs text-fg-muted">{t("No matches")}</p>
                    ) : null}

                    {canUseCustomSearch ? (
                      <button
                        type="button"
                        onClick={() => {
                          addValue(trimmedSearch);
                          setSearchText("");
                        }}
                        className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-xs font-medium text-primary hover:bg-surface-hover"
                      >
                        <Plus size={12} aria-hidden />
                        <span>{t('Use "{value}"', { value: trimmedSearch })}</span>
                      </button>
                    ) : null}

                    {hasMoreSuggestions ? (
                      <div className="p-1.5 text-center">
                        <Button
                          type="button"
                          size="sm"
                          variant="ghost"
                          disabled={loadingMore}
                          onClick={loadMoreSuggestions}
                        >
                          {loadingMore ? t("Loading…") : t("Show more")}
                        </Button>
                      </div>
                    ) : null}
                  </div>
                )}
              </div>
            ) : null}
          </div>
        </div>
      ) : (
        <div className="space-y-2">
          {shortListChips.length > 0 ? (
            <div
              className="flex flex-wrap gap-1.5"
              role="group"
              aria-label={pluginFieldLabel(parsed.fieldKey, kind)}
            >
              {shortListChips.map((item) => {
                const selected = parsed.values.includes(item.value);
                return (
                  <button
                    key={item.value}
                    type="button"
                    aria-pressed={selected}
                    onClick={() => toggleValue(item.value)}
                    className={cn(
                      "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs transition-colors",
                      selected
                        ? "border-primary bg-primary-subtle font-medium text-primary"
                        : "border-border bg-bg-subtle/60 text-fg hover:border-primary/40 hover:bg-surface-hover",
                    )}
                  >
                    <span>{item.value}</span>
                    {item.count !== null ? (
                      <span className="tabular-nums text-[11px] opacity-70">
                        {formatNumber(item.count)}
                      </span>
                    ) : null}
                  </button>
                );
              })}
            </div>
          ) : null}

          <div className="flex items-center gap-2">
            <Input
              value={customDraft}
              placeholder={t("Add another value…")}
              aria-label={t("Add another value…")}
              onChange={(e) => setCustomDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  commitCustomDraft();
                }
              }}
            />
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!customDraft.trim()}
              onClick={commitCustomDraft}
            >
              {t("Add")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

// ---- Plugin numeric / date / duration comparison editor ---------------------

function PluginNumericEditor({
  parsed,
  rule,
  onChange,
}: {
  parsed: ParsedNumericCondition;
  rule: Rule;
  onChange: (r: Rule) => void;
}) {
  const wireKey = `${rule.field}|${rule.op}|${String(rule.value ?? "")}`;
  const lastEmittedRef = useRef<string>(wireKey);

  const [op, setOp] = useState<NumericCompareOp>(parsed.op);
  const [a, setA] = useState(parsed.a);
  const [b, setB] = useState(parsed.b);
  const [error, setError] = useState<string | undefined>(undefined);

  useEffect(() => {
    if (wireKey === lastEmittedRef.current) return;
    lastEmittedRef.current = wireKey;
    setOp(parsed.op);
    setA(parsed.a);
    setB(parsed.b);
    setError(undefined);
  }, [wireKey, parsed.op, parsed.a, parsed.b]);

  const apply = (nextOp: NumericCompareOp, nextA: string, nextB: string) => {
    setOp(nextOp);
    setA(nextA);
    setB(nextB);
    const built = buildNumericCondition(parsed.fieldKey, parsed.valueKind, nextOp, nextA, nextB);
    setError(built.error);
    lastEmittedRef.current = `${built.rule.field}|${built.rule.op}|${String(built.rule.value ?? "")}`;
    onChange(built.rule);
  };

  const isDate = parsed.valueKind === "date";
  const isDuration = parsed.valueKind === "duration";
  const inputType = isDate ? "date" : "number";
  const step = parsed.valueKind === "int" ? 1 : "any";

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="w-36 shrink-0">
          <Select
            aria-label={t("Comparison")}
            value={op}
            onChange={(e) => apply(e.target.value as NumericCompareOp, a, b)}
          >
            <option value="is">{t("is")}</option>
            <option value="isNot">{t("is not")}</option>
            <option value="lt">{t("is less than")}</option>
            <option value="lte">{t("is at most")}</option>
            <option value="gt">{t("is greater than")}</option>
            <option value="gte">{t("is at least")}</option>
            <option value="between">{t("is between")}</option>
          </Select>
        </div>

        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
          <div className={isDate ? "w-40" : "w-28"}>
            <Input
              type={inputType}
              step={isDate ? undefined : step}
              min={isDuration ? 0 : undefined}
              value={a}
              aria-label={op === "between" ? (isDate ? t("From") : t("Minimum")) : t("Value")}
              onChange={(e) => apply(op, e.target.value, b)}
            />
          </div>

          {op === "between" ? (
            <>
              <span className="text-xs text-fg-muted">{t("and")}</span>
              <div className={isDate ? "w-40" : "w-28"}>
                <Input
                  type={inputType}
                  step={isDate ? undefined : step}
                  min={isDuration ? 0 : undefined}
                  value={b}
                  aria-label={isDate ? t("To") : t("Maximum")}
                  onChange={(e) => apply(op, a, e.target.value)}
                />
              </div>
            </>
          ) : null}

          {isDuration ? <span className="text-xs text-fg-muted">{t("min")}</span> : null}
        </div>
      </div>

      {error ? <p className="text-xs text-danger">{error}</p> : null}
    </div>
  );
}

// ---- ExtendedCondition ------------------------------------------------------

export function ExtendedCondition({
  rule,
  onChange,
  onRemove,
  renderChild,
  kind,
  libraryId,
  level,
}: {
  kind?: MediaKind;
  libraryId?: number;
  level?: string;
  rule: Rule;
  onChange: (r: Rule) => void;
  onRemove: () => void;
  renderChild: (
    r: Rule,
    i: number,
    onChange: (r: Rule) => void,
    onRemove: () => void,
  ) => React.ReactNode;
}) {
  const parsedText = kind ? parseTextCondition(rule, kind) : null;
  const parsedNumeric = kind ? parseNumericCondition(rule, kind) : null;
  const pluginBoolKey =
    kind &&
    rule.field.startsWith("browse:") &&
    pluginFieldKind(rule.field.slice("browse:".length), kind) === "bool"
      ? rule.field.slice("browse:".length)
      : null;

  const children = Array.isArray(rule.value) ? (rule.value as Rule[]) : [];
  const runtime = !kind && (rule.field.includes("Runtime") || rule.field.includes("Runtimes"));
  const convert = (value: unknown, mult: number): unknown =>
    Array.isArray(value)
      ? value.map((v) => convert(v, mult))
      : typeof value === "number"
        ? value * mult
        : value;
  const displayValue = runtime ? convert(rule.value, 1 / 60e9) : rule.value;

  return (
    <div className="space-y-3 rounded-xl border border-border p-3">
      <div className="flex items-center justify-between gap-3">
        <span className="font-medium">{conditionTitle(rule, kind)}</span>
        <Button size="sm" variant="ghost" onClick={onRemove}>
          {t("Remove")}
        </Button>
      </div>

      {parsedText && kind ? (
        <PluginTextValuePicker
          parsed={parsedText}
          kind={kind}
          libraryId={libraryId}
          level={level}
          onChange={onChange}
        />
      ) : parsedNumeric ? (
        <PluginNumericEditor parsed={parsedNumeric} rule={rule} onChange={onChange} />
      ) : pluginBoolKey ? (
        <div className="flex flex-wrap items-center gap-2">
          <div className="w-28 shrink-0">
            <Select
              aria-label={t("Comparison")}
              value={rule.op}
              onChange={(e) => onChange({ ...rule, op: e.target.value as Rule["op"] })}
            >
              <option value="is">{t("is")}</option>
              <option value="isNot">{t("is not")}</option>
            </Select>
          </div>
          <div className="w-28 shrink-0">
            <Select
              aria-label={t("Value")}
              value={String(rule.value) === "false" ? "false" : "true"}
              onChange={(e) => onChange({ ...rule, value: e.target.value })}
            >
              <option value="true">{t("Yes")}</option>
              <option value="false">{t("No")}</option>
            </Select>
          </div>
        </div>
      ) : rule.field === "group" ? (
        <>
          <Select
            value={rule.op}
            onChange={(e) => onChange({ ...rule, op: e.target.value as Rule["op"] })}
          >
            <option value="is">{t("All")}</option>
            <option value="in">{t("Any")}</option>
            <option value="isNot">{t("Not")}</option>
          </Select>
          {children.map((child, i) =>
            renderChild(
              child,
              i,
              (next) => onChange({ ...rule, value: children.map((v, j) => (i === j ? next : v)) }),
              () => onChange({ ...rule, value: children.filter((_, j) => j !== i) }),
            ),
          )}
          <Select
            aria-label={t("Add condition")}
            value=""
            onChange={(e) => {
              if (e.target.value)
                onChange({
                  ...rule,
                  value: [...children, newBrowseCondition(e.target.value, kind)],
                });
            }}
          >
            <option value="">{t("More conditions")}</option>
            {browseFields(kind).map(([key, label]) => (
              <option key={key} value={key}>
                {label}
              </option>
            ))}
          </Select>
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              onChange({
                ...rule,
                value: [
                  ...children,
                  kind ? newBrowseCondition("year", kind) : { field: "genre", op: "in", value: [] },
                ],
              })
            }
          >
            {t("Add condition")}
          </Button>
        </>
      ) : (
        <>
          {kind ? (
            <Select
              value={rule.op}
              onChange={(e) => onChange({ ...rule, op: e.target.value as Rule["op"] })}
            >
              <option value="is">{t("is")}</option>
              <option value="isNot">{t("is not")}</option>
            </Select>
          ) : null}
          <ValueEditor
            field={kind ? "" : rule.field.replace("browse:", "")}
            value={displayValue}
            choices={kind?.fields?.find((field) => `browse:${field.key}` === rule.field)?.enum}
            onChange={(value) =>
              onChange({ ...rule, value: runtime ? convert(value, 60e9) : value })
            }
          />
        </>
      )}
    </div>
  );
}

export function newBrowseCondition(key: string, kind?: MediaKind): Rule {
  if (key === "__group") return { field: "group", op: "is", value: [] };
  if (key.startsWith("__")) {
    const todayUnix = Math.floor(Date.now() / 86_400_000) * 86_400;
    const value: Record<string, unknown> =
      key === "__history"
        ? { history: true }
        : key === "__watched"
          ? { watched: true }
          : key === "__unwatched"
            ? { unwatched: true }
            : key === "__progress"
              ? { inProgress: true }
              : key === "__favorite"
                ? { favorite: true }
                : key === "__watchlist"
                  ? { onWatchlist: true }
                  : key === "__lastViewedAfter"
                    ? { lastViewedAfter: todayUnix - 30 * 86_400 }
                    : key === "__lastViewedBefore"
                      ? { lastViewedBefore: todayUnix }
                      : { stateField: "lastRatedAt", stateOperator: ">", stateValue: 0 };
    return {
      field: "viewer",
      op: "is",
      value,
    };
  }
  if (kind) {
    const fd = kind.fields?.find((field) => field.key === key);
    if (fd?.type === "bool") {
      return { field: `browse:${key}`, op: "is", value: "true" };
    }
    return {
      field: `browse:${key}`,
      op: "is",
      value: "",
    };
  }

  const arrays = [
    "qualities",
    "tags",
    "keywords",
    "years",
    "excludeYears",
    "statuses",
    "excludeRatings",
    "excludeRuntimes",
    "languages",
    "countries",
    "excludeCountries",
    "contentRatings",
    "excludeContentRatings",
    "collections",
    "excludeCollections",
  ];
  const numeric =
    (/^(min|max)/.test(key) && key !== "maxContentRating") ||
    /From$|To$/.test(key) ||
    ["collection", "person"].includes(key);
  return {
    field: `browse:${key}` as RuleField,
    op: "is",
    value:
      enumValues(key) && !arrays.includes(key)
        ? enumValues(key)![0]
        : ["anime", "kids"].includes(key)
          ? true
          : arrays.includes(key)
            ? [
                key.includes("Years") ||
                [
                  "years",
                  "excludeRatings",
                  "excludeRuntimes",
                  "collections",
                  "excludeCollections",
                ].includes(key)
                  ? 0
                  : (enumValues(key)?.[0] ?? ""),
              ]
            : key === "credits"
              ? [{ kind: "cast", names: [""] }]
              : ["titleFilters", "studioFilters"].includes(key)
                ? [{ op: "contains", value: "" }]
                : key === "releasedFilters"
                  ? [{ op: ">=", unix: 0, spanDay: true }]
                  : numeric
                    ? 0
                    : "",
  };
}

export function conditionValue(value: unknown, field = ""): string {
  if (typeof value === "number" && /runtime/i.test(field)) return String(value / 60e9);
  if (typeof value === "number" && /dateAdded|etherAdded|unix|lastViewed/.test(field) && value > 0)
    return new Date(value * 1000).toISOString().slice(0, 10);
  if (Array.isArray(value)) return value.map((v) => conditionValue(v, field)).join(", ");
  if (value && typeof value === "object")
    return Object.entries(value)
      .map(([key, v]) => `${valueLabel(key)}: ${conditionValue(v, key)}`)
      .join(" · ");
  if (typeof value === "boolean") return value ? t("Yes") : t("No");
  return String(value ?? "");
}

// Import a transient catalogue view directly into the shared rule model.
export function conditionsFromBrowse(filter: Record<string, unknown>, nested = false): Rule[] {
  const rules: Rule[] = [];
  for (const [key, value] of Object.entries(filter)) {
    if ((key === "type" && !nested) || key === "catalogueSources") continue;
    if (key === "genres") {
      rules.push({ field: "genre", op: "in", value });
      continue;
    }
    if (key === "group") {
      const walk = (value: unknown): Rule => {
        const group = value as Record<string, unknown>;
        const wrap = (children: Rule[], op: Rule["op"]): Rule =>
          children.length === 1 && op !== "isNot"
            ? children[0]!
            : { field: "group", op, value: children };
        if (group.filter)
          return wrap(conditionsFromBrowse(group.filter as Record<string, unknown>, true), "is");
        const { operator, children, ...state } = group;
        if (Object.keys(state).length) return { field: "viewer", op: "is", value: state };
        const converted = ((children ?? []) as unknown[]).map(walk);
        if (operator === "or" && converted.some(emptyGroup))
          return { field: "group", op: "is", value: [] };
        return wrap(
          operator === "not" ? converted : converted.filter((r) => !emptyGroup(r)),
          operator === "or" ? "in" : operator === "not" ? "isNot" : "is",
        );
      };
      const rule = walk(value);
      if (!emptyGroup(rule)) rules.push(rule);
      continue;
    }
    rules.push({ field: `browse:${key}`, op: "is", value });
  }
  return rules;
}

function emptyGroup(rule: Rule): boolean {
  return (
    rule.field === "group" &&
    rule.op !== "isNot" &&
    Array.isArray(rule.value) &&
    rule.value.length === 0
  );
}
