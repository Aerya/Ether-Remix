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
- Les vérifications d’arrière-plan de Remix n’ouvrent pas ces liens à la demande. Pour réserver les téléchargements à la lecture, laissez également désactivée l’option qui autorise l’ouverture des flux d’addons pendant les contrôles de disponibilité des clients. Un test manuel ouvrant le flux peut déclencher un téléchargement.

La vitesse de démarrage et les déplacements dans la vidéo dépendent des pairs et des morceaux déjà disponibles. Seedstrem reste une source à part du **Client torrent** intégré à Remix : inutile de configurer le même client dans les deux pour utiliser son manifeste.
