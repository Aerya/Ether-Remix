import { useEffect, useRef, useState } from "react";
import { Button, Input } from "@braedonsaunders/appkit-ui";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import type { StyleTarget } from "./DiscoveryCardStyles";
import { PACK_SOURCES, packMatches, parsePack, type PackImage } from "./artworkPacks";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export function DiscoveryArtworkPacks({
  targets,
  disabled,
  onApply,
}: {
  targets: (StyleTarget & { customized: boolean })[];
  disabled: boolean;
  onApply: (images: Record<string, string>) => void;
}) {
  const [source, setSource] = useState(PACK_SOURCES[0]!.id);
  const [url, setURL] = useState("");
  const [images, setImages] = useState<PackImage[]>([]);
  const [overrides, setOverrides] = useState<Record<string, string>>({});
  const [preserve, setPreserve] = useState(true);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [page, setPage] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const generation = useRef(0);
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );
  const definition = PACK_SOURCES.find((p) => p.id === source);
  const reset = () => {
    generation.current++;
    setImages([]);
    setOverrides({});
    setBusy(false);
    setError("");
    setMessage("");
  };
  async function load(file?: File) {
    const id = ++generation.current;
    setBusy(true);
    setError("");
    setImages([]);
    setOverrides({});
    setMessage("");
    try {
      if (file && file.size > 4 * 1024 * 1024) throw new Error();
      const value = file
        ? JSON.parse(await file.text())
        : await apiFetch<unknown>("/pse/discovery/pack", {
            method: "POST",
            body: JSON.stringify({ url: definition?.url ?? url }),
          });
      const parsed = parsePack(value, file ? undefined : definition);
      if (!parsed.length) throw new Error();
      if (id === generation.current) setImages(parsed);
    } catch {
      if (id === generation.current)
        setError(
          L(
            "Impossible de lire ce pack. Utilisez un fichier JSON de moins de 4 Mo ou son URL directe publique (une page de galerie ne suffit pas).",
            "Cannot read this pack. Use a JSON file under 4 MB or its public direct URL (a gallery page is not sufficient).",
          ),
        );
    } finally {
      if (id === generation.current) setBusy(false);
    }
  }
  const rows = targets.map((target) => {
    const matches = packMatches(images, target);
    const chosen = overrides[target.key] ?? (matches.length === 1 ? matches[0]!.url : "");
    return { target, matches, chosen, protected: preserve && target.customized };
  });
  const picked = Object.fromEntries(
    rows.filter((r) => !r.protected && r.chosen).map((r) => [r.target.key, r.chosen]),
  );
  return (
    <details className="rounded-lg border p-4 space-y-3">
      <summary className="cursor-pointer font-medium">
        {L("Packs de visuels", "Artwork packs")}
      </summary>
      <p className="text-sm text-fg-muted">
        {L(
          "Choisissez un pack, vérifiez les correspondances puis appliquez-le aux cartes sélectionnées. Les cartes sans correspondance gardent leur image.",
          "Choose a pack, review its matches and apply it to selected cards. Unmatched cards keep their artwork.",
        )}
      </p>
      <fieldset disabled={disabled || busy} className="flex flex-wrap items-end gap-3">
        <label>
          {L("Pack", "Pack")}
          <select
            className="block max-w-full rounded border bg-surface p-2"
            value={source}
            onChange={(e) => {
              reset();
              setSource(e.target.value);
            }}
          >
            {PACK_SOURCES.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
            <option value="import">
              {L("Importer un pack / export Nuvio", "Import a pack / Nuvio export")}
            </option>
          </select>
        </label>
        {definition ? (
          <a className="underline text-sm" href={definition.page} target="_blank" rel="noreferrer">
            {L("Voir la source", "View source")}
          </a>
        ) : (
          <label className="min-w-0 flex-1">
            {L("URL directe du JSON", "Direct JSON URL")}
            <Input
              type="url"
              value={url}
              onChange={(e) => {
                reset();
                setURL(e.target.value);
              }}
            />
          </label>
        )}
        <Button disabled={!definition && !url.startsWith("https://")} onClick={() => void load()}>
          {L("Prévisualiser le pack", "Preview pack")}
        </Button>
        {!definition && (
          <label>
            {L("Importer un fichier JSON", "Import JSON file")}
            <input
              className="block max-w-full"
              type="file"
              accept=".json,application/json"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void load(f);
                e.target.value = "";
              }}
            />
          </label>
        )}
      </fieldset>
      {busy && <p role="status">{L("Chargement du pack…", "Loading pack…")}</p>}
      {error && <p role="alert">{error}</p>}
      {!!images.length && (
        <>
          <div className="flex gap-3 overflow-x-auto">
            {images.slice(0, 6).map((i, n) => (
              <figure key={n} className="w-28 shrink-0">
                <img
                  src={i.url}
                  alt={i.name}
                  loading="lazy"
                  className="h-20 w-full rounded object-contain"
                  referrerPolicy="no-referrer"
                />
                <figcaption className="text-xs break-words">{i.name}</figcaption>
              </figure>
            ))}
          </div>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={preserve}
              disabled={disabled}
              onChange={(e) => setPreserve(e.target.checked)}
            />
            {L("Préserver mes visuels personnalisés", "Preserve my custom artwork")}
          </label>
          <p className="text-sm">
            {images.length} {L("visuels dans le pack", "images in this pack")} ·{" "}
            {Object.keys(picked).length}/{targets.length}{" "}
            {L("cartes prêtes à changer", "cards ready to change")}
          </p>
          <details onToggle={(e) => setReviewOpen(e.currentTarget.open)}>
            <summary className="cursor-pointer">
              {L("Vérifier ou ajuster les correspondances", "Review or adjust matches")}
            </summary>
            {reviewOpen && (
              <>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 my-3">
                  {rows
                    .slice(
                      Math.min(page, Math.max(0, Math.ceil(rows.length / 12) - 1)) * 12,
                      (Math.min(page, Math.max(0, Math.ceil(rows.length / 12) - 1)) + 1) * 12,
                    )
                    .map(({ target, matches, chosen, protected: protectedArt }) => (
                      <div key={target.key} className="min-w-0 rounded border p-3 space-y-2">
                        <p className="font-medium">{target.name}</p>
                        {chosen && !protectedArt && (
                          <img
                            src={chosen}
                            alt={target.name}
                            className="h-24 w-full object-contain"
                            loading="lazy"
                            referrerPolicy="no-referrer"
                          />
                        )}
                        <label className="block text-sm">
                          {protectedArt
                            ? L("Visuel personnalisé conservé", "Custom artwork preserved")
                            : matches.length > 1
                              ? L(
                                  "Plusieurs variantes : choisissez",
                                  "Multiple variants: choose one",
                                )
                              : L("Visuel du pack", "Pack artwork")}
                          <select
                            className="mt-1 block w-full min-w-0 rounded border bg-surface p-2"
                            aria-label={
                              L("Visuel pour ", "Artwork for ") +
                              target.name +
                              " (" +
                              target.key +
                              ")"
                            }
                            value={chosen}
                            disabled={disabled || protectedArt}
                            onChange={(e) =>
                              setOverrides({ ...overrides, [target.key]: e.target.value })
                            }
                          >
                            <option value="">
                              {L("Conserver l’image actuelle", "Keep current artwork")}
                            </option>
                            {images
                              .filter((i) => !i.group || i.group === target.group)
                              .map((i, n) => (
                                <option key={n} value={i.url}>
                                  {i.name}
                                  {matches.length > 1 && matches.some((m) => m.url === i.url)
                                    ? ` · ${n + 1}`
                                    : ""}
                                </option>
                              ))}
                          </select>
                        </label>
                      </div>
                    ))}
                </div>
                {rows.length > 12 && (
                  <div className="flex items-center gap-3">
                    <Button disabled={page <= 0} onClick={() => setPage(Math.max(0, page - 1))}>
                      {L("Précédent", "Previous")}
                    </Button>
                    <span>
                      {Math.min(page + 1, Math.ceil(rows.length / 12))}/
                      {Math.ceil(rows.length / 12)}
                    </span>
                    <Button
                      disabled={page >= Math.ceil(rows.length / 12) - 1}
                      onClick={() => setPage(page + 1)}
                    >
                      {L("Suivant", "Next")}
                    </Button>
                  </div>
                )}
              </>
            )}
          </details>
          <Button
            disabled={disabled || !Object.keys(picked).length}
            onClick={() => {
              onApply(picked);
              setMessage(
                L(
                  "Pack préparé. Enregistrez avec « Appliquer les cartes à mes accueils ».",
                  "Pack prepared. Save with ‘Apply cards to my home screens’.",
                ),
              );
            }}
          >
            {L("Appliquer ce pack", "Apply this pack")}
          </Button>
        </>
      )}
      {message && <p role="status">{message}</p>}
      <details className="text-sm">
        <summary className="cursor-pointer">
          {L("Autres sources et imports Nuvio", "More sources and Nuvio imports")}
        </summary>
        <p className="my-2">
          {L(
            "Importez un export JSON de collections pour en reprendre seulement les couvertures. Aucun addon ni catalogue n’est installé. Pour une couverture seule, copiez son URL dans « Personnaliser une carte ». Les GIF peuvent être affichés comme une image fixe selon le lecteur.",
            "Import a collections JSON export to reuse only its covers. No addon or catalogue is installed. For a single cover, paste its URL into ‘Customize a card’. GIFs may appear as still images depending on the player.",
          )}
        </p>
        <div className="flex flex-wrap gap-3">
          {[
            ["Nuvio · collections", "https://nuvio.tv/community-collections"],
            ["Nuvio · covers", "https://nuvio.tv/covers"],
            ["Kaptain", "https://kaptain.stream/"],
            ["Golden Catalogue", "https://github.com/0x8e8fb-p/nuvio-golden-catalogue"],
            ["MediUX", "https://mediux.pro/"],
          ].map(([name, href]) => (
            <a key={href} href={href} className="underline" target="_blank" rel="noreferrer">
              {name}
            </a>
          ))}
        </div>
      </details>
    </details>
  );
}
