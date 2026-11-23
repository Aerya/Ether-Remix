import { Badge, Button, Input, SearchSelect, cn } from "@braedonsaunders/appkit-ui";
import { Plus, Save, Trash2, X } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { Field, Segmented } from "../../components/fields";
import { t, tn } from "../../i18n";
import { ApiError, errorMessage, isAbort, uuid } from "../../lib/api";
import { formatNumber } from "../../lib/format";
import { UnratedChannelRating } from "./UnratedChannelRating";
import {
  isChannelSource,
  tv,
  type ChannelSummary,
  type OwnerGroup,
  type OwnerGroupMatch,
  type OwnerGroupPreview,
  type OwnerGroupRule,
  type OwnerGroupRuleField,
  type OwnerGroupRuleOp,
  type ProviderGroupRef,
  type Source,
} from "./model";

export function extractRuleErrors(err: unknown, rules: OwnerGroupRule[]): Record<number, string> {
  const out: Record<number, string> = {};
  if (err instanceof ApiError && err.fields?.length) {
    for (const f of err.fields) {
      const m = /(?:^|\.)rules\.(\d+)\.value$/.exec(f.field);
      if (m) out[Number(m[1])] = f.message;
    }
  }
  if (Object.keys(out).length === 0) {
    const idx = rules.findIndex(
      (r) => (r.op === "matches" || r.op === "notMatches") && r.value !== "",
    );
    if (idx >= 0) out[idx] = errorMessage(err);
  }
  return out;
}

export function MatchRuleRows({
  rules,
  ruleErrors,
  onUpdate,
  onRemove,
  onAdd,
  regexPlaceholder = "^(cbs\\.wcbs|espn\\.us)$",
}: {
  rules: OwnerGroupRule[];
  ruleErrors: Record<number, string>;
  onUpdate: (idx: number, patch: Partial<OwnerGroupRule>) => void;
  onRemove: (idx: number) => void;
  onAdd: () => void;
  regexPlaceholder?: string;
}) {
  const fieldOptions: { value: OwnerGroupRuleField; label: string }[] = [
    { value: "name", label: t("Channel name") },
    { value: "group", label: t("Provider group") },
    { value: "guideId", label: t("Guide ID") },
    { value: "number", label: t("Channel number") },
    { value: "source", label: t("Source") },
  ];

  const opOptions: { value: OwnerGroupRuleOp; label: string }[] = [
    { value: "contains", label: t("contains") },
    { value: "is", label: t("is") },
    { value: "startsWith", label: t("starts with") },
    { value: "matches", label: t("matches pattern") },
    { value: "notContains", label: t("doesn't contain") },
    { value: "notMatches", label: t("doesn't match pattern") },
  ];

  return (
    <>
      {rules.length > 0 ? (
        <div className="space-y-2.5">
          {rules.map((rule, idx) => {
            const isRegex = rule.op === "matches" || rule.op === "notMatches";
            const err = ruleErrors[idx];
            return (
              <div
                key={idx}
                className="space-y-1.5 rounded-lg border border-border bg-bg-subtle/50 p-2.5"
              >
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <div className="sm:flex-1">
                    <SearchSelect
                      ariaLabel={t("Rule field")}
                      value={rule.field}
                      options={fieldOptions}
                      searchable={false}
                      selectLabel={t("Select")}
                      closeLabel={t("Close")}
                      onChange={(val) => onUpdate(idx, { field: val as OwnerGroupRuleField })}
                    />
                  </div>
                  <div className="sm:flex-1">
                    <SearchSelect
                      ariaLabel={t("Rule condition")}
                      value={rule.op}
                      options={opOptions}
                      searchable={false}
                      selectLabel={t("Select")}
                      closeLabel={t("Close")}
                      onChange={(val) => onUpdate(idx, { op: val as OwnerGroupRuleOp })}
                    />
                  </div>
                </div>
                <div className="flex min-w-0 items-center gap-1.5">
                  <Input
                    aria-label={t("Rule value")}
                    aria-invalid={Boolean(err) || undefined}
                    value={rule.value}
                    spellCheck={false}
                    placeholder={isRegex ? regexPlaceholder : t("Value")}
                    className={cn("min-w-0 flex-1", isRegex && "font-mono text-xs")}
                    onChange={(e) => onUpdate(idx, { value: e.currentTarget.value })}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={t("Remove rule")}
                    className="shrink-0 text-fg-muted hover:text-danger"
                    onClick={() => onRemove(idx)}
                  >
                    <Trash2 size={14} />
                  </Button>
                </div>
                {err ? (
                  <p className="text-xs text-danger" role="alert">
                    {err}
                  </p>
                ) : isRegex ? (
                  <p className="text-xs text-fg-muted">
                    {t("Regular expression, case-insensitive by default.")}
                  </p>
                ) : null}
              </div>
            );
          })}
        </div>
      ) : null}

      <Button type="button" variant="outline" size="sm" onClick={onAdd}>
        <Plus size={14} /> {t("Add rule")}
      </Button>
    </>
  );
}

export function OwnerGroupDrawer({
  open,
  group,
  ownerGroups,
  sources,
  channels,
  onClose,
  onSave,
  onDelete,
}: {
  open: boolean;
  group: OwnerGroup | null;
  ownerGroups: OwnerGroup[];
  sources: Source[];
  channels: ChannelSummary[];
  onClose: () => void;
  onSave: (nextGroups: OwnerGroup[], savedGroup: OwnerGroup) => Promise<boolean>;
  onDelete?: (group: OwnerGroup) => Promise<void>;
}) {
  const formId = useId();
  const [name, setName] = useState("");
  const [nameError, setNameError] = useState("");
  const [rules, setRules] = useState<OwnerGroupRule[]>([]);
  const [match, setMatch] = useState<OwnerGroupMatch>("any");
  const [providerGroups, setProviderGroups] = useState<ProviderGroupRef[]>([]);
  const [explicitChannels, setExplicitChannels] = useState<string[]>([]);
  const [unratedRating, setUnratedRating] = useState("");
  const [ruleErrors, setRuleErrors] = useState<Record<number, string>>({});
  const [preview, setPreview] = useState<OwnerGroupPreview | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [validatedKey, setValidatedKey] = useState("");
  const [saving, setSaving] = useState(false);
  const initialPreviewRef = useRef(true);

  const targetKey = open ? (group?.id ?? "__new__") : "";

  useEffect(() => {
    if (!open) return;
    setName(group?.name ?? "");
    setNameError("");
    setRules((group?.rules ?? []).map((r) => ({ ...r })));
    setMatch(group?.match === "all" ? "all" : "any");
    setProviderGroups((group?.groups ?? []).map((g) => ({ ...g })));
    setExplicitChannels([...(group?.channels ?? [])]);
    setUnratedRating(group?.unratedRating ?? "");
    setRuleErrors({});
    setPreview(null);
    setValidatedKey("");
    initialPreviewRef.current = true;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, targetKey]);

  const sourceNameById = useMemo(() => {
    const m = new Map<string, string>();
    for (const s of sources) m.set(s.id, s.name);
    return m;
  }, [sources]);

  const availableProviderGroups = useMemo(() => {
    const counts = new Map<string, { sourceId: string; name: string; count: number }>();
    for (const c of channels) {
      if (c.kind === "virtual" || !c.sourceId || !c.group) continue;
      const k = `${c.sourceId}\u0000${c.group}`;
      const existing = counts.get(k);
      if (existing) existing.count++;
      else counts.set(k, { sourceId: c.sourceId, name: c.group, count: 1 });
    }
    for (const s of sources) {
      if (!isChannelSource(s)) continue;
      for (const sg of s.groups ?? []) {
        if (sg.excluded || !sg.name) continue;
        const k = `${s.id}\u0000${sg.name}`;
        if (!counts.has(k)) {
          counts.set(k, { sourceId: s.id, name: sg.name, count: sg.channels });
        }
      }
    }
    const picked = new Set(providerGroups.map((g) => `${g.sourceId}\u0000${g.name}`));
    return [...counts.values()]
      .filter((g) => !picked.has(`${g.sourceId}\u0000${g.name}`))
      .sort(
        (a, b) =>
          (sourceNameById.get(a.sourceId) ?? a.sourceId).localeCompare(
            sourceNameById.get(b.sourceId) ?? b.sourceId,
          ) || a.name.localeCompare(b.name),
      );
  }, [channels, sources, providerGroups, sourceNameById]);

  const previewPayload = useMemo(
    () => ({
      id: group?.id ?? "",
      name: name.trim() || "Preview",
      groups: providerGroups,
      channels: explicitChannels,
      all: Boolean(group?.all),
      match,
      rules,
    }),
    [group?.id, group?.all, name, providerGroups, explicitChannels, match, rules],
  );

  const previewKey = useMemo(() => JSON.stringify(previewPayload), [previewPayload]);

  useEffect(() => {
    if (!open) return;
    const ctl = new AbortController();
    const delay = initialPreviewRef.current ? 0 : 180;
    initialPreviewRef.current = false;
    setPreviewLoading(true);
    const timer = window.setTimeout(() => {
      tv.previewGroup(previewPayload, ctl.signal)
        .then((res) => {
          if (ctl.signal.aborted) return;
          setPreview({
            count: typeof res?.count === "number" ? res.count : 0,
            channels: Array.isArray(res?.channels) ? res.channels : [],
          });
          setRuleErrors({});
          setValidatedKey(previewKey);
          setPreviewLoading(false);
        })
        .catch((err) => {
          if (ctl.signal.aborted || isAbort(err)) return;
          setRuleErrors(extractRuleErrors(err, rules));
          setValidatedKey(previewKey);
          setPreviewLoading(false);
        });
    }, delay);
    return () => {
      window.clearTimeout(timer);
      ctl.abort();
    };
  }, [open, previewKey, previewPayload, rules]);

  const updateRule = (idx: number, patch: Partial<OwnerGroupRule>) => {
    setRules((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r)));
    setRuleErrors((prev) => {
      if (!(idx in prev)) return prev;
      const next = { ...prev };
      delete next[idx];
      return next;
    });
  };

  const removeRule = (idx: number) => {
    setRules((prev) => prev.filter((_, i) => i !== idx));
    setRuleErrors({});
  };

  const addRule = () => {
    setRules((prev) => [...prev, { field: "name", op: "contains", value: "" }]);
  };

  const hasRuleErrors = Object.keys(ruleErrors).length > 0;

  const handleSave = async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      setNameError(t("Group name is required."));
      return;
    }
    if (
      ownerGroups.some((g) => g.id !== group?.id && g.name.toLowerCase() === trimmed.toLowerCase())
    ) {
      setNameError(t("A group with that name already exists."));
      return;
    }
    if (hasRuleErrors || saving) return;

    const activeRules = rules.filter((r) => r.value !== "");
    setSaving(true);
    try {
      if (
        validatedKey !== previewKey &&
        activeRules.some((r) => r.op === "matches" || r.op === "notMatches")
      ) {
        try {
          await tv.previewGroup({
            ...previewPayload,
            name: trimmed,
            rules: activeRules,
          });
        } catch (err) {
          setRuleErrors(extractRuleErrors(err, rules));
          return;
        }
      }

      const savedGroup: OwnerGroup = {
        ...(group ?? { id: uuid(), groups: [] }),
        name: trimmed,
        groups: providerGroups,
        channels: explicitChannels.length ? explicitChannels : undefined,
        rules: activeRules.length ? activeRules : undefined,
        match: activeRules.length ? match : undefined,
        unratedRating: unratedRating || undefined,
      };
      const nextGroups = group
        ? ownerGroups.map((g) => (g.id === group.id ? savedGroup : g))
        : [...ownerGroups, savedGroup];

      const ok = await onSave(nextGroups, savedGroup);
      if (ok) onClose();
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer
      open={open}
      onClose={onClose}
      size="lg"
      title={group ? group.name || t("Edit group") : t("New group")}
      description={t("Group channels by rules, provider groups, or hand-picked channels.")}
      footer={
        <div className="flex w-full flex-wrap items-center gap-2">
          {group && onDelete ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={saving}
              className="text-danger hover:text-danger"
              onClick={async () => {
                onClose();
                await onDelete(group);
              }}
            >
              <Trash2 size={14} /> {t("Delete")}
            </Button>
          ) : null}
          <div className="ml-auto flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" disabled={saving} onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={saving || hasRuleErrors}
              onClick={() => void handleSave()}
            >
              <Save size={14} />{" "}
              {saving ? t("Saving…") : group ? t("Save group") : t("Create group")}
            </Button>
          </div>
        </div>
      }
    >
      <div className="space-y-6">
        <Field label={t("Group name")} htmlFor={`${formId}-name`} error={nameError}>
          <Input
            id={`${formId}-name`}
            aria-label={t("Group name")}
            value={name}
            maxLength={150}
            placeholder={t("e.g. US Sports")}
            onChange={(e) => {
              setName(e.currentTarget.value);
              if (nameError) setNameError("");
            }}
          />
        </Field>

        {/* Channels that match (rule-based membership) */}
        <section className="space-y-3" aria-labelledby={`${formId}-rules-heading`}>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="space-y-0.5">
              <h3 id={`${formId}-rules-heading`} className="text-sm font-semibold text-fg">
                {t("Channels that match")}
              </h3>
              <p className="text-xs text-fg-muted">
                {t("New channels from your providers join automatically when they match.")}
              </p>
            </div>
            {rules.length > 0 ? (
              <Segmented
                ariaLabel={t("Match mode")}
                size="sm"
                value={match}
                onChange={setMatch}
                options={[
                  { value: "any", label: t("Any rule") },
                  { value: "all", label: t("All rules") },
                ]}
              />
            ) : null}
          </div>

          <MatchRuleRows
            rules={rules}
            ruleErrors={ruleErrors}
            onUpdate={updateRule}
            onRemove={removeRule}
            onAdd={addRule}
          />
        </section>

        {/* Provider groups & hand-picked channels */}
        {(providerGroups.length > 0 ||
          availableProviderGroups.length > 0 ||
          explicitChannels.length > 0) && (
          <section className="space-y-2.5 border-t border-border pt-4">
            <div className="space-y-0.5">
              <h3 className="text-sm font-semibold text-fg">{t("Included provider groups")}</h3>
              <p className="text-xs text-fg-muted">
                {t(
                  "Whole groups from your IPTV sources combine with rules above; exclude rules still remove unwanted channels.",
                )}
              </p>
            </div>

            {providerGroups.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {providerGroups.map((pg) => {
                  const sname = sourceNameById.get(pg.sourceId) ?? pg.sourceName ?? pg.sourceId;
                  return (
                    <span
                      key={`${pg.sourceId}\u0000${pg.name}`}
                      className="inline-flex items-center gap-1 rounded-full border border-border bg-surface px-2.5 py-1 text-xs text-fg"
                    >
                      <span className="truncate">
                        {sname} › {pg.name}
                      </span>
                      <button
                        type="button"
                        aria-label={t("Remove {name}", { name: pg.name })}
                        className="rounded-full p-0.5 text-fg-muted hover:bg-surface-hover hover:text-fg"
                        onClick={() =>
                          setProviderGroups((prev) =>
                            prev.filter((x) => !(x.sourceId === pg.sourceId && x.name === pg.name)),
                          )
                        }
                      >
                        <X size={12} />
                      </button>
                    </span>
                  );
                })}
              </div>
            ) : null}

            {availableProviderGroups.length > 0 ? (
              <div className="max-w-sm">
                <SearchSelect
                  value=""
                  onChange={(val) => {
                    if (!val) return;
                    const sep = val.indexOf("\u0000");
                    if (sep < 0) return;
                    const sourceId = val.slice(0, sep);
                    const gname = val.slice(sep + 1);
                    setProviderGroups((prev) => [...prev, { sourceId, name: gname }]);
                  }}
                  options={availableProviderGroups.map((g) => ({
                    value: `${g.sourceId}\u0000${g.name}`,
                    label: `${sourceNameById.get(g.sourceId) ?? g.sourceId} › ${g.name} (${formatNumber(g.count)})`,
                  }))}
                  ariaLabel={t("Add provider group")}
                  placeholder={t("Add provider group…")}
                  selectLabel={t("Select")}
                  closeLabel={t("Close")}
                />
              </div>
            ) : null}

            {explicitChannels.length > 0 ? (
              <div className="flex items-center justify-between rounded-lg border border-border bg-bg-subtle/50 px-3 py-2 text-xs text-fg-muted">
                <span>
                  {tn(
                    explicitChannels.length,
                    "{count} hand-picked channel included",
                    "{count} hand-picked channels included",
                  )}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setExplicitChannels([])}
                >
                  {t("Clear")}
                </Button>
              </div>
            ) : null}
          </section>
        )}

        {/* Unrated fallback rating */}
        <div className="border-t border-border pt-4">
          <UnratedChannelRating value={unratedRating} onChange={setUnratedRating} />
        </div>

        {/* Live preview */}
        <section
          className="space-y-2.5 border-t border-border pt-4"
          aria-labelledby={`${formId}-preview-heading`}
        >
          <div className="flex items-center justify-between gap-2">
            <h3 id={`${formId}-preview-heading`} className="text-sm font-semibold text-fg">
              {t("Matching channels")}
            </h3>
            <Badge>
              {previewLoading && !preview
                ? t("Checking…")
                : tn(preview?.count ?? 0, "{count} channel", "{count} channels")}
            </Badge>
          </div>

          {!preview || preview.channels.length === 0 ? (
            <p className="rounded-lg border border-dashed border-border px-3 py-4 text-center text-xs text-fg-muted">
              {previewLoading ? t("Checking…") : t("No channels match yet.")}
            </p>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-surface">
              <ul className="max-h-56 divide-y divide-border overflow-y-auto text-xs">
                {preview.channels.map((c) => {
                  const sub = [c.group, c.guideId].filter(Boolean).join(" · ");
                  return (
                    <li key={c.id} className="flex items-center gap-2.5 px-3 py-2">
                      <span className="w-10 shrink-0 truncate tabular-nums text-fg-muted">
                        {c.number}
                      </span>
                      <span className="min-w-0 flex-1 truncate font-medium text-fg">{c.name}</span>
                      {sub ? (
                        <span className="max-w-[45%] shrink-0 truncate text-fg-muted">{sub}</span>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
              {preview.count > preview.channels.length ? (
                <p className="border-t border-border bg-bg-subtle/60 px-3 py-1.5 text-[11px] text-fg-muted">
                  {t("Showing first {shown} of {total} channels.", {
                    shown: formatNumber(preview.channels.length),
                    total: formatNumber(preview.count),
                  })}
                </p>
              ) : null}
            </div>
          )}
        </section>
      </div>
    </Drawer>
  );
}
