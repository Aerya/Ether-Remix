# Limiter le recours aux débrideurs, voire préférer le téléchargement & streaming BitTorrent/Usenet

[Guide Remix](GUIDE.fr.md) · [English](QUICK_SOURCES.en.md)

Dans **Remix → Lecture → Priorités**, activez **Configuration rapide**, désactivée par défaut. Cochez une ou plusieurs sources, puis ordonnez-les librement avec **↑ / ↓** ou par glisser-déposer : toutes les sources peuvent être déplacées, cochées ou non, et une source cochée prend la place où elle se trouve. Les sources cochées sont essayées de haut en bas. Cliquez sur **Enregistrer**. Les choix, l’ordre complet et l’instance Seedstrem sont conservés après redémarrage. Désactiver la configuration rapide retrouve le fonctionnement avancé sans effacer ses réglages.

| Source | Configuration réutilisée |
| --- | --- |
| StreamFusion Reborn — Alkodiques uniquement | Base PostgreSQL dans Sources → Stremio et compte AllDebrid actif. Uniquement les fichiers `sfr-alko:` vérifiés, jamais les torrents ordinaires, magnets ou recherches de cache. |
| Usenet | Fournisseurs NNTP et indexeurs Usenet activés. |
| BitTorrent — client distant Ether-Remix | Client torrent natif et indexeurs BitTorrent. Le dossier partagé, les correspondances de chemins, quotas et limites de téléchargement restent nécessaires. |
| BitTorrent — Seedstrem | Choisir explicitement l’instance déjà installée parmi les addons Stremio. Les liens de lecture restent HTTP et utilisent le client distant de Seedstrem. |

Les sources non configurées sont signalées avec un lien vers leurs paramètres. « Configurée » décrit les réglages présents, pas un contrôle réseau ni une garantie de disponibilité. Aucun compte, indexeur ou droit PostgreSQL n’est créé automatiquement. Pour Usenet, configurez aussi les indexeurs dans Sources → Indexeurs.

## Ordre et secours

Films, épisodes de séries et animés suivent le même ordre. Remix recherche dans la première source, applique les filtres de langue, qualité, identité et compatibilité, puis essaie ses releases utilisables. Les qualités de repli sous les exigences du profil ne remplacent pas une release conforme d’une source suivante. Une absence de résultat, un résultat rejeté, un client indisponible ou un échec de lecture permet de passer à la suivante.

Après l’échec des sources sélectionnées, les autres addons Stremio activés sont consultés, dont Debrider s’il est configuré. Leurs mécanismes habituels de recherche et secours sont réutilisés. Sans source cochée, seuls ces autres addons sont consultés. L’instance choisie comme Seedstrem n’est pas interrogée une deuxième fois en dernier recours.

Le gestionnaire conserve ses caches, budgets, délais et pauses. La validation reste bornée par ses limites existantes à chaque étape ; les résultats déjà examinés ne sont pas retentés pendant la même résolution. Une reprise après échec exclut les releases déjà échouées. Une référence indexée, un choix de version ou une épingle ne passe pas devant l’ordre de la configuration rapide ; ces réglages sont conservés pour le mode avancé.

Les recherches d’indexation et la navigation n’ajoutent aucun téléchargement Seedstrem. Le client torrent natif conserve ses protections et n’ajoute un téléchargement que lors d’une lecture demandée, si son réglage l’autorise. Un téléchargement en attente ne bloque pas les autres sources. Les chemins réseau, MediaFlow, quotas AllDebrid et droits PostgreSQL restent ceux configurés.

Les médias locaux et la musique gardent leurs parcours habituels. La configuration rapide ne propose pas XDCC, FTP ou IPTV comme étapes supplémentaires. Pour ces usages et les préférences avancées, désactivez la configuration rapide.
