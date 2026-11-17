import { Button, Input, Select } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const endpoint = "/pse/xperience/";
interface Extra {
  name: string;
  isRequired?: boolean;
  options?: string[];
}
interface Catalog {
  id: string;
  type: "movie" | "series";
  name: string;
  extras: Extra[];
  defaults: Record<string, string>;
}
interface Choice {
  id: string;
  type: string;
  name: string;
  params: Record<string, string>;
}
interface Result {
  collections: string[];
  libraries: number[];
}
const keyOf = (c: { type: string; id: string }) => JSON.stringify([c.type, c.id]);

export function XperienceImport({ onCreated }: { onCreated?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <section className="rounded-lg border border-border p-4 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="font-medium">{L("Importer Xperience", "Import Xperience")}</h3>
          <p className="text-sm text-fg-muted">
            {L(
              "Vos catalogues Xperience en bibliothèques et collections.",
              "Your Xperience catalogues as libraries and collections.",
            )}
          </p>
          <a
            href="https://xperience-app.com/"
            target="_blank"
            rel="noreferrer"
            className="text-sm underline"
          >
            xperience-app.com
          </a>
        </div>
        <Button size="sm" variant="outline" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? L("Fermer", "Close") : L("Importer un manifeste", "Import a manifest")}
        </Button>
      </div>
      {open ? <Picker onCreated={onCreated} /> : null}
    </section>
  );
}
function Picker({ onCreated }: { onCreated?: () => void }) {
  const [manifestRefreshHours, setManifestRefreshHours] = useState("1");
  const [collectionRefreshHours, setCollectionRefreshHours] = useState("24");
  const [manifest, setManifest] = useState("");
  const [catalogs, setCatalogs] = useState<Catalog[] | null>(null);
  const [selected, setSelected] = useState<Record<string, Choice>>({});
  const [search, setSearch] = useState("");
  const [mode, setMode] = useState("both");
  const [maxItems, setMaxItems] = useState("500");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<Result | null>(null);
  const choices = Object.values(selected);
  const incomplete = choices.some((c) =>
    catalogs
      ?.find((d) => keyOf(d) === keyOf(c))
      ?.extras.some((e) => e.isRequired && !c.params[e.name]?.trim()),
  );
  const load = async () => {
    setBusy(true);
    setError("");
    setResult(null);
    setCatalogs(null);
    setSelected({});
    try {
      setCatalogs(
        (
          await apiFetch<{ catalogs: Catalog[] }>(endpoint + "preview", {
            method: "POST",
            body: JSON.stringify({ manifest }),
          })
        ).catalogs,
      );
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const apply = async () => {
    setBusy(true);
    setError("");
    setResult(null);
    try {
      setResult(
        await apiFetch<Result>(endpoint, {
          method: "POST",
          body: JSON.stringify({
            manifest,
            catalogs: choices,
            mode,
            maxItems: Number(maxItems),
            manifestRefreshHours: Number(manifestRefreshHours),
            collectionRefreshHours: Number(collectionRefreshHours),
          }),
        }),
      );
      onCreated?.();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const change = (key: string, update: Partial<Choice>) => {
    setSelected((prev) => ({ ...prev, [key]: { ...prev[key]!, ...update } }));
    setResult(null);
  };
  return (
    <div className="space-y-3">
      <p className="text-sm text-fg-muted">
        {L(
          "Collez le manifeste personnel obtenu sur Xperience, puis choisissez vos catalogues. Le lien reste privé dans la configuration de la source.",
          "Paste your personal Xperience manifest, then choose catalogues. The link stays private in the source configuration.",
        )}
      </p>
      <Input
        type="password"
        autoComplete="off"
        aria-label={L("Manifeste Xperience", "Xperience manifest")}
        placeholder="https://xperience-app.com/…/manifest.json"
        value={manifest}
        disabled={busy}
        onChange={(e) => {
          setManifest(e.target.value);
          setCatalogs(null);
          setSelected({});
          setResult(null);
          setError("");
        }}
      />
      <Button variant="outline" disabled={busy || !manifest.trim()} onClick={() => void load()}>
        {busy ? L("Chargement…", "Loading…") : L("Afficher les catalogues", "Show catalogues")}
      </Button>
      {catalogs !== null ? (
        <>
          <p className="text-sm text-fg-muted">
            {L(
              "Classement Xperience conservé. Les filtres Remix de langue et de disponibilité restent appliqués : Xperience fournit des listes, pas des sources de lecture. Les catalogues de recherche sont exclus.",
              "Xperience ordering is preserved. Remix language and availability filters still apply: Xperience provides lists, not playback sources. Search catalogues are excluded.",
            )}
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="text-sm">
              {L("Actualiser le manifeste (heures)", "Refresh manifest (hours)")}
              <Input
                type="number"
                min={1}
                max={168}
                value={manifestRefreshHours}
                disabled={busy}
                onChange={(e) => {
                  setManifestRefreshHours(e.target.value);
                  setResult(null);
                }}
              />
            </label>
            <label className="text-sm">
              {L(
                "Actualiser les nouvelles collections (heures)",
                "Refresh new collections (hours)",
              )}
              <Input
                type="number"
                min={1}
                max={168}
                value={collectionRefreshHours}
                disabled={busy}
                onChange={(e) => {
                  setCollectionRefreshHours(e.target.value);
                  setResult(null);
                }}
              />
            </label>
          </div>
          <p className="text-xs text-fg-muted">
            {L(
              "De 1 à 168 heures. Le manifeste décrit les catalogues disponibles ; les collections actualisent leurs titres. L’import installe ou réutilise le plugin Xperience dans Ether. Un réimport règle l’intervalle du manifeste mais conserve celui des collections existantes, modifiable dans leur éditeur.",
              "From 1 to 168 hours. The manifest describes available catalogues; collections refresh their titles. Import installs or reuses the Xperience plugin in Ether. Reimport changes the manifest interval but keeps existing collection schedules, editable in their editor.",
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
            <option value="both">
              {L("Bibliothèques et collections", "Libraries and collections")}
            </option>
            <option value="collections">{L("Collections seulement", "Collections only")}</option>
            <option value="libraries">{L("Bibliothèques seulement", "Libraries only")}</option>
          </Select>
          {mode === "libraries" ? (
            <p className="text-xs text-fg-muted">
              {L(
                "Chaque bibliothèque utilise une collection de gestion, sans rangée d’accueil ni publication dans l’onglet Collections. Les réglages des éléments existants sont conservés.",
                "Each library uses a backing collection, without a Home row or publication in the Collections tab. Existing entries keep their settings.",
              )}
            </p>
          ) : null}
          <label className="block text-sm">
            {L("Titres maximum par catalogue (1–2000)", "Maximum titles per catalogue (1–2000)")}
            <Input
              type="number"
              min={1}
              max={2000}
              value={maxItems}
              disabled={busy}
              onChange={(e) => {
                setMaxItems(e.target.value);
                setResult(null);
              }}
            />
          </label>
          <Input
            aria-label={L("Rechercher un catalogue", "Search catalogues")}
            placeholder={L("Animation, nouveautés…", "Animation, trending…")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <p className="text-xs text-fg-muted">
            {L(
              `${choices.length}/30 catalogues sélectionnés.`,
              `${choices.length}/30 catalogues selected.`,
            )}
          </p>
          <div className="max-h-96 overflow-auto space-y-2">
            {catalogs
              .filter((c) =>
                (c.name + " " + c.id).toLocaleLowerCase().includes(search.toLocaleLowerCase()),
              )
              .map((c) => {
                const key = keyOf(c),
                  choice = selected[key];
                const label =
                  c.name +
                  " · " +
                  (c.type === "movie" ? L("Films", "Movies") : L("Séries", "Shows"));
                return (
                  <div key={key} className="rounded border border-border p-3 space-y-2">
                    <label className="flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        checked={!!choice}
                        disabled={busy || (!choice && choices.length >= 30)}
                        onChange={() => {
                          setSelected((prev) => {
                            const next = { ...prev };
                            if (next[key]) delete next[key];
                            else
                              next[key] = {
                                id: c.id,
                                type: c.type,
                                name: label.slice(0, 100),
                                params: { ...c.defaults },
                              };
                            return next;
                          });
                          setResult(null);
                        }}
                      />
                      {label}
                    </label>
                    {choice ? (
                      <>
                        <Input
                          aria-label={L(`Nom de ${label}`, `Name for ${label}`)}
                          maxLength={120}
                          value={choice.name}
                          disabled={busy}
                          onChange={(e) => change(key, { name: e.target.value })}
                        />
                        {c.extras.map((extra) => {
                          const props = {
                            "aria-label": `${extra.name} · ${label}`,
                            value: choice.params[extra.name] ?? "",
                            disabled: busy,
                            onChange: (e: { target: { value: string } }) =>
                              change(key, {
                                params: { ...choice.params, [extra.name]: e.target.value },
                              }),
                          };
                          return (
                            <label key={extra.name} className="block text-sm">
                              {extra.name}
                              {extra.isRequired ? " *" : ""}
                              {extra.options?.length ? (
                                <Select {...props}>
                                  <option value="">{L("Choisir…", "Choose…")}</option>
                                  {extra.options.map((o) => (
                                    <option key={o} value={o}>
                                      {o === "None"
                                        ? L("Sans filtre supplémentaire", "No additional filter")
                                        : o}
                                    </option>
                                  ))}
                                </Select>
                              ) : (
                                <Input {...props} maxLength={500} />
                              )}
                            </label>
                          );
                        })}
                      </>
                    ) : null}
                  </div>
                );
              })}
          </div>
          <Button
            disabled={
              busy ||
              !choices.length ||
              [manifestRefreshHours, collectionRefreshHours].some(
                (v) => !Number.isInteger(Number(v)) || Number(v) < 1 || Number(v) > 168,
              ) ||
              incomplete ||
              choices.some((c) => !c.name.trim()) ||
              !Number.isInteger(Number(maxItems)) ||
              Number(maxItems) < 1 ||
              Number(maxItems) > 2000
            }
            onClick={() => void apply()}
          >
            {busy
              ? L("Création…", "Creating…")
              : L(`Créer la sélection (${choices.length})`, `Create selection (${choices.length})`)}
          </Button>
          <p className="text-xs text-fg-muted">
            {L(
              "Les bibliothèques sont proposées aux clients compatibles, notamment Plex, Jellyfin et Emby. Le remplissage est progressif et limité au nombre choisi ; les titres non rapprochés du catalogue peuvent manquer. Un nouvel import réutilise les mêmes sélections sans remplacer vos personnalisations.",
              "Libraries are published to compatible clients, including Plex, Jellyfin and Emby. Filling happens in the background up to the selected limit; titles not matched to the catalogue may be missing. Reimporting reuses the same selections without replacing your customizations.",
            )}
          </p>
        </>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      ) : null}
      {result ? (
        <p role="status" className="text-sm">
          {L(
            `${result.collections.length} collections de sélection et ${result.libraries.length} bibliothèques prêtes. Les titres se chargent en arrière-plan.`,
            `${result.collections.length} selection collections and ${result.libraries.length} libraries ready. Titles load in the background.`,
          )}{" "}
          <Link to="/libraries" className="underline">
            {L("Bibliothèques", "Libraries")}
          </Link>
          {" · "}
          <Link to="/collections" className="underline">
            {L("Collections", "Collections")}
          </Link>
        </p>
      ) : null}
    </div>
  );
}
