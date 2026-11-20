# Seedstrem : lecture torrent à distance

[Guide Remix](GUIDE.fr.md) · [English](SEEDSTREM.en.md)

[Seedstrem](https://github.com/kipsilabs/seedstrem) est un addon Stremio auto-hébergé : il recherche via Prowlarr et diffuse les fichiers pendant leur téléchargement avec qBittorrent ou Deluge. Films, séries et animés dépendent des indexeurs et des types activés dans Seedstrem.

## Installation

1. Suivez les instructions du [dépôt officiel](https://github.com/kipsilabs/seedstrem). Configurez Prowlarr et un client torrent ; Seedstrem doit pouvoir lire les fichiers de ce client grâce à ses volumes et correspondances de chemins.
2. Définissez son adresse externe, joignable depuis Remix, de préférence en HTTPS. Protégez les routes du manifeste et de lecture : le mot de passe d’administration de Seedstrem ne protège pas à lui seul les routes Stremio. Un préfixe privé au reverse proxy peut être utilisé, avec ce même préfixe dans l’adresse externe.
3. Réservez une catégorie au client torrent. Choisissez explicitement les règles de partage, de nettoyage et la limite de disque ; laissez l’adoption des torrents existants et les téléchargements RSS désactivés si vous souhaitez uniquement des téléchargements à la demande.

Le trafic BitTorrent sort du **client torrent**. Un VPN configuré uniquement devant Remix ne protège donc pas ce trafic : configurez le réseau du client torrent selon vos besoins.

## Raccordement à Remix

Dans **Sources → Stremio → Seedstrem**, cliquez sur **Ajouter mon instance Seedstrem**, puis collez son manifeste, par exemple `https://seedstrem.example/ACCES_PRIVE/stremio/manifest.json`. Enregistrez l’addon et réglez sa priorité comme pour les autres sources Stremio. Le manifeste peut aussi être ajouté au formulaire générique.

Après enregistrement, **Modules Stremio** se déplie automatiquement et affiche la source. Le bloc **Seedstrem** reste une aide à l’ajout ; les modifications et suppressions se font dans la liste des modules.

Les recherches et la lecture empruntent l’instance configurée. Remix conserve les liens Seedstrem en HTTP : il ne transforme pas le magnet contenu dans ces liens en une demande à ses comptes débrideurs. Aucun dossier Seedstrem n’a besoin d’être monté dans Remix ; le client qBittorrent/Deluge et les fichiers peuvent se trouver sur une autre machine.

## Quand le téléchargement démarre-t-il ?

- Une recherche de sources ne lance pas le téléchargement.
- Ouvrir un lien de lecture Seedstrem peut ajouter le torrent au client et attendre les premières données.
- Remix n’ouvre les liens Seedstrem qu’à la lecture : les recherches, tests et contrôles de disponibilité restent sans téléchargement, même si l’ouverture des flux d’autres addons est autorisée. Un outil externe ouvrant directement un lien Seedstrem peut toujours lancer son téléchargement.

La vitesse de démarrage et les déplacements dans la vidéo dépendent des pairs et des morceaux déjà disponibles. Seedstrem reste une source à part du **Client torrent** intégré à Remix : inutile de configurer le même client dans les deux pour utiliser son manifeste.

## Lecture uniquement et dernier recours

Remix n’ouvre les liens Seedstrem qu’après une demande de lecture (ou sa reprise sur une autre release), jamais pour un diagnostic, un préchargement ou un contrôle de disponibilité, même si l’ouverture des autres flux d’addons est autorisée globalement. Un préchargement repris par une véritable lecture peut alors ouvrir le lien.

Dans le formulaire de la source Stremio, activez **Source de dernier recours à la lecture** pour essayer ses résultats seulement après l’échec des sources normales, indépendamment de leur qualité ou priorité. L’option est désactivée par défaut et s’applique par source Stremio. Une version explicitement imposée ou une release épinglée reste prioritaire.

La recherche et l’indexation continuent de consulter Seedstrem, sans téléchargement : ses releases restent des candidats, pas une preuve que le fichier est déjà téléchargé ou lisible. La recherche peut donc interroger Prowlarr via Seedstrem et directement si Prowlarr est également configuré dans Remix.


## Configuration rapide

Dans **Remix → Lecture → Priorités**, la [configuration rapide](QUICK_SOURCES.fr.md) place cette source dans un ordre strict. Elle conserve les réglages existants et reste désactivée par défaut. Les recherches et contrôles de disponibilité ne déclenchent aucun téléchargement Seedstrem.
