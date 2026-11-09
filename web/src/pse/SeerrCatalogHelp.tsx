import { getLocale } from "../i18n";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export function SeerrCatalogHelp({ status }: { status?: Record<string, unknown> }) {
  const sync = status?.sync as { lastSync?: string; items?: number; error?: string } | undefined;
  return (
    <div className="space-y-2 text-sm text-fg-muted">
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
            "Renseignez ici son adresse et la clé, activez puis enregistrez. Les contenus déjà contrôlés doivent être revérifiés dans SeerrCatalog.",
            "Enter its address and token here, enable and save. Previously checked items need another check in SeerrCatalog.",
          )}
        </li>
        <li>
          {L(
            "Facultatif : dans SeerrCatalog, indiquez l’adresse externe de Remix suivie de /api/integrations/seerrcatalog/v1/events pour accélérer les mises à jour.",
            "Optional: in SeerrCatalog, enter the external Remix address followed by /api/integrations/seerrcatalog/v1/events for faster updates.",
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
