# Base StreamFusion Reborn auto-hébergée

Option de **Sources → Stremio**. Elle met à jour le catalogue et la détection VF de Remix depuis votre propre base [StreamFusion Reborn](https://projetssd.github.io/ssdv2_docs/stream-fusion-reborn/installation/docker-compose-simple/#docker-compose-complet), en lecture seule. La lecture continue de passer par vos sources. Aide : [Discord](https://discord.gg/B6GuEWxH4t).

## Adapter le compose

1. Créez un compte en lecture seule (adaptez le conteneur, la base et le mot de passe) :

   ```bash
   docker exec -it sfr-stremio-postgres psql -U streamfusion -d streamfusion -c "CREATE ROLE remix_lecture LOGIN PASSWORD 'change-moi'; GRANT CONNECT ON DATABASE streamfusion TO remix_lecture; GRANT USAGE ON SCHEMA public TO remix_lecture; GRANT SELECT ON public.torrent_items TO remix_lecture;"
   ```

2. Ajoutez le réseau privé de StreamFusion au compose de Remix, puis recréez le conteneur. `docker network ls` donne le nom exact :

   ```yaml
   services:
     accent:
       networks:
         - stream-fusion_private

   networks:
     stream-fusion_private:
       external: true
   ```

   Gardez aussi les réseaux déjà présents. N'exposez jamais PostgreSQL sur Internet.

3. Dans la console, renseignez l'adresse, par exemple :

   ```text
   postgres://remix_lecture:change-moi@sfr-stremio-postgres:5432/streamfusion
   ```

## Fonctionnement

- Le premier import lit la base par lots et reprend après un redémarrage. Les suivants ne lisent que les nouveautés, toutes les 6 heures par défaut (minimum 30 minutes).
- Seules les releases françaises, MULTi ou VOSTFR de titres présents au catalogue sont gardées. Une série est représentée par son premier épisode ou un pack de la saison 1.
- Les trackers privés sont exclus par défaut.
- L'import s'interrompt pendant les lectures.
