import { SourceHosting } from "./SourceHosting";
import { Button } from "@braedonsaunders/appkit-ui";
import { ExternalLink, Plus } from "lucide-react";
import { useState } from "react";
import { getLocale } from "../i18n";
import { IndexerList } from "../pages/sources/Indexers";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

// Suggestions seulement : tout addon Stremio peut être ajouté par son manifest.
export const ANIME_ADDON_PROVIDERS = [
  { id: "uwu", name: "UwU+", configure: "https://uwu.nipva.com/" },
] as const;

export function AnimeSources({
  onAdd,
}: { onAdd?: (request: { nonce: number; type: "stremio"; name?: string }) => void } = {}) {
  const [add, setAdd] = useState<{ nonce: number; type: "stremio"; name?: string } | null>(null);
  const request = (value: { nonce: number; type: "stremio"; name?: string }) => {
    if (onAdd) onAdd(value);
    else setAdd(value);
  };
  return (
    <section
      className="space-y-4"
      aria-label={L("Fournisseurs Stremio suggérés", "Suggested Stremio providers")}
    >
      <div className="rounded-xl border border-border bg-surface p-5 space-y-4">
        <div>
          <h2 className="font-semibold">{L("Fournisseurs Stremio", "Stremio providers")}</h2>
          <p className="mt-1 text-sm text-fg-muted">
            {L(
              "Vos critères de langue habituels (FR/MULTi…) restent appliqués. TMDB fournit les fiches et les collections. Vos addons Stremio trouvent les flux : ajoutez-en plusieurs et réglez leur priorité dans la liste ci-dessous.",
              "Your usual language criteria (French/MULTi…) still apply. TMDB provides title details and collections. Your Stremio addons find streams: add several and set their priority in the list below.",
            )}
          </p>
        </div>
        {ANIME_ADDON_PROVIDERS.map((provider) => (
          <div key={provider.id} className="rounded-lg border border-border p-4 space-y-3">
            <div className="font-medium">{provider.name}</div>
            <SourceHosting source={provider.id} publicInstance />
            <p className="text-sm text-fg-muted">
              {L(
                "Configurez les sources et vos préférences VF/VOSTFR chez le fournisseur, puis collez le lien manifest.json généré dans Ether-Remix. UwU+ fournit les flux ; les bibliothèques se gèrent dans Contenus et bibliothèques.",
                "Configure sources and your audio/subtitle preferences with the provider, then paste the generated manifest.json link in Ether-Remix. UwU+ provides streams; manage libraries under Content & libraries.",
              )}
            </p>
            <div className="flex flex-wrap gap-2">
              <a
                href={provider.configure}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-sm text-primary underline underline-offset-4"
              >
                {L("Configurer UwU+", "Configure UwU+")} <ExternalLink size={14} aria-hidden />
              </a>
              <Button
                variant="outline"
                size="sm"
                onClick={() => request({ nonce: Date.now(), type: "stremio", name: provider.name })}
              >
                <Plus size={14} aria-hidden /> {L("Ajouter UwU+", "Add UwU+")}
              </Button>
            </div>
          </div>
        ))}
        <Button variant="outline" onClick={() => request({ nonce: Date.now(), type: "stremio" })}>
          <Plus size={14} aria-hidden />{" "}
          {L("Ajouter un autre addon Stremio", "Add another Stremio addon")}
        </Button>
        <p className="text-xs text-fg-muted">
          {L(
            "Ces sources sont partagées par tous les contenus compatibles. Chaque addon est interrogé selon les types et identifiants qu’il accepte ; sa désactivation s’applique partout.",
            "These sources are shared by all compatible content. Each addon is queried for the types and identifiers it supports; disabling it applies everywhere.",
          )}
        </p>
      </div>
      {!onAdd ? <IndexerList view="stremio" addRequest={add} /> : null}
    </section>
  );
}
