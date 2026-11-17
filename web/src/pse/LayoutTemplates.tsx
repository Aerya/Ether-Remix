import { Button, Input } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";

type Template = { id: string; name: string; createdAt: number };
const endpoint = "/pse/layouts/";
const L = (fr: string, en: string) => (getLocale() === "fr" ? fr : en);

export function LayoutTemplates({ dirty = false }: { dirty?: boolean }) {
  const doc = useAsync(() => apiFetch<Template[]>(endpoint), []);
  const [name, setName] = useState("");
  const [from, setFrom] = useState("");
  const [pending, setPending] = useState<(Template & { remove?: boolean }) | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const entries = Array.isArray(doc.data) ? doc.data : [];
  async function action(path: string, body?: object, method = "POST") {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const data = await apiFetch<Template[]>(endpoint + path, {
        method,
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      doc.setData(data);
      if (path.endsWith("/apply")) {
        window.location.reload();
        return;
      }
      setName("");
      setFrom("");
      setPending(null);
      setNotice(
        method === "DELETE"
          ? L("Modèle supprimé.", "Template deleted.")
          : L("Modèle enregistré.", "Template saved."),
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="rounded-xl border border-border bg-surface p-4">
      <summary className="cursor-pointer text-sm font-semibold">
        {L("Modèles de disposition", "Layout templates")}
      </summary>
      <div className="mt-3 space-y-3 text-sm">
        <p>
          {L(
            "Enregistrez l’existant, puis testez des variantes A, B ou C : accueil, bibliothèques et présentation des collections.",
            "Save your current setup, then try variants A, B or C: Home, libraries and collection presentation.",
          )}
        </p>
        <p className="text-fg-muted">
          {L(
            "Les droits d’accès, sources et médias restent inchangés. Les éléments créés après le modèle sont conservés ; une référence supprimée empêche son application. Enregistrez vos modifications dans leurs éditeurs avant de capturer un modèle.",
            "Access rights, sources and media stay unchanged. Items created after a template are retained; deleted references prevent applying it. Save changes in their editors before capturing a template.",
          )}
        </p>
        {dirty && (
          <p role="status">
            {L(
              "Cet éditeur contient des modifications non enregistrées : enregistrez-les d’abord.",
              "This editor has unsaved changes: save them first.",
            )}
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <Input
            aria-label={L("Nom du modèle", "Template name")}
            placeholder={L("Ex. Disposition familiale", "E.g. Family layout")}
            maxLength={80}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Button
            disabled={busy || dirty || !name.trim()}
            onClick={() => void action("", { name, from })}
          >
            {from
              ? L("Enregistrer la copie", "Save copy")
              : L("Enregistrer la disposition actuelle", "Save current layout")}
          </Button>
          {from && (
            <Button onClick={() => setFrom("")}>{L("Annuler la copie", "Cancel copy")}</Button>
          )}
        </div>
        {(Boolean(error) || Boolean(doc.error)) && (
          <p role="alert">
            {error || L("Impossible de charger les modèles.", "Could not load templates.")}
          </p>
        )}
        {notice && <p role="status">{notice}</p>}
        {entries.map((entry) => (
          <div
            key={entry.id}
            className="flex flex-wrap items-center gap-2 rounded border border-border p-2"
          >
            <span className="mr-auto">
              {entry.id === "_previous"
                ? L("Avant la dernière application", "Before the last application")
                : entry.name}
            </span>
            <Button disabled={busy || dirty} onClick={() => setPending(entry)}>
              {entry.id === "_previous"
                ? L("Revenir en arrière", "Go back")
                : L("Appliquer", "Apply")}
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                setFrom(entry.id);
                setName(entry.name + L(" — copie", " — copy"));
              }}
            >
              {L("Dupliquer", "Duplicate")}
            </Button>
            <Button disabled={busy} onClick={() => setPending({ ...entry, remove: true })}>
              {L("Supprimer", "Delete")}
            </Button>
          </div>
        ))}
        {pending && (
          <div
            role="alertdialog"
            aria-label={L("Appliquer un modèle", "Apply template")}
            className="space-y-2 rounded border border-border p-3"
          >
            <p>
              {pending.remove
                ? L("Supprimer le modèle", "Delete template")
                : L("Appliquer", "Apply")}{" "}
              « {pending.name} » ?{" "}
              {!pending.remove &&
                L(
                  "La disposition actuelle sera conservée dans « Avant la dernière application ». La page sera rechargée.",
                  "The current layout will be saved under ‘Before the last application’. The page will reload.",
                )}
            </p>
            <Button
              disabled={busy || dirty}
              onClick={() =>
                void action(
                  encodeURIComponent(pending.id) + (pending.remove ? "" : "/apply"),
                  undefined,
                  pending.remove ? "DELETE" : "POST",
                )
              }
            >
              {pending.remove
                ? L("Confirmer la suppression", "Confirm delete")
                : L("Confirmer l’application", "Confirm apply")}
            </Button>
            <Button disabled={busy} onClick={() => setPending(null)}>
              {L("Annuler", "Cancel")}
            </Button>
          </div>
        )}
      </div>
    </details>
  );
}
