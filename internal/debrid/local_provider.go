package debrid

// Ether-Remix : fournisseur adossé au client BitTorrent du propriétaire
// (qBittorrent, rTorrent/ruTorrent), en option. Voir internal/pse/torrentclient.
//
// Un tel fournisseur lit l'état de torrents que le client garde de toute
// façon : la vérification en arrière-plan peut l'interroger sans rien
// ajouter, et ses téléchargements restent permis quand « cache uniquement »
// protège les comptes debrid. Il ne retire jamais un torrent (le partage
// continue selon les limites du client).

// LocalDownloader marque un fournisseur adossé à un client BitTorrent local.
// LocalDownloads indique si l'ajout de nouveaux torrents est autorisé.
type LocalDownloader interface {
	Provider
	LocalDownloads() bool
}

// Pending signale un torrent resté sur le client, pas encore complet :
// Ether le suit par son identifiant au lieu de l'ajouter de nouveau.
func Pending(torrentID string, err error) error { return pending(torrentID, err) }

func isLocalProvider(p Provider) bool {
	_, ok := p.(LocalDownloader)
	return ok
}

func localDownloads(p Provider) bool {
	ld, ok := p.(LocalDownloader)
	return ok && ld.LocalDownloads()
}

// LocalDownloads indique si un fournisseur local actif peut télécharger.
func (s *Service) LocalDownloads() bool {
	if s == nil {
		return false
	}
	for _, e := range s.active() {
		if localDownloads(e.p) {
			return true
		}
	}
	return false
}

// LocalDownloads : voir Service.LocalDownloads.
func (x *Indexer) LocalDownloads() bool { return x != nil && x.svc.LocalDownloads() }
