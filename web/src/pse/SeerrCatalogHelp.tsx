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
          "Teste les réglages enregistrés. Enregistrez vos modifications avant de tester. Aucun média n’est importé par ce test.",
          "Tests saved settings. Save your changes before testing. This test does not import media.",
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
      <ol className="list-decimal pl-5 space-y-1">
        <li>
          {L(
            "Dans SeerrCatalog : Paramètres → Intégration Ether Remix, générez la clé puis activez l’export.",
            "In SeerrCatalog: Settings → Ether Remix integration, generate the token and enable export.",
          )}
        </li>
        <li>
          {L(
            "Renseignez son adresse de base (ex. https://seerrcatalog.example, sans /api/…), ainsi que la clé, activez puis enregistrez et testez la connexion. Les contenus déjà contrôlés doivent être revérifiés dans SeerrCatalog.",
            "Enter its base address (e.g. https://seerrcatalog.example, without /api/…) and token, enable, save and test the connection. Previously checked items need another check in SeerrCatalog.",
          )}
        </li>
      </ol>
      <p>
        {L(
          "Disponibilité valable 26 h, liens réutilisés au maximum 30 min ; recherche habituelle à la lecture si les sources sont périmées ou échouent. Un épisode disponible ne garantit pas toute la série. Utilisez HTTPS hors réseau privé.",
          "Availability lasts 26 h, links are reused for at most 30 min; playback falls back to normal discovery when sources expire or fail. One available episode does not guarantee the entire series. Use HTTPS outside a private network.",
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
