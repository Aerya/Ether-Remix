# Client torrent (qBittorrent, rTorrent / ruTorrent)

Option de **Sources → Client torrent**, désactivée par défaut. Remix envoie les releases torrent trouvées sur vos indexeurs (Prowlarr, Jackett) à votre client BitTorrent, puis les lit une fois téléchargées : films, séries, animés et musique. Jusqu'à trois clients.

Contrairement aux comptes debrid, **les fichiers sont stockés sur le disque du client** et partagés selon vos limites. Remix ne supprime jamais un torrent ni un fichier.

## Dossier partagé : indispensable pour lire

Le client peut être sur une autre machine, mais **Remix lit les fichiers sur son propre disque**. Son conteneur doit donc voir le dossier de téléchargement du client : même machine, ou partage réseau monté (NFS, SMB). Avec une seedbox distante sans dossier partagé, l'ajout fonctionne mais la lecture non.

```yaml
services:
  accent:
    volumes:
      - /srv/torrents:/downloads:ro   # même dossier que le client
  qbittorrent:
    volumes:
      - /srv/torrents:/downloads
```

Si Remix ne voit pas le dossier au même chemin que le client, renseignez **Correspondance des dossiers** (options avancées), par exemple `/downloads=/data/torrents`. Remix ne lit rien en dehors des dossiers indiqués.

## Connexion au client

- **qBittorrent** : adresse de l'interface web.
  - Avec identifiants : `http://utilisateur:motdepasse@qbittorrent:8080`.
  - Avec une **clé d'API** (qBittorrent 5.2 et suivants, *Options › Interface web › Clé d'API*) : adresse sans identifiants et clé dans **Clé d'API qBittorrent**.
  - Sans rien, si qBittorrent dispense le réseau local d'authentification.
- **rTorrent / ruTorrent** : adresse XML-RPC, `http://utilisateur:motdepasse@rutorrent/RPC2` ou `…/plugins/rpc/rpc.php` de ruTorrent. rTorrent n'a pas de clé d'API.

Le mot de passe et la clé ne sont jamais réaffichés dans la console.

## Réglages

Client principal (`PSE_TORRENT_…`) ; clients 2 et 3 dans leurs propres blocs (`PSE_TORRENT2`, `PSE_TORRENT2_…`, `PSE_TORRENT3_…`, mêmes champs).

| Réglage | Variable | Rôle |
|---|---|---|
| Activer | `PSE_TORRENT=on` | Désactivé par défaut. Clients 2 et 3 : `PSE_TORRENT2=on`, `PSE_TORRENT3=on`. |
| Client | `PSE_TORRENT_CLIENT` | `qbittorrent` (défaut) ou `rtorrent`. |
| Adresse du client | `PSE_TORRENT_URL` | Voir « Connexion au client ». |
| Clé d'API qBittorrent | `PSE_TORRENT_KEY` | Remplace les identifiants (qBittorrent 5.2 et suivants). |
| Ratio | `PSE_TORRENT_RATIO` | Ex. `1`. Vide : réglage du client. |
| Durée de partage | `PSE_TORRENT_SEED_TIME` | Ex. `72h`. Vide : réglage du client. |
| Dossier de téléchargement | `PSE_TORRENT_SAVE_PATH` | Chemin vu par le client. Vide : celui du client. Sert aussi à mesurer la place libre. |
| Correspondance des dossiers | `PSE_TORRENT_PATH_MAP` | `dossier client=dossier Remix`, séparés par `;`. |
| Télécharger ce qui manque | `PSE_TORRENT_DOWNLOAD` | Activé par défaut. Désactivé : Remix lit seulement les torrents déjà complets. |
| Musique | `PSE_TORRENT_MUSIC` | Activé par défaut. Un album à la fois. |
| Limites par tracker | `PSE_TORRENT_TRACKERS` | Ex. `c411=2/72h; exemple.org=1; autre=/168h`. Voir plus bas. |
| Ajouts par jour | `PSE_TORRENT_MAX_DAY` | 10 par défaut, tous clients confondus. |
| Place libre minimum | `PSE_TORRENT_MIN_FREE` | En Go, 20 par défaut ; 0 : aucun contrôle. |
| cross-seed | `PSE_TORRENT_XSEED_URL`, `PSE_TORRENT_XSEED_KEY` | En option, ex. `http://cross-seed:2468` et la clé d'API (`cross-seed api-key`). |
| Catégorie | `PSE_TORRENT_CATEGORY` | `remix` par défaut (catégorie qBittorrent ou libellé ruTorrent), sur tous les clients. |

## Limites de partage

- Chaque torrent reçoit à l'ajout le ratio et la durée de son client.
- Une **règle de tracker** l'emporte, champ par champ : `c411=2/72h` donne ratio 2 et 72 h aux torrents dont une adresse d'annonce contient « c411 ». `exemple.org=1` ne change que le ratio, `autre=/168h` que la durée. La première règle qui correspond s'applique.
- La première limite atteinte (ratio ou durée) termine le partage, sans rien supprimer.
- qBittorrent applique lui-même ces limites ; son action à l'arrivée (pause, arrêt) reste celle réglée dans qBittorrent. Avec rTorrent, Remix retient les limites sur le torrent et arrête le partage toutes les 5 minutes quand l'une est atteinte.

## Plusieurs clients

- Un torrent complet sur n'importe quel client se lit.
- Un nouveau téléchargement part sur le premier client qui a la place et l'accepte : le principal, puis le 2, puis le 3.

## Place libre

- Remix mesure la place du dossier de téléchargement de chaque client, tel qu'il le voit (dossier réglé, sinon celui du client).
- Sous le seuil, Remix n'envoie plus rien à ce client. Si aucun client n'a la place, le lecteur affiche « Plus assez de place sur le client torrent ».
- Une alerte part une seule fois par les notifications d'Ether (événement « Fournisseur indisponible »), puis un message au retour de la place (« Fournisseur rétabli »). Activez ces événements dans les notifications d'Ether pour les recevoir (Discord, ntfy, e-mail…).
- La console indique la place libre de chaque client.

## Pendant le téléchargement

- La lecture commence une fois le torrent à 100 %, pas pendant.
- **Plex** affiche à la place de la vidéo : « Téléchargement en cours sur le client torrent : 42 %, encore 12 min environ. Relancez la lecture une fois terminé. »
- **Jellyfin et Emby** affichent le même message à l'écran, en plus de leur erreur de lecture.
- **La console** (Sources → Client torrent) liste les téléchargements de Remix : avancement, temps restant, taille, ratio et client. Bouton **Actualiser**.
- Rien ne prévient automatiquement à la fin du téléchargement : relancez la lecture.

## Fonctionnement

- **Debrid** : vos comptes debrid restent en « cache uniquement ». Seuls les clients torrent téléchargent.
- **Vérifications** : l'indexation en arrière-plan et les vérifications ne téléchargent jamais rien.
- **cross-seed** : prévenu à la fin de chaque téléchargement de la catégorie Remix, 3 torrents au plus toutes les 5 minutes. L'envoi est retenu sur le client (étiquette `remix-xseed` dans qBittorrent), pour ne jamais relancer deux fois les recherches sur les trackers.
- **Musique** : un album absent est envoyé au client, un seul toutes les 2 minutes, pour qu'une liste de lecture n'envoie pas des dizaines d'albums d'un coup.

## Limites

- Les magnets portent les trackers du `.torrent` d'origine : le client récupère les métadonnées auprès des pairs.
- En mode cluster, seul le nœud qui voit le dossier de téléchargement peut lire ces fichiers.
- Le plafond d'ajouts par jour est remis à zéro au redémarrage de Remix.
