import { Button, Input, Select } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { useAsync } from "../lib/hooks";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface List {
  slug: string;
  name: string;
  type: "movie" | "show" | "mixed";
}
interface Result {
  collections: string[];
  libraries: number[];
}
const endpoint = "/pse/jukebox/";
const profile = "https://mdblist.com/lists/jukebox8786/";

export function JukeboxQuickSetup({ onCreated }: { onCreated?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <section className="rounded-lg border border-border p-4 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="font-medium">
            {L("Configuration rapide française", "French quick setup")}
          </h3>
          <p className="text-sm text-fg-muted">
            {L(
              "Films, séries et animés à partir des listes JukeBox.",
              "Movies, shows and anime from JukeBox lists.",
            )}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => setOpen(!open)} aria-expanded={open}>
          {open ? L("Fermer", "Close") : L("Configurer avec JukeBox", "Set up with JukeBox")}
        </Button>
      </div>
      {open ? <Picker onCreated={onCreated} /> : null}
    </section>
  );
}
function Picker({ onCreated }: { onCreated?: () => void }) {
  const defaults = useAsync(() => apiFetch<{ lists: List[] }>(endpoint), []);
  const [selection, setSelection] = useState<List[] | null>(null);
  const selected = selection ?? defaults.data?.lists ?? [];
  const [catalog, setCatalog] = useState<List[]>([]);
  const [search, setSearch] = useState("");
  const [mode, setMode] = useState("libraries");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<Result | null>(null);
  const lists = [
    ...(defaults.data?.lists ?? []),
    ...catalog.filter((l) => !defaults.data?.lists.some((d) => d.slug === l.slug)),
  ];
  const toggle = (l: List) => {
    setResult(null);
    setSelection(
      selected.some((s) => s.slug === l.slug)
        ? selected.filter((s) => s.slug !== l.slug)
        : [...selected, l],
    );
  };
  const load = async () => {
    setLoading(true);
    setError("");
    try {
      setCatalog((await apiFetch<{ lists: List[] }>(endpoint + "catalog")).lists);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  };
  const apply = async () => {
    setBusy(true);
    setError("");
    setResult(null);
    try {
      const next = await apiFetch<Result>(endpoint, {
        method: "POST",
        body: JSON.stringify({ lists: selected, libraries: mode === "libraries" }),
      });
      setResult(next);
      onCreated?.();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="space-y-3">
      <p className="text-sm text-fg-muted">
        {L(
          "Les trois listes sont précochées. Actualisation quotidienne, titres sortis uniquement, les plus récents d’abord. Vos sources et critères de langue restent nécessaires : une liste ne garantit pas une version française disponible.",
          "The three lists are preselected. Daily refresh, released titles only, newest first. Your sources and language settings still apply: a list does not guarantee an available French version.",
        )}
      </p>
      <p className="text-xs text-fg-muted">
        {L(
          "Les bibliothèques seront publiées dans Plex, Jellyfin, Emby et les autres clients compatibles. Une liste mixte crée deux bibliothèques, Films et Séries. Le remplissage est progressif ; un tri enregistré dans le client reste prioritaire.",
          "Libraries will be published to Plex, Jellyfin, Emby and other compatible clients. A mixed list creates two libraries, Movies and Shows. Filling happens in the background; a saved client sort takes precedence.",
        )}
      </p>
      <Select
        aria-label={L("Créer", "Create")}
        value={mode}
        disabled={busy}
        onChange={(e) => {
          setMode(e.target.value);
          setResult(null);
        }}
      >
        <option value="libraries">
          {L("Bibliothèques et collections", "Libraries and collections")}
        </option>
        <option value="collections">
          {L(
            "Collections / sélections automatiques seulement",
            "Collections / automatic selections only",
          )}
        </option>
      </Select>
      {defaults.error ? <p role="alert">{errorMessage(defaults.error)}</p> : null}
      {defaults.loading ? <p>{L("Chargement…", "Loading…")}</p> : null}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" disabled={loading || busy} onClick={() => void load()}>
          {loading
            ? L("Chargement…", "Loading…")
            : L("Choisir parmi toutes les listes JukeBox", "Browse all JukeBox lists")}
        </Button>
        <a className="text-sm underline" href={profile} target="_blank" rel="noreferrer">
          {L("Voir les listes sur MDBList", "View lists on MDBList")}
        </a>
      </div>
      {catalog.length ? (
        <Input
          aria-label={L("Rechercher une liste", "Search lists")}
          placeholder={L(
            "Rechercher (documentaires, studios…)",
            "Search (documentaries, studios…)",
          )}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      ) : null}
      <div className="max-h-80 overflow-auto space-y-2">
        {lists
          .filter((l) => l.name.toLocaleLowerCase().includes(search.toLocaleLowerCase()))
          .map((l) => {
            const chosen = selected.find((s) => s.slug === l.slug);
            return (
              <div
                key={l.slug}
                className="flex flex-wrap items-center gap-2 rounded border border-border p-2"
              >
                <label className="flex min-w-0 flex-1 items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={!!chosen}
                    disabled={busy || (!chosen && selected.length >= 30)}
                    onChange={() => toggle(l)}
                  />
                  {l.name}
                </label>
                <a
                  href={profile + l.slug}
                  target="_blank"
                  rel="noreferrer"
                  className="text-xs underline"
                >
                  MDBList
                </a>
                {chosen ? (
                  <Select
                    className="w-40 text-sm"
                    aria-label={L(`Type de ${l.name}`, `Type of ${l.name}`)}
                    disabled={busy}
                    value={chosen.type}
                    onChange={(e) => {
                      setSelection(
                        selected.map((s) =>
                          s.slug === l.slug ? { ...s, type: e.target.value as List["type"] } : s,
                        ),
                      );
                      setResult(null);
                    }}
                  >
                    <option value="movie">{L("Films", "Movies")}</option>
                    <option value="show">{L("Séries", "Shows")}</option>
                    <option value="mixed">{L("Films et séries", "Movies and shows")}</option>
                  </Select>
                ) : null}
              </div>
            );
          })}
      </div>
      <Button disabled={busy || !selected.length || !!defaults.error} onClick={() => void apply()}>
        {busy
          ? L("Création…", "Creating…")
          : L(
              `Créer ma sélection (${selected.length})`,
              `Create my selection (${selected.length})`,
            )}
      </Button>
      {error ? (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      ) : null}
      {result ? (
        <p role="status" className="text-sm">
          {L(
            `${result.collections.length} collections et ${result.libraries.length} bibliothèques prêtes ; les titres se chargent en arrière-plan. Les éléments existants ont été réutilisés.`,
            `${result.collections.length} collections and ${result.libraries.length} libraries ready; titles load in the background. Existing entries were reused.`,
          )}{" "}
          <Link className="underline" to="/libraries">
            {L("Bibliothèques", "Libraries")}
          </Link>
          {" · "}
          <Link className="underline" to="/collections">
            {L("Collections", "Collections")}
          </Link>
        </p>
      ) : null}
      <p className="text-xs text-fg-muted">
        {L(
          "Merci à JukeBox pour ses listes. Vous pourrez renommer les bibliothèques et personnaliser les collections après création.",
          "Thanks to JukeBox for these lists. You can rename libraries and customize collections after creation.",
        )}
      </p>
    </div>
  );
}
