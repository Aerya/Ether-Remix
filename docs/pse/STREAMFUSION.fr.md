# Base StreamFusion Reborn auto-hébergée

Option de **Sources → Stremio**. Elle met à jour le catalogue et la détection VF de Remix depuis votre propre base [StreamFusion Reborn](https://projetssd.github.io/ssdv2_docs/stream-fusion-reborn/installation/docker-compose-simple/#docker-compose-complet), en lecture seule. Elle ajoute aussi une source de recherche directe par film, saison et épisode dans le gestionnaire d’indexeurs de Remix. Les autres sources restent disponibles et les candidats passent par les vérifications habituelles de lecture. Aide : [Discord](https://discord.gg/B6GuEWxH4t).

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
- Un titre arrivé au catalogue après l'import de ses releases, par exemple via Tendances, est rattrapé : à chaque passage, Remix cherche dans SFR les titres sans release importée, par lots de 500 et avec les mêmes filtres. Chaque passage interroge au plus 10 000 titres et s'arrête après 2 minutes de lecture. Un titre vérifié sans résultat n'est redemandé qu'après 30 jours. Les nouveautés SFR de ce titre arrivent entre-temps par l'import habituel.
- Le rattrapage fonctionne sans index sur `tmdb_id`, mais chaque lot parcourt alors toute la table. Le compte en lecture seule n'a rien à créer.
- Les trackers privés sont exclus par défaut.
- L'import s'interrompt pendant les lectures.

## Disponibilité et lecture

L’import ne suppose plus qu’une release est en cache chez AllDebrid. Si la table `debrid_cache` est accessible, Remix utilise uniquement ses indications par service encore valides (`expires_at`). Sans table ou droit de lecture, la disponibilité reste inconnue ; les métadonnées peuvent néanmoins être importées. Pour autoriser cette lecture, si la table existe :

```sql
GRANT SELECT ON public.debrid_cache TO remix_lecture;
```

Les anciennes indications AllDebrid supposées sont retirées à la migration. Les preuves expirées sont ignorées à la lecture des données et les compteurs du catalogue sont corrigés dans la minute. Les titres dont les preuves ont expiré sont relus par lots, au plus une fois toutes les six heures et dans le budget du rattrapage ; l’absence de preuve ne signifie pas que le torrent est indisponible.

Quand cette intégration est activée, **StreamFusion Reborn (base)** rejoint automatiquement les sources de recherche. La recherche utilise l’identifiant TMDB et filtre les saisons/épisodes avant de limiter les résultats. Les packs de la saison demandée sont acceptés. Les requêtes sont bornées à quatre secondes et 200 lignes ; aucun appel de préchauffage à l’API StreamFusion n’est effectué. Les descripteurs bénéficient du cache de recherche de Remix, mais ne certifient pas une disponibilité debrid : celle-ci est revérifiée par les fournisseurs configurés avant lecture.

L’option trackers privés s’applique aussi à cette recherche. Les candidats sont des magnets par empreinte : un torrent privé absent du cache debrid peut nécessiter une source authentifiée supplémentaire pour être téléchargé par un client torrent.
