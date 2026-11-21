// Package continuewatch regroupe les réglages de la rangée « Continuer à
// regarder » : séries examinées, période de visionnage, fenêtre des nouveaux
// épisodes et quantités. Ajout Remix : voir docs/pse/FORK.fr.md.
//
// Les valeurs viennent de pseenv (console ou variable d'environnement) et sont
// relues à chaque demande, donc prises en compte sans redémarrage. Une valeur
// absente ou illisible donne la valeur par défaut ; une valeur hors bornes est
// ramenée à la borne (la console, elle, les refuse à l'enregistrement).
package continuewatch

import (
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Clés des réglages (déclarés dans internal/pse/console/fields.go).
const (
	KeyScanLimit = "PSE_CONTINUE_SCAN_LIMIT"
	KeyWatchDays = "PSE_CONTINUE_WATCH_DAYS"
	KeyNewDays   = "PSE_CONTINUE_NEW_DAYS"
	KeyMaxMovies = "PSE_CONTINUE_MAX_MOVIES"
	KeyMaxShows  = "PSE_CONTINUE_MAX_SHOWS"
)

// Valeurs par défaut et bornes.
const (
	DefaultScanLimit = 200
	MinScanLimit     = 20
	MaxScanLimit     = 1000

	DefaultWatchDays = 0 // illimitée
	MaxWatchDays     = 3650

	DefaultNewDays = 60
	MaxNewDays     = 365

	MaxQuantity = 200 // 0 = limite du client
)

// Settings sont les réglages effectifs de la rangée.
type Settings struct {
	// ScanLimit : séries de l'historique examinées (20 à 1000).
	ScanLimit int
	// WatchDays : période du dernier visionnage, en jours (0 = illimitée).
	WatchDays int
	// NewDays : fenêtre des nouveaux épisodes, en jours (0 = désactivée).
	NewDays int
	// MaxMovies, MaxShows : quantités maximales (0 = limite du client).
	MaxMovies int
	MaxShows  int
}

// Defaults renvoie les réglages sans valeur enregistrée.
func Defaults() Settings {
	return Settings{ScanLimit: DefaultScanLimit, WatchDays: DefaultWatchDays, NewDays: DefaultNewDays}
}

// Load lit les réglages effectifs.
func Load() Settings { return From(pseenv.Get) }

// From lit les réglages avec la fonction donnée (tests).
func From(get func(string) string) Settings {
	return Settings{
		ScanLimit: number(get(KeyScanLimit), DefaultScanLimit, MinScanLimit, MaxScanLimit),
		WatchDays: number(get(KeyWatchDays), DefaultWatchDays, 0, MaxWatchDays),
		NewDays:   number(get(KeyNewDays), DefaultNewDays, 0, MaxNewDays),
		MaxMovies: number(get(KeyMaxMovies), 0, 0, MaxQuantity),
		MaxShows:  number(get(KeyMaxShows), 0, 0, MaxQuantity),
	}
}

func number(raw string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}
