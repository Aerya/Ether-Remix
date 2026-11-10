import { useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export function SeerrCatalogHelp({ status }: { status?: Record<string, unknown> }) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<string>();
  const messages: Record<string, string> = {
    ok: L(
      "Connexion réussie : clé acceptée et flux de disponibilités valide.",
      "Connection successful: token accepted and availability feed valid.",
    ),
    configuration: L(
      "Enregistrez une adresse de base valide et une clé partagée avant de tester.",
      "Save a valid base address and shared token before testing.",
    ),
    unauthorized: L(
      "Accès refusé : vérifiez la clé partagée et activez l’export dans SeerrCatalog.",
      "Access denied: check the shared token and enable export in SeerrCatalog.",
    ),
    not_found: L(
      "Flux introuvable : vérifiez l’adresse de base et la version de SeerrCatalog.",
      "Feed not found: check the base address and SeerrCatalog version.",
    ),
    unreachable: L(
      "Serveur inaccessible depuis Remix ou délai dépassé. Vérifiez l’adresse et la connexion réseau.",
      "Server unreachable from Remix or request timed out. Check the address and network connection.",
    ),
    unavailable: L(
      "SeerrCatalog renvoie une erreur HTTP. Vérifiez le service puis réessayez.",
      "SeerrCatalog returned an HTTP error. Check the service and retry.",
    ),
    invalid_feed: L(
      "Réponse incompatible : le flux SeerrCatalog attendu n’a pas été reçu.",
      "Incompatible response: the expected SeerrCatalog feed was not received.",
    ),
  };
  async function testConnection() {
    setBusy(true);
    setResult(undefined);
    try {
      const response = await apiFetch<{ code: string }>("/pse/seerrcatalog/test", {
        method: "POST",
      });
      setResult(messages[response.code] ?? messages.invalid_feed);
    } catch {
      setResult(
        L(
          "Test impossible : vérifiez votre connexion à la console Remix puis réessayez.",
          "Test failed: check your Remix console connection and retry.",
        ),
      );
    } finally {
      setBusy(false);
    }
  }
  const sync = status?.sync as { lastSync?: string; items?: number; error?: string } | undefined;
  return (
    <div className="space-y-2 px-4 py-3 text-sm text-fg-muted">
      <Button disabled={busy} onClick={testConnection}>
        {busy ? L("Test en cours…", "Testing…") : L("Tester la connexion", "Test connection")}
      </Button>
      <p>
        {L(
          "Enregistrez avant de tester. Ce test vérifie le partage des disponibilités, sans importer de média.",
          "Save before testing. This test checks availability sharing without importing media.",
        )}
      </p>
      <p role="status" aria-live="polite">
        {result}
      </p>
      <p>
        <a
          className="underline"
          href="https://github.com/Aerya/Stremio-Seerr-Catalog"
          target="_blank"
          rel="noopener noreferrer"
        >
          SeerrCatalog · GitHub
        </a>
        {" · "}
        <a
          className="underline"
          href="https://github.com/Aerya/Ether-Remix"
          target="_blank"
          rel="noopener noreferrer"
        >
          Ether Remix · GitHub
        </a>
      </p>
      <p>
        {L(
          "Les disponibilités confirmées restent valides jusqu’à suppression. À la lecture, Remix recherche des sources si les liens ont expiré ou échouent.",
          "Confirmed availability stays valid until deletion. Playback falls back to normal discovery when links expire or fail.",
        )}
      </p>
      {sync?.lastSync && (
        <p>
          {L("Dernière synchronisation : ", "Last sync: ")}
          {new Date(sync.lastSync).toLocaleString()} · {sync.items ?? 0} {L("contenus", "items")}
        </p>
      )}
      {sync?.error && (
        <p role="status">
          {L(
            "Synchronisation impossible ; nouvel essai dans une minute.",
            "Synchronization failed; retrying in one minute.",
          )}
        </p>
      )}
    </div>
  );
}
