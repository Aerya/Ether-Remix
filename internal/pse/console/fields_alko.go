package console

import (
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/alko"
)

// Source Alkodiques par l'API de StreamFusion (Ether-Remix, PSE_ALKO) : voir
// docs/pse/ALKODIQUES.fr.md.

func alkoFeature() Feature {
	return Feature{
		ID:    "alko",
		Title: tx("Alkodiques (API StreamFusion)", "Alkodiques (StreamFusion API)"),
		Description: tx("Lit les fichiers AllDebrid vérifiés (Alkodiques) de votre StreamFusion Reborn par son API en lecture seule, sans accès à PostgreSQL. Remix en garde une copie locale, tenue à jour par le journal des modifications, et la publie pour les titres de votre catalogue, tous les épisodes compris. Un seul déblocage de lien par lecture. Désactivé par défaut.",
			"Reads the verified AllDebrid files (Alkodiques) of your StreamFusion Reborn through its read-only API, with no PostgreSQL access. Remix keeps a local copy, updated from the change journal, and publishes it for the titles in your catalogue, every episode included. One link unlock per playback. Off by default."),
		Fields: []Field{
			{Key: "PSE_ALKO", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("Nécessite la clé de lecture affichée par StreamFusion (page /admin/alko-lecture). Le premier passage lit tout le catalogue, par pages ; il peut durer plusieurs minutes. Quand la source est active, la lecture PostgreSQL des Alkodiques est ignorée.",
					"Needs the read key shown by StreamFusion (page /admin/alko-lecture). The first pass reads the whole catalogue, page by page; it can take several minutes. When the source is active, the PostgreSQL reading of Alkodiques is ignored.")},
			{Key: "PSE_ALKO_URL", Kind: KindURL, Default: alko.DefaultURL, Placeholder: alko.DefaultURL, Label: tx("Adresse de l'API", "API address"),
				Help: tx("Adresse interne conseillée par StreamFusion (réseau Docker commun), sans identifiants ni chemin.", "Internal address recommended by StreamFusion (shared Docker network), with no credentials or path.")},
			{Key: "PSE_ALKO_KEY", Kind: KindKey, Default: "", Label: tx("Clé de lecture", "Read key"),
				Help: tx("Affichée une fois dans l'administration de StreamFusion. Elle change si SECRET_API_KEY change. Jamais réaffichée ni écrite dans les journaux.", "Shown once in StreamFusion's administration. It changes when SECRET_API_KEY changes. Never shown again or written to logs.")},
			{Key: "PSE_ALKO_LANG_FR", Kind: KindToggle, Default: "on", Label: tx("Français, MULTi et VOSTFR seulement", "French, MULTi and VOSTFR only"),
				Help: tx("Filtre sur le nom du fichier (VF, MULTi, VOSTFR, sous-titres français). Désactivé : tous les fichiers des titres du catalogue sont proposés.", "Filters on the file name (French, MULTi, VOSTFR, French subtitles). Off: every file of the catalogue's titles is offered.")},
			{Key: "PSE_ALKO_INTERVAL", Advanced: true, Kind: KindDuration, Default: "10m", Placeholder: "10m", Label: tx("Intervalle", "Interval"),
				Help: tx("Entre deux synchronisations. Minimum 2m.", "Between two syncs. At least 2m.")},
		},
	}
}

// validateAlko vérifie les réglages Alkodiques particuliers ; handled est faux
// pour les autres clés.
func validateAlko(key, v string) (out string, handled bool, err error) {
	switch key {
	case "PSE_ALKO_URL":
		if v == "" {
			return "", true, nil
		}
		u, err := alko.ValidateURL(v)
		return u, true, err
	case "PSE_ALKO_INTERVAL":
		d, err := time.ParseDuration(v)
		if err != nil || d < alko.MinEvery {
			return "", true, errors.New("durée attendue, ex. 10m (minimum 2m)")
		}
		return d.String(), true, nil
	}
	return "", false, nil
}
