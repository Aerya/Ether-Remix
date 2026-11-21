import { Button, Input } from "@braedonsaunders/appkit-ui";
import { Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

interface Row {
  name?: string;
  url: string;
  key?: string;
}

const blank = (r: Row) => !r.url.trim() && !(r.name ?? "").trim() && !(r.key ?? "").trim();

function parse(value: string): Row[] {
  if (!value.trim()) return [];
  try {
    const list = JSON.parse(value) as Row[];
    return Array.isArray(list)
      ? list.map((r) => ({ name: r.name ?? "", url: r.url ?? "", key: r.key ?? "" }))
      : [];
  } catch {
    return [];
  }
}

/** Les lignes vides ne sont pas enregistrées ; une clé vide n’est pas écrite. */
function serialize(rows: Row[]): string {
  const kept = rows
    .filter((r) => !blank(r))
    .map((r) => ({
      ...(r.name?.trim() ? { name: r.name.trim() } : {}),
      url: r.url.trim(),
      ...(r.key?.trim() ? { key: r.key.trim() } : {}),
    }));
  return kept.length ? JSON.stringify(kept) : "";
}

/**
 * Liste de sites (flux RSS ou API) : autant de lignes que l’on veut, chacune avec un
 * nom facultatif, l’adresse et une clé d’API facultative. Une clé déjà enregistrée
 * s’affiche masquée ; la laisser telle quelle la conserve.
 */
export function PreFeedsControl({
  value,
  onChange,
  label,
}: {
  value: string;
  onChange: (value: string) => void;
  label: string;
}) {
  const [rows, setRows] = useState<Row[]>(() => parse(value));
  const last = useRef(serialize(rows));
  useEffect(() => {
    // Valeur changée de l’extérieur (annuler, enregistrement) : on repart d’elle.
    if (value !== last.current) {
      last.current = value;
      setRows(parse(value));
    }
  }, [value]);
  const update = (next: Row[]) => {
    setRows(next);
    last.current = serialize(next);
    onChange(last.current);
  };
  const edit = (i: number, patch: Partial<Row>) =>
    update(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <div className="w-full space-y-3" role="group" aria-label={label}>
      {rows.length === 0 ? (
        <p className="text-sm text-fg-muted">
          {L("Aucun site ajouté pour le moment.", "No site added yet.")}
        </p>
      ) : null}
      {rows.map((r, i) => (
        <div
          key={i}
          className="grid gap-2 rounded-xl border border-border p-3 md:grid-cols-[10rem_minmax(0,1fr)_11rem_auto]"
        >
          <Input
            aria-label={L(`Nom du site ${i + 1}`, `Site ${i + 1} name`)}
            placeholder={L("Nom (facultatif)", "Name (optional)")}
            value={r.name ?? ""}
            onChange={(e) => edit(i, { name: e.target.value })}
          />
          <Input
            aria-label={L(`Adresse du site ${i + 1}`, `Site ${i + 1} address`)}
            placeholder="https://…"
            value={r.url}
            spellCheck={false}
            onChange={(e) => edit(i, { url: e.target.value })}
          />
          <Input
            aria-label={L(`Clé d’API du site ${i + 1}`, `Site ${i + 1} API key`)}
            type="password"
            placeholder={L("Clé d’API (facultatif)", "API key (optional)")}
            autoComplete="new-password"
            value={r.key ?? ""}
            spellCheck={false}
            onChange={(e) => edit(i, { key: e.target.value })}
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-label={L(`Retirer le site ${i + 1}`, `Remove site ${i + 1}`)}
            onClick={() => update(rows.filter((_, j) => j !== i))}
          >
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setRows([...rows, { name: "", url: "", key: "" }])}
      >
        <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
        {L("Ajouter un site", "Add a site")}
      </Button>
    </div>
  );
}
