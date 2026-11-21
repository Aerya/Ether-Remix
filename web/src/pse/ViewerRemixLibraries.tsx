import { Switch } from "@braedonsaunders/appkit-ui";
import { SettingsRow } from "../components/SettingsRow";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import type { Library } from "../pages/libraries/api";
import type { ThemeData, ThemeLibrary } from "./ThemeLibraries";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

/** Numéro de section Plex de la bibliothèque Remix : 5100 + son identifiant. */
export const remixSection = (theme: ThemeLibrary) => 5100 + theme.id;

/**
 * 5100 n'est jamais un vrai thème : sa présence marque un choix Remix explicite
 * (tout décocher = aucun thème). Il n'est jamais affiché comme case.
 */
export const REMIX_MARKER = 5100;
const inRemixRange = (id: number) => id >= REMIX_MARKER && id <= 5299;

/** Bibliothèque d'Ether (Films / Séries) dont dépend la visibilité d'une bibliothèque Remix. */
function baseLibraryIds(theme: ThemeLibrary, libs: Library[]): number[] {
  const want = theme.type === "mixed" ? ["movie", "show"] : [theme.type];
  return libs.filter((l) => want.includes(l.mediaType ?? "")).map((l) => l.id);
}

/**
 * Bibliothèques Remix cochées. Un réglage restreint sans aucun numéro Remix
 * (antérieur à ces cases) garde la visibilité d'origine : un thème est coché
 * quand sa bibliothèque de base l'est. Sinon, la liste fait foi (un numéro
 * orphelin n'a pas de case et est simplement ignoré).
 */
export function remixChecked(current: number[], themes: ThemeLibrary[], libs: Library[]) {
  const legacy = !current.some(inRemixRange);
  const out = new Set<number>();
  for (const th of themes) {
    const on = legacy
      ? baseLibraryIds(th, libs).some((id) => current.includes(id))
      : current.includes(remixSection(th)) || current.includes(remixSection(th) + 100);
    if (on) out.add(remixSection(th));
  }
  return out;
}

/** Bibliothèques Remix du serveur. */
export function usePseRemixThemes() {
  return useAsync(() => apiFetch<ThemeData>("/pse/themes/"), []);
}

/**
 * La liste enregistrée : bibliothèques d'Ether, plus les Remix cochées (5100+ID).
 * Sans bibliothèque Remix, la liste reste telle quelle (aucun marqueur ajouté).
 */
const withRemix = (themes: ThemeLibrary[], ids: number[], on: Set<number>) =>
  themes.length === 0
    ? ids
    : [...ids.filter((x) => !inRemixRange(x)), REMIX_MARKER, ...on].sort((a, b) => a - b);

/** Nouvelle liste après (dé)cochage d'une bibliothèque d'Ether. */
export function pseToggleLibrary(
  base: number[],
  id: number,
  on: boolean,
  themes: ThemeLibrary[],
  libs: Library[],
) {
  const ids = on ? [...base.filter((x) => x !== id), id] : base.filter((x) => x !== id);
  return withRemix(themes, ids, remixChecked(base, themes, libs)).sort((a, b) => a - b);
}

/** Nouvelle liste après (dé)cochage d'une bibliothèque Remix. */
export function pseToggleRemix(
  base: number[],
  section: number,
  on: boolean,
  themes: ThemeLibrary[],
  libs: Library[],
) {
  const set = remixChecked(base, themes, libs);
  if (on) set.add(section);
  else set.delete(section);
  return withRemix(themes, base, set);
}

/** Liste après « Toutes les bibliothèques » décoché : tout reste accordé. */
export function pseAllLibraries(themes: ThemeLibrary[], libs: Library[]) {
  return [
    ...(themes.length > 0 ? [REMIX_MARKER] : []),
    ...libs.map((l) => l.id),
    ...themes.map(remixSection),
  ].sort((a, b) => a - b);
}

/** Cases des bibliothèques Remix d'un spectateur restreint. */
export function PseRemixLibraryRows({
  themes,
  checked,
  onToggle,
}: {
  themes: ThemeLibrary[];
  checked: Set<number>;
  onToggle: (section: number, on: boolean) => void;
}) {
  return (
    <>
      <h4 className="px-5 pt-4 text-sm font-medium text-fg">
        {L("Bibliothèques Remix", "Remix libraries")}
      </h4>
      {themes.map((th) => (
        <SettingsRow
          key={`remix-${th.id}`}
          title={th.name}
          control={
            <Switch
              aria-label={th.name}
              checked={checked.has(remixSection(th))}
              onChange={(e) => onToggle(remixSection(th), e.target.checked)}
            />
          }
        />
      ))}
    </>
  );
}

/** Message affiché quand les bibliothèques Remix n'ont pas pu être lues. */
export function PseRemixLoadError() {
  return (
    <p role="alert" className="px-5 py-2 text-sm text-danger">
      {L(
        "Impossible de charger les bibliothèques Remix : l'enregistrement est désactivé.",
        "Couldn't load the Remix libraries, so saving is disabled.",
      )}
    </p>
  );
}
