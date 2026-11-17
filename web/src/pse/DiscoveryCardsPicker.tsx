import { Button, Input } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { artworkPreviewURL } from "../pages/collections/artwork";

import { DiscoveryArtworkPacks } from "./DiscoveryArtworkPacks";
import { DiscoveryCardStyles } from "./DiscoveryCardStyles";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const endpoint = "/pse/discovery/";
export interface NavigationCard {
  id: string;
  name: string;
  group: string;
  source: string;
  entity: number;
  accent: string;
}
interface Art {
  publicOnly?: boolean;
  mode: string;
  url?: string;
  logo?: string;
  accent?: string;
}
interface Choice {
  version?: string;
  countryGroup?: boolean;
  card: NavigationCard;
  region: string;
  artwork?: Art;
  collectionId?: string;
}
interface State {
  enabled: boolean;
  cards: NavigationCard[];
  choices?: Choice[];
}
const choiceKey = (c: Choice) => c.card.id + (c.card.group === "services" ? ":" + c.region : "");
const countryCodes =
  "FR BE CH CA US GB DE ES IT PT NL AT IE LU MC AD AU NZ JP KR IN BR MX AR CL CO PE ZA MA DZ TN EG TR IL AE SA SE NO DK FI IS PL CZ SK HU RO BG HR SI RS UA GR CY MT LT LV EE ID MY SG TH PH VN HK TW".split(
    " ",
  );
const countryName = (code: string) => {
  try {
    return new Intl.DisplayNames([getLocale()], { type: "region" }).of(code) ?? code;
  } catch {
    return code;
  }
};
const picture = (c: Choice) =>
  c.artwork?.mode === "custom"
    ? artworkPreviewURL(c.artwork.url)
    : c.artwork?.logo
      ? artworkPreviewURL(c.artwork.logo)
      : undefined;

export function DiscoveryCardsPicker() {
  const state = useAsync(() => apiFetch<State>(endpoint), []);
  const [choices, setChoices] = useState<Choice[] | null>(null);
  const [family, setFamily] = useState("services");
  const [region, setRegion] = useState("FR");
  const [groupOverride, setGroupCountries] = useState<boolean | null>(null);
  const installedServices = state.data?.choices?.filter((c) => c.card.group === "services") ?? [];
  const groupCountries =
    groupOverride ??
    (installedServices.length ? installedServices.some((c) => c.countryGroup) : true);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Choice[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [dirtyArt, setDirtyArt] = useState<Set<string>>(new Set());
  const selected = choices ?? state.data?.choices ?? [];
  const starterCards = state.data?.cards ?? [];
  const [onlySelected, setOnlySelected] = useState(false);
  // Load the complete regional service catalogue without requiring a hidden extra step.
  useEffect(() => {
    setResults(null);
    setQuery("");
    if (family !== "services") return;
    let cancelled = false;
    setSearching(true);
    setError("");
    apiFetch<{ cards: NavigationCard[]; logos: Record<string, string> }>(
      endpoint + "catalog?" + new URLSearchParams({ kind: "watchProvider", region, q: "" }),
    )
      .then((data) => {
        if (!cancelled)
          setResults(
            data.cards.map((card) => ({
              card,
              region,
              artwork: { mode: "auto", logo: data.logos?.[card.id], accent: card.accent },
            })),
          );
      })
      .catch((e) => {
        if (!cancelled) setError(errorMessage(e));
      })
      .finally(() => {
        if (!cancelled) setSearching(false);
      });
    return () => {
      cancelled = true;
    };
  }, [family, region]);
  const visible: Choice[] = onlySelected
    ? selected.filter(
        (c) => c.card.group === family && (family !== "services" || c.region === region),
      )
    : results !== null
      ? results
      : starterCards.filter((c) => c.group === family).map((card) => ({ card, region }));
  const filtered = visible.filter(
    (c) =>
      (/^\d+$/.test(query.trim()) && results !== null) ||
      c.card.name.toLocaleLowerCase().includes(query.toLocaleLowerCase()),
  );
  function toggle(c: Choice) {
    const key = choiceKey(c);
    setChoices(
      selected.some((x) => choiceKey(x) === key)
        ? selected.filter((x) => choiceKey(x) !== key)
        : [...selected, c],
    );
  }
  function changeArt(key: string, artwork: Art) {
    setChoices(selected.map((c) => (choiceKey(c) === key ? { ...c, artwork } : c)));
    setDirtyArt(new Set([...dirtyArt, key]));
  }
  async function search() {
    setSearching(true);
    setError("");
    try {
      const data = await apiFetch<{ cards: NavigationCard[]; logos: Record<string, string> }>(
        endpoint +
          "catalog?" +
          new URLSearchParams({
            kind: family === "services" ? "watchProvider" : "company",
            region,
            q: query,
          }),
      );
      setResults(
        data.cards.map((card) => ({
          card,
          region,
          artwork: { mode: "auto", logo: data.logos[card.id], accent: card.accent },
        })),
      );
      if (!data.cards.length) setMessage(L("Aucun résultat TMDB.", "No TMDB results."));
      else setMessage("");
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSearching(false);
    }
  }
  async function apply(restore = false) {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await apiFetch(
        endpoint,
        restore
          ? { method: "DELETE" }
          : {
              method: "POST",
              body: JSON.stringify({
                groupCountries,
                choices: selected.map((c) => ({
                  card: c.card,
                  region: c.region,
                  ...(dirtyArt.has(choiceKey(c)) ? { artwork: c.artwork } : {}),
                })),
              }),
            },
      );
      await state.reload();
      setChoices(null);
      setDirtyArt(new Set());
      setGroupCountries(null);
      setMessage(
        restore
          ? L(
              "Accueil précédent rétabli. Les collections sont conservées.",
              "Previous home restored. Collections are retained.",
            )
          : L(
              "Cartes enregistrées. Les collections se remplissent en arrière-plan ; actualisez votre client.",
              "Cards saved. Collections fill in the background; refresh your client.",
            ),
      );
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const editingChoice = selected.find((c) => choiceKey(c) === editing);
  const previewGroups = new Map<string, Choice[]>();
  for (const c of selected) {
    const title =
      c.card.group === "services"
        ? "Services" + (groupCountries ? " · " + countryName(c.region) : "")
        : c.card.group === "studios"
          ? L("Studios et labels", "Studios and labels")
          : "Genres";
    previewGroups.set(title, [...(previewGroups.get(title) ?? []), c]);
  }
  return (
    <div className="space-y-4">
      <p className="text-sm">
        {L(
          "Bêta facultative : remplace les recommandations Plex et Jellyfin/Emby par vos cartes. Le rendu dépend du client. Vous pouvez rétablir l’accueil précédent.",
          "Optional beta: replaces Plex and Jellyfin/Emby recommendations with your cards. Rendering depends on the client. You can restore the previous home.",
        )}
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant="outline"
          disabled={busy}
          onClick={() => setChoices(starterCards.map((card) => ({ card, region })))}
        >
          {L("Sélection de démarrage", "Starter selection")}
        </Button>
        <Button variant="outline" disabled={busy} onClick={() => setChoices([])}>
          {L("Vider la sélection", "Clear selection")}
        </Button>
        <span className="text-sm" aria-live="polite">
          {selected.length} / 200 {L("cartes sélectionnées", "selected cards")}
        </span>
      </div>
      <fieldset disabled={busy || searching} className="space-y-4">
        <legend className="sr-only">{L("Choisir les cartes", "Choose cards")}</legend>
        <div className="flex flex-wrap gap-2">
          {[
            ["services", "Services"],
            ["studios", L("Studios et labels", "Studios and labels")],
            ["genres", "Genres"],
          ].map(([id, label]) => (
            <Button
              key={id}
              variant={family === id ? "default" : "outline"}
              aria-pressed={family === id}
              onClick={() => setFamily(id!)}
            >
              {label}
            </Button>
          ))}
        </div>
        {family === "services" && (
          <div className="space-y-2">
            <label className="block text-sm">
              {L("Catalogue du pays", "Country catalog")}
              <select
                className="ml-2 rounded border border-border bg-surface p-2"
                aria-label={L("Pays des services", "Services country")}
                value={countryCodes.includes(region) ? region : "custom"}
                onChange={(e) => setRegion(e.target.value === "custom" ? "" : e.target.value)}
              >
                {countryCodes.map((c) => (
                  <option key={c} value={c}>
                    {countryName(c)} ({c})
                  </option>
                ))}
                <option value="custom">{L("Autre pays…", "Other country…")}</option>
              </select>
            </label>
            {!countryCodes.includes(region) && (
              <Input
                aria-label={L("Code pays ISO", "ISO country code")}
                value={region}
                maxLength={2}
                onChange={(e) => setRegion(e.target.value.toUpperCase())}
                placeholder="FR"
                className="max-w-32"
              />
            )}
            <p className="text-xs text-fg-muted">
              {L(
                "Pays de disponibilité du catalogue, pas origine du studio. Ajoutez des services d’un pays, puis passez au suivant : votre sélection est conservée. Données TMDB / JustWatch.",
                "Catalog availability country, not studio origin. Add services from one country, then switch to another: your selection is retained. TMDB / JustWatch data.",
              )}
            </p>
          </div>
        )}
        <div className="flex flex-wrap gap-2">
          <Input
            aria-label={L("Rechercher une carte", "Search cards")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={L("Nom ou identifiant TMDB…", "Name or TMDB ID…")}
            className="min-w-40 flex-1"
          />
          {family !== "genres" && (
            <Button
              variant="outline"
              onClick={() => void search()}
              disabled={!/^[A-Z]{2}$/.test(region) || (family === "studios" && !query.trim())}
            >
              {searching
                ? L("Recherche…", "Searching…")
                : family === "services"
                  ? L("Charger les services", "Load services")
                  : L("Rechercher sur TMDB", "Search TMDB")}
            </Button>
          )}
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={onlySelected}
            onChange={(e) => setOnlySelected(e.target.checked)}
          />
          {L("Afficher uniquement ma sélection", "Show only my selection")}
        </label>
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
          {filtered.map((c) => {
            const key = choiceKey(c);
            const installed = selected.find((x) => choiceKey(x) === key);
            const displayed = installed
              ? { ...c, ...installed, artwork: installed.artwork ?? c.artwork }
              : c;
            return (
              <div
                key={key}
                className={`min-w-0 overflow-hidden rounded-lg border border-border ${editing === key ? "col-span-full" : ""}`}
              >
                <div className={editing === key ? "max-w-xs" : ""}>
                  <label
                    className="relative flex aspect-video items-center justify-center gap-2 p-3 text-center text-white font-medium"
                    style={{
                      background: `linear-gradient(135deg, ${c.card.accent || "#31566a"}, #10131d)`,
                    }}
                  >
                    <input
                      aria-label={c.card.name}
                      type="checkbox"
                      className="absolute top-2 left-2 z-10"
                      checked={!!installed}
                      onChange={() => toggle(c)}
                    />
                    {picture(displayed) ? (
                      <img
                        src={picture(displayed)}
                        alt={c.card.name}
                        className="max-h-full max-w-full object-contain"
                      />
                    ) : (
                      c.card.name
                    )}
                  </label>
                  <div className="p-2 text-sm">
                    {c.card.name}
                    {family === "services" && ` · ${c.region}`}
                  </div>
                  <Button
                    variant="ghost"
                    disabled={!installed}
                    className="w-full"
                    aria-expanded={editing === key}
                    onClick={() => setEditing(editing === key ? null : key)}
                  >
                    {L("Personnaliser une carte", "Customize a card")}
                  </Button>
                </div>
                {editing === key && editingChoice && (
                  <ArtworkPicker
                    key={editing}
                    choice={editingChoice}
                    onChange={(art) => changeArt(key, art)}
                    onClose={() => setEditing(null)}
                  />
                )}
              </div>
            );
          })}
        </div>
        {!filtered.length && (
          <p className="text-sm text-fg-muted">
            {L(
              "Aucune carte affichée. Lancez une recherche ou changez de filtre.",
              "No cards displayed. Run a search or change the filter.",
            )}
          </p>
        )}
      </fieldset>
      <DiscoveryArtworkPacks
        key={family}
        disabled={busy}
        targets={selected
          .filter((c) => c.card.group === family)
          .map((c) => ({
            key: choiceKey(c),
            name: c.card.name,
            group: c.card.group,
            entity: c.card.entity,
            customized: !!c.artwork && c.artwork.mode !== "auto",
          }))}
        onApply={(images) => {
          setChoices(
            selected.map((c) =>
              images[choiceKey(c)]
                ? { ...c, artwork: { mode: "custom", url: images[choiceKey(c)], publicOnly: true } }
                : c,
            ),
          );
          setDirtyArt(new Set([...dirtyArt, ...Object.keys(images)]));
        }}
      />
      <DiscoveryCardStyles
        key={family}
        disabled={busy}
        targets={selected
          .filter((c) => c.card.group === family)
          .map((c) => ({
            key: choiceKey(c),
            name: c.card.name,
            group: c.card.group,
            entity: c.card.entity,
          }))}
        onApply={(images) => {
          setChoices(
            selected.map((c) =>
              images[choiceKey(c)]
                ? { ...c, artwork: { mode: "custom", url: images[choiceKey(c)] } }
                : c,
            ),
          );
          setDirtyArt(new Set([...dirtyArt, ...Object.keys(images)]));
        }}
      />
      <details className="rounded-lg border border-border p-3" open>
        <summary className="cursor-pointer font-medium">
          {L("Aperçu de mes cartes", "My cards preview")}
        </summary>
        <label className="my-3 flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={groupCountries}
            disabled={busy}
            onChange={(e) => setGroupCountries(e.target.checked)}
          />
          {L(
            "Regrouper les services par pays sur les accueils",
            "Group services by country on home screens",
          )}
        </label>
        {[...previewGroups].map(([name, cards]) => (
          <div key={name} className="mb-3">
            <h4 className="mb-2 font-medium">{name}</h4>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
              {cards.map((c) => (
                <button
                  key={choiceKey(c)}
                  aria-label={c.card.name}
                  className="rounded border border-border p-2 text-sm"
                  onClick={() => {
                    setFamily(c.card.group);
                    setRegion(c.region);
                    setQuery("");
                    setOnlySelected(true);
                    setEditing(choiceKey(c));
                  }}
                >
                  {picture(c) || c.collectionId ? (
                    <img
                      className="mb-1 aspect-video w-full object-contain"
                      alt={c.card.name}
                      src={
                        picture(c) ||
                        `/ether/api/collections/${encodeURIComponent(c.collectionId!)}/artwork?w=300&v=${encodeURIComponent(c.version ?? "")}`
                      }
                    />
                  ) : (
                    <span
                      className="flex aspect-video items-center justify-center rounded text-white"
                      style={{ background: `linear-gradient(135deg, ${c.card.accent}, #10131d)` }}
                    >
                      {c.card.name}
                    </span>
                  )}
                  {c.card.name}
                  {c.card.group === "services" ? ` · ${c.region}` : ""}
                </button>
              ))}
            </div>
          </div>
        ))}
      </details>
      <p className="text-xs text-fg-muted">
        {L(
          "Films et séries ont des collections séparées, limitées à 100 titres chacune. Les collections vides sont masquées. L’application ajoute les cartes manquantes ; décocher ne supprime pas une collection déjà créée. Les visuels modifiés ici sont appliqués aux deux collections ; les autres personnalisations sont conservées.",
          "Movies and shows have separate collections, limited to 100 titles each. Empty collections are hidden. Applying adds missing cards; unchecking does not delete an existing collection. Artwork edited here applies to both collections; other customizations are preserved.",
        )}
      </p>
      <div className="flex flex-wrap gap-3">
        <Button
          disabled={busy || !selected.length || selected.length > 200}
          onClick={() => void apply()}
        >
          {busy
            ? L("Enregistrement…", "Saving…")
            : L("Appliquer les cartes à mes accueils", "Apply cards to my home screens")}
        </Button>
        {state.data?.enabled && (
          <Button variant="outline" disabled={busy} onClick={() => void apply(true)}>
            {L("Rétablir l’accueil précédent", "Restore previous home")}
          </Button>
        )}
        <Link className="self-center text-sm underline" to="/collections">
          {L("Gérer les collections créées", "Manage created collections")}
        </Link>
      </div>
      {message && (
        <p role="status" className="text-sm">
          {message}
        </p>
      )}
      {(!!error || !!state.error) && (
        <p role="alert" className="text-sm text-danger">
          {error || errorMessage(state.error)}
        </p>
      )}
    </div>
  );
}

interface GalleryImage {
  name: string;
  url: string;
  source: string;
  page?: string;
}
function ArtworkPicker({
  choice,
  onChange,
  onClose,
}: {
  choice: Choice;
  onChange: (art: Art) => void;
  onClose: () => void;
}) {
  const [source, setSource] = useState(choice.artwork?.mode === "custom" ? "import" : "auto");
  const [url, setURL] = useState(
    choice.artwork?.url?.startsWith("data:") ? "" : (choice.artwork?.url ?? ""),
  );
  const [query, setQuery] = useState(choice.card.name);
  const [images, setImages] = useState<GalleryImage[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const preview = picture(choice);
  async function gallery() {
    setBusy(true);
    setError("");
    try {
      const data = await apiFetch<{ images: GalleryImage[] }>(
        endpoint +
          "gallery?" +
          new URLSearchParams({
            q: query,
            provider: source,
            group: choice.card.group,
            genreId:
              choice.card.group === "genres" && query === choice.card.name
                ? String(choice.card.entity)
                : "",
          }),
      );
      setImages(data.images);
      if (!data.images.length)
        setError(
          L(
            "Aucun visuel trouvé. Essayez un autre nom ou un import.",
            "No artwork found. Try another name or an import.",
          ),
        );
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function upload(file?: File) {
    if (!file) return;
    setError("");
    setBusy(true);
    try {
      const data = await resizeArtwork(file);
      onChange({ mode: "custom", url: data });
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section
      className="space-y-3 rounded-lg border border-primary/40 p-4"
      aria-label={L("Choix du visuel", "Artwork picker")}
    >
      <div className="flex items-center justify-between gap-3">
        <h4 className="font-medium">
          {L("Visuel", "Artwork")} · {choice.card.name}
        </h4>
        <Button variant="ghost" onClick={onClose}>
          {L("Fermer", "Close")}
        </Button>
      </div>
      {preview && (
        <img
          src={preview}
          alt={choice.card.name}
          onError={() =>
            setError(
              L(
                "Image inaccessible ou URL non directe : choisissez un fichier ou une autre URL.",
                "Image unavailable or URL is not a direct image: choose a file or another URL.",
              ),
            )
          }
          onLoad={() => setError("")}
          className="aspect-video w-full max-w-sm rounded-lg border border-border object-contain"
        />
      )}
      <label className="block text-sm">
        {L("Source du visuel", "Artwork source")}
        <select
          aria-label={L("Source du visuel", "Artwork source")}
          className="ml-2 max-w-full rounded border border-border bg-surface p-2"
          value={source}
          disabled={busy}
          onChange={(e) => {
            setSource(e.target.value);
            setImages([]);
            setError("");
            if (e.target.value === "auto")
              onChange({ mode: "auto", accent: choice.card.accent, logo: choice.artwork?.logo });
          }}
        >
          <option value="auto">
            {L("Automatique · TMDB / mosaïque", "Automatic · TMDB / mosaic")}
          </option>
          <option value="github">GitHub · kometa-config-images</option>
          {["ThePosterDB", "ACdb", "MediUX"].map((name) => (
            <option key={name} value={name}>
              {name} · {L("galerie", "gallery")}
            </option>
          ))}
          <option value="import">{L("Importer · fichier ou URL", "Import · file or URL")}</option>
        </select>
      </label>
      {source === "MediUX" && (
        <p className="text-sm">
          {L(
            "MediUX : galerie du genre sélectionné ; utilisez un nom anglais pour rechercher un autre genre. Les studios et services ne sont pas couverts par ce connecteur.",
            "MediUX: selected genre gallery; use an English name to search another genre. This connector does not cover studios or services.",
          )}
        </p>
      )}
      <p className="text-xs text-fg-muted">
        {L(
          "Fanart.tv : recherche intégrée de cartes non disponible. L’import fichier/URL reste possible.",
          "Fanart.tv: integrated navigation card search is unavailable. File/URL import remains available.",
        )}
      </p>
      {["github", "ThePosterDB", "ACdb", "MediUX"].includes(source) && (
        <>
          <div className="flex flex-wrap gap-2">
            <Input
              aria-label={L("Rechercher un visuel", "Search artwork")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <Button
              variant="outline"
              disabled={busy || query.trim().length < 2}
              onClick={() => void gallery()}
            >
              {L("Chercher des visuels", "Search artwork")}
            </Button>
          </div>
          <a
            className="text-sm underline"
            href={
              source === "github"
                ? "https://github.com/nichtlegacy/kometa-config-images"
                : source === "ACdb"
                  ? "https://acdb.tv/library-posters/"
                  : source === "MediUX"
                    ? "https://mediux.pro/genres"
                    : "https://theposterdb.com/"
            }
            target="_blank"
            rel="noreferrer"
          >
            {source === "github" ? "nichtlegacy · kometa-config-images" : source}
          </a>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
            {images.map((image) => (
              <div key={image.url}>
                <button
                  className="rounded border border-border p-2 text-xs"
                  onClick={() => onChange({ mode: "custom", url: image.url })}
                >
                  <img
                    src={image.url}
                    alt={image.name}
                    className="aspect-video w-full object-contain"
                    loading="lazy"
                  />
                  <span className="break-words">{image.source}</span>
                </button>
                {image.page && (
                  <a
                    href={image.page}
                    target="_blank"
                    rel="noreferrer"
                    className="block text-xs underline"
                  >
                    {L("Voir la source et le créateur", "View source and creator")}
                  </a>
                )}
              </div>
            ))}
          </div>
        </>
      )}
      {source === "import" && (
        <>
          <p className="text-xs text-fg-muted">
            {L(
              "Alternative aux galeries : choisissez une image sur le site, puis importez son fichier ou son URL directe.",
              "Alternative to galleries: choose an image on the website, then import its file or direct URL.",
            )}
          </p>
          <div className="flex flex-wrap gap-3 text-sm">
            {[
              ["ThePosterDB", "https://theposterdb.com/"],
              ["MediUX", "https://mediux.pro/"],
              ["Fanart.tv", "https://fanart.tv/"],
              ["ACdb", "https://acdb.tv/art-department"],
            ]
              .filter(([name]) => source === "import" || name === source)
              .map(([name, href]) => (
                <a key={name} href={href} target="_blank" rel="noreferrer" className="underline">
                  {name}
                </a>
              ))}
          </div>
          <div className="flex flex-wrap gap-2">
            <Input
              aria-label={L("URL directe de l’image", "Direct image URL")}
              value={url}
              onChange={(e) => setURL(e.target.value)}
              placeholder="https://…/image.jpg"
              className="flex-1"
            />
            <Button
              variant="outline"
              disabled={!/^https?:\/\//.test(url)}
              onClick={() => onChange({ mode: "custom", url })}
            >
              {L("Prévisualiser cette image", "Preview this image")}
            </Button>
          </div>
          <label className="block text-sm">
            {L(
              "Importer une image (JPEG, PNG, WebP · 10 Mo maximum)",
              "Upload an image (JPEG, PNG, WebP · up to 10 MB)",
            )}
            <input
              aria-label={L("Importer une image", "Upload an image")}
              type="file"
              accept="image/jpeg,image/png,image/webp"
              disabled={busy}
              className="mt-2 block max-w-full"
              onChange={(e) => void upload(e.target.files?.[0])}
            />
          </label>
        </>
      )}
      <p className="text-xs text-fg-muted">
        {L(
          "Le choix est prévisualisé ici et enregistré avec « Appliquer les cartes ». Les images importées par fichier sont réduites et conservées dans Remix. Respectez les conditions de la source et les droits des créateurs.",
          "Your choice is previewed here and saved with Apply cards. Uploaded files are resized and stored in Remix. Respect source terms and creator rights.",
        )}
      </p>
      {error && (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      )}
    </section>
  );
}

export async function resizeArtwork(file: File): Promise<string> {
  if (
    !["image/jpeg", "image/png", "image/webp"].includes(file.type) ||
    file.size > 10 * 1024 * 1024
  )
    throw new Error(
      L("Image non prise en charge ou trop volumineuse.", "Unsupported or oversized image."),
    );
  const bitmap = await createImageBitmap(file);
  try {
    const ratio = Math.min(1, 1200 / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * ratio));
    canvas.height = Math.max(1, Math.round(bitmap.height * ratio));
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("Canvas unavailable");
    ctx.fillStyle = "#10131d";
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    for (const quality of [0.85, 0.7, 0.5, 0.35]) {
      const data = canvas.toDataURL("image/jpeg", quality);
      if (data.length < 340000) return data;
    }
    throw new Error(
      L(
        "Image trop détaillée : réduisez sa taille avant l’import.",
        "Image too detailed: reduce its size before uploading.",
      ),
    );
  } finally {
    bitmap.close();
  }
}
