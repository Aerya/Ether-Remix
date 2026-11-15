import { useEffect, useState } from "react";
import { Alert, AlertDescription, Button, Input } from "@braedonsaunders/appkit-ui";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
import { api } from "../lib/api";
import { Section } from "../components/layout";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const empty = { enabled: false, url: "", password: "" };
export function AllDebridMediaFlow() {
  const config = useAsync(() => api.getConfig(), []);
  const saved = config.data?.outbound?.allDebridMediaFlow;
  const [draft, setDraft] = useState(saved ?? empty);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    setDraft(saved ?? empty);
  }, [saved]);
  const save = async () => {
    setBusy(true);
    setMessage("");
    try {
      const outbound = config.data?.outbound ?? {
        enabled: false,
        url: "",
        indexers: true,
        stremio: true,
        debrid: true,
        usenet: true,
        metadata: false,
      };
      await api.putSection("outbound", { ...outbound, allDebridMediaFlow: draft });
      await config.reload();
      setMessage(
        L(
          "Enregistré. Utilisez ensuite le bouton Tester du compte AllDebrid : il suivra cette connexion. Un test réussi ne garantit pas l'absence de sanction.",
          "Saved. Use the AllDebrid account's Test button next: it follows this connection. A successful test does not guarantee against account restrictions.",
        ),
      );
    } catch {
      setMessage(
        L(
          "Enregistrement impossible. Vérifiez l'adresse et le mot de passe MediaFlow.",
          "Could not save. Check the MediaFlow address and password.",
        ),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <Section
      id="alldebrid-mediaflow"
      title={L("AllDebrid : connexion et MediaFlow", "AllDebrid: connection and MediaFlow")}
      bodyClassName="space-y-4 p-4"
    >
      <Alert variant="warning">
        <AlertDescription>
          {L(
            "Sur un VPS ou un serveur distant, AllDebrid peut bloquer les IP professionnelles, de datacenters et certaines sorties VPN, notamment Cloudflare WARP. Des accès depuis plusieurs pays ou assimilés à du partage peuvent entraîner une restriction, une suspension ou un bannissement du compte.",
            "On a VPS or remote server, AllDebrid may block professional/datacenter IPs and some VPN exits, including Cloudflare WARP. Access from multiple countries or interpreted as account sharing may lead to restrictions, suspension or an account ban.",
          )}
        </AlertDescription>
      </Alert>
      <p className="text-sm text-fg-muted">
        {L(
          "MediaFlow doit lui-même sortir via Gluetun connecté à votre VPN ou tunnel domestique. L'installer seul sur le VPS ne change pas son IP de sortie. Aucun VPN ne garantit l'acceptation de toutes ses IP.",
          "MediaFlow must itself use Gluetun connected to your VPN or home tunnel. Installing it alone on the VPS does not change its exit IP. No VPN guarantees acceptance of every exit IP.",
        )}
      </p>
      <div className="flex flex-wrap gap-4 text-sm underline">
        <a href="https://alldebrid.fr/account/" target="_blank" rel="noopener noreferrer">
          {L(
            "Vérifier les fournisseurs VPN dans mon compte AllDebrid",
            "Check VPN providers in my AllDebrid account",
          )}
        </a>
        <a
          href="https://airvpn.org/?referred_by=483746"
          target="_blank"
          rel="sponsored noopener noreferrer"
        >
          {L("Découvrir AirVPN — lien affilié", "Explore AirVPN — affiliate link")}
        </a>
      </div>
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={draft.enabled}
          disabled={busy}
          onChange={(e) => {
            setMessage("");
            setDraft({ ...draft, enabled: e.target.checked });
          }}
        />
        {L("Utiliser MediaFlow pour AllDebrid", "Use MediaFlow for AllDebrid")}
      </label>
      <label className="block space-y-1">
        <span>{L("Adresse de MediaFlow", "MediaFlow address")}</span>
        <Input
          aria-label="MediaFlow URL"
          value={draft.url}
          disabled={busy}
          placeholder="http://mediaflow:8888"
          onChange={(e) => {
            setMessage("");
            setDraft({ ...draft, url: e.target.value });
          }}
        />
      </label>
      <label className="block space-y-1">
        <span>{L("Mot de passe API MediaFlow", "MediaFlow API password")}</span>
        <Input
          aria-label="MediaFlow API password"
          type="password"
          autoComplete="new-password"
          value={draft.password}
          disabled={busy}
          onChange={(e) => {
            setMessage("");
            setDraft({ ...draft, password: e.target.value });
          }}
        />
      </label>
      <p className="text-sm text-fg-muted">
        {L(
          "Une fois activé, les appels API AllDebrid et les fichiers AllDebrid récupérés par Remix passent par MediaFlow, sans repli direct en cas de panne. Configurez la même sortie pour tous les outils qui accèdent à AllDebrid sur cette machine, y compris les tâches automatiques. Un outil sans intégration MediaFlow peut utiliser le proxy Gluetun de cette même sortie. Les appels exécutés par les addons publics restent hors du contrôle de Remix.",
          "When enabled, AllDebrid API calls and AllDebrid files fetched by Remix use MediaFlow, without direct fallback on failure. Configure the same exit for every tool accessing AllDebrid on this machine, including background jobs. Tools without MediaFlow integration can use the Gluetun proxy on that same exit. Calls made by public addons remain outside Remix's control.",
        )}
      </p>
      <Button
        disabled={busy || !config.data || (draft.enabled && (!draft.url.trim() || !draft.password))}
        onClick={() => void save()}
      >
        {busy ? L("Enregistrement…", "Saving…") : L("Enregistrer la connexion", "Save connection")}
      </Button>
      {message && (
        <p role="status" className="text-sm">
          {message}
        </p>
      )}
    </Section>
  );
}
