import { useEffect, useRef, useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { artworkPreviewURL } from "../pages/collections/artwork";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export interface StyleTarget {
  key: string;
  name: string;
  group: string;
  entity: number;
}
export interface StyleImage {
  name: string;
  url: string;
  source: string;
  creator?: string;
  setId?: string;
  page?: string;
}
export function styleMatches(image: StyleImage, filter: string) {
  return (
    !filter.trim() ||
    [image.source, image.creator, image.setId, image.name].some((value) =>
      value?.toLowerCase().includes(filter.trim().toLowerCase()),
    )
  );
}
export function DiscoveryCardStyles({
  targets,
  disabled,
  onApply,
}: {
  targets: StyleTarget[];
  disabled: boolean;
  onApply: (images: Record<string, string>) => void;
}) {
  const signature = targets.map((t) => t.key).join("|");
  const [provider, setProvider] = useState("ThePosterDB");
  const [filter, setFilter] = useState("");
  const [excluded, setExcluded] = useState<string[]>([]);
  const [results, setResults] = useState<Record<string, StyleImage[]>>({});
  const [picked, setPicked] = useState<Record<string, string>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setResults({});
    setPicked({});
    setErrors({});
    setBusy(false);
    setMessage("");
    return () => {
      generation.current++;
    };
  }, [signature, provider]);
  const active = targets.filter((t) => !excluded.includes(t.key));
  async function search() {
    const run = ++generation.current;
    setBusy(true);
    setResults({});
    setPicked({});
    setErrors({});
    setMessage("");
    // Sequential requests bound provider pressure; country duplicates share one result.
    const cache = new Map<string, StyleImage[]>();
    for (const target of active) {
      if (run !== generation.current) return;
      try {
        const query = new URLSearchParams({
          provider,
          q: target.name,
          group: target.group,
          ...(target.group === "genres" ? { genreId: String(target.entity) } : {}),
        }).toString();
        let images = cache.get(query);
        if (!images) {
          images = (await apiFetch<{ images: StyleImage[] }>("/pse/discovery/gallery?" + query))
            .images;
          cache.set(query, images);
        }
        if (run !== generation.current) return;
        setResults((previous) => ({ ...previous, [target.key]: images! }));
      } catch (error) {
        if (run !== generation.current) return;
        setErrors((previous) => ({ ...previous, [target.key]: errorMessage(error) }));
      }
    }
    if (run === generation.current) setBusy(false);
  }
  return (
    <details className="rounded-lg border p-4 space-y-3">
      <summary className="cursor-pointer font-medium">
        {L("Style pour plusieurs cartes", "Style for multiple cards")}
      </summary>
      <p className="text-sm text-fg-muted">
        {L(
          "Choisissez une source et recherchez les visuels de vos cartes sélectionnées. Filtrez par créateur, ensemble ou nom de dossier, puis choisissez une variante par carte. Un même créateur ne garantit pas un style identique. Les cartes sans choix gardent leur visuel.",
          "Choose a source and search artwork for your selected cards. Filter by creator, set or folder name, then choose a variant for each card. The same creator does not guarantee the same style. Cards without a choice keep their artwork.",
        )}
      </p>
      <fieldset disabled={disabled || busy} className="space-y-3">
        <label>
          {L("Source du style", "Style source")}{" "}
          <select
            className="rounded border bg-surface p-2"
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
          >
            <option>ThePosterDB</option>
            <option value="github">GitHub · kometa-config-images</option>
            <option>ACdb</option>
            {targets.every((t) => t.group === "genres") && <option>MediUX</option>}
          </select>
        </label>
        <p className="text-sm">
          {L(
            "MediUX : genres uniquement. Les métadonnées de style dépendent de la source ; le filtre reste facultatif.",
            "MediUX: genres only. Style metadata depends on the source; filtering is optional.",
          )}
        </p>
        <div className="flex flex-wrap gap-3">
          {targets.map((t) => (
            <label key={t.key}>
              <input
                aria-label={
                  L("Inclure dans le style : ", "Include in style: ") + t.name + " (" + t.key + ")"
                }
                type="checkbox"
                checked={!excluded.includes(t.key)}
                onChange={(e) => {
                  setExcluded(
                    e.target.checked ? excluded.filter((k) => k !== t.key) : [...excluded, t.key],
                  );
                  setPicked({});
                }}
              />{" "}
              {t.name}
            </label>
          ))}
        </div>
        <Button disabled={!active.length || active.length > 30} onClick={() => void search()}>
          {L("Chercher les visuels du groupe", "Search group artwork")}
        </Button>
        <span className="text-sm">
          {" "}
          {active.length}/30 {L("cartes par recherche", "cards per search")}
        </span>
      </fieldset>
      {busy && (
        <p role="status">
          {L("Recherche en cours…", "Searching…")}{" "}
          <Button
            onClick={() => {
              generation.current++;
              setBusy(false);
            }}
          >
            {L("Arrêter", "Stop")}
          </Button>
        </p>
      )}
      <label className="block">
        {L("Filtrer le style / créateur / dossier", "Filter style / creator / folder")}
        <input
          className="block w-full rounded border bg-surface p-2"
          value={filter}
          onChange={(e) => {
            setFilter(e.target.value);
            setPicked({});
          }}
        />
      </label>
      <Button
        disabled={disabled || busy || !filter.trim()}
        onClick={() => {
          const matches: Record<string, string> = {};
          for (const target of active) {
            const candidates =
              results[target.key]?.filter((image) => styleMatches(image, filter)) ?? [];
            if (candidates.length === 1) matches[target.key] = candidates[0]!.url;
          }
          setPicked(matches);
        }}
      >
        {L("Sélectionner les correspondances uniques", "Select unique matches")}
      </Button>
      <p className="text-sm">
        {L(
          "Après filtrage, les correspondances uniques peuvent être sélectionnées ensemble. Choisissez manuellement les variantes ambiguës avant application.",
          "After filtering, unique matches can be selected together. Choose ambiguous variants manually before applying.",
        )}
      </p>
      <div className="space-y-4">
        {active.map((target) =>
          results[target.key] || errors[target.key] ? (
            <section key={target.key} className="rounded border p-3">
              <h4 className="font-medium">{target.name}</h4>
              {errors[target.key] && <p role="alert">{errors[target.key]}</p>}
              {results[target.key] &&
                !results[target.key]?.some((i) => styleMatches(i, filter)) && (
                  <p>
                    {L("Aucune correspondance : visuel conservé.", "No match: artwork preserved.")}
                  </p>
                )}
              <div className="flex gap-3 overflow-x-auto">
                {results[target.key]
                  ?.filter((i) => styleMatches(i, filter))
                  .map((image) => (
                    <label key={image.url} className="w-36 shrink-0 text-sm">
                      <img
                        src={artworkPreviewURL(image.url)}
                        alt={image.name}
                        className="h-24 w-36 object-contain"
                        loading="lazy"
                      />
                      <input
                        type="radio"
                        name={"style-" + target.key}
                        checked={picked[target.key] === image.url}
                        onChange={() => setPicked({ ...picked, [target.key]: image.url })}
                      />{" "}
                      {image.name}
                      <span className="block">
                        {image.creator || image.source}
                        {image.setId ? " · " + image.setId : ""}
                      </span>
                      {(image.creator || (provider === "github" && image.source.includes("/"))) && (
                        <Button
                          onClick={() => {
                            setFilter(
                              image.creator || image.source.slice(0, image.source.lastIndexOf("/")),
                            );
                            setPicked({});
                          }}
                        >
                          {L("Prendre comme référence", "Use as reference")}
                        </Button>
                      )}
                      {image.page && (
                        <a href={image.page} target="_blank" rel="noreferrer" className="underline">
                          {L("Voir la source", "View source")}
                        </a>
                      )}
                    </label>
                  ))}
              </div>
            </section>
          ) : null,
        )}
      </div>
      <Button
        disabled={disabled || busy || !Object.keys(picked).length}
        onClick={() => {
          onApply(picked);
          setPicked({});
          setMessage(
            L(
              "Visuels préparés. Enregistrez avec « Appliquer les cartes à mes accueils ».",
              "Artwork prepared. Save with ‘Apply cards to my home screens’.",
            ),
          );
        }}
      >
        {L("Utiliser les visuels choisis", "Use selected artwork")} ({Object.keys(picked).length})
      </Button>
      {message && <p role="status">{message}</p>}
    </details>
  );
}
