package console

import "strconv"

// Réglages du client torrent (Ether-Remix, PSE_TORRENT) : le client
// principal et deux clients supplémentaires.

func torrentMainFields() []Field {
	out := []Field{
		{Key: "PSE_TORRENT", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
			Help: tx("Désactivé par défaut. Les fichiers téléchargés occupent le disque du client.", "Off by default. Downloaded files use the client's disk.")},
	}
	out = append(out, torrentClientFields(1)...)
	return append(out,
		Field{Key: "PSE_TORRENT_DOWNLOAD", Kind: KindToggle, Default: "on", Label: tx("Télécharger ce qui manque", "Download what is missing"),
			Help: tx("Désactivé : Remix lit seulement les torrents déjà complets sur les clients.", "Off: Remix only plays torrents already complete on the clients.")},
		Field{Key: "PSE_TORRENT_MUSIC", Kind: KindToggle, Default: "on", Label: tx("Musique", "Music"),
			Help: tx("Un album à la fois ; il est lisible une fois téléchargé.", "One album at a time; it plays once downloaded.")},
		Field{Key: "PSE_TORRENT_TRACKERS", Kind: KindFilter, Default: "", Placeholder: "c411=2/72h; exemple.org=1", Label: tx("Limites par tracker", "Per-tracker limits"),
			Help: tx("tracker=ratio/durée, séparés par « ; ». Le nom est une partie de l'adresse d'annonce. Prime sur les limites du client ; ratio ou durée facultatif (ex. c411=/168h).",
				"tracker=ratio/time, separated by “;”. The name is part of the announce address. Overrides the client's limits; ratio or time optional (e.g. c411=/168h).")},
		Field{Key: "PSE_TORRENT_MAX_DAY", Kind: KindNumber, Default: "10", Min: ptr(1), Max: ptr(200), Label: tx("Ajouts par jour", "Additions per day"),
			Help: tx("Tous clients confondus. Protège vos comptes trackers.", "All clients together. Protects your tracker accounts.")},
		Field{Key: "PSE_TORRENT_MIN_FREE", Kind: KindNumber, Default: "20", Min: ptr(0), Max: ptr(100000), Label: tx("Place libre minimum (Go)", "Minimum free space (GB)"),
			Help: tx("Sous ce seuil, Remix n'envoie plus rien à ce client et vous alerte par les notifications d'Ether (« Fournisseur indisponible »). 0 : aucun contrôle.",
				"Below this, Remix sends nothing more to that client and alerts you through Ether notifications (“Provider unavailable”). 0: no check.")},
		Field{Key: "PSE_TORRENT_XSEED_URL", Kind: KindURL, Default: "", Placeholder: "http://cross-seed:2468", Label: tx("cross-seed", "cross-seed"),
			Help: tx("En option : prévenu à la fin de chaque téléchargement de Remix (3 au plus toutes les 5 minutes).", "Optional: notified when each Remix download completes (at most 3 every 5 minutes).")},
		Field{Key: "PSE_TORRENT_XSEED_KEY", Kind: KindKey, Default: "", Label: tx("Clé d'API cross-seed", "cross-seed API key")},
		Field{Key: "PSE_TORRENT_CATEGORY", Advanced: true, Kind: KindFilter, Default: "remix", Placeholder: "remix", Label: tx("Catégorie", "Category"),
			Help: tx("Catégorie qBittorrent ou libellé ruTorrent des torrents de Remix, sur tous les clients.", "qBittorrent category or ruTorrent label of Remix torrents, on every client.")},
	)
}

// torrentClientFields sont les réglages d'un client : le principal
// (PSE_TORRENT_…) ou un client supplémentaire n (PSE_TORRENT2_…).
func torrentClientFields(n int) []Field {
	p := "PSE_TORRENT_"
	if n > 1 {
		p = "PSE_TORRENT" + strconv.Itoa(n) + "_"
	}
	return []Field{
		{Key: p + "CLIENT", Kind: KindSelect, Default: "qbittorrent", Options: []Option{{"qbittorrent", tx("qBittorrent", "qBittorrent")}, {"rtorrent", tx("rTorrent / ruTorrent", "rTorrent / ruTorrent")}},
			Label: tx("Client", "Client")},
		{Key: p + "URL", Kind: KindURL, Default: "", Placeholder: "http://utilisateur:motdepasse@qbittorrent:8080", Label: tx("Adresse du client", "Client address"),
			Help: tx("qBittorrent : adresse de l'interface web, avec identifiants ou avec la clé d'API ci-dessous. rTorrent : adresse XML-RPC (…/RPC2, ou ruTorrent …/plugins/rpc/rpc.php), identifiants dans l'adresse. Le mot de passe n'est jamais réaffiché.",
				"qBittorrent: web UI address, with credentials or with the API key below. rTorrent: XML-RPC address (…/RPC2, or ruTorrent …/plugins/rpc/rpc.php), credentials in the address. The password is never shown again.")},
		{Key: p + "KEY", Kind: KindKey, Default: "", Label: tx("Clé d'API qBittorrent", "qBittorrent API key"),
			Help: tx("qBittorrent 5.2 et suivants (Options › Interface web › Clé d'API) : remplace les identifiants. rTorrent n'en a pas.", "qBittorrent 5.2 and later (Options › Web UI › API key): replaces the credentials. rTorrent has none.")},
		{Key: p + "RATIO", Kind: KindFilter, Default: "", Placeholder: "1", Label: tx("Ratio", "Ratio"),
			Help: tx("Vide : réglage du client. Le partage s'arrête à la première limite atteinte (ratio ou durée), sans rien supprimer.", "Empty: the client's setting. Seeding stops at the first limit reached (ratio or time), deleting nothing.")},
		{Key: p + "SEED_TIME", Kind: KindFilter, Default: "", Placeholder: "72h", Label: tx("Durée de partage", "Seeding time"),
			Help: tx("Vide : réglage du client. Ex. 72h ou 168h.", "Empty: the client's setting. E.g. 72h or 168h.")},
		{Key: p + "SAVE_PATH", Advanced: true, Kind: KindFilter, Default: "", Placeholder: "/downloads/remix", Label: tx("Dossier de téléchargement", "Download folder"),
			Help: tx("Chemin vu par le client. Vide : celui du client. Sert aussi à mesurer la place libre.", "Path as seen by the client. Empty: the client's own. Also used to measure free space.")},
		{Key: p + "PATH_MAP", Advanced: true, Kind: KindFilter, Default: "", Placeholder: "/downloads=/data/torrents", Label: tx("Correspondance des dossiers", "Folder mapping"),
			Help: tx("Quand Remix ne voit pas les fichiers au même chemin que le client : dossier client=dossier Remix, séparés par « ; ».",
				"When Remix does not see files at the client's path: client folder=Remix folder, separated by “;”.")},
	}
}

// torrentExtraClient est le bloc d'un client supplémentaire.
func torrentExtraClient(n int) Feature {
	num := strconv.Itoa(n)
	return Feature{
		ID:    "torrent" + num,
		Title: tx("Client torrent "+num, "Torrent client "+num),
		Description: tx("Client supplémentaire, en option : Remix y cherche aussi les torrents complets et lui envoie un téléchargement quand les clients précédents manquent de place ou le refusent. Mêmes règles de dossier partagé.",
			"Optional extra client: Remix also looks for complete torrents there and sends it a download when the previous clients are out of space or refuse it. Same shared-folder rules."),
		Fields: append([]Field{{Key: "PSE_TORRENT" + num, Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
			Help: tx("Nécessite le client torrent principal activé.", "Needs the main torrent client enabled.")}}, torrentClientFields(n)...),
	}
}
