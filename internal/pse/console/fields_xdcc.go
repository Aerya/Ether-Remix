package console

import (
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/xdcc"
)

// Source XDCC (Ether-Remix, PSE_XDCC) : passe par XDCC Grabscher, un
// service externe (voir docs/pse/XDCC.fr.md).

func xdccFeature() Feature {
	return Feature{
		ID:    "xdcc",
		Title: tx("XDCC (via XDCC Grabscher)", "XDCC (via XDCC Grabscher)"),
		Description: tx("Cherche les films et épisodes proposés par les bots XDCC des réseaux IRC suivis par votre Grabscher, puis les télécharge à la demande. Grabscher tourne à part (même machine ou ailleurs, jamais exposé à Internet) ; Remix doit voir ses dossiers. Désactivé par défaut : contrairement au debrid, les fichiers sont écrits sur votre disque.",
			"Searches movies and episodes offered by XDCC bots on the IRC networks your Grabscher follows, then downloads them on demand. Grabscher runs separately (same machine or elsewhere, never exposed to the Internet); Remix must see its folders. Off by default: unlike debrid, files are written to your disk."),
		Fields: []Field{
			{Key: "PSE_XDCC", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("Nécessite l'adresse et la clé d'API de Grabscher.", "Needs Grabscher's address and API key.")},
			{Key: "PSE_XDCC_URL", Kind: KindURL, Default: "", Placeholder: "http://xdcc-grabscher:5556", Label: tx("Adresse de Grabscher", "Grabscher address"),
				Help: tx("Adresse du réseau interne (Docker ou LAN), sans identifiants. Ne publiez jamais le port 5556 sur Internet.", "Internal network address (Docker or LAN), without credentials. Never publish port 5556 to the Internet.")},
			{Key: "PSE_XDCC_KEY", Kind: KindKey, Default: "", Label: tx("Clé d'API de Grabscher", "Grabscher API key"),
				Help: tx("Créée dans Grabscher (engrenage › Api Keys), puis activée : une nouvelle clé y est désactivée. Jamais réaffichée, jamais envoyée dans une adresse.", "Created in Grabscher (gear › Api Keys), then enabled: new keys start disabled there. Never shown again, never sent in an address.")},
			{Key: "PSE_XDCC_MODE", Kind: KindSelect, Default: xdcc.ModeStream, Label: tx("Mode", "Mode"),
				Options: []Option{
					{xdcc.ModeStream, tx("Streaming (fichier temporaire)", "Streaming (temporary file)")},
					{xdcc.ModeBoth, tx("Streaming et fichier gardé", "Streaming and kept file")},
					{xdcc.ModeDownload, tx("Téléchargement seulement", "Download only")},
				},
				Help: tx("Streaming : la lecture commence pendant le transfert, le fichier est effacé après la durée de conservation. Streaming et fichier gardé : idem, sans effacement. Téléchargement seulement : lecture une fois le fichier complet. Le streaming exige que Remix voie le dossier temporaire de Grabscher ; sinon la lecture attend la fin du transfert.",
					"Streaming: playback starts during the transfer, the file is deleted after the retention time. Streaming and kept file: same, never deleted. Download only: playback once the file is complete. Streaming needs Remix to see Grabscher's temporary folder; otherwise playback waits for the transfer to finish.")},
			{Key: "PSE_XDCC_MAX_ACTIVE", Kind: KindNumber, Default: "2", Min: ptr(1), Max: ptr(20), Label: tx("Téléchargements simultanés", "Simultaneous downloads"),
				Help: tx("Les bots limitent eux-mêmes leurs envois ; au-delà, une lecture explique qu'il faut attendre.", "Bots limit their own sends; beyond this, playback explains that it must wait.")},
			{Key: "PSE_XDCC_CACHE_GB", Kind: KindNumber, Default: "50", Min: ptr(0), Max: ptr(100000), Label: tx("Taille du cache (Go)", "Cache size (GB)"),
				Help: tx("Fichiers temporaires du mode Streaming. Au-delà, les plus anciens non lus sont effacés. 0 : pas de plafond.", "Temporary files of Streaming mode. Beyond this, the oldest unread ones are deleted. 0: no cap.")},
			{Key: "PSE_XDCC_CACHE_HOURS", Kind: KindNumber, Default: "48", Min: ptr(1), Max: ptr(24 * 365), Label: tx("Conservation du cache (heures)", "Cache retention (hours)"),
				Help: tx("Depuis la dernière lecture. Un fichier gardé, en cours de lecture ou rangé dans une bibliothèque locale n'est jamais effacé.", "Since the last playback. A kept file, one being played, or one inside a local library is never deleted.")},
			{Key: "PSE_XDCC_MIN_FREE", Kind: KindNumber, Default: "20", Min: ptr(0), Max: ptr(100000), Label: tx("Place libre minimum (Go)", "Minimum free space (GB)"),
				Help: tx("Mesurée sur le dossier de téléchargement. Sous ce seuil, aucun téléchargement n'est lancé. 0 : aucun contrôle.", "Measured on the download folder. Below this, no download starts. 0: no check.")},
			{Key: "PSE_XDCC_MAX_DAY", Advanced: true, Kind: KindNumber, Default: "20", Min: ptr(1), Max: ptr(500), Label: tx("Lancements par jour", "Starts per day"),
				Help: tx("Lectures seulement ; un lancement manuel depuis la console n'est pas compté.", "Playback only; a manual start from the console is not counted.")},
			{Key: "PSE_XDCC_BUFFER_MB", Advanced: true, Kind: KindNumber, Default: "8", Min: ptr(1), Max: ptr(512), Label: tx("Tampon avant lecture (Mo)", "Buffer before playback (MB)"),
				Help: tx("Octets reçus du bot avant d'ouvrir la lecture.", "Bytes received from the bot before playback opens.")},
			{Key: "PSE_XDCC_WAIT", Advanced: true, Kind: KindDuration, Default: "10s", Placeholder: "10s", Label: tx("Attente maximale d'un bot lent", "Maximum wait for a slow bot"),
				Help: tx("De 2s à 12s. Une demande d'octets pas encore reçus n'attend que s'ils arrivent dans ce délai ; sinon le lecteur est prévenu tout de suite.", "From 2s to 12s. A request for bytes not received yet only waits if they arrive within this time; otherwise the player is told at once.")},
			{Key: "PSE_XDCC_PATH_MAP", Advanced: true, Kind: KindFilter, Default: "", Placeholder: "/config/.config/XG=/xdcc", Label: tx("Correspondance des dossiers", "Folder mapping"),
				Help: tx("Quand Remix ne voit pas les fichiers au même chemin que Grabscher : dossier Grabscher=dossier Remix, séparés par « ; ».", "When Remix does not see files at Grabscher's path: Grabscher folder=Remix folder, separated by “;”.")},
			{Key: "PSE_XDCC_TEMP_DIR", Advanced: true, Kind: KindFilter, Default: "", Placeholder: "/xdcc/tmp", Label: tx("Dossier temporaire (vu par Remix)", "Temporary folder (as seen by Remix)"),
				Help: tx("Vide : le dossier « tmp » voisin du dossier de téléchargement de Grabscher.", "Empty: the “tmp” folder next to Grabscher's download folder.")},
			{Key: "PSE_XDCC_MAX_RESULTS", Advanced: true, Kind: KindNumber, Default: "50", Min: ptr(5), Max: ptr(200), Label: tx("Résultats par recherche", "Results per search"),
				Help: tx("Paquets lus par requête, avant le tri d'Ether.", "Packets read per query, before Ether's ranking.")},
			{Key: "PSE_XDCC_SEARCH_DAILY", Advanced: true, Kind: KindNumber, Default: "3000", Min: ptr(50), Max: ptr(100000), Label: tx("Recherches par jour", "Searches per day"),
				Help: tx("Plafond de la source dans le gestionnaire d'indexeurs d'Ether (lectures et indexation).", "The source's cap in Ether's indexer manager (playback and indexing).")},
			{Key: "PSE_XDCC_BOTS_EXCLUDE", Advanced: true, Kind: KindFilter, Default: "", Placeholder: "BotLent,AutreBot", Label: tx("Bots ignorés", "Ignored bots"),
				Help: tx("Noms séparés par des virgules.", "Comma-separated names.")},
		},
	}
}

// validateXDCC vérifie les réglages XDCC particuliers ; handled est faux
// pour les autres clés.
func validateXDCC(key, v string) (out string, handled bool, err error) {
	switch key {
	case "PSE_XDCC_URL":
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v != "" {
			if err := xdcc.ValidateURL(v); err != nil {
				return "", true, err
			}
		}
		return v, true, nil
	case "PSE_XDCC_PATH_MAP":
		if _, err := xdcc.ParsePathMap(v); err != nil {
			return "", true, err
		}
		return v, true, nil
	case "PSE_XDCC_TEMP_DIR":
		if v != "" && !strings.HasPrefix(v, "/") {
			return "", true, errors.New("chemin absolu attendu")
		}
		return v, true, nil
	case "PSE_XDCC_WAIT":
		d, err := time.ParseDuration(v)
		if err != nil || d < 2*time.Second || d > 12*time.Second {
			return "", true, errors.New("durée de 2s à 12s attendue")
		}
		return d.String(), true, nil
	case "PSE_XDCC_BOTS_EXCLUDE":
		if len(v) > 2000 || strings.ContainsAny(v, "\n\r\t") {
			return "", true, errors.New("noms séparés par des virgules attendus")
		}
		return v, true, nil
	}
	return "", false, nil
}
