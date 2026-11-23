import { SettingsSection } from "../../components/CollapsibleSections";
import {
  Badge,
  Button,
  Input,
  SearchSelect,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  ChevronDown,
  ChevronRight,
  Eye,
  Pencil,
  Plus,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { MutationError } from "../../components/feedback";
import { TestButton, type TestState } from "../../components/fields";
import { SettingsRow } from "../../components/SettingsRow";
import { DragHandle, SortableList } from "../../components/Sortable";
import { t, tn } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api, errorMessage, newId } from "../../lib/api";
import type {
  IdentityMediaType,
  IdentityRule,
  IdentityTargetKind,
  Indexer,
  KVOverride,
  RequestPreview,
  RequestPreviewCall,
} from "../../lib/types";
import { OrderButtons, swap, toastError } from "./form";

const PROFILE_IDS = [
  "ether",
  "sonarr",
  "radarr",
  "prowlarr",
  "nzbhydra2",
  "sabnzbd",
  "nzbget",
  "browser",
  "custom",
] as const;

const TARGET_KINDS: IdentityTargetKind[] = [
  "indexer",
  "host",
  "nzb_downloads",
  "torrent_downloads",
  "all_searches",
];

const MEDIA_TYPES: IdentityMediaType[] = ["", "movie", "tv", "music", "books"];
const SPECIFIC_MEDIA_TYPES: Exclude<IdentityMediaType, "">[] = ["movie", "tv", "music", "books"];

const HEADER_NAME_RE = /^[A-Za-z0-9!#$%&'*+.^_`|~-]+$/;
const FORBIDDEN_HEADERS = new Set(["host", "content-length", "transfer-encoding"]);
const HOST_LABEL_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

export function identityProfileLabel(profile: string): string {
  switch (profile) {
    case "":
    case "ether":
      return t("Ether (honest, recommended)");
    case "sonarr":
      return t("Compatible with Sonarr");
    case "radarr":
      return t("Compatible with Radarr");
    case "prowlarr":
      return t("Compatible with Prowlarr");
    case "nzbhydra2":
      return t("Compatible with NZBHydra2");
    case "sabnzbd":
      return t("Compatible with SABnzbd");
    case "nzbget":
      return t("Compatible with NZBGet");
    case "browser":
      return t("Compatible with a web browser");
    case "custom":
      return t("Custom");
    default:
      return profile;
  }
}

export function identityShortLabel(profile: string, headers?: KVOverride[] | null): string {
  if (profile === "custom") {
    const ua = headers?.find((h) => h.name.toLowerCase() === "user-agent" && !h.remove)?.value;
    if (ua?.trim()) return ua.trim();
    return t("Custom");
  }
  if (!profile || profile === "ether") return t("Ether");
  return identityProfileLabel(profile);
}

export function targetKindLabel(target: IdentityTargetKind): string {
  switch (target) {
    case "indexer":
      return t("Specific indexer");
    case "host":
      return t("Host pattern");
    case "nzb_downloads":
      return t("All NZB downloads");
    case "torrent_downloads":
      return t("All torrent downloads");
    case "all_searches":
      return t("All indexer searches");
  }
}

export function targetKindBadge(target: IdentityTargetKind): string {
  switch (target) {
    case "indexer":
      return t("Indexer");
    case "host":
      return t("Host");
    case "nzb_downloads":
      return t("NZB downloads");
    case "torrent_downloads":
      return t("Torrent downloads");
    case "all_searches":
      return t("All searches");
  }
}

export function mediaTypeLabel(mediaType?: IdentityMediaType): string {
  switch (mediaType ?? "") {
    case "movie":
      return t("Movies");
    case "tv":
      return t("TV");
    case "music":
      return t("Music");
    case "books":
      return t("Books");
    default:
      return t("Any media type");
  }
}

export function mediaTypeBadge(mediaType?: IdentityMediaType): string {
  switch (mediaType ?? "") {
    case "movie":
      return t("Movies");
    case "tv":
      return t("TV");
    case "music":
      return t("Music");
    case "books":
      return t("Books");
    default:
      return "";
  }
}

function indexerDisplayName(ix: Indexer): string {
  if (ix.name.trim()) return ix.name.trim();
  try {
    return new URL(ix.url).host || ix.url;
  } catch {
    return ix.url || ix.id;
  }
}

function ruleTargetBaseSummary(rule: IdentityRule, indexers: Indexer[]): string {
  const scopeSuffix =
    rule.scope === "search"
      ? ` (${t("searches")})`
      : rule.scope === "download"
        ? ` (${t("downloads")})`
        : "";
  switch (rule.target) {
    case "indexer": {
      const ix = indexers.find((x) => x.id === rule.indexerId);
      const name = ix ? indexerDisplayName(ix) : rule.indexerId || t("Specific indexer");
      return `${name}${scopeSuffix}`;
    }
    case "host":
      return `${rule.host?.trim() || t("Host pattern")}${scopeSuffix}`;
    case "nzb_downloads":
      return t("All NZB downloads");
    case "torrent_downloads":
      return t("All torrent downloads");
    case "all_searches":
      return t("All indexer searches");
  }
}

export function ruleTargetSummary(rule: IdentityRule, indexers: Indexer[]): string {
  const base = ruleTargetBaseSummary(rule, indexers);
  const media = mediaTypeBadge(rule.mediaType);
  return media ? `${base} · ${media}` : base;
}

function hostOfURL(raw: string): string {
  try {
    return new URL(raw).hostname.toLowerCase().replace(/\.$/, "");
  } catch {
    return "";
  }
}

function matchHostPattern(pattern: string, host: string): boolean {
  const p = pattern.trim().toLowerCase().replace(/\.$/, "");
  const h = host.trim().toLowerCase().replace(/\.$/, "");
  if (!p || !h) return false;
  if (p.startsWith("*.")) {
    const suffix = p.slice(1);
    return h.length > suffix.length && h.endsWith(suffix);
  }
  return h === p;
}

export function validHostPattern(pattern: string): boolean {
  let p = pattern.trim().toLowerCase().replace(/\.$/, "");
  if (!p || p.includes("://") || p.includes("/") || p.includes(":")) return false;
  if (p.startsWith("*.")) p = p.slice(2);
  if (!p || p.includes("*")) return false;
  const labels = p.split(".");
  if (labels.length === 1 && labels[0] === "localhost") return true;
  return labels.every((lbl) => HOST_LABEL_RE.test(lbl));
}

/**
 * Mirrors internal/config/requestid.go ResolveRequest precedence:
 * specific indexer > exact host > wildcard host > download type / all searches > Ether default.
 * Within a target tier, a specific mediaType beats Any; list order breaks ties.
 */
export function resolveRuleForIndexer(
  rules: IdentityRule[],
  indexer: Pick<Indexer, "id" | "url" | "protocol">,
  kind: "search" | "nzb_download" | "torrent_download",
  mediaType: IdentityMediaType = "",
): { rule: IdentityRule | null; profile: string } {
  const host = hostOfURL(indexer.url);
  const isDownload = kind !== "search";
  let bestSpec = 0;
  let best: IdentityRule | null = null;
  for (const r of rules) {
    const ruleMedia = (r.mediaType ?? "") as IdentityMediaType;
    if (ruleMedia && ruleMedia !== mediaType) continue;
    let targetRank = 0;
    if (r.target === "indexer") {
      if (r.indexerId !== indexer.id) continue;
      if (r.scope === "search" && isDownload) continue;
      if (r.scope === "download" && !isDownload) continue;
      targetRank = 4;
    } else if (r.target === "host") {
      if (!matchHostPattern(r.host ?? "", host)) continue;
      if (r.scope === "search" && isDownload) continue;
      if (r.scope === "download" && !isDownload) continue;
      targetRank = (r.host ?? "").trim().startsWith("*.") ? 2 : 3;
    } else if (r.target === "nzb_downloads") {
      if (kind !== "nzb_download") continue;
      targetRank = 1;
    } else if (r.target === "torrent_downloads") {
      if (kind !== "torrent_download") continue;
      targetRank = 1;
    } else if (r.target === "all_searches") {
      if (kind !== "search") continue;
      targetRank = 1;
    }
    if (targetRank === 0) continue;
    const spec = targetRank * 2 + (ruleMedia ? 1 : 0);
    if (spec > bestSpec) {
      bestSpec = spec;
      best = r;
    }
  }
  return { rule: best, profile: best?.profile || "ether" };
}

function validateOverrides(
  ovs: KVOverride[] | null | undefined,
  kind: "header" | "param",
): string | undefined {
  if (!ovs?.length) return undefined;
  const seen = new Set<string>();
  for (const ov of ovs) {
    const name = ov.name.trim();
    if (!name) return t("Every row needs a name.");
    if (/[\r\n]/.test(name) || /[\r\n]/.test(ov.value ?? "")) {
      return t("{name} contains an unsupported line break.", { name });
    }
    if (kind === "header") {
      if (!HEADER_NAME_RE.test(name)) return t("{name} is not a valid header name.", { name });
      if (FORBIDDEN_HEADERS.has(name.toLowerCase())) {
        return t("{name} is managed by Ether and cannot be edited.", { name });
      }
    }
    if (kind === "param" && ov.remove && (name === "apikey" || name === "t")) {
      return t("{name} cannot be removed.", { name });
    }
    const key = kind === "header" ? name.toLowerCase() : name;
    if (seen.has(key)) return t("{name} is listed twice.", { name });
    seen.add(key);
  }
  return undefined;
}

function cleanOverrides(ovs: KVOverride[] | null | undefined): KVOverride[] | undefined {
  const cleaned = (ovs ?? [])
    .map((ov) => ({
      name: ov.name.trim(),
      ...(ov.remove ? { remove: true } : { value: ov.value ?? "" }),
    }))
    .filter((ov) => ov.name !== "");
  return cleaned.length ? cleaned : undefined;
}

function editCount(rule: IdentityRule): number {
  return (rule.headers?.length ?? 0) + (rule.params?.length ?? 0);
}

/** One HTTP call preview (request line, Host, and ordered headers). */
function PreviewCallCard({
  label,
  call,
  highlight,
}: {
  label: string;
  call: RequestPreviewCall;
  highlight?: boolean;
}) {
  const lines = call.requestLine.split("\n");
  return (
    <div
      className={
        highlight
          ? "min-w-0 rounded-lg border border-primary/30 bg-bg-subtle/70 p-3"
          : "min-w-0 rounded-lg border border-border bg-bg-subtle/50 p-3"
      }
    >
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-fg-muted">{label}</span>
        {call.profile ? (
          <Badge variant={highlight ? "default" : "outline"}>
            {identityShortLabel(call.profile)}
          </Badge>
        ) : null}
      </div>
      <div className="overflow-x-auto font-mono text-xs leading-relaxed">
        {lines.map((line, i) => (
          <div
            key={i}
            className={i === 0 ? "break-all font-medium text-fg" : "break-all text-fg-muted"}
          >
            {line}
          </div>
        ))}
        <div className="mt-1.5 space-y-0.5 border-t border-border/70 pt-1.5">
          {call.headers.map((h, i) => (
            <div key={`${h.name}-${i}`} className="break-all">
              <span className="font-medium text-fg">{h.name}:</span>{" "}
              <span className="text-fg-muted">{h.value}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

/** Live server-rendered "What the indexer sees" preview for a rule or default identity. */
function RulePreviewPanel({
  rule,
  rules,
  compact = false,
}: {
  rule?: IdentityRule;
  rules: IdentityRule[];
  compact?: boolean;
}) {
  const [preview, setPreview] = useState<RequestPreview | null>(null);
  const [error, setError] = useState("");
  const payloadKey = JSON.stringify({ rule, rules });

  useEffect(() => {
    let live = true;
    const timer = window.setTimeout(() => {
      api
        .previewIndexer({ rule, rules })
        .then((res) => {
          if (live) {
            setPreview(res);
            setError("");
          }
        })
        .catch((e: unknown) => {
          if (live) setError(errorMessage(e));
        });
    }, 180);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
    // payloadKey tracks rule & rules deterministically.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [payloadKey]);

  const downloadPrimary =
    rule?.target === "nzb_downloads" ||
    rule?.target === "torrent_downloads" ||
    rule?.scope === "download";

  const mediaBadge = mediaTypeBadge(rule?.mediaType);
  const searchLabel = mediaBadge
    ? t("Sample search ({media})", { media: mediaBadge })
    : t("Sample search");
  const downloadLabel =
    preview?.downloadKind === "torrent"
      ? mediaBadge
        ? t("Sample torrent download ({media})", { media: mediaBadge })
        : t("Sample torrent download")
      : mediaBadge
        ? t("Sample NZB download ({media})", { media: mediaBadge })
        : t("Sample NZB download");

  return (
    <div className={compact ? "space-y-2.5" : "space-y-3"}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs font-medium text-fg">{t("What the indexer sees")}</p>
        <span className="text-xs text-fg-subtle">{t("API keys and secrets are masked")}</span>
      </div>
      {error ? (
        <p className="text-xs text-danger" role="alert">
          {error}
        </p>
      ) : !preview ? (
        <p className="text-xs text-fg-muted">{t("Rendering preview…")}</p>
      ) : (
        <div className="grid min-w-0 grid-cols-1 gap-2.5">
          {downloadPrimary ? (
            <>
              <PreviewCallCard label={downloadLabel} call={preview.download} highlight />
              <PreviewCallCard label={searchLabel} call={preview.search} />
            </>
          ) : (
            <>
              <PreviewCallCard label={searchLabel} call={preview.search} highlight />
              <PreviewCallCard label={downloadLabel} call={preview.download} />
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** Header or query-parameter override editor inside the Advanced disclosure. */
function OverrideList({
  title,
  help,
  value,
  onChange,
  kind,
  namePlaceholder,
  valuePlaceholder,
  addLabel,
}: {
  title: string;
  help: string;
  value: KVOverride[];
  onChange: (next: KVOverride[]) => void;
  kind: "header" | "param";
  namePlaceholder: string;
  valuePlaceholder: string;
  addLabel: string;
}) {
  const problem = validateOverrides(value, kind);
  const update = (i: number, patch: Partial<KVOverride>) =>
    onChange(value.map((ov, j) => (j === i ? { ...ov, ...patch } : ov)));

  return (
    <SettingsRow
      stacked
      title={title}
      description={
        <>
          {help}
          {problem ? (
            <span className="mt-1 block text-xs text-danger" role="alert">
              {problem}
            </span>
          ) : null}
        </>
      }
    >
      <div className="space-y-2">
        {value.map((ov, i) => {
          const isProtectedParam =
            kind === "param" && (ov.name.trim() === "apikey" || ov.name.trim() === "t");
          return (
            <div key={i} className="flex flex-wrap items-center gap-2">
              <Input
                className="w-full sm:w-40"
                aria-label={kind === "header" ? t("Header name") : t("Parameter name")}
                placeholder={namePlaceholder}
                value={ov.name}
                spellCheck={false}
                onChange={(e) => {
                  const nextName = e.currentTarget.value;
                  const protect =
                    kind === "param" && (nextName.trim() === "apikey" || nextName.trim() === "t");
                  update(i, {
                    name: nextName,
                    ...(protect ? { remove: undefined } : {}),
                  });
                }}
              />
              <Input
                className="min-w-0 flex-1"
                aria-label={kind === "header" ? t("Header value") : t("Parameter value")}
                placeholder={ov.remove ? t("Removed") : valuePlaceholder}
                value={ov.value ?? ""}
                disabled={Boolean(ov.remove)}
                spellCheck={false}
                onChange={(e) => update(i, { value: e.currentTarget.value })}
              />
              <label className="inline-flex shrink-0 items-center gap-1.5 text-xs text-fg-muted">
                <input
                  type="checkbox"
                  checked={Boolean(ov.remove)}
                  disabled={isProtectedParam}
                  onChange={(e) => update(i, { remove: e.currentTarget.checked || undefined })}
                />
                {t("Remove")}
              </label>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={
                  kind === "header" ? t("Delete header edit") : t("Delete parameter edit")
                }
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <Trash2 size={14} />
              </Button>
            </div>
          );
        })}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...value, { name: "", value: "" }])}
        >
          <Plus size={14} /> {addLabel}
        </Button>
      </div>
    </SettingsRow>
  );
}

/** Stacked drawer to add or edit a single identity rule. */
function RuleEditorDrawer({
  open,
  isNew,
  initialRule,
  allRules,
  editIndex,
  indexers,
  defaultUserAgent,
  saving,
  error,
  onClose,
  onSave,
  onRemove,
}: {
  open: boolean;
  isNew: boolean;
  initialRule: IdentityRule | null;
  allRules: IdentityRule[];
  editIndex: number;
  indexers: Indexer[];
  defaultUserAgent: string;
  saving: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (rule: IdentityRule) => void;
  onRemove?: () => void;
}) {
  const { locale } = useLocale();
  const [draft, setDraft] = useState<IdentityRule | null>(initialRule);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [fieldError, setFieldError] = useState<string>("");

  useEffect(() => {
    setDraft(initialRule ? structuredClone(initialRule) : null);
    setAdvancedOpen(
      Boolean(
        initialRule &&
        ((initialRule.headers?.length ?? 0) > 0 || (initialRule.params?.length ?? 0) > 0),
      ),
    );
    setFieldError("");
  }, [initialRule, open]);

  const indexerOptions = useMemo(
    () =>
      indexers
        .filter((ix) => !ix.kind || ix.kind === "newznab" || ix.kind === "zilean")
        .map((ix) => ({ value: ix.id, label: indexerDisplayName(ix) })),
    [indexers],
  );

  const targetOptions = useMemo(
    () => TARGET_KINDS.map((value) => ({ value, label: targetKindLabel(value) })),
    [locale],
  );

  const mediaTypeOptions = useMemo(
    () => MEDIA_TYPES.map((value) => ({ value, label: mediaTypeLabel(value) })),
    [locale],
  );

  const profileOptions = useMemo(
    () => PROFILE_IDS.map((value) => ({ value, label: identityProfileLabel(value) })),
    [locale],
  );

  const previewRules = useMemo(() => {
    if (!draft) return allRules;
    const cleaned: IdentityRule = {
      ...draft,
      headers: cleanOverrides(draft.headers),
      params: cleanOverrides(draft.params),
    };
    if (isNew) return [...allRules, cleaned];
    return allRules.map((r, i) => (i === editIndex ? cleaned : r));
  }, [allRules, draft, editIndex, isNew]);

  const set = (patch: Partial<IdentityRule>) =>
    setDraft((prev) => (prev ? { ...prev, ...patch } : prev));

  const customUA =
    draft?.headers?.find((h) => h.name.toLowerCase() === "user-agent" && !h.remove)?.value ?? "";

  const setCustomUA = (ua: string) => {
    if (!draft) return;
    const prev = draft.headers ?? [];
    const idx = prev.findIndex((h) => h.name.toLowerCase() === "user-agent");
    if (!ua.trim()) {
      set({ headers: idx >= 0 ? prev.filter((_, i) => i !== idx) : prev });
      return;
    }
    if (idx >= 0) {
      set({
        headers: prev.map((h, i) => (i === idx ? { name: "User-Agent", value: ua } : h)),
      });
    } else {
      set({ headers: [{ name: "User-Agent", value: ua }, ...prev] });
    }
  };

  function handleSave() {
    if (!draft) return;
    if (draft.target === "indexer" && !draft.indexerId) {
      setFieldError(t("Choose an indexer for this rule."));
      return;
    }
    if (draft.target === "host" && !validHostPattern(draft.host ?? "")) {
      setFieldError(t("Enter a host like api.nzbgeek.info or a wildcard like *.nzbgeek.info."));
      return;
    }
    const headerErr = validateOverrides(draft.headers, "header");
    if (headerErr) {
      setAdvancedOpen(true);
      setFieldError(headerErr);
      return;
    }
    const paramErr = validateOverrides(draft.params, "param");
    if (paramErr) {
      setAdvancedOpen(true);
      setFieldError(paramErr);
      return;
    }
    setFieldError("");
    const next: IdentityRule = {
      id: draft.id || newId("idrule"),
      target: draft.target,
      ...(draft.target === "indexer" ? { indexerId: draft.indexerId } : {}),
      ...(draft.target === "host" ? { host: draft.host?.trim().toLowerCase() } : {}),
      ...(draft.scope && (draft.target === "indexer" || draft.target === "host")
        ? { scope: draft.scope }
        : {}),
      ...(draft.mediaType ? { mediaType: draft.mediaType } : {}),
      profile: draft.profile || "ether",
      ...(cleanOverrides(draft.headers) ? { headers: cleanOverrides(draft.headers) } : {}),
      ...(cleanOverrides(draft.params) ? { params: cleanOverrides(draft.params) } : {}),
    };
    onSave(next);
  }

  const advCount = draft ? editCount(draft) : 0;

  return (
    <Drawer
      open={open}
      onClose={() => {
        if (!saving) onClose();
      }}
      title={isNew ? t("Add identity rule") : t("Edit identity rule")}
      description={t("Choose what this rule applies to and how Ether identifies itself.")}
      size="lg"
      footer={
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            {!isNew && onRemove ? (
              <Button
                type="button"
                variant="destructive"
                size="sm"
                onClick={onRemove}
                disabled={saving}
              >
                {t("Remove rule")}
              </Button>
            ) : null}
          </div>
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={onClose} disabled={saving}>
              {t("Cancel")}
            </Button>
            <Button type="button" size="sm" onClick={handleSave} disabled={saving || !draft}>
              {saving ? t("Saving…") : t("Save rule")}
            </Button>
          </div>
        </div>
      }
    >
      {draft ? (
        <div className="space-y-5">
          {error ? <MutationError error={error} /> : null}
          {fieldError ? (
            <p
              className="rounded-lg border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger"
              role="alert"
            >
              {fieldError}
            </p>
          ) : null}

          <SettingsSection
            title={t("Rule target")}
            description={t("More specific targets take priority over broader ones.")}
          >
            <SettingsRow
              title={t("Applies to")}
              description={
                draft.target === "nzb_downloads"
                  ? t("Applies to NZB downloads from indexers, feeds, and Stremio add-ons.")
                  : draft.target === "torrent_downloads"
                    ? t("Applies to torrent downloads from indexers, feeds, and Stremio add-ons.")
                    : draft.target === "all_searches"
                      ? t("Applies to Newznab and Torznab search and capability requests.")
                      : undefined
              }
              control={
                <div className="w-full sm:w-64">
                  <SearchSelect
                    {...searchSelectLabels()}
                    ariaLabel={t("Applies to")}
                    value={draft.target}
                    options={targetOptions}
                    onChange={(v) => {
                      const nextTarget = v as IdentityTargetKind;
                      set({
                        target: nextTarget,
                        indexerId:
                          nextTarget === "indexer"
                            ? draft.indexerId || indexerOptions[0]?.value || ""
                            : undefined,
                        host: nextTarget === "host" ? draft.host || "" : undefined,
                        scope:
                          nextTarget === "indexer" || nextTarget === "host"
                            ? draft.scope
                            : undefined,
                      });
                      setFieldError("");
                    }}
                  />
                </div>
              }
            />

            {draft.target === "indexer" ? (
              <SettingsRow
                title={t("Indexer")}
                description={
                  indexerOptions.length === 0
                    ? t("Add a Newznab, Torznab, or Zilean indexer first.")
                    : undefined
                }
                control={
                  <div className="w-full sm:w-64">
                    <SearchSelect
                      {...searchSelectLabels()}
                      ariaLabel={t("Indexer")}
                      value={draft.indexerId ?? ""}
                      options={indexerOptions}
                      searchable={indexerOptions.length > 6}
                      onChange={(v) => {
                        set({ indexerId: v });
                        setFieldError("");
                      }}
                    />
                  </div>
                }
              />
            ) : null}

            {draft.target === "host" ? (
              <SettingsRow
                title={t("Host pattern")}
                description={t(
                  "Exact host (api.nzbgeek.info) or wildcard subdomain (*.nzbgeek.info).",
                )}
                control={
                  <Input
                    aria-label={t("Host pattern")}
                    className="w-full sm:w-64"
                    placeholder="*.example.com"
                    value={draft.host ?? ""}
                    spellCheck={false}
                    onChange={(e) => {
                      set({ host: e.currentTarget.value });
                      setFieldError("");
                    }}
                  />
                }
              />
            ) : null}

            <SettingsRow
              title={t("Media type")}
              description={t(
                "Optionally limit this rule to one media type for searches and downloads. Music and Books apply to plugin kinds when known.",
              )}
              control={
                <div className="w-full sm:w-64">
                  <SearchSelect
                    {...searchSelectLabels()}
                    ariaLabel={t("Media type")}
                    value={draft.mediaType ?? ""}
                    options={mediaTypeOptions}
                    onChange={(v) => set({ mediaType: (v as IdentityMediaType) || undefined })}
                  />
                </div>
              }
            />
          </SettingsSection>

          <SettingsSection
            title={t("Identity")}
            description={t(
              "Compatibility profiles send that app's headers for indexers that only accept specific apps. Ether's request parameters stay unchanged.",
            )}
          >
            <SettingsRow
              title={t("Identify as")}
              control={
                <div className="w-full sm:w-72">
                  <SearchSelect
                    {...searchSelectLabels()}
                    ariaLabel={t("Identify as")}
                    value={draft.profile || "ether"}
                    options={profileOptions}
                    onChange={(v) => set({ profile: v })}
                  />
                </div>
              }
            />

            {draft.profile === "custom" ? (
              <SettingsRow
                title={t("User-Agent")}
                description={t("Leave empty to keep Ether's default user agent.")}
                control={
                  <Input
                    aria-label={t("User-Agent")}
                    className="w-full sm:w-72"
                    placeholder={defaultUserAgent}
                    value={customUA}
                    spellCheck={false}
                    onChange={(e) => setCustomUA(e.currentTarget.value)}
                  />
                }
              />
            ) : null}

            <div className="border-t border-border">
              <button
                type="button"
                className="flex w-full items-center justify-between gap-2 px-4 py-3 text-left text-sm font-medium text-primary hover:bg-bg-subtle/40"
                aria-expanded={advancedOpen}
                onClick={() => setAdvancedOpen((v) => !v)}
              >
                <span className="inline-flex items-center gap-2">
                  {advancedOpen ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
                  <SlidersHorizontal size={14} />
                  {t("Advanced")}
                  {advCount > 0 ? (
                    <Badge variant="secondary">
                      {tn(advCount, "{count} edit", "{count} edits")}
                    </Badge>
                  ) : null}
                </span>
                <span className="text-xs font-normal text-fg-subtle">
                  {t("Headers & query parameters")}
                </span>
              </button>

              {advancedOpen ? (
                <div className="divide-y divide-border border-t border-border bg-bg-subtle/20">
                  <OverrideList
                    title={t("Header edits")}
                    help={t(
                      "Add, replace, or remove headers on top of the selected identity. Sensitive header values are sealed at rest and masked in previews.",
                    )}
                    value={draft.headers ?? []}
                    onChange={(headers) => set({ headers })}
                    kind="header"
                    namePlaceholder="X-Custom-Header"
                    valuePlaceholder={t("Value")}
                    addLabel={t("Add header edit")}
                  />
                  <OverrideList
                    title={t("Query parameter edits")}
                    help={t(
                      "Add, replace, or remove query parameters. apikey and t cannot be removed, and secret values are masked.",
                    )}
                    value={draft.params ?? []}
                    onChange={(params) => set({ params })}
                    kind="param"
                    namePlaceholder="limit"
                    valuePlaceholder={t("Value")}
                    addLabel={t("Add parameter edit")}
                  />
                </div>
              ) : null}
            </div>
          </SettingsSection>

          <div className="rounded-xl border border-border bg-surface p-4">
            <RulePreviewPanel
              rule={{
                ...draft,
                headers: cleanOverrides(draft.headers),
                params: cleanOverrides(draft.params),
              }}
              rules={previewRules}
            />
          </div>
        </div>
      ) : null}
    </Drawer>
  );
}

/** Compact "How Ether identifies itself" card on Sources > Indexers. */
export function IdentityRulesCard({
  rules,
  defaultUserAgent,
  onOpen,
}: {
  rules: IdentityRule[];
  defaultUserAgent: string;
  onOpen: () => void;
}) {
  return (
    <article
      aria-labelledby="identity-rules-card-heading"
      className="flex flex-col gap-3 rounded-xl border border-border bg-surface p-4 sm:flex-row sm:items-center sm:justify-between sm:px-5 sm:py-4"
    >
      <div className="flex min-w-0 items-start gap-3">
        <span
          aria-hidden
          className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary"
        >
          <ShieldCheck size={20} />
        </span>
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 id="identity-rules-card-heading" className="font-semibold text-fg">
              {t("How Ether identifies itself")}
            </h3>
            <Badge variant="success">{t("Ether (honest, recommended)")}</Badge>
            <Badge variant={rules.length > 0 ? "secondary" : "outline"}>
              {rules.length === 0
                ? t("No rules")
                : tn(rules.length, "{count} rule", "{count} rules")}
            </Badge>
          </div>
          <p className="text-sm text-fg-muted">
            {t(
              "Ether identifies honestly by default. Add compatibility rules for indexers that only accept specific apps.",
            )}
          </p>
          {defaultUserAgent ? (
            <p className="truncate font-mono text-xs text-fg-subtle" title={defaultUserAgent}>
              {defaultUserAgent}
            </p>
          ) : null}
        </div>
      </div>
      <div className="flex shrink-0 items-center">
        <Button type="button" variant="outline" size="sm" onClick={onOpen}>
          {t("Manage rules")}
        </Button>
      </div>
    </article>
  );
}

function describeEffectiveIdentity(
  rules: IdentityRule[],
  indexer: Pick<Indexer, "id" | "url" | "protocol">,
  kind: "search" | "nzb_download" | "torrent_download",
  indexers: Indexer[],
) {
  const base = resolveRuleForIndexer(rules, indexer, kind, "");
  const byMedia = SPECIFIC_MEDIA_TYPES.map((mt) => ({
    mt,
    res: resolveRuleForIndexer(rules, indexer, kind, mt),
  }));
  const overrides = byMedia.filter(
    ({ res }) => res.rule?.id !== base.rule?.id || res.profile !== base.profile,
  );
  const signature = [
    base.rule?.id ?? "",
    base.profile,
    ...byMedia.map(({ res }) => `${res.rule?.id ?? ""}:${res.profile}`),
  ].join("|");
  if (overrides.length === 0) {
    return {
      split: false as const,
      signature,
      identity: identityShortLabel(base.profile, base.rule?.headers),
      source: base.rule ? ruleTargetSummary(base.rule, indexers) : t("Default"),
    };
  }
  const parts = overrides.map(
    ({ mt, res }) => `${mediaTypeBadge(mt)}: ${identityShortLabel(res.profile, res.rule?.headers)}`,
  );
  if (base.rule) {
    parts.push(`${t("Other")}: ${identityShortLabel(base.profile, base.rule.headers)}`);
  }
  return {
    split: true as const,
    signature,
    summary: parts.join(" · "),
  };
}

/** Read-only summary row inside each indexer's drawer, linking to the rules drawer. */
export function IndexerIdentityRow({
  indexer,
  rules,
  indexers,
  onManage,
}: {
  indexer: Indexer;
  rules: IdentityRule[];
  indexers: Indexer[];
  onManage: () => void;
}) {
  const isTorrent = indexer.protocol === "torrent";
  const downloadOnly = indexer.kind === "stremio" || indexer.kind === "cardigann";
  const searchDesc = describeEffectiveIdentity(rules, indexer, "search", indexers);
  const dlDesc = describeEffectiveIdentity(
    rules,
    indexer,
    isTorrent ? "torrent_download" : "nzb_download",
    indexers,
  );
  const primary = downloadOnly ? dlDesc : searchDesc;
  const showSeparateDownload = !downloadOnly && dlDesc.signature !== searchDesc.signature;

  const title = primary.split
    ? t("Identifies by media type: {summary}", { summary: primary.summary })
    : t("Identifies as: {identity} (from rule: {rule})", {
        identity: primary.identity,
        rule: primary.source,
      });

  const description = downloadOnly
    ? t("Applies to NZB and torrent downloads from this source.")
    : showSeparateDownload
      ? dlDesc.split
        ? t("Downloads identify by media type: {summary}", { summary: dlDesc.summary })
        : t("Downloads identify as: {identity} (from rule: {rule})", {
            identity: dlDesc.identity,
            rule: dlDesc.source,
          })
      : t("Managed by your server-wide identity rules.");

  return (
    <SettingsSection title={t("Request identity")}>
      <SettingsRow
        title={title}
        description={description}
        control={
          <Button type="button" variant="link" size="sm" onClick={onManage} className="px-0">
            {t("Manage rules")}
          </Button>
        }
      />
    </SettingsSection>
  );
}

/** Main drawer for managing ordered request-identity rules, previews, and caps testing. */
export function IdentityRulesDrawer({
  open,
  stacked,
  rules,
  indexers,
  defaultUserAgent,
  onClose,
  onSaved,
}: {
  open: boolean;
  stacked?: boolean;
  rules: IdentityRule[];
  indexers: Indexer[];
  defaultUserAgent: string;
  onClose: () => void;
  onSaved: () => Promise<void> | void;
}) {
  const [editing, setEditing] = useState<{ index: number; rule: IdentityRule } | null>(null);
  const [openPreviews, setOpenPreviews] = useState<Record<string, boolean>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const testableIndexers = useMemo(
    () => indexers.filter((ix) => !ix.kind || ix.kind === "newznab" || ix.kind === "zilean"),
    [indexers],
  );
  const [testIndexerId, setTestIndexerId] = useState<string>("");
  const [testState, setTestState] = useState<TestState>({ status: "idle" });

  useEffect(() => {
    if (!testableIndexers.length) {
      setTestIndexerId("");
      return;
    }
    if (!testableIndexers.some((ix) => ix.id === testIndexerId)) {
      setTestIndexerId(testableIndexers[0]!.id);
    }
  }, [testableIndexers, testIndexerId]);

  const togglePreview = (id: string) => setOpenPreviews((prev) => ({ ...prev, [id]: !prev[id] }));

  async function commitRules(next: IdentityRule[], message: string) {
    setSaving(true);
    setError(null);
    try {
      await api.putSection("clientIdentity", { rules: next });
      toast.success(message);
      await onSaved();
      return true;
    } catch (e) {
      setError(e);
      toastError(t("Couldn't save identity rules"), e);
      return false;
    } finally {
      setSaving(false);
    }
  }

  function openAdd() {
    setError(null);
    const defaultTarget: IdentityTargetKind =
      testableIndexers.length > 0 ? "indexer" : "nzb_downloads";
    setEditing({
      index: rules.length,
      rule: {
        id: newId("idrule"),
        target: defaultTarget,
        ...(defaultTarget === "indexer" ? { indexerId: testableIndexers[0]?.id } : {}),
        profile: "sonarr",
      },
    });
  }

  async function saveEditedRule(rule: IdentityRule) {
    if (!editing) return;
    const isNew = editing.index >= rules.length;
    const next = isNew ? [...rules, rule] : rules.map((r, i) => (i === editing.index ? rule : r));
    const ok = await commitRules(next, isNew ? t("Identity rule added") : t("Identity rule saved"));
    if (ok) setEditing(null);
  }

  async function deleteRule(index: number) {
    const target = rules[index];
    if (!target) return;
    const ok = await confirmDialog({
      title: t("Remove identity rule?"),
      message: t("Requests matching {target} will fall back to the next matching rule or Ether.", {
        target: ruleTargetSummary(target, indexers),
      }),
      confirmLabel: t("Remove"),
      tone: "danger",
    });
    if (!ok) return;
    const saved = await commitRules(
      rules.filter((_, i) => i !== index),
      t("Identity rule removed"),
    );
    if (saved && editing?.index === index) setEditing(null);
  }

  async function moveRule(index: number, delta: -1 | 1) {
    const next = swap(rules, index, index + delta);
    await commitRules(next, t("Rule order saved"));
  }

  async function runCapsTest() {
    if (!testIndexerId) return;
    setTestState({ status: "running" });
    try {
      const res = await api.testIndexerIdentity(testIndexerId, rules);
      setTestState(
        res.ok
          ? {
              status: "ok",
              durationMs: res.durationMs,
              message: t("Capabilities answered using {identity}.", {
                identity: identityShortLabel(res.profile ?? "ether"),
              }),
            }
          : { status: "fail", message: res.error ?? t("failed") },
      );
    } catch (e) {
      setTestState({ status: "fail", message: errorMessage(e) });
    }
  }

  const testOptions = useMemo(
    () => testableIndexers.map((ix) => ({ value: ix.id, label: indexerDisplayName(ix) })),
    [testableIndexers],
  );

  return (
    <>
      <Drawer
        open={open}
        stacked={stacked}
        onClose={() => {
          if (!saving) onClose();
        }}
        title={t("How Ether identifies itself")}
        description={t(
          "Default is honest Ether. Add compatibility rules for indexers that only accept specific apps.",
        )}
        size="lg"
      >
        <div className="space-y-5">
          {error && !editing ? <MutationError error={error} /> : null}

          <div className="space-y-3">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0 flex-1 space-y-0.5">
                <h3 className="text-sm font-semibold text-fg">{t("Identity rules")}</h3>
                <p className="text-xs text-fg-muted">
                  {t(
                    "The most specific rule wins: indexer, exact host, wildcard host, then download type or all searches (a media type beats Any in the same tier). Drag to break ties.",
                  )}
                </p>
              </div>
              <Button
                type="button"
                size="sm"
                className="shrink-0"
                onClick={openAdd}
                disabled={saving}
              >
                <Plus size={14} /> {t("Add rule")}
              </Button>
            </div>

            {rules.length > 0 ? (
              <SortableList
                items={rules}
                getId={(r) => r.id}
                className="space-y-2.5"
                onReorder={(next) => void commitRules(next, t("Rule order saved"))}
                renderItem={(rule, handle, i) => {
                  const baseTargetName = ruleTargetBaseSummary(rule, indexers);
                  const targetName = ruleTargetSummary(rule, indexers);
                  const mediaLabel = mediaTypeBadge(rule.mediaType);
                  const idLabel = identityShortLabel(rule.profile, rule.headers);
                  const edits = editCount(rule);
                  const previewOpen = Boolean(openPreviews[rule.id]);
                  return (
                    <article className="rounded-xl border border-border bg-surface p-3.5">
                      <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-2 sm:gap-y-1">
                        <div className="col-start-1 row-start-1 flex shrink-0 items-center sm:row-span-2 sm:self-start sm:pt-0.5">
                          <DragHandle
                            handle={handle}
                            label={t("Drag to reorder {name}", { name: targetName })}
                          />
                        </div>
                        <div className="col-start-2 row-start-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                          <span className="text-xs font-medium tabular-nums text-fg-subtle">
                            {i + 1}
                          </span>
                          <h4 className="min-w-0 break-words font-semibold text-fg">
                            {baseTargetName}
                          </h4>
                          <Badge
                            variant="outline"
                            className="hidden whitespace-nowrap sm:inline-flex"
                          >
                            {targetKindBadge(rule.target)}
                          </Badge>
                          {mediaLabel ? (
                            <Badge
                              variant="outline"
                              className="hidden whitespace-nowrap sm:inline-flex"
                            >
                              {mediaLabel}
                            </Badge>
                          ) : null}
                        </div>
                        <div className="col-start-3 row-start-1 flex shrink-0 items-center gap-1 sm:row-span-2 sm:self-start">
                          <OrderButtons
                            label={targetName}
                            i={i}
                            n={rules.length}
                            onMove={(d) => void moveRule(i, d)}
                          />
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            aria-label={t("Edit {name}", { name: targetName })}
                            onClick={() => {
                              setError(null);
                              setEditing({ index: i, rule: structuredClone(rule) });
                            }}
                          >
                            <Pencil size={13} />
                            <span className="hidden sm:inline">{t("Edit")}</span>
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            aria-label={t("Remove {name}", { name: targetName })}
                            onClick={() => void deleteRule(i)}
                          >
                            <Trash2 size={14} />
                          </Button>
                        </div>
                        <div className="col-span-full row-start-2 flex min-w-0 flex-wrap items-center gap-1.5 text-xs text-fg-muted sm:col-span-1 sm:col-start-2">
                          <Badge variant="outline" className="whitespace-nowrap sm:hidden">
                            {targetKindBadge(rule.target)}
                          </Badge>
                          {mediaLabel ? (
                            <Badge variant="outline" className="whitespace-nowrap sm:hidden">
                              {mediaLabel}
                            </Badge>
                          ) : null}
                          <span>{t("Identifies as")}</span>
                          <Badge
                            variant={rule.profile === "ether" ? "success" : "secondary"}
                            className="whitespace-nowrap"
                          >
                            {idLabel}
                          </Badge>
                          {edits > 0 ? (
                            <Badge variant="outline" className="whitespace-nowrap">
                              {tn(edits, "+{count} edit", "+{count} edits")}
                            </Badge>
                          ) : null}
                        </div>
                      </div>

                      <div className="mt-2.5 border-t border-border/70 pt-2">
                        <button
                          type="button"
                          className="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline"
                          aria-expanded={previewOpen}
                          onClick={() => togglePreview(rule.id)}
                        >
                          <Eye size={13} />
                          {previewOpen
                            ? t("Hide what the indexer sees")
                            : t("What the indexer sees")}
                        </button>
                        {previewOpen ? (
                          <div className="mt-3">
                            <RulePreviewPanel rule={rule} rules={rules} compact />
                          </div>
                        ) : null}
                      </div>
                    </article>
                  );
                }}
              />
            ) : null}

            {/* Implicit fallback rule: always honest Ether */}
            <article className="rounded-xl border border-dashed border-border bg-bg-subtle/40 p-3.5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="success">{t("Default")}</Badge>
                    <span className="font-semibold text-fg">
                      {t("Ether (honest, recommended)")}
                    </span>
                  </div>
                  <p className="text-xs text-fg-muted">
                    {t("Used automatically for every request that does not match a rule above.")}
                  </p>
                  {defaultUserAgent ? (
                    <p className="break-all font-mono text-xs text-fg-subtle">{defaultUserAgent}</p>
                  ) : null}
                </div>
                <button
                  type="button"
                  className="inline-flex shrink-0 items-center gap-1.5 text-xs font-medium text-primary hover:underline"
                  aria-expanded={Boolean(openPreviews.__default__)}
                  onClick={() => togglePreview("__default__")}
                >
                  <Eye size={13} />
                  {openPreviews.__default__
                    ? t("Hide what the indexer sees")
                    : t("What the indexer sees")}
                </button>
              </div>
              {openPreviews.__default__ ? (
                <div className="mt-3 border-t border-border/70 pt-3">
                  <RulePreviewPanel
                    rule={{ id: "__default__", target: "all_searches", profile: "ether" }}
                    rules={[]}
                    compact
                  />
                </div>
              ) : null}
            </article>
          </div>

          {testOptions.length > 0 ? (
            <SettingsSection
              title={t("Test with an indexer")}
              description={t(
                "Runs a capabilities request (t=caps) through the resolved identity for a chosen indexer.",
              )}
            >
              <SettingsRow stacked title={t("Choose an indexer")}>
                <div className="flex flex-wrap items-center gap-2.5">
                  <div className="w-full shrink-0 sm:w-56">
                    <SearchSelect
                      {...searchSelectLabels()}
                      ariaLabel={t("Indexer to test")}
                      value={testIndexerId}
                      options={testOptions}
                      onChange={(v) => {
                        setTestIndexerId(v);
                        setTestState({ status: "idle" });
                      }}
                    />
                  </div>
                  <TestButton
                    state={testState}
                    onTest={() => void runCapsTest()}
                    disabled={!testIndexerId}
                  />
                </div>
              </SettingsRow>
            </SettingsSection>
          ) : null}
        </div>
      </Drawer>

      <RuleEditorDrawer
        open={editing !== null}
        isNew={editing ? editing.index >= rules.length : true}
        initialRule={editing?.rule ?? null}
        allRules={rules}
        editIndex={editing?.index ?? rules.length}
        indexers={indexers}
        defaultUserAgent={defaultUserAgent}
        saving={saving}
        error={error}
        onClose={() => setEditing(null)}
        onSave={(rule) => void saveEditedRule(rule)}
        onRemove={
          editing && editing.index < rules.length ? () => void deleteRule(editing.index) : undefined
        }
      />
    </>
  );
}
