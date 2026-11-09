// Package notifyfr traduit en français les notifications qu'Ether envoie
// aux canaux du propriétaire (Discord, Slack, Telegram, Gotify, ntfy,
// webhook, e-mail, résumé hebdomadaire).
//
// Ajout Private Streaming Enhanced. Ether rédige ses notifications en
// anglais à environ vingt endroits ; plutôt que de modifier chacun d'eux (et
// de multiplier les conflits avec upstream), la traduction est appliquée en
// un seul point, juste avant l'envoi (notify.Service.deliver, repère
// PSE-HOOK). Un message inconnu (nouveau texte upstream, événement de
// plugin) est envoyé tel quel, en anglais : rien n'est jamais perdu.
//
// Les journaux et erreurs techniques restent en anglais, y compris le
// détail d'erreur inclus dans certains messages.
//
// Langue : variable PSE_NOTIFY_LANGUAGE, « fr » par défaut ; « en »
// désactive la traduction.
package notifyfr

import (
	"regexp"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// EnvLanguage est la variable d'environnement qui choisit la langue.
const EnvLanguage = "PSE_NOTIFY_LANGUAGE"

// Enabled indique si les notifications doivent être traduites en français.
// Sans variable définie, le français est la valeur par défaut, sauf dans les
// binaires de test Go : les tests d'Ether vérifient ses textes anglais et
// doivent continuer de passer sans modification.
func Enabled() bool {
	v, set := pseenv.Lookup(EnvLanguage)
	if !set && testing.Testing() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "fr", "fr-fr", "fr-ca", "french", "français", "francais":
		return true
	default:
		return false
	}
}

// Localize renvoie le titre, le message et le HTML d'une notification dans
// la langue configurée.
func Localize(kind, title, message, html string) (string, string, string) {
	if !Enabled() {
		return title, message, html
	}
	return French(kind, title, message, html)
}

// French traduit une notification en français, indépendamment de la
// configuration.
func French(kind, title, message, html string) (string, string, string) {
	title = translate(kind, title, titles, titlePatterns)
	message = translate(kind, message, messages, messagePatterns)
	if html != "" && kind == "newsletter.weekly" {
		html = newsletter(html)
	}
	return title, message, html
}

func translate(kind, s string, exact map[string]string, patterns []pattern) string {
	if s == "" {
		return s
	}
	if fr, ok := exact[s]; ok {
		return fr
	}
	for _, p := range patterns {
		if p.kind != "" && p.kind != kind {
			continue
		}
		if p.re.MatchString(s) {
			return p.re.ReplaceAllString(s, p.repl)
		}
	}
	return s
}

type pattern struct {
	kind string // vide : tous les types d'événement
	re   *regexp.Regexp
	repl string
}

func rule(kind, expr, repl string) pattern {
	return pattern{kind: kind, re: regexp.MustCompile(`^` + expr + `$`), repl: repl}
}

// Titres fixes, à l'identique du texte d'Ether.
var titles = map[string]string{
	"Ether is online":             "Ether est en ligne",
	"Ether is stopping":           "Ether s'arrête",
	"Ether stopped unexpectedly":  "Ether s'est arrêté de façon inattendue",
	"Ether notification test":     "Test de notification Ether",
	"Title available":             "Titre disponible",
	"Provider recovered":          "Fournisseur rétabli",
	"Ether cache is full":         "Le cache d'Ether est plein",
	"Cache space available":       "Espace de cache disponible",
	"Scheduled backup failed":     "Échec de la sauvegarde planifiée",
	"Your week on Ether":          "Votre semaine sur Ether",
	"Ether playback start":        "Lecture démarrée sur Ether",
	"Ether playback stop":         "Lecture terminée sur Ether",
	"Ether playback error":        "Échec de lecture sur Ether",
	"Ether playback failover":     "Changement de source sur Ether",
	"Ether playback downfailover": "Changement de source sur Ether",
}

var titlePatterns = []pattern{
	rule("update.available", `Ether (.+) is available`, "Ether $1 est disponible"),
}

// Messages fixes.
var messages = map[string]string{
	"The server is shutting down or restarting.":                                              "Le serveur s'arrête ou redémarre.",
	"Your notification channel is connected. Real alerts will use your selected event rules.": "Votre canal de notification est connecté. Les vraies alertes suivront les règles d'événements choisies.",
	"The indexer is responding again.":                                                        "L'indexeur répond de nouveau.",
	"An indexer operation failed. See server logs for details.":                               "Une opération sur l'indexeur a échoué. Consultez les journaux du serveur pour plus de détails.",
	"The indexer's daily budget has been exhausted.":                                          "Le budget quotidien de l'indexeur est épuisé.",
	"Provider is unavailable or has rejected authentication. See server health for details.":  "Le fournisseur est indisponible ou a refusé l'authentification. Consultez l'état du serveur pour plus de détails.",
	"The configured segment cache budget has been reached.":                                   "Le budget configuré du cache de segments est atteint.",
	"The segment cache is below its configured budget again.":                                 "Le cache de segments est de nouveau sous son budget configuré.",
	"Weekly viewing summary":                                                                  "Résumé hebdomadaire de visionnage",
	"The server process exited without completing a clean shutdown. Check Ether's crash log and service status. This alert was sent by its independent crash watcher.": "Le processus du serveur s'est terminé sans arrêt propre. Consultez le journal de plantage d'Ether et l'état du service. Cette alerte a été envoyée par son surveillant de plantage indépendant.",
}

var messagePatterns = []pattern{
	rule("server.started", `(.+) is ready for connections\.`, "$1 est prêt à accepter les connexions."),
	rule("provider.recovered", `(.+) is available again\.`, "$1 est de nouveau disponible."),
	rule("update.available", `This server runs (.+)\. Open Maintenance → Updates to install (.+)\.`,
		"Ce serveur exécute la version $1. Ouvrez Maintenance → Mises à jour pour installer la version $2."),
	// Le détail d'erreur reste en anglais (erreur technique).
	rule("backup.failed", `(?s)Ether could not take its scheduled backup: (.*)`, "Ether n'a pas pu effectuer sa sauvegarde planifiée : $1"),
}

// Résumé hebdomadaire (gabarit internal/stats newsletterTemplate).
var newsletterPhrases = strings.NewReplacer(
	`<html>`, `<html lang="fr">`,
	`<title>Ether viewing report</title>`, `<title>Rapport de visionnage Ether</title>`,
	`<h1>Your Ether week</h1>`, `<h1>Votre semaine sur Ether</h1>`,
	`<h2>Popular on your server</h2>`, `<h2>Populaire sur votre serveur</h2>`,
	`<p>No recorded plays this week.</p>`, `<p>Aucune lecture enregistrée cette semaine.</p>`,
	`Playback time comes from client timeline updates; gaps and ambiguous simultaneous sessions are excluded.`,
	`La durée de lecture provient des mises à jour de progression envoyées par les clients ; les interruptions et les sessions simultanées ambiguës sont exclues.`,
)

var (
	newsletterTotals = regexp.MustCompile(`<p>(\d+) plays · (\d+) completed · ([0-9.]+) hours of reported playback</p>`)
	newsletterPlays  = regexp.MustCompile(` — (\d+) plays</li>`)
	englishDate      = regexp.MustCompile(`\b(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) (\d{1,2}), (\d{4})\b`)
	frenchMonths     = map[string]string{
		"Jan": "janv.", "Feb": "févr.", "Mar": "mars", "Apr": "avr.", "May": "mai", "Jun": "juin",
		"Jul": "juil.", "Aug": "août", "Sep": "sept.", "Oct": "oct.", "Nov": "nov.", "Dec": "déc.",
	}
)

func newsletter(html string) string {
	html = newsletterPhrases.Replace(html)
	html = newsletterTotals.ReplaceAllStringFunc(html, func(m string) string {
		p := newsletterTotals.FindStringSubmatch(m)
		return "<p>" + p[1] + " lecture" + plural(p[1]) + " · " + p[2] + " terminée" + plural(p[2]) +
			" · " + strings.ReplaceAll(p[3], ".", ",") + " h de lecture signalée</p>"
	})
	html = newsletterPlays.ReplaceAllStringFunc(html, func(m string) string {
		n := newsletterPlays.FindStringSubmatch(m)[1]
		return " — " + n + " lecture" + plural(n) + "</li>"
	})
	return englishDate.ReplaceAllStringFunc(html, func(m string) string {
		p := englishDate.FindStringSubmatch(m)
		return p[2] + " " + frenchMonths[p[1]] + " " + p[3]
	})
}

// plural : règle française, 0 et 1 au singulier.
func plural(n string) string {
	if n == "0" || n == "1" {
		return ""
	}
	return "s"
}
