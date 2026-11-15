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
  const [testing, setTesting] = useState(false);
  const account = config.data?.debrid?.providers?.find((p) => p.kind === "alldebrid" && p.enabled);
  const dirty =
    draft.enabled !== saved?.enabled ||
    draft.url !== saved?.url ||
    draft.password !== saved?.password;
  const test = async () => {
    if (!account || !saved?.enabled || dirty) return;
    setTesting(true);
    setMessage("");
    try {
      const result = await api.testDebrid(account);
      setMessage(
        result.ok
          ? L(
              "Connexion réussie : AllDebrid répond via MediaFlow. Cela ne vérifie ni l’IP de sortie, ni la lecture vidéo, ni l’acceptation durable de cette IP.",
              "Connection successful: AllDebrid responds through MediaFlow. This does not verify the exit IP, video playback or continued acceptance of this IP.",
            )
          : L(
              "Échec du test via MediaFlow. Vérifiez le relais, son mot de passe, le tunnel Gluetun et le compte AllDebrid.",
              "Test through MediaFlow failed. Check the relay, its password, the Gluetun tunnel and your AllDebrid account.",
            ),
      );
    } catch {
      setMessage(
        L(
          "Test impossible. Vérifiez la connexion à Remix et réessayez.",
          "Unable to test. Check your connection to Remix and try again.",
        ),
      );
    } finally {
      setTesting(false);
    }
  };
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
          "Enregistré. Vous pouvez maintenant tester AllDebrid via MediaFlow.",
          "Saved. You can now test AllDebrid through MediaFlow.",
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
      <p className="text-sm text-fg-muted">
        {L(
          "Option globale pour tous les comptes AllDebrid de Remix : MediaFlow relaie les appels API et les fichiers vidéo ou audio récupérés par Remix pour ses clients. Vous pouvez configurer plus finement un proxy dans chaque compte débrideur, AllDebrid ou autre. Le proxy du compte est prioritaire sur cette option globale.",
          "Global option for all AllDebrid accounts in Remix: MediaFlow relays API calls and video or audio files fetched by Remix for its clients. You can configure a dedicated proxy in each debrid account, AllDebrid or otherwise. The account proxy takes precedence over this global option.",
        )}
      </p>
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
      <p className="text-sm text-fg-muted">
        {L(
          "MediaFlow n’est pas embarqué dans Remix. Installez MediaFlow et Gluetun avec le Compose du guide, ou réutilisez vos services existants. MediaFlow relaie les requêtes ; Gluetun assure leur sortie par le VPN ou le tunnel domestique. Raccordez ensuite Remix au réseau indiqué et renseignez ci-dessous l’adresse et le mot de passe API de MediaFlow.",
          "MediaFlow is not bundled with Remix. Install MediaFlow and Gluetun using the guide’s Compose, or reuse existing services. MediaFlow relays requests; Gluetun sends them through the VPN or home tunnel. Then attach Remix to the documented network and enter the MediaFlow address and API password below.",
        )}
      </p>
      <div className="flex flex-wrap gap-4 text-sm underline">
        <a
          href="https://github.com/Aerya/Ether-Remix/blob/main/docs/alldebrid-mediaflow.md#installation"
          target="_blank"
          rel="noopener noreferrer"
        >
          {L(
            "Installer MediaFlow + Gluetun : Compose et raccordement à Remix",
            "Install MediaFlow + Gluetun: Compose and Remix setup",
          )}
        </a>
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
          disabled={busy || testing}
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
          disabled={busy || testing}
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
          disabled={busy || testing}
          onChange={(e) => {
            setMessage("");
            setDraft({ ...draft, password: e.target.value });
          }}
        />
      </label>
      <p className="text-sm text-fg-muted">
        {L(
          "Sans proxy spécifique au compte, activer cette option fait passer les accès AllDebrid gérés par Remix par MediaFlow, sans repli direct en cas de panne. Configurez la même sortie pour tous les outils qui accèdent à AllDebrid sur cette machine, y compris les tâches automatiques. Un outil sans intégration MediaFlow peut utiliser le proxy Gluetun de cette même sortie. Les appels exécutés par les addons publics restent hors du contrôle de Remix.",
          "Without a dedicated account proxy, enabling this option routes AllDebrid access handled by Remix through MediaFlow, without direct fallback on failure. Configure the same exit for every tool accessing AllDebrid on this machine, including background jobs. Tools without MediaFlow integration can use the Gluetun proxy on that same exit. Calls made by public addons remain outside Remix's control.",
        )}
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          disabled={
            busy ||
            testing ||
            !config.data ||
            (draft.enabled && (!draft.url.trim() || !draft.password))
          }
          onClick={() => void save()}
        >
          {busy
            ? L("Enregistrement…", "Saving…")
            : L("Enregistrer la connexion", "Save connection")}
        </Button>
        <Button
          disabled={busy || testing || !saved?.enabled || dirty || !account}
          onClick={() => void test()}
        >
          {testing
            ? L("Test en cours…", "Testing…")
            : L("Tester AllDebrid via MediaFlow", "Test AllDebrid through MediaFlow")}
        </Button>
      </div>
      <p className="text-sm text-fg-muted">
        {!account
          ? L(
              "Ajoutez et activez un compte AllDebrid pour effectuer le test.",
              "Add and enable an AllDebrid account to run the test.",
            )
          : dirty || !saved?.enabled
            ? L(
                "Activez puis enregistrez la connexion MediaFlow avant de la tester.",
                "Enable and save the MediaFlow connection before testing it.",
              )
            : L(
                `Compte testé : ${account.name || account.id}. Le test utilise les réglages enregistrés.`,
                `Account tested: ${account.name || account.id}. The test uses saved settings.`,
              )}
      </p>
      {message && (
        <p role="status" className="text-sm">
          {message}
        </p>
      )}
    </Section>
  );
}
