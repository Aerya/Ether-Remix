import { Button, toast } from "@braedonsaunders/appkit-ui";
import { Plus, RotateCcw } from "lucide-react";
import { useEffect, useState } from "react";
import { ErrorPanel, LoadingRows } from "../components/feedback";
import { getLocale } from "../i18n";
import { RuleRow } from "../pages/collections/RuleRow";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import type { Rule, RuleField } from "../lib/contract";
import { fieldMeta, isRuleComplete, type FieldMeta } from "../lib/rules";

/**
 * Règles d'exclusion des derniers ajouts Jellyfin de Films et Séries
 * (internal/pse/latestfilter, API GET/PUT /pse/jellyfin-latest). Même éditeur
 * de règles que « Modifier la collection » ; seul « exclude » est utilisé.
 */

const ENDPOINT = "/pse/jellyfin-latest";
const L = (fr: string, en: string) => (getLocale().toLowerCase().startsWith("fr") ? fr : en);

type Kind = "movie" | "show";
interface Filter {
  exclude?: Rule[];
}
interface Settings {
  movie: Filter;
  show: Filter;
}
interface Response extends Settings {
  defaults: Settings;
}

/** Champs que le filtre sait décider sans base de données ; genre, pays et langue d'abord. */
const ALLOWED: RuleField[] = [
  "genre",
  "country",
  "language",
  "studio",
  "year",
  "rating",
  "votes",
  "runtime",
  "contentRating",
];

function metaFor(field: RuleField): FieldMeta | undefined {
  const base = fieldMeta(field);
  if (!base) return undefined;
  if (field === "studio")
    return { ...base, label: L("Studio ou réseau", "Studio or network"), defaultOp: "in" };
  return base;
}

/** Les trois critères demandés, déjà prêts (valeurs vides) quand rien n'est défini. */
const starter = (): Rule[] => [
  { field: "genre", op: "in", value: [] },
  { field: "country", op: "in", value: [] },
  { field: "language", op: "in", value: [] },
];

const withStarter = (rules?: Rule[]): Rule[] => (rules?.length ? rules : starter());

function Block({
  kind,
  rules,
  defaults,
  onChange,
}: {
  kind: Kind;
  rules: Rule[];
  defaults: Rule[];
  onChange: (rules: Rule[]) => void;
}) {
  const title = kind === "movie" ? L("Films", "Movies") : L("Séries", "Shows");
  return (
    <section className="space-y-2" aria-label={title}>
      <h4 className="text-sm font-medium text-fg">{title}</h4>
      <p className="text-xs text-fg-muted">
        {L(
          "Retirer les titres qui correspondent à au moins une de ces règles.",
          "Remove titles matching at least one of these rules.",
        )}
      </p>
      <div className="space-y-2">
        {rules.map((rule, i) => (
          <RuleRow
            key={i}
            rule={rule}
            index={i}
            kind="exclude"
            mediaType={kind}
            region=""
            allowedFields={ALLOWED}
            metaFor={metaFor}
            inline
            onChange={(r) => onChange(rules.map((x, j) => (j === i ? r : x)))}
            onRemove={() => onChange(rules.filter((_, j) => j !== i))}
          />
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => onChange([...rules, { field: "genre", op: "in", value: [] }])}
        >
          <Plus size={14} /> {L("Ajouter une règle", "Add rule")}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            if (
              window.confirm(
                L(
                  "Remplacer les règles de ce bloc par le modèle par défaut ?",
                  "Replace this block's rules with the default template?",
                ),
              )
            )
              onChange(defaults.map((r) => ({ ...r })));
          }}
        >
          <RotateCcw size={14} /> {L("Appliquer le modèle par défaut", "Apply default template")}
        </Button>
      </div>
    </section>
  );
}

export function JellyfinLatestFilters() {
  const doc = useAsync((signal) => apiFetch<Response>(ENDPOINT, { signal }), []);
  const [draft, setDraft] = useState<{ movie: Rule[]; show: Rule[] } | null>(null);
  const [saving, setSaving] = useState(false);
  const data = doc.data;

  useEffect(() => {
    if (data)
      setDraft({
        movie: withStarter(data.movie.exclude),
        show: withStarter(data.show.exclude),
      });
  }, [data]);

  if (doc.error) return <ErrorPanel error={doc.error} onRetry={() => void doc.reload()} />;
  if (!data || !draft) return <LoadingRows rows={3} />;

  const save = async () => {
    const clean = (rules: Rule[]) => rules.filter(isRuleComplete);
    setSaving(true);
    try {
      const next = await apiFetch<Response>(ENDPOINT, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          movie: { exclude: clean(draft.movie) },
          show: { exclude: clean(draft.show) },
        }),
      });
      doc.setData(next);
      toast.success(L("Règles enregistrées", "Rules saved"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6 p-4" id="pse-jellyfinlatest-rules">
      {(["movie", "show"] as const).map((kind) => (
        <Block
          key={kind}
          kind={kind}
          rules={draft[kind]}
          defaults={data.defaults[kind].exclude ?? []}
          onChange={(rules) => setDraft({ ...draft, [kind]: rules })}
        />
      ))}
      <Button type="button" disabled={saving} onClick={() => void save()}>
        {L("Enregistrer", "Save")}
      </Button>
    </div>
  );
}
