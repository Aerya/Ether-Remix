package core

// Ajout Ether-Remix : source XDCC (internal/pse/xdcc).

import (
	"context"
	"strings"
)

// PSEAvailabilityCheck indique la vérification de disponibilité d'un client
// (WithOnDemandStreamsForAvailability). Elle peut ouvrir un lien à la
// demande déjà prêt, mais une source qui télécharge sur disque (XDCC) n'y
// lance jamais de nouveau téléchargement : seule une lecture le fait.
func PSEAvailabilityCheck(ctx context.Context) bool {
	return onDemandStreamsForAvailability(ctx)
}

// LocalNoticeError : message précis d'une source locale de Remix (XDCC) pour
// le spectateur : file d'attente d'un bot, progression, bot hors ligne,
// échec. Err porte la sémantique pour Ether (ErrDownloadQueued et
// ErrPrepareSlow pour une préparation en cours, ErrSourceUnavailable pour un
// échec, ErrDownloadLimit pour un plafond).
type LocalNoticeError struct {
	FR, EN string
	Err    error
}

func (e *LocalNoticeError) Error() string {
	if e.Err == nil {
		return e.EN
	}
	return e.EN + ": " + e.Err.Error()
}

func (e *LocalNoticeError) Unwrap() error { return e.Err }

// Message est le texte affiché au spectateur.
func (e *LocalNoticeError) Message(lang string) string {
	if strings.HasPrefix(strings.ToLower(localLang(lang)), "fr") && e.FR != "" {
		return e.FR
	}
	return e.EN
}
