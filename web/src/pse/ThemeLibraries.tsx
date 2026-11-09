import { Badge, Button, Input, Select, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { ArrowDown, ArrowUp, Plus, Trash2, X } from "lucide-react";
import { useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { PseAddonCatalogImport } from "./AddonCatalogImport";

/**
 * Bibliothèques à thème (internal/pse/themes, api.go) : des bibliothèques
 * Plex supplémentaires dont chaque rangée est une collection d'Ether.
 */

type Kind = "movie" | "show" | "mixed";

export interface ThemeLibrary {
  id: number;
  name: string;
  type: Kind;
  collections: string[];
  position: number;
  section: number;
  showSection?: number;
  allLanguages?: boolean;
}

export interface ThemeCollection {
  id: string;
  name: string;
  mediaType: string;
  items: number;
  builtIn: boolean;
}

interface Preset {
  id: string;
  name: string;
  type: Kind;
  summary: string;
  rows: string[];
  allLanguages?: boolean;
}

export interface ThemeData {
  libraries: ThemeLibrary[];
  collections: ThemeCollection[];
  presets: Preset[];
  max: number;
  exceptions?: string[];
  maxExceptions?: number;
  warnings?: string[];
}

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const ENDPOINT = "/pse/themes/";
const kindLabel = (k: Kind) =>
  k === "show"
    ? L("Séries", "Shows")
    : k === "mixed"
      ? L("Films & séries", "Movies & shows")
      : L("Films", "Movies");

const fits = (c: ThemeCollection, type: Kind) =>
  type === "mixed" || c.mediaType === type || c.mediaType === "mixed";

/** Collections proposées pour une bibliothèque d'un type. */
export function collectionsFor(all: ThemeCollection[], type: Kind, taken: string[]) {
  return all.filter((c) => fits(c, type) && !taken.includes(c.id));
}

const mixedHint = () =>
  L(
    "Dans Plex, une bibliothèque Films & séries apparaît en deux parties : « Nom · Films » et « Nom · Séries ».",
    "In Plex, a Movies & shows library shows up in two parts: “Name · Films” and “Name · Séries”.",
  );

interface Draft {
  name: string;
  type: Kind;
  collections: string[];
  allLanguages?: boolean;
}

function Editor({
  draft,
  setDraft,
  collections,
}: {
  draft: Draft;
  setDraft: (d: Draft) => void;
  collections: ThemeCollection[];
}) {
  const byId = new Map(collections.map((c) => [c.id, c]));
  const choices = collectionsFor(collections, draft.type, draft.collections);
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <Input
          className="h-9 w-56"
          aria-label={L("Nom de la bibliothèque", "Library name")}
          placeholder={L("Nom (ex. Concerts)", "Name (e.g. Concerts)")}
          value={draft.name}
          maxLength={60}
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
        />
        <Select
          className="h-9 w-40 text-sm"
          aria-label={L("Type", "Type")}
          value={draft.type}
          onChange={(e) => {
            const type = e.target.value as Kind;
            // Les rangées qui ne conviennent plus au type sont retirées.
            const keep = draft.collections.filter((id) => {
              const c = byId.get(id);
              return !c || fits(c, type);
            });
            setDraft({ ...draft, type, collections: keep });
          }}
        >
          <option value="movie">{kindLabel("movie")}</option>
          <option value="show">{kindLabel("show")}</option>
          <option value="mixed">{kindLabel("mixed")}</option>
        </Select>
      </div>
      {draft.type === "mixed" ? <p className="text-xs text-fg-muted">{mixedHint()}</p> : null}
      <label className="flex items-start gap-2 text-xs text-fg-muted">
        <input
          type="checkbox"
          className="mt-0.5 size-4 accent-primary"
          checked={!!draft.allLanguages}
          onChange={(e) => setDraft({ ...draft, allLanguages: e.currentTarget.checked })}
        />
        <span>
          <span className="font-medium text-fg">{L("Toutes langues", "Any language")}</span>
          {" — "}
          {L(
            "ses titres ignorent le filtre de langue du catalogue (VO, VOSTFR…) et apparaissent aussi dans Films et Séries. Idéal pour les concerts et spectacles.",
            "its titles skip the catalogue's language filter (original, subtitled…) and also show in Movies and Shows. Ideal for concerts and shows.",
          )}
        </span>
      </label>
      {draft.collections.length ? (
        <ol className="flex flex-wrap gap-1.5">
          {draft.collections.map((id, i) => (
            <li key={id}>
              <Badge variant="outline" className="gap-1">
                {i + 1}. {byId.get(id)?.name ?? `#${id}`}
                {draft.type === "mixed" && byId.get(id) && byId.get(id)!.mediaType !== "mixed" ? (
                  <span className="text-fg-muted">
                    · {kindLabel(byId.get(id)!.mediaType as Kind)}
                  </span>
                ) : null}
                <button
                  type="button"
                  className="text-fg-muted hover:text-fg"
                  aria-label={L(
                    `Retirer ${byId.get(id)?.name ?? id}`,
                    `Remove ${byId.get(id)?.name ?? id}`,
                  )}
                  onClick={() =>
                    setDraft({ ...draft, collections: draft.collections.filter((x) => x !== id) })
                  }
                >
                  <X size={12} aria-hidden />
                </button>
              </Badge>
            </li>
          ))}
        </ol>
      ) : (
        <p className="text-xs text-fg-muted">
          {L("Aucune rangée : ajoutez une collection.", "No rows yet: add a collection.")}
        </p>
      )}
      <Select
        className="h-9 w-full max-w-md text-sm"
        aria-label={L("Ajouter une collection", "Add a collection")}
        value=""
        onChange={(e) => {
          if (e.target.value)
            setDraft({ ...draft, collections: [...draft.collections, e.target.value] });
        }}
      >
        <option value="">
          {L("+ Ajouter une collection en rangée…", "+ Add a collection as a row…")}
        </option>
        {choices.map((c) => (
          <option key={c.id} value={c.id}>
            {c.name}
            {draft.type === "mixed" && c.mediaType !== "mixed" ? ` · ${kindLabel(c.mediaType as Kind)}` : ""} (
            {c.items})
          </option>
        ))}
      </Select>
    </div>
  );
}

export function ThemeLibraries() {
  const doc = useAsync(() => apiFetch<ThemeData>(ENDPOINT), []);
  const [data, setData] = useState<ThemeData | null>(null);
  const [edits, setEdits] = useState<Record<number, Draft>>({});
  const [creating, setCreating] = useState<Draft>({ name: "", type: "movie", collections: [] });
  const [busy, setBusy] = useState(false);
  const current = data ?? doc.data ?? null;
  if (!current) return null;

  const send = async (path: string, method: string, body: object | null, ok: string) => {
    setBusy(true);
    try {
      const next = await apiFetch<ThemeData>(ENDPOINT + path, {
        method,
        headers: body ? { "Content-Type": "application/json" } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      });
      setData(next);
      if (next.warnings?.length) toast.warning(next.warnings.join(" · "));
      else toast.success(ok);
      return true;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const full = current.libraries.length >= current.max;

  return (
    <div id="pse-themes">
      <SettingsSection
        title={L("Bibliothèques à thème", "Themed libraries")}
        description={L(
          "Des bibliothèques Plex en plus de Films et Séries (Services, Studios & labels, Animes, Concerts…). Chaque rangée est une collection d'Ether : MDBList, Trakt, catalogue d'addon Stremio, service de streaming, studio… L'onglet Bibliothèque regroupe tous leurs titres. Seuls les titres disponibles selon vos réglages VF apparaissent, les plus récents d'abord. Rien n'est téléchargé.",
          "Plex libraries on top of Movies and Shows (Services, Studios & labels, Anime, Concerts…). Each row is an Ether collection: MDBList, Trakt, Stremio add-on catalog, streaming service, studio… The Library tab gathers all their titles. Only titles available under your French settings show up, newest first. Nothing is downloaded.",
        )}
      >
        <div className="space-y-4 px-4 py-4">
          <div className="space-y-2">
            <div className="text-sm font-medium">
              {L("Créer en un clic", "One-click libraries")}
            </div>
            <p className="text-xs text-fg-muted">
              {L(
                "Les collections nécessaires sont créées automatiquement. Le nom et les rangées restent modifiables ensuite.",
                "The needed collections are created for you. Name and rows stay editable afterwards.",
              )}
            </p>
            <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
              {current.presets.map((p) => {
                const exists = current.libraries.some((l) => l.name === p.name);
                return (
                  <div
                    key={p.id}
                    className="flex flex-col gap-2 rounded-lg border border-border bg-bg-subtle p-3"
                  >
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{p.name}</span>
                      <Badge>{kindLabel(p.type)}</Badge>
                      {p.allLanguages ? <Badge>{L("Toutes langues", "Any language")}</Badge> : null}
                    </div>
                    <p className="flex-1 text-xs text-fg-muted" title={p.rows.join(", ")}>
                      {p.summary}
                    </p>
                    <Button
                      size="sm"
                      variant={exists ? "outline" : undefined}
                      disabled={busy || full}
                      onClick={() =>
                        void send(
                          "preset",
                          "POST",
                          { preset: p.id, name: "" },
                          L(
                            `Bibliothèque ${p.name} créée ; ses collections se remplissent`,
                            `${p.name} library created; its collections are filling`,
                          ),
                        )
                      }
                    >
                      <Plus size={14} aria-hidden />{" "}
                      {exists ? L("Créer une autre", "Create another") : L("Créer", "Create")}
                    </Button>
                  </div>
                );
              })}
            </div>
          </div>

          <div className="space-y-2 rounded-lg border border-border p-3">
            <div className="text-sm font-medium">
              {L("Depuis un addon Stremio", "From a Stremio add-on")}
            </div>
            <p className="text-xs text-fg-muted">
              {L(
                "Choisissez un addon installé ou collez le lien de son manifest : chaque catalogue coché devient une rangée. Films et séries vont dans la même bibliothèque.",
                "Pick an installed add-on or paste its manifest link: each ticked catalog becomes a row. Movies and shows go in the same library.",
              )}
            </p>
            {full ? null : (
              <PseAddonCatalogImport
                onImported={() => {}}
                theme={{
                  label: L("Choisir un addon", "Pick an add-on"),
                  onCreated: (addon, ids, types) =>
                    void send(
                      "",
                      "POST",
                      {
                        name: addon.slice(0, 60),
                        type: types.length === 1 ? types[0] : "mixed",
                        collections: ids.slice(0, 60),
                      },
                      L(
                        `Bibliothèque « ${addon} » créée ; ses rangées se remplissent`,
                        `“${addon}” library created; its rows are filling`,
                      ),
                    ),
                }}
              />
            )}
          </div>

          {current.libraries.map((lib, i) => {
            const draft = edits[lib.id] ?? {
              name: lib.name,
              type: lib.type,
              collections: lib.collections,
              allLanguages: !!lib.allLanguages,
            };
            const changed =
              draft.name !== lib.name ||
              draft.type !== lib.type ||
              !!draft.allLanguages !== !!lib.allLanguages ||
              draft.collections.join(",") !== lib.collections.join(",");
            return (
              <div key={lib.id} className="space-y-2 rounded-lg border border-border p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="flex items-center gap-2 text-sm font-medium">
                    {lib.name} <Badge variant="secondary">{kindLabel(lib.type)}</Badge>
                    {lib.allLanguages ? (
                      <Badge variant="outline">{L("Toutes langues", "Any language")}</Badge>
                    ) : null}
                  </span>
                  <span className="flex gap-1">
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={L("Monter", "Move up")}
                      disabled={busy || i === 0}
                      onClick={() =>
                        void send(
                          `${lib.id}/move`,
                          "POST",
                          { dir: -1 },
                          L("Ordre enregistré", "Order saved"),
                        )
                      }
                    >
                      <ArrowUp size={14} aria-hidden />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={L("Descendre", "Move down")}
                      disabled={busy || i === current.libraries.length - 1}
                      onClick={() =>
                        void send(
                          `${lib.id}/move`,
                          "POST",
                          { dir: 1 },
                          L("Ordre enregistré", "Order saved"),
                        )
                      }
                    >
                      <ArrowDown size={14} aria-hidden />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={L(`Supprimer ${lib.name}`, `Delete ${lib.name}`)}
                      disabled={busy}
                      onClick={() => {
                        if (
                          window.confirm(
                            L(
                              `Supprimer la bibliothèque « ${lib.name} » ? Ses collections restent.`,
                              `Delete the “${lib.name}” library? Its collections stay.`,
                            ),
                          )
                        )
                          void send(
                            String(lib.id),
                            "DELETE",
                            null,
                            L("Bibliothèque supprimée", "Library deleted"),
                          );
                      }}
                    >
                      <Trash2 size={14} aria-hidden />
                    </Button>
                  </span>
                </div>
                <Editor
                  draft={draft}
                  setDraft={(d) => setEdits({ ...edits, [lib.id]: d })}
                  collections={current.collections}
                />
                <Button
                  size="sm"
                  disabled={busy || !changed}
                  onClick={() =>
                    void send(
                      String(lib.id),
                      "PUT",
                      draft,
                      L("Bibliothèque enregistrée", "Library saved"),
                    ).then((ok) => {
                      if (!ok) return;
                      const next = { ...edits };
                      delete next[lib.id];
                      setEdits(next);
                    })
                  }
                >
                  {L("Enregistrer", "Save")}
                </Button>
              </div>
            );
          })}

          <LanguageExceptions
            current={current}
            busy={busy}
            save={(ids) =>
              send(
                "exceptions",
                "PUT",
                { collections: ids },
                L("Exceptions de langue enregistrées", "Language exceptions saved"),
              )
            }
          />

          <div className="space-y-2 rounded-lg border border-dashed border-border p-3">
            <div className="text-sm font-medium">{L(
                "Bibliothèque personnalisée (à partir de vos collections)",
                "Custom library (from your collections)",
              )}</div>
            <p className="text-xs text-fg-muted">
              {L(
                "Créez d'abord les collections voulues dans Collections (liste MDBList ou Trakt, catalogue d'addon Stremio…), puis ajoutez-les ici comme rangées.",
                "First create the collections you want under Collections (MDBList or Trakt list, Stremio add-on catalog…), then add them here as rows.",
              )}
            </p>
            <Editor draft={creating} setDraft={setCreating} collections={current.collections} />
            <Button
              size="sm"
              disabled={busy || full || !creating.name.trim() || creating.collections.length === 0}
              onClick={() =>
                void send("", "POST", creating, L("Bibliothèque créée", "Library created")).then(
                  (ok) => ok && setCreating({ name: "", type: creating.type, collections: [] }),
                )
              }
            >
              <Plus size={14} aria-hidden /> {L("Créer la bibliothèque", "Create library")}
            </Button>
          </div>
        </div>
      </SettingsSection>
    </div>
  );
}

/**
 * Collections dont les titres échappent au filtre de langue du catalogue :
 * le catalogue reste en VF, sauf ces titres (une sélection en VOSTFR…).
 */
function LanguageExceptions({
  current,
  busy,
  save,
}: {
  current: ThemeData;
  busy: boolean;
  save: (ids: string[]) => Promise<boolean>;
}) {
  const saved = current.exceptions ?? [];
  const [draft, setDraft] = useState<string[] | null>(null);
  const ids = draft ?? saved;
  const byId = new Map(current.collections.map((c) => [c.id, c]));
  const choices = current.collections.filter((c) => !ids.includes(c.id));
  const changed = draft !== null && draft.join(",") !== saved.join(",");
  return (
    <div className="space-y-2 rounded-lg border border-border p-3">
      <div className="text-sm font-medium">
        {L("Exceptions de langue (VO, VOSTFR…)", "Language exceptions (original, subtitled…)")}
      </div>
      <p className="text-xs text-fg-muted">
        {L(
          "Le catalogue garde son filtre de langue (ex. VF uniquement), sauf pour les titres de ces collections, acceptés dans n'importe quelle langue. Pour quelques titres précis, le plus simple : onglet Films & séries › Titres en VOSTFR.",
          "The catalogue keeps its language filter (e.g. French only), except for the titles of these collections, accepted in any language. For a few specific titles, simplest: Movies & shows tab › Titles in original version.",
        )}
      </p>
      {ids.length ? (
        <ul className="flex flex-wrap gap-1.5">
          {ids.map((id) => (
            <li key={id}>
              <Badge variant="outline" className="gap-1">
                {byId.get(id)?.name ?? `#${id}`}
                <button
                  type="button"
                  className="text-fg-muted hover:text-fg"
                  aria-label={L(
                    `Retirer ${byId.get(id)?.name ?? id}`,
                    `Remove ${byId.get(id)?.name ?? id}`,
                  )}
                  onClick={() => setDraft(ids.filter((x) => x !== id))}
                >
                  <X size={12} aria-hidden />
                </button>
              </Badge>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-fg-muted">{L("Aucune exception.", "No exception.")}</p>
      )}
      <div className="flex flex-wrap gap-2">
        <Select
          className="h-9 w-full max-w-md text-sm"
          aria-label={L("Ajouter une exception", "Add an exception")}
          value=""
          onChange={(e) => {
            if (e.target.value) setDraft([...ids, e.target.value]);
          }}
        >
          <option value="">{L("+ Ajouter une collection…", "+ Add a collection…")}</option>
          {choices.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name} ({c.items})
            </option>
          ))}
        </Select>
        <Button
          size="sm"
          disabled={busy || !changed}
          onClick={() => void save(ids).then((ok) => ok && setDraft(null))}
        >
          {L("Enregistrer", "Save")}
        </Button>
      </div>
    </div>
  );
}
