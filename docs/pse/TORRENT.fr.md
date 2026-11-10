# Client torrent (qBittorrent, rTorrent / ruTorrent)

Option de **Sources → Client torrent**, désactivée par défaut. Remix envoie les releases torrent trouvées sur vos indexeurs (Prowlarr, Jackett) à votre client BitTorrent, puis les lit une fois téléchargées : films, séries, animés et musique.

Contrairement aux comptes debrid, **les fichiers sont stockés sur le disque du client** et partagés selon vos limites. Remix ne supprime jamais un torrent ni un fichier.

## Prérequis

Remix lit les fichiers terminés directement sur le disque : son conteneur doit voir le dossier de téléchargement du client, comme Radarr ou Sonarr.

```yaml
services:
  accent:
    volumes:
      - /srv/torrents:/downloads:ro   # même dossier que le client
  qbittorrent:
    volumes:
      - /srv/torrents:/downloads
```

Si les deux conteneurs ne montent pas le dossier au même chemin, renseignez **Correspondance des dossiers** (options avancées), par exemple `/downloads=/data/torrents`. Remix ne lit rien en dehors des dossiers indiqués.

## Réglages

| Réglage | Variable | Rôle |
|---|---|---|
| Activer | `PSE_TORRENT=on` | Désactivé par défaut. |
| Client | `PSE_TORRENT_CLIENT` | `qbittorrent` (défaut) ou `rtorrent`. |
| Adresse du client | `PSE_TORRENT_URL` | qBittorrent : `http://utilisateur:motdepasse@qbittorrent:8080`. rTorrent : adresse XML-RPC, `http://utilisateur:motdepasse@rutorrent/RPC2` ou `…/plugins/rpc/rpc.php` de ruTorrent. |
| Télécharger ce qui manque | `PSE_TORRENT_DOWNLOAD` | Activé par défaut. Désactivé, Remix lit seulement les torrents déjà complets sur le client. |
| Musique | `PSE_TORRENT_MUSIC` | Activé par défaut. Un album à la fois. |
| Ratio | `PSE_TORRENT_RATIO` | Ex. `1`. Vide : réglage du client. |
| Durée de partage | `PSE_TORRENT_SEED_TIME` | Ex. `72h`. Vide : réglage du client. |
| Ajouts par jour | `PSE_TORRENT_MAX_DAY` | 10 par défaut, pour protéger vos comptes trackers. |
| cross-seed | `PSE_TORRENT_XSEED_URL`, `PSE_TORRENT_XSEED_KEY` | En option, ex. `http://cross-seed:2468` et la clé d'API (`cross-seed api-key`). |
| Catégorie | `PSE_TORRENT_CATEGORY` | `remix` par défaut (catégorie qBittorrent ou libellé ruTorrent). |
| Dossier de téléchargement | `PSE_TORRENT_SAVE_PATH` | Chemin vu par le client. Vide : celui du client. |
| Correspondance des dossiers | `PSE_TORRENT_PATH_MAP` | `dossier client=dossier Remix`, séparés par `;`. |

## Fonctionnement

- **Lecture** : un torrent complet sur le client se lit comme un fichier en cache, y compris un torrent que vous avez ajouté vous-même. Quand aucune source n'est disponible, Remix envoie la meilleure release au client. Le lecteur indique que le téléchargement est lancé ; relancez la lecture une fois qu'il est terminé.
- **Debrid** : vos comptes debrid restent en « cache uniquement ». Seul le client torrent télécharge.
- **Vérifications** : l'indexation en arrière-plan et les vérifications ne téléchargent jamais rien.
- **Partage** : qBittorrent applique lui-même le ratio et la durée posés sur chaque torrent ; son action à l'arrivée (pause, arrêt) reste celle réglée dans qBittorrent. Avec rTorrent, Remix arrête le partage toutes les 5 minutes quand une limite est atteinte, sans rien supprimer. La première limite atteinte termine le partage, comme dans qBittorrent.
- **cross-seed** : prévenu à la fin de chaque téléchargement de la catégorie Remix, 3 torrents au plus toutes les 5 minutes. L'envoi est retenu sur le client (étiquette `remix-xseed` dans qBittorrent), pour ne jamais relancer deux fois les recherches sur les trackers.
- **Musique** : un album absent est envoyé au client, un seul toutes les 2 minutes, pour qu'une liste de lecture n'envoie pas des dizaines d'albums d'un coup. Il est lisible une fois téléchargé.

## Limites

- La lecture commence une fois le téléchargement terminé, pas pendant.
- Les magnets portent les trackers du `.torrent` d'origine : le client récupère les métadonnées auprès des pairs.
- En mode cluster, seul le nœud qui voit le dossier de téléchargement peut lire ces fichiers.
- Le plafond d'ajouts par jour est remis à zéro au redémarrage de Remix.
