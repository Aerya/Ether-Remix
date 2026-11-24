package console

import (
	"errors"
	"github.com/braedonsaunders/ether/internal/pse/peerclient"
	"path/filepath"
	"strings"
)

func peerFeature(kind string) Feature {
	p := "PSE_" + strings.ToUpper(kind)
	name := "slskd (Soulseek)"
	address := "http://slskd:5030"
	secret := tx("Clé API slskd (lecture/écriture)", "slskd API key (read/write)")
	if kind == "amule" {
		name = "aMule (amuleapi)"
		address = "http://amule:4713"
		secret = tx("Mot de passe administrateur amuleapi", "amuleapi administrator password")
	}
	fields := []Field{
		{Key: p, Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable")},
		{Key: p + "_URL", Kind: KindURL, Placeholder: address, Label: tx("Adresse du service", "Service address"), Help: tx("Adresse de base, sans /api, identifiants ni paramètres. aMule nécessite amuleapi ; slskd nécessite l’API des lots de téléchargements.", "Base address without /api, credentials or query parameters. aMule requires amuleapi; slskd requires the download batches API.")},
		{Key: p + "_KEY", Kind: KindSecret, Label: secret},
		{Key: p + "_ROOT", Kind: KindFilter, Placeholder: "/downloads/" + kind, Label: tx("Dossier terminé, vu par Remix", "Completed downloads directory, as seen by Remix"), Help: tx("Montez ce dossier dans Remix. Les fichiers restent sur disque ; aucune suppression automatique.", "Mount this directory in Remix. Files remain on disk; no automatic deletion.")},
	}
	if kind == "amule" {
		fields = append(fields, Field{Key: p + "_REMOTE_ROOT", Kind: KindFilter, Placeholder: "/incoming", Label: tx("Même dossier, vu par aMule", "Same directory, as seen by aMule"), Help: tx("Vide : même chemin que dans Remix.", "Empty: same path as in Remix.")})
	}
	return Feature{ID: kind, Title: tx(name, name), Description: tx("Recherche de films, épisodes et musique ; téléchargement au lancement de la lecture ou sur demande manuelle. Lecture après réception complète. Deux transferts simultanés et vingt lancements sur 24 heures par connecteur. Les recherches et vérifications en arrière-plan ne téléchargent rien.", "Search movies, episodes and music; download on playback or manual request. Playback after completion. Two simultaneous transfers and twenty starts per 24 hours per connector. Background searches and checks never download files."), Fields: fields}
}
func validatePeer(key, v string) (string, bool, error) {
	if !strings.HasPrefix(key, "PSE_AMULE") && !strings.HasPrefix(key, "PSE_SLSKD") {
		return "", false, nil
	}
	if strings.HasSuffix(key, "_URL") {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v != "" {
			return v, true, peerclient.ValidateURL(v)
		}
		return v, true, nil
	}
	if strings.HasSuffix(key, "_ROOT") {
		v = strings.TrimSpace(v)
		if v != "" && !filepath.IsAbs(v) {
			return "", true, errors.New("chemin absolu attendu")
		}
		return v, true, nil
	}
	return "", false, nil
}
