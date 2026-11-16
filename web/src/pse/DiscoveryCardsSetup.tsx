import { Button, Input } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { useAsync } from "../lib/hooks";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const endpoint = "/pse/discovery/";
interface Card {
  id: string;
  name: string;
  group: string;
  accent: string;
}
interface State {
  enabled: boolean;
  cards: Card[];
}

export function DiscoveryCardsSetup() {
  const [open, setOpen] = useState(false);
  return (
    <section className="rounded-lg border border-border p-4 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="font-medium">{L("Cartes inspirées de Nuvio", "Nuvio-inspired cards")}</h3>
          <p className="text-sm text-fg-muted">
            {L(
              "Studios & labels, Services et Genres : des cartes qui ouvrent vos collections.",
              "Studios & labels, Services and Genres: cards that open your collections.",
            )}
          </p>
        </div>
        <Button variant="outline" onClick={() => setOpen(!open)} aria-expanded={open}>
          {L("Configuration rapide", "Quick setup")}
        </Button>
      </div>
      {open ? <Picker /> : null}
    </section>
  );
}
function Picker() {
  const state = useAsync(() => apiFetch<State>(endpoint), []);
  const [selection, setSelection] = useState<string[] | null>(null);
  const [region, setRegion] = useState("FR");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const cards = state.data?.cards ?? [];
  const selected = selection ?? cards.map((c) => c.id);
  async function apply(restore = false) {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await apiFetch(
        endpoint,
        restore
          ? { method: "DELETE" }
          : { method: "POST", body: JSON.stringify({ cards: selected, region }) },
      );
      await state.reload();
      setMessage(
        restore
          ? L(
              "Accueil précédent rétabli. Les collections créées restent disponibles.",
              "Previous home restored. Created collections remain available.",
            )
          : L(
              "Préréglage activé. Les collections se remplissent en arrière-plan ; actualisez ensuite votre client.",
              "Preset enabled. Collections fill in the background; refresh your client afterwards.",
            ),
      );
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-4">
      <p className="text-sm">
        {L(
          "Remplace les recommandations de l’accueil Plex et Jellyfin/Emby par les cartes sélectionnées, placées aussi en tête des collections. La disposition précédente est conservée et peut être rétablie.",
          "Replaces Plex and Jellyfin/Emby home recommendations with the selected cards, also placed first in collections. The previous layout is kept and can be restored.",
        )}
      </p>
      <p className="text-sm text-fg-muted">
        {L(
          "Le rendu dépend du client : Plex peut conserver des vignettes verticales ; Jellyfin peut préfixer les rangées par « Derniers ajouts ». Aucun accès à Nuvio ni manifeste Xperience nécessaire. Ce préréglage utilise les métadonnées TMDB, pas les fichiers ni la configuration de Nuvio.",
          "Rendering depends on the client: Plex may keep portrait tiles; Jellyfin may prefix rows with “Latest”. No Nuvio access or Xperience manifest is required. This preset uses TMDB metadata, not Nuvio files or configuration.",
        )}
      </p>
      <label className="block text-sm">
        {L("Pays des services (code à deux lettres)", "Services country (two-letter code)")}
        <Input
          className="mt-1 max-w-32"
          aria-label={L("Pays des services", "Services country")}
          value={region}
          maxLength={2}
          disabled={busy}
          onChange={(e) => setRegion(e.target.value.toUpperCase())}
        />
      </label>
      {[
        ["studios", L("Studios & labels", "Studios & labels")],
        ["services", "Services"],
        ["genres", "Genres"],
      ].map(([id, title]) => (
        <fieldset key={id} disabled={busy} className="space-y-2">
          <legend className="font-medium">{title}</legend>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
            {cards
              .filter((c) => c.group === id)
              .map((c) => (
                <label
                  key={c.id}
                  className="relative flex aspect-video items-center justify-center rounded-lg p-3 text-center text-white font-semibold cursor-pointer"
                  style={{ background: `linear-gradient(135deg, ${c.accent}, #10131d)` }}
                >
                  <input
                    type="checkbox"
                    className="absolute top-2 left-2"
                    checked={selected.includes(c.id)}
                    onChange={() =>
                      setSelection(
                        selected.includes(c.id)
                          ? selected.filter((v) => v !== c.id)
                          : [...selected, c.id],
                      )
                    }
                  />
                  {c.name}
                </label>
              ))}
          </div>
        </fieldset>
      ))}
      <p className="text-xs text-fg-muted">
        {L(
          "Aperçu indicatif. Les cartes réelles utilisent les logos TMDB disponibles ou une mosaïque des affiches ; un titre reste visible si le logo manque. Films et séries ont des cartes séparées, limitées à 100 titres chacune. Les collections vides sont masquées. Réappliquer ajoute les cartes manquantes sans supprimer les anciennes ni écraser vos personnalisations.",
          "Illustrative preview. Actual cards use available TMDB logos or poster mosaics, with a title fallback. Movies and shows have separate cards, up to 100 titles each. Empty collections are hidden. Reapplying adds missing cards without deleting previous ones or overwriting customizations.",
        )}
      </p>
      <div className="flex flex-wrap gap-3">
        <Button
          disabled={busy || !selected.length || !/^[A-Z]{2}$/.test(region)}
          onClick={() => void apply()}
        >
          {busy
            ? L("Enregistrement…", "Saving…")
            : L("Appliquer les cartes à mes accueils", "Apply cards to my home screens")}
        </Button>
        {state.data?.enabled ? (
          <Button variant="outline" disabled={busy} onClick={() => void apply(true)}>
            {L("Rétablir l’accueil précédent", "Restore previous home")}
          </Button>
        ) : null}
        <Link className="self-center text-sm underline" to="/collections">
          {L("Personnaliser les collections", "Customize collections")}
        </Link>
      </div>
      {message ? (
        <p role="status" className="text-sm">
          {message}
        </p>
      ) : null}
      {error || state.error ? (
        <p role="alert" className="text-sm text-danger">
          {error || errorMessage(state.error)}
        </p>
      ) : null}
    </div>
  );
}

export function DiscoveryCardsNotice() {
  const state = useAsync(() => apiFetch<State>(endpoint), []);
  if (!state.data?.enabled) return null;
  return (
    <div
      role="status"
      className="rounded-lg border border-primary/40 bg-primary-subtle p-3 text-sm"
    >
      {L(
        "Le préréglage Cartes inspirées de Nuvio remplace actuellement cet accueil sur Plex et Jellyfin/Emby. Cette page conserve votre disposition précédente.",
        "The Nuvio-inspired cards preset currently replaces this home on Plex and Jellyfin/Emby. This page retains your previous layout.",
      )}{" "}
      <Link to="/remix/contenus" className="underline">
        {L("Gérer les cartes ou rétablir cet accueil", "Manage cards or restore this home")}
      </Link>
    </div>
  );
}
