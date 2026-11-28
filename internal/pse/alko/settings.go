package alko

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// DefaultURL est l'adresse interne conseillée par SFR (page /admin/alko-lecture).
const DefaultURL = releasedb.AlkoDefaultURL

const (
	defaultEvery = 10 * time.Minute
	// MinEvery est l'intervalle minimal entre deux synchronisations.
	MinEvery = 2 * time.Minute
)

// Settings regroupe les réglages PSE_ALKO_*.
type Settings struct {
	Enabled bool
	URL     string        // PSE_ALKO_URL : jamais d'identifiants, de paramètres ni de fragment
	Key     string        // PSE_ALKO_KEY : jamais affichée, jamais journalisée
	LangFR  bool          // PSE_ALKO_LANG_FR : FR / MULTi / VOSTFR seulement
	Every   time.Duration // PSE_ALKO_INTERVAL
}

// LoadSettings lit les réglages effectifs (console, sinon environnement). La
// source reste inactive tant que la clé manque ou que l'adresse est invalide.
func LoadSettings() Settings {
	s := Settings{
		URL:    strings.TrimRight(strings.TrimSpace(pseenv.Get("PSE_ALKO_URL")), "/"),
		Key:    strings.TrimSpace(pseenv.Get("PSE_ALKO_KEY")),
		LangFR: true,
		Every:  defaultEvery,
	}
	if s.URL == "" {
		s.URL = DefaultURL
	}
	if v, ok := pseenv.Lookup("PSE_ALKO_LANG_FR"); ok {
		s.LangFR = !isOff(v)
	}
	if d, err := time.ParseDuration(strings.TrimSpace(pseenv.Get("PSE_ALKO_INTERVAL"))); err == nil {
		s.Every = d
	}
	if s.Every < MinEvery {
		s.Every = MinEvery
	}
	s.Enabled = releasedb.AlkoAPIEnabled() // activée, adresse valide, clé présente : définition unique
	return s
}

func isOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no", "non":
		return true
	}
	return false
}

// ValidateURL vérifie l'adresse de l'API (définition unique : releasedb).
func ValidateURL(raw string) (string, error) { return releasedb.AlkoValidateURL(raw) }

// Problem explique ce qui manque pour activer la source ("" : complet).
func (s Settings) Problem() string {
	if _, err := ValidateURL(s.URL); err != nil {
		return "adresse de l'API invalide"
	}
	if s.Key == "" {
		return "clé de lecture manquante (page /admin/alko-lecture de StreamFusion)"
	}
	return ""
}

// Identity résume ce qui change les résultats ; aucun secret n'y entre, même
// haché.
func (s Settings) Identity() string {
	h := sha256.Sum256([]byte(s.URL + "\x00" + strconv.FormatBool(s.LangFR)))
	return hex.EncodeToString(h[:8])
}

// sourceSignature identifie le serveur lu : un autre serveur impose de repartir
// de zéro. La clé n'y entre pas.
func (s Settings) sourceSignature() string {
	h := sha256.Sum256([]byte(strings.ToLower(s.URL)))
	return hex.EncodeToString(h[:8])
}

// publishSignature résume ce qui décide de la publication d'un fichier : un
// changement fait réévaluer les fichiers déjà jugés.
func (s Settings) publishSignature() string {
	return "v1|lang=" + strconv.FormatBool(s.LangFR)
}
