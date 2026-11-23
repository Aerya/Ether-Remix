import { Button, Input, Select, Switch, cn } from "@braedonsaunders/appkit-ui";
import { FolderPlus, Plus, Trash2 } from "lucide-react";
import { Field, NumberInput, Segmented } from "../../components/fields";
import { t } from "../../i18n";
import type {
  ConditionGroup,
  ConditionRule,
  FieldSchema,
  Operator,
  PickerFieldGroup,
} from "./types";
import { operatorLabel, operatorRequiresValue, operatorsForFieldType } from "./types";

type FieldWithPath = FieldSchema & { path: string };

function findField(groups: PickerFieldGroup[], path: string): FieldWithPath | undefined {
  for (const g of groups) for (const f of g.fields) if (f.path === path) return f;
  return undefined;
}

/** Coerce a rule's value when its field or operator changes. */
function defaultValue(field: FieldWithPath | undefined, op: Operator): unknown {
  if (!operatorRequiresValue(op)) return undefined;
  if (op === "in" || op === "not_in" || op === "contains_any" || op === "contains_all") {
    return field?.enum?.[0] ? [field.enum[0].value] : [];
  }
  if (!field) return "";
  if (field.type === "int" || field.type === "float" || field.type === "duration_ms")
    return typeof field.example === "number" ? field.example : 0;
  if (field.type === "bool") return true;
  if (field.type === "enum") return field.enum?.[0]?.value ?? "";
  return "";
}

function ValueEditor({
  rule,
  field,
  onChange,
}: {
  rule: ConditionRule;
  field?: FieldWithPath;
  onChange: (value: unknown) => void;
}) {
  if (!operatorRequiresValue(rule.op))
    return <span className="text-xs text-fg-subtle">{t("No value needed")}</span>;
  const multi =
    rule.op === "in" ||
    rule.op === "not_in" ||
    rule.op === "contains_any" ||
    rule.op === "contains_all";
  if (field?.type === "bool") {
    return (
      <Switch
        aria-label={t("Value")}
        checked={rule.value === true}
        onChange={(e) => onChange(e.target.checked)}
      />
    );
  }
  if (field?.enum?.length) {
    if (multi) {
      const selected = new Set((Array.isArray(rule.value) ? rule.value : []).map(String));
      return (
        <div className="flex flex-wrap gap-1">
          {field.enum.map((opt) => (
            <button
              key={opt.value}
              type="button"
              aria-pressed={selected.has(opt.value)}
              onClick={() => {
                const next = new Set(selected);
                if (next.has(opt.value)) next.delete(opt.value);
                else next.add(opt.value);
                onChange([...next]);
              }}
              className={cn(
                "rounded-full border px-2 py-0.5 text-xs transition-colors",
                selected.has(opt.value)
                  ? "border-primary bg-primary-subtle text-primary"
                  : "border-border text-fg-muted hover:text-fg",
              )}
            >
              {opt.label ?? opt.value}
            </button>
          ))}
        </div>
      );
    }
    return (
      <Select
        aria-label={t("Value")}
        value={String(rule.value ?? "")}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">{t("Choose…")}</option>
        {field.enum.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label ?? opt.value}
          </option>
        ))}
      </Select>
    );
  }
  if (field && (field.type === "int" || field.type === "duration_ms")) {
    return (
      <span className="flex items-center gap-1.5">
        <NumberInput
          aria-label={t("Value")}
          value={Number(rule.value ?? 0)}
          onChange={(n) => onChange(Math.round(n))}
          step={1}
          className="w-28"
        />
        {field.unit ? <span className="text-xs text-fg-subtle">{field.unit}</span> : null}
      </span>
    );
  }
  if (field?.type === "float") {
    return (
      <span className="flex items-center gap-1.5">
        <NumberInput
          aria-label={t("Value")}
          value={Number(rule.value ?? 0)}
          onChange={onChange}
          step="any"
          className="w-28"
        />
        {field.unit ? <span className="text-xs text-fg-subtle">{field.unit}</span> : null}
      </span>
    );
  }
  if (
    multi ||
    (field?.type === "string_list" && (rule.op === "contains_any" || rule.op === "contains_all"))
  ) {
    const text = Array.isArray(rule.value) ? rule.value.join(", ") : String(rule.value ?? "");
    return (
      <Input
        aria-label={t("Values, separated by commas")}
        value={text}
        placeholder={t("values, separated by commas")}
        onChange={(e) =>
          onChange(
            e.target.value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          )
        }
      />
    );
  }
  return (
    <Input
      aria-label={t("Value")}
      value={String(rule.value ?? "")}
      placeholder={field?.example !== undefined ? String(field.example) : undefined}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

/**
 * Typed condition editor: fields grouped by domain with labels and units,
 * operators limited to what the field supports, enum pickers for enums.
 * Groups nest one level so "(A and B) or C" stays readable.
 */
export function ConditionBuilder({
  value,
  onChange,
  fieldGroups,
  depth = 0,
}: {
  value: ConditionGroup;
  onChange: (next: ConditionGroup) => void;
  fieldGroups: PickerFieldGroup[];
  depth?: number;
}) {
  const group: ConditionGroup = { op: "and", rules: [], groups: [], ...value };
  const set = (patch: Partial<ConditionGroup>) => onChange({ ...group, ...patch });
  const rules = group.rules ?? [];
  const groups = group.groups ?? [];
  const firstField = fieldGroups[0]?.fields[0];

  return (
    <div
      className={cn(
        "space-y-2",
        depth > 0 && "rounded-lg border border-dashed border-border bg-bg-subtle/60 p-2.5",
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Segmented
          ariaLabel={t("Match")}
          value={group.op === "or" ? "or" : "and"}
          options={[
            { value: "and" as const, label: t("All must match") },
            { value: "or" as const, label: t("Any can match") },
          ]}
          onChange={(op) => set({ op })}
        />
        <label className="ml-auto flex items-center gap-1.5 text-xs text-fg-muted">
          <Switch
            checked={Boolean(group.negate)}
            onChange={(e) => set({ negate: e.target.checked || undefined })}
            aria-label={t("Invert this group")}
          />
          {t("Invert")}
        </label>
      </div>

      {rules.map((rule, i) => {
        const field = findField(fieldGroups, rule.field);
        const ops = operatorsForFieldType(field?.type ?? "string");
        const setRule = (next: ConditionRule) =>
          set({ rules: rules.map((r, j) => (j === i ? next : r)) });
        return (
          <div
            key={i}
            className="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-surface px-2.5 py-2"
          >
            <Field label={t("When")} className="min-w-[10rem] flex-1">
              <Select
                aria-label={t("Field")}
                value={rule.field}
                onChange={(e) => {
                  const nextField = findField(fieldGroups, e.target.value);
                  const nextOps = operatorsForFieldType(nextField?.type ?? "string");
                  const op = nextOps.includes(rule.op) ? rule.op : nextOps[0]!;
                  setRule({
                    ...rule,
                    field: e.target.value,
                    op,
                    value: defaultValue(nextField, op),
                  });
                }}
              >
                {fieldGroups.map((g) => (
                  <optgroup key={g.id} label={g.label}>
                    {g.fields.map((fld) => (
                      <option key={fld.path} value={fld.path}>
                        {fld.label}
                        {fld.unit ? ` (${fld.unit})` : ""}
                      </option>
                    ))}
                  </optgroup>
                ))}
                {fieldGroups.length === 0 ? <option value={rule.field}>{rule.field}</option> : null}
              </Select>
            </Field>
            <Field label={t("is")} className="w-36">
              <Select
                aria-label={t("Operator")}
                value={rule.op}
                onChange={(e) => {
                  const op = e.target.value as Operator;
                  setRule({
                    ...rule,
                    op,
                    value: operatorRequiresValue(op)
                      ? (rule.value ?? defaultValue(field, op))
                      : undefined,
                  });
                }}
              >
                {ops.map((op) => (
                  <option key={op} value={op}>
                    {operatorLabel(op)}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="min-w-[9rem] flex-1">
              <ValueEditor
                rule={rule}
                field={field}
                onChange={(v) => setRule({ ...rule, value: v })}
              />
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={t("Remove rule")}
              onClick={() => set({ rules: rules.filter((_, j) => j !== i) })}
            >
              <Trash2 size={14} />
            </Button>
          </div>
        );
      })}

      {groups.map((sub, i) => (
        <div key={i} className="relative">
          <ConditionBuilder
            value={sub}
            depth={depth + 1}
            fieldGroups={fieldGroups}
            onChange={(next) => set({ groups: groups.map((g, j) => (j === i ? next : g)) })}
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t("Remove group")}
            className="absolute right-1 top-1"
            onClick={() => set({ groups: groups.filter((_, j) => j !== i) })}
          >
            <Trash2 size={13} />
          </Button>
        </div>
      ))}

      <div className="flex flex-wrap gap-2 pt-0.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() =>
            set({
              rules: [
                ...rules,
                { field: firstField?.path ?? "", op: "eq", value: defaultValue(firstField, "eq") },
              ],
            })
          }
        >
          <Plus size={13} /> {t("Add rule")}
        </Button>
        {depth < 1 ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() =>
              set({
                groups: [
                  ...groups,
                  {
                    op: "and",
                    rules: firstField
                      ? [
                          {
                            field: firstField.path,
                            op: "eq",
                            value: defaultValue(firstField, "eq"),
                          },
                        ]
                      : [],
                  },
                ],
              })
            }
          >
            <FolderPlus size={13} /> {t("Add group")}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
