package core

// Ajout Ether-Remix : distinguer la pause imposée par un indexeur (limite de
// requêtes, Retry-After) du budget journalier réglé dans Ether.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrIndexerThrottled : un indexeur refuse les requêtes d'Ether et impose
// une pause. Distinct du disjoncteur local (newznab.ErrIndexerPaused).
var ErrIndexerThrottled = errors.New("indexer throttled by service")

// IndexerThrottledError décrit une pause imposée par le service. Err contient
// toujours ErrBudgetExhausted (et l'erreur locale quand le refus vient d'Ether),
// donc les appelants qui y voient une erreur passagère continuent de le faire.
type IndexerThrottledError struct {
	Indexers       []string  // indexeurs en pause
	RetryAt        time.Time // reprise la plus proche, zéro si inconnue
	OthersAnswered bool      // un autre indexeur a répondu sans erreur
	DailyLimited   []string  // indexeurs dont la limite journalière d'Ether est atteinte
	Err            error
}

func (e *IndexerThrottledError) Error() string {
	return IndexerThrottleMessage(e, time.Now()) + errDetail(e.Err)
}

var (
	urlInText    = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)
	secretInText = regexp.MustCompile(`(?i)\b(apikey|api_key|token|key|password|passkey)=[^&\s]+`)
)

// errDetail garde le détail d'origine pour les journaux et l'historique,
// sans URL (elle peut porter une clé) et en version courte.
func errDetail(err error) string {
	if err == nil {
		return ""
	}
	d := urlInText.ReplaceAllString(err.Error(), "<url>")
	d = secretInText.ReplaceAllString(d, "$1=***")
	if r := []rune(d); len(r) > 200 {
		d = string(r[:200]) + "…"
	}
	return " (" + d + ")"
}

func (e *IndexerThrottledError) Unwrap() []error {
	return []error{ErrIndexerThrottled, e.Err}
}

// IndexerDailyLimitError : la limite journalière réglée dans Ether est atteinte.
// Ce n'est pas une pause du service : aucune reprise annoncée.
type IndexerDailyLimitError struct {
	Indexers []string
	Err      error
}

func (e *IndexerDailyLimitError) Error() string {
	return IndexerDailyLimitMessage(e) + errDetail(e.Err)
}

func (e *IndexerDailyLimitError) Unwrap() error { return e.Err }

func indexerNames(names []string) string {
	switch len(names) {
	case 0:
		return "Un indexeur"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " et " + names[len(names)-1]
}

// IndexerThrottleMessage est le texte français d'une pause imposée par le service.
func IndexerThrottleMessage(e *IndexerThrottledError, now time.Time) string {
	verb := "limite"
	if len(e.Indexers) > 1 {
		verb = "limitent"
	}
	msg := fmt.Sprintf("%s %s les requêtes d'Ether : ", indexerNames(e.Indexers), verb)
	if e.RetryAt.IsZero() {
		msg += "nouvelle tentative automatique plus tard."
	} else {
		msg += fmt.Sprintf("reprise dans environ %s (%s).", throttleDelay(e.RetryAt.Sub(now)), e.RetryAt.In(time.Local).Format("15:04"))
	}
	if e.OthersAnswered {
		msg += " Aucune autre source n'a de release pour ce titre."
	}
	if len(e.DailyLimited) > 0 {
		msg += " " + IndexerDailyLimitMessage(&IndexerDailyLimitError{Indexers: e.DailyLimited})
	}
	return msg
}

// IndexerDailyLimitMessage est le texte français d'un budget journalier atteint.
func IndexerDailyLimitMessage(e *IndexerDailyLimitError) string {
	return "Limite journalière de requêtes réglée dans Ether atteinte pour " + indexerNames(e.Indexers) + "."
}

// throttleDelay arrondit à la minute (au moins 1 min), en heures au-delà de 60 min.
func throttleDelay(d time.Duration) string {
	m := max(1, int((d+30*time.Second)/time.Minute))
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d h", m/60)
	}
	return fmt.Sprintf("%d h %02d", m/60, m%60)
}

// indexerThrottleMessage sert LocalDownloadMessage ("" pour une autre erreur).
func indexerThrottleMessage(err error) string {
	var t *IndexerThrottledError
	if errors.As(err, &t) {
		return IndexerThrottleMessage(t, time.Now())
	}
	var d *IndexerDailyLimitError
	if errors.As(err, &d) {
		return IndexerDailyLimitMessage(d)
	}
	return ""
}
