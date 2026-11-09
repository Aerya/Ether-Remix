import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const COMPOSE =
  "https://projetssd.github.io/ssdv2_docs/stream-fusion-reborn/installation/docker-compose-simple/#docker-compose-complet";
const DISCORD = "https://discord.gg/B6GuEWxH4t";
const ROLE = `docker exec -it sfr-stremio-postgres psql -U streamfusion -d streamfusion -c "CREATE ROLE remix_lecture LOGIN PASSWORD 'change-moi'; GRANT CONNECT ON DATABASE streamfusion TO remix_lecture; GRANT USAGE ON SCHEMA public TO remix_lecture; GRANT SELECT ON public.torrent_items TO remix_lecture;"`;
const NETWORK = `services:
  accent:
    networks:
      - stream-fusion_private

networks:
  stream-fusion_private:
    external: true`;

/** Étapes pour relier Remix à sa propre instance StreamFusion Reborn. */
export function SfrHelp() {
  return (
    <details className="border-t border-border px-4 py-3 text-sm">
      <summary className="cursor-pointer font-medium">
        {L("Comment adapter votre compose", "How to adapt your compose")}
      </summary>
      <div className="mt-3 space-y-3">
        <p>
          <a className="text-primary underline" href={COMPOSE} rel="noreferrer" target="_blank">
            {L("Installer StreamFusion Reborn", "Install StreamFusion Reborn")}
          </a>
          {" · "}
          <a className="text-primary underline" href={DISCORD} rel="noreferrer" target="_blank">
            Discord
          </a>
        </p>
        <p>{L("1. Créez un compte en lecture seule :", "1. Create a read-only account:")}</p>
        <pre className="overflow-x-auto rounded bg-muted p-2 text-xs">{ROLE}</pre>
        <p>
          {L(
            "2. Ajoutez à Remix le réseau privé de StreamFusion, puis recréez le conteneur. Adaptez le nom du service et du réseau (`docker network ls`). N'exposez jamais PostgreSQL sur Internet.",
            "2. Add StreamFusion's private network to Remix, then recreate the container. Adjust the service and network names (`docker network ls`). Never expose PostgreSQL to the Internet.",
          )}
        </p>
        <pre className="overflow-x-auto rounded bg-muted p-2 text-xs">{NETWORK}</pre>
        <p>
          {L(
            "3. Renseignez l'adresse ci-dessus, avec le nom du conteneur PostgreSQL comme hôte.",
            "3. Fill in the address above, using the PostgreSQL container name as host.",
          )}
        </p>
      </div>
    </details>
  );
}
