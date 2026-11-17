import { useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
type Destination = { id: string; name: string; kind: string; mediaType: string; linked: boolean };
export function LocalDestinations({ rootId }: { rootId: string }) {
  const [items, setItems] = useState<Destination[]>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const load = async () => {
    setItems(
      await apiFetch<Destination[]>(
        "/pse/owned/local/destinations?id=" + encodeURIComponent(rootId),
      ),
    );
  };
  const refresh = async () => {
    setBusy(true);
    setError("");
    try {
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  const change = async (d: Destination) => {
    setBusy(true);
    setError("");
    try {
      await apiFetch("/pse/owned/local/destinations", {
        method: "PUT",
        body: JSON.stringify({ rootId, kind: d.kind, id: d.id, linked: !d.linked }),
      });
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <details
      className="mt-3 text-sm"
      onToggle={(e) => {
        if (e.currentTarget.open && !items && !busy) void refresh();
      }}
    >
      <summary className="cursor-pointer font-medium">
        {L("Bibliothèques et collections", "Libraries and collections")}
      </summary>
      <div className="mt-3 space-y-3">
        <p>
          {L(
            "Les nouveaux fichiers rejoignent les destinations après analyse et actualisation des collections, sans déplacement. Les filtres de la destination restent appliqués : les vidéos sans fiche TMDB restent des vidéos de type film.",
            "New files join destinations after scanning and collection refresh, without moving files. Destination filters still apply: videos without a TMDB match remain movie-type videos.",
          )}
        </p>
        <p>
          {L(
            "Le partage du dossier ne change pas. Chaque spectateur ne voit que les contenus des dossiers auxquels il a accès ; les autres contenus de la destination restent visibles. Détacher la dernière collection d’une bibliothèque retire cette bibliothèque vide.",
            "Folder sharing is unchanged. Viewers only see content from folders they can access; other content in the destination stays visible. Removing a library’s final collection removes that empty library.",
          )}
        </p>
        {(["library", "collection"] as const).map((kind) => (
          <fieldset key={kind} className="space-y-2">
            <legend className="font-medium">
              {kind === "library"
                ? L("Bibliothèques", "Libraries")
                : L("Autres collections personnalisées", "Other custom collections")}
            </legend>
            {items
              ?.filter((d) => d.kind === kind)
              .map((d) => (
                <label key={d.id} className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={d.linked}
                    disabled={busy}
                    onChange={() => void change(d)}
                  />
                  {d.name} ·{" "}
                  {d.mediaType === "movie"
                    ? L("Films", "Movies")
                    : d.mediaType === "show"
                      ? L("Séries", "Shows")
                      : L("Films et séries", "Movies and shows")}
                </label>
              ))}
          </fieldset>
        ))}
        <Link className="underline" to="/remix/contenus">
          {L("Créer ou modifier une bibliothèque", "Create or edit a library")}
        </Link>
        <Link className="underline" to="/collections">
          {L("Créer ou modifier une collection", "Create or edit a collection")}
        </Link>
        <Button variant="outline" size="sm" disabled={busy} onClick={() => void refresh()}>
          {L("Actualiser les destinations", "Refresh destinations")}
        </Button>
        {error && <p role="alert">{error}</p>}
      </div>
    </details>
  );
}
