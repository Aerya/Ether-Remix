import { SourceHosting } from "./SourceHosting";
import { Button } from "@braedonsaunders/appkit-ui";
import { useEffect, useState, type ReactNode } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

const L = (fr: string, en: string) => (getLocale() === "fr" ? fr : en);
interface Addon {
  id: string;
  key: string;
  name: string;
  setupUrl: string;
  account: boolean;
  listed: boolean;
  changed: boolean;
}
interface Snapshot {
  addons: Addon[];
  updatedAt: string;
  error?: string;
}

export function MusicAddons({ fields }: { fields: { key: string; control: ReactNode }[] }) {
  const [state, setState] = useState<Snapshot | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  async function load(refresh = false) {
    setBusy(true);
    setError(false);
    try {
      setState(await apiFetch<Snapshot>("/pse/music/addons", { method: refresh ? "POST" : "GET" }));
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  return (
    <details className="border-t border-border" open>
      <summary className="cursor-pointer px-4 py-3 text-sm font-medium">
        {L("Sources communautaires Eclipse", "Eclipse community sources")}
      </summary>
      <div className="px-4 pb-3 text-sm text-fg-muted space-y-2">
        <p>
          {L(
            "Ouvrez le site du fournisseur, générez son manifest puis collez le lien ci-dessous et enregistrez. Un champ vide désactive la source. UltraMax et Deezer Ricky nécessitent votre compte Deezer sur leur site.",
            "Open the provider’s site, generate its manifest, paste the link below and save. An empty field disables the source. UltraMax and Deezer Ricky require your Deezer account on their site.",
          )}
        </p>
        <p>
          {L(
            "Le catalogue reste celui de Deezer : seuls les titres correspondants peuvent être lus. La qualité dépend du fournisseur et du titre ; utilisez le diagnostic pour vérifier le flux réel.",
            "The catalogue remains Deezer’s: only matching tracks can play. Quality depends on the provider and track; use diagnostics to verify the actual stream.",
          )}
        </p>
        <Button size="sm" variant="secondary" disabled={busy} onClick={() => void load(true)}>
          {L("Actualiser les liens de configuration", "Refresh setup links")}
        </Button>
        {state?.updatedAt && !state.updatedAt.startsWith("0001") ? (
          <p>
            {L("Registre vérifié : ", "Registry checked: ")}
            {new Date(state.updatedAt).toLocaleString()}
          </p>
        ) : null}
        {error || state?.error ? (
          <p role="status">
            {L(
              "Registre indisponible. Les liens déjà connus sont conservés.",
              "Registry unavailable. Previously known links are retained.",
            )}
          </p>
        ) : null}
        <p>
          {L(
            "Actualisation automatique toutes les 6 h si activée. Les manifests personnels ne sont jamais remplacés. Discord n’est pas surveillé.",
            "Automatic refresh every 6 hours when enabled. Personal manifests are never replaced. Discord is not monitored.",
          )}
        </p>
      </div>
      {fields.map((field) => {
        const addon = state?.addons.find((a) => a.key === field.key);
        return (
          <div key={field.key} className="border-t border-border">
            {addon ? (
              <div className="px-4 pt-3 text-sm">
                <a
                  className="underline"
                  href={addon.setupUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {L("Configurer ", "Configure ")}
                  {addon.name} ↗
                </a>
                <SourceHosting source={addon.key} publicInstance />
                {addon.account ? <span> · {L("Compte requis", "Account required")}</span> : null}
                {addon.changed ? (
                  <p role="status">
                    {L(
                      "Adresse différente de celle fournie avec Ether-Remix. Vérifiez le fournisseur avant de régénérer votre manifest.",
                      "Address differs from the one shipped with Ether-Remix. Verify the provider before regenerating your manifest.",
                    )}
                  </p>
                ) : null}
                {!addon.listed && state && !state.updatedAt.startsWith("0001") ? (
                  <p>
                    {L(
                      "Absent du registre actuel : dernière adresse connue conservée.",
                      "Absent from the current registry: retaining the last known address.",
                    )}
                  </p>
                ) : null}
              </div>
            ) : null}
            {field.control}
          </div>
        );
      })}
    </details>
  );
}
