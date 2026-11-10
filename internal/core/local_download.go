package core

// Ajout Ether-Remix : client BitTorrent local en option (PSE_TORRENT).

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// LocalDownloadError : la release se télécharge sur le client BitTorrent du
// propriétaire et sera lisible une fois complète. Elle enveloppe
// ErrDownloadQueued, donc ErrNotCached : pour Ether, c'est un téléchargement
// en file comme sur un compte debrid, avec un message plus précis.
type LocalDownloadError struct {
	Client   string        // nom affiché du client, ex. « qBittorrent »
	Progress float64       // 0 à 1
	ETA      time.Duration // 0 : inconnu
	Started  bool          // envoyé au client à l'instant
	Lang     string        // langue par défaut des messages (fr, en)
}

func (e *LocalDownloadError) Error() string {
	return fmt.Sprintf("downloading on the torrent client (%d%%): %v", e.Percent(), ErrDownloadQueued)
}

func (e *LocalDownloadError) Unwrap() error { return ErrDownloadQueued }

// Percent arrondit l'avancement, sans annoncer 100 % avant la fin.
func (e *LocalDownloadError) Percent() int {
	p := int(e.Progress * 100)
	return min(max(p, 0), 99)
}

// LocalMessageLang est la langue des messages quand ni l'appelant ni
// l'erreur n'en donnent (réglée par l'application : fr ou en).
var LocalMessageLang atomic.Value

func localLang(lang string) string {
	if lang != "" {
		return lang
	}
	if v, ok := LocalMessageLang.Load().(string); ok && v != "" {
		return v
	}
	return "fr"
}

// Message est le texte affiché au spectateur ; lang vide : e.Lang.
func (e *LocalDownloadError) Message(lang string) string {
	if lang == "" {
		lang = e.Lang
	}
	lang = localLang(lang)
	fr := strings.HasPrefix(strings.ToLower(lang), "fr")
	if e.Started || e.Progress <= 0 {
		if fr {
			return "Envoyé au client torrent : le téléchargement commence. Relancez la lecture une fois terminé."
		}
		return "Sent to the torrent client: the download is starting. Play again once it finishes."
	}
	eta := ""
	if e.ETA > 0 {
		eta = roundETA(e.ETA)
	}
	if fr {
		if eta != "" {
			eta = ", encore " + eta + " environ"
		}
		return fmt.Sprintf("Téléchargement en cours sur le client torrent : %d %%%s. Relancez la lecture une fois terminé.", e.Percent(), eta)
	}
	if eta != "" {
		eta = ", about " + eta + " left"
	}
	return fmt.Sprintf("Downloading on the torrent client: %d%%%s. Play again once it finishes.", e.Percent(), eta)
}

func roundETA(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
	default:
		return fmt.Sprintf("%d h %02d", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
}

// ErrLocalNoSpace : le client torrent n'a plus assez de place libre ; Remix
// ne lui envoie plus rien tant que la place n'est pas revenue.
var ErrLocalNoSpace = fmt.Errorf("not enough free space on the torrent client: %w", ErrDownloadRefused)

// LocalDownloadMessage renvoie le message d'un téléchargement sur le client
// torrent local, ou "" pour une autre erreur.
func LocalDownloadMessage(err error, lang string) string {
	var lde *LocalDownloadError
	if errors.As(err, &lde) {
		return lde.Message(lang)
	}
	if errors.Is(err, ErrLocalNoSpace) {
		if strings.HasPrefix(strings.ToLower(localLang(lang)), "fr") {
			return "Plus assez de place sur le client torrent : le téléchargement n'a pas été lancé."
		}
		return "Not enough free space on the torrent client: the download was not started."
	}
	return ""
}
