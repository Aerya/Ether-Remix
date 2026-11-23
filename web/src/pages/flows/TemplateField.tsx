import { Button, Input, Popover, Textarea } from "@braedonsaunders/appkit-ui";
import { Braces, Search } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import type { PickerFieldGroup } from "./types";
import { previewTemplate, validateTemplateSyntax } from "./types";

/** The `{{ expr }}` path a picker inserts for one field. */
function insertPath(path: string): string {
  if (
    path.startsWith("steps.") ||
    path.startsWith("vars.") ||
    path.startsWith("event.") ||
    path.startsWith("correlation.") ||
    path.startsWith("trigger.")
  )
    return path;
  return `payload.${path}`;
}

/**
 * A text field that accepts `{{ templates }}`: insert field chips from the
 * picker instead of typing paths, preview the rendered text against sample
 * data, and see syntax problems inline as you type.
 */
export function TemplateField({
  value,
  onChange,
  fieldGroups,
  sample,
  multiline = false,
  placeholder,
  ariaLabel,
  id,
}: {
  value: string;
  onChange: (next: string) => void;
  fieldGroups: PickerFieldGroup[];
  sample?: Record<string, unknown>;
  multiline?: boolean;
  placeholder?: string;
  ariaLabel?: string;
  id?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const inputRef = useRef<HTMLInputElement | HTMLTextAreaElement | null>(null);
  const error = validateTemplateSyntax(value);
  const preview = useMemo(
    () => (sample && value.includes("{{") && !error ? previewTemplate(value, sample) : null),
    [value, sample, error],
  );

  const visibleGroups = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return fieldGroups;
    return fieldGroups
      .map((g) => ({
        ...g,
        fields: g.fields.filter(
          (f) => f.label.toLowerCase().includes(q) || f.path.toLowerCase().includes(q),
        ),
      }))
      .filter((g) => g.fields.length > 0);
  }, [fieldGroups, query]);

  const insert = (path: string) => {
    const el = inputRef.current;
    const token = `{{ ${insertPath(path)} }}`;
    const start = el?.selectionStart ?? value.length;
    const end = el?.selectionEnd ?? value.length;
    const next = value.slice(0, start) + token + value.slice(end);
    onChange(next);
    setOpen(false);
    requestAnimationFrame(() => {
      el?.focus();
      const pos = start + token.length;
      el?.setSelectionRange(pos, pos);
    });
  };

  return (
    <div className="space-y-1.5">
      <div className="flex items-start gap-1.5">
        <div className="min-w-0 flex-1">
          {multiline ? (
            <Textarea
              ref={(el) => {
                inputRef.current = el;
              }}
              id={id}
              aria-label={ariaLabel}
              value={value}
              rows={3}
              placeholder={placeholder}
              onChange={(e) => onChange(e.target.value)}
              className="font-mono text-[13px]"
              aria-invalid={error ? true : undefined}
            />
          ) : (
            <Input
              ref={(el) => {
                inputRef.current = el;
              }}
              id={id}
              aria-label={ariaLabel}
              value={value}
              placeholder={placeholder}
              onChange={(e) => onChange(e.target.value)}
              className="font-mono text-[13px]"
              aria-invalid={error ? true : undefined}
            />
          )}
        </div>
        <Popover
          open={open}
          onOpenChange={setOpen}
          align="end"
          trigger={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setOpen((o) => !o)}
              aria-expanded={open}
            >
              <Braces size={14} /> {t("Insert field")}
            </Button>
          }
        >
          <div className="w-72 max-w-[calc(100vw-2rem)] p-1">
            <label className="mb-1 flex items-center gap-2 rounded-md border border-border px-2 py-1.5">
              <Search size={13} className="shrink-0 text-fg-subtle" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t("Search fields…")}
                aria-label={t("Search fields")}
                className="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-fg-subtle"
              />
            </label>
            <div className="max-h-64 overflow-y-auto">
              {visibleGroups.map((g) => (
                <div key={g.id} className="py-1">
                  <p className="px-2 py-1 text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
                    {g.label}
                  </p>
                  {g.fields.map((f) => (
                    <button
                      key={f.path}
                      type="button"
                      onClick={() => insert(f.path)}
                      className="flex w-full items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-surface-hover"
                    >
                      <span className="min-w-0 truncate text-fg">{f.label}</span>
                      <span className="shrink-0 font-mono text-[11px] text-fg-subtle">
                        {f.path}
                      </span>
                    </button>
                  ))}
                </div>
              ))}
              {visibleGroups.length === 0 ? (
                <p className="px-2 py-3 text-sm text-fg-muted">{t("No fields match.")}</p>
              ) : null}
            </div>
          </div>
        </Popover>
      </div>
      {error ? (
        <p className="text-xs text-danger" role="alert">
          {error}
        </p>
      ) : preview !== null ? (
        <p className="rounded-md bg-bg-subtle px-2 py-1.5 text-xs text-fg-muted">
          <span className="font-medium text-fg-subtle">{t("Preview:")} </span>
          {preview || t("(empty for the sample event)")}
        </p>
      ) : null}
    </div>
  );
}
