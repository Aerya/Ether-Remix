import { Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { Check, Pencil, X } from "lucide-react";
import { useState, type FormEvent } from "react";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import type { Viewer } from "../lib/contract";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

/** Pencil beside a viewer's name: sets the name shown in statistics and lists. */
export function ViewerDisplayName({
  viewer,
  onChange,
}: {
  viewer: Pick<Viewer, "id" | "username" | "customName">;
  onChange: () => void;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  if (draft === null)
    return (
      <button
        type="button"
        aria-label={L("Modifier le nom affiché", "Change the display name")}
        title={L("Modifier le nom affiché", "Change the display name")}
        className="text-fg-muted hover:text-fg"
        onClick={() => setDraft(viewer.customName ?? "")}
      >
        <Pencil size={14} />
      </button>
    );

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try {
      await apiFetch(`/viewers/${viewer.id}/display-name`, {
        method: "PUT",
        body: JSON.stringify({ name: draft }),
      });
      toast.success(L("Nom affiché enregistré", "Display name saved"));
      setDraft(null);
      onChange();
    } catch (e) {
      toast.error(L("Impossible d’enregistrer le nom", "Couldn't save the name"), {
        description: errorMessage(e),
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <form className="flex items-center gap-1 text-sm font-normal" onSubmit={(e) => void submit(e)}>
      <Input
        autoFocus
        aria-label={L("Nom affiché", "Display name")}
        placeholder={viewer.username || L("Nom Plex", "Plex name")}
        maxLength={64}
        value={draft}
        onChange={(e) => setDraft(e.currentTarget.value)}
      />
      <Button
        type="submit"
        size="sm"
        disabled={saving}
        aria-label={L("Enregistrer", "Save")}
        title={L("Vide pour reprendre le nom Plex", "Empty restores the Plex name")}
      >
        <Check size={14} />
      </Button>
      <Button
        type="button"
        size="sm"
        variant="ghost"
        disabled={saving}
        aria-label={L("Annuler", "Cancel")}
        onClick={() => setDraft(null)}
      >
        <X size={14} />
      </Button>
    </form>
  );
}
